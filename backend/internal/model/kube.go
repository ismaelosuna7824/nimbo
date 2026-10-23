package model

import "strings"

// Kubernetes workflows: manifests, Kustomize trees and Helm charts (plus
// the Dockerfiles of the images built locally) stored as the workflow's
// files. The files are the source of truth; Kubernetes.Graph is derived
// from the RENDERED objects of the active environment on every save, like
// Compose.Graph for docker workflows.

// WorkflowKubernetes is Workflow.Kind of a Kubernetes workflow.
const WorkflowKubernetes = "kubernetes"

// IsKubernetes reports whether w is a Kubernetes workflow.
func (w *Workflow) IsKubernetes() bool { return w.Kind == WorkflowKubernetes }

// IsTerraform reports whether w is a Terraform workflow (Kind "").
func (w *Workflow) IsTerraform() bool { return w.Kind == "" }

// KindLabel names a workflow kind for messages ("Terraform", "docker",
// "Kubernetes").
func KindLabel(kind string) string {
	switch kind {
	case "":
		return "Terraform"
	case WorkflowKubernetes:
		return "Kubernetes"
	}
	return kind
}

// KubeClusterName is the local cluster a Kubernetes workflow runs on.
func KubeClusterName(id string) string { return "nimbo-k8s-" + id }

// Source kinds (KubeSource.Kind).
const (
	KubeManifests = "manifests"
	KubeKustomize = "kustomize"
	KubeHelm      = "helm"
)

// KubernetesModel is the deployment description of a Kubernetes workflow.
type KubernetesModel struct {
	// Sources are what is deployed, in apply order.
	Sources []KubeSource `json:"sources"`
	// Builds are the images built from the workflow's Dockerfiles and
	// loaded into the local cluster before `up`.
	Builds []KubeBuild `json:"builds,omitempty"`

	// Derived on every save (read-only for clients).
	// Environment is the environment the graph was rendered for
	// (Workflow.ActiveEnvironment when some source maps it, else "").
	Environment string `json:"environment,omitempty"`
	// Files are the workflow files rendering read (manifests,
	// kustomizations, patches, chart files), sorted.
	Files []string `json:"files,omitempty"`
	// Dockerfiles are the Dockerfiles of Builds and of folders whose name
	// matches a rendered image repository.
	Dockerfiles []string  `json:"dockerfiles,omitempty"`
	Graph       KubeGraph `json:"graph"`
}

// KubeSource is one deployable unit of a Kubernetes workflow.
type KubeSource struct {
	Kind string `json:"kind"` // KubeManifests | KubeKustomize | KubeHelm
	// Path is a slash path relative to the workflow files: a manifest
	// file or directory (its own *.yaml/*.yml/*.json files, sorted, like
	// `kubectl apply -f <dir>`), the
	// kustomization directory rendered without an environment (usually
	// "base"), or the chart directory.
	Path string `json:"path"`
	// Release is the Helm release name (default: the chart name).
	Release string `json:"release,omitempty"`
	// Namespace objects without one go to (default "default").
	Namespace string `json:"namespace,omitempty"`
	// ValuesFiles are Helm values files (relative to the workflow files)
	// layered on the chart's values.yaml for every environment.
	ValuesFiles []string `json:"valuesFiles,omitempty"`
	// Environments maps an environment name to what it renders instead
	// of / on top of Path: the Kustomize overlay directory, or a Helm
	// values file layered after ValuesFiles. Optional: conventions fill
	// EnvPaths (overlays/<env>, values-<env>.yaml, values.<env>.yaml).
	Environments map[string]string `json:"environments,omitempty"`

	// Derived (read-only).
	// EnvPaths are Environments plus the conventional overlays/values
	// files found, by environment name.
	EnvPaths map[string]string `json:"envPaths,omitempty"`
	// Chart describes a Helm source's chart.
	Chart *KubeChart `json:"chart,omitempty"`
	// Objects counts the objects rendered for the graph's environment.
	Objects int `json:"objects"`
	// Error is the render error of the source (also a diagnostic).
	Error string `json:"error,omitempty"`
}

