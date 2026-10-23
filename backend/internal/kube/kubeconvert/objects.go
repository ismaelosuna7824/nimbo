package kubeconvert

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"go.yaml.in/yaml/v4"
)

// builder writes the objects of one format.
type builder struct {
	c      *converter
	v      valuer
	format string
}

func (b *builder) app() string { return b.c.opts.Name }

// instance is the app.kubernetes.io/instance label: the release in Helm.
func (b *builder) instance() *yaml.Node { return b.v.expr(b.app(), "{{ .Release.Name }}") }

func (b *builder) selector(u *unit) *yaml.Node {
	return mapping("app.kubernetes.io/name", u.k8s, "app.kubernetes.io/instance", b.instance())
}

func (b *builder) labels(component string) *yaml.Node {
	m := mapping()
	if component != "" {
		set(m, "app.kubernetes.io/name", component)
		set(m, "app.kubernetes.io/instance", b.instance())
	}
	set(m, "app.kubernetes.io/part-of", b.app())
	if b.v.helm() {
		set(m, "app.kubernetes.io/managed-by", b.v.expr("", "{{ .Release.Service }}"))
		set(m, "helm.sh/chart", b.v.expr("", `{{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 }}`))
	}
	return m
}

func (b *builder) meta(name string, labels *yaml.Node) *yaml.Node {
	m := mapping("name", name)
	if b.format == "manifests" {
		set(m, "namespace", b.c.opts.Namespace)
	}
	set(m, "labels", labels)
	return m
}

func doc(apiVersion, kind string, meta *yaml.Node, rest ...any) *yaml.Node {
	return mapping(append([]any{"apiVersion", apiVersion, "kind", kind, "metadata", meta}, rest...)...)
}

func comment(n *yaml.Node, lines ...string) *yaml.Node {
	n.HeadComment = strings.Join(lines, "\n")
	return n
}

func (b *builder) origin(u *unit) string {
	where := ""
	if u.file != "" {
		where = fmt.Sprintf(" (%s:%d)", u.file, u.line)
	}
	return "From compose service " + u.name + where + "."
}

func (b *builder) namespace() *yaml.Node {
	return comment(doc("v1", "Namespace", mapping("name", b.c.opts.Namespace, "labels", mapping("app.kubernetes.io/part-of", b.app()))),
		"The namespace every object of "+b.app()+" lives in.")
}

// ---------------------------------------------------------------------------
// Workloads

func portValue(p string) *yaml.Node {
	if n, err := strconv.Atoi(p); err == nil {
		return num(int64(n))
	}
	return str(p)
}

func probeNode(p *probe) *yaml.Node {
	if p == nil {
		return nil
	}
	m := mapping()
	switch p.kind {
	case "http":
		h := mapping("path", p.path, "port", portValue(p.port))
		if p.scheme != "" {
			set(h, "scheme", p.scheme)
		}
		set(m, "httpGet", h)
	case "tcp":
		set(m, "tcpSocket", mapping("port", portValue(p.port)))
	case "exec":
		cmd := seq()
		for _, s := range p.command {
			cmd.Content = append(cmd.Content, str(s))
		}
		set(m, "exec", mapping("command", cmd))
	}
	set(m, "periodSeconds", p.period)
	set(m, "timeoutSeconds", p.timeout)
	set(m, "failureThreshold", p.failure)
	return m
}

func strList(items []string) *yaml.Node {
	n := seq()
	for _, s := range items {
		n.Content = append(n.Content, str(s))
	}
	return n
}

