package kuberules

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// workload is an object with a pod template.
type workload struct {
	o         *object
	podPath   string     // field path of the pod spec
	pod       *yaml.Node // pod spec
	podLabels map[string]string
	// longRunning: the pods serve until deleted (not Jobs or run-once pods).
	longRunning bool
}

// podTemplatePaths are where each kind keeps its pod template.
var podTemplatePaths = map[string]string{
	"Deployment": "spec.template", "StatefulSet": "spec.template", "DaemonSet": "spec.template",
	"ReplicaSet": "spec.template", "ReplicationController": "spec.template", "Job": "spec.template",
	"CronJob": "spec.jobTemplate.spec.template",
}

func newWorkload(o *object) *workload {
	if o.group() != "" && o.group() != "apps" && o.group() != "batch" {
		return nil
	}
	if o.kind == "Pod" {
		rp := str(o.node, "spec", "restartPolicy")
		return &workload{o: o, podPath: "spec", pod: get(o.node, "spec"), podLabels: strMap(o.node, "metadata", "labels"), longRunning: rp == "" || rp == "Always"}
	}
	tp, ok := podTemplatePaths[o.kind]
	if !ok {
		return nil
	}
	tmpl := get(o.node, strings.Split(tp, ".")...)
	if tmpl == nil {
		return nil
	}
	return &workload{o: o, podPath: tp + ".spec", pod: get(tmpl, "spec"), podLabels: strMap(tmpl, "metadata", "labels"),
		longRunning: o.kind != "Job" && o.kind != "CronJob"}
}

// container is one container of a pod spec.
type container struct {
	n    *yaml.Node
	path string
	name string
	init bool
	// sidecar: a native sidecar (init container with restartPolicy Always).
	sidecar bool
}

func (w *workload) containers() []container {
	var out []container
	for i, n := range items(w.pod, "initContainers") {
		out = append(out, container{n, field(w.podPath, "initContainers"+idx(i)), str(n, "name"), true, str(n, "restartPolicy") == "Always"})
	}
	for i, n := range items(w.pod, "containers") {
		out = append(out, container{n, field(w.podPath, "containers"+idx(i)), str(n, "name"), false, false})
	}
	return out
}

func (w *workload) label() string { return w.o.kind + " " + w.o.name }

func (c container) label() string {
	if c.init && !c.sidecar {
		return "init container " + c.name
	}
	return "container " + c.name
}

// serves: the container runs for the pod's lifetime.
func (c container) serves(w *workload) bool { return w.longRunning && (!c.init || c.sidecar) }

// limitRangeDefaults reports whether a LimitRange of ns gives containers
// default requests (cpu/memory) and limits.
func (c *checker) limitRangeDefaults(ns string) (requests, limits map[string]bool) {
	requests, limits = map[string]bool{}, map[string]bool{}
	for _, o := range c.in.objects {
		if o.kind != "LimitRange" || o.namespace != ns {
			continue
		}
		for _, l := range items(o.node, "spec", "limits") {
			if str(l, "type") != "Container" {
				continue
			}
			for _, r := range []string{"cpu", "memory"} {
				if str(l, "defaultRequest", r) != "" || str(l, "default", r) != "" {
					requests[r] = true // a default limit is also the default request
				}
				if str(l, "default", r) != "" {
					limits[r] = true
				}
			}
		}
	}
	return requests, limits
}

// missingRequests are the resources (cpu, memory) a container has neither
// a request nor a limit for (a limit alone sets the request).
func missingRequests(n *yaml.Node, defaults map[string]bool, only ...string) []string {
	var out []string
	for _, r := range []string{"cpu", "memory"} {
		if len(only) > 0 && only[0] != r {
			continue
		}
		if str(n, "resources", "requests", r) == "" && str(n, "resources", "limits", r) == "" && !defaults[r] {
			out = append(out, r)
		}
	}
	return out
}

