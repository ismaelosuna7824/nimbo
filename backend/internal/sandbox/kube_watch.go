package sandbox

import (
	"context"
	"encoding/json"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// kubeListers read the informers' caches.
type kubeListers struct {
	factory informers.SharedInformerFactory
}

func listAll[T any](l interface {
	List(labels.Selector) ([]T, error)
}) []T {
	out, _ := l.List(labels.Everything())
	return out
}

func userOnly[T interface{ GetNamespace() string }](in []T) []T {
	out := in[:0:0]
	for _, o := range in {
		if userNamespace(o.GetNamespace()) {
			out = append(out, o)
		}
	}
	return out
}

func (l kubeListers) snapshot(wfID string, cluster model.KubeClusterStatus) kubeSnapshot {
	f := l.factory
	return kubeSnapshot{
		wfID:         wfID,
		cluster:      cluster,
		pods:         userOnly(listAll[*corev1.Pod](f.Core().V1().Pods().Lister())),
		deployments:  userOnly(listAll[*appsv1.Deployment](f.Apps().V1().Deployments().Lister())),
		statefulSets: userOnly(listAll[*appsv1.StatefulSet](f.Apps().V1().StatefulSets().Lister())),
		daemonSets:   userOnly(listAll[*appsv1.DaemonSet](f.Apps().V1().DaemonSets().Lister())),
		replicaSets:  userOnly(listAll[*appsv1.ReplicaSet](f.Apps().V1().ReplicaSets().Lister())),
		jobs:         userOnly(listAll[*batchv1.Job](f.Batch().V1().Jobs().Lister())),
		cronJobs:     userOnly(listAll[*batchv1.CronJob](f.Batch().V1().CronJobs().Lister())),
		services:     userOnly(listAll[*corev1.Service](f.Core().V1().Services().Lister())),
		slices:       userOnly(listAll[*discoveryv1.EndpointSlice](f.Discovery().V1().EndpointSlices().Lister())),
		ingresses:    userOnly(listAll[*networkingv1.Ingress](f.Networking().V1().Ingresses().Lister())),
		events:       userOnly(listAll[*corev1.Event](f.Core().V1().Events().Lister())),
		now:          time.Now(),
	}
}

// endpointSlices of one Service (live, for the port-forwarder).
func (l kubeListers) endpointSlices(ns, svc string) []*discoveryv1.EndpointSlice {
	sel := labels.SelectorFromSet(labels.Set{discoveryv1.LabelServiceName: svc})
	out, _ := l.factory.Discovery().V1().EndpointSlices().Lister().EndpointSlices(ns).List(sel)
	return out
}

// hpas of the user namespaces (load test autoscaling samples).
func (l kubeListers) hpas() []*autoscalingv2.HorizontalPodAutoscaler {
	return userOnly(listAll[*autoscalingv2.HorizontalPodAutoscaler](l.factory.Autoscaling().V2().HorizontalPodAutoscalers().Lister()))
}

// clusterStatus describes the cluster the client talks to.
func (m *Manager) clusterStatus(wfID string, kc *kubeClient, state model.KubeClusterState, msg string) model.KubeClusterStatus {
	cs := model.KubeClusterStatus{Name: model.KubeClusterName(wfID), State: state, Image: KubeNodeImage, Message: msg}
	if kc != nil {
		cs.Version, cs.APIPort, cs.IngressPort = kc.version, kc.apiPort, kc.ingressPort
		cs.Kubeconfig = kc.kubeconfig
		cs.IngressClass, cs.StorageClass = KubeIngressClass, KubeStorageClass
	}
	return cs
}

// watchKube starts the informers of a running cluster (once) and keeps
// the status, port-forwards and samplers in sync with every change.
func (m *Manager) watchKube(sb *sandbox, kc *kubeClient) {
	k := &sb.kube
	k.mu.Lock()
	if k.watch != nil {
		k.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	k.watch = cancel
	k.synced = make(chan struct{})
	k.gen++
	gen, synced := k.gen, k.synced
	if k.forwards == nil {
		k.forwards = newForwarder(m.kubeDir(sb.id))
	}
	k.mu.Unlock()

	f := informers.NewSharedInformerFactory(kc.cs, 10*time.Minute)
	l := kubeListers{factory: f}
	changed := make(chan struct{}, 1)
	notify := func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}
	h := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { notify() },
		UpdateFunc: func(any, any) { notify() },
		DeleteFunc: func(any) { notify() },
	}
	for _, inf := range []cache.SharedIndexInformer{
		f.Core().V1().Pods().Informer(), f.Apps().V1().Deployments().Informer(), f.Apps().V1().StatefulSets().Informer(),
		f.Apps().V1().DaemonSets().Informer(), f.Apps().V1().ReplicaSets().Informer(), f.Batch().V1().Jobs().Informer(),
		f.Batch().V1().CronJobs().Informer(), f.Core().V1().Services().Informer(), f.Discovery().V1().EndpointSlices().Informer(),
		f.Networking().V1().Ingresses().Informer(), f.Core().V1().Events().Informer(),
	} {
		_, _ = inf.AddEventHandler(h)
	}
	// Load tests sample HPAs (kubeProbe.Scaling); the status is not built
	// from them, so their 15 s status updates need no refresh.
	f.Autoscaling().V2().HorizontalPodAutoscalers().Informer()
	f.Start(ctx.Done())
	k.forwards.setConnector(newKubeConnector(kc, l))

	go func() {
		defer f.Shutdown()
		f.WaitForCacheSync(ctx.Done())
		k.mu.Lock()
		if k.gen == gen {
			k.listers = &l
		}
		k.mu.Unlock()
		close(synced)
		m.refreshKube(sb, kc, l)
		health := time.NewTicker(10 * time.Second)
		defer health.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-changed:
				// Debounce bursts (a rollout updates many objects).
				time.Sleep(300 * time.Millisecond)
				select {
				case <-changed:
				default:
				}
				m.refreshKube(sb, kc, l)
			case <-health.C:
				// The node may be stopped behind Nimbo's back (Docker
				// restart, docker stop): stop watching it then.
				hctx, hcancel := context.WithTimeout(ctx, 10*time.Second)
				node, err := m.inspectKubeNode(hctx, sb.id)
				hcancel()
				if ctx.Err() != nil {
					return
				}
				if err != nil || !node.running() {
					go m.stopKubeWatch(sb, model.ClusterStopped, "The cluster's node container is not running.")
					return
				}
				m.refreshKube(sb, kc, l)
			}
		}
	}()
}

