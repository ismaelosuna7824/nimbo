// Package kubeconvert converts the compose project of a docker workflow
// into the files of a Kubernetes workflow: plain manifests, a Kustomize
// base with dev/prod overlays, or a Helm chart with values-dev/prod.yaml.
//
// Every service becomes a Deployment (a StatefulSet with
// volumeClaimTemplates for data services owning named volumes, a Job for
// one-shot services others wait for), a Service named like the compose
// service (so `db:5432` in connection strings keeps resolving), a
// ConfigMap and a Secret for its environment (credentials and values that
// interpolate them go to the Secret), probes from its healthcheck,
// requests/limits from deploy.resources, a non-root security context when
// the image's user is known, init containers that wait for the Services of
// its depends_on (ready pods only: readiness probes gate them), and an
// Ingress rule when users reach it through a published HTTP port. Images
// built from the workflow keep their build contexts and are listed in
// Kubernetes.Builds (`<service>:dev`). What can't be mapped is reported.
package kubeconvert

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Options tune Convert.
type Options struct {
	Format    string // model.ConvertManifests (default) | ConvertKustomize | ConvertHelm
	Name      string // application name: labels, chart and release name, Ingress hosts (DNS-1123 label)
	Namespace string // default Name
}

// Result is a conversion: the new workflow's files and the report.
type Result struct {
	// Files are every file of the new workflow: Generated plus the build
	// contexts copied from the docker workflow.
	Files     map[string][]byte
	Generated []string // the manifests/kustomizations/chart files written, sorted
	Model     *model.KubernetesModel
	Report    model.KubeConvertReport
}

// Image tags and helpers the converted workloads use.
const (
	localTag     = "dev"          // tag of images built from the workflow (K8sRuntime loads them into the cluster)
	waitImage    = "busybox:1.37" // init containers waiting for dependencies
	ingressClass = "traefik"      // k3s' bundled controller (dev); prod uses "nginx"
	prodClass    = "nginx"
	maxConfigMap = 900 << 10 // bind-mounted files above this don't fit a ConfigMap (1 MiB object limit)
)

var dnsLabelRe = regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)

// ValidName reports whether s is usable as a name/namespace (DNS-1123
// label starting with a letter).
func ValidName(s string) bool { return dnsLabelRe.MatchString(s) }

// SuggestName turns a workflow name into a valid application name.
func SuggestName(s string) string {
	n := dnsName(s)
	if n == "" {
		return "app"
	}
	return n
}

