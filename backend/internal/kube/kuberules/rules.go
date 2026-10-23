// Package kuberules checks the rendered objects of a Kubernetes workflow:
// schema validation against the OpenAPI of the pinned Kubernetes release
// (like kubeconform), wiring between objects (selectors, Ingress backends,
// references) and best practices (k8s_* diagnostics with file:line, why,
// fix and docs; the advice can be turned off per rule in Settings).
package kuberules

import (
	"slices"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Rule is one Kubernetes check (codes k8s_*); Why explains it in diagnostics.
type Rule struct {
	model.BestPracticeRule
	Why string
}

// Rule categories: errors are "kubernetes" (the API server or the deploy
// refuses them; they can't be turned off); advice is grouped for Settings.
const (
	catErrors      = "kubernetes"
	catReliability = "kubernetes-reliability" // probes, resources, replicas, images, storage
	catSecurity    = "kubernetes-security"    // privileges, host access, credentials
	catNetworking  = "kubernetes-networking"  // selectors, Services, Ingress, references, namespaces
	catHygiene     = "kubernetes-hygiene"     // labels, quotas, image digests
)

const (
	docsConcepts  = "https://kubernetes.io/docs/concepts/"
	docsProbes    = "https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/"
	docsResources = "https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/"
	docsSecCtx    = "https://kubernetes.io/docs/tasks/configure-pod-container/security-context/"
	docsPSS       = "https://kubernetes.io/docs/concepts/security/pod-security-standards/"
	docsDeprecate = "https://kubernetes.io/docs/reference/using-api/deprecation-guide/"
)

func rule(code, title, category string, sev model.Severity, description, docs, why string) Rule {
	return Rule{model.BestPracticeRule{Code: code, Title: title, Category: category, Severity: sev, Description: description, Docs: docs}, why}
}

func optIn(r Rule) Rule {
	r.DefaultOff = true
	return r
}

// rules lists every Kubernetes check. k8s_yaml, k8s_render, k8s_load,
// k8s_dependency and k8s_object are reported by the renderer (package kube);
// they are listed so every k8s_* code has a title, why and docs.
var rules = []Rule{
	rule("k8s_yaml", "YAML syntax", catErrors, model.SeverityError,
		"Manifests, kustomizations and values files must be valid YAML.", "https://kubernetes.io/docs/concepts/overview/working-with-objects/",
		"kubectl and the API server can't read a document that is not valid YAML; nothing in the file is applied."),
	rule("k8s_render", "Kustomize and Helm render", catErrors, model.SeverityError,
		"Kustomizations and Helm charts must render (resources, patches, templates and values).", "https://kubectl.docs.kubernetes.io/references/kustomize/",
		"The deploy applies the rendered objects; a kustomization or chart that fails to render deploys nothing."),
	rule("k8s_load", "Sources exist", catErrors, model.SeverityError,
		"Every source of the workflow must point at existing manifests, a kustomization or a chart.", "https://kubernetes.io/docs/concepts/overview/working-with-objects/object-management/",
		"A source whose path is missing has nothing to render or apply."),
	rule("k8s_dependency", "Helm dependencies vendored", catErrors, model.SeverityError,
		"Chart dependencies must be present in charts/ (helm dependency build).", "https://helm.sh/docs/helm/helm_dependency/",
		"Helm renders a chart with its dependencies from charts/; a missing dependency fails the install."),
	rule("k8s_object", "Objects are identified", catErrors, model.SeverityError,
		"Every document needs apiVersion, kind and metadata.name.", "https://kubernetes.io/docs/concepts/overview/working-with-objects/#required-fields",
		"The API server refuses an object without apiVersion, kind or a name."),
	rule("k8s_schema", "Kubernetes API schema", catErrors, model.SeverityError,
		"Objects must follow the Kubernetes "+SchemaVersion+" API: known kinds, known fields, right types and required fields.", "https://kubernetes.io/docs/reference/kubernetes-api/",
		"kubectl apply validates strictly: an unknown field, a number where a string is expected (an unquoted env value) or a missing required field is refused, and the whole apply stops."),
	rule("k8s_removed_api", "Removed API versions", catErrors, model.SeverityError,
		"apiVersions removed from Kubernetes must be migrated to their replacement.", docsDeprecate,
		"The cluster no longer serves the removed version: apply fails with \"no matches for kind\"."),
	rule("k8s_duplicate", "Unique objects", catErrors, model.SeverityError,
		"Two documents can't define the same kind, namespace and name.", "https://kubernetes.io/docs/concepts/overview/working-with-objects/names/",
		"The second document silently overwrites the first one on apply: only one of them ever exists in the cluster."),
	rule("k8s_workload_selector", "Workload selector matches its pods", catErrors, model.SeverityError,
		"spec.selector of a Deployment, StatefulSet, DaemonSet, ReplicaSet or Job must match spec.template.metadata.labels.", docsConcepts+"workloads/controllers/deployment/#selector",
		"The API server refuses a workload whose selector does not select its own pod template (\"selector does not match template labels\")."),

	// Reliability
	rule("k8s_deprecated_api", "Deprecated API versions", catReliability, model.SeverityWarning,
		"apiVersions deprecated in Kubernetes should be migrated before they are removed.", docsDeprecate,
		"A deprecated version still works today but is removed in a later release: the same manifests stop applying after a cluster upgrade."),
	rule("k8s_no_requests", "Resource requests", catReliability, model.SeverityWarning,
		"Containers should request cpu and memory (resources.requests).", docsResources,
		"Without requests the scheduler packs pods blindly, the pod gets the BestEffort QoS class (evicted first under memory pressure) and HorizontalPodAutoscalers can't compute utilization."),
	rule("k8s_no_memory_limit", "Memory limits", catReliability, model.SeverityInfo,
		"Containers should set a memory limit (resources.limits.memory).", docsResources,
		"Without a memory limit a leaking container grows until the node runs out of memory and the kubelet evicts other pods; with one, only that container is OOM-killed and restarted."),
	rule("k8s_no_readiness_probe", "Readiness probes", catReliability, model.SeverityWarning,
		"Containers that serve ports should have a readinessProbe.", docsProbes,
		"Without a readiness probe a pod receives traffic as soon as its process starts: during startup and rollouts clients get connection errors, and a hung pod is never taken out of the Service."),
	rule("k8s_no_liveness_probe", "Liveness probes", catReliability, model.SeverityInfo,
		"Long-running containers that serve ports should have a livenessProbe.", docsProbes,
		"Without a liveness probe a deadlocked process keeps running (and failing requests) until someone restarts it."),
	rule("k8s_probe_port", "Probes target declared ports", catReliability, model.SeverityWarning,
		"A probe naming a port must name one of the container's ports.", docsProbes,
		"A probe on an unknown named port always fails: the pod never becomes ready (readiness) or restarts forever (liveness)."),
	rule("k8s_latest_tag", "Pinned image tags", catReliability, model.SeverityWarning,
		"Images should name a version tag (or digest) instead of latest or no tag.", docsConcepts+"containers/images/#image-names",
		"latest changes whenever the publisher pushes: nodes run different versions of the same Deployment, a rollback restarts the new image, and the pull policy defaults to Always (every start pulls)."),
	rule("k8s_local_image", "Locally built images are not pulled", catReliability, model.SeverityWarning,
		"Images built from the workflow's Dockerfiles should use a fixed tag (e.g. :dev) and imagePullPolicy IfNotPresent.", docsConcepts+"containers/images/#image-pull-policy",
		"Local images are loaded straight into the cluster's nodes: with :latest or imagePullPolicy Always the kubelet tries to pull them from a registry and the pod stays in ErrImagePull."),
	rule("k8s_single_replica", "Redundancy in production", catReliability, model.SeverityWarning,
		"In production-like environments, stateless Deployments and StatefulSets should run 2+ replicas with a PodDisruptionBudget.", docsConcepts+"workloads/pods/disruptions/",
		"One replica means downtime on every rollout, node drain or crash; without a PodDisruptionBudget a node drain may evict every replica at once."),
	rule("k8s_hpa_no_requests", "Autoscaling needs requests", catReliability, model.SeverityWarning,
		"A HorizontalPodAutoscaler scaling on cpu or memory needs requests for that resource in every container of its target.", "https://kubernetes.io/docs/tasks/run-application/horizontal-pod-autoscale/",
		"Utilization is usage divided by request: without the request the HPA reports \"missing request for cpu\" and never scales."),
	rule("k8s_pvc_storage_class", "Explicit storage classes", catReliability, model.SeverityInfo,
		"PersistentVolumeClaims should name their storageClassName.", docsConcepts+"storage/persistent-volumes/#class-1",
		"Without one the claim uses the cluster's default class: fine locally (k3s local-path), but a cluster without a default class leaves it Pending forever and production clusters often default to a slow or non-replicated disk."),
	rule("k8s_topology_spread", "Replicas spread over nodes", catReliability, model.SeverityWarning,
		"In production-like environments, Deployments and StatefulSets with 2+ replicas should spread them over nodes or zones (topologySpreadConstraints or podAntiAffinity on kubernetes.io/hostname or topology.kubernetes.io/zone).", docsConcepts+"scheduling-eviction/topology-spread-constraints/",
		"The scheduler may put every replica on the same node: one node failure, drain or upgrade then takes the whole service down even though it runs several replicas."),
	rule("k8s_pdb", "Disruption budgets allow evictions", catReliability, model.SeverityWarning,
		"A PodDisruptionBudget should leave room for one eviction (not maxUnavailable: 0, minAvailable: 100% or minAvailable ≥ replicas) and each pod should be covered by one budget only.", "https://kubernetes.io/docs/tasks/run-application/configure-pdb/",
		"A budget that allows no eviction makes kubectl drain and cluster upgrades hang until someone deletes it; a pod selected by two budgets can't be evicted at all (the eviction API refuses it)."),
	rule("k8s_rollout_strategy", "Rollouts keep serving", catReliability, model.SeverityWarning,
		"In production-like environments, Deployments with 2+ replicas should roll out with RollingUpdate keeping pods available (not Recreate or maxUnavailable: 100%); maxSurge and maxUnavailable can't both be 0.", docsConcepts+"workloads/controllers/deployment/#strategy",
		"Recreate (or a rolling update allowed to remove every pod) stops all replicas before the new ones are ready: every deploy is an outage. maxUnavailable: 0 with maxSurge: 1 starts each new pod before removing an old one."),
	rule("k8s_recommended_labels", "Recommended labels", catHygiene, model.SeverityInfo,
		"Workloads should carry the recommended labels app.kubernetes.io/name and app.kubernetes.io/instance (plus part-of and version).", docsConcepts+"overview/working-with-objects/common-labels/",
		"Dashboards, kubectl selectors, Helm, Argo CD and cost tools group objects by these labels; without them you can't tell which app or release a pod belongs to during an incident."),
	optIn(rule("k8s_resource_quota", "Namespace quotas", catHygiene, model.SeverityInfo,
		"In production-like environments, every namespace with workloads should have a ResourceQuota and a LimitRange.", docsConcepts+"policy/resource-quotas/",
		"Without a quota one runaway deploy or HPA can take the whole cluster's capacity; without a LimitRange a container that forgets requests gets none and is packed blindly.")),
	optIn(rule("k8s_image_digest", "Images pinned by digest", catHygiene, model.SeverityInfo,
		"In production-like environments, images should be pinned by digest (image@sha256:…), not only by tag.", docsConcepts+"containers/images/#image-names",
		"A tag can be moved to other content: two nodes pulling the same tag at different times may run different code. A digest always names the same image.")),

	// Networking and wiring
	rule("k8s_selector_mismatch", "Selectors match pods", catNetworking, model.SeverityWarning,
		"Service and PodDisruptionBudget selectors should match the pod labels of a workload in the workflow.", docsConcepts+"services-networking/service/#defining-a-service",
		"A Service selecting no pods has no endpoints: every connection is refused. A PodDisruptionBudget selecting no pods protects nothing."),
	rule("k8s_service_port", "Service target ports exist", catNetworking, model.SeverityWarning,
		"A named targetPort must be a port name declared by the selected containers.", docsConcepts+"services-networking/service/#field-spec-ports",
		"A named targetPort that no selected container declares gets no endpoints: the Service refuses connections on that port."),
	rule("k8s_ingress_backend", "Ingress backends exist", catNetworking, model.SeverityWarning,
		"Ingress and HTTPRoute backends must name a Service of the workflow and one of its ports.", docsConcepts+"services-networking/ingress/",
		"A backend pointing at a missing Service or port answers 503 (or 404) for every request on that route."),
	rule("k8s_unknown_ref", "References exist", catNetworking, model.SeverityWarning,
		"ConfigMaps, Secrets, PersistentVolumeClaims and ServiceAccounts used by pods, and HPA/PDB targets, should be defined in the workflow.", docsConcepts+"configuration/configmap/",
		"A pod referencing a missing ConfigMap or Secret stays in CreateContainerConfigError, a missing claim keeps it Pending, a missing ServiceAccount stops the ReplicaSet from creating pods."),
	rule("k8s_namespace_missing", "Namespaces exist", catNetworking, model.SeverityInfo,
		"Objects placed in a namespace should have that Namespace in the workflow (or be in a source's namespace).", docsConcepts+"overview/working-with-objects/namespaces/",
		"Nimbo's local deploy creates missing namespaces, but kubectl apply, Helm or Argo CD on another cluster fail with \"namespaces not found\" unless the Namespace is part of the manifests."),
	optIn(rule("k8s_default_namespace", "Explicit namespaces", catNetworking, model.SeverityInfo,
		"Namespaced objects should set a namespace (in the object, the kustomization or the source) instead of landing in default.", docsConcepts+"overview/working-with-objects/namespaces/",
		"Everything in default shares quotas, RBAC and NetworkPolicies with whatever else lands there; a namespace per app keeps them apart and makes cleanup one command.")),
	optIn(rule("k8s_network_policy", "NetworkPolicies", catNetworking, model.SeverityInfo,
		"Every workload's pods should be selected by a NetworkPolicy.", docsConcepts+"services-networking/network-policies/",
		"Pods accept traffic from every pod of the cluster by default: one compromised pod reaches every database. A NetworkPolicy limits who can connect.")),

	// Security
	rule("k8s_secret_env", "Secrets written in env", catSecurity, model.SeverityWarning,
		"Passwords, tokens and keys should come from a Secret (valueFrom.secretKeyRef or envFrom), not a literal env value.", docsConcepts+"configuration/secret/#using-secrets-as-environment-variables",
		"Manifests are committed: a literal credential ends up in the repository history and in every rendered object anyone with read access to the Deployment can see."),
	rule("k8s_secret_in_configmap", "Secrets in ConfigMaps", catSecurity, model.SeverityWarning,
		"Passwords, tokens and keys belong in a Secret, not a ConfigMap.", docsConcepts+"configuration/secret/",
		"ConfigMaps are readable by every role that can read configuration and are never encrypted at rest; Secrets get their own RBAC and can be encrypted."),
	rule("k8s_privileged", "Privileged containers", catSecurity, model.SeverityWarning,
		"Avoid privileged: true, allowPrivilegeEscalation: true and adding ALL, SYS_ADMIN or NET_ADMIN capabilities.", docsSecCtx,
		"A privileged container has every device and capability of the node: escaping it is trivial and gives root on the node."),
	rule("k8s_host_namespaces", "Host namespaces", catSecurity, model.SeverityWarning,
		"Pods should not set hostNetwork, hostPID or hostIPC.", docsPSS+"#baseline",
		"They switch off the pod's isolation: host networking binds node ports and sees node traffic, host PID sees (and signals) every process of the node."),
	rule("k8s_host_path", "hostPath volumes", catSecurity, model.SeverityWarning,
		"Pods should not mount hostPath volumes (use a PersistentVolumeClaim, emptyDir or a ConfigMap).", docsConcepts+"storage/volumes/#hostpath",
		"A hostPath mount exposes the node's filesystem to the pod (credentials, the container runtime socket) and ties the data to one node."),
	rule("k8s_run_as_root", "Containers run as non-root", catSecurity, model.SeverityInfo,
		"Pods should set securityContext.runAsNonRoot: true (or a non-zero runAsUser).", docsSecCtx,
		"A process running as root in the container is root over everything mounted into it and one kernel bug away from root on the node."),
	optIn(rule("k8s_hardening", "Restricted pod security", catSecurity, model.SeverityInfo,
		"Containers should set allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities.drop [ALL] and seccompProfile RuntimeDefault; pods that don't call the API should set automountServiceAccountToken: false.", docsPSS+"#restricted",
		"These are the restricted Pod Security Standard: they limit what an attacker can do after compromising the app (no setuid, no writes outside volumes, no capabilities, no API token).")),
}

var ruleByCode = func() map[string]Rule {
	m := map[string]Rule{}
	for _, r := range rules {
		m[r.Code] = r
	}
	return m
}()

// Rules lists the Kubernetes checks (GET /best-practices/rules lists the
// warning/info ones).
func Rules() []model.BestPracticeRule {
	out := make([]model.BestPracticeRule, len(rules))
	for i, r := range rules {
		out[i] = r.BestPracticeRule
	}
	return out
}

// RuleWhy is why a Kubernetes rule matters ("" for unknown codes).
func RuleWhy(code string) string { return ruleByCode[code].Why }

// FilterSettings drops warning/info findings of rules turned off in
// Settings, and of opt-in rules not turned on. Errors always stay.
func FilterSettings(diags []model.Diagnostic, cfg model.BestPracticeSettings) []model.Diagnostic {
	return slices.DeleteFunc(diags, func(d model.Diagnostic) bool {
		if d.Severity == model.SeverityError {
			return false
		}
		if cfg.Disabled || slices.Contains(cfg.DisabledRules, d.Code) {
			return true
		}
		return ruleByCode[d.Code].DefaultOff && !slices.Contains(cfg.EnabledRules, d.Code)
	})
}