// KubeChart describes a Helm chart.
type KubeChart struct {
	Name         string                `json:"name"`
	Version      string                `json:"version"`
	AppVersion   string                `json:"appVersion,omitempty"`
	Description  string                `json:"description,omitempty"`
	Icon         string                `json:"icon,omitempty"`
	Dependencies []KubeChartDependency `json:"dependencies,omitempty"`
}

// KubeChartDependency is one Chart.yaml dependency.
type KubeChartDependency struct {
	Name       string `json:"name"`
	Version    string `json:"version,omitempty"`
	Repository string `json:"repository,omitempty"`
	Alias      string `json:"alias,omitempty"`
	Condition  string `json:"condition,omitempty"`
	// Vendored: the dependency is in charts/ (rendered); a missing one is
	// a diagnostic.
	Vendored bool `json:"vendored"`
	Enabled  bool `json:"enabled"`
}

// KubeBuild is an image built from the workflow's files.
type KubeBuild struct {
	Image      string            `json:"image"`                // the reference manifests use ("api:dev")
	Context    string            `json:"context"`              // slash path relative to the workflow files
	Dockerfile string            `json:"dockerfile,omitempty"` // relative to Context; default Dockerfile
	Target     string            `json:"target,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// KubeGraph is the canvas of a Kubernetes workflow.
type KubeGraph struct {
	Nodes []KubeNode `json:"nodes"`
	Edges []KubeEdge `json:"edges"`
	// Charts are the third-party charts vendored in Helm sources; the
	// canvas collapses each into one release node (KubeNode.Chart).
	Charts []KubeChartGroup `json:"charts,omitempty"`
}

// KubeChartGroup is a third-party chart a Helm source renders: a
// dependency with a remote repository (https, oci or a helm repo name)
// vendored in the chart's charts/ folder, like the wrapper charts the
// palette adds. The source's own templates (a converter chart, file://
// subcharts) are not grouped.
type KubeChartGroup struct {
	// ID is "chart:<vendored subchart path>"
	// ("chart:charts/monitoring/charts/kube-prometheus-stack").
	ID         string `json:"id"`
	Source     int    `json:"source"`
	SourcePath string `json:"sourcePath"`
	// Chart, Version and AppVersion are the vendored chart's (aliased
	// dependencies keep the chart's own name).
	Chart      string `json:"chart"`
	Version    string `json:"version,omitempty"`
	AppVersion string `json:"appVersion,omitempty"`
	Repository string `json:"repository,omitempty"`
	Release    string `json:"release"`
	Namespace  string `json:"namespace"`
	// ValuesFiles are the files its values come from, in merge order
	// (the source chart's values.yaml first).
	ValuesFiles []string `json:"valuesFiles,omitempty"`
	// Objects counts the rendered objects of the chart.
	Objects int `json:"objects"`
	// Position is the collapsed node's saved canvas position.
	Position *Position `json:"position,omitempty"`
}

// Node categories (KubeNode.Category).
const (
	KubeCatWorkload    = "workload"    // Deployment, StatefulSet, DaemonSet, ReplicaSet, Pod, Job, CronJob
	KubeCatNetwork     = "network"     // Service, Ingress, IngressClass, Gateway, HTTPRoute, NetworkPolicy
	KubeCatConfig      = "config"      // ConfigMap, Secret
	KubeCatStorage     = "storage"     // PVC, PV, StorageClass
	KubeCatAutoscaling = "autoscaling" // HPA, PDB
	KubeCatRBAC        = "rbac"        // ServiceAccount, Role, ClusterRole, bindings
	KubeCatCluster     = "cluster"     // Namespace, CRD, webhooks, priority classes, ...
	KubeCatCustom      = "custom"      // custom resources
)

// KubeNodeID is the stable graph node ID of an object:
// "<Kind>/<namespace>/<name>" for namespaced objects, "<Kind>/<name>" for
// cluster-scoped ones. Kind is "<Kind>.<group>" for kinds outside the
// built-in API groups ("Certificate.cert-manager.io/default/web").
func KubeNodeID(kind, namespace, name string) string {
	if namespace == "" {
		return kind + "/" + name
	}
	return kind + "/" + namespace + "/" + name
}

// ParseKubeNodeID splits a KubeNodeID.
func ParseKubeNodeID(id string) (kind, namespace, name string, ok bool) {
	parts := strings.Split(id, "/")
	switch len(parts) {
	case 2:
		return parts[0], "", parts[1], parts[0] != "" && parts[1] != ""
	case 3:
		return parts[0], parts[1], parts[2], parts[0] != "" && parts[1] != "" && parts[2] != ""
	}
	return "", "", "", false
}

// KubeNode is one rendered object.
type KubeNode struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"` // Kubernetes kind ("Deployment")
	APIVersion string `json:"apiVersion"`
	Name       string `json:"name"`
	// Namespace is the effective namespace ("" for cluster-scoped kinds).
	Namespace string `json:"namespace,omitempty"`
	Category  string `json:"category"`
	// Position is the saved canvas position; nil = auto layout.
	Position *Position `json:"position,omitempty"`
	// Source indexes Kubernetes.Sources; SourceKind/SourcePath repeat it.
	Source     int    `json:"source"`
	SourceKind string `json:"sourceKind"`
	SourcePath string `json:"sourcePath"`
	// File and Line locate the declaration (1-based): the manifest
	// document, the Kustomize resource, or the Helm template that
	// produced it.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`
	// Editable: canvas/inspector edits can be written back (plain
	// manifests and Kustomize resources; Helm objects field by field,
	// see GET .../kubernetes/fields).
	Editable bool              `json:"editable"`
	Labels   map[string]string `json:"labels,omitempty"`
	// Stack are icon hints from the images (repositories, without tags),
	// for techIconFor.
	Stack      []string          `json:"stack,omitempty"`
	Workload   *KubeWorkload     `json:"workload,omitempty"`
	Service    *KubeServiceFacts `json:"service,omitempty"`
	Routes     []KubeRoute       `json:"routes,omitempty"` // Ingress / HTTPRoute
	Scaler     *KubeScaler       `json:"scaler,omitempty"` // HPA
	Disruption *KubeDisruption   `json:"disruption,omitempty"`
	Storage    *KubeStorage      `json:"storage,omitempty"` // PVC, PV, StorageClass
	// Data are the keys of a ConfigMap/Secret (values never sent).
	Data []string `json:"data,omitempty"`
	// Custom: a custom resource (its CRD may be absent).
	Custom bool `json:"custom,omitempty"`
	// Chart is the KubeChartGroup ID of the vendored chart that renders
	// the object ("" for the workflow's own objects).
	Chart string `json:"chart,omitempty"`
}

