package kuberules

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// object is one rendered object the rules read.
type object struct {
	id, apiVersion, kind, name, namespace string
	node                                  *yaml.Node
	// explicitNS: the namespace was set by the document, a kustomization
	// or the source (not defaulted to "default").
	explicitNS bool
	// vendored: a dependency chart vendored in a wrapper chart's charts/
	// folder rendered the object (a palette chart): its advice is the
	// chart maintainers' choice, tuned through values, so it is capped at
	// info and never a warning in Problems.
	vendored bool
	// locate maps a field path to the file:line that produced it.
	locate func(path string) (string, int)
}

func (o *object) group() string {
	g, _ := splitAPIVersion(o.apiVersion)
	return g
}

// input is what the rules check: the rendered objects of the active
// environment.
type input struct {
	objects []*object
	// env is the active environment ("" = base).
	env string
	// localImages are the image repositories built from the workflow.
	localImages map[string]bool
	// namespaces are created by the deploy (sources' namespaces).
	namespaces map[string]bool
}

// checker collects the findings of one run.
type checker struct {
	in     input
	schema *apiSchema
	diags  []model.Diagnostic
	// crds are the custom kinds defined by CustomResourceDefinitions of
	// the workflow: "group/version/Kind" -> schema, and their scope.
	crds       map[string]*schemaNode
	crdKinds   map[string]bool // "group/Kind"
	crdCluster map[string]bool // "group/Kind" cluster-scoped
	byKey      map[string][]*object
	workloads  []*workload
}

func (c *checker) diag(sev model.Severity, code string, o *object, path, msg, fix string) {
	r := ruleByCode[code]
	file, line := o.locate(path)
	if o.vendored && sev == model.SeverityWarning {
		sev = model.SeverityInfo
		fix = strings.TrimSpace(fix + " The object comes from a vendored chart: set it through the wrapper chart's values.yaml.")
	}
	c.diags = append(c.diags, model.Diagnostic{
		Severity: sev, Code: code, Message: msg, Detail: r.Why, Fix: fix, Docs: r.Docs,
		NodeID: o.id, Attribute: path, File: file, Line: line,
	})
}

// run checks every object.
func run(in input) []model.Diagnostic {
	c := &checker{in: in, schema: loadSchema(), crds: map[string]*schemaNode{}, crdKinds: map[string]bool{}, crdCluster: map[string]bool{}, byKey: map[string][]*object{}}
	c.collectCRDs()
	for _, o := range in.objects {
		c.byKey[o.kind+"/"+o.namespace+"/"+o.name] = append(c.byKey[o.kind+"/"+o.namespace+"/"+o.name], o)
	}
	c.checkDuplicates()
	for _, o := range in.objects {
		c.checkAPI(o)
		if w := newWorkload(o); w != nil {
			c.workloads = append(c.workloads, w)
		}
	}
	c.checkNamespaces()
	for _, w := range c.workloads {
		c.workloadRules(w)
	}
	for _, o := range in.objects {
		switch o.kind {
		case "Service":
			c.checkService(o)
		case "Ingress":
			c.checkIngress(o)
		case "HTTPRoute":
			c.checkHTTPRoute(o)
		case "HorizontalPodAutoscaler":
			c.checkHPA(o)
		case "PodDisruptionBudget":
			c.checkPDB(o)
		case "ConfigMap":
			c.checkConfigMap(o)
		case "PersistentVolumeClaim":
			if o.group() == "" && str(o.node, "spec", "storageClassName") == "" && str(o.node, "spec", "volumeName") == "" {
				c.diag(model.SeverityInfo, "k8s_pvc_storage_class", o, "spec", fmt.Sprintf("PersistentVolumeClaim %s uses the cluster's default storage class", o.name),
					"Set spec.storageClassName (local-path on the local k3s cluster; your cloud's class in production).")
			}
		case "StatefulSet":
			for i, t := range items(o.node, "spec", "volumeClaimTemplates") {
				if str(t, "spec", "storageClassName") == "" {
					c.diag(model.SeverityInfo, "k8s_pvc_storage_class", o, field("spec", "volumeClaimTemplates"+idx(i), "spec"),
						fmt.Sprintf("StatefulSet %s: volume claim template %s uses the cluster's default storage class", o.name, str(t, "metadata", "name")),
						"Set storageClassName in the claim template (local-path on the local k3s cluster; your cloud's class in production).")
				}
			}
		}
	}
	c.checkReplicas()
	c.checkTopologySpread()
	c.checkDisruptionBudgets()
	c.checkRolloutStrategy()
	c.checkRecommendedLabels()
	c.checkQuotas()
	c.checkImageDigests()
	c.checkNetworkPolicies()
	return c.diags
}

