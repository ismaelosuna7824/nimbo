package kuberules

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Rules for the policy recommendations (section 5) of the CIS Kubernetes
// Benchmark that a workflow's manifests show and the rules in rules.go don't
// decide on their own (backend/internal/compliance maps the benchmark onto
// both). Rules that would flag most ordinary manifests (capabilities,
// seccomp, mounted tokens, narrow RBAC grants) are opt-in so Problems stays
// quiet; the compliance view still runs them.

const docsRBAC = "https://kubernetes.io/docs/concepts/security/rbac-good-practices/"

var cisRules = []Rule{
	// Pods.
	rule("k8s_host_port", "hostPort", catSecurity, model.SeverityWarning,
		"Containers should not set ports[].hostPort (CIS Kubernetes 5.2.12).", docsPSS+"#baseline",
		"A hostPort binds the port on the node's own address, bypassing Services and NetworkPolicies, and limits the pod to nodes where that port is free."),
	rule("k8s_windows_host_process", "Windows HostProcess containers", catSecurity, model.SeverityWarning,
		"Pods should not set securityContext.windowsOptions.hostProcess: true (CIS Kubernetes 5.2.10).", docsPSS+"#baseline",
		"A HostProcess container runs directly on the Windows node with the node's network and file system: it is the node."),
	optIn(rule("k8s_net_raw", "NET_RAW dropped", catSecurity, model.SeverityInfo,
		"Containers should drop the NET_RAW capability (capabilities.drop [ALL] or [NET_RAW]) (CIS Kubernetes 5.2.8).", docsSecCtx+"#set-capabilities-for-a-container",
		"Container runtimes grant NET_RAW by default: a compromised container can craft raw packets to spoof traffic or poison ARP on the node's network.")),
	optIn(rule("k8s_capabilities", "No Linux capabilities", catSecurity, model.SeverityInfo,
		"Containers should drop ALL capabilities and add back at most NET_BIND_SERVICE (CIS Kubernetes 5.2.9).", docsPSS+"#restricted",
		"Every capability is a slice of root: dropping them all leaves an attacker inside the container with an ordinary user's powers.")),
	optIn(rule("k8s_seccomp", "seccomp profile", catSecurity, model.SeverityInfo,
		"Pods or containers should set seccompProfile type RuntimeDefault (or Localhost) (CIS Kubernetes 5.6.2).", "https://kubernetes.io/docs/tutorials/security/seccomp/",
		"Unless the kubelet enables seccomp by default, containers run Unconfined: every system call, including the ones container escapes rely on, is available.")),
	optIn(rule("k8s_secret_env_ref", "Secrets as files", catSecurity, model.SeverityInfo,
		"Containers should mount Secrets as volumes rather than read them into environment variables (CIS Kubernetes 5.4.1).", docsConcepts+"configuration/secret/#using-secrets-as-files-from-a-pod",
		"Environment variables leak: crash reports, debug endpoints and child processes print or inherit them, and they never update when the Secret rotates.")),
	optIn(rule("k8s_automount_token", "API tokens only where needed", catSecurity, model.SeverityInfo,
		"Pods that don't call the Kubernetes API should set automountServiceAccountToken: false (CIS Kubernetes 5.1.6).", docsRBAC+"#minimize-distribution-of-privileged-tokens",
		"Every mounted token is a credential an attacker inside the pod can use against the API server.")),

	// RBAC.
	rule("k8s_rbac_cluster_admin", "cluster-admin bindings", catSecurity, model.SeverityWarning,
		"Bindings should not grant the cluster-admin ClusterRole (CIS Kubernetes 5.1.1).", docsRBAC+"#least-privilege",
		"cluster-admin can do anything to any object of the cluster: whoever holds the bound identity owns the cluster."),
	rule("k8s_rbac_system_masters", "system:masters group", catSecurity, model.SeverityWarning,
		"Bindings should not name the system:masters group (CIS Kubernetes 5.1.7).", docsRBAC+"#least-privilege",
		"Members of system:masters bypass RBAC and admission webhooks entirely, and their access can't be revoked by deleting bindings."),
	rule("k8s_rbac_default_sa", "Default ServiceAccounts with grants", catSecurity, model.SeverityWarning,
		"Bindings should not grant permissions to a namespace's default ServiceAccount (CIS Kubernetes 5.1.5).", docsConcepts+"security/service-accounts/#default-service-accounts",
		"Every pod that doesn't name a ServiceAccount runs as default: a grant to it reaches every such pod of the namespace."),
	rule("k8s_rbac_wildcard", "Wildcards in Roles", catSecurity, model.SeverityInfo,
		"Roles and ClusterRoles should list their apiGroups, resources and verbs instead of * (CIS Kubernetes 5.1.3).", docsRBAC+"#least-privilege",
		"A wildcard also grants everything added later: new resources, custom resources and subresources such as pods/exec."),
	optIn(rule("k8s_rbac_secrets", "Access to Secrets", catSecurity, model.SeverityInfo,
		"Roles should not list or watch Secrets, nor get them without resourceNames (CIS Kubernetes 5.1.2).", docsRBAC+"#listing-secrets",
		"list and watch return the contents of every Secret they cover: service account tokens, database passwords, TLS keys.")),
	optIn(rule("k8s_rbac_create_pods", "Creating pods", catSecurity, model.SeverityInfo,
		"Roles should grant create on pods only where needed (CIS Kubernetes 5.1.4).", docsRBAC+"#workload-creation",
		"Whoever creates pods can run them as any ServiceAccount of the namespace and mount its Secrets, ConfigMaps and volumes.")),
	optIn(rule("k8s_rbac_escalate", "bind, escalate and impersonate", catSecurity, model.SeverityInfo,
		"Roles should not grant bind or escalate on roles, or impersonate on users, groups or service accounts (CIS Kubernetes 5.1.8).", docsRBAC+"#escalate-verb",
		"These verbs let the holder give itself, or act with, permissions it doesn't have: escalate writes any rule, bind grants any role, impersonate becomes any identity.")),
	optIn(rule("k8s_rbac_create_pv", "Creating PersistentVolumes", catSecurity, model.SeverityInfo,
		"Roles should not grant create on persistentvolumes (CIS Kubernetes 5.1.9).", docsRBAC+"#persistent-volume-creation",
		"A PersistentVolume can be a hostPath: creating one mounts any directory of a node into a pod.")),
	optIn(rule("k8s_rbac_node_proxy", "Node proxy", catSecurity, model.SeverityInfo,
		"Roles should not grant access to nodes/proxy (CIS Kubernetes 5.1.10).", docsRBAC+"#access-to-proxy-subresource-of-nodes",
		"nodes/proxy reaches the kubelet API directly, bypassing audit logging and admission control: it runs commands in any pod of the node.")),
	optIn(rule("k8s_rbac_csr_approval", "Approving certificates", catSecurity, model.SeverityInfo,
		"Roles should not grant update on certificatesigningrequests/approval (CIS Kubernetes 5.1.11).", docsRBAC+"#csrs-and-certificate-issuing",
		"Approving a certificate signing request issues a client certificate for any identity the signer allows, system:masters included.")),
	optIn(rule("k8s_rbac_webhook_config", "Admission webhook configurations", catSecurity, model.SeverityInfo,
		"Roles should not grant write access to validating or mutating webhook configurations (CIS Kubernetes 5.1.12).", docsRBAC+"#control-admission-webhooks",
		"A webhook configuration decides which objects the cluster accepts and can rewrite them: changing one disables policies or injects containers into every pod.")),
	optIn(rule("k8s_rbac_sa_token", "Creating ServiceAccount tokens", catSecurity, model.SeverityInfo,
		"Roles should not grant create on serviceaccounts/token (CIS Kubernetes 5.1.13).", docsRBAC+"#token-request",
		"Creating a token for a ServiceAccount is acting as it, with all its permissions, for as long as the token lives.")),
}

