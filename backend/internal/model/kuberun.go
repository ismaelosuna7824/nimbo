package model

import "time"

// ---------------------------------------------------------------------------
// Kubernetes runtime (Kubernetes workflows): every workflow runs on its own
// local cluster, a k3s node container named KubeClusterName(id) labelled
// com.nimbo.workflow=<id>, reached through a kubeconfig in the Nimbo home
// (never the user's ~/.kube/config). Runs use RunAction up/down/restart and
// the cluster actions below; see sandbox.KubeRun.

// Kubernetes cluster run actions (up, down and restart are shared with
// docker workflows).
const (
	ActionClusterStop   RunAction = "cluster-stop"   // stop the node container, keeping everything
	ActionClusterStart  RunAction = "cluster-start"  // start it again
	ActionClusterDelete RunAction = "cluster-delete" // delete the cluster and its built images
)

// KubeAction reports whether a is a run action of Kubernetes workflows.
func KubeAction(a RunAction) bool {
	switch a {
	case ActionUp, ActionDown, ActionRestart, ActionClusterStop, ActionClusterStart, ActionClusterDelete:
		return true
	}
	return false
}

// Event types of the Kubernetes runtime.
const (
	EventKubeStatus = "kube.status" // Data: KubeStatus; on cluster/pod/rollout/event changes and after runs
	EventKubeStats  = "kube.stats"  // Data: ComposeStatsEvent (Service = workload node ID); about once a second while pods run
)

// SetupActionPullKubeImage downloads the Kubernetes node image (POST
// /setup/actions/pull-kube-image; setup.progress events like pull-images).
const SetupActionPullKubeImage = "pull-kube-image"

// KubeState summarizes the pods of a workload or of the whole cluster
// (same values as ComposeState).
type KubeState string

const (
	KubeAbsent   KubeState = "absent"   // no pods (or no cluster)
	KubeStarting KubeState = "starting" // pending, rolling out, not ready yet, restarting
	KubeRunning  KubeState = "running"  // every desired pod ready
	KubeStopped  KubeState = "stopped"  // scaled to zero, completed jobs
	KubeError    KubeState = "error"    // crash loops, image pull errors, failed jobs, OOM kills
)

// KubeClusterState is the lifecycle of a workflow's local cluster.
type KubeClusterState string

const (
	ClusterAbsent   KubeClusterState = "absent"
	ClusterStarting KubeClusterState = "starting" // node container created/started, API not ready yet
	ClusterRunning  KubeClusterState = "running"
	ClusterStopped  KubeClusterState = "stopped"
	ClusterError    KubeClusterState = "error"
)

// KubeStatus is the live state of a Kubernetes workflow's cluster and
// workloads (GET /workflows/{id}/kubernetes/status, event kube.status).
type KubeStatus struct {
	WorkflowID string            `json:"workflowId"`
	Cluster    KubeClusterStatus `json:"cluster"`
	// State is the worst workload state (absent without workloads).
	State     KubeState            `json:"state"`
	Workloads []KubeWorkloadStatus `json:"workloads"`
	// Forwards are the daemon's port-forwards to the Services, on stable
	// 127.0.0.1 ports.
	Forwards []KubeForward `json:"forwards"`
	// Ingresses are the URLs of the Ingress rules, served by the cluster's
	// ingress controller on Cluster.IngressPort.
	Ingresses []KubeIngressURL `json:"ingresses"`
	// Events are the newest Warning events (and the latest Normal ones of
	// the workloads), newest first.
	Events []KubeEvent `json:"events"`
	// Problems: OOM kills, crash loops, restarts, image pull errors,
	// unschedulable pods, failed probes and jobs.
	Problems  []KubeProblem `json:"problems"`
	Message   string        `json:"message,omitempty"`
	UpdatedAt time.Time     `json:"updatedAt"`
}

// KubeClusterStatus describes the workflow's local cluster.
type KubeClusterStatus struct {
	Name    string           `json:"name"` // nimbo-k8s-<workflow id>
	State   KubeClusterState `json:"state"`
	Image   string           `json:"image"`             // node image (rancher/k3s:<version>)
	Version string           `json:"version,omitempty"` // Kubernetes server version once reachable
	// APIPort and IngressPort are published on 127.0.0.1: the API server
	// and the ingress controller (HTTP).
	APIPort     int `json:"apiPort,omitempty"`
	IngressPort int `json:"ingressPort,omitempty"`
	// Kubeconfig is Nimbo's kubeconfig file for this cluster (context
	// Name); kubectl --kubeconfig <it> works from a terminal.
	Kubeconfig string `json:"kubeconfig,omitempty"`
	// IngressClass is the default IngressClass ("traefik"); StorageClass
	// the default StorageClass ("local-path").
	IngressClass string `json:"ingressClass,omitempty"`
	StorageClass string `json:"storageClass,omitempty"`
	Message      string `json:"message,omitempty"`
}

// KubeWorkloadStatus is one workload (Deployment, StatefulSet, DaemonSet,
// Job, CronJob, bare Pod) of the cluster with its pods. NodeID is the
// graph node ID (KubeNodeID). Desired/Ready/Updated/Available follow the
// workload's status; Jobs count Succeeded/Failed pods.
type KubeWorkloadStatus struct {
	NodeID    string    `json:"nodeId"`
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace"`
	Name      string    `json:"name"`
	State     KubeState `json:"state"`
	Desired   int       `json:"desired"`
	Ready     int       `json:"ready"`
	Updated   int       `json:"updated"`
	Available int       `json:"available"`
	Succeeded int       `json:"succeeded,omitempty"`
	Failed    int       `json:"failed,omitempty"`
	// Rollout explains the rollout in one line ("2 of 3 updated pods are
	// ready").
	Rollout string    `json:"rollout,omitempty"`
	Images  []string  `json:"images"`
	Pods    []KubePod `json:"pods"`
	// StartupMs: pod start → ready of the slowest current pod.
	StartupMs *int64 `json:"startupMs,omitempty"`
	// LastSchedule is a CronJob's last scheduled run.
	LastSchedule *time.Time `json:"lastSchedule,omitempty"`
}

