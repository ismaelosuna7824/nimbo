package api

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/agents"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/events"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/mcp"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
	"github.com/ismaelosuna7824/nimbo/backend/internal/settings"
	"github.com/ismaelosuna7824/nimbo/backend/internal/store"
)

type testEnv struct {
	t     testing.TB
	srv   *httptest.Server
	hub   *events.Hub
	st    *store.Store
	conns *store.Connections
	home  string
}

var testCatalog *catalog.Catalog

func newEnv(t testing.TB) *testEnv { return newEnvDocker(t, "") }

// newEnvDocker is newEnv with the sandbox driving the given docker binary
// ("" for the default).
func newEnvDocker(t testing.TB, docker string) *testEnv {
	t.Helper()
	home := t.TempDir()
	st, err := store.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if testCatalog == nil {
		if testCatalog, err = catalog.Load(); err != nil {
			t.Fatal(err)
		}
	}
	hub := events.NewHub()
	sb, err := sandbox.New(sandbox.Config{Home: home, Docker: docker, Events: hub})
	if err != nil {
		t.Fatal(err)
	}
	conns, err := store.OpenConnections(home)
	if err != nil {
		t.Fatal(err)
	}
	prefs, err := settings.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Config{
		Version: "test", Home: home, Store: st, Connections: conns, Catalog: testCatalog, Sandbox: sb,
		LoadTest: loadtest.NewEngine(loadtest.EngineConfig{Home: home, Events: hub, Resolver: sb, Connections: conns}),
		Events:   hub,
		Settings: prefs,
		Agents:   agents.New(agents.Env{Home: filepath.Join(home, "user"), GOOS: "darwin", Executable: "/opt/nimbo/bin/nimbo"}),
		MCPCatalog: func() MCPCatalog {
			return MCPCatalog{Tools: mcp.ToolCatalog(), Prompts: mcp.PromptCatalog()}
		},
	})
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return &testEnv{t: t, srv: srv, hub: hub, st: st, conns: conns, home: home}
}

// call performs a JSON request and decodes the response into out (if non-nil).
func (e *testEnv) call(method, path string, body, out any) int {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, e.srv.URL+"/api/v1"+path, rd)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			e.t.Fatalf("%s %s: decode %q: %v", method, path, data, err)
		}
	}
	return res.StatusCode
}

func (e *testEnv) mustCall(status int, method, path string, body, out any) {
	e.t.Helper()
	var raw json.RawMessage
	got := e.call(method, path, body, &raw)
	if got != status {
		e.t.Fatalf("%s %s = %d, want %d: %s", method, path, got, status, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *testEnv) createEmpty(name string) model.Workflow {
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": name}, &wf)
	return wf
}

func (e *testEnv) addNode(wfID, typ string) (model.Workflow, string) {
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wfID+"/nodes", map[string]any{"type": typ, "position": model.Position{X: 10, Y: 20}}, &res)
	return res.Workflow, res.NodeID
}

func nodeByID(wf *model.Workflow, id string) *model.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}

func errorDiags(wf *model.Workflow) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

