package kubeconvert

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

// openTemplate writes a docker template into a temp dir and opens it.
func openTemplate(t *testing.T, id string) *compose.Project {
	t.Helper()
	files, ok := compose.TemplateFiles(id)
	if !ok {
		t.Fatalf("no template %s", id)
	}
	return openFiles(t, files)
}

func openFiles(t *testing.T, files map[string][]byte) *compose.Project {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := compose.Open(context.Background(), dir, []string{"compose.yaml"}, nil, compose.Options{})
	if p.Model == nil {
		t.Fatalf("compose: %+v", p.Diagnostics)
	}
	return p
}

// docs parses every YAML document of the files under prefix (Helm
// templates excluded: they aren't YAML).
func docs(t *testing.T, files map[string][]byte, prefix string) map[string]*yaml.Node {
	t.Helper()
	out := map[string]*yaml.Node{}
	for rel, data := range files {
		if !strings.HasPrefix(rel, prefix) || !strings.HasSuffix(rel, ".yaml") || strings.Contains(rel, "/templates/") || strings.Contains(rel, "kustomization") {
			continue
		}
		dec := yaml.NewDecoder(strings.NewReader(string(data)))
		for {
			var n yaml.Node
			if err := dec.Decode(&n); err != nil {
				break
			}
			d := n.Content[0]
			if get(d, "kind") == nil {
				continue
			}
			k, name := docKind(d)
			out[k+"/"+name] = d
		}
	}
	return out
}

// at walks a mapping by keys; ints index sequences; "name=x" selects the
// item of a sequence with that name.
func at(n *yaml.Node, path ...string) *yaml.Node {
	for _, p := range path {
		if n == nil {
			return nil
		}
		if k, v, ok := strings.Cut(p, "="); ok && n.Kind == yaml.SequenceNode {
			var found *yaml.Node
			for _, it := range n.Content {
				if x := get(it, k); x != nil && x.Value == v {
					found = it
				}
			}
			n = found
			continue
		}
		if n.Kind == yaml.SequenceNode {
			i := 0
			for _, r := range p {
				i = i*10 + int(r-'0')
			}
			if i >= len(n.Content) {
				return nil
			}
			n = n.Content[i]
			continue
		}
		n = get(n, p)
	}
	return n
}

func val(n *yaml.Node, path ...string) string {
	if x := at(n, path...); x != nil {
		return x.Value
	}
	return ""
}