// get looks up an object of kind in namespace ns.
func (c *checker) get(kind, ns, name string) *object {
	if l := c.byKey[kind+"/"+ns+"/"+name]; len(l) > 0 {
		return l[0]
	}
	return nil
}

// ---------------------------------------------------------------------------
// Schema, API versions, duplicates

func (c *checker) collectCRDs() {
	for _, o := range c.in.objects {
		if o.kind != "CustomResourceDefinition" || o.group() != "apiextensions.k8s.io" {
			continue
		}
		group, kind := str(o.node, "spec", "group"), str(o.node, "spec", "names", "kind")
		if group == "" || kind == "" {
			continue
		}
		c.crdKinds[group+"/"+kind] = true
		if str(o.node, "spec", "scope") == "Cluster" {
			c.crdCluster[group+"/"+kind] = true
		}
		for _, v := range items(o.node, "spec", "versions") {
			sn := get(v, "schema", "openAPIV3Schema")
			if sn == nil {
				continue
			}
			var s schemaNode
			if err := sn.Decode(&s); err != nil {
				continue
			}
			// apiVersion, kind and metadata are implicit in a CRD schema.
			if s.Properties != nil {
				for _, k := range []string{"apiVersion", "kind", "metadata"} {
					if _, ok := s.Properties[k]; !ok {
						s.Properties[k] = &schemaNode{}
					}
				}
			}
			c.crds[group+"/"+str(v, "name")+"/"+kind] = &s
		}
	}
}

func (c *checker) checkDuplicates() {
	seen := map[string]*object{}
	for _, o := range c.in.objects {
		k := o.kind + "." + o.group() + "/" + o.namespace + "/" + o.name
		if first, ok := seen[k]; ok {
			file, line := first.locate("")
			c.diag(model.SeverityError, "k8s_duplicate", o, "metadata.name",
				fmt.Sprintf("%s %s is defined twice (first at %s:%d)", o.kind, qualified(o), file, line),
				"Rename one of them, or delete the duplicate document.")
			continue
		}
		seen[k] = o
	}
}

func qualified(o *object) string {
	if o.namespace == "" {
		return o.name
	}
	return o.namespace + "/" + o.name
}

func (c *checker) checkAPI(o *object) {
	if o.apiVersion == "" || o.kind == "" {
		return // k8s_object (renderer)
	}
	group, version := splitAPIVersion(o.apiVersion)
	if s := c.crds[group+"/"+version+"/"+o.kind]; s != nil {
		c.validate(o, s)
		return
	}
	if ch, ok := lookupAPI(removedAPIs, o.apiVersion, o.kind); ok && c.schema.definition(o.apiVersion, o.kind) == nil {
		msg := fmt.Sprintf("%s %s uses %s, removed in Kubernetes %s", o.kind, o.name, o.apiVersion, ch.Release)
		fix := "Remove it: " + ch.Note + "."
		if ch.Replacement != "" {
			fix = "Use apiVersion: " + ch.Replacement + "."
			if ch.Note != "" {
				fix += " Also: " + ch.Note + "."
			}
		}
		c.diag(model.SeverityError, "k8s_removed_api", o, "apiVersion", msg, fix)
		return
	}
	if !builtinGroup(group) {
		return // a custom resource whose definition lives in the cluster
	}
	def := c.schema.definition(o.apiVersion, o.kind)
	if def == nil {
		versions := c.schema.kindsOf[o.kind]
		msg := fmt.Sprintf("%s has no kind %s in Kubernetes %s", o.apiVersion, o.kind, SchemaVersion)
		fix := "Check the kind and apiVersion (kubectl api-resources lists them)."
		switch {
		case len(versions) > 0:
			fix = fmt.Sprintf("%s is served as apiVersion: %s.", o.kind, strings.Join(versions, " or "))
		default:
			if k := closestKind(o.kind, c.schema.kindsOf); k != "" {
				fix = fmt.Sprintf("Did you mean kind: %s (%s)?", k, strings.Join(c.schema.kindsOf[k], " or "))
			}
		}
		c.diag(model.SeverityError, "k8s_schema", o, "kind", msg, fix)
		return
	}
	if ch, ok := lookupAPI(deprecatedAPIs, o.apiVersion, o.kind); ok {
		fix := ch.Note
		if ch.Replacement != "" {
			fix = "Use apiVersion: " + ch.Replacement + strings.TrimSuffix(" ("+ch.Note+")", " ()") + "."
		}
		c.diag(model.SeverityWarning, "k8s_deprecated_api", o, "apiVersion", fmt.Sprintf("%s %s uses %s/%s, deprecated in Kubernetes", o.kind, o.name, o.apiVersion, o.kind), fix)
	}
	c.validate(o, def)
}