// dnsName lower-cases s and replaces what a DNS-1123 label can't hold.
func dnsName(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out != "" && (out[0] >= '0' && out[0] <= '9') {
		out = "s-" + out
	}
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

// valuesKey is the values.yaml key of a service ("my-api" → "myApi").
func valuesKey(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	var b strings.Builder
	for i, p := range parts {
		if i == 0 {
			b.WriteString(strings.ToLower(p[:1]) + p[1:])
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	k := b.String()
	if k == "" || k[0] >= '0' && k[0] <= '9' {
		k = "svc" + k
	}
	return k
}

// Workload kinds.
const (
	kindDeployment  = "Deployment"
	kindStatefulSet = "StatefulSet"
	kindJob         = "Job"
)

// unit is one compose service and everything decided for it.
type unit struct {
	name    string // compose service name
	k8s     string // object name (DNS label)
	key     string // values.yaml key
	svc     types.ServiceConfig
	facts   *model.ServiceFacts
	file    string
	line    int
	catalog *model.DockerImage
	data    bool // data service (database, cache, queue, search, storage)
	kind    string

	image      string
	build      *model.KubeBuild
	pullPolicy string
	command    []string
	args       []string
	replicas   int

	ports   []port
	ingress *port // the published HTTP port users reach

	env    []envVar
	cfgs   []*config // ConfigMaps/Secrets of this service (env, files)
	mounts []mount
	claims []claim // StatefulSet volumeClaimTemplates
	pvcs   []claim // standalone PVCs this service mounts (created by the first user)

	readiness, liveness, startup *probe

	limits, requests quantities

	sec      security
	waits    []wait
	recreate bool // mounts a ReadWriteOnce PVC: Recreate strategy, no second replica
}

type port struct {
	name      string
	number    int
	protocol  string // TCP | UDP
	http      bool
	published string // host port ("" = not published)
	hostIP    string
}

type envVar struct {
	key, value string
	secret     bool
}

// config is a ConfigMap or Secret the converter writes.
type config struct {
	kind    string // ConfigMap | Secret
	name    string
	env     bool // entries are environment variables (envFrom)
	entries []entry
}

type entry struct {
	key   string
	value []byte
	path  string // files: the workflow file it comes from (slash, relative)
	item  string // files of a directory: the path inside the mount
}

type mount struct {
	name     string // pod volume name
	target   string
	subPath  string
	readOnly bool
	source   volumeSource
}

type volumeSource struct {
	kind      string // configMap | secret | persistentVolumeClaim | emptyDir | claimTemplate
	name      string // ConfigMap/Secret/claim name
	medium    string // emptyDir: Memory
	sizeLimit string
	items     []entry // configMap/secret items (dirs with sub-folders)
}

type claim struct {
	name   string // claim (template) name
	volume string // compose volume name
	size   string
}

type probe struct {
	kind    string // http | tcp | exec
	path    string
	port    string // port name or number
	scheme  string
	command []string

	period, timeout, failure int
}

type quantities struct{ cpu, memory string }

type security struct {
	uid, gid int64 // known numeric user (nonRoot false: root or unknown)
	nonRoot  bool
	fsGroup  bool
	capAdd   []string
	capDrop  []string
	readOnly bool
}

type wait struct {
	service string
	host    string
	port    int
}

// converter holds the conversion state.
type converter struct {
	p      *compose.Project
	opts   Options
	units  []*unit
	byName map[string]*unit
	report model.KubeConvertReport
	files  map[string][]byte // build contexts copied
	// volumeUsers counts the services mounting each named volume.
	volumeUsers map[string][]string
	pvcOwner    map[string]string // standalone PVC → service creating it
	shared      []*config         // top-level configs/secrets (one per compose config/secret)
}

// Convert converts the compose project p (opened from the docker
// workflow's files) into a Kubernetes workflow's files.
func Convert(p *compose.Project, opts Options) (*Result, error) {
	if p == nil {
		return nil, errors.New("can't convert: no compose project")
	}
	if p.Model == nil {
		msg := "the compose files don't load"
		for _, d := range p.Diagnostics {
			if d.Severity == model.SeverityError {
				msg = d.Message
				break
			}
		}
		return nil, fmt.Errorf("can't convert: %s (fix the dk_* errors first)", msg)
	}
	switch opts.Format {
	case "":
		opts.Format = model.ConvertManifests
	case model.ConvertManifests, model.ConvertKustomize, model.ConvertHelm:
	default:
		return nil, fmt.Errorf("invalid format %q: use manifests, kustomize or helm", opts.Format)
	}
	if opts.Name == "" {
		opts.Name = SuggestName(p.Name)
	}
	if opts.Namespace == "" {
		opts.Namespace = opts.Name
	}
	if !ValidName(opts.Name) {
		return nil, fmt.Errorf("invalid name %q: lower-case letters, digits and '-', starting with a letter (at most 63)", opts.Name)
	}
	if !ValidName(opts.Namespace) {
		return nil, fmt.Errorf("invalid namespace %q: lower-case letters, digits and '-', starting with a letter (at most 63)", opts.Namespace)
	}
	c := &converter{
		p: p, opts: opts, byName: map[string]*unit{}, files: map[string][]byte{},
		volumeUsers: map[string][]string{}, pvcOwner: map[string]string{},
		report: model.KubeConvertReport{
			Format: opts.Format, Name: opts.Name, Namespace: opts.Namespace,
			Sources: []model.KubeSource{}, Builds: []model.KubeBuild{}, Environments: []string{},
			Objects: []model.KubeConvertObj{}, Mappings: []model.KubeConvertMap{}, Unmapped: []model.KubeConvertIssue{},
		},
	}
	if err := c.analyze(); err != nil {
		return nil, err
	}
	files, err := c.write()
	if err != nil {
		return nil, err
	}
	generated := slices.Sorted(maps.Keys(files))
	maps.Copy(files, c.files)
	k := &model.KubernetesModel{Sources: c.report.Sources, Builds: c.report.Builds}
	return &Result{Files: files, Generated: generated, Model: k, Report: c.report}, nil
}

func (c *converter) mapped(svc, from, to string) {
	c.report.Mappings = append(c.report.Mappings, model.KubeConvertMap{Service: svc, From: from, To: to})
}

func (c *converter) issue(u *unit, sev, feature, detail string, field ...string) {
	is := model.KubeConvertIssue{Feature: feature, Detail: detail, Severity: sev}
	if u != nil {
		is.Service = u.name
		is.File, is.Line = u.file, u.line
		if len(field) > 0 {
			is.File, is.Line = c.p.FieldLine(u.name, field...)
		}
	}
	c.report.Unmapped = append(c.report.Unmapped, is)
}

// analyze decides every service's objects.
func (c *converter) analyze() error {
	graph := c.p.Graph(nil)
	facts := map[string]*model.ServiceFacts{}
	for _, n := range graph.Nodes {
		if n.Kind == model.ComposeService {
			facts[n.Name] = n.Service
		}
	}
	names := c.p.SortedServiceNames()
	catalog := compose.Images()
	used := map[string]string{}
	for _, name := range names {
		if c.p.Inactive[name] {
			c.issue(nil, "info", "profiles", fmt.Sprintf("Service %s belongs to an inactive profile and was not converted.", name))
			continue
		}
		s := c.p.Model.Services[name]
		u := &unit{name: name, k8s: dnsName(name), key: valuesKey(name), svc: s, facts: facts[name], replicas: 1}
		if u.facts == nil {
			u.facts = &model.ServiceFacts{}
		}
		u.file, u.line = c.p.ServiceLine(name)
		if u.k8s != name {
			c.issue(u, "warning", "service name", fmt.Sprintf("%q is not a valid Kubernetes name; the Service is %q, so update the hosts other services use to reach it.", name, u.k8s))
		}
		if other, dup := used[u.k8s]; dup {
			return fmt.Errorf("services %s and %s both map to the Kubernetes name %q: rename one", other, name, u.k8s)
		}
		used[u.k8s] = name
		u.catalog = catalogFor(catalog, s.Image)
		if u.catalog != nil {
			switch u.catalog.Category {
			case "database", "cache", "messaging", "search", "storage":
				u.data = true
			}
		}
		c.units = append(c.units, u)
		c.byName[name] = u
		for _, v := range s.Volumes {
			if v.Type == types.VolumeTypeVolume && v.Source != "" {
				if !slices.Contains(c.volumeUsers[v.Source], name) {
					c.volumeUsers[v.Source] = append(c.volumeUsers[v.Source], name)
				}
			}
		}
	}
	if len(c.units) == 0 {
		return errors.New("can't convert: the compose project has no active service")
	}
	// One-shot services: someone waits for them to complete.
	oneShot := map[string]bool{}
	for _, u := range c.units {
		for dep, d := range u.svc.DependsOn {
			if d.Condition == types.ServiceConditionCompletedSuccessfully {
				oneShot[dep] = true
			}
		}
	}
	for _, u := range c.units {
		u.kind = kindDeployment
		if oneShot[u.name] {
			u.kind = kindJob
		}
		c.image(u)
		c.command(u)
		c.replicas(u)
		c.portsOf(u)
	}
	for _, u := range c.units {
		c.envOf(u)
		c.volumes(u)
		c.secretsAndConfigs(u)
		c.probes(u)
		c.resources(u)
		c.security(u)
		c.unsupported(u)
	}
	for _, u := range c.units {
		c.dependencies(u)
	}
	return nil
}

func catalogFor(list []model.DockerImage, image string) *model.DockerImage {
	ref := compose.ParseImage(image)
	if ref == nil {
		return nil
	}
	repo := strings.TrimPrefix(strings.TrimPrefix(ref.Repo, "docker.io/"), "library/")
	for i := range list {
		if list[i].Image == repo {
			return &list[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Images and commands

func (c *converter) image(u *unit) {
	s := u.svc
	if s.Build != nil {
		ctx := c.rel(s.Build.Context)
		if ctx == "" || strings.HasPrefix(ctx, "..") {
			c.issue(u, "warning", "build", fmt.Sprintf("The build context %s is outside the workflow (or remote): build and push the image yourself and set it on the workload.", s.Build.Context), "build")
			u.image = s.Image
			if u.image == "" {
				u.image = u.k8s + ":" + localTag
			}
			return
		}
		u.image = u.k8s + ":" + localTag
		u.pullPolicy = "IfNotPresent"
		b := &model.KubeBuild{Image: u.image, Context: ctx, Target: s.Build.Target}
		if df := s.Build.Dockerfile; df != "" && df != "Dockerfile" {
			b.Dockerfile = df
		}
		if s.Build.DockerfileInline != "" {
			c.issue(u, "warning", "build.dockerfile_inline", "Inline Dockerfiles aren't kept: write it to a Dockerfile in the build context.", "build")
		}
		for k, v := range s.Build.Args {
			if v != nil {
				if b.Args == nil {
					b.Args = map[string]string{}
				}
				b.Args[k] = *v
			}
		}
		u.build = b
		c.report.Builds = append(c.report.Builds, *b)
		c.copyTree(ctx)
		c.mapped(u.name, "build: "+ctx, "image "+u.image+" built from "+ctx+" (Kubernetes builds, loaded into the local cluster)")
		return
	}
	u.image = s.Image
	ref := compose.ParseImage(s.Image)
	if ref != nil && ref.Digest == "" && (ref.Tag == "" || ref.Tag == "latest") {
		if u.catalog != nil && u.catalog.Tag != "" {
			u.image = ref.Repo + ":" + u.catalog.Tag
			c.mapped(u.name, "image: "+s.Image, "image "+u.image+" (pinned to the catalog tag: :latest changes under you)")
		} else {
			c.issue(u, "warning", "image tag", fmt.Sprintf("%s has no pinned tag: pin a version (k8s_latest_tag).", s.Image), "image")
		}
	}
	if s.PullPolicy != "" && s.PullPolicy != types.PullPolicyMissing && s.PullPolicy != types.PullPolicyIfNotPresent {
		switch s.PullPolicy {
		case types.PullPolicyAlways:
			u.pullPolicy = "Always"
		case types.PullPolicyNever:
			u.pullPolicy = "Never"
		}
	}
}

// rel makes an absolute loader path relative to the workflow files ("" when
// remote).
func (c *converter) rel(abs string) string {
	if abs == "" || strings.Contains(abs, "://") || strings.HasPrefix(abs, "git@") {
		return ""
	}
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.p.Dir, filepath.FromSlash(c.p.ProjectDir), abs)
	}
	r, err := filepath.Rel(c.p.Dir, abs)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(r)
}

// copyTree copies a build context into the new workflow.
func (c *converter) copyTree(rel string) {
	root := filepath.Join(c.p.Dir, filepath.FromSlash(rel))
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		r, _ := filepath.Rel(c.p.Dir, p)
		c.files[filepath.ToSlash(r)] = data
		return nil
	})
}

func escapeK8s(list []string) []string {
	out := make([]string, len(list))
	for i, s := range list {
		// Kubernetes expands $(VAR) in command/args; compose didn't.
		out[i] = strings.ReplaceAll(s, "$(", "$$(")
	}
	return out
}

func (c *converter) command(u *unit) {
	if len(u.svc.Entrypoint) > 0 {
		u.command = escapeK8s(u.svc.Entrypoint)
		c.mapped(u.name, "entrypoint", "container command")
	}
	if len(u.svc.Command) > 0 {
		u.args = escapeK8s(u.svc.Command)
		c.mapped(u.name, "command", "container args")
	}
}

func (c *converter) replicas(u *unit) {
	switch {
	case u.svc.Deploy != nil && u.svc.Deploy.Replicas != nil:
		u.replicas = *u.svc.Deploy.Replicas
		c.mapped(u.name, "deploy.replicas", fmt.Sprintf("replicas: %d", u.replicas))
	case u.svc.Scale != nil:
		u.replicas = *u.svc.Scale
		c.mapped(u.name, "scale", fmt.Sprintf("replicas: %d", u.replicas))
	}
}

// ---------------------------------------------------------------------------
// Ports

// wellKnown names the ports of common protocols.
var wellKnown = map[int]string{
	5432: "postgres", 3306: "mysql", 6379: "redis", 5672: "amqp", 27017: "mongodb", 9092: "kafka",
	4222: "nats", 11211: "memcached", 1433: "mssql", 1521: "oracle", 9042: "cql", 2181: "zookeeper",
	5671: "amqps", 1883: "mqtt", 6650: "pulsar", 7687: "bolt", 8086: "influx", 9000: "tcp-9000",
}

// httpPorts are ports that speak HTTP unless known otherwise.
var httpPorts = map[int]bool{80: true, 8080: true, 8000: true, 3000: true, 5000: true, 8081: true, 8888: true, 4200: true, 5173: true, 15672: true, 9090: true, 3001: true, 8082: true, 9200: true, 8983: true, 8161: true, 16686: true}

var hostPortRe = regexp.MustCompile(`([A-Za-z0-9_.-]+):(\d{2,5})\b`)

func (c *converter) portsOf(u *unit) {
	seen := map[string]*port{}
	add := func(n int, proto string) *port {
		if n <= 0 || n > 65535 {
			return nil
		}
		proto = strings.ToUpper(proto)
		if proto == "" {
			proto = "TCP"
		}
		k := strconv.Itoa(n) + "/" + proto
		if p := seen[k]; p != nil {
			return p
		}
		p := &port{number: n, protocol: proto}
		seen[k] = p
		u.ports = append(u.ports, *p)
		return &u.ports[len(u.ports)-1]
	}
	find := func(n int, proto string) *port {
		for i := range u.ports {
			if u.ports[i].number == n && u.ports[i].protocol == proto {
				return &u.ports[i]
			}
		}
		return nil
	}
	for _, sp := range u.svc.Ports {
		add(int(sp.Target), sp.Protocol)
		p := find(int(sp.Target), strings.ToUpper(cmpOr(sp.Protocol, "tcp")))
		if p != nil && sp.Published != "" {
			p.published, p.hostIP = sp.Published, sp.HostIP
		}
		if p != nil && strings.HasPrefix(sp.AppProtocol, "http") {
			p.http = true
		}
	}
	for _, e := range u.facts.Expose {
		num, proto, _ := strings.Cut(e, "/")
		if strings.Contains(num, "-") {
			c.issue(u, "info", "expose", fmt.Sprintf("Port range %s isn't declared on the container: list the ports you need.", e), "expose")
			continue
		}
		n, err := strconv.Atoi(num)
		if err == nil {
			add(n, proto)
		}
	}
	if u.catalog != nil {
		for _, n := range u.catalog.Ports {
			add(n, "tcp")
		}
	}
	// Ports other services (and mounted config files) connect to.
	for _, o := range c.units {
		if o == u {
			continue
		}
		for _, v := range o.svc.Environment {
			if v == nil {
				continue
			}
			for _, m := range hostPortRe.FindAllStringSubmatch(*v, -1) {
				if m[1] == u.name {
					n, _ := strconv.Atoi(m[2])
					add(n, "tcp")
				}
			}
		}
		for _, v := range o.svc.Volumes {
			if v.Type != types.VolumeTypeBind {
				continue
			}
			for _, data := range c.bindTexts(v.Source) {
				for _, m := range hostPortRe.FindAllStringSubmatch(data, -1) {
					if m[1] == u.name {
						n, _ := strconv.Atoi(m[2])
						add(n, "tcp")
					}
				}
			}
		}
	}
	if chk := u.healthcheckText(); chk != "" {
		if m := httpCheckRe.FindStringSubmatch(chk); m != nil {
			n := 80
			if m[1] == "https" {
				n = 443
			}
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
			if p := add(n, "tcp"); p != nil {
				find(n, "TCP").http = true
			}
		}
	}
	httpish := u.build != nil || u.catalog != nil && slices.Contains([]string{"web", "gateway", "cms", "apps", "observability", "auth", "workflow", "ai", "devtools", "tools"}, u.catalog.Category)
	for i := range u.ports {
		p := &u.ports[i]
		if p.protocol == "TCP" && httpPorts[p.number] && (httpish || p.number == 15672 || p.published != "") {
			p.http = true
		}
	}
	// Names: unique, at most 15 characters.
	names := map[string]bool{}
	for i := range u.ports {
		p := &u.ports[i]
		switch {
		case p.http && !names["http"]:
			p.name = "http"
		case p.http:
			p.name = "http-" + strconv.Itoa(p.number)
		case wellKnown[p.number] != "" && p.protocol == "TCP" && !names[wellKnown[p.number]]:
			p.name = wellKnown[p.number]
		default:
			p.name = strings.ToLower(p.protocol) + "-" + strconv.Itoa(p.number)
		}
		names[p.name] = true
	}
	// Users reach a service through a published HTTP port bound to every
	// interface; loopback-only ports were for this machine.
	for i := range u.ports {
		p := &u.ports[i]
		if p.published == "" {
			continue
		}
		loop := p.hostIP == "127.0.0.1" || p.hostIP == "::1" || p.hostIP == "localhost"
		if p.http && !loop && !u.data && u.ingress == nil {
			u.ingress = p
			c.mapped(u.name, fmt.Sprintf("ports: %s:%d", p.published, p.number), fmt.Sprintf("Ingress rule http://%s → Service %s:%s", c.host(u), u.k8s, p.name))
			continue
		}
		c.mapped(u.name, fmt.Sprintf("ports: %s%s:%d", hostPrefix(p.hostIP), p.published, p.number), fmt.Sprintf("Service %s port %d (ClusterIP; reach it from this machine through Endpoints / port-forward)", u.k8s, p.number))
	}
	if len(u.ports) > 0 {
		var list []string
		for _, p := range u.ports {
			list = append(list, fmt.Sprintf("%s %d", p.name, p.number))
		}
		c.mapped(u.name, "ports/expose", fmt.Sprintf("Service %s (ClusterIP: %s), named like the compose service so connection strings keep working", u.k8s, strings.Join(list, ", ")))
	}
}

func hostPrefix(ip string) string {
	if ip == "" {
		return ""
	}
	return ip + ":"
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func (c *converter) host(u *unit) string { return u.k8s + "." + c.opts.Name + ".localhost" }

// bindTexts reads the text files a bind mount source names (a file or the
// files of a folder inside the workflow; nil otherwise).
func (c *converter) bindTexts(source string) []string {
	rel := c.rel(source)
	if rel == "" || strings.HasPrefix(rel, "..") {
		return nil
	}
	var out []string
	for _, f := range c.readBind(rel) {
		out = append(out, string(f.value))
	}
	return out
}

// readBind reads a bind-mounted file or folder (relative paths; nil when
// missing).
func (c *converter) readBind(rel string) []entry {
	abs := filepath.Join(c.p.Dir, filepath.FromSlash(rel))
	fi, err := os.Stat(abs)
	if err != nil {
		return nil
	}
	if !fi.IsDir() {
		data, err := os.ReadFile(abs)
		if err != nil {
			return nil
		}
		return []entry{{key: path.Base(rel), value: data, path: rel}}
	}
	var out []entry
	_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		in, _ := filepath.Rel(abs, p)
		in = filepath.ToSlash(in)
		r, _ := filepath.Rel(c.p.Dir, p)
		out = append(out, entry{key: path.Base(in), value: data, path: filepath.ToSlash(r), item: in})
		return nil
	})
	return out
}

// ---------------------------------------------------------------------------
// Environment

var (
	looseSecretRe = regexp.MustCompile(`(?i)(pass|secret|token|key|credential|auth|private|cert)`)
	credURLRe     = regexp.MustCompile(`[a-z][a-z0-9+.-]*://[^/@\s:]+:[^/@\s]+@`)
	passwordKVRe  = regexp.MustCompile(`(?i)(password|pwd)=`)
	varRefRe      = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)|\$([A-Za-z_][A-Za-z0-9_]*)`)
)

// secretName reports whether an environment key names a credential.
func secretName(k string) bool {
	if strings.HasSuffix(strings.ToUpper(k), "_FILE") {
		return false
	}
	return compose.IsSecretKey(k) || looseSecretRe.MatchString(k)
}

func (c *converter) envOf(u *unit) {
	values := map[string]*string{}
	for _, ef := range u.svc.EnvFiles {
		data, err := os.ReadFile(ef.Path)
		if err != nil {
			continue
		}
		vars, err := dotenv.UnmarshalBytesWithLookup(data, func(k string) (string, bool) {
			v, ok := c.p.Env[k]
			return v, ok
		})
		if err != nil {
			continue
		}
		for k, v := range vars {
			values[k] = &v
		}
		c.mapped(u.name, "env_file: "+c.rel(ef.Path), "ConfigMap "+u.k8s+"-env / Secret "+u.k8s+"-secret (envFrom)")
	}
	maps.Copy(values, u.svc.Environment)
	raw := c.p.RawEnv(u.name)
	var plain, secret []string
	for _, k := range slices.Sorted(maps.Keys(values)) {
		v := values[k]
		if v == nil {
			c.issue(u, "warning", "environment", fmt.Sprintf("%s is passed from the host environment without a value: add it to the ConfigMap/Secret.", k), "environment")
			continue
		}
		sec := secretName(k) || credURLRe.MatchString(*v) || passwordKVRe.MatchString(*v)
		for _, m := range varRefRe.FindAllStringSubmatch(raw[k], -1) {
			if secretName(m[1] + m[2]) {
				sec = true
			}
		}
		u.env = append(u.env, envVar{key: k, value: *v, secret: sec})
		if sec {
			secret = append(secret, k)
		} else {
			plain = append(plain, k)
		}
	}
	// Postgres refuses a data directory that is a mount point with other
	// files (lost+found on real disks) and can't chmod one it doesn't own:
	// its documented Kubernetes setup is a sub-directory.
	if u.catalog != nil && u.catalog.Image == "postgres" && values["PGDATA"] == nil {
		for _, v := range u.svc.Volumes {
			if v.Type == types.VolumeTypeVolume && v.Target == "/var/lib/postgresql/data" {
				u.env = append(u.env, envVar{key: "PGDATA", value: "/var/lib/postgresql/data/pgdata"})
				plain = append(plain, "PGDATA")
				c.mapped(u.name, "volumes: "+v.Source+":"+v.Target, "PGDATA=/var/lib/postgresql/data/pgdata (postgres' data directory below the volume mount)")
			}
		}
	}
	if len(plain) > 0 {
		cfg := &config{kind: "ConfigMap", name: u.k8s + "-env", env: true}
		for _, e := range u.env {
			if !e.secret {
				cfg.entries = append(cfg.entries, entry{key: e.key, value: []byte(e.value)})
			}
		}
		u.cfgs = append(u.cfgs, cfg)
		c.mapped(u.name, "environment: "+strings.Join(plain, ", "), "ConfigMap "+cfg.name+" (envFrom)")
	}
	if len(secret) > 0 {
		cfg := &config{kind: "Secret", name: u.k8s + "-secret", env: true}
		for _, e := range u.env {
			if e.secret {
				cfg.entries = append(cfg.entries, entry{key: e.key, value: []byte(e.value)})
			}
		}
		u.cfgs = append(u.cfgs, cfg)
		c.mapped(u.name, "environment: "+strings.Join(secret, ", "), "Secret "+cfg.name+" (envFrom): credentials and values that interpolate them")
	}
}

// ---------------------------------------------------------------------------
// Volumes

func (c *converter) volumes(u *unit) {
	ownsAll := true
	var named []types.ServiceVolumeConfig
	for _, v := range u.svc.Volumes {
		if v.Type == types.VolumeTypeVolume && v.Source != "" {
			named = append(named, v)
			if len(c.volumeUsers[v.Source]) > 1 {
				ownsAll = false
			}
		}
	}
	if u.data && len(named) > 0 && ownsAll && u.kind == kindDeployment {
		u.kind = kindStatefulSet
	}
	for i, v := range u.svc.Volumes {
		volName := fmt.Sprintf("vol-%d", i)
		switch v.Type {
		case types.VolumeTypeVolume:
			if v.Source == "" {
				u.mounts = append(u.mounts, mount{name: volName, target: v.Target, readOnly: v.ReadOnly, source: volumeSource{kind: "emptyDir"}})
				c.mapped(u.name, "volumes: "+v.Target+" (anonymous)", "emptyDir "+volName)
				continue
			}
			claimName := dnsName(v.Source)
			size := "1Gi"
			if tv, ok := c.p.Model.Volumes[v.Source]; ok && bool(tv.External) {
				c.issue(u, "warning", "external volume", fmt.Sprintf("Volume %s is external: a new empty PersistentVolumeClaim %s is created; copy its data over if you need it.", v.Source, claimName), "volumes")
			}
			if tv, ok := c.p.Model.Volumes[v.Source]; ok && tv.Driver != "" && tv.Driver != "local" {
				c.issue(u, "info", "volume driver", fmt.Sprintf("Volume driver %s of %s is replaced by the cluster's default StorageClass.", tv.Driver, v.Source), "volumes")
			}
			if u.kind == kindStatefulSet {
				u.claims = append(u.claims, claim{name: claimName, volume: v.Source, size: size})
				u.mounts = append(u.mounts, mount{name: claimName, target: v.Target, readOnly: v.ReadOnly, source: volumeSource{kind: "claimTemplate", name: claimName}})
				c.mapped(u.name, "volumes: "+v.Source+":"+v.Target, fmt.Sprintf("StatefulSet %s volumeClaimTemplate %s (%s, ReadWriteOnce)", u.k8s, claimName, size))
				continue
			}
			shared := len(c.volumeUsers[v.Source]) > 1
			if owner, ok := c.pvcOwner[claimName]; !ok {
				c.pvcOwner[claimName] = u.name
				u.pvcs = append(u.pvcs, claim{name: claimName, volume: v.Source, size: size})
			} else if shared {
				c.issue(u, "warning", "shared volume", fmt.Sprintf("Volume %s is shared with %s: the PersistentVolumeClaim is ReadWriteOnce, so both pods must run on the same node (true on the local cluster); use a ReadWriteMany StorageClass on multi-node clusters.", v.Source, owner), "volumes")
			}
			u.recreate = true
			u.mounts = append(u.mounts, mount{name: claimName, target: v.Target, readOnly: v.ReadOnly, source: volumeSource{kind: "persistentVolumeClaim", name: claimName}})
			c.mapped(u.name, "volumes: "+v.Source+":"+v.Target, fmt.Sprintf("PersistentVolumeClaim %s (%s, ReadWriteOnce), Deployment strategy Recreate", claimName, size))
		case types.VolumeTypeBind:
			c.bindMount(u, v, volName)
		case types.VolumeTypeTmpfs:
			src := volumeSource{kind: "emptyDir", medium: "Memory"}
			if v.Tmpfs != nil && v.Tmpfs.Size > 0 {
				src.sizeLimit = memQuantity(int64(v.Tmpfs.Size))
			}
			u.mounts = append(u.mounts, mount{name: volName, target: v.Target, source: src})
			c.mapped(u.name, "tmpfs: "+v.Target, "emptyDir (medium: Memory) "+volName)
		default:
			c.issue(u, "warning", "volumes", fmt.Sprintf("%s mount %s → %s has no Kubernetes equivalent here.", v.Type, v.Source, v.Target), "volumes")
		}
	}
	for i, t := range u.svc.Tmpfs {
		target, _, _ := strings.Cut(t, ":")
		name := fmt.Sprintf("tmpfs-%d", i)
		u.mounts = append(u.mounts, mount{name: name, target: target, source: volumeSource{kind: "emptyDir", medium: "Memory"}})
		c.mapped(u.name, "tmpfs: "+t, "emptyDir (medium: Memory) "+name)
	}
	if len(u.svc.VolumesFrom) > 0 {
		c.issue(u, "warning", "volumes_from", "volumes_from has no equivalent: mount the same PersistentVolumeClaims explicitly.", "volumes_from")
	}
}

func (c *converter) bindMount(u *unit, v types.ServiceVolumeConfig, volName string) {
	rel := c.rel(v.Source)
	if rel == "" || strings.HasPrefix(rel, "..") || rel == "." {
		sev := "warning"
		detail := fmt.Sprintf("Bind mount of the host path %s isn't converted: hostPath volumes tie pods to one node and expose the host (k8s_host_path). Bake the files into an image, or use a ConfigMap/PersistentVolumeClaim.", v.Source)
		if strings.HasSuffix(v.Source, "docker.sock") {
			detail = "The Docker socket isn't available in Kubernetes pods (and gives root on the host): use the Kubernetes API with a ServiceAccount instead."
		}
		c.issue(u, sev, "bind mount", detail, "volumes")
		return
	}
	files := c.readBind(rel)
	if len(files) == 0 {
		c.issue(u, "warning", "bind mount", fmt.Sprintf("%s is mounted but missing from the workflow files: nothing to put in a ConfigMap.", rel), "volumes")
		return
	}
	total := 0
	for _, f := range files {
		total += len(f.value)
	}
	if total > maxConfigMap {
		c.issue(u, "warning", "bind mount", fmt.Sprintf("%s holds %d KiB: too big for a ConfigMap (1 MiB). COPY it into an image built from the workflow instead.", rel, total>>10), "volumes")
		return
	}
	name := u.k8s + "-" + dnsName(path.Base(rel))
	for _, other := range u.cfgs {
		if other.name == name {
			name += "-" + strings.TrimPrefix(volName, "vol-")
		}
	}
	cfg := &config{kind: "ConfigMap", name: name}
	volName = strings.TrimPrefix(name, u.k8s+"-")
	fi, _ := os.Stat(filepath.Join(c.p.Dir, filepath.FromSlash(rel)))
	dir := fi != nil && fi.IsDir()
	nested := false
	keys := map[string]bool{}
	for i, f := range files {
		k := configKey(f.key)
		if dir && (strings.Contains(f.item, "/") || keys[k]) {
			nested = true
			k = configKey(strings.ReplaceAll(f.item, "/", "_"))
		}
		keys[k] = true
		files[i].key = k
	}
	cfg.entries = files
	u.cfgs = append(u.cfgs, cfg)
	m := mount{name: volName, target: v.Target, readOnly: true, source: volumeSource{kind: "configMap", name: name}}
	if !dir {
		m.subPath = files[0].key
	} else if nested {
		m.source.items = files
	}
	u.mounts = append(u.mounts, m)
	what := "file"
	if dir {
		what = fmt.Sprintf("folder (%d files)", len(files))
	}
	c.mapped(u.name, "volumes: ./"+rel+":"+v.Target, fmt.Sprintf("ConfigMap %s from the %s, mounted read-only", name, what))
	if !v.ReadOnly {
		c.issue(u, "info", "bind mount", fmt.Sprintf("./%s was mounted read-write; ConfigMaps are read-only: writes the container made there now fail.", rel), "volumes")
	}
}

var configKeyRe = regexp.MustCompile(`[^-._a-zA-Z0-9]`)

func configKey(s string) string { return configKeyRe.ReplaceAllString(s, "_") }

// secretsAndConfigs maps the service's compose secrets/configs to Secrets
// and ConfigMaps mounted where compose puts them.
func (c *converter) secretsAndConfigs(u *unit) {
	for _, ref := range u.svc.Secrets {
		def, ok := c.p.Model.Secrets[ref.Source]
		target := ref.Target
		if target == "" {
			target = "/run/secrets/" + ref.Source
		} else if !strings.HasPrefix(target, "/") {
			target = "/run/secrets/" + target
		}
		c.fileObject(u, "Secret", ref.Source, types.FileObjectConfig(def), ok, target)
	}
	for _, ref := range u.svc.Configs {
		def, ok := c.p.Model.Configs[ref.Source]
		target := ref.Target
		if target == "" {
			target = "/" + ref.Source
		}
		c.fileObject(u, "ConfigMap", ref.Source, types.FileObjectConfig(def), ok, target)
	}
}

func (c *converter) fileObject(u *unit, kind, name string, def types.FileObjectConfig, ok bool, target string) {
	field := "secrets"
	if kind == "ConfigMap" {
		field = "configs"
	}
	if !ok || bool(def.External) {
		c.issue(u, "warning", field, fmt.Sprintf("%s %s is external: create the %s %s in the cluster yourself.", field[:len(field)-1], name, kind, dnsName(name)), field)
		return
	}
	var data []byte
	src := ""
	switch {
	case def.File != "":
		rel := c.rel(def.File)
		if rel == "" || strings.HasPrefix(rel, "..") {
			c.issue(u, "warning", field, fmt.Sprintf("%s comes from %s outside the workflow: create the %s yourself.", name, def.File, kind), field)
			return
		}
		b, err := os.ReadFile(filepath.Join(c.p.Dir, filepath.FromSlash(rel)))
		if err != nil {
			c.issue(u, "warning", field, fmt.Sprintf("%s: %s is missing.", name, rel), field)
			return
		}
		data, src = b, rel
	case def.Environment != "":
		data = []byte(c.p.Env[def.Environment])
	case def.Content != "":
		data = []byte(def.Content)
	}
	objName := strings.ToLower(field[:len(field)-1]) + "-" + dnsName(name)
	var cfg *config
	for _, s := range c.shared {
		if s.name == objName && s.kind == kind {
			cfg = s
		}
	}
	key := configKey(path.Base(target))
	if cfg == nil {
		cfg = &config{kind: kind, name: objName, entries: []entry{{key: key, value: data, path: src}}}
		c.shared = append(c.shared, cfg)
		u.cfgs = append(u.cfgs, cfg)
	}
	u.mounts = append(u.mounts, mount{name: objName, target: target, subPath: cfg.entries[0].key, readOnly: true, source: volumeSource{kind: strings.ToLower(kind[:1]) + kind[1:], name: objName}})
	c.mapped(u.name, field+": "+name, fmt.Sprintf("%s %s mounted at %s", kind, objName, target))
}

// ---------------------------------------------------------------------------
// Probes

var httpCheckRe = regexp.MustCompile(`(?:wget|curl|urlopen|requests\.get|http\.get|fetch)\b[^|;&]*?\b(https?)://(?:127\.0\.0\.1|localhost|0\.0\.0\.0|\[::1\])(?::(\d+))?(/[^\s'"\)]*)?`)

// healthcheckText is the service's check command line ("" without one).
func (u *unit) healthcheckText() string {
	if h := u.svc.HealthCheck; h != nil && !h.Disable && len(h.Test) > 0 && h.Test[0] != "NONE" {
		if h.Test[0] == "CMD" || h.Test[0] == "CMD-SHELL" {
			return strings.Join(h.Test[1:], " ")
		}
		return strings.Join(h.Test, " ")
	}
	if u.facts.Healthcheck != nil && u.facts.HealthcheckFrom == "dockerfile" && !u.facts.Healthcheck.Disabled {
		return u.facts.Healthcheck.Test
	}
	return ""
}

func seconds(d *types.Duration, def time.Duration) int {
	v := def
	if d != nil && *d > 0 {
		v = time.Duration(*d)
	}
	s := int(math.Ceil(v.Seconds()))
	return max(s, 1)
}

func parseSeconds(s string, def time.Duration) int {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return max(int(math.Ceil(d.Seconds())), 1)
	}
	return max(int(def.Seconds()), 1)
}

func (c *converter) probes(u *unit) {
	if u.kind == kindJob {
		return
	}
	text := u.healthcheckText()
	if text == "" {
		disabled := u.svc.HealthCheck != nil && (u.svc.HealthCheck.Disable || len(u.svc.HealthCheck.Test) > 0 && u.svc.HealthCheck.Test[0] == "NONE")
		if len(u.ports) == 0 {
			c.issue(u, "info", "healthcheck", "No healthcheck and no port: the workload has no probes, so Kubernetes only restarts it when the process exits. Add a healthcheck command.", "healthcheck")
			return
		}
		p := u.ports[0]
		u.readiness = &probe{kind: "tcp", port: p.name, period: 10, timeout: 3, failure: 3}
		u.liveness = &probe{kind: "tcp", port: p.name, period: 20, timeout: 3, failure: 3}
		u.startup = &probe{kind: "tcp", port: p.name, period: 5, timeout: 3, failure: 30}
		why := "no healthcheck"
		if disabled {
			why = "healthcheck disabled"
		}
		c.mapped(u.name, "("+why+")", fmt.Sprintf("TCP probes on port %s (startup 150 s, readiness, liveness)", p.name))
		return
	}
	interval, timeout, retries, startPeriod := 30, 30, 3, 0
	if h := u.svc.HealthCheck; h != nil && !h.Disable {
		interval, timeout = seconds(h.Interval, 30*time.Second), seconds(h.Timeout, 30*time.Second)
		if h.Retries != nil {
			retries = int(*h.Retries)
		}
		if h.StartPeriod != nil {
			startPeriod = seconds(h.StartPeriod, 0)
		}
	} else if f := u.facts.Healthcheck; f != nil {
		interval, timeout = parseSeconds(f.Interval, 30*time.Second), parseSeconds(f.Timeout, 30*time.Second)
		if f.Retries != nil {
			retries = *f.Retries
		}
		if f.StartPeriod != "" {
			startPeriod = parseSeconds(f.StartPeriod, 0)
		}
	}
	retries = max(retries, 1)
	check := probe{}
	if m := httpCheckRe.FindStringSubmatch(text); m != nil && !strings.Contains(text, "grep") && !strings.Contains(text, "&&") {
		n := 80
		if m[1] == "https" {
			n = 443
		}
		if m[2] != "" {
			n, _ = strconv.Atoi(m[2])
		}
		check = probe{kind: "http", path: cmpOr(m[3], "/"), port: strconv.Itoa(n)}
		for _, p := range u.ports {
			if p.number == n && p.protocol == "TCP" {
				check.port = p.name
			}
		}
		if m[1] == "https" {
			check.scheme = "HTTPS"
		}
	} else {
		check = probe{kind: "exec", command: u.checkCommand()}
	}
	ready := check
	ready.period, ready.timeout, ready.failure = interval, timeout, retries
	start := check
	start.period = min(interval, 5)
	if h := u.svc.HealthCheck; h != nil && h.StartInterval != nil {
		start.period = seconds(h.StartInterval, 5*time.Second)
	}
	start.timeout = timeout
	start.failure = max(int(math.Ceil(float64(startPeriod+interval*retries)/float64(start.period))), 6)
	u.readiness, u.startup = &ready, &start
	// Liveness restarts the container: a dependency outage must not kill
	// it, so it checks the process listens; without a port, the check
	// itself with more patience.
	if len(u.ports) > 0 {
		lp := u.ports[0].name
		if check.kind == "http" {
			lp = check.port
		}
		u.liveness = &probe{kind: "tcp", port: lp, period: max(interval, 10), timeout: min(timeout, 5), failure: 3}
	} else {
		live := check
		live.period, live.timeout, live.failure = max(interval, 10), timeout, max(retries, 3)
		u.liveness = &live
	}
	from := "healthcheck"
	if u.svc.HealthCheck == nil {
		from = "Dockerfile HEALTHCHECK"
	}
	desc := "exec " + strings.Join(check.command, " ")
	if check.kind == "http" {
		desc = "HTTP GET " + check.path + " on " + check.port
	}
	c.mapped(u.name, from+": "+text, fmt.Sprintf("readiness + startup probes (%s; startup allows %d s), liveness %s", desc, start.period*start.failure, u.liveness.kind))
}

// checkCommand is the exec form of the service's check.
func (u *unit) checkCommand() []string {
	if h := u.svc.HealthCheck; h != nil && len(h.Test) > 0 {
		switch h.Test[0] {
		case "CMD":
			return escapeK8s(h.Test[1:])
		case "CMD-SHELL":
			return escapeK8s([]string{"sh", "-c", strings.Join(h.Test[1:], " ")})
		}
		return escapeK8s([]string{"sh", "-c", strings.Join(h.Test, " ")})
	}
	t := u.healthcheckText()
	if strings.ContainsAny(t, "|&;<>$*?`") {
		return escapeK8s([]string{"sh", "-c", t})
	}
	return escapeK8s(compose.Words(t))
}

// ---------------------------------------------------------------------------
// Resources

func cpuQuantity(cpus float64) string {
	m := int64(math.Round(cpus * 1000))
	if m%1000 == 0 {
		return strconv.FormatInt(m/1000, 10)
	}
	return strconv.FormatInt(m, 10) + "m"
}

func memQuantity(b int64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return strconv.FormatInt(b>>30, 10) + "Gi"
	case b >= 1<<20 && b%(1<<20) == 0:
		return strconv.FormatInt(b>>20, 10) + "Mi"
	case b >= 1<<10 && b%(1<<10) == 0:
		return strconv.FormatInt(b>>10, 10) + "Ki"
	}
	return strconv.FormatInt(b, 10)
}

