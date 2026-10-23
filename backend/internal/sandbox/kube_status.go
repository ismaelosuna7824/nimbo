package sandbox

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// kubeSnapshot is everything the status is computed from: the informers'
// objects of the user's namespaces plus the cluster facts.
type kubeSnapshot struct {
	wfID         string
	cluster      model.KubeClusterStatus
	pods         []*corev1.Pod
	deployments  []*appsv1.Deployment
	statefulSets []*appsv1.StatefulSet
	daemonSets   []*appsv1.DaemonSet
	replicaSets  []*appsv1.ReplicaSet
	jobs         []*batchv1.Job
	cronJobs     []*batchv1.CronJob
	services     []*corev1.Service
	slices       []*discoveryv1.EndpointSlice
	ingresses    []*networkingv1.Ingress
	events       []*corev1.Event
	now          time.Time
}

// forwardSpec is a Service port the daemon forwards.
type forwardSpec struct {
	key       string // namespace/service/port
	nodeID    string
	namespace string
	service   string
	port      int
	portName  string
	http      bool
	ready     bool
}

// kubeView is a computed status plus what the forwarder and the samplers
// need.
type kubeView struct {
	status   model.KubeStatus
	links    map[string][]string      // Service node ID -> workload node IDs
	pods     map[string]containerInfo // running pod UID -> workload node ID, limits
	forwards []forwardSpec
}

// userNamespace: objects of the k3s system namespaces (CoreDNS, Traefik,
// metrics-server, ...) are not the workflow's.
func userNamespace(ns string) bool {
	return ns != "kube-system" && ns != "kube-public" && ns != "kube-node-lease"
}

// workloadAcc collects one workload while the view is built.
type workloadAcc struct {
	st       model.KubeWorkloadStatus
	complete bool // rollout complete / job finished
	failed   bool // job failed
	suspend  bool
	idle     bool // CronJob between runs
}

const maxKubeEvents = 50