func init() {
	// ruleByCode is built before init functions run.
	for _, r := range cisRules {
		rules = append(rules, r)
		ruleByCode[r.Code] = r
	}
}

// rbacGroup is the API group of Roles and bindings.
const rbacGroup = "rbac.authorization.k8s.io"

// sensitiveGrants are the permissions the narrow RBAC rules look for: a
// policy rule matches when it covers one of verbs on one of resources of
// group.
var sensitiveGrants = []struct {
	code, what string
	group      string
	resources  []string
	verbs      []string
}{
	{"k8s_rbac_create_pods", "create on pods", "", []string{"pods"}, []string{"create"}},
	{"k8s_rbac_escalate", "bind or escalate on roles", rbacGroup, []string{"roles", "clusterroles"}, []string{"bind", "escalate"}},
	{"k8s_rbac_escalate", "impersonate", "", []string{"users", "groups", "serviceaccounts"}, []string{"impersonate"}},
	{"k8s_rbac_create_pv", "create on persistentvolumes", "", []string{"persistentvolumes"}, []string{"create"}},
	{"k8s_rbac_node_proxy", "access to nodes/proxy", "", []string{"nodes/proxy"}, []string{"get", "create"}},
	{"k8s_rbac_csr_approval", "update on certificatesigningrequests/approval", "certificates.k8s.io", []string{"certificatesigningrequests/approval"}, []string{"update", "patch"}},
	{"k8s_rbac_webhook_config", "write access to webhook configurations", "admissionregistration.k8s.io", []string{"validatingwebhookconfigurations", "mutatingwebhookconfigurations"}, []string{"create", "update", "patch", "delete"}},
	{"k8s_rbac_sa_token", "create on serviceaccounts/token", "", []string{"serviceaccounts/token"}, []string{"create"}},
}