func (c *converter) resources(u *unit) {
	var limCPU, reqCPU float64
	var limMem, reqMem int64
	if d := u.svc.Deploy; d != nil {
		if l := d.Resources.Limits; l != nil {
			limCPU, limMem = float64(l.NanoCPUs), int64(l.MemoryBytes)
		}
		if r := d.Resources.Reservations; r != nil {
			reqCPU, reqMem = float64(r.NanoCPUs), int64(r.MemoryBytes)
		}
	}
	if limCPU == 0 && u.svc.CPUS > 0 {
		limCPU = float64(u.svc.CPUS)
	}
	if limMem == 0 && u.svc.MemLimit > 0 {
		limMem = int64(u.svc.MemLimit)
	}
	if reqMem == 0 && u.svc.MemReservation > 0 {
		reqMem = int64(u.svc.MemReservation)
	}
	defaulted := []string{}
	if limCPU == 0 {
		limCPU = 0.5
		if u.data {
			limCPU = 1
		}
		defaulted = append(defaulted, "cpu limit "+cpuQuantity(limCPU))
	}
	if limMem == 0 {
		limMem = 256 << 20
		if u.data {
			limMem = 512 << 20
		}
		defaulted = append(defaulted, "memory limit "+memQuantity(limMem))
	}
	if reqCPU == 0 {
		// A quarter of the limit (at least 50m): bursts stay possible
		// without reserving the whole limit on the node.
		reqCPU = max(math.Round(limCPU*250/5)*5/1000, 0.05)
	}
	if reqMem == 0 {
		// Memory isn't compressible: request what the limit allows so the
		// scheduler never overcommits it.
		reqMem = limMem
	}
	u.limits = quantities{cpu: cpuQuantity(limCPU), memory: memQuantity(limMem)}
	u.requests = quantities{cpu: cpuQuantity(min(reqCPU, limCPU)), memory: memQuantity(min(reqMem, limMem))}
	c.mapped(u.name, "deploy.resources", fmt.Sprintf("requests cpu %s / memory %s, limits cpu %s / memory %s", u.requests.cpu, u.requests.memory, u.limits.cpu, u.limits.memory))
	if len(defaulted) > 0 {
		c.issue(u, "info", "resources", "No deploy.resources limits: defaulted "+strings.Join(defaulted, ", ")+". Tune them with the Perf verdict after a load test.")
	}
}