func (c *checker) workloadRules(w *workload) {
	o := w.o
	if w.pod == nil {
		return
	}
	// Selector vs pod template.
	if o.kind != "Pod" && o.kind != "CronJob" {
		sel := labelSelector(get(o.node, "spec", "selector"))
		if !sel.empty() && !sel.matches(w.podLabels) {
			c.diag(model.SeverityError, "k8s_workload_selector", o, "spec.selector",
				fmt.Sprintf("%s: spec.selector (%s) does not match the pod template labels %s", w.label(), sel, labelsYAML(w.podLabels)),
				"Make spec.template.metadata.labels include every spec.selector.matchLabels pair (the selector can't change after creation).")
		}
	}

	reqDefaults, limitDefaults := c.limitRangeDefaults(o.namespace)
	for _, ct := range w.containers() {
		c.containerRules(w, ct, reqDefaults, limitDefaults)
	}
	c.podSecurity(w)
	c.podRefs(w)
}

func (c *checker) containerRules(w *workload, ct container, reqDefaults, limitDefaults map[string]bool) {
	o := w.o
	// Resources.
	if miss := missingRequests(ct.n, reqDefaults); len(miss) > 0 {
		c.diag(model.SeverityWarning, "k8s_no_requests", o, field(ct.path, "resources"),
			fmt.Sprintf("%s: %s has no %s request", w.label(), ct.label(), strings.Join(miss, "/")),
			"Set resources.requests, e.g. resources: {requests: {cpu: 100m, memory: 128Mi}, limits: {memory: 256Mi}} (measure with the Perf tab or kubectl top pod).")
	}
	if str(ct.n, "resources", "limits", "memory") == "" && !limitDefaults["memory"] {
		fix := "Set resources.limits.memory, e.g. 256Mi (about 2× the memory request)."
		if req := str(ct.n, "resources", "requests", "memory"); req != "" {
			fix = fmt.Sprintf("Set resources.limits.memory above the request (%s), e.g. twice it.", req)
		}
		c.diag(model.SeverityInfo, "k8s_no_memory_limit", o, field(ct.path, "resources"),
			fmt.Sprintf("%s: %s has no memory limit", w.label(), ct.label()), fix)
	}

	// Probes.
	ports := items(ct.n, "ports")
	portNames := map[string]bool{}
	for _, p := range ports {
		if n := str(p, "name"); n != "" {
			portNames[n] = true
		}
	}
	if ct.serves(w) && len(ports) > 0 {
		first := str(ports[0], "containerPort")
		if get(ct.n, "readinessProbe") == nil {
			c.diag(model.SeverityWarning, "k8s_no_readiness_probe", o, ct.path,
				fmt.Sprintf("%s: %s serves port %s without a readinessProbe", w.label(), ct.label(), first),
				fmt.Sprintf("Add readinessProbe: {httpGet: {path: /healthz, port: %s}, periodSeconds: 10} (or tcpSocket: {port: %s} when it is not HTTP).", first, first))
		}
		if get(ct.n, "livenessProbe") == nil {
			c.diag(model.SeverityInfo, "k8s_no_liveness_probe", o, ct.path,
				fmt.Sprintf("%s: %s serves port %s without a livenessProbe", w.label(), ct.label(), first),
				fmt.Sprintf("Add livenessProbe: {tcpSocket: {port: %s}, periodSeconds: 20, failureThreshold: 3} (keep it cheaper and more tolerant than the readiness probe).", first))
		}
	}
	for _, probe := range []string{"readinessProbe", "livenessProbe", "startupProbe"} {
		for _, kind := range []string{"httpGet", "tcpSocket"} {
			pn := get(ct.n, probe, kind, "port")
			if pn == nil || pn.Kind != yaml.ScalarNode || pn.ShortTag() != "!!str" || isNumber(pn.Value) || portNames[pn.Value] {
				continue
			}
			c.diag(model.SeverityWarning, "k8s_probe_port", o, field(ct.path, probe, kind, "port"),
				fmt.Sprintf("%s: the %s of %s uses port %q, which the container does not declare", w.label(), probe, ct.label(), pn.Value),
				"Use a containerPort number, or name the port in ports: [{name: "+pn.Value+", containerPort: …}].")
		}
	}

	// Image.
	c.imageRules(w, ct)

	// Literal secrets in env.
	for i, e := range items(ct.n, "env") {
		name, val := str(e, "name"), get(e, "value")
		if val == nil || val.Kind != yaml.ScalarNode || !isSecretValue(name, val.Value) {
			continue
		}
		secret := strings.ToLower(strings.NewReplacer("_", "-").Replace(w.o.name)) + "-secrets"
		c.diag(model.SeverityWarning, "k8s_secret_env", o, field(ct.path, "env"+idx(i), "value"),
			fmt.Sprintf("%s: %s sets %s to a literal value", w.label(), ct.label(), name),
			fmt.Sprintf("Put it in a Secret and reference it: valueFrom: {secretKeyRef: {name: %s, key: %s}}.", secret, name))
	}

	// Privileges.
	sc := get(ct.n, "securityContext")
	if b := boolAt(sc, "privileged"); b != nil && *b {
		c.diag(model.SeverityWarning, "k8s_privileged", o, field(ct.path, "securityContext", "privileged"),
			fmt.Sprintf("%s: %s runs privileged", w.label(), ct.label()),
			"Remove privileged: true; add only the capabilities it needs (securityContext.capabilities.add).")
	}
	if b := boolAt(sc, "allowPrivilegeEscalation"); b != nil && *b {
		c.diag(model.SeverityWarning, "k8s_privileged", o, field(ct.path, "securityContext", "allowPrivilegeEscalation"),
			fmt.Sprintf("%s: %s allows privilege escalation", w.label(), ct.label()),
			"Set allowPrivilegeEscalation: false.")
	}
	for _, cap := range items(sc, "capabilities", "add") {
		switch strings.TrimPrefix(strings.ToUpper(cap.Value), "CAP_") {
		case "ALL", "SYS_ADMIN", "NET_ADMIN", "SYS_PTRACE", "SYS_MODULE":
			c.diag(model.SeverityWarning, "k8s_privileged", o, field(ct.path, "securityContext", "capabilities", "add"),
				fmt.Sprintf("%s: %s adds the %s capability", w.label(), ct.label(), cap.Value),
				"Drop it; grant the narrow capability the process needs (e.g. NET_BIND_SERVICE to bind ports below 1024).")
		}
	}

	// Hardening (opt-in).
	var missing []string
	if b := boolAt(sc, "allowPrivilegeEscalation"); b == nil || *b {
		missing = append(missing, "allowPrivilegeEscalation: false")
	}
	if b := boolAt(sc, "readOnlyRootFilesystem"); b == nil || !*b {
		missing = append(missing, "readOnlyRootFilesystem: true")
	}
	dropsAll := false
	for _, d := range items(sc, "capabilities", "drop") {
		dropsAll = dropsAll || strings.EqualFold(d.Value, "ALL")
	}
	if !dropsAll {
		missing = append(missing, "capabilities: {drop: [ALL]}")
	}
	if str(sc, "seccompProfile", "type") == "" && str(w.pod, "securityContext", "seccompProfile", "type") == "" {
		missing = append(missing, "seccompProfile: {type: RuntimeDefault}")
	}
	if len(missing) > 0 {
		c.diag(model.SeverityInfo, "k8s_hardening", o, field(ct.path, "securityContext"),
			fmt.Sprintf("%s: %s is not hardened (%d of 4 settings missing)", w.label(), ct.label(), len(missing)),
			"Set securityContext: {"+strings.Join(missing, ", ")+"} (mount an emptyDir where the app writes, e.g. /tmp).")
	}
}