func closestKind(kind string, kinds map[string][]string) string {
	best, bestD := "", 3
	for k := range kinds {
		if strings.EqualFold(k, kind) {
			return k
		}
		if d := distance(strings.ToLower(k), strings.ToLower(kind)); d < bestD || d == bestD && best != "" && k < best {
			best, bestD = k, d
		}
	}
	return best
}

// maxSchemaIssues caps the schema findings of one object (a document of
// the wrong kind would otherwise list every field).
const maxSchemaIssues = 10

func (c *checker) validate(o *object, s *schemaNode) {
	v := &validator{defs: c.schema.Definitions}
	v.walk("", o.node, s)
	if name := str(o.node, "metadata", "name"); name != "" && len(name) > 253 {
		v.add("metadata.name", get(o.node, "metadata", "name"), "metadata.name is longer than 253 characters")
	}
	for i, is := range v.issues {
		if i == maxSchemaIssues {
			c.diag(model.SeverityError, "k8s_schema", o, "", fmt.Sprintf("%s %s: %d more schema problems", o.kind, o.name, len(v.issues)-i), "Check the object against the API reference.")
			break
		}
		c.diag(model.SeverityError, "k8s_schema", o, is.path, fmt.Sprintf("%s %s: %s", o.kind, o.name, is.msg), schemaFix(is.msg))
	}
}

func schemaFix(msg string) string {
	switch {
	case strings.Contains(msg, "quote it"):
		return "Quote the value: Kubernetes reads it as a string."
	case strings.Contains(msg, "unknown field"):
		return "Remove the field or fix its name and indentation (kubectl explain <kind>.<path> lists the fields)."
	case strings.Contains(msg, "missing required"):
		return "Add the required field."
	case strings.Contains(msg, "base64"):
		return "Move the value under stringData:, or base64-encode it."
	}
	return "Fix the value's type (kubectl explain <kind>.<path> shows it)."
}

// ---------------------------------------------------------------------------
// Namespaces

var systemNamespaces = map[string]bool{"default": true, "kube-system": true, "kube-public": true, "kube-node-lease": true}

func (c *checker) namespaced(o *object) bool {
	if c.schema.namespaced[o.apiVersion+"/"+o.kind] {
		return true
	}
	if c.crdKinds[o.group()+"/"+o.kind] {
		return !c.crdCluster[o.group()+"/"+o.kind]
	}
	return false
}

func (c *checker) checkNamespaces() {
	declared := map[string]bool{}
	for _, o := range c.in.objects {
		if o.kind == "Namespace" && o.group() == "" {
			declared[o.name] = true
		}
	}
	reported := map[string]bool{}
	for _, o := range c.in.objects {
		if !c.namespaced(o) {
			continue
		}
		ns := o.namespace
		if !o.explicitNS || ns == "default" {
			c.diag(model.SeverityInfo, "k8s_default_namespace", o, "metadata", fmt.Sprintf("%s %s goes to the default namespace", o.kind, o.name),
				"Set a namespace for the app: metadata.namespace, namespace: in the kustomization, or the source's namespace.")
			continue
		}
		if systemNamespaces[ns] || declared[ns] || c.in.namespaces[ns] || reported[ns] {
			continue
		}
		reported[ns] = true
		c.diag(model.SeverityInfo, "k8s_namespace_missing", o, "metadata.namespace",
			fmt.Sprintf("namespace %s (used by %s %s) is not created by the workflow", ns, o.kind, o.name),
			fmt.Sprintf("Add a Namespace object:\napiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n(or set it as the source's namespace).", ns))
	}
}

// ---------------------------------------------------------------------------
// Secrets

// credURLRe matches a URL carrying a password (postgres://user:pass@host).
var credURLRe = regexp.MustCompile(`^[a-z][a-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@`)

// secretKeyRe matches keys holding credentials.
var secretKeyRe = regexp.MustCompile(`(?i)(passw(or)?d|pwd|secret|token|api[_-]?key|private[_-]?key|credential|access[_-]?key|auth[_-]?key|[_-]pass$|^pass$)`)