// buildKubeView computes the status of the cluster's workloads.
func buildKubeView(s kubeSnapshot) kubeView {
	v := kubeView{links: map[string][]string{}, pods: map[string]containerInfo{}}
	st := model.KubeStatus{
		WorkflowID: s.wfID, Cluster: s.cluster, State: model.KubeAbsent,
		Workloads: []model.KubeWorkloadStatus{}, Forwards: []model.KubeForward{}, Ingresses: []model.KubeIngressURL{},
		Events: []model.KubeEvent{}, Problems: []model.KubeProblem{},
	}
	accs := map[string]*workloadAcc{}
	add := func(kind string, obj metav1.ObjectMeta) *workloadAcc {
		id := model.KubeNodeID(kind, obj.Namespace, obj.Name)
		a := &workloadAcc{st: model.KubeWorkloadStatus{NodeID: id, Kind: kind, Namespace: obj.Namespace, Name: obj.Name, Images: []string{}, Pods: []model.KubePod{}}}
		accs[id] = a
		return a
	}
	for _, d := range s.deployments {
		a := add("Deployment", d.ObjectMeta)
		a.st.Desired = int(ptrOr(d.Spec.Replicas, 1))
		a.st.Ready, a.st.Updated, a.st.Available = int(d.Status.ReadyReplicas), int(d.Status.UpdatedReplicas), int(d.Status.AvailableReplicas)
		a.complete = d.Status.ObservedGeneration >= d.Generation && a.st.Updated == a.st.Desired &&
			a.st.Available == a.st.Desired && int(d.Status.Replicas) == a.st.Desired
		a.st.Images = podImages(d.Spec.Template.Spec)
	}
	for _, d := range s.statefulSets {
		a := add("StatefulSet", d.ObjectMeta)
		a.st.Desired = int(ptrOr(d.Spec.Replicas, 1))
		a.st.Ready, a.st.Updated, a.st.Available = int(d.Status.ReadyReplicas), int(d.Status.UpdatedReplicas), int(d.Status.AvailableReplicas)
		a.complete = d.Status.ObservedGeneration >= d.Generation && a.st.Ready == a.st.Desired && int(d.Status.Replicas) == a.st.Desired &&
			(d.Status.UpdateRevision == "" || d.Status.CurrentRevision == d.Status.UpdateRevision || a.st.Updated == a.st.Desired)
		a.st.Images = podImages(d.Spec.Template.Spec)
	}
	for _, d := range s.daemonSets {
		a := add("DaemonSet", d.ObjectMeta)
		a.st.Desired = int(d.Status.DesiredNumberScheduled)
		a.st.Ready, a.st.Updated, a.st.Available = int(d.Status.NumberReady), int(d.Status.UpdatedNumberScheduled), int(d.Status.NumberAvailable)
		a.complete = d.Status.ObservedGeneration >= d.Generation && a.st.Updated == a.st.Desired && a.st.Available == a.st.Desired
		a.st.Images = podImages(d.Spec.Template.Spec)
	}
	cronOf := map[string]string{} // job namespace/name -> CronJob node ID
	for _, c := range s.cronJobs {
		a := add("CronJob", c.ObjectMeta)
		a.suspend = c.Spec.Suspend != nil && *c.Spec.Suspend
		a.idle = len(c.Status.Active) == 0
		if c.Status.LastScheduleTime != nil {
			t := c.Status.LastScheduleTime.UTC()
			a.st.LastSchedule = &t
		}
		a.st.Images = podImages(c.Spec.JobTemplate.Spec.Template.Spec)
	}
	// A CronJob's state follows its newest Job.
	newestJob := map[string]*batchv1.Job{}
	for _, j := range s.jobs {
		if owner := controllerOf(j.OwnerReferences); owner != nil && owner.Kind == "CronJob" {
			id := model.KubeNodeID("CronJob", j.Namespace, owner.Name)
			if accs[id] != nil {
				cronOf[j.Namespace+"/"+j.Name] = id
				if n := newestJob[id]; n == nil || j.CreationTimestamp.After(n.CreationTimestamp.Time) {
					newestJob[id] = j
				}
				continue
			}
		}
		a := add("Job", j.ObjectMeta)
		a.st.Desired = int(ptrOr(j.Spec.Completions, 1))
		a.st.Ready, a.st.Succeeded, a.st.Failed = int(ptrOr(j.Status.Ready, 0)), int(j.Status.Succeeded), int(j.Status.Failed)
		a.st.Available = a.st.Ready
		a.complete, a.failed = jobFinished(j)
		a.st.Images = podImages(j.Spec.Template.Spec)
	}
	for id, j := range newestJob {
		a := accs[id]
		a.st.Succeeded, a.st.Failed = int(j.Status.Succeeded), int(j.Status.Failed)
		a.complete, a.failed = jobFinished(j)
	}
	rsOwner := map[string]string{} // ReplicaSet namespace/name -> Deployment node ID
	for _, r := range s.replicaSets {
		if owner := controllerOf(r.OwnerReferences); owner != nil && owner.Kind == "Deployment" {
			rsOwner[r.Namespace+"/"+r.Name] = model.KubeNodeID("Deployment", r.Namespace, owner.Name)
		}
	}
	podWorkload := func(p *corev1.Pod) string {
		owner := controllerOf(p.OwnerReferences)
		if owner == nil {
			return ""
		}
		switch owner.Kind {
		case "ReplicaSet":
			return rsOwner[p.Namespace+"/"+owner.Name]
		case "Job":
			if id := cronOf[p.Namespace+"/"+owner.Name]; id != "" {
				return id
			}
			return model.KubeNodeID("Job", p.Namespace, owner.Name)
		case "StatefulSet", "DaemonSet":
			return model.KubeNodeID(owner.Kind, p.Namespace, owner.Name)
		}
		return ""
	}

	podNode := map[string]string{}       // pod namespace/name -> workload node ID
	readySince := map[string]time.Time{} // pod namespace/name -> when it last became Ready (ready pods only)
	for _, p := range sortedPods(s.pods) {
		id := podWorkload(p)
		if id == "" && controllerOf(p.OwnerReferences) == nil {
			a := add("Pod", p.ObjectMeta)
			a.st.Desired = 1
			a.st.Images = podImages(p.Spec)
			id = a.st.NodeID
		}
		a := accs[id]
		if a == nil {
			continue // a pod of something else (an operator's custom workload)
		}
		podNode[p.Namespace+"/"+p.Name] = id
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				readySince[p.Namespace+"/"+p.Name] = c.LastTransitionTime.Time
			}
		}
		kp := kubePod(p)
		a.st.Pods = append(a.st.Pods, kp)
		if a.st.Kind == "Pod" {
			if kp.Ready {
				a.st.Ready, a.st.Available, a.st.Updated = 1, 1, 1
			}
			a.complete = p.Status.Phase == corev1.PodSucceeded || kp.Ready
			a.failed = p.Status.Phase == corev1.PodFailed
		}
		if p.Status.Phase == corev1.PodRunning {
			v.pods[string(p.UID)] = containerInfo{service: id, cpuLimit: kp.CPULimit, memLimit: kp.MemLimit}
		}
		st.Problems = append(st.Problems, podProblems(id, p)...)
	}
	for _, a := range accs {
		a.st.State = workloadState(a)
		a.st.Rollout = rolloutText(a)
		a.st.StartupMs = slowestStartup(a.st.Pods)
		if a.failed {
			st.Problems = append(st.Problems, model.KubeProblem{
				NodeID: a.st.NodeID, Namespace: a.st.Namespace, Kind: "job-failed", Severity: "error",
				Message: fmt.Sprintf("%s %s failed (%d failed pods)", a.st.Kind, a.st.Name, a.st.Failed),
				Fix:     "Open the logs of its pods; the Job stops retrying after backoffLimit failures.",
			})
		}
		st.Workloads = append(st.Workloads, a.st)
		if stateRank[composeState(a.st.State)] > stateRank[composeState(st.State)] {
			st.State = a.st.State
		}
	}
	slices.SortFunc(st.Workloads, func(a, b model.KubeWorkloadStatus) int { return strings.Compare(a.NodeID, b.NodeID) })

	// Events (probe failures become problems while they still matter: the pod exists and failed a probe since it
	// last became ready — a readiness probe failing while the app starts is not a problem once the pod is ready).
	probeSeen := map[string]bool{}
	events := slices.Clone(s.events)
	slices.SortFunc(events, func(a, b *corev1.Event) int { return eventTime(b).Compare(eventTime(a)) })
	for _, e := range events {
		io := e.InvolvedObject
		id := ""
		switch io.Kind {
		case "Pod":
			id = podNode[io.Namespace+"/"+io.Name]
		case "ReplicaSet":
			id = rsOwner[io.Namespace+"/"+io.Name]
		}
		if id == "" {
			id = kubeNodeID(io.APIVersion, io.Kind, io.Namespace, io.Name)
		}
		podKey := io.Namespace + "/" + io.Name
		_, podExists := podNode[podKey]
		since, ready := readySince[podKey]
		current := podExists && (!ready || eventTime(e).After(since))
		if e.Type == corev1.EventTypeWarning && e.Reason == "Unhealthy" && io.Kind == "Pod" && current && !probeSeen[io.Name] && s.now.Sub(eventTime(e)) < 5*time.Minute {
			probeSeen[io.Name] = true
			st.Problems = append(st.Problems, model.KubeProblem{
				NodeID: id, Namespace: io.Namespace, Pod: io.Name, Kind: "probe", Severity: "warning",
				Message: truncate(e.Message, 300), Fix: "Check the probe's path/port and initialDelaySeconds, and that the app answers on it.",
			})
		}
		if len(st.Events) < maxKubeEvents {
			st.Events = append(st.Events, model.KubeEvent{
				Type: e.Type, Reason: e.Reason, Message: truncate(e.Message, 500), NodeID: id, Kind: io.Kind,
				Namespace: io.Namespace, Name: io.Name, Count: int(max(e.Count, 1)), LastSeen: eventTime(e).UTC(),
			})
		}
	}
	slices.SortStableFunc(st.Problems, func(a, b model.KubeProblem) int {
		if a.Severity != b.Severity {
			return strings.Compare(a.Severity, b.Severity) // error < warning
		}
		return cmp.Compare(a.NodeID, b.NodeID)
	})

	// Service -> workloads, and the forwarded ports.
	slicesBySvc := map[string][]*discoveryv1.EndpointSlice{}
	for _, es := range s.slices {
		if svc := es.Labels[discoveryv1.LabelServiceName]; svc != "" {
			slicesBySvc[es.Namespace+"/"+svc] = append(slicesBySvc[es.Namespace+"/"+svc], es)
		}
	}
	for _, svc := range sortedServices(s.services) {
		// ExternalName Services have no pods; default/kubernetes is the API
		// server itself (reached through the kubeconfig).
		if svc.Spec.Type == corev1.ServiceTypeExternalName || svc.Namespace == "default" && svc.Name == "kubernetes" {
			continue
		}
		sid := model.KubeNodeID("Service", svc.Namespace, svc.Name)
		ws := map[string]bool{}
		for _, es := range slicesBySvc[svc.Namespace+"/"+svc.Name] {
			for _, ep := range es.Endpoints {
				if ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
					if id := podNode[svc.Namespace+"/"+ep.TargetRef.Name]; id != "" {
						ws[id] = true
					}
				}
			}
		}
		if len(ws) == 0 && len(svc.Spec.Selector) > 0 {
			sel := labels.SelectorFromSet(svc.Spec.Selector)
			for _, p := range s.pods {
				if p.Namespace == svc.Namespace && sel.Matches(labels.Set(p.Labels)) {
					if id := podNode[p.Namespace+"/"+p.Name]; id != "" {
						ws[id] = true
					}
				}
			}
		}
		if len(ws) > 0 {
			v.links[sid] = slices.Sorted(maps.Keys(ws))
		}
		for _, sp := range svc.Spec.Ports {
			if sp.Protocol != "" && sp.Protocol != corev1.ProtocolTCP {
				continue
			}
			target, ready := endpointPort(slicesBySvc[svc.Namespace+"/"+svc.Name], sp.Name)
			if target == 0 {
				target = int(sp.TargetPort.IntVal)
			}
			v.forwards = append(v.forwards, forwardSpec{
				key: svc.Namespace + "/" + svc.Name + "/" + strconv.Itoa(int(sp.Port)), nodeID: sid,
				namespace: svc.Namespace, service: svc.Name, port: int(sp.Port), portName: sp.Name,
				http: httpPort(sp.Name, int(sp.Port), target, sp.AppProtocol), ready: ready,
			})
		}
	}

	// Ingress URLs.
	for _, ing := range s.ingresses {
		class := ""
		if ing.Spec.IngressClassName != nil {
			class = *ing.Spec.IngressClassName
		} else if c := ing.Annotations["kubernetes.io/ingress.class"]; c != "" {
			class = c
		}
		iid := model.KubeNodeID("Ingress", ing.Namespace, ing.Name)
		addURL := func(host, p string, b *networkingv1.IngressServiceBackend) {
			if b == nil {
				return
			}
			if p == "" {
				p = "/"
			}
			port := b.Port.Name
			if b.Port.Number != 0 {
				port = strconv.Itoa(int(b.Port.Number))
			}
			u := model.KubeIngressURL{
				NodeID: iid, Namespace: ing.Namespace, Ingress: ing.Name, Host: host, Path: p, Service: b.Name, Port: port,
				ServiceNodeID: model.KubeNodeID("Service", ing.Namespace, b.Name), Class: class,
			}
			if ip := s.cluster.IngressPort; ip > 0 {
				u.Address = fmt.Sprintf("http://127.0.0.1:%d%s", ip, p)
				u.URL = u.Address
				if host != "" && !strings.Contains(host, "*") {
					u.URL = fmt.Sprintf("http://%s:%d%s", host, ip, p)
				}
			}
			st.Ingresses = append(st.Ingresses, u)
		}
		if db := ing.Spec.DefaultBackend; db != nil {
			addURL("", "/", db.Service)
		}
		for _, r := range ing.Spec.Rules {
			if r.HTTP == nil {
				continue
			}
			for _, p := range r.HTTP.Paths {
				addURL(r.Host, p.Path, p.Backend.Service)
			}
		}
	}
	slices.SortStableFunc(st.Ingresses, func(a, b model.KubeIngressURL) int { return strings.Compare(a.NodeID, b.NodeID) })
	v.status = st
	return v
}

func ptrOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func controllerOf(refs []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range refs {
		if refs[i].Controller != nil && *refs[i].Controller {
			return &refs[i]
		}
	}
	return nil
}

func jobFinished(j *batchv1.Job) (complete, failed bool) {
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete, batchv1.JobSuccessCriteriaMet:
			complete = true
		case batchv1.JobFailed, batchv1.JobFailureTarget:
			failed = true
		}
	}
	return complete, failed
}

func podImages(spec corev1.PodSpec) []string {
	out := []string{}
	for _, c := range slices.Concat(spec.InitContainers, spec.Containers) {
		if c.Image != "" && !slices.Contains(out, c.Image) {
			out = append(out, c.Image)
		}
	}
	return out
}

func sortedPods(pods []*corev1.Pod) []*corev1.Pod {
	out := slices.Clone(pods)
	slices.SortFunc(out, func(a, b *corev1.Pod) int {
		return cmp.Or(strings.Compare(a.Namespace, b.Namespace), strings.Compare(a.Name, b.Name))
	})
	return out
}

func sortedServices(svcs []*corev1.Service) []*corev1.Service {
	out := slices.Clone(svcs)
	slices.SortFunc(out, func(a, b *corev1.Service) int {
		return cmp.Or(strings.Compare(a.Namespace, b.Namespace), strings.Compare(a.Name, b.Name))
	})
	return out
}