// KubeWorkload describes a workload's pod template.
type KubeWorkload struct {
	Replicas       *int              `json:"replicas,omitempty"`
	Selector       map[string]string `json:"selector,omitempty"`  // matchLabels
	PodLabels      map[string]string `json:"podLabels,omitempty"` // template labels
	ServiceAccount string            `json:"serviceAccount,omitempty"`
	Schedule       string            `json:"schedule,omitempty"`    // CronJob
	ServiceName    string            `json:"serviceName,omitempty"` // StatefulSet governing Service
	Containers     []KubeContainer   `json:"containers"`
	InitContainers []KubeContainer   `json:"initContainers,omitempty"`
	Volumes        []KubeVolume      `json:"volumes,omitempty"`
	// VolumeClaimTemplates are a StatefulSet's claim templates.
	VolumeClaimTemplates []KubeClaimTemplate `json:"volumeClaimTemplates,omitempty"`
}

// KubeContainer is one container of a pod template.
type KubeContainer struct {
	Name         string              `json:"name"`
	Image        string              `json:"image"`
	ImageRef     *ImageRef           `json:"imageRef,omitempty"`
	PullPolicy   string              `json:"pullPolicy,omitempty"`
	Command      []string            `json:"command,omitempty"`
	Args         []string            `json:"args,omitempty"`
	Ports        []KubeContainerPort `json:"ports"`
	Probes       KubeProbes          `json:"probes"`
	Resources    KubeResources       `json:"resources"`
	Env          []KubeEnv           `json:"env"`
	EnvFrom      []KubeEnvFrom       `json:"envFrom"`
	VolumeMounts []KubeVolumeMount   `json:"volumeMounts"`
}