// refreshKube rebuilds the status from the caches, syncs the forwards and
// the sampler, and publishes the status when it changed.
func (m *Manager) refreshKube(sb *sandbox, kc *kubeClient, l kubeListers) model.KubeStatus {
	v := buildKubeView(l.snapshot(sb.id, m.clusterStatus(sb.id, kc, model.ClusterRunning, "")))
	k := &sb.kube
	k.mu.Lock()
	k.links, k.pods = v.links, v.pods
	fw := k.forwards
	k.mu.Unlock()
	if fw != nil {
		v.status.Forwards = fw.sync(v.forwards)
		for i := range v.status.Forwards {
			v.status.Forwards[i].Workloads = v.links[v.status.Forwards[i].NodeID]
		}
	}
	m.syncKubeStats(sb, v.pods)
	v.status.UpdatedAt = time.Now().UTC()
	m.recordKube(sb, v.status)
	return v.status
}

// recordKube stores st and publishes it when it changed.
func (m *Manager) recordKube(sb *sandbox, st model.KubeStatus) {
	cmp := st
	cmp.UpdatedAt = time.Time{}
	data, _ := json.Marshal(cmp)
	k := &sb.kube
	k.mu.Lock()
	changed := k.lastJSON != string(data)
	k.lastJSON = string(data)
	k.last = &cmp
	k.mu.Unlock()
	if changed {
		m.publish(model.EventKubeStatus, sb.id, st)
	}
}

// publishKubeCluster publishes the cluster part of the status while no
// informer runs (creating, starting, stopped, deleted).
func (m *Manager) publishKubeCluster(sb *sandbox, state model.KubeClusterState, msg string) model.KubeStatus {
	st := emptyKubeStatus(sb.id, m.clusterStatus(sb.id, nil, state, msg))
	if node := m.loadKubeCluster(sb.id); node.APIPort > 0 && state != model.ClusterAbsent {
		st.Cluster.APIPort, st.Cluster.IngressPort = node.APIPort, node.IngressPort
	}
	st.UpdatedAt = time.Now().UTC()
	m.recordKube(sb, st)
	return st
}