// notSecretSuffix are keys that name a credential's location, policy or
// format rather than holding one (DB_PASSWORD_FILE, TOKEN_URL…).
var notSecretSuffix = regexp.MustCompile(`(?i)([_.-](file|path|url|uri|endpoint|name|ref|key[_-]?ref|host|port|ttl|length|len|expiry|expires|expiration|timeout|enabled|enable|required|policy|header|type|algorithm|issuer|audience|mode|dir|location|secret[_-]?name|min|max|size|count|lifetime|duration|prefix)$|^(enable|use|require)[_.-])`)

// isSecretValue reports whether key = value looks like a literal credential.
func isSecretValue(key, value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || strings.HasPrefix(v, "$(") || strings.HasPrefix(v, "${") || strings.Contains(v, "{{") {
		return false
	}
	if credURLRe.MatchString(v) {
		return true
	}
	if !secretKeyRe.MatchString(key) || notSecretSuffix.MatchString(key) {
		return false
	}
	switch strings.ToLower(v) {
	case "true", "false", "yes", "no", "on", "off", "null", "none", "disabled", "enabled":
		return false
	}
	if isNumber(v) || strings.HasPrefix(v, "/") {
		return false // a length, a timeout, a file path
	}
	return true
}

func isNumber(s string) bool {
	for _, r := range s {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return s != ""
}

func (c *checker) checkConfigMap(o *object) {
	if o.group() != "" {
		return
	}
	data := get(o.node, "data")
	if data == nil || data.Kind != yaml.MappingNode {
		return
	}
	var keys []string
	for i := 0; i+1 < len(data.Content); i += 2 {
		k, v := data.Content[i].Value, resolve(data.Content[i+1])
		if v != nil && v.Kind == yaml.ScalarNode && !strings.Contains(v.Value, "\n") && isSecretValue(k, v.Value) {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	c.diag(model.SeverityWarning, "k8s_secret_in_configmap", o, field("data", keys[0]),
		fmt.Sprintf("ConfigMap %s holds credentials: %s", o.name, strings.Join(keys, ", ")),
		fmt.Sprintf("Move them to a Secret (stringData: {%s: …}) and read it with secretKeyRef or envFrom.secretRef.", keys[0]))
}

// ---------------------------------------------------------------------------
// Label selectors

// selector is a LabelSelector (or a Service's plain selector map).
type selector struct {
	labels map[string]string
	exprs  []selectorExpr
}

type selectorExpr struct {
	key, op string
	values  []string
}

func labelSelector(n *yaml.Node) *selector {
	if n == nil {
		return nil
	}
	s := &selector{labels: strMap(n, "matchLabels")}
	for _, e := range items(n, "matchExpressions") {
		var vals []string
		for _, v := range items(e, "values") {
			vals = append(vals, v.Value)
		}
		s.exprs = append(s.exprs, selectorExpr{str(e, "key"), str(e, "operator"), vals})
	}
	return s
}

func (s *selector) empty() bool { return s == nil || len(s.labels) == 0 && len(s.exprs) == 0 }

// matches reports whether labels satisfy the selector.
func (s *selector) matches(labels map[string]string) bool {
	for k, v := range s.labels {
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	for _, e := range s.exprs {
		v, has := labels[e.key]
		switch e.op {
		case "In":
			if !has || !slices.Contains(e.values, v) {
				return false
			}
		case "NotIn":
			if has && slices.Contains(e.values, v) {
				return false
			}
		case "Exists":
			if !has {
				return false
			}
		case "DoesNotExist":
			if has {
				return false
			}
		}
	}
	return true
}

func (s *selector) String() string {
	var parts []string
	for _, k := range sortedKeys(s.labels) {
		parts = append(parts, k+"="+s.labels[k])
	}
	for _, e := range s.exprs {
		parts = append(parts, fmt.Sprintf("%s %s (%s)", e.key, e.op, strings.Join(e.values, ",")))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// selected are the workloads of namespace ns whose pods s selects.
func (c *checker) selected(ns string, s *selector) []*workload {
	var out []*workload
	for _, w := range c.workloads {
		if w.o.namespace == ns && w.o.kind != "Job" && w.o.kind != "CronJob" && s.matches(w.podLabels) {
			out = append(out, w)
		}
	}
	return out
}

// nearest is a workload of ns sharing a label with s (for fix hints).
func (c *checker) nearest(ns string, s *selector) *workload {
	for _, w := range c.workloads {
		if w.o.namespace != ns || len(w.podLabels) == 0 {
			continue
		}
		for k := range s.labels {
			if _, ok := w.podLabels[k]; ok {
				return w
			}
		}
	}
	return nil
}

func labelsYAML(m map[string]string) string {
	var parts []string
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+": "+m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