func (c *checker) imageRules(w *workload, ct container) {
	img := get(ct.n, "image")
	if img == nil || img.Value == "" || strings.ContainsAny(img.Value, "${}") {
		return
	}
	ref := compose.ParseImage(img.Value)
	if ref == nil {
		return
	}
	pull := str(ct.n, "imagePullPolicy")
	if c.in.localImages[ref.Repo] {
		if ref.Digest == "" && (ref.Tag == "" || ref.Tag == "latest") || pull == "Always" {
			c.diag(model.SeverityWarning, "k8s_local_image", w.o, field(ct.path, "image"),
				fmt.Sprintf("%s: %s runs the locally built %s with %s", w.label(), ct.label(), img.Value, orStr(pull == "Always", "imagePullPolicy Always", "the latest tag (pulled from a registry)")),
				fmt.Sprintf("Use image: %s:dev and imagePullPolicy: IfNotPresent (Nimbo loads the image into the local cluster).", ref.Repo))
		}
		return
	}
	if ref.Digest == "" && (ref.Tag == "" || ref.Tag == "latest") {
		suggestion := ref.Repo + ":<version>"
		if im, ok := compose.ImageFor(img.Value); ok {
			suggestion = ref.Repo + ":" + im.Tag
		}
		c.diag(model.SeverityWarning, "k8s_latest_tag", w.o, field(ct.path, "image"),
			fmt.Sprintf("%s: %s uses %s without a version tag", w.label(), ct.label(), img.Value),
			fmt.Sprintf("Pin a version, e.g. image: %s.", suggestion))
	}
}