// scalars are the string items of a list field.
func scalars(n *yaml.Node, k string) []string {
	var out []string
	for _, it := range items(n, k) {
		out = append(out, it.Value)
	}
	return out
}

// grants reports whether a policy rule covers verb on resource of group,
// with RBAC's matching: "*" matches anything (subresources included) and
// "*/sub" any resource's sub subresource.
func grants(r *yaml.Node, group, resource string, verbs ...string) bool {
	if g := scalars(r, "apiGroups"); !slices.Contains(g, group) && !slices.Contains(g, "*") {
		return false
	}
	_, sub, _ := strings.Cut(resource, "/")
	if !slices.ContainsFunc(scalars(r, "resources"), func(x string) bool { return x == resource || x == "*" || sub != "" && x == "*/"+sub }) {
		return false
	}
	v := scalars(r, "verbs")
	return slices.Contains(v, "*") || slices.ContainsFunc(verbs, func(x string) bool { return slices.Contains(v, x) })
}

// checkCIS runs the CIS rules on the workloads and RBAC objects.
func (c *checker) checkCIS() {
	for _, w := range c.workloads {
		if w.pod != nil {
			c.cisWorkload(w)
		}
	}
	for _, o := range c.in.objects {
		if o.group() != rbacGroup {
			continue
		}
		switch o.kind {
		case "Role", "ClusterRole":
			c.cisRole(o)
		case "RoleBinding", "ClusterRoleBinding":
			c.cisBinding(o)
		}
	}
}