func (b *builder) container(u *unit) *yaml.Node {
	c := mapping("name", u.k8s)
	set(c, "image", b.image(u))
	if u.pullPolicy != "" {
		set(c, "imagePullPolicy", b.v.scalar(u.key+".image.pullPolicy", str(u.pullPolicy)))
	}
	if len(u.command) > 0 {
		set(c, "command", strList(u.command))
	}
	if len(u.args) > 0 {
		set(c, "args", strList(u.args))
	}
	if u.svc.WorkingDir != "" {
		set(c, "workingDir", u.svc.WorkingDir)
	}
	if len(u.ports) > 0 {
		ports := seq()
		for _, p := range u.ports {
			pm := mapping("name", p.name, "containerPort", p.number)
			if p.protocol != "TCP" {
				set(pm, "protocol", p.protocol)
			}
			ports.Content = append(ports.Content, pm)
		}
		set(c, "ports", ports)
	}
	envFrom := seq()
	for _, cfg := range u.cfgs {
		if !cfg.env {
			continue
		}
		if cfg.kind == "Secret" {
			envFrom.Content = append(envFrom.Content, mapping("secretRef", mapping("name", cfg.name)))
		} else {
			envFrom.Content = append(envFrom.Content, mapping("configMapRef", mapping("name", cfg.name)))
		}
	}
	if len(envFrom.Content) > 0 {
		set(c, "envFrom", envFrom)
	}
	res := mapping(
		"requests", mapping("cpu", u.requests.cpu, "memory", u.requests.memory),
		"limits", mapping("cpu", u.limits.cpu, "memory", u.limits.memory),
	)
	set(c, "resources", b.v.block(u.key+".resources", res))
	set(c, "startupProbe", probeNode(u.startup))
	set(c, "readinessProbe", probeNode(u.readiness))
	set(c, "livenessProbe", probeNode(u.liveness))
	sc := mapping("allowPrivilegeEscalation", false)
	switch {
	case u.sec.nonRoot:
		caps := mapping("drop", strSeq("ALL"))
		if len(u.sec.capAdd) > 0 {
			set(caps, "add", strSeq(upper(u.sec.capAdd)...))
		}
		set(sc, "capabilities", caps)
	case len(u.sec.capAdd) > 0 || len(u.sec.capDrop) > 0:
		caps := mapping()
		if len(u.sec.capDrop) > 0 {
			set(caps, "drop", strSeq(upper(u.sec.capDrop)...))
		}
		if len(u.sec.capAdd) > 0 {
			set(caps, "add", strSeq(upper(u.sec.capAdd)...))
		}
		set(sc, "capabilities", caps)
	}
	if u.sec.readOnly {
		set(sc, "readOnlyRootFilesystem", true)
	}
	set(c, "securityContext", sc)
	if len(u.mounts) > 0 {
		vm := seq()
		for _, m := range u.mounts {
			mm := mapping("name", m.name, "mountPath", m.target)
			if m.subPath != "" {
				set(mm, "subPath", m.subPath)
			}
			if m.readOnly {
				set(mm, "readOnly", true)
			}
			vm.Content = append(vm.Content, mm)
		}
		set(c, "volumeMounts", vm)
	}
	if u.svc.Tty {
		set(c, "tty", true)
	}
	if u.svc.StdinOpen {
		set(c, "stdin", true)
	}
	return c
}

func upper(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = strings.TrimPrefix(strings.ToUpper(s), "CAP_")
	}
	return out
}

func (b *builder) image(u *unit) *yaml.Node {
	h, ok := b.v.(*helmValuer)
	if !ok {
		return str(u.image)
	}
	ref := compose.ParseImage(u.image)
	if ref == nil || ref.Digest != "" || ref.Tag == "" {
		h.put(u.key+".image.repository", str(u.image))
		return h.mark(marker{text: "{{ .Values." + u.key + ".image.repository | quote }}"})
	}
	h.put(u.key+".image.repository", str(ref.Repo))
	h.put(u.key+".image.tag", str(ref.Tag))
	return h.mark(marker{text: `"{{ .Values.` + u.key + `.image.repository }}:{{ .Values.` + u.key + `.image.tag }}"`})
}

func (b *builder) waitContainer(w wait) *yaml.Node {
	return mapping(
		"name", "wait-for-"+w.host,
		"image", waitImage,
		"command", strList([]string{"sh", "-c", fmt.Sprintf("until nc -z -w 2 %s %d; do echo waiting for %s:%d; sleep 2; done", w.host, w.port, w.host, w.port)}),
		"resources", mapping("requests", mapping("cpu", "10m", "memory", "16Mi"), "limits", mapping("cpu", "100m", "memory", "32Mi")),
		"securityContext", mapping(
			"runAsNonRoot", true, "runAsUser", 65534, "runAsGroup", 65534,
			"allowPrivilegeEscalation", false, "readOnlyRootFilesystem", true,
			"capabilities", mapping("drop", strSeq("ALL")),
		),
	)
}

