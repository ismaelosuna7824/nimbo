package api

import (
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func (e *testEnv) createDocker(body map[string]string) model.Workflow {
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", body, &wf)
	if !wf.IsDocker() || wf.Compose == nil {
		e.t.Fatalf("not a docker workflow: %+v", wf)
	}
	return wf
}

func (e *testEnv) file(id, rel string) string {
	data, err := os.ReadFile(filepath.Join(e.st.AssetsDir(id), filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(data)
}

func composeNodeIDs(wf *model.Workflow) []string {
	var out []string
	for _, n := range wf.Compose.Graph.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestDockerWorkflowFromTemplate(t *testing.T) {
	e := newEnv(t)
	var templates []model.Template
	e.mustCall(http.StatusOK, "GET", "/catalog/templates", nil, &templates)
	docker := slices.DeleteFunc(slices.Clone(templates), func(t model.Template) bool { return t.Kind != model.WorkflowDocker })
	if len(docker) != 11 {
		t.Fatalf("docker templates: %d", len(docker))
	}
	for _, tmpl := range docker {
		wf := e.createDocker(map[string]string{"templateId": tmpl.ID})
		if len(wf.Diagnostics) > 0 {
			t.Errorf("%s: %+v", tmpl.ID, wf.Diagnostics)
		}
		if wf.Compose.Project != "nimbo-dk-"+wf.ID || len(wf.Compose.Graph.Nodes) == 0 || len(wf.Nodes) != 0 {
			t.Errorf("%s: %+v", tmpl.ID, wf.Compose)
		}
	}
	var list []model.WorkflowSummary
	e.mustCall(http.StatusOK, "GET", "/workflows", nil, &list)
	if list[0].Kind != model.WorkflowDocker || list[0].NodeCount == 0 {
		t.Errorf("summary: %+v", list[0])
	}
}

// Edge-pattern templates carry their category and pattern through the API
// (the New workflow dialog groups them; MCP list_templates relays them):
// Docker and Kubernetes have one reverse proxy, API gateway and load
// balancer each.
func TestEdgePatternTemplates(t *testing.T) {
	e := newEnv(t)
	var raw []map[string]any
	e.mustCall(http.StatusOK, "GET", "/catalog/templates", nil, &raw)
	patterns := map[string][]string{} // kind → patterns
	for _, tmpl := range raw {
		if tmpl["category"] == nil {
			if tmpl["pattern"] != nil {
				t.Errorf("%s: pattern without category", tmpl["id"])
			}
			continue
		}
		if tmpl["category"] != model.TemplateCategoryEdgePatterns {
			t.Errorf("%s: category %v", tmpl["id"], tmpl["category"])
		}
		p, _ := tmpl["pattern"].(string)
		if p != model.PatternReverseProxy && p != model.PatternAPIGateway && p != model.PatternLoadBalancer {
			t.Errorf("%s: pattern %q", tmpl["id"], p)
		}
		kind, _ := tmpl["kind"].(string)
		patterns[kind] = append(patterns[kind], p)
	}
	want := []string{model.PatternReverseProxy, model.PatternAPIGateway, model.PatternLoadBalancer}
	for _, kind := range []string{model.WorkflowDocker, model.WorkflowKubernetes} {
		if !slices.Equal(patterns[kind], want) {
			t.Errorf("%s edge patterns %v, want %v", kind, patterns[kind], want)
		}
	}
}

func TestDockerGraphEditing(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"name": "shop", "kind": "docker"})
	id := wf.ID
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+id+"/compose/services", map[string]any{"catalogId": "postgres", "name": "db"}, &res)
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+id+"/compose/services", map[string]any{"build": map[string]string{"context": "api", "starter": "go"}, "position": map[string]float64{"x": 10, "y": 20}}, &res)
	if res.NodeID != "service:api" {
		t.Fatalf("node %s", res.NodeID)
	}
	if !strings.Contains(e.file(id, "api/Dockerfile"), "golang") || !strings.Contains(e.file(id, ".env"), "DB_POSTGRES_PASSWORD=") {
		t.Fatal("starter or .env not written")
	}
	// A service named after the image keeps the image's variable name (no doubled prefix).
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+id+"/compose/services", map[string]any{"catalogId": "postgres"}, &res)
	if env := e.file(id, ".env"); !strings.Contains(env, "\nPOSTGRES_PASSWORD=") || strings.Contains(env, "POSTGRES_POSTGRES") {
		t.Fatalf(".env:\n%s", env)
	}
	e.mustCall(http.StatusOK, "DELETE", "/workflows/"+id+"/compose/services/postgres", nil, &wf)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+id+"/compose/connect", map[string]string{"from": "api", "to": "service:db", "recipeId": "env"}, &wf)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+id+"/compose/connect", map[string]string{"from": "api", "to": "service:db", "recipeId": "depends_on_healthy"}, &wf)
	if got := e.file(id, "compose.yaml"); !strings.Contains(got, "DATABASE_URL: postgres://app:${DB_POSTGRES_PASSWORD}@db:5432/app") || !strings.Contains(got, "condition: service_healthy") {
		t.Fatalf("compose:\n%s", got)
	}
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+id+"/compose/services/api", map[string]any{"name": "backend", "restart": "unless-stopped"}, &wf)
	var pos *model.Position
	for _, n := range wf.Compose.Graph.Nodes {
		if n.ID == "service:backend" {
			pos = n.Position
		}
	}
	if pos == nil || pos.X != 10 {
		t.Errorf("position not kept over the rename: %v", composeNodeIDs(&wf))
	}
	if !slices.ContainsFunc(wf.Compose.Graph.Edges, func(ed model.ComposeEdge) bool {
		return ed.Kind == model.ComposeEdgeDependsOn && ed.Source == "service:db" && ed.Target == "service:backend"
	}) {
		t.Errorf("edges %+v", wf.Compose.Graph.Edges)
	}
	e.mustCall(http.StatusOK, "DELETE", "/workflows/"+id+"/compose/services/db", nil, &wf)
	if got := e.file(id, "compose.yaml"); strings.Contains(got, "depends_on") {
		t.Errorf("reference left:\n%s", got)
	}
	// Terraform-only endpoints refuse docker workflows and vice versa.
	e.mustCall(http.StatusBadRequest, "GET", "/workflows/"+id+"/terraform", nil, nil)
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+id+"/nodes", map[string]string{"type": "aws_s3_bucket"}, nil)
	tf := e.createEmpty("tf")
	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+tf.ID+"/compose/services", map[string]string{"image": "nginx:1"}, nil)
}

