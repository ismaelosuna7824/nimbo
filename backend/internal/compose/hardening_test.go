package compose

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// editReopen opens files (in environment env), applies fn, writes what
// changed and reopens the project.
func editReopen(t *testing.T, files map[string]string, env string, fn func(e *Editor) error) (map[string]string, *Project) {
	t.Helper()
	dir := writeTree(t, files)
	p := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: env})
	if p.Model == nil {
		t.Fatal(p.Diagnostics)
	}
	out := edit(t, p, fn)
	for rel, content := range out {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: env})
	if after.Model == nil {
		t.Fatalf("edited project does not load: %v\n%v", after.Diagnostics, out)
	}
	return out, after
}

func facts(t *testing.T, p *Project, svc string) *model.ServiceFacts {
	t.Helper()
	for _, n := range p.Graph(nil).Nodes {
		if n.Kind == model.ComposeService && n.Name == svc {
			return n.Service
		}
	}
	t.Fatalf("no service %s", svc)
	return nil
}

func hasCode(p *Project, code, node string) bool {
	return slices.ContainsFunc(p.Diagnostics, func(d model.Diagnostic) bool { return d.Code == code && d.NodeID == node })
}

const rendererCompose = `services:
  # The report renderer
  renderer:
    build: ./renderer
    privileged: true # incident leftover
    ports:
      - "3000:3000"
    restart: unless-stopped
`

var rendererFiles = map[string]string{
	"compose.yaml":        rendererCompose,
	"renderer/Dockerfile": "FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nCMD [\"node\", \"server.js\"]\n",
	"renderer/server.js":  "",
}

func TestPatchHardening(t *testing.T) {
	before := open(t, rendererFiles)
	f := facts(t, before, "renderer")
	if !f.Privileged || f.User != "" || f.NonRootUser != "node" || f.ReadOnly || f.Init {
		t.Fatalf("facts %+v", f)
	}
	for _, code := range []string{"dk_privileged", "dk_root_user", "dk_hardening", "dk_no_init"} {
		if !hasCode(before, code, ServiceID("renderer")) {
			t.Errorf("starter lacks %s", code)
		}
	}

	out, after := editReopen(t, rendererFiles, "", func(e *Editor) error {
		return e.PatchService("renderer", patchOf(t,
			"privileged", false,
			"user", "node",
			"readOnly", true,
			"tmpfs", []string{"/tmp"},
			"capDrop", []string{"ALL"},
			"securityOpt", []string{"no-new-privileges:true"},
			"init", true,
		))
	})
	want := `services:
  # The report renderer
  renderer:
    build: ./renderer
    ports:
      - "3000:3000"
    restart: unless-stopped
    cap_drop:
      - ALL
    init: true
    read_only: true
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /tmp
    user: node
`
	if out["compose.yaml"] != want {
		t.Errorf("got:\n%s\nwant:\n%s", out["compose.yaml"], want)
	}
	f = facts(t, after, "renderer")
	if f.Privileged || !f.ReadOnly || !f.Init || f.User != "node" || f.UserFrom != "compose" ||
		!slices.Equal(f.CapDrop, []string{"ALL"}) || !slices.Equal(f.SecurityOpt, []string{"no-new-privileges:true"}) || !slices.Equal(f.Tmpfs, []string{"/tmp"}) {
		t.Errorf("facts %+v", f)
	}
	for _, code := range []string{"dk_privileged", "dk_root_user", "dk_hardening", "dk_no_init"} {
		if hasCode(after, code, ServiceID("renderer")) {
			t.Errorf("%s still reported", code)
		}
	}
}

