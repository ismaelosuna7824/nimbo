package kube

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// Connect recipes (ConnectRecipe.Kind): ways to wire two objects (or to
// add the object a workload needs) by editing the files.
const (
	RecipeExpose         = "expose"         // workload: a new Service selecting its pods
	RecipeSelects        = "selects"        // Service <-> workload: the Service selects the workload's pods
	RecipeRoute          = "route"          // Ingress -> Service: a new rule
	RecipeIngress        = "ingress"        // Service: a new Ingress routing to it
	RecipeEnvFrom        = "envFrom"        // workload -> ConfigMap/Secret: every key as a variable
	RecipeVolume         = "volume"         // workload -> ConfigMap/Secret/PVC: mounted at Path
	RecipeEnv            = "env"            // workload -> Service: a variable with the Service's URL
	RecipeServiceAccount = "serviceaccount" // workload -> ServiceAccount
	RecipeScale          = "scale"          // HPA -> workload, or workload: a new HPA
)

// ConnectRecipe is one way to connect from to to
// (GET /workflows/{id}/kubernetes/connect).
type ConnectRecipe struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Port        int    `json:"port,omitempty"`
	Path        string `json:"path,omitempty"` // mount path, ingress path
	Key         string `json:"key,omitempty"`  // env variable name
}