func (b *builder) podSpec(u *unit) *yaml.Node {
	s := mapping("automountServiceAccountToken", false, "enableServiceLinks", false)
	if u.svc.Init != nil && *u.svc.Init {
		set(s, "shareProcessNamespace", true)
	}
	if u.kind == kindJob {
		set(s, "restartPolicy", "OnFailure")
	}
	if u.svc.StopGracePeriod != nil {
		set(s, "terminationGracePeriodSeconds", seconds(u.svc.StopGracePeriod, 0))
	}
	psc := mapping()
	if u.sec.nonRoot {
		set(psc, "runAsNonRoot", true)
		set(psc, "runAsUser", u.sec.uid)
		set(psc, "runAsGroup", u.sec.gid)
		if u.sec.fsGroup {
			set(psc, "fsGroup", u.sec.gid)
		}
	}
	set(psc, "seccompProfile", mapping("type", "RuntimeDefault"))
	set(s, "securityContext", psc)
	if len(u.svc.ExtraHosts) > 0 {
		byIP := map[string][]string{}
		var ips []string
		for host, list := range u.svc.ExtraHosts {
			for _, ip := range list {
				if _, ok := byIP[ip]; !ok {
					ips = append(ips, ip)
				}
				byIP[ip] = append(byIP[ip], host)
			}
		}
		sortStrings(ips)
		ha := seq()
		for _, ip := range ips {
			sortStrings(byIP[ip])
			ha.Content = append(ha.Content, mapping("ip", ip, "hostnames", strList(byIP[ip])))
		}
		set(s, "hostAliases", ha)
	}
	if len(u.svc.DNS) > 0 || len(u.svc.DNSSearch) > 0 {
		dc := mapping()
		if len(u.svc.DNS) > 0 {
			set(dc, "nameservers", strList(u.svc.DNS))
		}
		if len(u.svc.DNSSearch) > 0 {
			set(dc, "searches", strList(u.svc.DNSSearch))
		}
		set(s, "dnsConfig", dc)
	}
	if len(u.waits) > 0 {
		ic := seq()
		for _, w := range u.waits {
			ic.Content = append(ic.Content, b.waitContainer(w))
		}
		set(s, "initContainers", ic)
	}
	set(s, "containers", seq(b.container(u)))
	vols := seq()
	seen := map[string]bool{}
	for _, m := range u.mounts {
		if seen[m.name] || m.source.kind == "claimTemplate" {
			continue
		}
		seen[m.name] = true
		v := mapping("name", m.name)
		switch m.source.kind {
		case "configMap":
			cm := mapping("name", m.source.name)
			if len(m.source.items) > 0 {
				set(cm, "items", items(m.source.items))
			}
			set(v, "configMap", cm)
		case "secret":
			sm := mapping("secretName", m.source.name)
			if len(m.source.items) > 0 {
				set(sm, "items", items(m.source.items))
			}
			set(v, "secret", sm)
		case "persistentVolumeClaim":
			set(v, "persistentVolumeClaim", mapping("claimName", m.source.name))
		case "emptyDir":
			ed := mapping()
			if m.source.medium != "" {
				set(ed, "medium", m.source.medium)
			}
			if m.source.sizeLimit != "" {
				set(ed, "sizeLimit", m.source.sizeLimit)
			}
			ed.Style = yaml.FlowStyle
			set(v, "emptyDir", ed)
		}
		vols.Content = append(vols.Content, v)
	}
	if len(vols.Content) > 0 {
		set(s, "volumes", vols)
	}
	return s
}