func (c *checker) cisWorkload(w *workload) {
	o, pod := w.o, w.pod
	hostSev, agent := model.SeverityWarning, ""
	if o.kind == "DaemonSet" {
		hostSev, agent = model.SeverityInfo, " (expected for a node agent; check it needs it)"
	}
	if b := boolAt(pod, "securityContext", "windowsOptions", "hostProcess"); b != nil && *b {
		c.diag(model.SeverityWarning, "k8s_windows_host_process", o, field(w.podPath, "securityContext", "windowsOptions", "hostProcess"),
			fmt.Sprintf("%s runs Windows HostProcess containers", w.label()), "Remove windowsOptions.hostProcess; run the tool as a regular Windows container.")
	}
	windows := str(pod, "os", "name") == "windows"
	podSeccomp := str(pod, "securityContext", "seccompProfile", "type")
	for _, ct := range w.containers() {
		sc := get(ct.n, "securityContext")
		for i, p := range items(ct.n, "ports") {
			if n, ok := intAt(p, "hostPort"); ok && n > 0 {
				c.diag(hostSev, "k8s_host_port", o, field(ct.path, "ports"+idx(i), "hostPort"),
					fmt.Sprintf("%s: %s binds node port %d (hostPort)%s", w.label(), ct.label(), n, agent),
					"Remove hostPort; expose the port through a Service (NodePort or LoadBalancer when it must be reached from outside).")
			}
		}
		if b := boolAt(sc, "windowsOptions", "hostProcess"); b != nil && *b {
			c.diag(model.SeverityWarning, "k8s_windows_host_process", o, field(ct.path, "securityContext", "windowsOptions", "hostProcess"),
				fmt.Sprintf("%s: %s is a Windows HostProcess container", w.label(), ct.label()), "Remove windowsOptions.hostProcess; run the tool as a regular Windows container.")
		}
		c.cisSecrets(w, ct)
		if windows {
			continue // capabilities and seccomp are Linux settings
		}
		var added []string
		for _, a := range items(sc, "capabilities", "add") {
			added = append(added, strings.TrimPrefix(strings.ToUpper(a.Value), "CAP_"))
		}
		drops := map[string]bool{}
		for _, d := range items(sc, "capabilities", "drop") {
			drops[strings.TrimPrefix(strings.ToUpper(d.Value), "CAP_")] = true
		}
		privileged := false
		if b := boolAt(sc, "privileged"); b != nil && *b {
			privileged = true
		}
		capsPath := field(ct.path, "securityContext", "capabilities")
		switch {
		case privileged || slices.Contains(added, "NET_RAW") || slices.Contains(added, "ALL"):
			c.diag(model.SeverityInfo, "k8s_net_raw", o, capsPath, fmt.Sprintf("%s: %s has the NET_RAW capability", w.label(), ct.label()),
				"Remove NET_RAW (or ALL, or privileged: true) unless it sends raw packets.")
		case !drops["ALL"] && !drops["NET_RAW"]:
			c.diag(model.SeverityInfo, "k8s_net_raw", o, capsPath, fmt.Sprintf("%s: %s keeps the runtime's default NET_RAW capability", w.label(), ct.label()),
				"Set securityContext.capabilities.drop: [ALL] (or at least [NET_RAW]).")
		}
		extra := slices.DeleteFunc(slices.Clone(added), func(a string) bool { return a == "NET_BIND_SERVICE" })
		switch {
		case !drops["ALL"]:
			c.diag(model.SeverityInfo, "k8s_capabilities", o, capsPath, fmt.Sprintf("%s: %s doesn't drop all capabilities", w.label(), ct.label()),
				"Set securityContext.capabilities: {drop: [ALL]} (add NET_BIND_SERVICE only if it binds ports below 1024).")
		case len(extra) > 0:
			c.diag(model.SeverityInfo, "k8s_capabilities", o, capsPath, fmt.Sprintf("%s: %s adds %s", w.label(), ct.label(), strings.Join(extra, ", ")),
				"Remove the added capabilities (only NET_BIND_SERVICE is allowed by the restricted Pod Security Standard).")
		}
		seccomp, path := str(sc, "seccompProfile", "type"), field(ct.path, "securityContext", "seccompProfile")
		if seccomp == "" {
			seccomp, path = podSeccomp, field(w.podPath, "securityContext", "seccompProfile")
		}
		if seccomp != "RuntimeDefault" && seccomp != "Localhost" {
			msg := fmt.Sprintf("%s: %s sets no seccomp profile", w.label(), ct.label())
			if seccomp != "" {
				msg = fmt.Sprintf("%s: %s runs with seccomp %s", w.label(), ct.label(), seccomp)
			}
			c.diag(model.SeverityInfo, "k8s_seccomp", o, path, msg, "Set securityContext: {seccompProfile: {type: RuntimeDefault}} on the pod.")
		}
	}
	if c.unusedToken(w) {
		c.diag(model.SeverityInfo, "k8s_automount_token", o, w.podPath,
			fmt.Sprintf("%s mounts a Kubernetes API token it does not seem to use", w.label()),
			"Set automountServiceAccountToken: false in the pod spec (or the ServiceAccount).")
	}
}