// KubeContainerPort is a container port.
type KubeContainerPort struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol,omitempty"`
}

// KubeProbes are a container's probes (nil = none).
type KubeProbes struct {
	Liveness  *KubeProbe `json:"liveness,omitempty"`
	Readiness *KubeProbe `json:"readiness,omitempty"`
	Startup   *KubeProbe `json:"startup,omitempty"`
}

// KubeProbe is one probe.
type KubeProbe struct {
	Type                string `json:"type"` // http | tcp | exec | grpc
	Path                string `json:"path,omitempty"`
	Port                string `json:"port,omitempty"` // number or port name
	Command             string `json:"command,omitempty"`
	InitialDelaySeconds int    `json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int    `json:"periodSeconds,omitempty"`
	TimeoutSeconds      int    `json:"timeoutSeconds,omitempty"`
	FailureThreshold    int    `json:"failureThreshold,omitempty"`
}

// KubeResources are a container's requests and limits.
type KubeResources struct {
	Requests KubeQuantities `json:"requests"`
	Limits   KubeQuantities `json:"limits"`
}

// KubeQuantities are cpu/memory quantities as written ("250m", "512Mi").
type KubeQuantities struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
}

// KubeEnv is one env entry of a container.
type KubeEnv struct {
	Name string `json:"name"`
	// Value is nil for secret-looking names (Secret set) and valueFrom.
	Value  *string  `json:"value,omitempty"`
	Secret bool     `json:"secret,omitempty"`
	From   *KubeRef `json:"from,omitempty"`
}

// KubeRef is a valueFrom reference: Kind ConfigMap | Secret (Name, Key),
// Field (Key = fieldPath) or Resource (Key = resource).
type KubeRef struct {
	Kind string `json:"kind"`
	Name string `json:"name,omitempty"`
	Key  string `json:"key,omitempty"`
}

// KubeEnvFrom is one envFrom entry.
type KubeEnvFrom struct {
	Kind   string `json:"kind"` // ConfigMap | Secret
	Name   string `json:"name"`
	Prefix string `json:"prefix,omitempty"`
}

// KubeVolumeMount is one volumeMounts entry.
type KubeVolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SubPath   string `json:"subPath,omitempty"`
	ReadOnly  bool   `json:"readOnly,omitempty"`
}

// KubeVolume is one pod volume. Kind: configMap | secret |
// persistentVolumeClaim | emptyDir | hostPath | projected | other; Source
// is the ConfigMap/Secret/claim name or host path.
type KubeVolume struct {
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	Source string `json:"source,omitempty"`
}

// KubeClaimTemplate is a StatefulSet volumeClaimTemplate.
type KubeClaimTemplate struct {
	Name         string `json:"name"`
	StorageClass string `json:"storageClass,omitempty"`
	Size         string `json:"size,omitempty"`
}

// KubeServiceFacts describe a Service.
type KubeServiceFacts struct {
	Type      string            `json:"type"` // ClusterIP | NodePort | LoadBalancer | ExternalName
	Headless  bool              `json:"headless,omitempty"`
	Ports     []KubeServicePort `json:"ports"`
	Selector  map[string]string `json:"selector,omitempty"`
	External  string            `json:"externalName,omitempty"`
	Unmatched bool              `json:"unmatched,omitempty"` // the selector selects no rendered pod template
}

// KubeServicePort is one Service port.
type KubeServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int    `json:"port"`
	TargetPort string `json:"targetPort,omitempty"` // number or container port name
	NodePort   int    `json:"nodePort,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
}

// KubeRoute is one Ingress rule path or HTTPRoute backend.
type KubeRoute struct {
	Host    string `json:"host,omitempty"`
	Path    string `json:"path,omitempty"`
	Service string `json:"service"`
	Port    string `json:"port,omitempty"` // number or name
	TLS     bool   `json:"tls,omitempty"`
	Class   string `json:"class,omitempty"` // ingressClassName / parent Gateway
}

