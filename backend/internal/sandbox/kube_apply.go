package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// applyOrder ranks kinds like Helm's install order, with
// CustomResourceDefinitions right after Namespaces so the custom resources
// of the same up find their kind. Unknown kinds (custom resources) come
// last; deletion runs in reverse.
var applyOrder = []string{
	"Namespace", "CustomResourceDefinition", "NetworkPolicy", "ResourceQuota", "LimitRange",
	"PriorityClass", "PodDisruptionBudget", "ServiceAccount", "Secret", "ConfigMap", "StorageClass",
	"PersistentVolume", "PersistentVolumeClaim", "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding",
	"Service", "DaemonSet", "Pod", "ReplicationController", "ReplicaSet", "Deployment",
	"HorizontalPodAutoscaler", "StatefulSet", "Job", "CronJob", "IngressClass", "Ingress",
	"GatewayClass", "Gateway", "HTTPRoute", "APIService", "MutatingWebhookConfiguration", "ValidatingWebhookConfiguration",
}

var applyRank = func() map[string]int {
	r := make(map[string]int, len(applyOrder))
	for i, k := range applyOrder {
		r[k] = i
	}
	return r
}()

func kindRank(kind string) int {
	if r, ok := applyRank[kind]; ok {
		return r
	}
	return len(applyOrder)
}

// sortForApply orders objects for apply (stable within a kind).
func sortForApply(objs []map[string]any) {
	slices.SortStableFunc(objs, func(a, b map[string]any) int {
		return kindRank(str(a, "kind")) - kindRank(str(b, "kind"))
	})
}

func str(obj map[string]any, key string) string {
	s, _ := obj[key].(string)
	return s
}

func mapAt(obj map[string]any, path ...string) map[string]any {
	cur := obj
	for _, p := range path {
		next, _ := cur[p].(map[string]any)
		if next == nil {
			return nil
		}
		cur = next
	}
	return cur
}

func objMeta(obj map[string]any) (name, namespace string) {
	md := mapAt(obj, "metadata")
	if md == nil {
		return "", ""
	}
	return str(md, "name"), str(md, "namespace")
}

// kubeRef identifies an applied object (inventory entry).
type kubeRef struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Source     int    `json:"source"`
}

func (r kubeRef) key() string {
	g := schema.FromAPIVersionAndKind(r.APIVersion, r.Kind).GroupKind()
	return g.String() + "/" + r.Namespace + "/" + r.Name
}

// nodeID is the graph node ID of the object (model.KubeNodeID).
func (r kubeRef) nodeID() string { return kubeNodeID(r.APIVersion, r.Kind, r.Namespace, r.Name) }

// kubeNodeID is the graph node ID of an object, like the graph's
// (kube.IsBuiltin decides when the kind carries its group).
func kubeNodeID(apiVersion, kind, namespace, name string) string {
	if !kube.IsBuiltin(apiVersion) {
		kind += "." + schema.FromAPIVersionAndKind(apiVersion, kind).Group
	}
	return model.KubeNodeID(kind, namespace, name)
}

// kubeInventory is <kube dir>/inventory.json: what the last ups applied,
// so a later up prunes objects removed from the files and down deletes
// exactly them.
type kubeInventory struct {
	Objects []kubeRef `json:"objects"`
	// Releases are the Helm releases installed (namespace/name).
	Releases []kubeRelease `json:"releases"`
	// Namespaces were created by Nimbo for objects that named a missing
	// namespace; down deletes them.
	Namespaces []string `json:"namespaces"`
}

type kubeRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Source    int    `json:"source"`
}

func (m *Manager) loadInventory(wfID string) kubeInventory {
	var inv kubeInventory
	if data, err := os.ReadFile(filepath.Join(m.kubeDir(wfID), "inventory.json")); err == nil {
		_ = json.Unmarshal(data, &inv)
	}
	return inv
}

