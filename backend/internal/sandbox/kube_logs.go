package sandbox

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// KubeLogSelector picks the containers whose logs KubeLogs reads: the pods
// of a workload (node ID) or one pod (Namespace + Pod), every container
// unless Container is set.
type KubeLogSelector struct {
	Workload  string
	Namespace string
	Pod       string
	Container string
}

// MaxKubeLogTail caps the lines KubeLogs returns per container.
const MaxKubeLogTail = 5000

// KubeLogs emits the last tail lines of the selected containers, oldest
// first per container. With follow it keeps streaming until ctx ends or
// every followed container stops. emit is never called concurrently.
func (m *Manager) KubeLogs(ctx context.Context, wfID string, sel KubeLogSelector, tail int, follow bool, emit func(model.KubeLogLine)) error {
	if err := checkID(wfID); err != nil {
		return err
	}
	if sel.Workload == "" && (sel.Pod == "" || sel.Namespace == "") {
		return invalidKube("logs need a workload node ID, or a namespace and a pod")
	}
	tail = max(0, min(tail, MaxKubeLogTail))
	sb := m.sandbox(wfID)
	if _, err := m.KubeStatus(ctx, wfID); err != nil {
		return err
	}
	l, ok := m.waitKubeSynced(ctx, sb, 10*time.Second)
	kc := sb.kube.current()
	if !ok || kc == nil {
		return invalidKube("the cluster is not running")
	}
	view := buildKubeView(l.snapshot(wfID, model.KubeClusterStatus{}))
	type target struct {
		nodeID, ns, pod, container string
	}
	var targets []target
	for _, w := range view.status.Workloads {
		if sel.Workload != "" && w.NodeID != sel.Workload {
			continue
		}
		for _, p := range w.Pods {
			if sel.Pod != "" && (p.Name != sel.Pod || p.Namespace != sel.Namespace) {
				continue
			}
			for _, c := range p.Containers {
				// A container that never started has no log yet.
				if (sel.Container == "" || c.Name == sel.Container) && (c.State != "waiting" || c.Restarts > 0) {
					targets = append(targets, target{w.NodeID, p.Namespace, p.Name, c.Name})
				}
			}
		}
	}
	if len(targets) == 0 {
		if sel.Workload != "" && !slices.ContainsFunc(view.status.Workloads, func(w model.KubeWorkloadStatus) bool { return w.NodeID == sel.Workload }) {
			return invalidKube("workload %s is not in the cluster", sel.Workload)
		}
		return nil // no container has started yet
	}
	slices.SortFunc(targets, func(a, b target) int {
		return cmp.Or(strings.Compare(a.pod, b.pod), strings.Compare(a.container, b.container))
	})
	var mu sync.Mutex
	send := func(l model.KubeLogLine) {
		mu.Lock()
		emit(l)
		mu.Unlock()
	}
	read := func(ctx context.Context, t target, follow bool) error {
		opts := &corev1.PodLogOptions{Container: t.container, Follow: follow, Timestamps: true}
		if tail > 0 || !follow {
			n := int64(tail)
			opts.TailLines = &n
		}
		rc, err := kc.cs.CoreV1().Pods(t.ns).GetLogs(t.pod, opts).Stream(ctx)
		if err != nil {
			return errors.New(apiMessage(err))
		}
		defer rc.Close()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			send(model.KubeLogLine{NodeID: t.nodeID, Namespace: t.ns, Pod: t.pod, Container: t.container, Line: parseLogLine("stdout", sc.Text())})
		}
		return nil
	}
	if !follow {
		for _, t := range targets {
			if err := read(ctx, t, false); err != nil {
				return err
			}
		}
		return nil
	}
	var wg sync.WaitGroup
	errs := make([]error, len(targets))
	for i, t := range targets {
		wg.Go(func() { errs[i] = read(ctx, t, true) })
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(errs...)
}
