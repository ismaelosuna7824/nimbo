package compose

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// envProject is a base compose file with an environment override that
// exercises compose's merge rules.
var envProject = map[string]string{
	"compose.yaml": `services:
  api:
    image: app:${TAG:-1}
    ports: ["3100:3000"]
    expose: ["9000"]
    environment:
      A: base
      B: ${B_VAR}
    command: ["node", "a.js"]
    deploy:
      resources:
        limits: {cpus: "1", memory: 256M}
  db:
    image: postgres:16-alpine
    ports: ["5432:5432"]
`,
	"compose.prod.yaml": `services:
  api:
    expose: ["9100"]
    environment:
      A: prod
      C: c
    command: ["node", "b.js"]
    deploy:
      replicas: 2
      resources:
        limits: {memory: 512M}
  db:
    ports: !override ["5433:5432"]
`,
	"docker-compose.prod.yml":    "services: {}\n", // same environment, other spelling: compose.prod.yaml wins
	"compose.override.yaml":      "services: {}\n", // part of the base (when listed), never an environment
	"docker-compose.staging.yml": "services:\n  api:\n    image: app:rc\n",
	".env":                       "TAG=2\nB_VAR=fromenv\nTOKEN=base-secret\n",
	".env.prod":                  "B_VAR=fromprod\nX=${TAG}-x\nTOKEN=prod-secret\n",
	".env.example":               "TAG=\n",
}

func TestEnvironmentDiscovery(t *testing.T) {
	p := open(t, envProject)
	got := map[string]model.ComposeEnvironment{}
	for _, e := range p.Environments {
		got[e.Name] = e
	}
	if len(got) != 2 {
		t.Fatalf("environments %+v", p.Environments)
	}
	if e := got["prod"]; e.File != "compose.prod.yaml" || e.EnvFile != ".env.prod" || !slices.Equal(e.Files, []string{"compose.prod.yaml", ".env.prod"}) {
		t.Errorf("prod %+v", e)
	}
	if e := got["staging"]; e.File != "docker-compose.staging.yml" || e.EnvFile != "" {
		t.Errorf("staging %+v", e)
	}
	// An override listed among the base files is not an environment.
	listed, _ := Environments([]string{"compose.yaml", "compose.prod.yaml"}, ".", []string{"compose.yaml", "compose.prod.yaml"}, nil)
	if len(listed) != 0 {
		t.Errorf("listed override became an environment: %+v", listed)
	}
	// Files in a folder: the environment sits next to the first file.
	nested, _ := Environments([]string{"docker-compose.yml", "docker-compose.dev.yml", ".env.dev"}, "docker", []string{"docker/docker-compose.yml"}, nil)
	if len(nested) != 1 || nested[0].File != "docker/docker-compose.dev.yml" || nested[0].EnvFile != "docker/.env.dev" {
		t.Errorf("nested %+v", nested)
	}
	if f := OverrideFile([]string{"docker/docker-compose.yml"}, "qa"); f != "docker/docker-compose.qa.yml" {
		t.Errorf("override file %s", f)
	}
	if EnvironmentName("compose.override.yaml") != "" || EnvironmentName("compose.yaml") != "" || EnvironmentName("compose.prod.yml") != "prod" {
		t.Error("EnvironmentName")
	}
}