func eventTime(e *corev1.Event) time.Time {
	switch {
	case !e.LastTimestamp.IsZero():
		return e.LastTimestamp.Time
	case !e.EventTime.IsZero():
		return e.EventTime.Time
	case e.Series != nil && !e.Series.LastObservedTime.IsZero():
		return e.Series.LastObservedTime.Time
	case !e.FirstTimestamp.IsZero():
		return e.FirstTimestamp.Time
	}
	return e.CreationTimestamp.Time
}

// kubePod converts a pod to the status shape.
func kubePod(p *corev1.Pod) model.KubePod {
	kp := model.KubePod{
		Name: p.Name, Namespace: p.Namespace, Phase: string(p.Status.Phase), Node: p.Spec.NodeName, IP: p.Status.PodIP,
		Reason: p.Status.Reason, Message: p.Status.Message, Containers: []model.KubePodContainer{},
	}
	if kp.Phase == "" {
		kp.Phase = string(corev1.PodPending)
	}
	if p.Status.StartTime != nil {
		t := p.Status.StartTime.UTC()
		kp.StartedAt = &t
	}
	for _, c := range p.Status.Conditions {
		switch {
		case c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue:
			kp.Ready = true
			t := c.LastTransitionTime.UTC()
			kp.ReadyAt = &t
		case c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && kp.Reason == "":
			kp.Reason, kp.Message = c.Reason, c.Message
		}
	}
	add := func(statuses []corev1.ContainerStatus, init bool) {
		for _, cs := range statuses {
			kc := model.KubePodContainer{Name: cs.Name, Image: cs.Image, Init: init, Ready: cs.Ready, Restarts: int(cs.RestartCount)}
			switch {
			case cs.State.Waiting != nil:
				kc.State, kc.Reason, kc.Message = "waiting", cs.State.Waiting.Reason, cs.State.Waiting.Message
			case cs.State.Running != nil:
				kc.State = "running"
			case cs.State.Terminated != nil:
				t := cs.State.Terminated
				kc.State, kc.Reason, kc.Message, kc.ExitCode = "terminated", t.Reason, t.Message, int(t.ExitCode)
				kc.OOMKilled = t.Reason == "OOMKilled"
			}
			if t := cs.LastTerminationState.Terminated; t != nil {
				kc.LastReason, kc.LastExitCode = t.Reason, int(t.ExitCode)
				kc.OOMKilled = kc.OOMKilled || t.Reason == "OOMKilled"
			}
			kp.Restarts += kc.Restarts
			kp.Containers = append(kp.Containers, kc)
		}
	}
	add(p.Status.InitContainerStatuses, true)
	add(p.Status.ContainerStatuses, false)
	// The most telling reason: a waiting container's, then an OOM kill.
	for _, c := range kp.Containers {
		if c.State == "waiting" && c.Reason != "" && c.Reason != "PodInitializing" && c.Reason != "ContainerCreating" {
			kp.Reason, kp.Message = c.Reason, c.Message
			break
		}
		if c.OOMKilled && kp.Reason == "" {
			kp.Reason = "OOMKilled"
		}
	}
	cpu, mem, limited := 0.0, int64(0), len(p.Spec.Containers) > 0
	for _, c := range p.Spec.Containers {
		l := c.Resources.Limits
		cq, cok := l[corev1.ResourceCPU]
		mq, mok := l[corev1.ResourceMemory]
		if !cok || !mok {
			limited = false
		}
		cpu += cq.AsApproximateFloat64()
		mem += mq.Value()
	}
	if limited {
		kp.CPULimit, kp.MemLimit = round2(cpu), mem
	}
	return kp
}

