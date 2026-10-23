package compose

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// A compose file in a subfolder is its project directory: .env and relative
// paths resolve against it, as docker compose reads it.
func TestNestedProjectDir(t *testing.T) {
	p := open(t, map[string]string{
		"docker/compose.yaml": `include: [../shared/extra.yaml]
services:
  api:
    build: ${API}
    env_file: ../config/app.env
    volumes: ["../config/nginx.conf:/etc/nginx.conf:ro", "/var/run/docker.sock:/var/run/docker.sock"]
    secrets: [token]
configs:
  settings:
    file: ../config/settings.json
secrets:
  token:
    file: ../secrets/token.txt
`,
		"docker/.env":             "API=../services/api\n",
		"services/api/Dockerfile": "FROM alpine:3.20\nUSER nobody\n",
		"config/app.env":          "GREETING=hi\n",
		"config/nginx.conf":       "events {}\n",
		"config/settings.json":    "{}\n",
		"secrets/token.txt":       "x\n",
		"shared/extra.yaml":       "services:\n  cache:\n    image: redis:7\n",
	}, "docker/compose.yaml")
	if p.ProjectDir != "docker" || p.Model == nil {
		t.Fatalf("project dir %q, diagnostics %v", p.ProjectDir, p.Diagnostics)
	}
	for _, d := range p.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("error %+v", d)
		}
	}
	var api *model.ServiceFacts
	for _, n := range p.Graph(nil).Nodes {
		if n.Name == "api" {
			api = n.Service
		}
	}
	if api == nil || api.Build.Context != "../services/api" || api.Build.Dockerfile != "services/api/Dockerfile" ||
		!slices.Equal(api.EnvFiles, []string{"config/app.env"}) || api.Volumes[0].Source != "../config/nginx.conf" || api.Volumes[1].Source != "/var/run/docker.sock" {
		t.Fatalf("api %+v", api)
	}
	got := map[string]string{}
	host := 0
	for _, r := range p.References() {
		if r.Kind == RefBind && strings.HasSuffix(r.Path, "/run/docker.sock") && strings.HasPrefix(r.Path, "../") {
			host++ // the host path, relative to the temp dir
			continue
		}
		got[r.Path] = r.Kind
	}
	want := map[string]string{
		"docker/compose.yaml":     RefCompose,
		"shared/extra.yaml":       RefCompose,
		"services/api":            RefContext,
		"services/api/Dockerfile": RefDockerfile,
		"config/app.env":          RefEnvFile,
		"config/nginx.conf":       RefBind,
		"config/settings.json":    RefConfig,
		"secrets/token.txt":       RefSecret,
	}
	if !maps.Equal(got, want) || host != 1 {
		t.Errorf("references %v (+%d host paths), want %v", got, host, want)
	}
}

func TestProjectDir(t *testing.T) {
	for files, want := range map[string]string{"": ".", "compose.yaml": ".", "docker/compose.yaml": "docker", "a/b/c.yml": "a/b"} {
		var list []string
		if files != "" {
			list = []string{files}
		}
		if got := ProjectDir(list); got != want {
			t.Errorf("ProjectDir(%q) = %q, want %q", files, got, want)
		}
	}
}