// cisSecrets reports a container reading a Secret into its environment.
func (c *checker) cisSecrets(w *workload, ct container) {
	for i, e := range items(ct.n, "env") {
		if ref := get(e, "valueFrom", "secretKeyRef"); ref != nil {
			c.diag(model.SeverityInfo, "k8s_secret_env_ref", w.o, field(ct.path, "env"+idx(i), "valueFrom"),
				fmt.Sprintf("%s: %s reads Secret %s into the environment variable %s", w.label(), ct.label(), str(ref, "name"), str(e, "name")),
				fmt.Sprintf("Mount the Secret as a volume (volumes: [{name: %s, secret: {secretName: %s}}] with a readOnly volumeMount) and read the file.", str(ref, "name"), str(ref, "name")))
			return
		}
	}
	for i, e := range items(ct.n, "envFrom") {
		if ref := get(e, "secretRef"); ref != nil {
			c.diag(model.SeverityInfo, "k8s_secret_env_ref", w.o, field(ct.path, "envFrom"+idx(i), "secretRef"),
				fmt.Sprintf("%s: %s loads Secret %s into its environment", w.label(), ct.label(), str(ref, "name")),
				fmt.Sprintf("Mount the Secret as a volume (volumes: [{name: %s, secret: {secretName: %s}}] with a readOnly volumeMount) and read the files.", str(ref, "name"), str(ref, "name")))
			return
		}
	}
}

func (c *checker) cisRole(o *object) {
	label := o.kind + " " + o.name
	for i, r := range items(o.node, "rules") {
		path := "rules" + idx(i)
		var wild []string
		for _, k := range []string{"apiGroups", "resources", "verbs"} {
			if slices.Contains(scalars(r, k), "*") {
				wild = append(wild, k)
			}
		}
		if len(wild) > 0 {
			c.diag(model.SeverityInfo, "k8s_rbac_wildcard", o, path, fmt.Sprintf("%s uses * in the %s of %s", label, strings.Join(wild, " and "), path),
				"List the exact apiGroups, resources and verbs the identity needs.")
		}
		named := len(items(r, "resourceNames")) > 0
		if grants(r, "", "secrets", "list", "watch") || !named && grants(r, "", "secrets", "get") {
			c.diag(model.SeverityInfo, "k8s_rbac_secrets", o, path, fmt.Sprintf("%s can read every Secret it covers (%s)", label, path),
				"Grant get on the Secrets it needs by name (resourceNames), or mount them in its pods instead.")
		}
		reported := map[string]bool{}
		for _, g := range sensitiveGrants {
			if reported[g.code] {
				continue
			}
			for _, res := range g.resources {
				if grants(r, g.group, res, g.verbs...) {
					reported[g.code] = true
					c.diag(model.SeverityInfo, g.code, o, path, fmt.Sprintf("%s grants %s (%s)", label, g.what, path),
						"Remove the permission unless the identity's job needs it; keep it in a Role of its own, bound only to that identity.")
					break
				}
			}
		}
	}
}

func (c *checker) cisBinding(o *object) {
	label := o.kind + " " + o.name
	if str(o.node, "roleRef", "kind") == "ClusterRole" && str(o.node, "roleRef", "name") == "cluster-admin" {
		c.diag(model.SeverityWarning, "k8s_rbac_cluster_admin", o, "roleRef", fmt.Sprintf("%s grants cluster-admin", label),
			"Bind a Role listing only the verbs and resources the subjects need.")
	}
	for i, s := range items(o.node, "subjects") {
		path := "subjects" + idx(i)
		switch kind, name := str(s, "kind"), str(s, "name"); {
		case kind == "Group" && name == "system:masters":
			c.diag(model.SeverityWarning, "k8s_rbac_system_masters", o, path, fmt.Sprintf("%s names the system:masters group", label),
				"Remove the subject: grant a named group a Role with the permissions it needs.")
		case kind == "ServiceAccount" && name == "default":
			c.diag(model.SeverityWarning, "k8s_rbac_default_sa", o, path, fmt.Sprintf("%s grants permissions to the default ServiceAccount", label),
				"Create a ServiceAccount for the workload that needs the permissions, bind it instead and set serviceAccountName in its pods.")
		}
	}
}