// fatalWaiting are waiting reasons that do not resolve by themselves.
var fatalWaiting = map[string]bool{
	"CrashLoopBackOff": true, "ErrImagePull": true, "ImagePullBackOff": true, "InvalidImageName": true,
	"CreateContainerConfigError": true, "CreateContainerError": true, "RunContainerError": true, "ErrImageNeverPull": true,
}

// workloadState maps a workload's rollout and pods to a KubeState.
func workloadState(a *workloadAcc) model.KubeState {
	for _, p := range a.st.Pods {
		for _, c := range p.Containers {
			if c.State == "waiting" && fatalWaiting[c.Reason] {
				return model.KubeError
			}
		}
		if p.Reason == "Unschedulable" {
			return model.KubeError
		}
	}
	switch a.st.Kind {
	case "Job":
		switch {
		case a.failed:
			return model.KubeError
		case a.complete:
			return model.KubeStopped
		case a.st.Ready > 0:
			return model.KubeRunning
		}
		return model.KubeStarting
	case "CronJob":
		switch {
		case a.failed:
			return model.KubeError
		case a.suspend:
			return model.KubeStopped
		case !a.idle && !a.complete:
			return model.KubeStarting
		}
		return model.KubeRunning // scheduled; runs on its schedule
	case "Pod":
		switch {
		case a.failed:
			return model.KubeError
		case len(a.st.Pods) > 0 && a.st.Pods[0].Phase == string(corev1.PodSucceeded):
			return model.KubeStopped
		case a.complete:
			return model.KubeRunning
		}
		return model.KubeStarting
	}
	switch {
	case a.st.Desired == 0 && len(a.st.Pods) == 0:
		return model.KubeStopped
	case a.complete:
		return model.KubeRunning
	}
	return model.KubeStarting
}

