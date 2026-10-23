package sandbox

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func (r KubeRunRequest) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultKubeWait
}

// KubeRun starts a Kubernetes run asynchronously and returns it queued.
// Only one run per workflow may be active.
func (m *Manager) KubeRun(ctx context.Context, req KubeRunRequest) (*model.Run, error) {
	if req.Workflow == nil {
		return nil, errors.New("run: workflow is required")
	}
	wfID := req.Workflow.ID
	if err := checkID(wfID); err != nil {
		return nil, err
	}
	if !model.KubeAction(req.Action) {
		return nil, invalidKube("unknown Kubernetes action %q", req.Action)
	}
	if req.Workload != "" && req.Action != model.ActionRestart {
		return nil, invalidKube("workload applies to restart only")
	}
	if req.Workload != "" {
		kind, _, _, ok := model.ParseKubeNodeID(req.Workload)
		if !ok || !restartable(kind) {
			return nil, invalidKube("workload %q must be the node ID of a Deployment, StatefulSet or DaemonSet", req.Workload)
		}
	}
	if err := m.DockerAvailable(ctx); err != nil {
		return nil, ErrDockerUnavailable
	}
	m.ensureHistory(wfID)
	sb := m.sandbox(wfID)
	rs := &runState{
		run: model.Run{
			ID: newRunID(), WorkflowID: wfID, Action: req.Action, Workload: req.Workload,
			RemoveVolumes: req.Action == model.ActionDown && req.RemoveVolumes,
			DeleteCluster: req.Action == model.ActionDown && req.DeleteCluster,
			Status:        model.RunQueued, CreatedAt: time.Now().UTC(), Log: []model.LogLine{},
		},
		sb:   sb,
		done: make(chan struct{}),
	}
	runCtx, cancel := context.WithCancel(context.Background())
	rs.cancel = cancel
	m.mu.Lock()
	if sb.active != nil {
		m.mu.Unlock()
		cancel()
		return nil, ErrRunActive
	}
	sb.active = rs
	m.runs[rs.run.ID] = rs
	snap := m.snapshot(rs, false)
	m.mu.Unlock()

	m.publish(model.EventRunStatus, wfID, snap)
	go m.execute(runCtx, rs, func(ctx context.Context) error {
		return m.kubeSteps(ctx, rs, sb, req)
	}, func(model.Run) {})
	return &snap, nil
}

func restartable(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet"
}

func (m *Manager) kubeSteps(ctx context.Context, rs *runState, sb *sandbox, req KubeRunRequest) error {
	wfID := sb.id
	switch req.Action {
	case model.ActionUp:
		kc, err := m.ensureKubeCluster(ctx, rs, sb)
		if err != nil {
			return err
		}
		m.watchKube(sb, kc)
		if err := m.ensureNginxAlias(ctx, rs, kc, req.Sources); err != nil {
			return err
		}
		builds := req.Builds
		built := map[string]bool{}
		for _, b := range builds {
			built[normalizeImage(b.Image)] = true
		}
		if len(builds) > 0 {
			if err := m.buildAndLoad(ctx, rs, wfID, model.KubeClusterName(wfID), req.SourceDir, builds); err != nil {
				return err
			}
		}
		applied, err := m.applySources(ctx, rs, kc, req, built)
		if err != nil {
			return err
		}
		if err := m.waitRollout(ctx, rs, sb, applied, req.timeout()); err != nil {
			return err
		}
		if slices.ContainsFunc(applied, func(r kubeRef) bool { return r.Kind == "Ingress" }) {
			return m.waitIngressController(ctx, rs, kc, req.timeout())
		}
		return nil

	case model.ActionDown:
		node, err := m.inspectKubeNode(ctx, wfID)
		if err != nil {
			return err
		}
		if req.DeleteCluster {
			return m.deleteKubeCluster(ctx, rs, sb)
		}
		if node == nil {
			m.logLine(rs, "system", "There is no cluster: nothing to remove.")
			return nil
		}
		if !node.running() {
			return invalidKube("the cluster is stopped: start it to remove the workloads, or delete the cluster")
		}
		kc, err := m.connectKube(ctx, wfID, node, kubeAPIWait)
		if err != nil {
			return err
		}
		m.watchKube(sb, kc)
		if err := m.deleteWorkloads(ctx, rs, kc, wfID, req.RemoveVolumes); err != nil {
			return err
		}
		return m.waitGone(ctx, rs, sb, 2*time.Minute)

	case model.ActionRestart:
		node, err := m.inspectKubeNode(ctx, wfID)
		if err != nil {
			return err
		}
		if !node.running() {
			return invalidKube("the cluster is not running: run Up first")
		}
		kc, err := m.connectKube(ctx, wfID, node, kubeAPIWait)
		if err != nil {
			return err
		}
		m.watchKube(sb, kc)
		refs, err := m.restartWorkloads(ctx, rs, kc, sb, req.Workload)
		if err != nil {
			return err
		}
		return m.waitRollout(ctx, rs, sb, refs, req.timeout())

	case model.ActionClusterStart:
		kc, err := m.ensureKubeCluster(ctx, rs, sb)
		if err != nil {
			return err
		}
		m.watchKube(sb, kc)
		return nil

	case model.ActionClusterStop:
		node, err := m.inspectKubeNode(ctx, wfID)
		if err != nil {
			return err
		}
		if !node.running() {
			m.logLine(rs, "system", "The cluster is not running.")
			return nil
		}
		m.stopKubeWatch(sb, model.ClusterStopped, "")
		m.logLine(rs, "system", "$ docker stop "+node.Name)
		if _, err := m.docker(ctx, "stop", "-t", "30", node.Name); err != nil {
			return fmt.Errorf("stop the cluster: %w", err)
		}
		m.publishKubeCluster(sb, model.ClusterStopped, "The cluster is stopped: Up or Start cluster starts it again.")
		return nil

	case model.ActionClusterDelete:
		return m.deleteKubeCluster(ctx, rs, sb)
	}
	return invalidKube("unknown Kubernetes action %q", req.Action)
}