func orStr(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func (c *checker) podSecurity(w *workload) {
	o, pod := w.o, w.pod
	// Node agents (DaemonSets: exporters, log shippers, CNI) read the node
	// by design: their host access is reported as info.
	hostSev, agent := model.SeverityWarning, ""
	if o.kind == "DaemonSet" {
		hostSev, agent = model.SeverityInfo, " (expected for a node agent; check it needs it)"
	}
	for _, h := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		if b := boolAt(pod, h); b != nil && *b {
			c.diag(hostSev, "k8s_host_namespaces", o, field(w.podPath, h),
				fmt.Sprintf("%s uses %s: true%s", w.label(), h, agent),
				"Remove it; expose ports through a Service (hostNetwork) and keep the pod's own process namespace.")
		}
	}
	for i, v := range items(pod, "volumes") {
		if hp := str(v, "hostPath", "path"); hp != "" {
			c.diag(hostSev, "k8s_host_path", o, field(w.podPath, "volumes"+idx(i), "hostPath"),
				fmt.Sprintf("%s mounts the node path %s (volume %s)%s", w.label(), hp, str(v, "name"), agent),
				"Use a PersistentVolumeClaim for data, an emptyDir for scratch space or a ConfigMap for files; mount host paths readOnly when an agent must read them.")
		}
	}

	// Non-root: the pod or every container must say so.
	nonRoot := func(sc *yaml.Node) bool {
		if b := boolAt(sc, "runAsNonRoot"); b != nil && *b {
			return true
		}
		u, ok := intAt(sc, "runAsUser")
		return ok && u > 0
	}
	if !nonRoot(get(pod, "securityContext")) {
		var root []string
		for _, ct := range w.containers() {
			sc := get(ct.n, "securityContext")
			if b := boolAt(sc, "runAsNonRoot"); b != nil && !*b || !nonRoot(sc) {
				root = append(root, ct.name)
			}
		}
		if len(root) > 0 {
			c.diag(model.SeverityInfo, "k8s_run_as_root", o, field(w.podPath, "securityContext"),
				fmt.Sprintf("%s may run as root (%s)", w.label(), strings.Join(root, ", ")),
				"Set securityContext: {runAsNonRoot: true, runAsUser: 10001} on the pod (the image must not need root: use an unprivileged variant such as nginxinc/nginx-unprivileged).")
		}
	}
	if c.unusedToken(w) {
		c.diag(model.SeverityInfo, "k8s_hardening", o, w.podPath,
			fmt.Sprintf("%s mounts a Kubernetes API token it does not seem to use", w.label()),
			"Set automountServiceAccountToken: false in the pod spec (or the ServiceAccount).")
	}
}

// unusedToken: the pods get a Kubernetes API token mounted (neither the pod
// nor its ServiceAccount turn automountServiceAccountToken off) although
// their ServiceAccount is bound to no Role.
func (c *checker) unusedToken(w *workload) bool {
	pod := w.pod
	if b := boolAt(pod, "automountServiceAccountToken"); b != nil && !*b {
		return false
	}
	sa := c.get("ServiceAccount", w.o.namespace, orStr(str(pod, "serviceAccountName") == "", "default", str(pod, "serviceAccountName")))
	if sa != nil {
		if b := boolAt(sa.node, "automountServiceAccountToken"); b != nil && !*b {
			return false
		}
	}
	return !c.usesAPI(w)
}

