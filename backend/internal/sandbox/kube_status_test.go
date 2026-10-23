package sandbox

import (
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func objectMeta(ns, name string, owner ...metav1.OwnerReference) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: ns, Name: name, UID: types.UID(ns + "-" + name), OwnerReferences: owner, Generation: 1, CreationTimestamp: metav1.NewTime(t0)}
}

func ownedBy(kind, name string) metav1.OwnerReference {
	return metav1.OwnerReference{Kind: kind, Name: name, Controller: new(true)}
}

func readyPod(ns, name string, owner metav1.OwnerReference, labels map[string]string) *corev1.Pod {
	p := &corev1.Pod{ObjectMeta: objectMeta(ns, name, owner), Spec: corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyAlways,
		Containers: []corev1.Container{{Name: "app", Image: "app:dev", Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("64Mi"),
		}}}},
	}}
	p.Labels = labels
	p.Status = corev1.PodStatus{
		Phase: corev1.PodRunning, StartTime: new(metav1.NewTime(t0)),
		Conditions:        []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(t0.Add(1500 * time.Millisecond))}},
		ContainerStatuses: []corev1.ContainerStatus{{Name: "app", Image: "app:dev", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
	}
	return p
}

func deployment(ns, name string, replicas, ready, updated, available int32) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: objectMeta(ns, name), Spec: appsv1.DeploymentSpec{Replicas: new(replicas),
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "app:dev"}}}}},
		Status: appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: replicas, ReadyReplicas: ready, UpdatedReplicas: updated, AvailableReplicas: available}}
}

func workload(t *testing.T, st model.KubeStatus, id string) model.KubeWorkloadStatus {
	t.Helper()
	for _, w := range st.Workloads {
		if w.NodeID == id {
			return w
		}
	}
	t.Fatalf("no workload %s in %+v", id, st.Workloads)
	return model.KubeWorkloadStatus{}
}

func problemKinds(st model.KubeStatus, id string) []string {
	var out []string
	for _, p := range st.Problems {
		if p.NodeID == id {
			out = append(out, p.Kind)
		}
	}
	return out
}

