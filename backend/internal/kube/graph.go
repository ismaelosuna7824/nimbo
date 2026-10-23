package kube

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Graph derives the canvas from the rendered objects; node and chart
// positions are kept from prev by ID.
func (p *Project) Graph(prev *model.KubeGraph) model.KubeGraph {
	pos := map[string]*model.Position{}
	if prev != nil {
		for _, n := range prev.Nodes {
			if n.Position != nil {
				pos[n.ID] = n.Position
			}
		}
		for _, c := range prev.Charts {
			if c.Position != nil {
				pos[c.ID] = c.Position
			}
		}
	}
	g := model.KubeGraph{Nodes: []model.KubeNode{}, Edges: []model.KubeEdge{}}
	charts := map[string]int{}
	for _, o := range p.Objects {
		r := p.Source(o)
		n := model.KubeNode{
			ID: o.ID, Kind: o.Kind, APIVersion: o.APIVersion, Name: o.Name, Namespace: o.Namespace,
			Category: Category(o.APIVersion, o.Kind), Position: pos[o.ID], Source: o.Source,
			File: o.File, Line: o.Line, Labels: strMap(dig(o.Obj, "metadata", "labels")), Custom: !IsBuiltin(o.APIVersion),
		}
		if r != nil {
			n.SourceKind, n.SourcePath = r.Source.Kind, r.Source.Path
			n.Editable = r.Source.Kind != model.KubeHelm && o.Doc >= 0 && !o.generated
			if r.helm != nil {
				if sub, ok := r.helm.thirdPartyOf(o.Template); ok {
					n.Chart = "chart:" + joinRel(r.helm.chartDir, sub)
					i, seen := charts[n.Chart]
					if !seen {
						i = len(g.Charts)
						charts[n.Chart] = i
						tp := r.helm.thirdParty[sub]
						g.Charts = append(g.Charts, model.KubeChartGroup{
							ID: n.Chart, Source: o.Source, SourcePath: r.Source.Path, Chart: tp.name, Version: tp.version, AppVersion: tp.appVersion,
							Repository: tp.repository, Release: r.helm.release, Namespace: r.helm.namespace,
							ValuesFiles: slices.Clone(r.helm.valuesFiles), Position: pos[n.Chart],
						})
					}
					g.Charts[i].Objects++
				}
			}
		}
		facts(o, &n)
		g.Nodes = append(g.Nodes, n)
	}
	b := edgeBuilder{p: p, g: &g, seen: map[string]int{}}
	b.build()
	return g
}