// usesAPI reports whether the workload runs as a ServiceAccount bound to a
// Role (it calls the Kubernetes API).
func (c *checker) usesAPI(w *workload) bool {
	sa := str(w.pod, "serviceAccountName")
	if sa == "" || sa == "default" {
		return false
	}
	for _, o := range c.in.objects {
		if o.kind != "RoleBinding" && o.kind != "ClusterRoleBinding" {
			continue
		}
		for _, s := range items(o.node, "subjects") {
			if str(s, "kind") == "ServiceAccount" && str(s, "name") == sa {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// References from pods

// generatedSecret reports whether a missing Secret is made at runtime by
// the workflow: by a custom resource (cert-manager Certificates,
// ExternalSecrets, SealedSecrets…), or by a workload whose role may create
// Secrets in that namespace (webhook certificate jobs, init jobs of charts).
func (c *checker) generatedSecret(ns, name string) bool {
	for _, o := range c.in.objects {
		if o.namespace != ns && o.kind != "ClusterRole" || o.kind != "Role" && o.kind != "ClusterRole" && builtinGroup(o.group()) {
			continue
		}
		if o.kind == "Role" || o.kind == "ClusterRole" {
			if createsSecrets(o) {
				return true
			}
			continue
		}
		if o.name == name && strings.Contains(o.kind, "Secret") || str(o.node, "spec", "secretName") == name || str(o.node, "spec", "target", "name") == name {
			return true
		}
	}
	return false
}

// createsSecrets reports whether a Role/ClusterRole may create Secrets.
func createsSecrets(o *object) bool {
	if o.group() != "rbac.authorization.k8s.io" {
		return false
	}
	for _, r := range items(o.node, "rules") {
		var res, verbs, groups bool
		for _, x := range items(r, "resources") {
			res = res || x.Value == "secrets" || x.Value == "*"
		}
		for _, x := range items(r, "verbs") {
			verbs = verbs || x.Value == "create" || x.Value == "*"
		}
		for _, x := range items(r, "apiGroups") {
			groups = groups || x.Value == "" || x.Value == "*"
		}
		if res && verbs && groups {
			return true
		}
	}
	return false
}

// podsFromElsewhere reports whether pods of namespace ns may come from
// outside the workflow's workloads: a system namespace, or one holding
// custom resources (operators create pods for them: Prometheus, …).
func (c *checker) podsFromElsewhere(ns string) bool {
	if systemNamespaces[ns] && ns != "default" {
		return true
	}
	for _, o := range c.in.objects {
		if o.namespace == ns && custom(o) {
			return true
		}
	}
	return false
}

// custom reports whether o is a custom resource (not a Kubernetes kind,
// current or removed).
func custom(o *object) bool {
	if builtinGroup(o.group()) {
		return false
	}
	_, removed := lookupAPI(removedAPIs, o.apiVersion, o.kind)
	return !removed
}

func (c *checker) podRefs(w *workload) {
	o, ns := w.o, w.o.namespace
	missing := func(path, kind, name string) {
		c.diag(model.SeverityWarning, "k8s_unknown_ref", o, path,
			fmt.Sprintf("%s uses %s %s, which is not in the workflow", w.label(), kind, name),
			fmt.Sprintf("Add the %s (or mark the reference optional: true if the app works without it).", kind))
	}
	ref := func(path, kind, name string, optional *bool) {
		if name == "" || optional != nil && *optional || strings.ContainsAny(name, "${}") {
			return
		}
		if c.get(kind, ns, name) != nil || kind == "Secret" && c.generatedSecret(ns, name) {
			return
		}
		missing(path, kind, name)
	}
	claims := map[string]bool{}
	for _, t := range items(o.node, "spec", "volumeClaimTemplates") {
		claims[str(t, "metadata", "name")] = true
	}
	for _, ct := range w.containers() {
		for i, e := range items(ct.n, "env") {
			p := field(ct.path, "env"+idx(i), "valueFrom")
			if r := get(e, "valueFrom", "configMapKeyRef"); r != nil {
				ref(field(p, "configMapKeyRef"), "ConfigMap", str(r, "name"), boolAt(r, "optional"))
			}
			if r := get(e, "valueFrom", "secretKeyRef"); r != nil {
				ref(field(p, "secretKeyRef"), "Secret", str(r, "name"), boolAt(r, "optional"))
			}
		}
		for i, e := range items(ct.n, "envFrom") {
			p := field(ct.path, "envFrom"+idx(i))
			if r := get(e, "configMapRef"); r != nil {
				ref(field(p, "configMapRef"), "ConfigMap", str(r, "name"), boolAt(r, "optional"))
			}
			if r := get(e, "secretRef"); r != nil {
				ref(field(p, "secretRef"), "Secret", str(r, "name"), boolAt(r, "optional"))
			}
		}
	}
	for i, v := range items(w.pod, "volumes") {
		p := field(w.podPath, "volumes"+idx(i))
		if r := get(v, "configMap"); r != nil {
			ref(field(p, "configMap"), "ConfigMap", str(r, "name"), boolAt(r, "optional"))
		}
		if r := get(v, "secret"); r != nil {
			ref(field(p, "secret"), "Secret", str(r, "secretName"), boolAt(r, "optional"))
		}
		if r := get(v, "persistentVolumeClaim"); r != nil && !claims[str(r, "claimName")] {
			ref(field(p, "persistentVolumeClaim"), "PersistentVolumeClaim", str(r, "claimName"), nil)
		}
		for j, s := range items(v, "projected", "sources") {
			sp := field(p, "projected", "sources"+idx(j))
			if r := get(s, "configMap"); r != nil {
				ref(field(sp, "configMap"), "ConfigMap", str(r, "name"), boolAt(r, "optional"))
			}
			if r := get(s, "secret"); r != nil {
				ref(field(sp, "secret"), "Secret", str(r, "name"), boolAt(r, "optional"))
			}
		}
	}
	if sa := str(w.pod, "serviceAccountName"); sa != "" && sa != "default" {
		ref(field(w.podPath, "serviceAccountName"), "ServiceAccount", sa, nil)
	}
}

// ---------------------------------------------------------------------------
// Services, Ingress, HTTPRoute

func (c *checker) checkService(o *object) {
	if o.group() != "" || str(o.node, "spec", "type") == "ExternalName" {
		return
	}
	sel := &selector{labels: strMap(o.node, "spec", "selector")}
	if sel.empty() {
		return // endpoints managed by hand (EndpointSlices) or by a controller
	}
	targets := c.selected(o.namespace, sel)
	if len(targets) == 0 {
		if c.podsFromElsewhere(o.namespace) {
			return
		}
		fix := "Set spec.selector to the pod labels of the workload it fronts (spec.template.metadata.labels)."
		if w := c.nearest(o.namespace, sel); w != nil {
			fix = fmt.Sprintf("%s's pods are labeled %s: use those labels as spec.selector.", w.label(), labelsYAML(w.podLabels))
		}
		c.diag(model.SeverityWarning, "k8s_selector_mismatch", o, "spec.selector",
			fmt.Sprintf("Service %s selects no pods of the workflow (%s)", o.name, sel), fix)
		return
	}
	names := map[string]bool{}
	for _, w := range targets {
		for _, ct := range w.containers() {
			for _, p := range items(ct.n, "ports") {
				names[str(p, "name")] = true
			}
		}
	}
	for i, p := range items(o.node, "spec", "ports") {
		tp := get(p, "targetPort")
		if tp == nil || tp.ShortTag() != "!!str" || isNumber(tp.Value) || names[tp.Value] {
			continue
		}
		c.diag(model.SeverityWarning, "k8s_service_port", o, field("spec", "ports"+idx(i), "targetPort"),
			fmt.Sprintf("Service %s: targetPort %q is not a port name of %s", o.name, tp.Value, targets[0].label()),
			fmt.Sprintf("Name the container port (ports: [{name: %s, containerPort: …}]) or use its number.", tp.Value))
	}
}

// servicePort reports whether Service svc has a port with number n or name.
func servicePort(svc *object, port string) bool {
	for _, p := range items(svc.node, "spec", "ports") {
		if str(p, "port") == port || str(p, "name") == port {
			return true
		}
	}
	return false
}

func servicePorts(svc *object) string {
	var out []string
	for _, p := range items(svc.node, "spec", "ports") {
		s := str(p, "port")
		if n := str(p, "name"); n != "" {
			s += " (" + n + ")"
		}
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}

func (c *checker) backend(o *object, path, ns, name, port string) {
	if name == "" || strings.ContainsAny(name, "${}") {
		return
	}
	svc := c.get("Service", ns, name)
	if svc == nil {
		c.diag(model.SeverityWarning, "k8s_ingress_backend", o, path,
			fmt.Sprintf("%s %s routes to Service %s, which is not in the workflow", o.kind, o.name, name),
			"Point the backend at one of the workflow's Services, or add the Service.")
		return
	}
	if port != "" && !servicePort(svc, port) {
		c.diag(model.SeverityWarning, "k8s_ingress_backend", o, path,
			fmt.Sprintf("%s %s routes to port %s of Service %s, which has ports %s", o.kind, o.name, port, name, servicePorts(svc)),
			"Use one of the Service's ports (number or name).")
	}
}

func (c *checker) checkIngress(o *object) {
	if o.group() != "networking.k8s.io" {
		return
	}
	ingressBackend := func(path string, b *yaml.Node) {
		s := get(b, "service")
		if s == nil {
			return
		}
		port := str(s, "port", "number")
		if port == "" {
			port = str(s, "port", "name")
		}
		c.backend(o, field(path, "service"), o.namespace, str(s, "name"), port)
	}
	if b := get(o.node, "spec", "defaultBackend"); b != nil {
		ingressBackend("spec.defaultBackend", b)
	}
	for i, r := range items(o.node, "spec", "rules") {
		for j, p := range items(r, "http", "paths") {
			ingressBackend(field("spec", "rules"+idx(i), "http", "paths"+idx(j), "backend"), get(p, "backend"))
		}
	}
}

func (c *checker) checkHTTPRoute(o *object) {
	if o.group() != "gateway.networking.k8s.io" {
		return
	}
	for i, r := range items(o.node, "spec", "rules") {
		for j, b := range items(r, "backendRefs") {
			if k := str(b, "kind"); k != "" && k != "Service" || str(b, "group") != "" {
				continue
			}
			c.backend(o, field("spec", "rules"+idx(i), "backendRefs"+idx(j)), orStr(str(b, "namespace") != "", str(b, "namespace"), o.namespace), str(b, "name"), str(b, "port"))
		}
	}
}

// ---------------------------------------------------------------------------
// Autoscaling, disruption budgets, replicas

// target is the workload an HPA scales.
func (c *checker) target(ns, kind, name string) *workload {
	for _, w := range c.workloads {
		if w.o.namespace == ns && w.o.kind == kind && w.o.name == name {
			return w
		}
	}
	return nil
}

func (c *checker) checkHPA(o *object) {
	kind, name := str(o.node, "spec", "scaleTargetRef", "kind"), str(o.node, "spec", "scaleTargetRef", "name")
	if name == "" {
		return
	}
	w := c.target(o.namespace, kind, name)
	if w == nil {
		if c.get(kind, o.namespace, name) == nil {
			c.diag(model.SeverityWarning, "k8s_unknown_ref", o, "spec.scaleTargetRef",
				fmt.Sprintf("HorizontalPodAutoscaler %s scales %s %s, which is not in the workflow", o.name, kind, name),
				"Point scaleTargetRef at a Deployment or StatefulSet of the workflow.")
		}
		return
	}
	var resources []string
	if str(o.node, "spec", "targetCPUUtilizationPercentage") != "" {
		resources = append(resources, "cpu")
	}
	for _, m := range items(o.node, "spec", "metrics") {
		if str(m, "type") == "Resource" && str(m, "resource", "target", "type") == "Utilization" {
			resources = append(resources, str(m, "resource", "name"))
		}
		if str(m, "type") == "ContainerResource" && str(m, "containerResource", "target", "type") == "Utilization" {
			resources = append(resources, str(m, "containerResource", "name"))
		}
	}
	reqDefaults, _ := c.limitRangeDefaults(o.namespace)
	for _, r := range resources {
		for _, ct := range w.containers() {
			if ct.init && !ct.sidecar {
				continue
			}
			if len(missingRequests(ct.n, reqDefaults, r)) > 0 {
				c.diag(model.SeverityWarning, "k8s_hpa_no_requests", o, "spec.metrics",
					fmt.Sprintf("HorizontalPodAutoscaler %s scales on %s utilization but %s's %s has no %s request", o.name, r, w.label(), ct.label(), r),
					fmt.Sprintf("Set resources.requests.%s on every container of %s.", r, w.label()))
				break
			}
		}
	}
}

func (c *checker) checkPDB(o *object) {
	sel := labelSelector(get(o.node, "spec", "selector"))
	if sel.empty() || len(c.selected(o.namespace, sel)) > 0 || c.podsFromElsewhere(o.namespace) {
		return
	}
	fix := "Set spec.selector.matchLabels to the pod labels of the workload it protects."
	if w := c.nearest(o.namespace, sel); w != nil {
		fix = fmt.Sprintf("%s's pods are labeled %s: use those labels as spec.selector.matchLabels.", w.label(), labelsYAML(w.podLabels))
	}
	c.diag(model.SeverityWarning, "k8s_selector_mismatch", o, "spec.selector",
		fmt.Sprintf("PodDisruptionBudget %s selects no pods of the workflow (%s)", o.name, sel), fix)
}

// prodLike matches environment names of production-like environments.
func prodLike(env string) bool { return model.ProdLike(env) }

// stateful reports why a workload can't simply run more replicas: its own
// volume claim templates, a ReadWriteOnce claim or a Recreate strategy.
func (c *checker) stateful(w *workload) bool {
	return len(items(w.o.node, "spec", "volumeClaimTemplates")) > 0 || str(w.o.node, "spec", "strategy", "type") == "Recreate" || c.rwoClaim(w)
}

func (c *checker) checkReplicas() {
	if !prodLike(c.in.env) {
		return
	}
	for _, w := range c.workloads {
		if w.o.kind != "Deployment" && w.o.kind != "StatefulSet" {
			continue
		}
		replicas, set := intAt(w.o.node, "spec", "replicas")
		if !set && get(w.o.node, "spec", "replicas") == nil {
			replicas = 1
		}
		for _, o := range c.in.objects {
			if o.kind == "HorizontalPodAutoscaler" && o.namespace == w.o.namespace && str(o.node, "spec", "scaleTargetRef", "name") == w.o.name {
				if m, ok := intAt(o.node, "spec", "minReplicas"); ok && m > replicas {
					replicas = m
				} else if !ok && replicas < 1 {
					replicas = 1
				}
			}
		}
		if replicas <= 1 {
			sev := model.SeverityWarning
			fix := "Run spec.replicas: 2 or more (or an HPA with minReplicas: 2) and add a PodDisruptionBudget with minAvailable: 1."
			if c.stateful(w) {
				sev = model.SeverityInfo
				fix = "It keeps state on a single volume: run it highly available with the database's own replication (an operator or a chart with replicas), or accept the downtime."
			}
			c.diag(sev, "k8s_single_replica", w.o, "spec.replicas",
				fmt.Sprintf("%s runs a single replica in %s", w.label(), c.in.env), fix)
			continue
		}
		protected := false
		for _, o := range c.in.objects {
			if o.kind == "PodDisruptionBudget" && o.namespace == w.o.namespace {
				if sel := labelSelector(get(o.node, "spec", "selector")); !sel.empty() && sel.matches(w.podLabels) {
					protected = true
				}
			}
		}
		if !protected {
			c.diag(model.SeverityWarning, "k8s_single_replica", w.o, "spec.replicas",
				fmt.Sprintf("%s has %d replicas but no PodDisruptionBudget in %s", w.label(), replicas, c.in.env),
				fmt.Sprintf("Add a PodDisruptionBudget: {spec: {minAvailable: 1, selector: {matchLabels: %s}}}.", labelsYAML(w.podLabels)))
		}
	}
}

func (c *checker) checkNetworkPolicies() {
	for _, w := range c.workloads {
		covered := false
		for _, o := range c.in.objects {
			if o.kind != "NetworkPolicy" || o.namespace != w.o.namespace {
				continue
			}
			sel := labelSelector(get(o.node, "spec", "podSelector"))
			if sel.empty() || sel.matches(w.podLabels) {
				covered = true
				break
			}
		}
		if !covered {
			c.diag(model.SeverityInfo, "k8s_network_policy", w.o, w.podPath,
				fmt.Sprintf("%s accepts traffic from every pod of the cluster (no NetworkPolicy selects it)", w.label()),
				fmt.Sprintf("Add a NetworkPolicy with podSelector: {matchLabels: %s} allowing ingress only from its clients.", labelsYAML(w.podLabels)))
		}
	}
}