func TestCreateFromTemplate(t *testing.T) {
	e := newEnv(t)
	events, cancel := e.hub.Subscribe("")
	defer cancel()

	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Jobs", "templateId": "aws-queue-worker"}, &wf)
	if wf.ID == "" || wf.Name != "Jobs" || len(wf.Nodes) == 0 {
		t.Fatalf("unexpected workflow: id=%q name=%q nodes=%d", wf.ID, wf.Name, len(wf.Nodes))
	}
	for _, n := range wf.Nodes {
		if n.Positioned {
			t.Fatalf("template node %s is positioned; the UI must lay it out", n.ID)
		}
	}
	if errs := errorDiags(&wf); len(errs) > 0 {
		t.Fatalf("template has error diagnostics: %+v", errs)
	}
	if len(wf.Edges) == 0 || !slices.Contains(wf.Clouds, model.CloudAWS) {
		t.Fatalf("derived data missing: edges=%d clouds=%v", len(wf.Edges), wf.Clouds)
	}

	select {
	case m := <-events:
		if m.Type != model.EventWorkflowUpdated || m.WorkflowID != wf.ID {
			t.Fatalf("event %+v", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no workflow.updated event")
	}

	var list []model.WorkflowSummary
	e.mustCall(http.StatusOK, "GET", "/workflows", nil, &list)
	if len(list) != 1 || list[0].ID != wf.ID || list[0].NodeCount != len(wf.Nodes) {
		t.Fatalf("list = %+v", list)
	}

	if got := e.call("POST", "/workflows", map[string]string{"name": "x", "templateId": "nope"}, nil); got != http.StatusBadRequest {
		t.Fatalf("unknown template status %d", got)
	}
}

func TestAddNodeAndConnect(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Wiring")
	wf, queueID := e.addNode(wf.ID, "aws_sqs_queue")
	queue := nodeByID(&wf, queueID)
	if queue == nil || queue.Position != (model.Position{X: 10, Y: 20}) {
		t.Fatalf("queue node %+v", queue)
	}
	if len(wf.Providers) == 0 {
		t.Fatal("adding an AWS node must add an aws provider block")
	}
	wf, fnID := e.addNode(wf.ID, "aws_lambda_function")

	// Connections accept addresses as well as IDs.
	var recipes []model.ConnectionRecipe
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/connections?source="+hclcodec.Address(queue)+"&target="+fnID, nil, &recipes)
	if len(recipes) == 0 {
		t.Fatal("no recipes from queue to lambda")
	}
	before := len(wf.Edges)
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/connections", map[string]string{"source": queueID, "target": fnID, "recipeId": recipes[0].ID}, &wf)
	var linked bool
	for _, edge := range wf.Edges {
		if edge.Source == queueID {
			linked = true
		}
	}
	if !linked || len(wf.Edges) <= before {
		t.Fatalf("connect did not derive an edge from the queue: %+v", wf.Edges)
	}

	stored, err := e.st.Get(wf.ID)
	if err != nil || len(stored.Edges) != len(wf.Edges) {
		t.Fatalf("connection not persisted: %v", err)
	}

	if got := e.call("POST", "/workflows/"+wf.ID+"/connections", map[string]string{"source": queueID, "target": fnID, "recipeId": "no-such-recipe"}, nil); got != http.StatusBadRequest {
		t.Fatalf("unknown recipe status %d", got)
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/nodes", map[string]string{"type": "aws_not_a_resource"}, nil); got != http.StatusBadRequest {
		t.Fatalf("unknown type status %d", got)
	}
}

func TestPutTerraformRoundTripKeepsLayout(t *testing.T) {
	e := newEnv(t)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"name": "Code", "templateId": "aws-queue-worker"}, &wf)
	first := wf.Nodes[0]
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+wf.ID+"/nodes/"+first.ID, map[string]any{"position": model.Position{X: 400, Y: 300}}, &wf)

	var tf struct {
		Files map[string]string `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &tf)
	if !strings.Contains(tf.Files["main.tf"], `"`+first.Name+`"`) {
		t.Fatalf("main.tf does not contain node %s:\n%s", first.Name, tf.Files["main.tf"])
	}
	tf.Files["main.tf"] += "\nresource \"aws_s3_bucket\" \"extra\" {\n  bucket = \"extra-bucket\"\n}\n"

	var next model.Workflow
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": tf.Files}, &next)
	if len(next.Nodes) != len(wf.Nodes)+1 {
		t.Fatalf("nodes %d, want %d", len(next.Nodes), len(wf.Nodes)+1)
	}
	kept := nodeByID(&next, first.ID)
	if kept == nil || !kept.Positioned || kept.Position != (model.Position{X: 400, Y: 300}) {
		t.Fatalf("node identity/layout lost on code edit: %+v", kept)
	}
	if next.ID != wf.ID || next.Name != "Code" || !next.CreatedAt.Equal(wf.CreatedAt) {
		t.Fatalf("workflow metadata lost: %s %s %v", next.ID, next.Name, next.CreatedAt)
	}
	var extra *model.Node
	for i := range next.Nodes {
		if next.Nodes[i].Name == "extra" {
			extra = &next.Nodes[i]
		}
	}
	if extra == nil || extra.Positioned {
		t.Fatalf("new node missing or positioned: %+v", extra)
	}

	var again struct {
		Files map[string]string `json:"files"`
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/terraform", nil, &again)
	if !strings.Contains(again.Files["main.tf"], `bucket = "extra-bucket"`) {
		t.Fatalf("re-export lost the edit:\n%s", again.Files["main.tf"])
	}

	var apiErr struct {
		Error string `json:"error"`
	}
	if got := e.call("PUT", "/workflows/"+wf.ID+"/terraform", map[string]any{"files": map[string]string{"main.tf": "resource \"x\" {"}}, &apiErr); got != http.StatusBadRequest || apiErr.Error == "" {
		t.Fatalf("broken HCL: status %d, body %+v", got, apiErr)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportDirWithAssetsAndExport(t *testing.T) {
	e := newEnv(t)
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "main.tf"), `
variable "bucket_name" {
  type = string
}

resource "aws_s3_bucket" "site" {
  bucket = var.bucket_name
}
`)
	writeFile(t, filepath.Join(src, "environments", "dev.tfvars"), "bucket_name = \"dev-site\"\n")
	writeFile(t, filepath.Join(src, "environments", "dev.env.example"), "AWS_PROFILE=\n")
	writeFile(t, filepath.Join(src, "src", "index.js"), "exports.handler = async () => 'ok'\n")
	writeFile(t, filepath.Join(src, ".terraform", "providers", "x"), "binary")
	writeFile(t, filepath.Join(src, ".terraform.lock.hcl"), "lock")
	writeFile(t, filepath.Join(src, "terraform.tfstate"), "{}")
	writeFile(t, filepath.Join(src, "terraform.tfstate.backup"), "{}")
	writeFile(t, filepath.Join(src, ".git", "config"), "[core]")

	wf := e.importOne(map[string]string{"dir": src})
	if wf.Name != filepath.Base(src) || len(wf.Nodes) != 1 || wf.Nodes[0].Type != "aws_s3_bucket" {
		t.Fatalf("imported workflow: name=%q nodes=%+v", wf.Name, wf.Nodes)
	}
	wantVars := []model.EnvVar{{Key: "AWS_PROFILE", Secret: true}}
	if len(wf.Environments) != 1 || wf.Environments[0].Name != "dev" || wf.Environments[0].Values["bucket_name"].Expr != `"dev-site"` ||
		!reflect.DeepEqual(wf.Environments[0].EnvVars, wantVars) {
		t.Fatalf("environments = %+v", wf.Environments)
	}

	var assets []string
	root := e.st.AssetsDir(wf.ID)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(root, p)
			assets = append(assets, filepath.ToSlash(rel))
		}
		return nil
	})
	if !slices.Equal(assets, []string{"src/index.js"}) {
		t.Fatalf("assets = %v, want only src/index.js", assets)
	}

	out := filepath.Join(t.TempDir(), "exported")
	var res struct {
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	}
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": out, "environments": true}, &res)
	for _, want := range []string{"main.tf", "src/index.js", "environments/dev.tfvars", "environments/dev.env.example"} {
		if !slices.Contains(res.Files, want) {
			t.Fatalf("export files %v missing %s", res.Files, want)
		}
		if _, err := os.Stat(filepath.Join(out, want)); err != nil {
			t.Fatalf("%s not written: %v", want, err)
		}
	}
	tfvars, _ := os.ReadFile(filepath.Join(out, "environments", "dev.tfvars"))
	if !strings.Contains(string(tfvars), `"dev-site"`) {
		t.Fatalf("dev.tfvars = %q", tfvars)
	}

	// The export re-imports into an equivalent workflow, environments included.
	again := e.importOne(map[string]string{"dir": out, "name": "Again"})
	if len(again.Nodes) != 1 || len(again.Environments) != 1 || !reflect.DeepEqual(again.Environments[0].EnvVars, wantVars) {
		t.Fatalf("re-import: nodes=%d envs=%+v", len(again.Nodes), again.Environments)
	}

	if got := e.call("POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": "relative/dir"}, nil); got != http.StatusBadRequest {
		t.Fatalf("relative export dir status %d", got)
	}
}

func TestImportFilesRejectsTraversal(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"files": map[string]string{"main.tf": `resource "aws_sqs_queue" "q" {}`, "../evil.js": "x"}}
	if got := e.call("POST", "/import", body, nil); got != http.StatusBadRequest {
		t.Fatalf("status %d", got)
	}
}

func TestBadExpressionProducesDiagnosticsAndBlocksPlan(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Broken")
	wf, id := e.addNode(wf.ID, "aws_sqs_queue")
	body := nodeByID(&wf, id).Body
	body.Attrs = append(body.Attrs, model.Attr{Name: "name", Value: model.Value{Expr: `aws_sqs_queue.(`}})
	e.mustCall(http.StatusOK, "PATCH", "/workflows/"+wf.ID+"/nodes/"+id, map[string]any{"body": body}, &wf)

	var found bool
	for _, d := range errorDiags(&wf) {
		if d.NodeID == id {
			found = true
		}
	}
	if !found {
		t.Fatalf("no error diagnostic on node %s: %+v", id, wf.Diagnostics)
	}

	var apiErr struct {
		Error       string             `json:"error"`
		Diagnostics []model.Diagnostic `json:"diagnostics"`
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/runs", map[string]string{"action": "plan"}, &apiErr); got != http.StatusBadRequest {
		t.Fatalf("plan with errors: status %d", got)
	}
	if len(apiErr.Diagnostics) == 0 {
		t.Fatalf("plan rejection carries no diagnostics: %+v", apiErr)
	}
	if got := e.call("POST", "/workflows/"+wf.ID+"/runs", map[string]string{"action": "apply", "environment": "nope"}, nil); got != http.StatusNotFound {
		t.Fatalf("unknown environment status %d", got)
	}
}

func TestDuplicateCopiesAssets(t *testing.T) {
	e := newEnv(t)
	wf := e.importOne(map[string]any{"files": map[string]string{
		"main.tf":      `resource "aws_sqs_queue" "q" {}`,
		"src/index.js": "exports.handler = 1",
	}})
	var dup model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/duplicate", map[string]string{"name": "Copy"}, &dup)
	if dup.ID == wf.ID || dup.Name != "Copy" || len(dup.Nodes) != 1 {
		t.Fatalf("duplicate = %+v", dup)
	}
	if _, err := os.Stat(filepath.Join(e.st.AssetsDir(dup.ID), "src", "index.js")); err != nil {
		t.Fatalf("assets not copied: %v", err)
	}
}

func TestWorkflowIcon(t *testing.T) {
	e := newEnv(t)
	icon := func(wf model.Workflow) model.WorkflowIcon {
		t.Helper()
		if wf.Icon == nil || !slices.Contains(model.WorkflowIconGlyphs, wf.Icon.Glyph) || !slices.Contains(model.WorkflowIconColors, wf.Icon.Color) {
			t.Fatalf("workflow %s icon = %+v, want a random known one", wf.ID, wf.Icon)
		}
		return *wf.Icon
	}

	// Blank and template workflows get random icons; each differs in glyph
	// from the most recently updated workflow.
	first := e.createEmpty("First")
	icon(first)
	var wf model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows", map[string]string{"templateId": "aws-serverless-api"}, &wf)
	if icon(wf).Glyph == first.Icon.Glyph {
		t.Fatalf("template workflow repeats the last glyph %q", first.Icon.Glyph)
	}
	for _, tmpl := range testCatalog.Templates() {
		if err := tmpl.Icon.Validate(); err != nil || tmpl.Icon == nil {
			t.Fatalf("template %s icon %+v: %v", tmpl.ID, tmpl.Icon, err)
		}
	}

	// PUT sets it; a body without an icon keeps it; the summary lists it.
	wf.Icon = &model.WorkflowIcon{Glyph: "rocket", Color: "pink"}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	wf.Icon = nil
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, wf, &wf)
	var got model.Workflow
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &got)
	if got.Icon == nil || *got.Icon != (model.WorkflowIcon{Glyph: "rocket", Color: "pink"}) {
		t.Fatalf("persisted icon = %+v", got.Icon)
	}
	if s := e.summary(wf.ID); s.Icon == nil || s.Icon.Glyph != "rocket" {
		t.Fatalf("summary icon = %+v", s.Icon)
	}

	// Malformed IDs are rejected and leave the stored icon alone.
	for _, bad := range []model.WorkflowIcon{
		{Glyph: "", Color: "pink"},
		{Glyph: "Rocket", Color: "pink"},
		{Glyph: "rocket", Color: "../x"},
		{Glyph: "rocket", Color: strings.Repeat("a", 33)},
	} {
		body := got
		body.Icon = &bad
		if status := e.call("PUT", "/workflows/"+wf.ID, body, nil); status != http.StatusBadRequest {
			t.Fatalf("PUT icon %+v = %d, want 400", bad, status)
		}
	}
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID, nil, &got)
	if got.Icon.Glyph != "rocket" {
		t.Fatalf("icon after rejected PUT = %+v", got.Icon)
	}

	// Duplicates get a fresh random icon, unlike the source's.
	var dup model.Workflow
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/duplicate", map[string]string{}, &dup)
	if d := icon(dup); d.Glyph == "rocket" || d.Color == "pink" {
		t.Fatalf("duplicate icon %+v repeats the source", d)
	}

	// The mascot is an explicit, valid choice.
	got.Icon = &model.WorkflowIcon{Glyph: model.MascotGlyph, Color: "violet"}
	e.mustCall(http.StatusOK, "PUT", "/workflows/"+wf.ID, got, &got)
	if got.Icon == nil || got.Icon.Glyph != model.MascotGlyph {
		t.Fatalf("mascot icon = %+v", got.Icon)
	}

	// Every imported workflow gets one too.
	imported := e.importOne(map[string]any{"files": map[string]string{"main.tf": `resource "aws_sqs_queue" "q" {}`}})
	if icon(imported).Glyph == dup.Icon.Glyph {
		t.Fatalf("import repeats the last glyph %q", dup.Icon.Glyph)
	}
}

func TestOriginAndHostChecks(t *testing.T) {
	e := newEnv(t)
	req, _ := http.NewRequest(http.MethodOptions, e.srv.URL+"/api/v1/workflows", nil)
	req.Header.Set("Origin", "tauri://localhost")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Access-Control-Allow-Origin") != "tauri://localhost" {
		t.Fatalf("preflight: %d %v", res.StatusCode, res.Header)
	}

	req, _ = http.NewRequest(http.MethodPost, e.srv.URL+"/api/v1/workflows", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Origin", "https://evil.example")
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin status %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/workflows", nil)
	req.Host = "attacker.example:7788"
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding host status %d", res.StatusCode)
	}
}

// assertNoOverlap fails when two positioned leaf cards overlap (card plus gap).
func assertNoOverlap(t *testing.T, wf *model.Workflow) {
	t.Helper()
	groups := map[string]bool{}
	for _, n := range wf.Nodes {
		if n.Parent != "" {
			groups[n.Parent] = true
		}
	}
	var cards []model.Node
	for _, n := range wf.Nodes {
		if n.Positioned && !groups[n.ID] {
			cards = append(cards, n)
		}
	}
	for i := range cards {
		for j := i + 1; j < len(cards); j++ {
			a, b := cards[i].Position, cards[j].Position
			if math.Abs(a.X-b.X) < cardWidth+cardGap && math.Abs(a.Y-b.Y) < cardHeight+cardGap {
				t.Fatalf("%s at %+v overlaps %s at %+v", cards[i].ID, a, cards[j].ID, b)
			}
		}
	}
}

func TestAddAndConnectNeverStackCards(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Layout")
	// Every node is dropped on the same spot, as an agent that omits x/y does.
	wf, queue := e.addNode(wf.ID, "aws_sqs_queue")
	assertNoOverlap(t, &wf)
	wf, fn := e.addNode(wf.ID, "aws_lambda_function")
	assertNoOverlap(t, &wf)
	wf, apiID := e.addNode(wf.ID, "aws_apigatewayv2_api")
	assertNoOverlap(t, &wf)

	connect := func(a, b string) {
		t.Helper()
		for _, pair := range [][2]string{{a, b}, {b, a}} {
			var recipes []model.ConnectionRecipe
			e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/connections?source="+pair[0]+"&target="+pair[1], nil, &recipes)
			if len(recipes) > 0 {
				before := len(wf.Nodes)
				e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/connections", map[string]string{"source": pair[0], "target": pair[1], "recipeId": recipes[0].ID}, &wf)
				t.Logf("%s -> %s via %s added %d nodes", pair[0], pair[1], recipes[0].ID, len(wf.Nodes)-before)
				return
			}
		}
		t.Fatalf("no recipe between %s and %s", a, b)
	}
	connect(queue, fn)
	assertNoOverlap(t, &wf)
	connect(apiID, fn)
	assertNoOverlap(t, &wf)
	if len(wf.Nodes) <= 5 {
		t.Fatalf("expected curated helpers to be added, got %d nodes", len(wf.Nodes))
	}

	// A node dropped on a free spot stays exactly where it was dropped.
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	free := model.Position{X: 5000, Y: 5000}
	e.mustCall(http.StatusCreated, "POST", "/workflows/"+wf.ID+"/nodes", map[string]any{"type": "aws_s3_bucket", "position": free}, &res)
	if got := nodeByID(&res.Workflow, res.NodeID).Position; got != free {
		t.Fatalf("node on a free spot moved to %+v", got)
	}
	assertNoOverlap(t, &res.Workflow)
}

func TestEndpointsNothingDeployed(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("empty")

	var info model.EndpointsInfo
	e.mustCall(http.StatusOK, "GET", "/workflows/"+wf.ID+"/endpoints", nil, &info)
	if info.WorkflowID != wf.ID || info.Resources == nil || len(info.Resources) != 0 || info.Sandbox == nil || len(info.Sandbox) != 0 {
		t.Fatalf("endpoints of an undeployed workflow: %+v", info)
	}

	e.mustCall(http.StatusBadRequest, "POST", "/workflows/"+wf.ID+"/endpoints/try", model.TryRequest{Action: "send"}, nil)
	e.mustCall(http.StatusNotFound, "POST", "/workflows/"+wf.ID+"/endpoints/try", model.TryRequest{NodeID: "aws_sqs_queue.q", Action: "send"}, nil)
	e.mustCall(http.StatusNotFound, "GET", "/workflows/nope/endpoints", nil, nil)

	res, err := http.Get(e.srv.URL + "/api/v1/workflows/" + wf.ID + "/endpoints/postman.json")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var col struct {
		Info struct{ Name, Schema string } `json:"info"`
		Item []any                         `json:"item"`
	}
	if err := json.NewDecoder(res.Body).Decode(&col); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || !strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;") ||
		col.Info.Name != "empty (Nimbo sandbox)" || !strings.Contains(col.Info.Schema, "v2.1.0") || col.Item == nil {
		t.Fatalf("postman export: %d %q %+v", res.StatusCode, res.Header.Get("Content-Disposition"), col)
	}
}

// "Try it" must stay a plain request to the emulator: once the deployed
// state is cached it runs no docker command (no Terraform container, no
// sandbox status check), however slow docker is.
func TestTryEndpointRunsNoDockerWhenWarm(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a shell script as docker")
	}
	emu := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"path":"`+r.URL.Path+`"}`)
	}))
	defer emu.Close()
	port := emu.URL[strings.LastIndex(emu.URL, ":")+1:]

	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	show := filepath.Join(dir, "show.json")
	if err := os.WriteFile(show, []byte(`{"values":{"root_module":{"resources":[
		{"address":"aws_apigatewayv2_api.http","mode":"managed","type":"aws_apigatewayv2_api","name":"http","values":{"id":"abc","protocol_type":"HTTP"}},
		{"address":"aws_apigatewayv2_stage.default","mode":"managed","type":"aws_apigatewayv2_stage","name":"default","values":{"api_id":"abc","name":"$default"}},
		{"address":"aws_apigatewayv2_route.items","mode":"managed","type":"aws_apigatewayv2_route","name":"items","values":{"api_id":"abc","route_key":"GET /items"}}]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	docker := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$1\" >> '" + log + "'\ncase \"$1\" in\n" +
		"port) echo '4566/tcp -> 127.0.0.1:" + port + "' ;;\nrun) cat '" + show + "' ;;\nimage) echo sha256:test ;;\nesac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEnvDocker(t, docker)
	wf := e.createEmpty("api")
	work := filepath.Join(e.home, "workflows", wf.ID, "work")
	if err := os.MkdirAll(filepath.Join(work, ".terraform"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "terraform.tfstate"), []byte(`{"version":4}`), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := func() []string {
		data, _ := os.ReadFile(log)
		return strings.Fields(string(data))
	}
	try := func() model.TryResult {
		var res model.TryResult
		e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/endpoints/try",
			model.TryRequest{NodeID: "aws_apigatewayv2_api.http", Action: "request", Method: "GET", Path: "/items"}, &res)
		if !res.OK || !strings.Contains(res.Body, "/execute-api/abc/$default/items") {
			t.Fatalf("try: %+v", res)
		}
		return res
	}

	try() // warms the state cache (one Terraform container) and the port cache
	if got := slices.Index(calls(), "run"); got < 0 {
		t.Fatalf("the first try must read the state with terraform show; docker calls: %v", calls())
	}
	if err := os.Truncate(log, 0); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		try()
	}
	if got := calls(); len(got) != 0 {
		t.Fatalf("warm tries ran docker %v", got)
	}
}