func TestBuildKubeViewWorkloads(t *testing.T) {
	rs := &appsv1.ReplicaSet{ObjectMeta: objectMeta("shop", "web-7d9", ownedBy("Deployment", "web"))}
	web1 := readyPod("shop", "web-7d9-a", ownedBy("ReplicaSet", "web-7d9"), map[string]string{"app": "web"})
	web2 := readyPod("shop", "web-7d9-b", ownedBy("ReplicaSet", "web-7d9"), map[string]string{"app": "web"})
	web2.Status.ContainerStatuses[0].RestartCount = 2
	web2.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}}

	// A rolling Deployment whose new pod crash-loops.
	apiRS := &appsv1.ReplicaSet{ObjectMeta: objectMeta("shop", "api-1", ownedBy("Deployment", "api"))}
	api := readyPod("shop", "api-1-x", ownedBy("ReplicaSet", "api-1"), map[string]string{"app": "api"})
	api.Status.Conditions = nil
	api.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", RestartCount: 4,
		State:                corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off 40s"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}}}}

	// A StatefulSet pod that cannot pull a local-looking image.
	db := &appsv1.StatefulSet{ObjectMeta: objectMeta("shop", "db"), Spec: appsv1.StatefulSetSpec{Replicas: new(int32(1))},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 1, Replicas: 1}}
	dbPod := readyPod("shop", "db-0", ownedBy("StatefulSet", "db"), map[string]string{"app": "db"})
	dbPod.Spec.Containers[0].Image = "mydb:dev"
	dbPod.Status.Conditions = nil
	dbPod.Status.ContainerStatuses = []corev1.ContainerStatus{{Name: "app", State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff", Message: "pull access denied"}}}}

	// A pending pod that does not fit the node.
	big := &appsv1.DaemonSet{ObjectMeta: objectMeta("shop", "agent"), Status: appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 1}}
	bigPod := &corev1.Pod{ObjectMeta: objectMeta("shop", "agent-x", ownedBy("DaemonSet", "agent")), Status: corev1.PodStatus{Phase: corev1.PodPending,
		Conditions: []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes are available: 1 Insufficient cpu."}}}}

	// Jobs: one done, one failed; a CronJob whose last Job succeeded.
	done := &batchv1.Job{ObjectMeta: objectMeta("shop", "migrate"), Status: batchv1.JobStatus{Succeeded: 1,
		Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}}
	failed := &batchv1.Job{ObjectMeta: objectMeta("shop", "seed"), Status: batchv1.JobStatus{Failed: 3,
		Conditions: []batchv1.JobCondition{{Type: batchv1.JobFailed, Status: corev1.ConditionTrue}}}}
	failedPod := &corev1.Pod{ObjectMeta: objectMeta("shop", "seed-x", ownedBy("Job", "seed")), Spec: corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever},
		Status: corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{Name: "seed",
			State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 2}}}}}}
	cron := &batchv1.CronJob{ObjectMeta: objectMeta("shop", "nightly"), Status: batchv1.CronJobStatus{LastScheduleTime: new(metav1.NewTime(t0))}}
	cronRun := &batchv1.Job{ObjectMeta: objectMeta("shop", "nightly-1", ownedBy("CronJob", "nightly")), Status: batchv1.JobStatus{Succeeded: 1,
		Conditions: []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}}}
	cronPod := &corev1.Pod{ObjectMeta: objectMeta("shop", "nightly-1-x", ownedBy("Job", "nightly-1")), Status: corev1.PodStatus{Phase: corev1.PodSucceeded}}

	// A bare pod and a Deployment scaled to zero.
	bare := readyPod("shop", "debug", metav1.OwnerReference{}, nil)
	bare.OwnerReferences = nil

	s := kubeSnapshot{
		wfID: "wf", now: t0,
		deployments:  []*appsv1.Deployment{deployment("shop", "web", 2, 2, 2, 2), deployment("shop", "api", 1, 0, 1, 0), deployment("shop", "idle", 0, 0, 0, 0)},
		replicaSets:  []*appsv1.ReplicaSet{rs, apiRS},
		statefulSets: []*appsv1.StatefulSet{db},
		daemonSets:   []*appsv1.DaemonSet{big},
		jobs:         []*batchv1.Job{done, failed, cronRun},
		cronJobs:     []*batchv1.CronJob{cron},
		pods:         []*corev1.Pod{web1, web2, api, dbPod, bigPod, failedPod, cronPod, bare},
		events: []*corev1.Event{{
			// A liveness probe failing after the pod became ready (t0+1.5s): a problem.
			ObjectMeta: objectMeta("shop", "ev1"), Type: corev1.EventTypeWarning, Reason: "Unhealthy", Count: 3, LastTimestamp: metav1.NewTime(t0.Add(2 * time.Second)),
			Message:        "Liveness probe failed: HTTP probe failed with statuscode: 500",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: "web-7d9-a", APIVersion: "v1"},
		}, {
			// A readiness probe failing while the app started, before the pod became ready: not a problem.
			ObjectMeta: objectMeta("shop", "ev2"), Type: corev1.EventTypeWarning, Reason: "Unhealthy", Count: 1, LastTimestamp: metav1.NewTime(t0),
			Message:        "Readiness probe failed: connect: connection refused",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: "web-7d9-b", APIVersion: "v1"},
		}, {
			// A pod of a previous rollout that is gone: not a problem.
			ObjectMeta: objectMeta("shop", "ev3"), Type: corev1.EventTypeWarning, Reason: "Unhealthy", Count: 1, LastTimestamp: metav1.NewTime(t0),
			Message:        "Readiness probe failed: connect: connection refused",
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Namespace: "shop", Name: "web-6c1-z", APIVersion: "v1"},
		}},
	}
	v := buildKubeView(s)
	st := v.status

	w := workload(t, st, "Deployment/shop/web")
	if w.State != model.KubeRunning || w.Ready != 2 || len(w.Pods) != 2 || w.Rollout != "2 of 2 ready" || w.StartupMs == nil || *w.StartupMs != 1500 {
		t.Errorf("web = %+v", w)
	}
	if w.Pods[0].CPULimit != 0.25 || w.Pods[0].MemLimit != 64<<20 {
		t.Errorf("web limits = %v %v", w.Pods[0].CPULimit, w.Pods[0].MemLimit)
	}
	if got := problemKinds(st, "Deployment/shop/web"); !slices.Equal(got, []string{"oom", "probe"}) {
		t.Errorf("web problems = %v (%+v)", got, st.Problems)
	}
	if w := workload(t, st, "Deployment/shop/api"); w.State != model.KubeError || w.Pods[0].Reason != "CrashLoopBackOff" {
		t.Errorf("api = %+v", w)
	}
	var crash model.KubeProblem
	for _, p := range st.Problems {
		if p.Kind == "crashloop" {
			crash = p
		}
	}
	if crash.NodeID != "Deployment/shop/api" || crash.Restarts != 4 || !strings.Contains(crash.Message, "last exit code 1 (application error)") {
		t.Errorf("crash problem = %+v", crash)
	}
	if w := workload(t, st, "StatefulSet/shop/db"); w.State != model.KubeError {
		t.Errorf("db = %+v", w)
	}
	for _, p := range st.Problems {
		if p.Kind == "image-pull" && !strings.Contains(p.Fix, "builds") {
			t.Errorf("local image fix = %q", p.Fix)
		}
	}
	if got := problemKinds(st, "DaemonSet/shop/agent"); !slices.Equal(got, []string{"unschedulable"}) || workload(t, st, "DaemonSet/shop/agent").State != model.KubeError {
		t.Errorf("agent problems %v", got)
	}
	if w := workload(t, st, "Job/shop/migrate"); w.State != model.KubeStopped || w.Rollout != "completed (1 of 1)" {
		t.Errorf("migrate = %+v", w)
	}
	if w := workload(t, st, "Job/shop/seed"); w.State != model.KubeError || !slices.Equal(problemKinds(st, "Job/shop/seed"), []string{"exit", "job-failed"}) {
		t.Errorf("seed = %+v / %v", w, problemKinds(st, "Job/shop/seed"))
	}
	if w := workload(t, st, "CronJob/shop/nightly"); w.State != model.KubeRunning || len(w.Pods) != 1 || w.LastSchedule == nil {
		t.Errorf("nightly = %+v", w)
	}
	if slices.ContainsFunc(st.Workloads, func(w model.KubeWorkloadStatus) bool { return w.NodeID == "Job/shop/nightly-1" }) {
		t.Error("a CronJob's Job is listed as its own workload")
	}
	if w := workload(t, st, "Pod/shop/debug"); w.State != model.KubeRunning {
		t.Errorf("bare pod = %+v", w)
	}
	if w := workload(t, st, "Deployment/shop/idle"); w.State != model.KubeStopped || w.Rollout != "scaled to 0" {
		t.Errorf("idle = %+v", w)
	}
	if st.State != model.KubeError {
		t.Errorf("overall = %s", st.State)
	}
	// Every event stays in the history, newest first; only the current probe failure is a problem.
	if len(st.Events) != 3 || st.Events[0].NodeID != "Deployment/shop/web" || st.Events[0].Count != 3 {
		t.Errorf("events = %+v", st.Events)
	}
	if st.Problems[0].Severity != "error" || st.Problems[len(st.Problems)-1].Severity != "warning" {
		t.Errorf("problems not errors first: %+v", st.Problems)
	}
	// Running pods feed the sampler, keyed by UID.
	if ci, ok := v.pods["shop-web-7d9-a"]; !ok || ci.service != "Deployment/shop/web" || ci.cpuLimit != 0.25 {
		t.Errorf("sampler pods = %+v", v.pods)
	}
}