func TestConvertNodePostgresRedisManifests(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-node-postgres-redis"), Options{Format: model.ConvertManifests, Name: "notes"})
	if err != nil {
		t.Fatal(err)
	}
	d := docs(t, res.Files, "k8s/")
	api, db, cache := d["Deployment/api"], d["StatefulSet/db"], d["StatefulSet/cache"]
	if api == nil || db == nil || cache == nil {
		t.Fatalf("objects: %v", keys(d))
	}
	// The build context is kept and built as api:dev.
	if _, ok := res.Files["api/Dockerfile"]; !ok {
		t.Error("build context not copied")
	}
	if len(res.Model.Builds) != 1 || res.Model.Builds[0].Image != "api:dev" || res.Model.Builds[0].Context != "api" {
		t.Errorf("builds %+v", res.Model.Builds)
	}
	c := at(api, "spec", "template", "spec", "containers", "0")
	if val(c, "image") != "api:dev" || val(c, "imagePullPolicy") != "IfNotPresent" {
		t.Errorf("api image %s %s", val(c, "image"), val(c, "imagePullPolicy"))
	}
	// Healthcheck → HTTP readiness; liveness only checks the port.
	if val(c, "readinessProbe", "httpGet", "path") != "/health" || val(c, "readinessProbe", "httpGet", "port") != "http" {
		t.Errorf("readiness %v", encodeOne(at(c, "readinessProbe")))
	}
	if val(c, "livenessProbe", "tcpSocket", "port") != "http" || at(c, "startupProbe") == nil {
		t.Error("liveness/startup")
	}
	// deploy.resources → limits; requests set.
	if val(c, "resources", "limits", "memory") != "256Mi" || val(c, "resources", "limits", "cpu") != "1" || val(c, "resources", "requests", "cpu") != "250m" || val(c, "resources", "requests", "memory") != "256Mi" {
		t.Errorf("resources %s", encodeOne(at(c, "resources")))
	}
	// USER node → UID 1000.
	ps := at(api, "spec", "template", "spec", "securityContext")
	if val(ps, "runAsNonRoot") != "true" || val(ps, "runAsUser") != "1000" {
		t.Errorf("security %s", encodeOne(ps))
	}
	// Credentials in the Secret, the rest in the ConfigMap.
	sec, cm := d["Secret/api-secret"], d["ConfigMap/api-env"]
	if val(sec, "stringData", "DATABASE_URL") != "postgres://app:app-dev-password@db:5432/app" {
		t.Errorf("secret %s", encodeOne(sec))
	}
	if val(cm, "data", "REDIS_URL") != "redis://cache:6379" || at(cm, "data", "DATABASE_URL") != nil {
		t.Errorf("configmap %s", encodeOne(cm))
	}
	if val(d["Secret/db-secret"], "stringData", "POSTGRES_PASSWORD") != "app-dev-password" {
		t.Error("db password not in its Secret")
	}
	// depends_on → init containers waiting for the Services.
	ic := at(api, "spec", "template", "spec", "initContainers")
	if ic == nil || len(ic.Content) != 2 || !strings.Contains(val(ic, "name=wait-for-db", "command", "2"), "nc -z -w 2 db 5432") {
		t.Errorf("init containers %s", encodeOne(ic))
	}
	// init: true → shareProcessNamespace.
	if val(api, "spec", "template", "spec", "shareProcessNamespace") != "true" {
		t.Error("init: true not mapped")
	}
	// Named volumes of data services → volumeClaimTemplates; postgres runs
	// as UID 70 with PGDATA below the mount.
	if val(db, "spec", "volumeClaimTemplates", "0", "metadata", "name") != "pgdata" || val(db, "spec", "serviceName") != "db-headless" {
		t.Errorf("db %s", encodeOne(at(db, "spec")))
	}
	if val(db, "spec", "template", "spec", "securityContext", "runAsUser") != "70" || val(db, "spec", "template", "spec", "securityContext", "fsGroup") != "70" {
		t.Error("postgres security")
	}
	if val(d["ConfigMap/db-env"], "data", "PGDATA") != "/var/lib/postgresql/data/pgdata" {
		t.Error("PGDATA")
	}
	dbc := at(db, "spec", "template", "spec", "containers", "0")
	if !slices.Equal(execOf(at(dbc, "readinessProbe")), []string{"sh", "-c", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"}) {
		t.Errorf("db probe %s", encodeOne(at(dbc, "readinessProbe")))
	}
	if d["Service/db"] == nil || val(d["Service/db"], "spec", "ports", "0", "port") != "5432" || d["Service/db-headless"] == nil || d["Service/cache"] == nil {
		t.Error("services")
	}
	// The published API port → Ingress.
	ing := d["Ingress/notes"]
	if val(ing, "spec", "rules", "0", "host") != "api.notes.localhost" || val(ing, "spec", "rules", "0", "http", "paths", "0", "backend", "service", "port", "name") != "http" {
		t.Errorf("ingress %s", encodeOne(ing))
	}
	for _, n := range d {
		if k := val(n, "kind"); k != "Namespace" && val(n, "metadata", "namespace") != "notes" {
			t.Errorf("%s has no namespace", k)
		}
	}
	if !hasIssue(res.Report, "logging") {
		t.Errorf("logging not reported: %+v", res.Report.Unmapped)
	}
	if len(res.Report.Mappings) == 0 || len(res.Report.Sources) != 1 || res.Report.Sources[0].Path != "k8s" {
		t.Errorf("report %+v", res.Report)
	}
}

func execOf(p *yaml.Node) []string {
	var out []string
	if c := at(p, "exec", "command"); c != nil {
		for _, it := range c.Content {
			out = append(out, it.Value)
		}
	}
	return out
}

func hasIssue(r model.KubeConvertReport, feature string) bool {
	for _, is := range r.Unmapped {
		if is.Feature == feature {
			return true
		}
	}
	return false
}

func keys(m map[string]*yaml.Node) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestConvertBindMountsToConfigMaps(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-go-nginx"), Options{Name: "gonginx"})
	if err != nil {
		t.Fatal(err)
	}
	d := docs(t, res.Files, "k8s/")
	cm := d["ConfigMap/proxy-nginx-conf"]
	if !strings.Contains(val(cm, "data", "nginx.conf"), "server api:8080;") {
		t.Fatalf("nginx.conf configmap %v", keys(d))
	}
	proxy := d["Deployment/proxy"]
	m := at(proxy, "spec", "template", "spec", "containers", "0", "volumeMounts", "0")
	if val(m, "mountPath") != "/etc/nginx/conf.d/default.conf" || val(m, "subPath") != "nginx.conf" || val(m, "readOnly") != "true" {
		t.Errorf("mount %s", encodeOne(m))
	}
	// distroless nonroot → 65532, CMD healthcheck → exec array.
	api := d["Deployment/api"]
	if val(api, "spec", "template", "spec", "securityContext", "runAsUser") != "65532" {
		t.Error("distroless uid")
	}
	c := at(api, "spec", "template", "spec", "containers", "0")
	if !slices.Equal(execOf(at(c, "readinessProbe")), []string{"/api", "-healthcheck"}) {
		t.Errorf("exec probe %s", encodeOne(at(c, "readinessProbe")))
	}
	// expose 8080 (and nginx.conf's api:8080) → Service api.
	if val(d["Service/api"], "spec", "ports", "0", "port") != "8080" {
		t.Error("api service port")
	}
	// The proxy waits for the API.
	if !strings.Contains(val(proxy, "spec", "template", "spec", "initContainers", "0", "command", "2"), "api 8080") {
		t.Error("proxy wait")
	}
	// Folder mounts (nginx-static) keep every file.
	res, err = Convert(openTemplate(t, "docker-nginx-static"), Options{Name: "site"})
	if err != nil {
		t.Fatal(err)
	}
	d = docs(t, res.Files, "k8s/")
	site := d["ConfigMap/web-site"]
	if at(site, "data", "index.html") == nil || at(site, "data", "style.css") == nil {
		t.Errorf("site configmap %s", encodeOne(site))
	}
}

func TestConvertRabbitmqPorts(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-rabbitmq-worker"), Options{Name: "jobs"})
	if err != nil {
		t.Fatal(err)
	}
	d := docs(t, res.Files, "k8s/")
	broker := d["Service/broker"]
	var ports []string
	for _, p := range at(broker, "spec", "ports").Content {
		ports = append(ports, val(p, "name")+":"+val(p, "port"))
	}
	if !slices.Contains(ports, "amqp:5672") || !slices.Contains(ports, "http:15672") {
		t.Errorf("broker ports %v", ports)
	}
	// Loopback-only management UI: no Ingress rule; the producer gets one.
	ing := d["Ingress/jobs"]
	if len(at(ing, "spec", "rules").Content) != 1 || val(ing, "spec", "rules", "0", "host") != "producer.jobs.localhost" {
		t.Errorf("ingress %s", encodeOne(ing))
	}
	// The worker has no port: exec probes, no Service, still a Deployment.
	w := d["Deployment/worker"]
	if d["Service/worker"] != nil || !slices.Equal(execOf(at(w, "spec", "template", "spec", "containers", "0", "readinessProbe")), []string{"sh", "-c", "test -f /tmp/worker-ready"}) {
		t.Error("worker")
	}
	// python useradd app → 1000.
	if val(w, "spec", "template", "spec", "securityContext", "runAsUser") != "1000" {
		t.Error("worker uid")
	}
}

func TestConvertKustomize(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-fastapi-postgres"), Options{Format: model.ConvertKustomize, Name: "fastapi", Namespace: "fa"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"k8s/base/kustomization.yaml", "k8s/overlays/dev/kustomization.yaml", "k8s/overlays/prod/kustomization.yaml", "k8s/overlays/prod/pdb.yaml", "k8s/base/env/api.secret.env", "k8s/base/env/db.env"} {
		if _, ok := res.Files[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	var k yaml.Node
	if err := yaml.Unmarshal(res.Files["k8s/base/kustomization.yaml"], &k); err != nil {
		t.Fatal(err)
	}
	if val(k.Content[0], "namespace") != "fa" || val(k.Content[0], "secretGenerator", "name=api-secret", "envs", "0") != "env/api.secret.env" {
		t.Errorf("kustomization %s", res.Files["k8s/base/kustomization.yaml"])
	}
	if !strings.Contains(string(res.Files["k8s/base/env/api.secret.env"]), "DATABASE_URL=postgresql://app:app-dev-password@db:5432/app") {
		t.Error("secret env file")
	}
	var prod yaml.Node
	_ = yaml.Unmarshal(res.Files["k8s/overlays/prod/kustomization.yaml"], &prod)
	if val(prod.Content[0], "replicas", "name=api", "count") != "2" || at(prod.Content[0], "replicas", "name=db") != nil {
		t.Errorf("prod %s", res.Files["k8s/overlays/prod/kustomization.yaml"])
	}
	if !strings.Contains(string(res.Files["k8s/overlays/prod/kustomization.yaml"]), "value: nginx") {
		t.Error("prod ingress class")
	}
	if res.Report.Sources[0].Kind != model.KubeKustomize || res.Report.Sources[0].Path != "k8s/base" || !slices.Equal(res.Report.Environments, []string{"dev", "prod"}) {
		t.Errorf("sources %+v", res.Report.Sources)
	}
	// Base objects have no namespace (kustomize sets it) and no inline
	// ConfigMaps.
	d := docs(t, res.Files, "k8s/base/")
	if d["ConfigMap/api-env"] != nil || val(d["Deployment/api"], "metadata", "namespace") != "" {
		t.Error("base objects")
	}
}

func TestConvertHelm(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-spring-mysql"), Options{Format: model.ConvertHelm, Name: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"chart/Chart.yaml", "chart/values.yaml", "chart/values-dev.yaml", "chart/values-prod.yaml", "chart/templates/api-deployment.yaml", "chart/templates/db-statefulset.yaml", "chart/templates/api-pdb.yaml", "chart/templates/ingress.yaml", "chart/templates/api-secret.yaml", "chart/templates/NOTES.txt"} {
		if _, ok := res.Files[f]; !ok {
			t.Errorf("missing %s", f)
		}
	}
	var v yaml.Node
	if err := yaml.Unmarshal(res.Files["chart/values.yaml"], &v); err != nil {
		t.Fatal(err)
	}
	root := v.Content[0]
	if val(root, "api", "image", "repository") != "api" || val(root, "api", "image", "tag") != "dev" || val(root, "api", "replicaCount") != "1" ||
		val(root, "api", "resources", "limits", "memory") != "768Mi" || val(root, "db", "persistence", "mysqldata", "size") != "1Gi" ||
		val(root, "api", "secretEnv", "SPRING_DATASOURCE_PASSWORD") != "app-dev-password" || val(root, "ingress", "hosts", "api") != "api.shop.localhost" {
		t.Errorf("values:\n%s", res.Files["chart/values.yaml"])
	}
	dep := string(res.Files["chart/templates/api-deployment.yaml"])
	for _, want := range []string{
		`image: "{{ .Values.api.image.repository }}:{{ .Values.api.image.tag }}"`,
		`replicas: {{ .Values.api.replicaCount }}`,
		"resources:\n            {{- toYaml .Values.api.resources | nindent 12 }}",
		`app.kubernetes.io/instance: {{ .Release.Name }}`,
		`checksum/api-secret: {{ include (print $.Template.BasePath "/api-secret.yaml") . | sha256sum }}`,
	} {
		if !strings.Contains(dep, want) {
			t.Errorf("deployment template lacks %q:\n%s", want, dep)
		}
	}
	sec := string(res.Files["chart/templates/api-secret.yaml"])
	if !strings.Contains(sec, "stringData:\n  {{- range $k, $v := .Values.api.secretEnv }}\n  {{ $k }}: {{ $v | toString | quote }}\n  {{- end }}") {
		t.Errorf("secret template:\n%s", sec)
	}
	if !strings.HasPrefix(string(res.Files["chart/templates/api-pdb.yaml"]), "{{- if .Values.api.pdb.enabled }}") {
		t.Error("pdb condition")
	}
	var prod yaml.Node
	_ = yaml.Unmarshal(res.Files["chart/values-prod.yaml"], &prod)
	if val(prod.Content[0], "api", "replicaCount") != "2" || val(prod.Content[0], "api", "pdb", "enabled") != "true" || at(prod.Content[0], "db") != nil {
		t.Errorf("values-prod:\n%s", res.Files["chart/values-prod.yaml"])
	}
	if s := res.Report.Sources[0]; s.Kind != model.KubeHelm || s.Path != "chart" || s.Release != "shop" || s.Namespace != "shop" {
		t.Errorf("source %+v", s)
	}
}

func TestConvertUnmappedAndErrors(t *testing.T) {
	p := openFiles(t, map[string][]byte{"compose.yaml": []byte(`services:
  app:
    image: nginx
    privileged: true
    network_mode: host
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - cache:/cache
  other:
    image: busybox:1.37
    command: ["sh", "-c", "echo $(date)"]
    volumes:
      - cache:/cache
volumes:
  cache:
`)})
	res, err := Convert(p, Options{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"privileged", "network_mode", "bind mount", "shared volume"} {
		if !hasIssue(res.Report, f) {
			t.Errorf("%s not reported: %+v", f, res.Report.Unmapped)
		}
	}
	d := docs(t, res.Files, "k8s/")
	// nginx without a tag is pinned to the catalog tag.
	if img := val(d["Deployment/app"], "spec", "template", "spec", "containers", "0", "image"); !strings.HasPrefix(img, "nginx:") || img == "nginx:latest" {
		t.Errorf("image %s", img)
	}
	// $( in commands is escaped for Kubernetes.
	if got := val(d["Deployment/other"], "spec", "template", "spec", "containers", "0", "args", "2"); got != "echo $$(date)" {
		t.Errorf("args %q", got)
	}
	// One PVC for the shared volume.
	if d["PersistentVolumeClaim/cache"] == nil {
		t.Error("pvc")
	}
	if _, err := Convert(p, Options{Name: "Bad_Name"}); err == nil {
		t.Error("invalid name accepted")
	}
	if _, err := Convert(p, Options{Format: "ansible"}); err == nil {
		t.Error("invalid format accepted")
	}
}

func TestConvertAllTemplates(t *testing.T) {
	for _, tmpl := range compose.Templates() {
		for _, f := range []string{model.ConvertManifests, model.ConvertKustomize, model.ConvertHelm} {
			res, err := Convert(openTemplate(t, tmpl.ID), Options{Format: f, Name: "app"})
			if err != nil {
				t.Fatalf("%s %s: %v", tmpl.ID, f, err)
			}
			for rel, data := range res.Files {
				if f == model.ConvertHelm && strings.Contains(rel, "/templates/") || !strings.HasSuffix(rel, ".yaml") {
					continue
				}
				dec := yaml.NewDecoder(strings.NewReader(string(data)))
				for {
					var n yaml.Node
					err := dec.Decode(&n)
					if err != nil {
						if err.Error() != "EOF" {
							t.Errorf("%s %s %s: %v", tmpl.ID, f, rel, err)
						}
						break
					}
				}
			}
		}
	}
}

func TestSuggestName(t *testing.T) {
	for in, want := range map[string]string{
		"Node.js API + PostgreSQL + Redis": "node-js-api-postgresql-redis",
		"  Shop_Backend  ":                 "shop-backend",
		"3 tier app":                       "s-3-tier-app",
		"¡¡!!":                             "app",
	} {
		if got := SuggestName(in); got != want || !ValidName(got) {
			t.Errorf("SuggestName(%q) = %q, want %q", in, got, want)
		}
	}
}