// rolloutText explains a workload's rollout in one line.
func rolloutText(a *workloadAcc) string {
	w := a.st
	switch w.Kind {
	case "Job":
		switch {
		case a.failed:
			return fmt.Sprintf("failed (%d failed pods)", w.Failed)
		case a.complete:
			return fmt.Sprintf("completed (%d of %d)", w.Succeeded, w.Desired)
		}
		return fmt.Sprintf("running: %d of %d completed", w.Succeeded, w.Desired)
	case "CronJob":
		switch {
		case a.suspend:
			return "suspended"
		case w.LastSchedule == nil:
			return "waiting for its first scheduled run"
		}
		return "last run " + w.LastSchedule.Format(time.RFC3339)
	case "Pod":
		if len(w.Pods) > 0 {
			return strings.ToLower(w.Pods[0].Phase)
		}
		return "pending"
	}
	switch {
	case w.Desired == 0:
		return "scaled to 0"
	case a.complete:
		return fmt.Sprintf("%d of %d ready", w.Ready, w.Desired)
	case w.Updated < w.Desired:
		return fmt.Sprintf("rolling out: %d of %d pods updated, %d ready", w.Updated, w.Desired, w.Ready)
	}
	return fmt.Sprintf("waiting for pods: %d of %d ready", w.Ready, w.Desired)
}