// KubePod is one pod of a workload. Reason is the most telling condition
// (CrashLoopBackOff, OOMKilled, ImagePullBackOff, Unschedulable, ...).
type KubePod struct {
	Name       string             `json:"name"`
	Namespace  string             `json:"namespace"`
	Phase      string             `json:"phase"` // Pending | Running | Succeeded | Failed | Unknown
	Ready      bool               `json:"ready"`
	Restarts   int                `json:"restarts"`
	Reason     string             `json:"reason,omitempty"`
	Message    string             `json:"message,omitempty"`
	Node       string             `json:"node,omitempty"`
	IP         string             `json:"ip,omitempty"`
	StartedAt  *time.Time         `json:"startedAt,omitempty"`
	ReadyAt    *time.Time         `json:"readyAt,omitempty"`
	Containers []KubePodContainer `json:"containers"`
	// CPULimit (cores) and MemLimit (bytes) sum the containers' limits; 0
	// when a container has none.
	CPULimit float64 `json:"cpuLimit,omitempty"`
	MemLimit int64   `json:"memLimit,omitempty"`
}

// KubePodContainer is one container (or init container) of a pod.
type KubePodContainer struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Init     bool   `json:"init,omitempty"`
	Ready    bool   `json:"ready"`
	State    string `json:"state"` // waiting | running | terminated
	Reason   string `json:"reason,omitempty"`
	Message  string `json:"message,omitempty"`
	ExitCode int    `json:"exitCode,omitempty"`
	Restarts int    `json:"restarts"`
	// LastReason/LastExitCode describe the previous termination
	// (OOMKilled, Error, ...).
	LastReason   string `json:"lastReason,omitempty"`
	LastExitCode int    `json:"lastExitCode,omitempty"`
	OOMKilled    bool   `json:"oomKilled,omitempty"`
}

// KubeForward is a daemon-managed port-forward from 127.0.0.1:HostPort to
// a Service port (connections go to a ready pod behind the Service). Host
// ports are stable: the same Service port keeps its host port across runs
// and restarts while it is free. URL is set for HTTP-like ports.
type KubeForward struct {
	NodeID    string `json:"nodeId"` // Service node ID
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	PortName  string `json:"portName,omitempty"`
	Protocol  string `json:"protocol"`
	HostPort  int    `json:"hostPort"`
	URL       string `json:"url,omitempty"`
	// Ready: a ready pod backs the Service; connections fail otherwise.
	Ready   bool   `json:"ready"`
	Message string `json:"message,omitempty"`
	// Workloads are the node IDs of the workloads behind the Service.
	Workloads []string `json:"workloads,omitempty"`
}

// KubeIngressURL is one Ingress rule path reachable through the ingress
// controller. URL uses the rule's host (http://<host>:<port><path>, or
// 127.0.0.1 without host); clients that cannot resolve the host send the
// request to Address with the Host header.
type KubeIngressURL struct {
	NodeID    string `json:"nodeId"` // Ingress node ID
	Namespace string `json:"namespace"`
	Ingress   string `json:"ingress"`
	Host      string `json:"host,omitempty"`
	Path      string `json:"path"`
	Service   string `json:"service"`
	Port      string `json:"port,omitempty"` // number or name
	URL       string `json:"url"`
	Address   string `json:"address"` // http://127.0.0.1:<ingress port><path>
	// ServiceNodeID is the backend Service's node ID.
	ServiceNodeID string `json:"serviceNodeId"`
	Class         string `json:"class,omitempty"`
}

// KubeEvent is one Kubernetes event (aggregated by the API server).
type KubeEvent struct {
	Type      string    `json:"type"` // Normal | Warning
	Reason    string    `json:"reason"`
	Message   string    `json:"message"`
	NodeID    string    `json:"nodeId,omitempty"` // the involved object's node ID (workload for its pods)
	Kind      string    `json:"kind"`
	Namespace string    `json:"namespace,omitempty"`
	Name      string    `json:"name"`
	Count     int       `json:"count"`
	LastSeen  time.Time `json:"lastSeen"`
}

// KubeProblem is a pod condition worth the user's attention: kind "oom",
// "crashloop", "restarts", "image-pull", "unschedulable", "probe", "exit",
// "job-failed", "config" (missing ConfigMap/Secret, invalid image name).
type KubeProblem struct {
	NodeID    string `json:"nodeId"` // workload node ID
	Namespace string `json:"namespace"`
	Pod       string `json:"pod,omitempty"`
	Container string `json:"container,omitempty"`
	Kind      string `json:"kind"`
	Severity  string `json:"severity"` // error | warning
	Message   string `json:"message"`
	Fix       string `json:"fix,omitempty"`
	ExitCode  int    `json:"exitCode,omitempty"`
	Restarts  int    `json:"restarts,omitempty"`
}

// KubeLogLine is one line of a pod container's log.
type KubeLogLine struct {
	NodeID    string  `json:"nodeId,omitempty"` // workload node ID
	Namespace string  `json:"namespace"`
	Pod       string  `json:"pod"`
	Container string  `json:"container"`
	Line      LogLine `json:"line"`
}