func TestDockerCodeTab(t *testing.T) {
	e := newEnv(t)
	wf := e.createDocker(map[string]string{"templateId": "docker-go-nginx"})
	id := wf.ID
	var body struct {
		Files []model.WorkflowFile `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+id+"/files", nil, &body)
	kinds := map[string]model.FileKind{}
	for _, f := range body.Files {
		kinds[f.Path] = f.Kind
	}
	if kinds["compose.yaml"] != model.FileCompose || kinds["api/Dockerfile"] != model.FileDockerfile || kinds["api/go.mod"] != model.FileAsset {
		t.Fatalf("kinds %v", kinds)
	}
	// YAML syntax errors refuse the save with file:line.
	var bad struct {
		Diagnostics []model.Diagnostic `json:"diagnostics"`
	}
	e.mustCall(http.StatusBadRequest, "PUT", "/workflows/"+id+"/terraform", map[string]any{"changes": map[string]string{"compose.yaml": "services:\n  a: [\n"}}, &bad)
	if len(bad.Diagnostics) != 1 || bad.Diagnostics[0].File != "compose.yaml" || bad.Diagnostics[0].Line == 0 {
		t.Fatalf("%+v", bad.Diagnostics)
	}
	// Moving the compose file follows it; dotfiles are allowed.
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+id+"/terraform", map[string]any{
		"moves":   []map[string]string{{"from": "compose.yaml", "to": "docker-compose.yml"}},
		"changes": map[string]string{".env": "GREETING=hi\n"},
	}, &wf)
	if !slices.Equal(wf.Compose.Files, []string{"docker-compose.yml"}) || len(wf.Compose.Graph.Nodes) == 0 {
		t.Fatalf("compose %+v", wf.Compose)
	}
	var check struct {
		Problems []model.CodeProblem `json:"problems"`
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+id+"/terraform/check", map[string]any{
		"path": "docker-compose.yml", "files": map[string]string{"docker-compose.yml": "services:\n  web:\n    image: nginx\n    depends_on: [nope]\n"},
	}, &check)
	codes := []string{}
	for _, p := range check.Problems {
		codes = append(codes, p.Code)
	}
	if !slices.Contains(codes, "dk_unknown_ref") || !slices.Contains(codes, "dk_latest_tag") {
		t.Errorf("problems %v", codes)
	}
}

func TestImportDockerProjects(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "infra", "main.tf"), "resource \"aws_sqs_queue\" \"q\" {}\n")
	writeFile(t, filepath.Join(dir, "app", "compose.yaml"), "services:\n  web:\n    build: ./web\n  db:\n    image: postgres:16\n")
	writeFile(t, filepath.Join(dir, "app", "compose.override.yaml"), "services:\n  web:\n    ports: [\"8080:80\"]\n")
	writeFile(t, filepath.Join(dir, "app", "web", "Dockerfile"), "FROM nginx:1.27\n")
	writeFile(t, filepath.Join(dir, "app", ".env"), "TAG=1\n")
	writeFile(t, filepath.Join(dir, "tool", "Dockerfile"), "FROM alpine:3.20\nEXPOSE 9000\nUSER nobody\n")
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": dir}, &scan)
	byPath := map[string]model.ImportProject{}
	for _, p := range scan.Projects {
		byPath[p.Path] = p
	}
	if byPath["infra"].Kind != "" || byPath["app"].Kind != model.WorkflowDocker || byPath["app"].Services != 2 ||
		!slices.Equal(byPath["app"].ComposeFiles, []string{"compose.yaml", "compose.override.yaml"}) || byPath["tool"].Kind != model.WorkflowDocker {
		t.Fatalf("scan %+v", scan.Projects)
	}
	if _, ok := byPath["app/web"]; ok {
		t.Error("a Dockerfile of a compose project is not a project of its own")
	}
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": dir, "projects": []string{"app", "tool", "infra"}}, &res)
	if len(res.Workflows) != 3 {
		t.Fatalf("%d workflows", len(res.Workflows))
	}
	app, tool := res.Workflows[0], res.Workflows[1]
	if !app.IsDocker() || len(app.Compose.Graph.Nodes) != 2 || e.file(app.ID, ".env") != "TAG=1\n" {
		t.Errorf("app %+v", app.Compose)
	}
	if !strings.Contains(e.file(tool.ID, "compose.yaml"), "\"9000:9000\"") {
		t.Errorf("tool compose:\n%s", e.file(tool.ID, "compose.yaml"))
	}
	if res.Workflows[2].IsDocker() {
		t.Error("infra is Terraform")
	}
}