// ---------------------------------------------------------------------------
// Dependencies

func (c *converter) dependencies(u *unit) {
	for _, dep := range slices.Sorted(maps.Keys(u.svc.DependsOn)) {
		d := u.svc.DependsOn[dep]
		du := c.byName[dep]
		if du == nil {
			continue
		}
		if du.kind == kindJob {
			c.issue(u, "info", "depends_on", fmt.Sprintf("%s waits for the one-shot %s to complete; Kubernetes starts it right away (it is a Job now), so make %s retry until what the job prepares exists.", u.name, dep, u.name), "depends_on")
			continue
		}
		if len(du.ports) == 0 {
			c.issue(u, "info", "depends_on", fmt.Sprintf("%s has no port to wait for, so %s starts without waiting for it; Kubernetes restarts it if it fails meanwhile.", dep, u.name), "depends_on")
			continue
		}
		p := du.ports[0].number
		// The port this service connects to, when its environment says.
		for _, e := range u.env {
			for _, m := range hostPortRe.FindAllStringSubmatch(e.value, -1) {
				if m[1] == dep {
					n, _ := strconv.Atoi(m[2])
					for _, dp := range du.ports {
						if dp.number == n {
							p = n
						}
					}
				}
			}
		}
		u.waits = append(u.waits, wait{service: dep, host: du.k8s, port: p})
		cond := d.Condition
		if cond == "" {
			cond = types.ServiceConditionStarted
		}
		c.mapped(u.name, "depends_on: "+dep+" ("+cond+")", fmt.Sprintf("init container wait-for-%s: waits until Service %s:%d accepts connections (only ready pods, so %s's readiness probe gates it)", du.k8s, du.k8s, p, dep))
	}
}

