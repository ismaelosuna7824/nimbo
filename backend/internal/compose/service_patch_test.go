package compose

import (
	"strings"
	"testing"
)

func TestPatchLabels(t *testing.T) {
	files := map[string]string{"compose.yaml": `services:
  api:
    image: app:1
    labels:
      team: shop # owner
      tier: web
`}
	out, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "labels", map[string]string{
			"team": "shop", "prometheus.io/scrape": "true", "prometheus.io/port": "3000", "prometheus.io/path": "/metrics",
		}))
	})
	got := out["compose.yaml"]
	if !strings.Contains(got, "team: shop # owner") || strings.Contains(got, "tier") || !strings.Contains(got, `"true"`) || !strings.Contains(got, `"3000"`) {
		t.Errorf("got:\n%s", got)
	}
	if f := facts(t, after, "api"); len(f.Labels) != 4 || f.Labels["prometheus.io/scrape"] != "true" || f.Labels["prometheus.io/port"] != "3000" || f.Labels["prometheus.io/path"] != "/metrics" {
		t.Errorf("labels %v", f.Labels)
	}

	// The list form is rewritten as a mapping; no labels removes the key.
	files["compose.yaml"] = "services:\n  api:\n    image: app:1\n    labels:\n      - team=shop\n"
	_, after = editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "labels", map[string]string{"team": "shop", "tier": "web"}))
	})
	if f := facts(t, after, "api"); f.Labels["tier"] != "web" || f.Labels["team"] != "shop" {
		t.Errorf("labels %v", f.Labels)
	}
	out, after = editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "labels", map[string]string{}))
	})
	if strings.Contains(out["compose.yaml"], "labels") || len(facts(t, after, "api").Labels) != 0 {
		t.Errorf("labels kept:\n%s", out["compose.yaml"])
	}
	if err := mustEdit(t, open(t, files)).PatchService("api", patchOf(t, "labels", map[string]string{"a b": "x"})); err == nil {
		t.Error("a label with a space accepted")
	}
}

const loggingCompose = `services:
  renderer:
    build: ./renderer
    logging:
      driver: json-file
    restart: unless-stopped
`

