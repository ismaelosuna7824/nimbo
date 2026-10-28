package compliance

import "fmt"

// CIS Kubernetes Benchmark v2.0.1. Recommendation numbers and titles follow that release as implemented by
// kube-bench's cis-2.0 configuration (Kubernetes 1.34-1.35); cisecurity.org also lists a v2.1.0 (September 2026)
// whose recommendation list isn't public yet, so this pack moves to it once it can be verified. A design shows the
// policies of section 5 (RBAC objects, pod specs, NetworkPolicies, namespaces); the control plane, etcd and worker
// nodes (sections 1-4) are the cluster's configuration and are manual, grouped by the benchmark's own subsections.
// Every control of section 5 is Manual in the benchmark because it is about what the cluster admits: Nimbo grades
// what this design asks for, not what else runs in the cluster.

const (
	k8sControlPlane = "Control Plane Components"
	k8sEtcd         = "etcd"
	k8sCPConfig     = "Control Plane Configuration"
	k8sWorkers      = "Worker Nodes"
	k8sRBAC         = "RBAC and Service Accounts"
	k8sPSS          = "Pod Security Standards"
	k8sNetwork      = "Network Policies and CNI"
	k8sSecrets      = "Secrets Management"
	k8sAdmission    = "Extensible Admission Control"
	k8sGeneral      = "General Policies"
	k8sOnCluster    = "Cluster configuration, outside the design: run kube-bench (or your provider's CIS scan) on the cluster and attest it. On managed clusters (EKS, AKS, GKE, OKE) the provider runs the control plane: use the provider's CIS benchmark for the parts you manage. Nimbo's local k3s cluster is for development. "
	k8sRolesOnly    = " Nimbo checks the Roles and ClusterRoles of this design; roles already in the cluster aren't visible."
	k8sHostNS       = "Nimbo flags pods with %[1]s: true (the Host namespaces rule's %[1]s findings; the other host namespaces count for their own controls)."
)