func emptyKubeStatus(wfID string, cluster model.KubeClusterStatus) model.KubeStatus {
	return model.KubeStatus{
		WorkflowID: wfID, Cluster: cluster, State: model.KubeAbsent, Workloads: []model.KubeWorkloadStatus{},
		Forwards: []model.KubeForward{}, Ingresses: []model.KubeIngressURL{}, Events: []model.KubeEvent{}, Problems: []model.KubeProblem{},
	}
}

// stopKubeWatch stops the informers, the forwards and the sampler and
// publishes the cluster as state.
func (m *Manager) stopKubeWatch(sb *sandbox, state model.KubeClusterState, msg string) {
	k := &sb.kube
	k.mu.Lock()
	cancel := k.watch
	k.watch, k.synced, k.listers = nil, nil, nil
	k.client = nil
	k.gen++
	fw := k.forwards
	k.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if fw != nil {
		fw.stopAll()
	}
	m.syncKubeStats(sb, nil)
	m.publishKubeCluster(sb, state, msg)
}

// KubeStatus returns the live status of a Kubernetes workflow's cluster.
// Without Docker or a cluster the status is absent with a message, not an
// error. Reading it starts the informers of a running cluster.
func (m *Manager) KubeStatus(ctx context.Context, wfID string) (model.KubeStatus, error) {
	if err := checkID(wfID); err != nil {
		return model.KubeStatus{}, err
	}
	sb := m.sandbox(wfID)
	node, err := m.inspectKubeNode(ctx, wfID)
	switch {
	case err != nil:
		if ctx.Err() != nil {
			return model.KubeStatus{}, ctx.Err()
		}
		msg := "Docker is not available: " + err.Error()
		if m.DockerAvailable(ctx) == nil {
			msg = err.Error()
		}
		return m.statusWithout(sb, model.ClusterAbsent, msg), nil
	case node == nil:
		return m.statusWithout(sb, model.ClusterAbsent, "No cluster yet: Up creates it."), nil
	case !node.running():
		return m.statusWithout(sb, model.ClusterStopped, "The cluster is stopped: Up or Start cluster starts it again."), nil
	}
	kc, err := m.connectKube(ctx, wfID, node, 15*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return model.KubeStatus{}, ctx.Err()
		}
		st := emptyKubeStatus(wfID, m.clusterStatus(wfID, nil, model.ClusterStarting, "The cluster's API server is not answering yet: "+err.Error()))
		st.Cluster.APIPort, st.Cluster.IngressPort = node.APIPort, node.IngressPort
		st.UpdatedAt = time.Now().UTC()
		m.recordKube(sb, st)
		return st, nil
	}
	m.watchKube(sb, kc)
	l, ok := m.waitKubeSynced(ctx, sb, 10*time.Second)
	if !ok {
		if ctx.Err() != nil {
			return model.KubeStatus{}, ctx.Err()
		}
		st := emptyKubeStatus(wfID, m.clusterStatus(wfID, kc, model.ClusterStarting, "Reading the cluster's objects…"))
		st.UpdatedAt = time.Now().UTC()
		return st, nil
	}
	return m.refreshKube(sb, kc, l), nil
}

// statusWithout stops watching and returns the cluster-only status.
func (m *Manager) statusWithout(sb *sandbox, state model.KubeClusterState, msg string) model.KubeStatus {
	k := &sb.kube
	k.mu.Lock()
	watching := k.watch != nil
	k.mu.Unlock()
	if watching {
		m.stopKubeWatch(sb, state, msg)
	}
	return m.publishKubeCluster(sb, state, msg)
}

// waitKubeSynced waits for the informers' first sync.
func (m *Manager) waitKubeSynced(ctx context.Context, sb *sandbox, d time.Duration) (kubeListers, bool) {
	k := &sb.kube
	k.mu.Lock()
	synced := k.synced
	k.mu.Unlock()
	if synced == nil {
		return kubeListers{}, false
	}
	select {
	case <-synced:
	case <-ctx.Done():
		return kubeListers{}, false
	case <-time.After(d):
		return kubeListers{}, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.listers == nil {
		return kubeListers{}, false
	}
	return *k.listers, true
}