// slowestStartup is start → ready of the slowest ready pod.
func slowestStartup(pods []model.KubePod) *int64 {
	var best *int64
	for _, p := range pods {
		if p.StartedAt == nil || p.ReadyAt == nil || p.ReadyAt.Before(*p.StartedAt) {
			continue
		}
		ms := p.ReadyAt.Sub(*p.StartedAt).Milliseconds()
		if best == nil || ms > *best {
			best = &ms
		}
	}
	return best
}

// podProblems explains what is wrong with a pod, if anything.
func podProblems(nodeID string, p *corev1.Pod) []model.KubeProblem {
	var out []model.KubeProblem
	add := func(container, kind, sev, msg, fix string, exit, restarts int) {
		out = append(out, model.KubeProblem{NodeID: nodeID, Namespace: p.Namespace, Pod: p.Name, Container: container,
			Kind: kind, Severity: sev, Message: msg, Fix: fix, ExitCode: exit, Restarts: restarts})
	}
	limitOf := func(name string) string {
		for _, c := range slices.Concat(p.Spec.InitContainers, p.Spec.Containers) {
			if c.Name == name {
				if q, ok := c.Resources.Limits[corev1.ResourceMemory]; ok {
					return q.String()
				}
			}
		}
		return ""
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason == "Unschedulable" {
			add("", "unschedulable", "error", "Cannot be scheduled: "+truncate(c.Message, 300),
				"The local cluster has one node with the Docker host's CPUs and memory: lower the pod's resources.requests, or fix its nodeSelector/affinity/tolerations.", 0, 0)
		}
	}
	for _, cs := range slices.Concat(p.Status.InitContainerStatuses, p.Status.ContainerStatuses) {
		restarts := int(cs.RestartCount)
		last := cs.LastTerminationState.Terminated
		oom := last != nil && last.Reason == "OOMKilled" || cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled"
		if oom {
			msg := fmt.Sprintf("%s was killed for using more memory than its limit", cs.Name)
			if l := limitOf(cs.Name); l != "" {
				msg += " (" + l + ")"
			}
			if restarts > 0 {
				msg += fmt.Sprintf("; restarted %d times", restarts)
			}
			add(cs.Name, "oom", "error", msg, "Raise resources.limits.memory of the container, or reduce what the app keeps in memory.", 137, restarts)
			continue
		}
		if w := cs.State.Waiting; w != nil {
			switch w.Reason {
			case "CrashLoopBackOff":
				msg := fmt.Sprintf("%s keeps crashing (restarted %d times)", cs.Name, restarts)
				exit := 0
				if last != nil {
					exit = int(last.ExitCode)
					msg += fmt.Sprintf("; last exit code %d", exit)
					if m := exitMeaning(exit); m != "" {
						msg += " (" + m + ")"
					}
				}
				add(cs.Name, "crashloop", "error", msg, "Open its logs (the previous run's last lines explain the crash) and check its command, env and probes.", exit, restarts)
				continue
			case "ErrImagePull", "ImagePullBackOff", "ErrImageNeverPull":
				fix := "Check the image name and tag, and that the registry is reachable from Docker."
				if img := containerImage(p, cs.Name); !registryHost(img) && !strings.Contains(img, "/") {
					fix = "If this image is built from the workflow's files, add it to the workflow's builds (or name its folder after the image) so up builds and loads it; otherwise check the name and tag."
				}
				add(cs.Name, "image-pull", "error", fmt.Sprintf("%s cannot pull %s: %s", cs.Name, containerImage(p, cs.Name), truncate(w.Message, 300)), fix, 0, restarts)
				continue
			case "InvalidImageName", "CreateContainerConfigError", "CreateContainerError", "RunContainerError":
				add(cs.Name, "config", "error", fmt.Sprintf("%s: %s: %s", cs.Name, w.Reason, truncate(w.Message, 300)),
					"A ConfigMap/Secret (or a key) the container references is missing, or the image name is invalid: fix the reference or create the object.", 0, restarts)
				continue
			}
		}
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 && p.Spec.RestartPolicy != corev1.RestartPolicyAlways {
			msg := fmt.Sprintf("%s exited with code %d", cs.Name, t.ExitCode)
			if m := exitMeaning(int(t.ExitCode)); m != "" {
				msg += " (" + m + ")"
			}
			add(cs.Name, "exit", "error", msg, "Open its logs.", int(t.ExitCode), restarts)
			continue
		}
		if restarts > 0 {
			msg := fmt.Sprintf("%s restarted %d times", cs.Name, restarts)
			if last != nil {
				msg += fmt.Sprintf(" (last exit code %d", last.ExitCode)
				if m := exitMeaning(int(last.ExitCode)); m != "" {
					msg += ", " + m
				}
				msg += ")"
			}
			add(cs.Name, "restarts", "warning", msg, "A failing liveness probe or a crash restarts the container: check its logs and probes.", 0, restarts)
		}
	}
	return out
}