var (
	k8sWorkloads = []string{"Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "ReplicationController", "Job", "CronJob"}
	k8sRoles     = []string{"Role", "ClusterRole"}
	k8sBindings  = []string{"RoleBinding", "ClusterRoleBinding"}
)

func init() {
	Register(Framework{
		ID:       "cis-kubernetes-2.0",
		Name:     "CIS Kubernetes Benchmark",
		Version:  "2.0.1",
		Platform: "kubernetes",
		Source:   "https://www.cisecurity.org/benchmark/kubernetes",
		Controls: []Control{
			// 1-4 Cluster components
			{ID: "1.1", Title: "Control plane node configuration files (1.1.1-1.1.21)", Section: k8sControlPlane, Manual: true,
				Guidance: k8sOnCluster + "The static pod manifests, the CNI files, the etcd data directory, admin.conf, scheduler.conf, controller-manager.conf and the PKI directory must be owned by root with the restrictive modes the benchmark gives."},
			{ID: "1.2", Title: "API Server (1.2.1-1.2.30)", Section: k8sControlPlane, Manual: true,
				Guidance: k8sOnCluster + "No anonymous auth or static token file, kubelet client certificates, authorization mode Node,RBAC, the admission plugins the benchmark lists, audit logging, request timeouts, service account key and lookup, etcd and TLS certificates, strong ciphers and encryption at rest."},
			{ID: "1.3", Title: "Controller Manager (1.3.1-1.3.7)", Section: k8sControlPlane, Manual: true,
				Guidance: k8sOnCluster + "terminated-pod-gc-threshold, no profiling, use-service-account-credentials, service account private key and root CA, kubelet server certificate rotation, bind address 127.0.0.1."},
			{ID: "1.4", Title: "Scheduler (1.4.1-1.4.2)", Section: k8sControlPlane, Manual: true,
				Guidance: k8sOnCluster + "No profiling, bind address 127.0.0.1."},
			{ID: "2", Title: "etcd node configuration (2.1-2.7)", Section: k8sEtcd, Manual: true,
				Guidance: k8sOnCluster + "TLS for clients and peers with client certificate auth, no auto-generated certificates, and a CA of its own."},
			{ID: "3.1", Title: "Authentication and authorization (3.1.1-3.1.3)", Section: k8sCPConfig, Manual: true,
				Guidance: k8sOnCluster + "People authenticate through an identity provider (OIDC), not client certificates, service account tokens or bootstrap tokens."},
			{ID: "3.2", Title: "Logging (3.2.1-3.2.2)", Section: k8sCPConfig, Manual: true,
				Guidance: k8sOnCluster + "An audit policy that covers the security-relevant requests."},
			{ID: "4.1", Title: "Worker node configuration files (4.1.1-4.1.10)", Section: k8sWorkers, Manual: true,
				Guidance: k8sOnCluster + "The kubelet service file, kubeconfigs, kube-proxy kubeconfig, client CA and kubelet config must be owned by root with the restrictive modes the benchmark gives."},
			{ID: "4.2", Title: "Kubelet (4.2.1-4.2.14)", Section: k8sWorkers, Manual: true,
				Guidance: k8sOnCluster + "No anonymous auth, Webhook authorization, a client CA, read-only port 0, streaming timeouts, protect-kernel-defaults, iptables chains, hostname not overridden, event capture, TLS certificates and rotation, strong ciphers, a pod PID limit."},
			{ID: "4.3", Title: "kube-proxy (4.3.1)", Section: k8sWorkers, Manual: true,
				Guidance: k8sOnCluster + "The kube-proxy metrics service binds to localhost."},

			// 5.1 RBAC and Service Accounts
			{ID: "5.1.1", Title: "Ensure that the cluster-admin role is only used where required", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_cluster_admin"}, AppliesTo: k8sBindings,
				Guidance: "Nimbo flags RoleBindings and ClusterRoleBindings of this design that grant cluster-admin. Bindings already in the cluster aren't visible."},
			{ID: "5.1.2", Title: "Minimize access to secrets", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_secrets"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules that list or watch Secrets, or get them without resourceNames (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.3", Title: "Minimize wildcard use in Roles and ClusterRoles", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_wildcard"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules with * in apiGroups, resources or verbs." + k8sRolesOnly},
			{ID: "5.1.4", Title: "Minimize access to create pods", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_create_pods"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting create on pods (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.5", Title: "Ensure that default service accounts are not actively used", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_default_sa"}, AppliesTo: k8sBindings,
				Guidance: "Nimbo flags bindings that grant permissions to a namespace's default ServiceAccount. The benchmark also asks for automountServiceAccountToken: false on every default ServiceAccount of the cluster: check the namespaces you deploy to."},
			{ID: "5.1.6", Title: "Ensure that Service Account Tokens are only mounted where necessary", Section: k8sRBAC,
				Rules: []string{"k8s_automount_token"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags workloads that get an API token mounted (neither the pod nor its ServiceAccount turn automountServiceAccountToken off) although their ServiceAccount is bound to no Role in the design."},
			{ID: "5.1.7", Title: "Avoid use of system:masters group", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_system_masters"}, AppliesTo: k8sBindings,
				Guidance: "Nimbo flags bindings naming the system:masters group. Membership itself comes from client certificates (O=system:masters) issued outside the design: check the cluster's kubeconfigs too."},
			{ID: "5.1.8", Title: "Limit use of the Bind, Impersonate and Escalate permissions in the Kubernetes cluster", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_escalate"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting bind or escalate on roles and clusterroles, or impersonate on users, groups or service accounts (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.9", Title: "Minimize access to create persistent volumes", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_create_pv"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting create on persistentvolumes (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.10", Title: "Minimize access to the proxy sub-resource of nodes", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_node_proxy"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting nodes/proxy (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.11", Title: "Minimize access to the approval sub-resource of certificatesigningrequests objects", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_csr_approval"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting update or patch on certificatesigningrequests/approval (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.12", Title: "Minimize access to webhook configuration objects", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_webhook_config"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting create, update, patch or delete on validating or mutating webhook configurations (wildcards included)." + k8sRolesOnly},
			{ID: "5.1.13", Title: "Minimize access to the service account token creation", Section: k8sRBAC,
				Rules: []string{"k8s_rbac_sa_token"}, AppliesTo: k8sRoles,
				Guidance: "Nimbo flags rules granting create on serviceaccounts/token (wildcards included)." + k8sRolesOnly},

			// 5.2 Pod Security Standards
			{ID: "5.2.1", Title: "Ensure that the cluster has at least one active policy control mechanism in place", Section: k8sPSS, Manual: true,
				Guidance: "Label every namespace with workloads for Pod Security Admission (pod-security.kubernetes.io/enforce: restricted, or baseline) or run a policy engine (Kyverno, Gatekeeper), then attest. The checks below grade what this design asks for; only admission control stops what others deploy."},
			{ID: "5.2.2", Title: "Minimize the admission of privileged containers", Section: k8sPSS,
				Rules: []string{"k8s_privileged"}, Counts: onAttribute("privileged"), AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags containers with privileged: true (the Privileged containers rule's privileged findings; its privilege escalation findings count for 5.2.6, its added capabilities for none: 5.2.9 has a rule of its own)."},
			{ID: "5.2.3", Title: "Minimize the admission of containers wishing to share the host process ID namespace", Section: k8sPSS,
				Rules: []string{"k8s_host_namespaces"}, Counts: onAttribute("hostPID"), AppliesTo: k8sWorkloads, Guidance: fmt.Sprintf(k8sHostNS, "hostPID")},
			{ID: "5.2.4", Title: "Minimize the admission of containers wishing to share the host IPC namespace", Section: k8sPSS,
				Rules: []string{"k8s_host_namespaces"}, Counts: onAttribute("hostIPC"), AppliesTo: k8sWorkloads, Guidance: fmt.Sprintf(k8sHostNS, "hostIPC")},
			{ID: "5.2.5", Title: "Minimize the admission of containers wishing to share the host network namespace", Section: k8sPSS,
				Rules: []string{"k8s_host_namespaces"}, Counts: onAttribute("hostNetwork"), AppliesTo: k8sWorkloads, Guidance: fmt.Sprintf(k8sHostNS, "hostNetwork")},
			{ID: "5.2.6", Title: "Minimize the admission of containers with allowPrivilegeEscalation", Section: k8sPSS,
				Rules: []string{"k8s_privileged"}, Counts: onAttribute("allowPrivilegeEscalation"), AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags containers with allowPrivilegeEscalation: true (the Privileged containers rule's escalation findings). Like the benchmark's audit, an unset value passes; the restricted profile (Restricted pod security rule) wants it explicitly false."},
			{ID: "5.2.7", Title: "Minimize the admission of root containers", Section: k8sPSS,
				Rules: []string{"k8s_run_as_root"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags pods where neither the pod nor every container sets runAsNonRoot: true or a non-zero runAsUser."},
			{ID: "5.2.8", Title: "Minimize the admission of containers with the NET_RAW capability", Section: k8sPSS,
				Rules: []string{"k8s_net_raw"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags Linux containers that don't drop NET_RAW (or ALL), add it back, or run privileged."},
			{ID: "5.2.9", Title: "Minimize the admission of containers with capabilities assigned", Section: k8sPSS,
				Rules: []string{"k8s_capabilities"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags Linux containers that don't drop ALL capabilities or add any besides NET_BIND_SERVICE (the restricted Pod Security Standard)."},
			{ID: "5.2.10", Title: "Minimize the admission of Windows HostProcess containers", Section: k8sPSS,
				Rules: []string{"k8s_windows_host_process"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags pods and containers with windowsOptions.hostProcess: true."},
			{ID: "5.2.11", Title: "Minimize the admission of HostPath volumes", Section: k8sPSS,
				Rules: []string{"k8s_host_path"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags pods with hostPath volumes (as info for DaemonSets, the node agents that may need them)."},
			{ID: "5.2.12", Title: "Minimize the admission of containers which use HostPorts", Section: k8sPSS,
				Rules: []string{"k8s_host_port"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags containers that set ports[].hostPort (as info for DaemonSets)."},

			// 5.3 Network Policies and CNI
			{ID: "5.3.1", Title: "Ensure that the CNI in use supports NetworkPolicies", Section: k8sNetwork, Manual: true,
				Guidance: "Check the cluster's CNI enforces NetworkPolicy (Calico, Cilium, Antrea; k3s's default flannel does through its embedded network policy controller) and attest."},
			{ID: "5.3.2", Title: "Ensure that all Namespaces have NetworkPolicies defined", Section: k8sNetwork,
				Rules: []string{"k8s_network_policy"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags workloads whose pods no NetworkPolicy of the design selects, which is stricter than one policy per namespace: a namespace-wide default-deny policy satisfies both."},

			// 5.4 Secrets Management
			{ID: "5.4.1", Title: "Prefer using Secrets as files over Secrets as environment variables", Section: k8sSecrets,
				Rules: []string{"k8s_secret_env_ref"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags containers reading Secrets through env valueFrom.secretKeyRef or envFrom.secretRef; mount them as a secret volume instead."},
			{ID: "5.4.2", Title: "Consider external secret storage", Section: k8sSecrets, Manual: true,
				Guidance: "Keep secrets in a vault (cloud secret manager, HashiCorp Vault) synced by the External Secrets Operator or the Secrets Store CSI driver, with audit and rotation; attest the choice."},

			// 5.5 Extensible Admission Control
			{ID: "5.5.1", Title: "Configure Image Provenance using ImagePolicyWebhook admission controller", Section: k8sAdmission, Manual: true,
				Guidance: "Admit only images from trusted registries with verified signatures (ImagePolicyWebhook, Kyverno verifyImages, Sigstore policy-controller) and attest."},

			// 5.6 General Policies
			{ID: "5.6.1", Title: "Create administrative boundaries between resources using namespaces", Section: k8sGeneral, Manual: true,
				Guidance: "Give each application or team its own namespace with its own RBAC, quotas and NetworkPolicies; attest after reviewing the namespace layout."},
			{ID: "5.6.2", Title: "Ensure that the seccomp profile is set to docker/default in your Pod definitions", Section: k8sGeneral,
				Rules: []string{"k8s_seccomp"}, AppliesTo: k8sWorkloads,
				Guidance: "Nimbo flags Linux containers whose effective seccompProfile (container, else pod) isn't RuntimeDefault or Localhost; RuntimeDefault replaced docker/default."},
			{ID: "5.6.3", Title: "Apply SecurityContext to your Pods and Containers", Section: k8sGeneral,
				Rules: []string{"k8s_run_as_root", "k8s_hardening"}, Counts: onAttribute("securityContext"), AppliesTo: k8sWorkloads,
				Guidance: "Graded on the restricted Pod Security Standard's security contexts: non-root (Containers run as non-root) plus allowPrivilegeEscalation: false, readOnlyRootFilesystem: true, capabilities.drop [ALL] and seccompProfile RuntimeDefault (Restricted pod security; its unused API token findings don't count here, 5.1.6 grades tokens)."},
			{ID: "5.6.4", Title: "The default namespace should not be used", Section: k8sGeneral,
				Rules:    []string{"k8s_default_namespace"},
				Guidance: "Nimbo flags namespaced objects that land in default (no namespace in the object, the kustomization or the source)."},
		},
	})
}