// ---------------------------------------------------------------------------
// Compose features without an equivalent

func (c *converter) unsupported(u *unit) {
	s := u.svc
	info := func(cond bool, field, detail string) {
		if cond {
			c.issue(u, "info", field, detail, field)
		}
	}
	warn := func(cond bool, field, detail string) {
		if cond {
			c.issue(u, "warning", field, detail, field)
		}
	}
	info(s.ContainerName != "", "container_name", "Pod names are generated; other services reach this one through its Service "+u.k8s+".")
	info(s.Logging != nil || s.LogDriver != "", "logging", "The kubelet rotates container logs (10 MiB × 5 by default); ship them with a cluster log agent instead of a logging driver.")
	info(s.Restart != "" && s.Restart != types.RestartPolicyAlways && s.Restart != types.RestartPolicyUnlessStopped && u.kind != kindJob, "restart", fmt.Sprintf("restart: %s → Deployments always restart their containers (restartPolicy Always).", s.Restart))
	info(len(s.Networks) > 1 || hasCustomNetwork(s), "networks", "Pods share one flat cluster network and reach each other by Service name; isolate them with NetworkPolicies if the compose networks separated services.")
	info(len(s.Links) > 0, "links", "Links aren't needed: every Service resolves by name in the namespace.")
	info(s.Hostname != "", "hostname", "The pod hostname is the pod name; reach this service through its Service "+u.k8s+".")
	info(s.Platform != "", "platform", fmt.Sprintf("platform %s isn't pinned (a nodeSelector would leave the pod Pending on nodes of another architecture); build/pull a multi-arch image.", s.Platform))
	info(s.StopSignal != "", "stop_signal", "Kubernetes sends SIGTERM; make the process handle it (or wrap it in a script that translates it).")
	info(len(s.Ulimits) > 0, "ulimits", "Ulimits come from the container runtime's defaults in Kubernetes; they can't be set per pod.")
	info(len(s.Labels) > 0, "labels", "Container labels aren't copied; Kubernetes labels/annotations on the objects carry the app's metadata.")
	info(s.Develop != nil, "develop", "compose watch has no equivalent: rebuild and run up again (or use a dev tool such as Tilt/Skaffold).")
	info(s.Deploy != nil && s.Deploy.Placement.Constraints != nil, "deploy.placement", "Placement constraints aren't converted: use nodeSelector/affinity for your cluster's node labels.")
	info(s.Deploy != nil && s.Deploy.UpdateConfig != nil, "deploy.update_config", "The Deployment uses the default rolling update (25% surge/unavailable).")
	info(s.Deploy != nil && s.Deploy.RestartPolicy != nil, "deploy.restart_policy", "Containers restart with Kubernetes' back-off; restart_policy delay/max_attempts don't apply.")
	info(s.ShmSize > 0, "shm_size", "Mount an emptyDir (medium: Memory) at /dev/shm if the app needs a bigger shared memory.")
	info(len(s.Sysctls) > 0, "sysctls", "Sysctls need the cluster to allow them (securityContext.sysctls); not converted.")
	info(len(s.SecurityOpt) > 0, "security_opt", "security_opt isn't converted: the pod uses the RuntimeDefault seccomp profile.")
	warn(s.NetworkMode != "" && s.NetworkMode != "bridge" && s.NetworkMode != "default", "network_mode", fmt.Sprintf("network_mode: %s isn't converted (host networking is a k8s_host_namespaces risk; container:<x> sharing needs both in one pod as sidecars).", s.NetworkMode))
	warn(s.Pid != "" || s.Ipc != "", "pid/ipc", "Host PID/IPC namespaces aren't converted (k8s_host_namespaces).")
	warn(len(s.Devices) > 0, "devices", "Devices aren't converted: they need a device plugin on the node.")
	warn(s.Privileged, "privileged", "privileged: true isn't converted (k8s_privileged): grant the specific capabilities the app needs with cap_add.")
	warn(len(s.Gpus) > 0, "gpus", "GPUs need the NVIDIA device plugin and resources.limits nvidia.com/gpu; not converted.")
	warn(len(s.PostStart) > 0 || len(s.PreStop) > 0, "lifecycle hooks", "post_start/pre_stop hooks aren't converted: use lifecycle.postStart/preStop exec hooks on the container.")
	if len(s.ExtraHosts) > 0 {
		c.mapped(u.name, "extra_hosts", "pod hostAliases")
	}
	if len(s.DNS) > 0 || len(s.DNSSearch) > 0 {
		c.mapped(u.name, "dns/dns_search", "pod dnsConfig (dnsPolicy ClusterFirst kept)")
	}
	if s.Init != nil && *s.Init {
		c.mapped(u.name, "init: true", "shareProcessNamespace: true (the pod's pause process reaps zombies and the app isn't PID 1, like tini)")
	}
	if s.StopGracePeriod != nil {
		c.mapped(u.name, "stop_grace_period", fmt.Sprintf("terminationGracePeriodSeconds: %d", seconds(s.StopGracePeriod, 30*time.Second)))
	}
	if s.WorkingDir != "" {
		c.mapped(u.name, "working_dir", "container workingDir")
	}
}

func hasCustomNetwork(s types.ServiceConfig) bool {
	for n := range s.Networks {
		if n != "default" {
			return true
		}
	}
	return false
}