func TestPatchPidsAndLogging(t *testing.T) {
	files := map[string]string{
		"compose.yaml":        loggingCompose,
		"renderer/Dockerfile": "FROM node:22-alpine\nUSER node\nCMD [\"node\", \"server.js\"]\n",
	}
	if !hasCode(open(t, files), "dk_log_rotation", ServiceID("renderer")) {
		t.Fatal("the starter has no dk_log_rotation")
	}
	out, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("renderer", patchOf(t,
			"resources", map[string]any{"cpus": "0.5", "memory": "128M", "pids": 100},
			"logging", map[string]any{"driver": "json-file", "maxSize": "10m", "maxFile": "3"},
		))
	})
	want := `services:
  renderer:
    build: ./renderer
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
    restart: unless-stopped
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 128M
          pids: 100
`
	if out["compose.yaml"] != want {
		t.Errorf("got:\n%s\nwant:\n%s", out["compose.yaml"], want)
	}
	f := facts(t, after, "renderer")
	if f.Resources == nil || f.Resources.Pids != 100 || f.Resources.CPUs != "0.5" || f.Resources.Memory != "128M" {
		t.Errorf("resources %+v", f.Resources)
	}
	if f.Logging == nil || f.Logging.Driver != "json-file" || f.Logging.MaxSize != "10m" || f.Logging.MaxFile != "3" {
		t.Errorf("logging %+v", f.Logging)
	}
	if hasCode(after, "dk_log_rotation", ServiceID("renderer")) {
		t.Error("dk_log_rotation still reported")
	}

	// pids 0 removes only pids; emptied logging fields remove logging:.
	files["compose.yaml"] = want
	out, after = editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("renderer", patchOf(t,
			"resources", map[string]any{"cpus": "0.5", "memory": "128M"},
			"logging", map[string]any{},
		))
	})
	got := out["compose.yaml"]
	if strings.Contains(got, "pids") || strings.Contains(got, "logging") || !strings.Contains(got, "memory: 128M") {
		t.Errorf("got:\n%s", got)
	}
	if f := facts(t, after, "renderer"); f.Logging != nil || f.Resources == nil || f.Resources.Pids != 0 {
		t.Errorf("facts %+v %+v", f.Logging, f.Resources)
	}

	p := open(t, files)
	e, err := p.Edit()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"resources": map[string]any{"pids": -1}},
		{"resources": map[string]any{"pids": "many"}},
		{"logging": "json-file"},
	} {
		var kv []any
		for k, v := range bad {
			kv = append(kv, k, v)
		}
		if err := e.PatchService("renderer", patchOf(t, kv...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// Logging inherited through a merge key is copied into the service before
// it changes, so the shared anchor stays as it is.
func TestPatchLoggingFromAnchor(t *testing.T) {
	files := map[string]string{"compose.yaml": `x-defaults: &defaults
  logging:
    driver: json-file
    options:
      max-size: "10m"

services:
  web:
    <<: *defaults
    image: nginx:1.27-alpine
  api:
    <<: *defaults
    image: nginx:1.27-alpine
`}
	_, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("web", patchOf(t, "logging", map[string]any{"driver": "json-file", "maxSize": "10m", "maxFile": "3"}))
	})
	if f := facts(t, after, "web"); f.Logging == nil || f.Logging.MaxFile != "3" || f.Logging.MaxSize != "10m" {
		t.Errorf("web logging %+v", f.Logging)
	}
	if f := facts(t, after, "api"); f.Logging == nil || f.Logging.MaxFile != "" || f.Logging.MaxSize != "10m" {
		t.Errorf("api logging changed: %+v", f.Logging)
	}
}

const prodBase = `services:
  api:
    build: ./api
    init: true
    environment:
      LOG_LEVEL: ${LOG_LEVEL:-debug}
    volumes:
      - ./api/server.js:/app/server.js:ro
`

// "scope": "environment" writes the patch to the active environment's
// override only: lists replace the base's (!override), removed keys are
// reset, and the base files stay as they are.
func TestPatchScopeEnvironment(t *testing.T) {
	files := map[string]string{
		"compose.yaml":      prodBase,
		"compose.prod.yaml": "# prod differences\nservices: {}\n",
		"api/Dockerfile":    "FROM node:22-alpine\nUSER node\nCMD [\"node\", \"server.js\"]\n",
	}
	out, after := editReopen(t, files, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t,
			"scope", "environment",
			"volumes", []any{},
			"restart", "always",
			"init", false,
			"resources", map[string]any{"cpus": "0.5", "memory": "128M"},
			"logging", map[string]any{"driver": "json-file", "maxSize": "10m", "maxFile": "3"},
		))
	})
	if _, ok := out["compose.yaml"]; ok {
		t.Fatalf("the base file changed:\n%s", out["compose.yaml"])
	}
	prod := out["compose.prod.yaml"]
	for _, s := range []string{"volumes: !override []", "restart: always", "init: !reset null", "max-size: \"10m\"", "memory: 128M"} {
		if !strings.Contains(prod, s) {
			t.Errorf("override lacks %q:\n%s", s, prod)
		}
	}
	f := facts(t, after, "api")
	if len(f.Volumes) != 0 || f.Restart != "always" || f.Init || f.Logging == nil || f.Logging.MaxSize != "10m" || f.Resources == nil || f.Resources.Memory != "128M" {
		t.Errorf("prod facts %+v", f)
	}
	base := open(t, map[string]string{"compose.yaml": prodBase, "api/Dockerfile": files["api/Dockerfile"]})
	if f := facts(t, base, "api"); len(f.Volumes) != 1 || f.Restart != "" || !f.Init {
		t.Errorf("base facts %+v", f)
	}

	// Removing the override's own value in scope brings the base back.
	files["compose.prod.yaml"] = prod
	out, after = editReopen(t, files, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "scope", "environment", "restart", nil))
	})
	if strings.Contains(out["compose.prod.yaml"], "restart") {
		t.Errorf("restart kept:\n%s", out["compose.prod.yaml"])
	}
	if f := facts(t, after, "api"); f.Restart != "" {
		t.Errorf("restart %q", f.Restart)
	}

	p := Open(t.Context(), writeTree(t, files), []string{"compose.yaml"}, nil, Options{Environment: "prod"})
	e, err := p.Edit()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]any{
		{"scope", "environment", "environment", []any{}},
		{"scope", "environment", "name", "web"},
		{"scope", "everywhere", "restart", "always"},
	} {
		if err := e.PatchService("api", patchOf(t, bad...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if err := mustEdit(t, open(t, map[string]string{"compose.yaml": prodBase})).PatchService("api", patchOf(t, "scope", "environment", "restart", "always")); err == nil {
		t.Error("scope accepted without an active environment")
	}
}

func mustEdit(t *testing.T, p *Project) *Editor {
	t.Helper()
	e, err := p.Edit()
	if err != nil {
		t.Fatal(err)
	}
	return e
}
