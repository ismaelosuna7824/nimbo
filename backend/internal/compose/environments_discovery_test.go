package compose

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// An environment is an override file and/or an env file (.env.<env> or
// <env>.env); sample, local-only and service env files never are.
func TestEnvironmentDiscoveryMatrix(t *testing.T) {
	names := []string{
		"compose.yaml", ".env",
		"compose.prod.yaml", ".env.prod", // both files
		"compose.staging.yaml", // override only
		".env.qa",              // env file only, dotenv style
		"dev.env",              // env file only, <env>.env style
		".env.uat", "uat.env",  // both styles: .env.uat wins
		".env.example", ".env.sample", ".env.template", ".env.dist", ".env.defaults", "prod.example.env", // samples
		".env.local", ".env.prod.local", // local-only
		"api.env",                                        // a service's env_file
		".env.Bad_Name", "compose.Bad.yaml", ".env.base", // invalid names
		".envrc", "notes.txt",
	}
	envs, notes := Environments(names, ".", []string{"compose.yaml"}, []string{"api.env"})
	got := map[string]model.ComposeEnvironment{}
	for _, e := range envs {
		got[e.Name] = e
	}
	want := map[string][2]string{
		"prod":    {"compose.prod.yaml", ".env.prod"},
		"staging": {"compose.staging.yaml", ""},
		"qa":      {"", ".env.qa"},
		"dev":     {"", "dev.env"},
		"uat":     {"", ".env.uat"},
	}
	if len(got) != len(want) {
		t.Fatalf("environments %+v", envs)
	}
	for name, w := range want {
		e := got[name]
		files := slices.DeleteFunc([]string{w[0], w[1]}, func(s string) bool { return s == "" })
		if e.File != w[0] || e.EnvFile != w[1] || !slices.Equal(e.Files, files) {
			t.Errorf("%s: %+v", name, e)
		}
	}
	var msgs []string
	for _, n := range notes {
		if n.Code != "dk_environment_file" || n.Severity != model.SeverityInfo {
			t.Errorf("note %+v", n)
		}
		msgs = append(msgs, n.File+": "+n.Message)
	}
	all := strings.Join(msgs, "\n")
	for _, s := range []string{"uat.env is ignored", ".env.Bad_Name", "compose.Bad.yaml", ".env.base", ".env.local, .env.prod.local"} {
		if !strings.Contains(all, s) {
			t.Errorf("notes lack %q:\n%s", s, all)
		}
	}
	for _, s := range []string{"example", "api.env", ".envrc"} {
		if strings.Contains(all, s) {
			t.Errorf("notes mention %q:\n%s", s, all)
		}
	}

	if f := RenamedEnvFile("docker/dev.env", "dev", "qa"); f != "docker/qa.env" {
		t.Errorf("renamed %s", f)
	}
	if f := RenamedEnvFile(".env.dev", "dev", "qa"); f != ".env.qa" {
		t.Errorf("renamed %s", f)
	}
}

// An environment that is only an env file runs the base files with .env
// then its env file; files a service names in env_file stay service-scoped.
func TestEnvFileOnlyEnvironment(t *testing.T) {
	c := &model.ComposeModel{Files: []string{"compose.yaml"}, Environment: "dev", Environments: []model.ComposeEnvironment{
		{Name: "dev", EnvFile: "dev.env", Files: []string{"dev.env"}},
	}}
	files, envFiles := RunFiles(c, func(string) bool { return true })
	if !slices.Equal(files, []string{"compose.yaml"}) || !slices.Equal(envFiles, []string{".env", "dev.env"}) {
		t.Errorf("run files %v env files %v", files, envFiles)
	}

	dir := writeTree(t, map[string]string{
		"compose.yaml": "services:\n  api:\n    image: app:1\n    env_file: [api.env]\n    environment:\n      LOG_LEVEL: ${LOG_LEVEL:-info}\n",
		".env":         "TAG=1\n",
		"dev.env":      "LOG_LEVEL=debug\n",
		"api.env":      "A=1\n",
		".env.example": "LOG_LEVEL=\n",
	})
	base := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{})
	if names := environmentNames(base.Environments); !slices.Equal(names, []string{"dev"}) {
		t.Fatalf("environments %v", names)
	}
	dev := Open(context.Background(), dir, []string{"compose.yaml"}, nil, Options{Environment: "dev"})
	if dev.Model == nil {
		t.Fatal(dev.Diagnostics)
	}
	if dev.Override != "" || !slices.Equal(dev.Files, []string{"compose.yaml"}) || !slices.Equal(dev.EnvFiles, []string{".env", "dev.env"}) {
		t.Errorf("override %q files %v env files %v", dev.Override, dev.Files, dev.EnvFiles)
	}
	if v := dev.Model.Services["api"].Environment["LOG_LEVEL"]; v == nil || *v != "debug" {
		t.Errorf("LOG_LEVEL %v", v)
	}
	if v := base.Model.Services["api"].Environment["LOG_LEVEL"]; v == nil || *v != "info" {
		t.Errorf("base LOG_LEVEL %v", v)
	}
}

func environmentNames(envs []model.ComposeEnvironment) []string {
	out := []string{}
	for _, e := range envs {
		out = append(out, e.Name)
	}
	return out
}