// ConnectRequest applies a recipe (POST /workflows/{id}/kubernetes/connect).
// From/To are node IDs; Kind (or RecipeID, a recipe of the GET) says what
// to do; the other fields override the recipe's defaults.
type ConnectRequest struct {
	From     string `json:"from"`
	To       string `json:"to,omitempty"`
	RecipeID string `json:"recipeId,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Port     int    `json:"port,omitempty"`
	Path     string `json:"path,omitempty"`
	Host     string `json:"host,omitempty"`
	Key      string `json:"key,omitempty"`
	// Container picks the container of a workload (default: the first).
	Container string `json:"container,omitempty"`
}

func isWorkload(o *Object) bool {
	spec, _ := podSpec(o)
	return spec != nil && IsBuiltin(o.APIVersion)
}

// containerPorts are the container ports of a workload's pods.
func containerPorts(o *Object) []int {
	spec, _ := podSpec(o)
	var out []int
	for _, c := range list(spec["containers"]) {
		for _, pt := range list(dig(c, "ports")) {
			if n, ok := toInt(dig(pt, "containerPort")); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

func firstServicePort(o *Object) int {
	for _, pt := range list(dig(o.Obj, "spec", "ports")) {
		if n, ok := toInt(dig(pt, "port")); ok {
			return n
		}
	}
	return 0
}

// Recipes lists the ways from can be connected to to ("" = recipes that
// add a new object for from).
func (p *Project) Recipes(from, to string) ([]ConnectRecipe, error) {
	src := p.Object(from)
	if src == nil {
		return nil, &EditError{Msg: fmt.Sprintf("object %q not found", from), NotFound: true}
	}
	var dst *Object
	if to != "" {
		if dst = p.Object(to); dst == nil {
			return nil, &EditError{Msg: fmt.Sprintf("object %q not found", to), NotFound: true}
		}
		if dst == src {
			return nil, editErrorf("an object can't connect to itself")
		}
	}
	var out []ConnectRecipe
	add := func(r ConnectRecipe) {
		if r.ID == "" {
			r.ID = r.Kind
		}
		out = append(out, r)
	}
	switch {
	case dst == nil && isWorkload(src):
		port := 80
		if ps := containerPorts(src); len(ps) > 0 {
			port = ps[0]
		}
		add(ConnectRecipe{Kind: RecipeExpose, Port: port, Label: "Expose with a Service", Description: fmt.Sprintf("Add a Service named %s selecting the pods of %s on port %d.", src.Name, src.Name, port)})
		if src.Kind == "Deployment" || src.Kind == "StatefulSet" {
			add(ConnectRecipe{Kind: RecipeScale, Label: "Autoscale (HPA)", Description: fmt.Sprintf("Add a HorizontalPodAutoscaler scaling %s between 1 and 3 replicas at 80%% CPU (needs CPU requests).", src.Name)})
		}
	case dst == nil && src.Kind == "Service":
		add(ConnectRecipe{Kind: RecipeIngress, Port: firstServicePort(src), Path: "/", Label: "Publish with an Ingress", Description: fmt.Sprintf("Add an Ingress routing / to %s:%d.", src.Name, firstServicePort(src))})
	case dst == nil:
		return nil, editErrorf("%s has nothing to add on its own: pick an object to connect it to", src.Kind)
	case isWorkload(src) && (dst.Kind == "ConfigMap" || dst.Kind == "Secret"):
		add(ConnectRecipe{Kind: RecipeEnvFrom, Label: "Load every key as environment variables", Description: fmt.Sprintf("envFrom %s %s in %s's container.", dst.Kind, dst.Name, src.Name)})
		add(ConnectRecipe{Kind: RecipeVolume, Path: "/etc/" + dst.Name, Label: "Mount as files", Description: fmt.Sprintf("Mount %s %s at /etc/%s (one file per key).", dst.Kind, dst.Name, dst.Name)})
	case isWorkload(src) && dst.Kind == "PersistentVolumeClaim":
		add(ConnectRecipe{Kind: RecipeVolume, Path: "/data", Label: "Mount the volume", Description: fmt.Sprintf("Mount the claim %s at /data in %s.", dst.Name, src.Name)})
	case isWorkload(src) && dst.Kind == "ServiceAccount":
		add(ConnectRecipe{Kind: RecipeServiceAccount, Label: "Run as this ServiceAccount", Description: fmt.Sprintf("%s's pods use the ServiceAccount %s.", src.Name, dst.Name)})
	case isWorkload(src) && dst.Kind == "Service", src.Kind == "Service" && isWorkload(dst):
		w, svc := src, dst
		if src.Kind == "Service" {
			w, svc = dst, src
		}
		if !matches(strMap(dig(svc.Obj, "spec", "selector")), PodTemplateLabels(w)) {
			add(ConnectRecipe{Kind: RecipeSelects, Label: "Route the Service to these pods", Description: fmt.Sprintf("Set %s's selector to the pod labels of %s.", svc.Name, w.Name)})
		}
		if w == src {
			key := envName(svc.Name) + "_URL"
			add(ConnectRecipe{Kind: RecipeEnv, Key: key, Port: firstServicePort(svc), Label: "Set " + key, Description: fmt.Sprintf("Give %s the variable %s=%s.", w.Name, key, serviceURL(svc, w))})
		}
	case src.Kind == "Ingress" && dst.Kind == "Service":
		add(ConnectRecipe{Kind: RecipeRoute, Port: firstServicePort(dst), Path: "/" + dst.Name, Label: "Route a path to " + dst.Name, Description: fmt.Sprintf("Add a rule to %s sending /%s to %s:%d.", src.Name, dst.Name, dst.Name, firstServicePort(dst))})
	case src.Kind == "HorizontalPodAutoscaler" && isWorkload(dst):
		add(ConnectRecipe{Kind: RecipeScale, Label: "Scale " + dst.Name, Description: fmt.Sprintf("Point %s's scaleTargetRef at %s %s.", src.Name, dst.Kind, dst.Name)})
	}
	if len(out) == 0 && dst != nil {
		return nil, editErrorf("no recipe connects %s %s to %s %s", src.Kind, src.Name, dst.Kind, dst.Name)
	}
	return out, nil
}

// envName turns a name into an environment variable prefix.
func envName(s string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, s))
}

// serviceURL is how pods of w reach svc.
func serviceURL(svc, w *Object) string {
	host := svc.Name
	if svc.Namespace != w.Namespace {
		host += "." + svc.Namespace
	}
	port := firstServicePort(svc)
	scheme := "http"
	switch port {
	case 5432:
		scheme = "postgres"
	case 3306:
		scheme = "mysql"
	case 6379:
		scheme = "redis"
	case 27017:
		scheme = "mongodb"
	case 5672:
		scheme = "amqp"
	case 443:
		scheme = "https"
	}
	if port == 0 || port == 80 && scheme == "http" || port == 443 && scheme == "https" {
		return scheme + "://" + host
	}
	return scheme + "://" + host + ":" + strconv.Itoa(port)
}

// Connect applies a recipe; it returns the IDs of the objects it added.
func (e *Editor) Connect(req ConnectRequest) ([]string, error) {
	p := e.p
	src := p.Object(req.From)
	if src == nil {
		return nil, &EditError{Msg: fmt.Sprintf("object %q not found", req.From), NotFound: true}
	}
	recipes, err := p.Recipes(req.From, req.To)
	if err != nil {
		return nil, err
	}
	var rc ConnectRecipe
	want := req.RecipeID
	if want == "" {
		want = req.Kind
	}
	i := slices.IndexFunc(recipes, func(r ConnectRecipe) bool { return r.ID == want || want == "" })
	if i < 0 {
		ids := make([]string, len(recipes))
		for j, r := range recipes {
			ids[j] = r.ID
		}
		return nil, editErrorf("unknown recipe %q for these objects: use %s", want, strings.Join(ids, ", "))
	}
	rc = recipes[i]
	if req.Port != 0 {
		rc.Port = req.Port
	}
	if req.Path != "" {
		rc.Path = req.Path
	}
	if req.Key != "" {
		rc.Key = req.Key
	}
	dst := p.Object(req.To)
	ns := func(o *Object) string {
		if o.Namespace != "" && o.Namespace != firstNonEmpty(p.Source(o).Source.Namespace, "default") {
			return "\n  namespace: " + o.Namespace
		}
		return ""
	}
	switch rc.Kind {
	case RecipeExpose:
		labels := PodTemplateLabels(src)
		if len(labels) == 0 {
			return nil, editErrorf("%s's pods have no labels to select", src.Name)
		}
		doc := fmt.Sprintf("apiVersion: v1\nkind: Service\nmetadata:\n  name: %s%s\nspec:\n  selector:\n%s  ports:\n    - name: http\n      port: %d\n      targetPort: %d\n",
			src.Name, ns(src), yamlMap(labels, 4), rc.Port, rc.Port)
		return e.addFor(src, doc)
	case RecipeIngress:
		doc := fmt.Sprintf("apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: %s%s\nspec:\n  rules:\n    - http:\n        paths:\n          - path: %s\n            pathType: Prefix\n            backend:\n              service:\n                name: %s\n                port:\n                  number: %d\n",
			src.Name, ns(src), firstNonEmpty(rc.Path, "/"), src.Name, rc.Port)
		if req.Host != "" {
			doc = strings.Replace(doc, "    - http:", "    - host: "+req.Host+"\n      http:", 1)
		}
		return e.addFor(src, doc)
	case RecipeScale:
		target := src
		if dst != nil {
			target = dst
		}
		if src.Kind == "HorizontalPodAutoscaler" {
			if err := e.Set(src, "spec.scaleTargetRef.kind", target.Kind); err != nil {
				return nil, err
			}
			if err := e.Set(src, "spec.scaleTargetRef.apiVersion", target.APIVersion); err != nil {
				return nil, err
			}
			return nil, e.Set(src, "spec.scaleTargetRef.name", target.Name)
		}
		doc := fmt.Sprintf("apiVersion: autoscaling/v2\nkind: HorizontalPodAutoscaler\nmetadata:\n  name: %s%s\nspec:\n  scaleTargetRef:\n    apiVersion: %s\n    kind: %s\n    name: %s\n  minReplicas: 1\n  maxReplicas: 3\n  metrics:\n    - type: Resource\n      resource:\n        name: cpu\n        target:\n          type: Utilization\n          averageUtilization: 80\n",
			target.Name, ns(target), target.APIVersion, target.Kind, target.Name)
		return e.addFor(target, doc)
	case RecipeSelects:
		w, svc := src, dst
		if src.Kind == "Service" {
			w, svc = dst, src
		}
		labels := PodTemplateLabels(w)
		if len(labels) == 0 {
			return nil, editErrorf("%s's pods have no labels to select", w.Name)
		}
		m := map[string]any{}
		for k, v := range labels {
			m[k] = v
		}
		return nil, e.Set(svc, "spec.selector", m)
	case RecipeRoute:
		rule := map[string]any{"http": map[string]any{"paths": []any{map[string]any{
			"path": firstNonEmpty(rc.Path, "/"), "pathType": "Prefix",
			"backend": map[string]any{"service": map[string]any{"name": dst.Name, "port": map[string]any{"number": rc.Port}}},
		}}}}
		if req.Host != "" {
			rule = map[string]any{"host": req.Host, "http": rule["http"]}
		}
		return nil, e.Append(src, "spec.rules", rule)
	}
	c, err := container(src, req.Container)
	if err != nil {
		return nil, err
	}
	base := PodSpecPath(src)
	cpath := base + ".containers[name=" + c + "]"
	switch rc.Kind {
	case RecipeEnvFrom:
		ref := "configMapRef"
		if dst.Kind == "Secret" {
			ref = "secretRef"
		}
		return nil, e.Append(src, cpath+".envFrom", map[string]any{ref: map[string]any{"name": dst.Name}})
	case RecipeVolume:
		vol := map[string]any{"name": dst.Name}
		switch dst.Kind {
		case "ConfigMap":
			vol["configMap"] = map[string]any{"name": dst.Name}
		case "Secret":
			vol["secret"] = map[string]any{"secretName": dst.Name}
		default:
			vol["persistentVolumeClaim"] = map[string]any{"claimName": dst.Name}
		}
		if err := e.Append(src, base+".volumes", vol); err != nil {
			return nil, err
		}
		mount := map[string]any{"name": dst.Name, "mountPath": firstNonEmpty(rc.Path, "/data")}
		if dst.Kind != "PersistentVolumeClaim" {
			mount["readOnly"] = true
		}
		return nil, e.Append(src, cpath+".volumeMounts", mount)
	case RecipeEnv:
		return nil, e.Set(src, cpath+".env[name="+rc.Key+"].value", serviceURL(dst, src))
	case RecipeServiceAccount:
		return nil, e.Set(src, base+".serviceAccountName", dst.Name)
	}
	return nil, editErrorf("unknown recipe %q", rc.Kind)
}

// container picks a workload's container by name (the first by default).
func container(o *Object, name string) (string, error) {
	spec, _ := podSpec(o)
	cs := list(spec["containers"])
	if len(cs) == 0 {
		return "", editErrorf("%s has no container", o.Name)
	}
	if name == "" {
		return str(cs[0], "name"), nil
	}
	for _, c := range cs {
		if str(c, "name") == name {
			return name, nil
		}
	}
	return "", &EditError{Msg: fmt.Sprintf("%s has no container %q", o.Name, name), NotFound: true}
}

// addFor adds a new object in the source of o (next to it for manifests).
func (e *Editor) addFor(o *Object, doc string) ([]string, error) {
	_, objs, err := e.AddObjects(o.Source, doc, "")
	if err != nil {
		return nil, err
	}
	def := firstNonEmpty(e.p.Source(o).Source.Namespace, "default")
	ids := make([]string, len(objs))
	for i, n := range objs {
		ids[i] = ObjectID(n, def)
	}
	return ids, nil
}

// ObjectID is the node ID a parsed object gets once rendered in a source
// whose default namespace is defaultNamespace.
func ObjectID(o *Object, defaultNamespace string) string {
	ns := o.Namespace
	if isClusterScoped(o.Kind, apiGroup(o.APIVersion)) {
		ns = ""
	} else if ns == "" {
		ns = defaultNamespace
	}
	return model.KubeNodeID(nodeKind(o.APIVersion, o.Kind), ns, o.Name)
}

// Append adds an item to the list at fieldPath of o (created when
// missing), in the object's manifest document.
func (e *Editor) Append(o *Object, fieldPath string, item any) error {
	segs, err := ParsePath(fieldPath)
	if err != nil {
		return editErrorf("%v", err)
	}
	if o.Doc < 0 || o.generated {
		return editErrorf("%s is rendered by %s: edit it there in code", o.ID, o.File)
	}
	f, s, err := e.segment(o.File, o.Doc)
	if err != nil {
		return err
	}
	keys, missing, found := concrete(docValue(f, o.Doc), segs)
	if found && !missing {
		if n := s.Doc.Node(keys...); n != nil && n.Kind == yaml.SequenceNode {
			if err := s.Doc.AppendItem(keys, toNode(item, nil)); err != nil {
				return editErrorf("%v", err)
			}
			f.Sync(s)
			e.touch(o.File)
			return nil
		}
	}
	if err := setSegs(s.Doc, segs, []any{item}); err != nil {
		return editErrorf("%v", err)
	}
	f.Sync(s)
	e.touch(o.File)
	return nil
}

// yamlMap renders a string map as block YAML at indent.
func yamlMap(m map[string]string, indent int) string {
	var b strings.Builder
	for _, k := range slices.Sorted(maps.Keys(m)) {
		b.WriteString(strings.Repeat(" ", indent) + yamlScalar(k) + ": " + yamlScalar(m[k]) + "\n")
	}
	return b.String()
}

func yamlScalar(s string) string {
	out, err := yaml.Marshal(s)
	if err != nil {
		return strconv.Quote(s)
	}
	return strings.TrimSpace(string(out))
}