// The merged model of an environment follows docker compose's rules
// (checked against `docker compose config` below when docker is around).
func TestEnvironmentMergeSemantics(t *testing.T) {
	dir := writeTree(t, envProject)
	base := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{})
	prod := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: "prod"})
	if base.Model == nil || prod.Model == nil {
		t.Fatalf("load: %v / %v", base.Diagnostics, prod.Diagnostics)
	}
	if !slices.Equal(prod.Files, []string{"compose.yaml", "compose.prod.yaml"}) || prod.Override != "compose.prod.yaml" || !slices.Equal(prod.EnvFiles, []string{".env", ".env.prod"}) {
		t.Fatalf("prod files %v override %s env files %v", prod.Files, prod.Override, prod.EnvFiles)
	}
	api := prod.Model.Services["api"]
	// Interpolation: .env then .env.prod, later wins; .env.prod sees .env.
	if api.Image != "app:2" || *api.Environment["B"] != "fromprod" || prod.Env["X"] != "2-x" || prod.Env["TOKEN"] != "prod-secret" {
		t.Errorf("image %s B %s X %s", api.Image, *api.Environment["B"], prod.Env["X"])
	}
	if *base.Model.Services["api"].Environment["B"] != "fromenv" {
		t.Error("base reads .env only")
	}
	// Mappings merge key by key.
	if *api.Environment["A"] != "prod" || *api.Environment["C"] != "c" {
		t.Errorf("environment %v", api.Environment)
	}
	// Sequences of commands are replaced.
	if strings.Join(api.Command, " ") != "node b.js" {
		t.Errorf("command %v", api.Command)
	}
	// expose (and ports) are appended.
	if !slices.Equal(api.Expose, []string{"9000", "9100"}) {
		t.Errorf("expose %v", api.Expose)
	}
	// deploy.resources.limits merges: cpus from the base, memory from prod.
	if l := api.Deploy.Resources.Limits; l.NanoCPUs != 1 || l.MemoryBytes != 512<<20 || *api.Deploy.Replicas != 2 {
		t.Errorf("deploy %+v", api.Deploy)
	}
	// !override replaces a list.
	if db := prod.Model.Services["db"]; len(db.Ports) != 1 || db.Ports[0].Published != "5433" {
		t.Errorf("db ports %+v", db.Ports)
	}
	// The inspector knows what the override sets.
	for _, n := range prod.Graph(nil).Nodes {
		if n.Name == "api" && n.Kind == model.ComposeService {
			if !slices.Equal(n.Service.Overrides, []string{"expose", "environment", "command", "deploy.replicas", "deploy.resources"}) {
				t.Errorf("overrides %v", n.Service.Overrides)
			}
		}
	}
	if missing := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: "qa"}); missing.Model != nil || !slices.Contains(codes(missing), "error:dk_environment") {
		t.Errorf("unknown environment: %v", codes(missing))
	}

	if testing.Short() {
		return
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	cmd := exec.Command("docker", "compose", "-p", "nimbo-envtest", "-f", "compose.yaml", "-f", "compose.prod.yaml", "--env-file", ".env", "--env-file", ".env.prod", "config", "--format", "json")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("docker compose config: %v", err)
	}
	var cfg struct {
		Services map[string]struct {
			Image       string            `json:"image"`
			Command     []string          `json:"command"`
			Expose      []string          `json:"expose"`
			Environment map[string]string `json:"environment"`
			Ports       []struct {
				Published string `json:"published"`
			} `json:"ports"`
			Deploy struct {
				Replicas  int `json:"replicas"`
				Resources struct {
					Limits struct {
						Memory string `json:"memory"`
					} `json:"limits"`
				} `json:"resources"`
			} `json:"deploy"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	d := cfg.Services["api"]
	env := map[string]string{}
	for k, v := range api.Environment {
		env[k] = *v
	}
	if d.Image != api.Image || !slices.Equal(d.Command, []string(api.Command)) || !slices.Equal(d.Expose, api.Expose) ||
		!mapsEqual(d.Environment, env) || d.Deploy.Replicas != *api.Deploy.Replicas || d.Deploy.Resources.Limits.Memory != "536870912" {
		t.Errorf("docker compose config differs:\n%s", out)
	}
	if p := cfg.Services["db"].Ports; len(p) != 1 || p[0].Published != "5433" {
		t.Errorf("docker db ports %+v", p)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func TestRunFiles(t *testing.T) {
	c := &model.ComposeModel{Files: []string{"compose.yaml", "monitoring/compose.yaml"}, Environments: []model.ComposeEnvironment{
		{Name: "prod", File: "compose.prod.yaml", EnvFile: ".env.prod"}, {Name: "dev", File: "compose.dev.yaml"},
	}}
	has := func(string) bool { return true }
	if f, e := RunFiles(c, has); !slices.Equal(f, c.Files) || e != nil {
		t.Errorf("base %v %v", f, e)
	}
	c.Environment = "prod"
	if f, e := RunFiles(c, has); !slices.Equal(f, []string{"compose.yaml", "monitoring/compose.yaml", "compose.prod.yaml"}) || !slices.Equal(e, []string{".env", ".env.prod"}) {
		t.Errorf("prod %v %v", f, e)
	}
	if _, e := RunFiles(c, func(r string) bool { return r != ".env" }); !slices.Equal(e, []string{".env.prod"}) {
		t.Errorf("prod without .env %v", e)
	}
	// Without its own env file compose reads .env by itself: no --env-file.
	c.Environment = "dev"
	if f, e := RunFiles(c, has); f[len(f)-1] != "compose.dev.yaml" || e != nil {
		t.Errorf("dev %v %v", f, e)
	}
}

func TestSetDotenv(t *testing.T) {
	src := "# comment\nA=1\nexport B=2\n\nC=3\n"
	v := "two words"
	if got := string(SetDotenv([]byte(src), "B", &v)); got != "# comment\nA=1\nB=\"two words\"\n\nC=3\n" {
		t.Errorf("set:\n%s", got)
	}
	if got := string(SetDotenv([]byte(src), "A", nil)); got != "# comment\nexport B=2\n\nC=3\n" {
		t.Errorf("unset:\n%s", got)
	}
	n := "x${A}"
	if got := string(SetDotenv(nil, "NEW", &n)); got != "NEW=x${A}\n" {
		t.Errorf("new:\n%s", got)
	}
	q := `say "hi" #1`
	env, err := ParseEnvFile(SetDotenv(nil, "Q", &q), nil)
	if err != nil || env["Q"] != q {
		t.Errorf("round trip %q %v", env["Q"], err)
	}
}

func TestEnvironmentVariables(t *testing.T) {
	vars := Variables(map[string]string{"A": "1", "DB_PASSWORD": "x"}, map[string]string{"A": "2", "B": "3"}, ".env.prod")
	if len(vars) != 3 {
		t.Fatalf("%+v", vars)
	}
	a, b, pw := vars[0], vars[1], vars[2]
	if a.Key != "A" || *a.Value != "2" || *a.Base != "1" || !a.Overridden {
		t.Errorf("A %+v", a)
	}
	if b.Key != "B" || *b.Value != "3" || b.Base != nil || !b.Overridden {
		t.Errorf("B %+v", b)
	}
	if pw.Key != "DB_PASSWORD" || !pw.Secret || pw.Value != nil || pw.Base != nil || pw.Overridden {
		t.Errorf("secret %+v", pw)
	}
}

// Settings go to the override file only, minimal, and the result still
// loads (with fixed host ports turned into ranges for replicas).
func TestSetSettingsWritesMinimalOverride(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"compose.yaml": envProject["compose.yaml"],
		".env":         "TAG=2\nB_VAR=x\n",
	})
	file := OverrideFile([]string{"compose.yaml"}, "prod")
	if err := os.WriteFile(filepath.Join(dir, file), NewOverride("prod", []string{"compose.yaml"}, file), 0o644); err != nil {
		t.Fatal(err)
	}
	openProd := func() *Project {
		return Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: "prod"})
	}
	p := openProd()
	if p.Model == nil {
		t.Fatalf("empty override does not load: %v", p.Diagnostics)
	}
	two, mem, img := "2", "512M", "postgres:17-alpine"
	files, err := SetSettings(p, "api", map[string]*string{SettingReplicas: &two, SettingMemory: &mem})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), files[file], 0o644); err != nil {
		t.Fatal(err)
	}
	files, err = SetSettings(openProd(), "db", map[string]*string{SettingImage: &img})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file), files[file], 0o644); err != nil {
		t.Fatal(err)
	}
	want := "services:\n  api:\n    ports: !override [\"3100-3101:3000\"]\n    deploy:\n      replicas: 2\n      resources:\n        limits:\n          memory: 512M\n  db:\n    image: postgres:17-alpine\n"
	got := string(files[file])
	if !strings.HasSuffix(got, want) {
		t.Errorf("override:\n%s", got)
	}
	p = openProd()
	if p.Model == nil {
		t.Fatalf("override does not load: %v", p.Diagnostics)
	}
	api := p.Model.Services["api"]
	if *api.Deploy.Replicas != 2 || api.Deploy.Resources.Limits.MemoryBytes != 512<<20 || api.Deploy.Resources.Limits.NanoCPUs != 1 || len(api.Ports) != 1 || api.Ports[0].Published != "3100-3101" {
		t.Errorf("api %+v ports %+v", api.Deploy, api.Ports)
	}
	if slices.Contains(codes(p), "error:dk_port_clash") {
		t.Errorf("port clash: %v", codes(p))
	}
	base := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{})
	st, err := Settings(base, p, "api")
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]model.EnvSetting{}
	for _, s := range st {
		byKey[s.Key] = s
	}
	if r := byKey[SettingReplicas]; r.Value != "2" || r.Base != "1" || !r.Overridden {
		t.Errorf("replicas %+v", r)
	}
	if c := byKey[SettingCPUs]; c.Value != "1" || c.Overridden {
		t.Errorf("cpus %+v", c)
	}
	// Removing a setting falls back to the base and prunes empty parents.
	files, err = SetSettings(p, "api", map[string]*string{SettingMemory: nil})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(files[file]), "limits") {
		t.Errorf("after reset:\n%s", files[file])
	}
	if _, err := SetSettings(p, "api", map[string]*string{SettingReplicas: new("x")}); err == nil {
		t.Error("bad replicas accepted")
	}
}