// facts fills the kind-specific facts of a node.
func facts(o *Object, n *model.KubeNode) {
	if spec, _ := podSpec(o); spec != nil && n.Category == model.KubeCatWorkload {
		w := &model.KubeWorkload{
			Selector:       strMap(dig(o.Obj, "spec", "selector", "matchLabels")),
			PodLabels:      PodTemplateLabels(o),
			ServiceAccount: firstNonEmpty(str(spec, "serviceAccountName"), str(spec, "serviceAccount")),
			Schedule:       str(o.Obj, "spec", "schedule"),
			ServiceName:    str(o.Obj, "spec", "serviceName"),
			Containers:     containers(spec["containers"]),
		}
		if o.Kind == "ReplicationController" {
			w.Selector = strMap(dig(o.Obj, "spec", "selector"))
		}
		if v, ok := toInt(dig(o.Obj, "spec", "replicas")); ok {
			w.Replicas = &v
		}
		if ic := containers(spec["initContainers"]); len(ic) > 0 {
			w.InitContainers = ic
		}
		for _, v := range list(spec["volumes"]) {
			w.Volumes = append(w.Volumes, volumeOf(v))
		}
		for _, t := range list(dig(o.Obj, "spec", "volumeClaimTemplates")) {
			w.VolumeClaimTemplates = append(w.VolumeClaimTemplates, model.KubeClaimTemplate{
				Name: str(t, "metadata", "name"), StorageClass: str(t, "spec", "storageClassName"),
				Size: str(t, "spec", "resources", "requests", "storage"),
			})
		}
		n.Workload = w
		seen := map[string]bool{}
		for _, c := range append(slices.Clone(w.Containers), w.InitContainers...) {
			if c.ImageRef != nil && !seen[c.ImageRef.Repo] {
				seen[c.ImageRef.Repo] = true
				n.Stack = append(n.Stack, c.ImageRef.Repo)
			}
		}
	}
	switch o.Kind {
	case "Service":
		s := &model.KubeServiceFacts{Type: firstNonEmpty(str(o.Obj, "spec", "type"), "ClusterIP"), Selector: strMap(dig(o.Obj, "spec", "selector")), External: str(o.Obj, "spec", "externalName")}
		s.Headless = str(o.Obj, "spec", "clusterIP") == "None"
		s.Ports = []model.KubeServicePort{}
		for _, pt := range list(dig(o.Obj, "spec", "ports")) {
			port, _ := toInt(dig(pt, "port"))
			np, _ := toInt(dig(pt, "nodePort"))
			s.Ports = append(s.Ports, model.KubeServicePort{Name: str(pt, "name"), Port: port, TargetPort: str(pt, "targetPort"), NodePort: np, Protocol: str(pt, "protocol")})
		}
		n.Service = s
	case "Ingress":
		n.Routes = ingressRoutes(o)
	case "HTTPRoute", "GRPCRoute":
		n.Routes = httpRoutes(o)
	case "HorizontalPodAutoscaler":
		sc := &model.KubeScaler{MinReplicas: 1}
		ref := dig(o.Obj, "spec", "scaleTargetRef")
		sc.Target = model.KubeNodeID(nodeKind(str(ref, "apiVersion"), str(ref, "kind")), o.Namespace, str(ref, "name"))
		if v, ok := toInt(dig(o.Obj, "spec", "minReplicas")); ok {
			sc.MinReplicas = v
		}
		sc.MaxReplicas, _ = toInt(dig(o.Obj, "spec", "maxReplicas"))
		sc.CPU, _ = toInt(dig(o.Obj, "spec", "targetCPUUtilizationPercentage"))
		for _, m := range list(dig(o.Obj, "spec", "metrics")) {
			if str(m, "type") != "Resource" {
				continue
			}
			v, _ := toInt(dig(m, "resource", "target", "averageUtilization"))
			if v == 0 {
				v, _ = toInt(dig(m, "resource", "targetAverageUtilization"))
			}
			switch str(m, "resource", "name") {
			case "cpu":
				sc.CPU = v
			case "memory":
				sc.Memory = v
			}
		}
		n.Scaler = sc
	case "PodDisruptionBudget":
		n.Disruption = &model.KubeDisruption{MinAvailable: str(o.Obj, "spec", "minAvailable"), MaxUnavailable: str(o.Obj, "spec", "maxUnavailable"), Selector: strMap(dig(o.Obj, "spec", "selector", "matchLabels"))}
	case "PersistentVolumeClaim":
		n.Storage = &model.KubeStorage{StorageClass: str(o.Obj, "spec", "storageClassName"), Size: str(o.Obj, "spec", "resources", "requests", "storage"), AccessModes: anyStrings(dig(o.Obj, "spec", "accessModes"))}
	case "PersistentVolume":
		n.Storage = &model.KubeStorage{StorageClass: str(o.Obj, "spec", "storageClassName"), Size: str(o.Obj, "spec", "capacity", "storage"), AccessModes: anyStrings(dig(o.Obj, "spec", "accessModes"))}
	case "StorageClass":
		n.Storage = &model.KubeStorage{Provisioner: str(o.Obj, "provisioner"), Default: str(o.Obj, "metadata", "annotations", "storageclass.kubernetes.io/is-default-class") == "true"}
	case "ConfigMap", "Secret":
		keys := map[string]bool{}
		for _, f := range []string{"data", "binaryData", "stringData"} {
			if m, ok := o.Obj[f].(map[string]any); ok {
				for k := range m {
					keys[k] = true
				}
			}
		}
		n.Data = slices.Sorted(maps.Keys(keys))
	}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func containers(v any) []model.KubeContainer {
	out := []model.KubeContainer{}
	for _, c := range list(v) {
		kc := model.KubeContainer{
			Name: str(c, "name"), Image: str(c, "image"), PullPolicy: str(c, "imagePullPolicy"),
			Command: anyStrs(dig(c, "command")), Args: anyStrs(dig(c, "args")),
			Ports: []model.KubeContainerPort{}, Env: []model.KubeEnv{}, EnvFrom: []model.KubeEnvFrom{}, VolumeMounts: []model.KubeVolumeMount{},
		}
		kc.ImageRef = compose.ParseImage(kc.Image)
		for _, pt := range list(dig(c, "ports")) {
			cp, _ := toInt(dig(pt, "containerPort"))
			kc.Ports = append(kc.Ports, model.KubeContainerPort{Name: str(pt, "name"), ContainerPort: cp, Protocol: str(pt, "protocol")})
		}
		kc.Probes.Liveness = probe(dig(c, "livenessProbe"))
		kc.Probes.Readiness = probe(dig(c, "readinessProbe"))
		kc.Probes.Startup = probe(dig(c, "startupProbe"))
		kc.Resources = model.KubeResources{
			Requests: model.KubeQuantities{CPU: str(c, "resources", "requests", "cpu"), Memory: str(c, "resources", "requests", "memory")},
			Limits:   model.KubeQuantities{CPU: str(c, "resources", "limits", "cpu"), Memory: str(c, "resources", "limits", "memory")},
		}
		for _, e := range list(dig(c, "env")) {
			ke := model.KubeEnv{Name: str(e, "name")}
			vf := dig(e, "valueFrom")
			switch {
			case dig(vf, "configMapKeyRef") != nil:
				ke.From = &model.KubeRef{Kind: "ConfigMap", Name: str(vf, "configMapKeyRef", "name"), Key: str(vf, "configMapKeyRef", "key")}
			case dig(vf, "secretKeyRef") != nil:
				ke.From = &model.KubeRef{Kind: "Secret", Name: str(vf, "secretKeyRef", "name"), Key: str(vf, "secretKeyRef", "key")}
			case dig(vf, "fieldRef") != nil:
				ke.From = &model.KubeRef{Kind: "Field", Key: str(vf, "fieldRef", "fieldPath")}
			case dig(vf, "resourceFieldRef") != nil:
				ke.From = &model.KubeRef{Kind: "Resource", Key: str(vf, "resourceFieldRef", "resource")}
			default:
				val := str(e, "value")
				if compose.IsSecretKey(ke.Name) && val != "" {
					ke.Secret = true
				} else {
					ke.Value = &val
				}
			}
			kc.Env = append(kc.Env, ke)
		}
		for _, e := range list(dig(c, "envFrom")) {
			switch {
			case dig(e, "configMapRef") != nil:
				kc.EnvFrom = append(kc.EnvFrom, model.KubeEnvFrom{Kind: "ConfigMap", Name: str(e, "configMapRef", "name"), Prefix: str(e, "prefix")})
			case dig(e, "secretRef") != nil:
				kc.EnvFrom = append(kc.EnvFrom, model.KubeEnvFrom{Kind: "Secret", Name: str(e, "secretRef", "name"), Prefix: str(e, "prefix")})
			}
		}
		for _, m := range list(dig(c, "volumeMounts")) {
			ro, _ := dig(m, "readOnly").(bool)
			kc.VolumeMounts = append(kc.VolumeMounts, model.KubeVolumeMount{Name: str(m, "name"), MountPath: str(m, "mountPath"), SubPath: str(m, "subPath"), ReadOnly: ro})
		}
		out = append(out, kc)
	}
	return out
}

func anyStrs(v any) []string {
	var out []string
	for _, it := range list(v) {
		out = append(out, fmt.Sprint(it))
	}
	return out
}

func probe(v any) *model.KubeProbe {
	if v == nil {
		return nil
	}
	p := &model.KubeProbe{}
	switch {
	case dig(v, "httpGet") != nil:
		p.Type, p.Path, p.Port = "http", str(v, "httpGet", "path"), str(v, "httpGet", "port")
	case dig(v, "tcpSocket") != nil:
		p.Type, p.Port = "tcp", str(v, "tcpSocket", "port")
	case dig(v, "grpc") != nil:
		p.Type, p.Port = "grpc", str(v, "grpc", "port")
	case dig(v, "exec") != nil:
		p.Type, p.Command = "exec", strings.Join(anyStrs(dig(v, "exec", "command")), " ")
	default:
		return nil
	}
	p.InitialDelaySeconds, _ = toInt(dig(v, "initialDelaySeconds"))
	p.PeriodSeconds, _ = toInt(dig(v, "periodSeconds"))
	p.TimeoutSeconds, _ = toInt(dig(v, "timeoutSeconds"))
	p.FailureThreshold, _ = toInt(dig(v, "failureThreshold"))
	return p
}

func volumeOf(v any) model.KubeVolume {
	kv := model.KubeVolume{Name: str(v, "name"), Kind: "other"}
	switch {
	case dig(v, "configMap") != nil:
		kv.Kind, kv.Source = "configMap", str(v, "configMap", "name")
	case dig(v, "secret") != nil:
		kv.Kind, kv.Source = "secret", str(v, "secret", "secretName")
	case dig(v, "persistentVolumeClaim") != nil:
		kv.Kind, kv.Source = "persistentVolumeClaim", str(v, "persistentVolumeClaim", "claimName")
	case dig(v, "emptyDir") != nil:
		kv.Kind = "emptyDir"
	case dig(v, "hostPath") != nil:
		kv.Kind, kv.Source = "hostPath", str(v, "hostPath", "path")
	case dig(v, "projected") != nil:
		kv.Kind = "projected"
	}
	return kv
}

func ingressRoutes(o *Object) []model.KubeRoute {
	class := firstNonEmpty(str(o.Obj, "spec", "ingressClassName"), str(o.Obj, "metadata", "annotations", "kubernetes.io/ingress.class"))
	tls := map[string]bool{}
	for _, t := range list(dig(o.Obj, "spec", "tls")) {
		for _, h := range anyStrings(dig(t, "hosts")) {
			tls[h] = true
		}
	}
	backend := func(b any) (string, string) {
		if svc := dig(b, "service"); svc != nil {
			return str(svc, "name"), firstNonEmpty(str(svc, "port", "number"), str(svc, "port", "name"))
		}
		return str(b, "serviceName"), str(b, "servicePort") // networking/v1beta1
	}
	var out []model.KubeRoute
	if b := firstNonNil(dig(o.Obj, "spec", "defaultBackend"), dig(o.Obj, "spec", "backend")); b != nil {
		svc, port := backend(b)
		if svc != "" {
			out = append(out, model.KubeRoute{Service: svc, Port: port, Class: class})
		}
	}
	for _, rule := range list(dig(o.Obj, "spec", "rules")) {
		host := str(rule, "host")
		for _, pth := range list(dig(rule, "http", "paths")) {
			svc, port := backend(dig(pth, "backend"))
			if svc == "" {
				continue
			}
			out = append(out, model.KubeRoute{Host: host, Path: str(pth, "path"), Service: svc, Port: port, TLS: tls[host], Class: class})
		}
	}
	return out
}

func firstNonNil(v ...any) any {
	for _, x := range v {
		if x != nil {
			return x
		}
	}
	return nil
}

func httpRoutes(o *Object) []model.KubeRoute {
	hosts := anyStrings(dig(o.Obj, "spec", "hostnames"))
	host := strings.Join(hosts, ",")
	class := ""
	if refs := list(dig(o.Obj, "spec", "parentRefs")); len(refs) > 0 {
		class = str(refs[0], "name")
	}
	var out []model.KubeRoute
	for _, rule := range list(dig(o.Obj, "spec", "rules")) {
		pth := ""
		if m := list(dig(rule, "matches")); len(m) > 0 {
			pth = str(m[0], "path", "value")
		}
		for _, b := range list(dig(rule, "backendRefs")) {
			if k := str(b, "kind"); k != "" && k != "Service" {
				continue
			}
			out = append(out, model.KubeRoute{Host: host, Path: pth, Service: str(b, "name"), Port: str(b, "port"), Class: class})
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Edges

type edgeBuilder struct {
	p    *Project
	g    *model.KubeGraph
	seen map[string]int // edge key -> index
}

func (b *edgeBuilder) node(id string) *model.KubeNode {
	for i := range b.g.Nodes {
		if b.g.Nodes[i].ID == id {
			return &b.g.Nodes[i]
		}
	}
	return nil
}

// find returns the node of kind (bare Kind of a built-in group) in ns.
func (b *edgeBuilder) find(kind, ns, name string) *model.KubeNode {
	if name == "" {
		return nil
	}
	return b.node(model.KubeNodeID(kind, ns, name))
}

func (b *edgeBuilder) add(kind, source, target, label string) {
	if source == target {
		return
	}
	key := kind + ":" + source + "->" + target
	if i, ok := b.seen[key]; ok {
		e := &b.g.Edges[i]
		if label != "" && !slices.Contains(strings.Split(e.Label, ", "), label) {
			if e.Label == "" {
				e.Label = label
			} else {
				e.Label += ", " + label
			}
		}
		return
	}
	b.seen[key] = len(b.g.Edges)
	b.g.Edges = append(b.g.Edges, model.KubeEdge{ID: key, Source: source, Target: target, Kind: kind, Label: label})
}

func matches(selector, labels map[string]string) bool {
	if len(selector) == 0 || len(labels) == 0 {
		return false
	}
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (b *edgeBuilder) workloads(ns string, selector map[string]string) []*model.KubeNode {
	var out []*model.KubeNode
	for i := range b.g.Nodes {
		n := &b.g.Nodes[i]
		if n.Workload != nil && n.Namespace == ns && matches(selector, n.Workload.PodLabels) {
			out = append(out, n)
		}
	}
	return out
}

func (b *edgeBuilder) build() {
	for i := range b.g.Nodes {
		n := &b.g.Nodes[i]
		switch {
		case n.Service != nil:
			if len(n.Service.Selector) > 0 {
				ws := b.workloads(n.Namespace, n.Service.Selector)
				for _, w := range ws {
					b.add(model.KubeEdgeSelects, n.ID, w.ID, "")
				}
				n.Service.Unmatched = len(ws) == 0
			}
		case n.Routes != nil:
			for _, rt := range n.Routes {
				if svc := b.find("Service", n.Namespace, rt.Service); svc != nil {
					b.add(model.KubeEdgeRoutes, n.ID, svc.ID, rt.Port)
				}
			}
		case n.Scaler != nil:
			if b.node(n.Scaler.Target) != nil {
				b.add(model.KubeEdgeScales, n.ID, n.Scaler.Target, "")
			}
		case n.Disruption != nil:
			for _, w := range b.workloads(n.Namespace, n.Disruption.Selector) {
				b.add(model.KubeEdgeDisrupts, n.ID, w.ID, "")
			}
		case n.Kind == "PersistentVolumeClaim" && n.Storage != nil:
			if sc := b.find("StorageClass", "", n.Storage.StorageClass); sc != nil {
				b.add(model.KubeEdgeStorageClass, n.ID, sc.ID, "")
			}
			o := b.p.Object(n.ID)
			if o != nil {
				if pv := b.find("PersistentVolume", "", str(o.Obj, "spec", "volumeName")); pv != nil {
					b.add(model.KubeEdgeStorageClass, n.ID, pv.ID, "")
				}
			}
		case n.Kind == "PersistentVolume" && n.Storage != nil:
			if sc := b.find("StorageClass", "", n.Storage.StorageClass); sc != nil {
				b.add(model.KubeEdgeStorageClass, n.ID, sc.ID, "")
			}
		case n.Kind == "RoleBinding" || n.Kind == "ClusterRoleBinding":
			o := b.p.Object(n.ID)
			if o == nil {
				continue
			}
			ref := dig(o.Obj, "roleRef")
			roleNS := n.Namespace
			if str(ref, "kind") == "ClusterRole" {
				roleNS = ""
			}
			if role := b.find(str(ref, "kind"), roleNS, str(ref, "name")); role != nil {
				b.add(model.KubeEdgeBinds, n.ID, role.ID, "")
			}
			for _, s := range list(o.Obj["subjects"]) {
				if str(s, "kind") != "ServiceAccount" {
					continue
				}
				if sa := b.find("ServiceAccount", firstNonEmpty(str(s, "namespace"), n.Namespace), str(s, "name")); sa != nil {
					b.add(model.KubeEdgeBinds, n.ID, sa.ID, "")
				}
			}
		case n.Kind == "NetworkPolicy":
			o := b.p.Object(n.ID)
			if o == nil {
				continue
			}
			for _, w := range b.workloads(n.Namespace, strMap(dig(o.Obj, "spec", "podSelector", "matchLabels"))) {
				b.add(model.KubeEdgeNetpol, n.ID, w.ID, "")
			}
		}
		if n.Workload != nil {
			b.workloadEdges(n)
		}
	}
	b.inferred()
}

func (b *edgeBuilder) workloadEdges(n *model.KubeNode) {
	w := n.Workload
	ref := func(kind, name string) *model.KubeNode { return b.find(kind, n.Namespace, name) }
	for _, c := range append(slices.Clone(w.Containers), w.InitContainers...) {
		for _, e := range c.Env {
			if e.From != nil && (e.From.Kind == "ConfigMap" || e.From.Kind == "Secret") {
				if t := ref(e.From.Kind, e.From.Name); t != nil {
					b.add(model.KubeEdgeEnv, n.ID, t.ID, e.Name)
				}
			}
		}
		for _, e := range c.EnvFrom {
			if t := ref(e.Kind, e.Name); t != nil {
				b.add(model.KubeEdgeEnvFrom, n.ID, t.ID, "")
			}
		}
	}
	mounts := map[string][]string{}
	for _, c := range w.Containers {
		for _, m := range c.VolumeMounts {
			mounts[m.Name] = append(mounts[m.Name], m.MountPath)
		}
	}
	for _, v := range w.Volumes {
		label := strings.Join(mounts[v.Name], ", ")
		switch v.Kind {
		case "configMap":
			if t := ref("ConfigMap", v.Source); t != nil {
				b.add(model.KubeEdgeVolume, n.ID, t.ID, label)
			}
		case "secret":
			if t := ref("Secret", v.Source); t != nil {
				b.add(model.KubeEdgeVolume, n.ID, t.ID, label)
			}
		case "persistentVolumeClaim":
			if t := ref("PersistentVolumeClaim", v.Source); t != nil {
				b.add(model.KubeEdgePVC, n.ID, t.ID, label)
			}
		case "projected":
			if o := b.p.Object(n.ID); o != nil {
				spec, _ := podSpec(o)
				for _, vol := range list(spec["volumes"]) {
					if str(vol, "name") != v.Name {
						continue
					}
					for _, s := range list(dig(vol, "projected", "sources")) {
						if t := ref("ConfigMap", str(s, "configMap", "name")); t != nil {
							b.add(model.KubeEdgeVolume, n.ID, t.ID, label)
						}
						if t := ref("Secret", str(s, "secret", "name")); t != nil {
							b.add(model.KubeEdgeVolume, n.ID, t.ID, label)
						}
					}
				}
			}
		}
	}
	if sa := ref("ServiceAccount", w.ServiceAccount); sa != nil {
		b.add(model.KubeEdgeServiceAccount, n.ID, sa.ID, "")
	}
	if n.Kind == "StatefulSet" {
		if svc := ref("Service", w.ServiceName); svc != nil {
			b.add(model.KubeEdgeGoverns, n.ID, svc.ID, "")
		}
	}
}

// inferred adds workload -> Service edges for env values naming a
// Service's DNS name ("db", "db.prod", "db.prod.svc.cluster.local", in
// URLs or host:port).
func (b *edgeBuilder) inferred() {
	type svc struct {
		id, name, ns string
		re           *regexp.Regexp
	}
	var svcs []svc
	for _, n := range b.g.Nodes {
		if n.Service == nil {
			continue
		}
		q := regexp.QuoteMeta(n.Name)
		nsq := regexp.QuoteMeta(n.Namespace)
		re := regexp.MustCompile(`(?:^|[/@=,;\s"'])` + q + `(\.` + nsq + `(\.svc(\.cluster\.local)?)?)?(?::\d+|[/,;?\s"']|$)`)
		svcs = append(svcs, svc{id: n.ID, name: n.Name, ns: n.Namespace, re: re})
	}
	if len(svcs) == 0 {
		return
	}
	for i := range b.g.Nodes {
		n := &b.g.Nodes[i]
		if n.Workload == nil {
			continue
		}
		// Env values, then command/args tokens ("--cache=tcp://redis:6379").
		type ref struct{ value, envKey, label string }
		var refs []ref
		for _, c := range append(slices.Clone(n.Workload.Containers), n.Workload.InitContainers...) {
			for _, e := range c.Env {
				if e.Value != nil && *e.Value != "" {
					refs = append(refs, ref{*e.Value, e.Name, e.Name})
				}
			}
			for _, a := range append(slices.Clone(c.Command), c.Args...) {
				refs = append(refs, ref{a, "", "args"})
			}
		}
		for _, r := range refs {
			for _, s := range svcs {
				m := s.re.FindStringSubmatch(r.value)
				if m == nil || (m[1] == "" && s.ns != n.Namespace) {
					continue
				}
				if _, selects := b.seen[model.KubeEdgeSelects+":"+s.id+"->"+n.ID]; selects {
					continue
				}
				key := model.KubeEdgeInferred + ":" + n.ID + "->" + s.id
				if _, ok := b.seen[key]; ok {
					continue
				}
				b.add(model.KubeEdgeInferred, n.ID, s.id, r.label)
				b.g.Edges[b.seen[key]].EnvKey = r.envKey
			}
		}
	}
}

// itoa is strconv.Itoa (shorter in path building).
func itoa(i int) string { return strconv.Itoa(i) }