func TestPatchHardeningListsAndRemoval(t *testing.T) {
	files := map[string]string{"compose.yaml": `services:
  web:
    image: nginx:1.27-alpine
    cap_drop: [ALL]
    cap_add:
      - CHOWN # needed by the entrypoint
      - SETUID
    read_only: true
    init: true
    tmpfs: /run
`}
	out, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("web", patchOf(t,
			"capAdd", []string{"CHOWN", "SETGID"},
			"tmpfs", []string{"/run", "/tmp"},
			"readOnly", false,
			"init", nil,
			"capDrop", nil,
		))
	})
	want := `services:
  web:
    image: nginx:1.27-alpine
    cap_add:
      - CHOWN # needed by the entrypoint
      - SETGID
    tmpfs:
      - /run
      - /tmp
`
	if out["compose.yaml"] != want {
		t.Errorf("got:\n%s\nwant:\n%s", out["compose.yaml"], want)
	}
	f := facts(t, after, "web")
	if f.ReadOnly || f.Init || len(f.CapDrop) != 0 || !slices.Equal(f.CapAdd, []string{"CHOWN", "SETGID"}) || !slices.Equal(f.Tmpfs, []string{"/run", "/tmp"}) {
		t.Errorf("facts %+v", f)
	}

	p := open(t, files)
	e, err := p.Edit()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"readOnly": "yes"},
		{"capDrop": "ALL"},
		{"tmpfs": []string{"tmp"}},
		{"securityOpt": []string{""}},
		{"secrets": []string{"nope"}},
	} {
		var kv []any
		for k, v := range bad {
			kv = append(kv, k, v)
		}
		if err := e.PatchService("web", patchOf(t, kv...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// Edits follow the active environment's override like the other fields:
// what it sets is edited (or removed) there, the base stays.
func TestPatchHardeningRespectsOverride(t *testing.T) {
	files := map[string]string{
		"compose.yaml": `services:
  api:
    image: app:1
    cap_add: [NET_ADMIN]
`,
		"compose.prod.yaml": `services:
  api:
    read_only: true
    cap_add: [CHOWN]
`,
	}
	out, after := editReopen(t, files, "prod", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "readOnly", false, "capAdd", []string{"CHOWN", "SETUID"}))
	})
	if _, ok := out["compose.yaml"]; ok {
		t.Fatalf("the base file changed:\n%s", out["compose.yaml"])
	}
	if got := out["compose.prod.yaml"]; strings.Contains(got, "read_only") || !strings.Contains(got, "!override") {
		t.Errorf("override:\n%s", got)
	}
	f := facts(t, after, "api")
	if f.ReadOnly || !slices.Equal(f.CapAdd, []string{"CHOWN", "SETUID"}) || !slices.Contains(f.Overrides, "cap_add") {
		t.Errorf("facts %+v", f)
	}
}

const secretsCompose = `services:
  api:
    build: ./api
    environment:
      PGHOST: db
      PGPASSWORD: "s3cr3t-orders-pw"
  db:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: orders
      POSTGRES_PASSWORD: "s3cr3t-orders-pw"
    secrets:
      - source: tls_key
        target: server.key

secrets:
  tls_key:
    file: ./tls.key
`