func TestWorkloadStateRollout(t *testing.T) {
	cases := []struct {
		name string
		d    *appsv1.Deployment
		want model.KubeState
		text string
	}{
		{"complete", deployment("a", "d", 3, 3, 3, 3), model.KubeRunning, "3 of 3 ready"},
		{"updating", deployment("a", "d", 3, 3, 1, 3), model.KubeStarting, "rolling out: 1 of 3 pods updated, 3 ready"},
		{"waiting", deployment("a", "d", 3, 1, 3, 1), model.KubeStarting, "waiting for pods: 1 of 3 ready"},
	}
	for _, c := range cases {
		st := buildKubeView(kubeSnapshot{deployments: []*appsv1.Deployment{c.d}, now: t0}).status
		if w := st.Workloads[0]; w.State != c.want || w.Rollout != c.text {
			t.Errorf("%s: %s %q", c.name, w.State, w.Rollout)
		}
	}
	// An old generation is not rolled out yet.
	d := deployment("a", "d", 1, 1, 1, 1)
	d.Generation = 2
	if st := buildKubeView(kubeSnapshot{deployments: []*appsv1.Deployment{d}}).status; st.Workloads[0].State != model.KubeStarting {
		t.Errorf("stale generation = %s", st.Workloads[0].State)
	}
}