func containerImage(p *corev1.Pod, name string) string {
	for _, c := range slices.Concat(p.Spec.InitContainers, p.Spec.Containers) {
		if c.Name == name {
			return c.Image
		}
	}
	return ""
}

// endpointPort returns the target port of a Service port (by name) and
// whether a ready endpoint serves it.
func endpointPort(eps []*discoveryv1.EndpointSlice, portName string) (int, bool) {
	port, ready := 0, false
	for _, es := range eps {
		for _, pp := range es.Ports {
			if ptrOr(pp.Name, "") != portName || pp.Port == nil {
				continue
			}
			port = int(*pp.Port)
			for _, ep := range es.Endpoints {
				if ptrOr(ep.Conditions.Ready, true) {
					ready = true
				}
			}
		}
	}
	return port, ready
}

// readyEndpoint picks a ready pod behind a Service port: its name and the
// container port. n rotates over the ready pods (the n-th modulo their
// count), so successive connections spread like the Service's own ones.
func readyEndpoint(eps []*discoveryv1.EndpointSlice, portName string, n uint64) (pod string, port int, ok bool) {
	type target struct {
		pod  string
		port int
	}
	var ready []target
	for _, es := range eps {
		for _, pp := range es.Ports {
			if ptrOr(pp.Name, "") != portName || pp.Port == nil {
				continue
			}
			for _, ep := range es.Endpoints {
				if ptrOr(ep.Conditions.Ready, true) && ep.TargetRef != nil && ep.TargetRef.Kind == "Pod" {
					ready = append(ready, target{ep.TargetRef.Name, int(*pp.Port)})
				}
			}
		}
	}
	if len(ready) == 0 {
		return "", 0, false
	}
	// Informer order is not stable: rotate over a sorted list.
	slices.SortFunc(ready, func(a, b target) int { return strings.Compare(a.pod, b.pod) })
	t := ready[n%uint64(len(ready))]
	return t.pod, t.port, true
}

// httpPort reports whether a Service port is offered as an HTTP URL: its
// appProtocol, else its name (the http/https/web, http-*, *-http and tcp-*
// conventions Istio and most charts follow: Alertmanager's http-web on
// 9093 is HTTP although Kafka's TLS listener uses the same number), else
// the well-known numbers of other protocols.
func httpPort(name string, port, target int, appProtocol *string) bool {
	if appProtocol != nil {
		ap := strings.ToLower(*appProtocol)
		return ap == "http" || ap == "https" || strings.HasPrefix(ap, "kubernetes.io/h") || ap == "http2"
	}
	switch n := strings.ToLower(name); {
	case n == "http" || n == "https" || n == "web" || strings.HasPrefix(n, "http-") || strings.HasPrefix(n, "https-") || strings.HasSuffix(n, "-http"):
		return true
	case strings.HasPrefix(n, "tcp") || strings.HasPrefix(n, "udp") || strings.HasPrefix(n, "grpc") || strings.HasPrefix(n, "tls"):
		return false
	}
	return !nonHTTPPorts[port] && !nonHTTPPorts[target]
}

// composeState lets KubeState reuse the compose state ranks.
func composeState(s model.KubeState) model.ComposeState { return model.ComposeState(s) }