func TestPatchSecretFromEnv(t *testing.T) {
	files := map[string]string{
		"compose.yaml":   secretsCompose,
		"api/Dockerfile": "FROM node:22-alpine\nUSER node\nCMD [\"node\", \"server.js\"]\n",
		"tls.key":        "key",
	}
	p := open(t, files)
	db := facts(t, p, "db")
	if db.NonRootUser != "postgres" || !slices.Contains(db.FileEnv, "POSTGRES_PASSWORD") || !slices.Equal(db.Secrets, []string{"tls_key"}) {
		t.Errorf("db facts %+v", db)
	}
	if api := facts(t, p, "api"); api.User != "node" || api.UserFrom != "dockerfile" || len(api.FileEnv) != 0 {
		t.Errorf("api facts %+v", api)
	}
	if !hasCode(p, "dk_secret_env", ServiceID("db")) || !hasCode(p, "dk_secret_env", ServiceID("api")) {
		t.Fatalf("starter: %v", codes(p))
	}

	out, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("db", patchOf(t, "secretFromEnv", SecretFromEnv{Key: "POSTGRES_PASSWORD", Secret: "db_password", FileEnv: true}))
	})
	if got := out["secrets/db_password.txt"]; got != "s3cr3t-orders-pw" {
		t.Errorf("secret file %q", got)
	}
	compose := out["compose.yaml"]
	for _, want := range []string{
		"      POSTGRES_PASSWORD_FILE: /run/secrets/db_password\n",
		"      - source: tls_key\n        target: server.key\n      - db_password\n",
		"  db_password:\n    file: ./secrets/db_password.txt\n",
	} {
		if !strings.Contains(compose, want) {
			t.Errorf("missing %q in:\n%s", want, compose)
		}
	}
	if strings.Contains(compose, "POSTGRES_PASSWORD:") || !strings.Contains(compose, `PGPASSWORD: "s3cr3t-orders-pw"`) {
		t.Errorf("environment:\n%s", compose)
	}
	if db := facts(t, after, "db"); !slices.Equal(db.Secrets, []string{"tls_key", "db_password"}) {
		t.Errorf("db secrets %v", db.Secrets)
	}

	// The API reuses the declared secret: no second file.
	files["compose.yaml"], files["secrets/db_password.txt"] = compose, out["secrets/db_password.txt"]
	out2, after2 := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "secretFromEnv", SecretFromEnv{Key: "PGPASSWORD", Secret: "db_password", FileEnv: true}))
	})
	if _, wrote := out2["secrets/db_password.txt"]; wrote {
		t.Error("the secret file was written again")
	}
	if !strings.Contains(out2["compose.yaml"], "      PGPASSWORD_FILE: /run/secrets/db_password\n    secrets:\n      - db_password\n") {
		t.Errorf("api:\n%s", out2["compose.yaml"])
	}
	if hasCode(after2, "dk_secret_env", ServiceID("db")) || hasCode(after2, "dk_secret_env", ServiceID("api")) {
		t.Errorf("literal secrets still reported: %v", codes(after2))
	}

	// Without fileEnv the variable just goes; errors for unknown keys,
	// combined patches and taken file names.
	_, after3 := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("api", patchOf(t, "secretFromEnv", SecretFromEnv{Key: "PGPASSWORD"}))
	})
	api := facts(t, after3, "api")
	if !slices.Equal(api.Secrets, []string{"pgpassword"}) || slices.ContainsFunc(api.Environment, func(e model.ServiceEnv) bool { return strings.HasPrefix(e.Key, "PGPASSWORD") }) {
		t.Errorf("api %+v", api)
	}
	e, err := open(t, files).Edit()
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"secretFromEnv": SecretFromEnv{Key: "NOPE"}},
		{"secretFromEnv": SecretFromEnv{Key: "PGPASSWORD"}, "environment": []EnvSpec{}},
		{"secretFromEnv": SecretFromEnv{Key: "PGPASSWORD", Secret: "bad name"}},
	} {
		var kv []any
		for k, v := range bad {
			kv = append(kv, k, v)
		}
		if err := e.PatchService("api", patchOf(t, kv...)); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	files["secrets/pghost.txt"] = "taken"
	e, _ = open(t, files).Edit()
	if err := e.PatchService("api", patchOf(t, "secretFromEnv", SecretFromEnv{Key: "PGHOST"})); err == nil {
		t.Error("overwrote secrets/pghost.txt")
	}
}

func TestPatchSecretsList(t *testing.T) {
	files := map[string]string{"compose.yaml": secretsCompose + "  other:\n    environment: OTHER\n", "tls.key": "key"}
	out, after := editReopen(t, files, "", func(e *Editor) error {
		return e.PatchService("db", patchOf(t, "secrets", []string{"tls_key", "other"}))
	})
	if !strings.Contains(out["compose.yaml"], "      - source: tls_key\n        target: server.key\n      - other\n") {
		t.Errorf("long syntax not kept:\n%s", out["compose.yaml"])
	}
	if db := facts(t, after, "db"); !slices.Equal(db.Secrets, []string{"tls_key", "other"}) {
		t.Errorf("secrets %v", db.Secrets)
	}
	out, after = editReopen(t, files, "", func(e *Editor) error { return e.PatchService("db", patchOf(t, "secrets", nil)) })
	if strings.Contains(out["compose.yaml"], "target: server.key") || len(facts(t, after, "db").Secrets) != 0 {
		t.Errorf("not removed:\n%s", out["compose.yaml"])
	}
}
