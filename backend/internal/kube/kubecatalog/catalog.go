package kubecatalog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Item is one palette entry (GET /catalog/kubernetes).
type Item struct {
	ID string `json:"id"`
	// Group: "starter" (a workload or object skeleton), "image" (a
	// Deployment/StatefulSet from the Docker image catalog) or "chart"
	// (a pinned Helm chart added as a helm source).
	Group       string   `json:"group"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Icon        string   `json:"icon"`  // tech icon id
	Kinds       []string `json:"kinds"` // the objects it adds
	Image       string   `json:"image,omitempty"`
	Workload    string   `json:"workload,omitempty"` // Deployment | StatefulSet (images)
	Chart       *Chart   `json:"chart,omitempty"`
}

// Item groups.
const (
	GroupStarter = "starter"
	GroupImage   = "image"
	GroupChart   = "chart"
)

// starter is a skeleton of one or more objects.
type starter struct {
	Item
	build func(name, ns string) []*yaml.Node
}

// Items lists the palette: starters, images, charts.
func Items() []Item {
	out := make([]Item, 0, len(starters)+len(compose.Images())+len(charts))
	for _, s := range starters {
		out = append(out, s.Item)
	}
	for _, im := range compose.Images() {
		if it, ok := imageItem(im); ok {
			out = append(out, it)
		}
	}
	for _, c := range charts {
		out = append(out, c.item())
	}
	return out
}

// Find returns the palette entry id.
func Find(id string) (Item, bool) {
	for _, it := range Items() {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

var nameRe = regexp.MustCompile(`[^a-z0-9-]+`)

// ObjectName turns s into a DNS-1123 label ("My API" -> "my-api").
func ObjectName(s string) string {
	s = strings.Trim(nameRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 52 {
		s = strings.Trim(s[:52], "-")
	}
	return s
}

// YAML renders the documents a starter or image entry adds, named name
// (default: the entry's) in namespace ns ("" = the source's namespace).
func YAML(id, name, ns string) (string, error) {
	for _, s := range starters {
		if s.ID == id {
			return docs(s.build(orDefault(ObjectName(name), defaultName(s.ID)), ns)...), nil
		}
	}
	if imgID, ok := strings.CutPrefix(id, "image:"); ok {
		if im, found := compose.Image(imgID); found {
			if _, ok := imageItem(im); ok {
				return docs(imageObjects(im, orDefault(ObjectName(name), ObjectName(im.ID)), ns)...), nil
			}
		}
	}
	if _, ok := chartByID(id); ok {
		return "", fmt.Errorf("%s is a Helm chart: it is added as a helm source, not as YAML", id)
	}
	return "", fmt.Errorf("unknown palette entry %q", id)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// DefaultName is the name a starter or image entry gives its objects
// when the request names none.
func DefaultName(id string) string {
	if imgID, ok := strings.CutPrefix(id, "image:"); ok {
		return ObjectName(imgID)
	}
	return defaultName(id)
}

// defaultName names a starter's objects ("deployment-service" -> "web").
func defaultName(id string) string {
	switch id {
	case "deployment-service", "ingress", "httproute", "hpa", "pdb", "networkpolicy":
		return "web"
	case "statefulset":
		return "db"
	case "daemonset":
		return "agent"
	case "job":
		return "task"
	case "cronjob":
		return "report"
	case "configmap":
		return "config"
	case "secret":
		return "credentials"
	case "serviceaccount-role":
		return "reader"
	}
	return id
}

// ---------------------------------------------------------------------------
// Shared pieces

func labels(name string) *yaml.Node { return m("app", name) }

// appLabels are a workload's recommended labels: app.kubernetes.io/name is
// what it runs, instance this copy (k8s_recommended_labels).
func appLabels(app, name string) *yaml.Node {
	return m("app.kubernetes.io/name", app, "app.kubernetes.io/instance", name)
}

func requests(cpu, mem, limit string) *yaml.Node {
	return m("requests", m("cpu", cpu, "memory", mem), "limits", m("memory", limit))
}

// nonRoot is a pod securityContext running as uid.
func nonRoot(uid int, fsGroup bool) *yaml.Node {
	sc := m("runAsNonRoot", true, "runAsUser", uid)
	if fsGroup {
		sc.Content = append(sc.Content, node("fsGroup"), node(uid))
	}
	sc.Content = append(sc.Content, node("seccompProfile"), m("type", "RuntimeDefault"))
	return sc
}

func hardened(readOnlyRoot bool) *yaml.Node {
	return m("allowPrivilegeEscalation", false, "readOnlyRootFilesystem", readOnlyRoot, "capabilities", m("drop", flow(strs("ALL"))))
}

func service(name, ns string, headless bool, ports ...*yaml.Node) *yaml.Node {
	spec := m("selector", labels(name), "ports", seq(anys(ports)...))
	if headless {
		spec = m("clusterIP", "None", "selector", labels(name), "ports", seq(anys(ports)...))
	}
	return obj("v1", "Service", meta(name, ns, nil), "spec", spec)
}

func anys[T any](s []T) []any {
	out := make([]any, len(s))
	for i, v := range s {
		out[i] = v
	}
	return out
}

func svcPort(name string, port int, target string) *yaml.Node {
	return m("name", name, "port", port, "targetPort", target)
}

// ---------------------------------------------------------------------------
// Starters

var starters = []starter{
	{Item{ID: "deployment-service", Label: "Deployment + Service", Description: "A stateless web workload (NGINX, non-root on 8080) with probes, resources and a Service", Category: model.KubeCatWorkload, Kinds: []string{"Deployment", "Service"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{
				obj("apps/v1", "Deployment", meta(name, ns, appLabels(name, name)), "spec", m(
					"replicas", 1,
					"selector", m("matchLabels", labels(name)),
					"template", m("metadata", m("labels", labels(name)), "spec", m(
						"securityContext", nonRoot(101, false),
						"automountServiceAccountToken", false,
						"containers", seq(m(
							"name", name,
							"image", "nginxinc/nginx-unprivileged:1.30-alpine",
							"ports", seq(m("name", "http", "containerPort", 8080)),
							"readinessProbe", m("httpGet", m("path", "/", "port", "http"), "periodSeconds", 5),
							"livenessProbe", m("httpGet", m("path", "/", "port", "http"), "periodSeconds", 20),
							"resources", requests("50m", "64Mi", "128Mi"),
							"securityContext", hardened(false),
						)),
					)),
				)),
				service(name, ns, false, svcPort("http", 80, "http")),
			}
		}},
	{Item{ID: "statefulset", Label: "StatefulSet + headless Service + volume", Description: "A stateful workload (Redis with persistence) with its own PersistentVolumeClaim per replica and stable DNS", Category: model.KubeCatWorkload, Kinds: []string{"StatefulSet", "Service", "PersistentVolumeClaim"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{
				service(name, ns, true, svcPort("redis", 6379, "redis")),
				obj("apps/v1", "StatefulSet", meta(name, ns, appLabels(name, name)), "spec", m(
					"serviceName", name,
					"replicas", 1,
					"selector", m("matchLabels", labels(name)),
					"template", m("metadata", m("labels", labels(name)), "spec", m(
						"securityContext", nonRoot(999, true),
						"automountServiceAccountToken", false,
						"containers", seq(m(
							"name", name,
							"image", "redis:7.4-alpine",
							"args", flow(strs("--appendonly", "yes")),
							"ports", seq(m("name", "redis", "containerPort", 6379)),
							"readinessProbe", m("exec", m("command", flow(strs("redis-cli", "ping"))), "periodSeconds", 5),
							"livenessProbe", m("tcpSocket", m("port", "redis"), "periodSeconds", 20),
							"resources", requests("50m", "64Mi", "256Mi"),
							"securityContext", hardened(false),
							"volumeMounts", seq(m("name", "data", "mountPath", "/data")),
						)),
					)),
					"volumeClaimTemplates", seq(m(
						"metadata", m("name", "data"),
						"spec", m("accessModes", flow(strs("ReadWriteOnce")), "resources", m("requests", m("storage", "1Gi"))),
					)),
				)),
			}
		}},
	{Item{ID: "daemonset", Label: "DaemonSet", Description: "One pod on every node (node agents: log shippers, monitoring exporters)", Category: model.KubeCatWorkload, Kinds: []string{"DaemonSet"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("apps/v1", "DaemonSet", meta(name, ns, appLabels(name, name)), "spec", m(
				"selector", m("matchLabels", labels(name)),
				"template", m("metadata", m("labels", labels(name)), "spec", m(
					"securityContext", nonRoot(65534, false),
					"automountServiceAccountToken", false,
					"containers", seq(m(
						"name", name,
						"image", "busybox:1.37",
						"command", flow(strs("sh", "-c", "while true; do echo \"$(date) on $NODE_NAME\"; sleep 60; done")),
						"env", seq(m("name", "NODE_NAME", "valueFrom", m("fieldRef", m("fieldPath", "spec.nodeName")))),
						"resources", requests("10m", "16Mi", "32Mi"),
						"securityContext", hardened(true),
					)),
				)),
			))}
		}},
	{Item{ID: "job", Label: "Job", Description: "A task that runs to completion once (migrations, imports); retried up to twice and cleaned up after 10 minutes", Category: model.KubeCatWorkload, Kinds: []string{"Job"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("batch/v1", "Job", meta(name, ns, appLabels(name, name)), "spec", m(
				"backoffLimit", 2,
				"ttlSecondsAfterFinished", 600,
				"template", m("metadata", m("labels", labels(name)), "spec", m(
					"restartPolicy", "Never",
					"securityContext", nonRoot(65534, false),
					"automountServiceAccountToken", false,
					"containers", seq(m(
						"name", name,
						"image", "busybox:1.37",
						"command", flow(strs("sh", "-c", "echo \"job $HOSTNAME started\"; date; echo done")),
						"resources", requests("10m", "16Mi", "32Mi"),
						"securityContext", hardened(true),
					)),
				)),
			))}
		}},
	{Item{ID: "cronjob", Label: "CronJob", Description: "A task on a schedule (every 5 minutes); runs never overlap and old runs are cleaned up", Category: model.KubeCatWorkload, Kinds: []string{"CronJob"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("batch/v1", "CronJob", meta(name, ns, appLabels(name, name)), "spec", m(
				"schedule", "*/5 * * * *",
				"concurrencyPolicy", "Forbid",
				"successfulJobsHistoryLimit", 3,
				"failedJobsHistoryLimit", 1,
				"jobTemplate", m("spec", m(
					"backoffLimit", 2,
					"ttlSecondsAfterFinished", 600,
					"template", m("metadata", m("labels", labels(name)), "spec", m(
						"restartPolicy", "Never",
						"securityContext", nonRoot(65534, false),
						"automountServiceAccountToken", false,
						"containers", seq(m(
							"name", name,
							"image", "busybox:1.37",
							"command", flow(strs("sh", "-c", "echo \"report at $(date)\"")),
							"resources", requests("10m", "16Mi", "32Mi"),
							"securityContext", hardened(true),
						)),
					)),
				)),
			))}
		}},
	{Item{ID: "ingress", Label: "Ingress", Description: "HTTP routing from a host name to a Service (http://<name>.localhost on the local cluster)", Category: model.KubeCatNetwork, Kinds: []string{"Ingress"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("networking.k8s.io/v1", "Ingress", meta(name, ns, nil), "spec", m(
				"rules", seq(m("host", name+".localhost", "http", m("paths", seq(m(
					"path", "/",
					"pathType", "Prefix",
					"backend", m("service", m("name", name, "port", m("name", "http"))),
				))))),
			))}
		}},
	{Item{ID: "httproute", Label: "Gateway + HTTPRoute", Description: "Gateway API routing: a Gateway (class traefik) and an HTTPRoute to a Service. Requires a Gateway API provider: the local cluster ships the CRDs but its Traefik does not serve Gateways, so use an Ingress to reach the app locally", Category: model.KubeCatNetwork, Kinds: []string{"Gateway", "HTTPRoute"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{
				obj("gateway.networking.k8s.io/v1", "Gateway", meta(name+"-gateway", ns, nil), "spec", m(
					"gatewayClassName", "traefik",
					"listeners", seq(m("name", "http", "protocol", "HTTP", "port", 8000)),
				)),
				obj("gateway.networking.k8s.io/v1", "HTTPRoute", meta(name, ns, nil), "spec", m(
					"parentRefs", seq(m("name", name+"-gateway")),
					"hostnames", flow(strs(name+".localhost")),
					"rules", seq(m(
						"matches", seq(m("path", m("type", "PathPrefix", "value", "/"))),
						"backendRefs", seq(m("name", name, "port", 80)),
					)),
				)),
			}
		}},
	{Item{ID: "hpa", Label: "HorizontalPodAutoscaler", Description: "Scales a Deployment between 2 and 5 replicas on CPU (its containers need cpu requests)", Category: model.KubeCatAutoscaling, Kinds: []string{"HorizontalPodAutoscaler"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("autoscaling/v2", "HorizontalPodAutoscaler", meta(name, ns, nil), "spec", m(
				"scaleTargetRef", m("apiVersion", "apps/v1", "kind", "Deployment", "name", name),
				"minReplicas", 2,
				"maxReplicas", 5,
				"metrics", seq(m("type", "Resource", "resource", m("name", "cpu", "target", m("type", "Utilization", "averageUtilization", 70)))),
			))}
		}},
	{Item{ID: "pdb", Label: "PodDisruptionBudget", Description: "Keeps at least one pod of a workload running during node drains and upgrades", Category: model.KubeCatAutoscaling, Kinds: []string{"PodDisruptionBudget"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("policy/v1", "PodDisruptionBudget", meta(name, ns, nil), "spec", m(
				"minAvailable", 1,
				"selector", m("matchLabels", labels(name)),
			))}
		}},
	{Item{ID: "configmap", Label: "ConfigMap", Description: "Non-secret configuration read as environment variables (envFrom) or mounted files", Category: model.KubeCatConfig, Kinds: []string{"ConfigMap"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("v1", "ConfigMap", meta(name, ns, nil), "data", m("LOG_LEVEL", "info", "FEATURE_FLAGS", ""))}
		}},
	{Item{ID: "secret", Label: "Secret", Description: "Credentials read as environment variables (secretKeyRef/envFrom) or mounted files", Category: model.KubeCatConfig, Kinds: []string{"Secret"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{comment(obj("v1", "Secret", meta(name, ns, nil), "type", "Opaque", "stringData", m("USERNAME", "app", "PASSWORD", "change-me")),
				"Plain values under stringData: the API server stores them base64-encoded.\nKeep real credentials out of git (a .env-generated Secret, sealed-secrets or external-secrets).")}
		}},
	{Item{ID: "networkpolicy", Label: "NetworkPolicy", Description: "Allows traffic to a workload's pods only from pods of the same namespace, on its port", Category: model.KubeCatNetwork, Kinds: []string{"NetworkPolicy"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{obj("networking.k8s.io/v1", "NetworkPolicy", meta(name, ns, nil), "spec", m(
				"podSelector", m("matchLabels", labels(name)),
				"policyTypes", flow(strs("Ingress")),
				"ingress", seq(m(
					"from", seq(m("podSelector", flow(m()))),
					"ports", seq(m("protocol", "TCP", "port", 8080)),
				)),
			))}
		}},
	{Item{ID: "serviceaccount-role", Label: "ServiceAccount + Role", Description: "An identity for pods that call the Kubernetes API, allowed to read pods, services and config maps of its namespace", Category: model.KubeCatRBAC, Kinds: []string{"ServiceAccount", "Role", "RoleBinding"}},
		func(name, ns string) []*yaml.Node {
			return []*yaml.Node{
				obj("v1", "ServiceAccount", meta(name, ns, nil)),
				obj("rbac.authorization.k8s.io/v1", "Role", meta(name, ns, nil), "rules", seq(m(
					"apiGroups", flow(strs("")),
					"resources", flow(strs("pods", "services", "configmaps")),
					"verbs", flow(strs("get", "list", "watch")),
				))),
				obj("rbac.authorization.k8s.io/v1", "RoleBinding", meta(name, ns, nil),
					"subjects", seq(m("kind", "ServiceAccount", "name", name)),
					"roleRef", m("apiGroup", "rbac.authorization.k8s.io", "kind", "Role", "name", name),
				),
			}
		}},
}

func init() {
	for i := range starters {
		starters[i].Group = GroupStarter
		starters[i].Icon = "kubernetes"
	}
}

// ---------------------------------------------------------------------------
// From the Docker image catalog

// statefulCategories keep data: their images become StatefulSets with a
// volume claim per data path.
var statefulCategories = map[string]bool{"database": true, "cache": true, "search": true, "messaging": true, "storage": true}

// imageItem is the palette entry of a Docker catalog image (false for
// build-only images: no port and no command, they exit at once).
func imageItem(im model.DockerImage) (Item, bool) {
	if len(im.Ports) == 0 && im.Command == "" {
		return Item{}, false
	}
	it := Item{ID: "image:" + im.ID, Group: GroupImage, Label: im.Label, Description: im.Description, Category: im.Category, Icon: im.Icon,
		Image: im.Image + ":" + im.Tag, Workload: "Deployment"}
	if statefulCategories[im.Category] && hasNamedVolume(im) {
		it.Workload = "StatefulSet"
	}
	it.Kinds = []string{it.Workload}
	if len(im.Ports) > 0 {
		it.Kinds = append(it.Kinds, "Service")
	}
	if secretEnv(im) {
		it.Kinds = append(it.Kinds, "Secret")
	}
	return it, true
}

func hasNamedVolume(im model.DockerImage) bool {
	for _, v := range im.Volumes {
		if v.Name != "" {
			return true
		}
	}
	return false
}

func secretEnv(im model.DockerImage) bool {
	for _, e := range im.Env {
		if e.Secret {
			return true
		}
	}
	return false
}

// resourcesFor sizes requests by category.
func resourcesFor(category string) *yaml.Node {
	switch category {
	case "database", "search":
		return requests("250m", "512Mi", "1Gi")
	case "messaging", "observability", "workflow", "ai", "apps", "auth", "cms":
		return requests("100m", "256Mi", "512Mi")
	case "cache", "web", "gateway", "network", "tools":
		return requests("50m", "64Mi", "256Mi")
	}
	return requests("100m", "128Mi", "512Mi")
}

// shellTest turns a compose CMD-SHELL healthcheck into an exec probe
// command ($$ escapes become $).
func shellTest(test string) *yaml.Node {
	return flow(strs("sh", "-c", strings.ReplaceAll(test, "$$", "$")))
}

func seconds(d string, def int) int {
	d = strings.TrimSpace(d)
	if s, ok := strings.CutSuffix(d, "s"); ok {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	if s, ok := strings.CutSuffix(d, "m"); ok {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n * 60
		}
	}
	return def
}

// imageObjects are the objects of a catalog image: the workload (probes
// from the catalog's healthcheck, resources by category, a claim per data
// path for stateful images), its Service and a Secret for credentials.
func imageObjects(im model.DockerImage, name, ns string) []*yaml.Node {
	var out []*yaml.Node
	stateful := statefulCategories[im.Category] && hasNamedVolume(im)
	secretName := name + "-secrets"

	ct := m("name", name, "image", im.Image+":"+im.Tag)
	add := func(k string, v any) { ct.Content = append(ct.Content, node(k), node(v)) }
	if im.Entrypoint != "" {
		add("command", flow(strs(compose.Words(im.Entrypoint)...)))
	}
	if im.Command != "" {
		add("args", flow(strs(compose.Words(im.Command)...)))
	}
	if len(im.Ports) > 0 {
		var ports []any
		for i, p := range im.Ports {
			ports = append(ports, m("name", portName(i, p), "containerPort", p))
		}
		add("ports", seq(ports...))
	}
	var env, secrets []any
	for _, e := range im.Env {
		if e.Secret {
			secrets = append(secrets, e.Key, e.Value)
			env = append(env, m("name", e.Key, "valueFrom", m("secretKeyRef", m("name", secretName, "key", e.Key))))
			continue
		}
		env = append(env, m("name", e.Key, "value", e.Value))
	}
	if len(env) > 0 {
		add("env", seq(env...))
	}
	period := 10
	if im.Healthcheck != nil {
		period = seconds(im.Healthcheck.Interval, 10)
	}
	switch {
	case im.Healthcheck != nil:
		probe := m("exec", m("command", shellTest(im.Healthcheck.Test)), "periodSeconds", period, "timeoutSeconds", seconds(im.Healthcheck.Timeout, 5))
		if start := seconds(im.Healthcheck.StartPeriod, 0); start > 0 {
			probe.Content = append(probe.Content, node("initialDelaySeconds"), node(start))
		}
		add("readinessProbe", probe)
	case len(im.Ports) > 0:
		add("readinessProbe", m("tcpSocket", m("port", portName(0, im.Ports[0])), "periodSeconds", period))
	}
	if len(im.Ports) > 0 {
		add("livenessProbe", m("tcpSocket", m("port", portName(0, im.Ports[0])), "initialDelaySeconds", 30, "periodSeconds", 20, "failureThreshold", 6))
	}
	add("resources", resourcesFor(im.Category))
	var mounts, claims []any
	for i, v := range im.Volumes {
		if v.Name == "" || !stateful {
			continue
		}
		claim := ObjectName(v.Name)
		if claim == "" {
			claim = "data-" + strconv.Itoa(i)
		}
		mounts = append(mounts, m("name", claim, "mountPath", v.Target))
		claims = append(claims, m("metadata", m("name", claim),
			"spec", m("accessModes", flow(strs("ReadWriteOnce")), "resources", m("requests", m("storage", storageFor(im.Category))))))
	}
	if len(mounts) > 0 {
		add("volumeMounts", seq(mounts...))
	}

	pod := m("automountServiceAccountToken", false, "containers", seq(ct))
	tmpl := m("metadata", m("labels", labels(name)), "spec", pod)
	if len(secrets) > 0 {
		out = append(out, comment(obj("v1", "Secret", meta(secretName, ns, nil), "type", "Opaque", "stringData", m(secrets...)),
			"Development credentials of "+im.Label+": change them (and keep real ones out of git)."))
	}
	if len(im.Ports) > 0 {
		var ports []*yaml.Node
		for i, p := range im.Ports {
			ports = append(ports, svcPort(portName(i, p), p, portName(i, p)))
		}
		out = append(out, service(name, ns, stateful, ports...))
	}
	if stateful {
		out = append(out, obj("apps/v1", "StatefulSet", meta(name, ns, appLabels(im.ID, name)), "spec", m(
			"serviceName", name,
			"replicas", 1,
			"selector", m("matchLabels", labels(name)),
			"template", tmpl,
			"volumeClaimTemplates", seq(claims...),
		)))
		return out
	}
	out = append(out, obj("apps/v1", "Deployment", meta(name, ns, appLabels(im.ID, name)), "spec", m(
		"replicas", 1,
		"selector", m("matchLabels", labels(name)),
		"template", tmpl,
	)))
	return out
}

func storageFor(category string) string {
	switch category {
	case "database", "search", "storage":
		return "2Gi"
	}
	return "1Gi"
}

// portName names a container port (IANA names: at most 15 characters).
func portName(i, port int) string {
	switch port {
	case 80, 8080, 8000, 3000, 9898, 5000:
		if i == 0 {
			return "http"
		}
	case 443, 8443, 9443:
		if i == 0 {
			return "https"
		}
	}
	return "port-" + strconv.Itoa(port)
}