// deleteKubeCluster removes the node container (with its anonymous
// volumes: every object and PersistentVolume of the cluster), the images
// built for the workflow and the kubeconfig. The saved forward ports stay,
// so a new cluster gets the same host ports.
func (m *Manager) deleteKubeCluster(ctx context.Context, rs *runState, sb *sandbox) error {
	wfID := sb.id
	node, err := m.inspectKubeNode(ctx, wfID)
	if err != nil {
		return err
	}
	m.stopKubeWatch(sb, model.ClusterAbsent, "")
	if node != nil {
		if rs != nil {
			m.logLine(rs, "system", "$ docker rm -f -v "+node.Name)
		}
		if _, err := m.docker(ctx, "rm", "-f", "-v", node.Name); err != nil && !isNoSuch(err) {
			return fmt.Errorf("delete the cluster: %w", err)
		}
	} else if rs != nil {
		m.logLine(rs, "system", "There is no cluster to delete.")
	}
	if err := m.removeBuiltImages(ctx, wfID); err != nil {
		return fmt.Errorf("remove the workflow's built images: %w", err)
	}
	dir := m.kubeDir(wfID)
	for _, name := range []string{"kubeconfig", "inventory.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	m.publishKubeCluster(sb, model.ClusterAbsent, "No cluster yet: Up creates it.")
	return nil
}

// KubeStop stops a workflow's cluster outside a run (archiving frees its
// resources; everything is kept). ErrRunActive while a run is active.
func (m *Manager) KubeStop(ctx context.Context, wfID string) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	if m.activeRun(sb) {
		return ErrRunActive
	}
	node, err := m.inspectKubeNode(ctx, wfID)
	if err != nil || !node.running() {
		return err
	}
	m.stopKubeWatch(sb, model.ClusterStopped, "")
	if _, err := m.docker(ctx, "stop", "-t", "30", node.Name); err != nil {
		return err
	}
	m.publishKubeCluster(sb, model.ClusterStopped, "The cluster is stopped: Up or Start cluster starts it again.")
	return nil
}