func TestCompareEnvironments(t *testing.T) {
	dir := writeTree(t, envProject)
	cmp, changes := Compare(OpenEnvironments(context.Background(), dir, []string{"compose.yaml"}, nil, Options{}), []string{"prod", "staging"})
	if len(cmp.Columns) != 3 || cmp.Columns[0].Label != "Base" || len(cmp.Errors) != 0 {
		t.Fatalf("columns %+v errors %v", cmp.Columns, cmp.Errors)
	}
	row := func(svc, key string) model.EnvCompareRow {
		for _, g := range cmp.Groups {
			if g.Label == svc {
				for _, r := range g.Rows {
					if r.Key == key {
						return r
					}
				}
			}
		}
		t.Fatalf("no row %s %s", svc, key)
		return model.EnvCompareRow{}
	}
	if r := row("api", "replicas"); !slices.Equal(r.Values, []string{"1", "2", "1"}) || !r.Differs {
		t.Errorf("replicas %+v", r)
	}
	if r := row("api", "image"); !slices.Equal(r.Values, []string{"app:2", "app:2", "app:rc"}) {
		t.Errorf("image %+v", r)
	}
	if r := row("api", "env:B"); !slices.Equal(r.Values, []string{"fromenv", "fromprod", "fromenv"}) {
		t.Errorf("B %+v", r)
	}
	if r := row("db", "ports"); !slices.Equal(r.Values, []string{"5432:5432", "5433:5432", "5432:5432"}) {
		t.Errorf("db ports %+v", r)
	}
	fields := map[string]bool{}
	for _, c := range changes["prod"] {
		fields[c.Target+"."+c.Field] = true
	}
	for _, want := range []string{"api.Replicas", "api.Memory limit", "api.B", "api.A", "api.C", "db.Ports"} {
		if !fields[want] {
			t.Errorf("prod changes lack %s: %+v", want, changes["prod"])
		}
	}
	if fields["api.Image"] || fields["db.Image"] {
		t.Errorf("unchanged fields reported: %+v", changes["prod"])
	}
}