func TestBuildKubeViewNetwork(t *testing.T) {
	rs := &appsv1.ReplicaSet{ObjectMeta: objectMeta("default", "web-1", ownedBy("Deployment", "web"))}
	pod := readyPod("default", "web-1-a", ownedBy("ReplicaSet", "web-1"), map[string]string{"app": "web"})
	dbPod := readyPod("default", "db-0", ownedBy("StatefulSet", "db"), map[string]string{"app": "db"})
	svc := &corev1.Service{ObjectMeta: objectMeta("default", "web"), Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "web"}, Ports: []corev1.ServicePort{
		{Name: "http", Port: 80, TargetPort: intstr.FromString("http"), Protocol: corev1.ProtocolTCP},
		{Name: "metrics", Port: 9090, TargetPort: intstr.FromInt32(9090)},
		{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP},
	}}}
	dbSvc := &corev1.Service{ObjectMeta: objectMeta("default", "db"), Spec: corev1.ServiceSpec{Selector: map[string]string{"app": "db"}, Ports: []corev1.ServicePort{{Port: 5432}}}}
	api := &corev1.Service{ObjectMeta: objectMeta("default", "kubernetes"), Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "https", Port: 443}}}}
	ext := &corev1.Service{ObjectMeta: objectMeta("default", "ext"), Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "example.com"}}
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "web-abc", Labels: map[string]string{discoveryv1.LabelServiceName: "web"}},
		Ports:     []discoveryv1.EndpointPort{{Name: new("http"), Port: new(int32(8080))}, {Name: new("metrics"), Port: new(int32(9090))}},
		Endpoints: []discoveryv1.Endpoint{{Conditions: discoveryv1.EndpointConditions{Ready: new(true)}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "web-1-a"}}},
	}
	ing := &networkingv1.Ingress{ObjectMeta: objectMeta("default", "web"), Spec: networkingv1.IngressSpec{
		IngressClassName: new("nginx"),
		Rules: []networkingv1.IngressRule{
			{Host: "web.shop.localhost", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Path: "/api", Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "web", Port: networkingv1.ServiceBackendPort{Name: "http"}}}},
			}}}},
			{IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
				{Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "web", Port: networkingv1.ServiceBackendPort{Number: 80}}}},
			}}}},
		},
	}}
	v := buildKubeView(kubeSnapshot{
		cluster:     model.KubeClusterStatus{IngressPort: 61080},
		replicaSets: []*appsv1.ReplicaSet{rs}, statefulSets: []*appsv1.StatefulSet{{ObjectMeta: objectMeta("default", "db")}},
		deployments: []*appsv1.Deployment{deployment("default", "web", 1, 1, 1, 1)},
		pods:        []*corev1.Pod{pod, dbPod}, services: []*corev1.Service{svc, dbSvc, api, ext},
		slices: []*discoveryv1.EndpointSlice{slice}, ingresses: []*networkingv1.Ingress{ing},
	})
	var keys []string
	for _, f := range v.forwards {
		keys = append(keys, f.key)
	}
	if !slices.Equal(keys, []string{"default/db/5432", "default/web/80", "default/web/9090"}) {
		t.Fatalf("forwards = %v", keys)
	}
	if f := v.forwards[1]; !f.http || !f.ready || f.portName != "http" || f.nodeID != "Service/default/web" {
		t.Errorf("web forward = %+v", f)
	}
	if f := v.forwards[0]; f.http || f.ready {
		t.Errorf("db forward (no slice, postgres port) = %+v", f)
	}
	if !slices.Equal(v.links["Service/default/web"], []string{"Deployment/default/web"}) || !slices.Equal(v.links["Service/default/db"], []string{"StatefulSet/default/db"}) {
		t.Errorf("links = %v", v.links)
	}
	if pod, port, ok := readyEndpoint([]*discoveryv1.EndpointSlice{slice}, "http", 0); !ok || pod != "web-1-a" || port != 8080 {
		t.Errorf("ready endpoint = %s %d %v", pod, port, ok)
	}
	// Successive connections rotate over the ready pods (sorted), skipping unready ones.
	three := &discoveryv1.EndpointSlice{Ports: slice.Ports, Endpoints: []discoveryv1.Endpoint{
		{Conditions: discoveryv1.EndpointConditions{Ready: new(true)}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "web-1-c"}},
		{Conditions: discoveryv1.EndpointConditions{Ready: new(false)}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "web-1-x"}},
		{Conditions: discoveryv1.EndpointConditions{Ready: new(true)}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "web-1-a"}},
		{Conditions: discoveryv1.EndpointConditions{Ready: new(true)}, TargetRef: &corev1.ObjectReference{Kind: "Pod", Name: "web-1-b"}},
	}}
	var picked []string
	for n := range uint64(4) {
		pod, _, _ := readyEndpoint([]*discoveryv1.EndpointSlice{three}, "http", n)
		picked = append(picked, pod)
	}
	if !slices.Equal(picked, []string{"web-1-a", "web-1-b", "web-1-c", "web-1-a"}) {
		t.Errorf("rotation = %v", picked)
	}
	us := v.status.Ingresses
	if len(us) != 2 || us[0].URL != "http://web.shop.localhost:61080/api" || us[0].Address != "http://127.0.0.1:61080/api" || us[0].Port != "http" || us[0].Class != "nginx" ||
		us[1].URL != "http://127.0.0.1:61080/" || us[1].Host != "" || us[1].ServiceNodeID != "Service/default/web" {
		t.Errorf("ingresses = %+v", us)
	}
	if !httpPort("", 8080, 8080, nil) || httpPort("", 6379, 6379, nil) || httpPort("", 80, 5432, nil) || !httpPort("", 5432, 5432, new("http")) {
		t.Error("httpPort")
	}
	// Names win over port numbers: Alertmanager's http-web on 9093 (a Kafka
	// TLS number) is HTTP; its tcp-mesh cluster port and Kafka's own are not.
	if !httpPort("http-web", 9093, 9093, nil) || httpPort("tcp-mesh", 9094, 9094, nil) || httpPort("kafka-tls", 9093, 9093, nil) || httpPort("grpc", 8080, 8080, nil) || !httpPort("web", 9090, 9090, nil) {
		t.Error("httpPort by port name")
	}
	if !userNamespace("shop") || userNamespace("kube-system") {
		t.Error("userNamespace")
	}
}