func items(list []entry) *yaml.Node {
	n := seq()
	for _, e := range list {
		n.Content = append(n.Content, mapping("key", e.key, "path", e.item))
	}
	return n
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (b *builder) template(u *unit) *yaml.Node {
	labels := b.selectorWithPartOf(u)
	set(labels, "app.kubernetes.io/version", b.version(u))
	meta := mapping("labels", labels)
	if b.v.helm() {
		ann := mapping()
		for _, cfg := range u.cfgs {
			if cfg.env {
				set(ann, "checksum/"+cfg.name, b.v.expr("", `{{ include (print $.Template.BasePath "/`+helmFile(cfg.kind, cfg.name)+`") . | sha256sum }}`))
			}
		}
		if len(ann.Content) > 0 {
			set(meta, "annotations", ann)
		}
	}
	spec := b.podSpec(u)
	if u.stateless() {
		set(spec, "topologySpreadConstraints", b.spread(u))
	}
	return mapping("metadata", meta, "spec", spec)
}

func (b *builder) selectorWithPartOf(u *unit) *yaml.Node {
	m := b.selector(u)
	set(m, "app.kubernetes.io/part-of", b.app())
	return m
}

func (b *builder) workload(u *unit) *yaml.Node {
	labels := b.labels(u.k8s)
	set(labels, "app.kubernetes.io/version", b.version(u))
	meta := b.meta(u.k8s, labels)
	var d *yaml.Node
	switch u.kind {
	case kindJob:
		d = doc("batch/v1", "Job", meta, "spec", mapping("backoffLimit", 4, "template", b.template(u)))
		return comment(d, b.origin(u), "A one-shot service other services waited for (service_completed_successfully): a Job.")
	case kindStatefulSet:
		claims := seq()
		for _, cl := range u.claims {
			claims.Content = append(claims.Content, mapping(
				"metadata", mapping("name", cl.name),
				"spec", mapping(
					"accessModes", strSeq("ReadWriteOnce"),
					"resources", mapping("requests", mapping("storage", b.v.scalar(u.key+".persistence."+valuesKey(cl.name)+".size", str(cl.size)))),
				),
			))
		}
		d = doc("apps/v1", "StatefulSet", meta, "spec", mapping(
			"serviceName", u.k8s+"-headless",
			"replicas", b.replicas(u),
			"selector", mapping("matchLabels", b.selector(u)),
			"template", b.template(u),
			"volumeClaimTemplates", claims,
		))
		return comment(d, b.origin(u), "A data service with its own named volumes: a StatefulSet whose volumeClaimTemplates keep the data across restarts.")
	}
	spec := mapping("replicas", b.replicas(u))
	set(spec, "selector", mapping("matchLabels", b.selector(u)))
	if u.recreate {
		set(spec, "strategy", mapping("type", "Recreate"))
	} else {
		set(spec, "strategy", rollingUpdate())
	}
	set(spec, "template", b.template(u))
	d = doc("apps/v1", "Deployment", meta, "spec", spec)
	if u.recreate {
		return comment(d, b.origin(u), "Recreate: its ReadWriteOnce volume can't be mounted by an old and a new pod at once.")
	}
	return comment(d, b.origin(u))
}

func (b *builder) servicePorts(u *unit) *yaml.Node {
	if len(u.ports) == 0 {
		return nil
	}
	ports := seq()
	for _, p := range u.ports {
		pm := mapping("name", p.name, "port", p.number, "targetPort", p.name)
		if p.protocol != "TCP" {
			set(pm, "protocol", p.protocol)
		}
		ports.Content = append(ports.Content, pm)
	}
	return ports
}

func (b *builder) service(u *unit) *yaml.Node {
	d := doc("v1", "Service", b.meta(u.k8s, b.labels(u.k8s)), "spec", mapping(
		"type", "ClusterIP",
		"selector", b.selector(u),
		"ports", b.servicePorts(u),
	))
	return comment(d, "Other services reach "+u.name+" as "+u.k8s+":<port>, like on the compose network.")
}

func (b *builder) headless(u *unit) *yaml.Node {
	d := doc("v1", "Service", b.meta(u.k8s+"-headless", b.labels(u.k8s)), "spec", mapping(
		"clusterIP", "None",
		"publishNotReadyAddresses", true,
		"selector", b.selector(u),
		"ports", b.servicePorts(u),
	))
	return comment(d, "Governing Service of the "+u.k8s+" StatefulSet (stable pod DNS names).")
}

func (b *builder) pvc(u *unit, cl claim) *yaml.Node {
	d := doc("v1", "PersistentVolumeClaim", b.meta(cl.name, b.labels(u.k8s)), "spec", mapping(
		"accessModes", strSeq("ReadWriteOnce"),
		"resources", mapping("requests", mapping("storage", b.v.scalar(u.key+".persistence."+valuesKey(cl.name)+".size", str(cl.size)))),
	))
	return comment(d, "Compose volume "+cl.volume+" (the cluster's default StorageClass provisions it).")
}

func (b *builder) pdb(u *unit) *yaml.Node {
	d := doc("policy/v1", "PodDisruptionBudget", b.meta(u.k8s, b.labels(u.k8s)), "spec", mapping(
		"minAvailable", 1,
		"selector", mapping("matchLabels", b.selector(u)),
	))
	return comment(d, "Keeps one "+u.k8s+" pod serving during node drains and upgrades.")
}

// stateless units scale out in prod (2 replicas + a PDB).
func (u *unit) stateless() bool { return u.kind == kindDeployment && !u.recreate }

func (b *builder) ingress(units []*unit) *yaml.Node {
	rules := seq()
	for _, u := range units {
		host := b.v.scalar("ingress.hosts."+u.key, str(b.c.host(u)))
		rules.Content = append(rules.Content, mapping(
			"host", host,
			"http", mapping("paths", seq(mapping(
				"path", "/",
				"pathType", "Prefix",
				"backend", mapping("service", mapping("name", u.k8s, "port", mapping("name", u.ingress.name))),
			))),
		))
	}
	d := doc("networking.k8s.io/v1", "Ingress", b.meta(b.app(), b.labels("")), "spec", mapping(
		"ingressClassName", b.v.scalar("ingress.className", str(ingressClass)),
		"rules", rules,
	))
	return comment(d, "Services users reached through published ports. *.localhost resolves to this machine.")
}

// ---------------------------------------------------------------------------
// ConfigMaps and Secrets (manifests and Helm; Kustomize generates them)

func isText(b []byte) bool { return utf8.Valid(b) && !strings.ContainsRune(string(b), 0) }

func textNode(s string) *yaml.Node {
	n := str(s)
	if strings.Contains(s, "\n") {
		n.Style = yaml.LiteralStyle
	}
	return n
}

func (b *builder) configObject(cfg *config, u *unit) *yaml.Node {
	text, binary := mapping(), mapping()
	textKey, binKey := "data", "binaryData"
	if cfg.kind == "Secret" {
		textKey, binKey = "stringData", "data"
	}
	if cfg.env {
		m := mapping()
		for _, e := range cfg.entries {
			set(m, e.key, str(string(e.value)))
		}
		path := u.key + ".env"
		if cfg.kind == "Secret" {
			path = u.key + ".secretEnv"
		}
		text = b.v.rangeMap(path, m)
	} else {
		for _, e := range cfg.entries {
			file := "files/" + e.path
			switch {
			case isText(e.value):
				set(text, e.key, b.textValue(string(e.value), `{{ .Files.Get "`+file+`" | quote }}`))
			default:
				set(binary, e.key, b.v.expr(base64.StdEncoding.EncodeToString(e.value), `{{ .Files.Get "`+file+`" | b64enc }}`))
			}
		}
	}
	d := doc("v1", cfg.kind, b.meta(cfg.name, b.labels(u.k8s)))
	if cfg.kind == "Secret" {
		set(d, "type", "Opaque")
	}
	if text.Kind != yaml.MappingNode || len(text.Content) > 0 {
		set(d, textKey, text)
	}
	if len(binary.Content) > 0 {
		set(d, binKey, binary)
	}
	switch {
	case cfg.env && cfg.kind == "Secret":
		return comment(d, "Credentials of "+u.name+" (and values that embed them), from its environment / .env.")
	case cfg.env:
		return comment(d, "Environment of "+u.name+" (envFrom).")
	}
	what := "files"
	if len(cfg.entries) > 0 && cfg.entries[0].path != "" {
		what = "./" + cfg.entries[0].path
		if cfg.entries[0].item != "" {
			what = "the files of ./" + strings.TrimSuffix(cfg.entries[0].path, "/"+cfg.entries[0].item)
		}
	}
	return comment(d, "Mounted by "+u.name+": "+what+".")
}

func (b *builder) textValue(lit, helm string) *yaml.Node {
	if b.v.helm() {
		return b.v.expr(lit, helm)
	}
	return textNode(lit)
}

// helmFile is the template file of an object in a converted chart.
func helmFile(kind, name string) string {
	short := map[string]string{
		"Deployment": "deployment", "StatefulSet": "statefulset", "Job": "job", "Service": "service",
		"ConfigMap": "configmap", "Secret": "secret", "PersistentVolumeClaim": "pvc",
		"PodDisruptionBudget": "pdb", "Ingress": "ingress", "HorizontalPodAutoscaler": "hpa",
	}[kind]
	if name == short || strings.HasSuffix(name, "-"+short) {
		return name + ".yaml"
	}
	return name + "-" + short + ".yaml"
}