// KubeRemove deletes the workflow's cluster, built images and Kubernetes
// work dir when the workflow is deleted. An active run is canceled first.
func (m *Manager) KubeRemove(ctx context.Context, wfID string) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	sb := m.sandbox(wfID)
	m.mu.Lock()
	active := sb.active
	m.mu.Unlock()
	if active != nil {
		_ = m.Cancel(active.run.ID)
		select {
		case <-active.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := m.deleteKubeCluster(ctx, nil, sb); err != nil {
		return err
	}
	return os.RemoveAll(m.kubeDir(wfID))
}

// ensureNginxAlias adds an IngressClass "nginx" served by Traefik, so
// Ingresses written for ingress-nginx route, unless the cluster or the
// workflow brings its own.
func (m *Manager) ensureNginxAlias(ctx context.Context, rs *runState, kc *kubeClient, sources []KubeApplySource) error {
	for _, src := range sources {
		if src.Helm != nil && strings.Contains(src.Helm.Chart.Name(), "ingress-nginx") {
			return nil
		}
		for _, o := range src.Objects {
			if name, _ := objMeta(o); str(o, "kind") == "IngressClass" && name == kubeNginxAlias {
				return nil
			}
		}
	}
	if _, err := kc.cs.NetworkingV1().IngressClasses().Get(ctx, kubeNginxAlias, metav1.GetOptions{}); err == nil {
		return nil
	}
	obj := map[string]any{
		"apiVersion": "networking.k8s.io/v1", "kind": "IngressClass",
		"metadata": map[string]any{"name": kubeNginxAlias, "labels": map[string]any{ComposeLabel: kc.wfID}},
		"spec":     map[string]any{"controller": "traefik.io/ingress-controller"},
	}
	if err := kc.applyObject(ctx, obj); err != nil {
		return fmt.Errorf("create the nginx IngressClass alias: %s", apiMessage(err))
	}
	m.logLine(rs, "system", "IngressClass nginx → Traefik (ingress-nginx manifests route through the cluster's Traefik)")
	return nil
}

// restartWorkloads triggers a rollout restart (the restartedAt template
// annotation, like kubectl rollout restart) of one workload or of every
// Deployment, StatefulSet and DaemonSet of the user's namespaces.
func (m *Manager) restartWorkloads(ctx context.Context, rs *runState, kc *kubeClient, sb *sandbox, workload string) ([]kubeRef, error) {
	l, ok := m.waitKubeSynced(ctx, sb, 30*time.Second)
	if !ok {
		return nil, errors.New("the cluster's objects could not be read")
	}
	st := buildKubeView(l.snapshot(sb.id, model.KubeClusterStatus{})).status
	var targets []model.KubeWorkloadStatus
	for _, w := range st.Workloads {
		if restartable(w.Kind) && (workload == "" || w.NodeID == workload) {
			targets = append(targets, w)
		}
	}
	if len(targets) == 0 {
		if workload != "" {
			return nil, invalidKube("workload %s is not in the cluster: run Up first", workload)
		}
		m.logLine(rs, "system", "No Deployment, StatefulSet or DaemonSet to restart.")
		return nil, nil
	}
	patch := fmt.Appendf(nil, `{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, time.Now().UTC().Format(time.RFC3339))
	var refs []kubeRef
	for _, w := range targets {
		var err error
		apps := kc.cs.AppsV1()
		opts := metav1.PatchOptions{FieldManager: kubeFieldManager}
		switch w.Kind {
		case "Deployment":
			_, err = apps.Deployments(w.Namespace).Patch(ctx, w.Name, types.StrategicMergePatchType, patch, opts)
		case "StatefulSet":
			_, err = apps.StatefulSets(w.Namespace).Patch(ctx, w.Name, types.StrategicMergePatchType, patch, opts)
		case "DaemonSet":
			_, err = apps.DaemonSets(w.Namespace).Patch(ctx, w.Name, types.StrategicMergePatchType, patch, opts)
		}
		if err != nil {
			return nil, fmt.Errorf("restart %s: %s", w.NodeID, apiMessage(err))
		}
		m.logLine(rs, "stdout", w.NodeID+" restarted")
		refs = append(refs, kubeRef{APIVersion: appsv1.SchemeGroupVersion.String(), Kind: w.Kind, Namespace: w.Namespace, Name: w.Name})
	}
	return refs, nil
}

// waitRollout waits until every applied workload is rolled out (Jobs and
// CronJobs are not waited for). It fails early when a workload stays in
// error (crash loop, image pull error, unschedulable) for ~10 s, with the
// problems, and on timeout with what is still pending.
func (m *Manager) waitRollout(ctx context.Context, rs *runState, sb *sandbox, refs []kubeRef, timeout time.Duration) error {
	want := map[string]bool{}
	for _, r := range refs {
		switch r.Kind {
		case "Deployment", "StatefulSet", "DaemonSet", "Pod":
			want[r.nodeID()] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	m.logLine(rs, "system", fmt.Sprintf("Waiting for %d workloads to roll out (up to %s)", len(want), timeout.Round(time.Second)))
	deadline := time.Now().Add(timeout)
	lastText := map[string]string{}
	errorSince := map[string]time.Time{}
	for {
		l, ok := m.waitKubeSynced(ctx, sb, 30*time.Second)
		if !ok {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("the cluster's objects could not be read")
		}
		v := buildKubeView(l.snapshot(sb.id, model.KubeClusterStatus{}))
		pending := []string{}
		var failing []model.KubeWorkloadStatus
		for _, id := range slices.Sorted(maps.Keys(want)) {
			i := slices.IndexFunc(v.status.Workloads, func(w model.KubeWorkloadStatus) bool { return w.NodeID == id })
			if i < 0 {
				pending = append(pending, id+" (not created yet)")
				continue
			}
			w := v.status.Workloads[i]
			if text := string(w.State) + ": " + w.Rollout; text != lastText[id] {
				lastText[id] = text
				m.logLine(rs, "stdout", id+" "+text)
			}
			switch w.State {
			case model.KubeRunning, model.KubeStopped:
				delete(errorSince, id)
				continue
			case model.KubeError:
				if _, ok := errorSince[id]; !ok {
					errorSince[id] = time.Now()
				}
				if time.Since(errorSince[id]) >= 10*time.Second {
					failing = append(failing, w)
				}
			default:
				delete(errorSince, id)
			}
			pending = append(pending, id+" ("+w.Rollout+")")
		}
		if len(failing) > 0 {
			var msgs []string
			for _, w := range failing {
				for _, p := range v.status.Problems {
					if p.NodeID == w.NodeID && p.Severity == "error" {
						m.logLine(rs, "stderr", p.Message+fixSuffix(p.Fix))
						msgs = append(msgs, p.Message)
					}
				}
			}
			return fmt.Errorf("rollout failed: %s", strings.Join(compactStrings(msgs), "; "))
		}
		if len(pending) == 0 {
			m.logLine(rs, "system", "All workloads are rolled out.")
			m.refreshKube(sb, sb.kube.current(), l)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("rollout did not finish within %s: %s", timeout.Round(time.Second), strings.Join(pending, ", "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// waitGone waits until the user's namespaces have no pods left.
func (m *Manager) waitGone(ctx context.Context, rs *runState, sb *sandbox, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		l, ok := m.waitKubeSynced(ctx, sb, 30*time.Second)
		if !ok {
			return ctx.Err()
		}
		s := l.snapshot(sb.id, model.KubeClusterStatus{})
		if len(s.pods) == 0 {
			m.logLine(rs, "system", "Every workload is gone.")
			if kc := sb.kube.current(); kc != nil {
				m.refreshKube(sb, kc, l)
			}
			return nil
		}
		if time.Now().After(deadline) {
			m.logLine(rs, "system", fmt.Sprintf("%d pods are still terminating; they go away on their own.", len(s.pods)))
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func fixSuffix(fix string) string {
	if fix == "" {
		return ""
	}
	return " — " + fix
}

func compactStrings(s []string) []string {
	out := []string{}
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// waitIngressController waits until the cluster's Traefik serves (a new
// cluster pulls and installs it within its first minute or two), so the
// Ingress URLs answer when up ends.
func (m *Manager) waitIngressController(ctx context.Context, rs *runState, kc *kubeClient, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	logged := false
	for {
		d, err := kc.cs.AppsV1().Deployments("kube-system").Get(ctx, "traefik", metav1.GetOptions{})
		if err == nil && d.Status.AvailableReplicas > 0 && d.Status.UpdatedReplicas == d.Status.Replicas {
			if logged {
				m.logLine(rs, "system", "The ingress controller (Traefik) is ready.")
			}
			return nil
		}
		if !logged {
			m.logLine(rs, "system", "Waiting for the cluster's ingress controller (Traefik; a new cluster downloads it first)")
			logged = true
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the ingress controller (Traefik) was not ready within %s", timeout.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