// KubeScaler describes an HPA.
type KubeScaler struct {
	Target      string `json:"target"` // node ID of scaleTargetRef
	MinReplicas int    `json:"minReplicas"`
	MaxReplicas int    `json:"maxReplicas"`
	CPU         int    `json:"cpu,omitempty"`    // target average CPU utilization (%)
	Memory      int    `json:"memory,omitempty"` // target average memory utilization (%)
}

// KubeDisruption describes a PodDisruptionBudget.
type KubeDisruption struct {
	MinAvailable   string            `json:"minAvailable,omitempty"`
	MaxUnavailable string            `json:"maxUnavailable,omitempty"`
	Selector       map[string]string `json:"selector,omitempty"`
}

// KubeStorage describes a PVC, PV or StorageClass.
type KubeStorage struct {
	StorageClass string   `json:"storageClass,omitempty"`
	Size         string   `json:"size,omitempty"`
	AccessModes  []string `json:"accessModes,omitempty"`
	Provisioner  string   `json:"provisioner,omitempty"` // StorageClass
	Default      bool     `json:"default,omitempty"`     // default StorageClass
}

// Edge kinds (KubeEdge.Kind). Source refers to Target: the Service
// selects the workload, the workload uses the ConfigMap, the HPA scales
// the Deployment.
const (
	KubeEdgeSelects        = "selects"        // Service -> workload (label selector)
	KubeEdgeRoutes         = "routes"         // Ingress/HTTPRoute -> Service (Label = port)
	KubeEdgeEnv            = "env"            // workload -> ConfigMap/Secret (valueFrom)
	KubeEdgeEnvFrom        = "envFrom"        // workload -> ConfigMap/Secret
	KubeEdgeVolume         = "volume"         // workload -> ConfigMap/Secret (Label = mount path)
	KubeEdgePVC            = "pvc"            // workload -> PVC
	KubeEdgeStorageClass   = "storageclass"   // PVC -> StorageClass, PVC -> PV
	KubeEdgeScales         = "scales"         // HPA -> workload
	KubeEdgeDisrupts       = "disrupts"       // PDB -> workload
	KubeEdgeServiceAccount = "serviceaccount" // workload -> ServiceAccount
	KubeEdgeBinds          = "binds"          // RoleBinding -> ServiceAccount, RoleBinding -> Role
	KubeEdgeNetpol         = "netpol"         // NetworkPolicy -> workload
	KubeEdgeGoverns        = "governs"        // StatefulSet -> its headless Service (serviceName)
	KubeEdgeInferred       = "inferred"       // workload -> Service named by an env value (dashed)
)

// KubeEdge is derived from the rendered objects.
type KubeEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Label  string `json:"label,omitempty"`
	// EnvKey is the variable of an inferred edge.
	EnvKey string `json:"envKey,omitempty"`
}

// KubeField is where one field of a rendered object comes from
// (GET /workflows/{id}/kubernetes/fields?node=<id>).
type KubeField struct {
	// Path addresses the field in the object: dot-separated keys, list
	// items by name ("spec.template.spec.containers[name=web].image") or
	// index ("[0]").
	Path  string `json:"path"`
	Value any    `json:"value"`
	// Editable: a write to Path lands in File (in place). Otherwise
	// Reason says why (computed by a template expression, several values
	// keys, ...).
	Editable bool   `json:"editable"`
	Reason   string `json:"reason,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	// Via: "manifest" (the resource document), "patch" (a Kustomize
	// patch), "kustomization" (images:/replicas: transformers), "values"
	// (a Helm values key: ValuesKeys) or "template" (a literal in a Helm
	// template).
	Via string `json:"via,omitempty"`
	// ValuesKeys are the Helm values keys the field renders
	// ("image.tag"); several when the field joins them
	// ("{{ .Values.image.repository }}:{{ .Values.image.tag }}").
	ValuesKeys []string `json:"valuesKeys,omitempty"`
	// Format is how the template builds the field from ValuesKeys when it
	// adds text ("<image.registry>/<image.repository>:<image.tag>",
	// "quay.io/x:<image.tag>"): only the <key> parts can change.
	Format string `json:"format,omitempty"`
}

// KubeFields answers GET /workflows/{id}/kubernetes/fields.
type KubeFields struct {
	NodeID string      `json:"nodeId"`
	Fields []KubeField `json:"fields"`
}