func (m *Manager) saveInventory(wfID string, inv kubeInventory) error {
	inv.Objects = nonNilSlice(inv.Objects)
	inv.Releases = nonNilSlice(inv.Releases)
	inv.Namespaces = nonNilSlice(inv.Namespaces)
	data, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(m.kubeDir(wfID), "inventory.json"), data)
}

func nonNilSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// localImages normalizes image references for matching ("api" and
// "docker.io/library/api:latest" are the same image).
func normalizeImage(ref string) string {
	ref = strings.TrimSpace(ref)
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	ref = strings.TrimPrefix(ref, "docker.io/")
	ref = strings.TrimPrefix(ref, "library/")
	if slash, colon := strings.LastIndex(ref, "/"), strings.LastIndex(ref, ":"); colon <= slash {
		ref += ":latest"
	}
	return ref
}

// podSpecs returns the pod specs inside a workload object.
func podSpecs(obj map[string]any) []map[string]any {
	switch str(obj, "kind") {
	case "Pod":
		if s := mapAt(obj, "spec"); s != nil {
			return []map[string]any{s}
		}
	case "CronJob":
		if s := mapAt(obj, "spec", "jobTemplate", "spec", "template", "spec"); s != nil {
			return []map[string]any{s}
		}
	default:
		if s := mapAt(obj, "spec", "template", "spec"); s != nil {
			return []map[string]any{s}
		}
	}
	return nil
}

// KubeObjectImages lists the container images of a workload object (its
// pod template's init and app containers).
func KubeObjectImages(obj map[string]any) []string {
	var out []string
	for _, spec := range podSpecs(obj) {
		for _, key := range []string{"initContainers", "containers"} {
			list, _ := spec[key].([]any)
			for _, c := range list {
				if cm, _ := c.(map[string]any); cm != nil && str(cm, "image") != "" {
					out = append(out, str(cm, "image"))
				}
			}
		}
	}
	return out
}

// localPullPolicy makes containers of locally built images use the image
// loaded into the node (IfNotPresent) instead of pulling it: Always, or
// the default of untagged/:latest images, would fail against a registry
// that has no such image. It returns the images it changed.
func localPullPolicy(obj map[string]any, built map[string]bool) []string {
	var changed []string
	for _, spec := range podSpecs(obj) {
		for _, key := range []string{"initContainers", "containers"} {
			list, _ := spec[key].([]any)
			for _, c := range list {
				cm, _ := c.(map[string]any)
				if cm == nil || !built[normalizeImage(str(cm, "image"))] {
					continue
				}
				if p := str(cm, "imagePullPolicy"); p == "IfNotPresent" || p == "Never" {
					continue
				}
				cm["imagePullPolicy"] = "IfNotPresent"
				changed = append(changed, str(cm, "image"))
			}
		}
	}
	return changed
}

// prepareObjects deep-copies a source's objects, fills the default
// namespace of namespaced kinds and the pull policy of local images.
func prepareObjects(src KubeApplySource, built map[string]bool, namespaced func(apiVersion, kind string) (bool, error)) ([]map[string]any, []string, error) {
	ns := src.Namespace
	if ns == "" {
		ns = "default"
	}
	var out []map[string]any
	var notes []string
	for _, o := range src.Objects {
		obj := deepCopyMap(o)
		if str(obj, "apiVersion") == "" || str(obj, "kind") == "" {
			return nil, nil, invalidKube("source %s: an object has no apiVersion or kind", src.Path)
		}
		name, objNS := objMeta(obj)
		if name == "" {
			return nil, nil, invalidKube("source %s: a %s has no metadata.name", src.Path, str(obj, "kind"))
		}
		scoped, err := namespaced(str(obj, "apiVersion"), str(obj, "kind"))
		if err != nil {
			return nil, nil, err
		}
		md := mapAt(obj, "metadata")
		switch {
		case scoped && objNS == "":
			md["namespace"] = ns
		case !scoped && objNS != "":
			delete(md, "namespace")
		}
		for _, img := range localPullPolicy(obj, built) {
			notes = append(notes, fmt.Sprintf("%s %s uses the locally built image %s: imagePullPolicy IfNotPresent", str(obj, "kind"), name, img))
		}
		out = append(out, obj)
	}
	sortForApply(out)
	return out, notes, nil
}

func deepCopyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return deepCopyMap(t)
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopyValue(e)
		}
		return out
	}
	return v
}

// mapping resolves the REST mapping of a kind, refreshing discovery once
// when the kind is unknown (a CRD applied moments ago).
func (kc *kubeClient) mapping(apiVersion, kind string) (*meta.RESTMapping, error) {
	gvk := schema.FromAPIVersionAndKind(apiVersion, kind)
	mp, err := kc.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil && meta.IsNoMatchError(err) {
		kc.mapper.Reset()
		mp, err = kc.mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	}
	if err != nil && meta.IsNoMatchError(err) {
		return nil, invalidKube("the cluster does not know %s %s: install its CustomResourceDefinition first (add it to the workflow or to a Helm chart's crds/)", apiVersion, kind)
	}
	return mp, err
}

func (kc *kubeClient) namespaced(apiVersion, kind string) (bool, error) {
	if kind == "CustomResourceDefinition" || kind == "Namespace" {
		return false, nil
	}
	mp, err := kc.mapping(apiVersion, kind)
	if err != nil {
		var inv *InvalidKubeError
		if errors.As(err, &inv) {
			// Custom resources of a CRD applied in the same up: assume
			// namespaced until discovery knows the kind.
			return true, nil
		}
		return false, err
	}
	return mp.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

// applyObject server-side applies obj (field manager nimbo, forcing
// conflicts like `kubectl apply --server-side --force-conflicts`).
func (kc *kubeClient) applyObject(ctx context.Context, obj map[string]any) error {
	mp, err := kc.mapping(str(obj, "apiVersion"), str(obj, "kind"))
	if err != nil {
		return err
	}
	name, ns := objMeta(obj)
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	ri := kc.dyn.Resource(mp.Resource)
	opts := metav1.PatchOptions{FieldManager: kubeFieldManager, Force: new(true)}
	if mp.Scope.Name() == meta.RESTScopeNameNamespace {
		_, err = ri.Namespace(ns).Patch(ctx, name, types.ApplyPatchType, data, opts)
	} else {
		_, err = ri.Patch(ctx, name, types.ApplyPatchType, data, opts)
	}
	return err
}

// waitCRD waits until a CustomResourceDefinition is established and
// resets discovery so its kind maps.
func (kc *kubeClient) waitCRD(ctx context.Context, name string) error {
	gvr := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	deadline := time.Now().Add(time.Minute)
	for {
		u, err := kc.dyn.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			conds, _ := u.Object["status"].(map[string]any)["conditions"].([]any)
			for _, c := range conds {
				cm, _ := c.(map[string]any)
				if str(cm, "type") == "Established" && str(cm, "status") == "True" {
					kc.mapper.Reset()
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("CustomResourceDefinition %s was not established within a minute", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// ensureNamespace creates namespace ns when missing; it reports whether it
// did.
func (kc *kubeClient) ensureNamespace(ctx context.Context, ns string) (bool, error) {
	if ns == "" {
		return false, nil
	}
	_, err := kc.cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err == nil {
		return false, nil
	}
	if !apierrors.IsNotFound(err) {
		return false, err
	}
	_, err = kc.cs.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}, metav1.CreateOptions{FieldManager: kubeFieldManager})
	if apierrors.IsAlreadyExists(err) {
		return false, nil
	}
	return err == nil, err
}

// deleteRef deletes one object (background propagation); missing objects
// and unknown kinds are not errors.
func (kc *kubeClient) deleteRef(ctx context.Context, r kubeRef) error {
	mp, err := kc.mapping(r.APIVersion, r.Kind)
	if err != nil {
		var inv *InvalidKubeError
		if errors.As(err, &inv) {
			return nil // its CRD went away with it
		}
		return err
	}
	opts := metav1.DeleteOptions{PropagationPolicy: new(metav1.DeletePropagationBackground)}
	ri := kc.dyn.Resource(mp.Resource)
	if mp.Scope.Name() == meta.RESTScopeNameNamespace {
		err = ri.Namespace(r.Namespace).Delete(ctx, r.Name, opts)
	} else {
		err = ri.Delete(ctx, r.Name, opts)
	}
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// systemNamespaces are never deleted, nor listed as the user's.
var systemNamespaces = map[string]bool{"default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true}

// applySources applies every source in order and records the inventory;
// objects of the previous inventory that are gone from the files are
// pruned. It returns the applied refs (for the rollout wait).
func (m *Manager) applySources(ctx context.Context, rs *runState, kc *kubeClient, req KubeRunRequest, built map[string]bool) ([]kubeRef, error) {
	wfID := req.Workflow.ID
	old := m.loadInventory(wfID)
	inv := kubeInventory{Namespaces: slices.Clone(old.Namespaces)}
	var applied []kubeRef
	for _, src := range req.Sources {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if src.Helm != nil {
			refs, err := m.helmUpgrade(ctx, rs, kc, wfID, src, built, req.timeout())
			if err != nil {
				return nil, fmt.Errorf("helm release %s: %w", src.Helm.Release, err)
			}
			inv.Releases = append(inv.Releases, kubeRelease{Name: src.Helm.Release, Namespace: helmNamespace(src), Source: src.Index})
			applied = append(applied, refs...)
			continue
		}
		m.logLine(rs, "system", fmt.Sprintf("Applying %s source %s (%d objects, server-side apply)", src.Kind, src.Path, len(src.Objects)))
		objs, notes, err := prepareObjects(src, built, kc.namespaced)
		if err != nil {
			return nil, err
		}
		for _, n := range notes {
			m.logLine(rs, "system", n)
		}
		for _, obj := range objs {
			name, ns := objMeta(obj)
			if ns != "" && !slices.ContainsFunc(objs, func(o map[string]any) bool { n, _ := objMeta(o); return str(o, "kind") == "Namespace" && n == ns }) {
				created, err := kc.ensureNamespace(ctx, ns)
				if err != nil {
					return nil, fmt.Errorf("create namespace %s: %w", ns, err)
				}
				if created && !slices.Contains(inv.Namespaces, ns) {
					m.logLine(rs, "system", "Created namespace "+ns)
					inv.Namespaces = append(inv.Namespaces, ns)
				}
			}
			ref := kubeRef{APIVersion: str(obj, "apiVersion"), Kind: str(obj, "kind"), Namespace: ns, Name: name, Source: src.Index}
			if err := applyWithRetry(ctx, serviceIPRetry, func() error { return kc.applyObject(ctx, obj) }); err != nil {
				return nil, fmt.Errorf("apply %s: %s", ref.nodeID(), apiMessage(err))
			}
			m.logLine(rs, "stdout", ref.nodeID()+" applied")
			applied = append(applied, ref)
			if ref.Kind == "CustomResourceDefinition" {
				if err := kc.waitCRD(ctx, name); err != nil {
					return nil, err
				}
			}
		}
	}
	inv.Objects = slices.DeleteFunc(slices.Clone(applied), func(r kubeRef) bool { return r.Source < 0 })
	// Prune: previously applied objects that this up did not apply, then
	// releases of removed Helm sources.
	keep := map[string]bool{}
	for _, r := range applied {
		keep[r.key()] = true
	}
	stale := slices.DeleteFunc(slices.Clone(old.Objects), func(r kubeRef) bool { return keep[r.key()] })
	sortRefsForDelete(stale)
	for _, r := range stale {
		m.logLine(rs, "system", "Pruning "+r.nodeID()+" (no longer in the files)")
		if err := kc.deleteRef(ctx, r); err != nil {
			return nil, fmt.Errorf("prune %s: %s", r.nodeID(), apiMessage(err))
		}
	}
	for _, rel := range old.Releases {
		if !slices.ContainsFunc(inv.Releases, func(n kubeRelease) bool { return n.Name == rel.Name && n.Namespace == rel.Namespace }) {
			m.logLine(rs, "system", "Uninstalling Helm release "+rel.Name+" (its source was removed)")
			if err := m.helmUninstall(ctx, rs, kc, rel); err != nil {
				return nil, err
			}
		}
	}
	if err := m.saveInventory(wfID, inv); err != nil {
		return nil, err
	}
	return applied, nil
}

// sortRefsForDelete orders refs for deletion: reverse apply order.
func sortRefsForDelete(refs []kubeRef) {
	slices.SortStableFunc(refs, func(a, b kubeRef) int { return kindRank(b.Kind) - kindRank(a.Kind) })
}

// apiMessage is the API server's message of err (without the Go
// wrapping), else err.Error().
func apiMessage(err error) string {
	var se apierrors.APIStatus
	if errors.As(err, &se) {
		if msg := se.Status().Message; msg != "" {
			return msg
		}
	}
	return err.Error()
}

// deleteWorkloads removes what ups applied: Helm releases, then the
// inventory's objects in reverse apply order. PersistentVolumeClaims stay
// unless volumes (then every claim of the namespaces used goes too), and
// so do the namespaces Nimbo created that still hold them.
func (m *Manager) deleteWorkloads(ctx context.Context, rs *runState, kc *kubeClient, wfID string, volumes bool) error {
	inv := m.loadInventory(wfID)
	for _, rel := range inv.Releases {
		m.logLine(rs, "system", "Uninstalling Helm release "+rel.Name)
		if err := m.helmUninstall(ctx, rs, kc, rel); err != nil {
			return err
		}
	}
	refs := slices.Clone(inv.Objects)
	sortRefsForDelete(refs)
	var kept []kubeRef
	namespaces := map[string]bool{}
	for _, r := range refs {
		if r.Namespace != "" {
			namespaces[r.Namespace] = true
		}
		if r.Kind == "PersistentVolumeClaim" && !volumes {
			kept = append(kept, r)
			continue
		}
		if r.Kind == "Namespace" && !volumes && m.namespaceHasClaims(ctx, kc, r.Name) {
			m.logLine(rs, "system", "Keeping namespace "+r.Name+": it holds PersistentVolumeClaims (delete them with volumes)")
			kept = append(kept, r)
			continue
		}
		if err := kc.deleteRef(ctx, r); err != nil {
			return fmt.Errorf("delete %s: %s", r.nodeID(), apiMessage(err))
		}
		m.logLine(rs, "stdout", r.nodeID()+" deleted")
	}
	for _, rel := range inv.Releases {
		namespaces[rel.Namespace] = true
	}
	if volumes {
		for _, ns := range slices.Sorted(maps.Keys(namespaces)) {
			if ns == "kube-system" {
				continue
			}
			list, err := kc.cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{})
			if err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return err
			}
			for _, c := range list.Items {
				if err := kc.cs.CoreV1().PersistentVolumeClaims(ns).Delete(ctx, c.Name, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
					return err
				}
				m.logLine(rs, "stdout", model.KubeNodeID("PersistentVolumeClaim", ns, c.Name)+" deleted")
			}
		}
	}
	var keptNS []string
	for _, ns := range inv.Namespaces {
		if systemNamespaces[ns] {
			continue
		}
		if !volumes && m.namespaceHasClaims(ctx, kc, ns) {
			keptNS = append(keptNS, ns)
			continue
		}
		if err := kc.deleteRef(ctx, kubeRef{APIVersion: "v1", Kind: "Namespace", Name: ns}); err != nil {
			return err
		}
		m.logLine(rs, "stdout", "Namespace/"+ns+" deleted")
	}
	return m.saveInventory(wfID, kubeInventory{Objects: kept, Namespaces: keptNS})
}

func (m *Manager) namespaceHasClaims(ctx context.Context, kc *kubeClient, ns string) bool {
	list, err := kc.cs.CoreV1().PersistentVolumeClaims(ns).List(ctx, metav1.ListOptions{Limit: 1})
	return err == nil && len(list.Items) > 0
}
