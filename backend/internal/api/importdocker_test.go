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

// outsideRepo is a repository whose compose project (docker/) references
// every kind of path outside its folder.
const outsideCompose = `# repo/docker/compose.yaml: everything lives next to this folder
include:
  - ../shared/cache.yaml
services:
  api:
    build:
      context: ${API_DIR}
    env_file: ../config/app.env
    ports: ["3000:3000"]
  web:
    build:
      context: ../services/web
      dockerfile: ../../dockerfiles/web.Dockerfile
    volumes:
      - ../config/nginx.conf:/etc/nginx/conf.d/default.conf:ro
      - /var/run/docker.sock:/var/run/docker.sock
    configs: [settings]
    secrets: [token]
  worker:
    extends:
      file: ../shared/common.yaml
      service: base
    env_file:
      - path: ../../elsewhere.env
        required: false
configs:
  settings:
    file: ../config/settings.json
secrets:
  token:
    file: ../secrets/token.txt
`

func outsideRepo(t *testing.T) (repo string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "shop")
	files := map[string]string{
		"docker/compose.yaml":            outsideCompose,
		"docker/.env":                    "API_DIR=../services/api\n",
		"services/api/Dockerfile":        "FROM node:22-alpine\nWORKDIR /app\nCOPY . .\nCMD [\"node\", \"app.js\"]\n",
		"services/api/app.js":            "console.log('api')\n",
		"services/api/.dockerignore":     "*.log\nsecret/\n",
		"services/api/debug.log":         "noise\n",
		"services/api/secret/key.pem":    "KEY\n",
		"services/api/node_modules/x.js": "dep\n",
		"services/api/package.json":      "{}\n",
		"services/api/dist/bundle.js":    "built\n",
		"services/api/bin/run.sh":        "#!/bin/sh\nexec node app.js\n",
		"services/web/index.html":        "<h1>web</h1>\n",
		"dockerfiles/web.Dockerfile":     "FROM nginx:1.27-alpine\nCOPY index.html /usr/share/nginx/html/\n",
		"config/app.env":                 "GREETING=hello\n",
		"config/nginx.conf":              "server { listen 80; }\n",
		"config/settings.json":           "{}\n",
		"secrets/token.txt":              "s3cret\n",
		"shared/cache.yaml":              "services:\n  cache:\n    image: redis:7-alpine\n",
		"shared/common.yaml":             "services:\n  base:\n    image: alpine:3.20\n    command: sleep infinity\n",
		"README.md":                      "# shop\n",
	}
	for rel, content := range files {
		writeFile(t, filepath.Join(repo, filepath.FromSlash(rel)), content)
	}
	if err := os.Chmod(filepath.Join(repo, "services", "api", "bin", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A git work tree: scanning repo/docker may bring its siblings.
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(filepath.Dir(repo), "elsewhere.env"), "OUTSIDE=1\n")
	return repo
}

func TestImportDockerOutsideReferences(t *testing.T) {
	e := newEnv(t)
	repo := outsideRepo(t)
	compose := filepath.Join(repo, "docker")

	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": compose}, &scan)
	if len(scan.Projects) != 1 || scan.Projects[0].Path != "." {
		t.Fatalf("scan %+v", scan.Projects)
	}
	p := scan.Projects[0]
	if p.Name != "shop" || p.Services != 4 {
		t.Errorf("project %s with %d services, want shop with 4", p.Name, p.Services)
	}
	outside := map[string]model.ImportOutside{}
	for _, o := range p.Outside {
		outside[o.Path] = o
	}
	for path, kind := range map[string]string{
		"../services/api":               "build context",
		"../services/web":               "build context",
		"../dockerfiles/web.Dockerfile": "Dockerfile",
		"../config/app.env":             "env_file",
		"../config/nginx.conf":          "bind mount",
		"../config/settings.json":       "config",
		"../secrets/token.txt":          "secret",
		"../shared/cache.yaml":          "compose file",
		"../shared/common.yaml":         "compose file",
	} {
		o, ok := outside[path]
		if !ok || !slices.Contains(o.Kinds, kind) || o.Bytes == 0 || o.Files == 0 {
			t.Errorf("outside %s = %+v (ok %v), want kind %s with a size", path, o, ok, kind)
		}
	}
	if len(outside) != 9 {
		t.Errorf("outside entries %v", slices.Collect(func(yield func(string) bool) {
			for k := range outside {
				yield(k)
			}
		}))
	}
	// The api context: .dockerignore'd files, dependencies and regenerated
	// Node output stay out; the rest counts.
	if api := outside["../services/api"]; api.Files != 5 {
		t.Errorf("api context brings %d files, want Dockerfile, .dockerignore, app.js, package.json, bin/run.sh", api.Files)
	}
	var warned bool
	for _, d := range p.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("scan error %+v", d)
		}
		if d.Code == "docker_ref_not_imported" && strings.Contains(d.Message, "../../elsewhere.env") {
			warned = true
		}
		if strings.Contains(d.Message, "docker.sock") {
			t.Errorf("host bind mount reported: %s", d.Message)
		}
	}
	if !warned {
		t.Errorf("no warning for the env_file outside the repository: %+v", p.Diagnostics)
	}

	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": compose, "projects": []string{"."}}, &res)
	wf := res.Workflows[0]
	if !slices.Equal(wf.Compose.Files, []string{"docker/compose.yaml"}) {
		t.Fatalf("compose files %v", wf.Compose.Files)
	}
	if got := e.file(wf.ID, "docker/compose.yaml"); got != outsideCompose {
		t.Errorf("compose file changed:\n%s", got)
	}
	for _, want := range []string{"services/api/app.js", "services/api/.dockerignore", "services/api/bin/run.sh", "services/web/index.html",
		"dockerfiles/web.Dockerfile", "config/app.env", "config/nginx.conf", "config/settings.json", "secrets/token.txt",
		"shared/cache.yaml", "shared/common.yaml", "docker/.env"} {
		if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), filepath.FromSlash(want))); err != nil {
			t.Errorf("%s not imported: %v", want, err)
		}
	}
	for _, unwanted := range []string{"services/api/debug.log", "services/api/secret/key.pem", "services/api/node_modules/x.js", "services/api/dist/bundle.js", "README.md"} {
		if _, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), filepath.FromSlash(unwanted))); err == nil {
			t.Errorf("%s imported", unwanted)
		}
	}
	if fi, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), "services", "api", "bin", "run.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("run.sh lost its executable mode: %v %v", fi.Mode(), err)
	}
	services := map[string]*model.ServiceFacts{}
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind == model.ComposeService {
			services[n.Name] = n.Service
		}
	}
	if len(services) != 4 || services["api"].Build.Context != "../services/api" || services["web"].Build.Dockerfile != "dockerfiles/web.Dockerfile" {
		t.Fatalf("services %+v", services)
	}
	if !slices.ContainsFunc(services["api"].Environment, func(e model.ServiceEnv) bool { return e.Key == "GREETING" && e.Value != nil && *e.Value == "hello" }) {
		t.Errorf("env_file not read: %+v", services["api"].Environment)
	}
	if !slices.ContainsFunc(services["web"].Volumes, func(m model.ServiceMount) bool { return m.Source == "../config/nginx.conf" }) {
		t.Errorf("bind mount %+v", services["web"].Volumes)
	}
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("workflow error %+v", d)
		}
	}

	// Export writes the same tree back, and importing it again gives the
	// same workflow.
	out := filepath.Join(t.TempDir(), "export")
	e.mustCall(http.StatusOK, "POST", "/workflows/"+wf.ID+"/export", map[string]any{"dir": out}, nil)
	exported := readDirTree(t, out)
	stored := readDirTree(t, e.st.AssetsDir(wf.ID))
	if len(exported) != len(stored) {
		t.Errorf("export wrote %d files, the workflow has %d", len(exported), len(stored))
	}
	for rel, data := range stored {
		if string(exported[rel]) != string(data) {
			t.Errorf("exported %s differs", rel)
		}
	}
	if fi, err := os.Stat(filepath.Join(out, "services", "api", "bin", "run.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("exported run.sh is not executable")
	}
	var again model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": filepath.Join(out, "docker"), "projects": []string{"."}}, &again)
	if !slices.Equal(again.Workflows[0].Compose.Files, wf.Compose.Files) {
		t.Errorf("re-import compose files %v", again.Workflows[0].Compose.Files)
	}
	if back := readDirTree(t, e.st.AssetsDir(again.Workflows[0].ID)); len(back) != len(stored) {
		t.Errorf("re-import has %d files, want %d", len(back), len(stored))
	}
}

// Scanning the repository: the compose project builds services/api and
// services/web, which are not lone Dockerfile projects of their own.
func TestImportDockerBuiltFoldersNotProjects(t *testing.T) {
	e := newEnv(t)
	repo := outsideRepo(t)
	writeFile(t, filepath.Join(repo, "services", "api", "Dockerfile"), "FROM node:22-alpine\n")
	writeFile(t, filepath.Join(repo, "services", "web", "Dockerfile"), "FROM nginx:1.27-alpine\n")
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": repo}, &scan)
	var paths []string
	for _, p := range scan.Projects {
		paths = append(paths, p.Path)
	}
	if !slices.Equal(paths, []string{"docker"}) {
		t.Fatalf("projects %v, want only docker", paths)
	}
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": repo, "projects": []string{"docker"}}, &res)
	if wf := res.Workflows[0]; wf.Name != "shop" || !slices.Equal(wf.Compose.Files, []string{"docker/compose.yaml"}) {
		t.Errorf("workflow %s files %v", wf.Name, wf.Compose.Files)
	}
}

func TestImportDockerTooLarge(t *testing.T) {
	e := newEnv(t)
	repo := outsideRepo(t)
	media := filepath.Join(repo, "services", "api", "media")
	if err := os.MkdirAll(media, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(media, "intro.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	// Sparse: the size counts, the disk stays empty.
	if err := f.Truncate(maxDockerImportBytes + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	compose := filepath.Join(repo, "docker")
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": compose}, &scan)
	var msg string
	for _, d := range scan.Projects[0].Diagnostics {
		if d.Code == "docker_import_size" {
			msg = d.Message
		}
	}
	if !strings.Contains(msg, "../services/api/media (100.0 MB)") {
		t.Fatalf("size problem %q", msg)
	}
	var fail struct{ Error string }
	e.mustCall(http.StatusBadRequest, "POST", "/import", map[string]any{"dir": compose, "projects": []string{"."}}, &fail)
	if !strings.Contains(fail.Error, "../services/api/media") {
		t.Errorf("import error %q", fail.Error)
	}
	// A .dockerignore leaving it out lets the project in.
	writeFile(t, filepath.Join(repo, "services", "api", ".dockerignore"), "*.log\nsecret/\nmedia/\n")
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": compose, "projects": []string{"."}}, nil)
}

func TestImportDockerfileGroup(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "services", "api", "Dockerfile"), "FROM node:22-alpine\nEXPOSE 3000\nCMD [\"node\", \"-e\", \"require('http').createServer((q,s)=>s.end('ok')).listen(3000)\"]\n")
	writeFile(t, filepath.Join(dir, "services", "web", "Dockerfile.prod"), "FROM node:22-alpine\nEXPOSE 3000\n")
	writeFile(t, filepath.Join(dir, "services", "web", "index.js"), "// web\n")
	writeFile(t, filepath.Join(dir, "tools", "cli", "Dockerfile"), "FROM alpine:3.20\n")
	var scan model.ImportScan
	e.mustCall(http.StatusOK, "POST", "/import/scan", map[string]string{"dir": dir}, &scan)
	var paths []string
	var group model.ImportProject
	for _, p := range scan.Projects {
		paths = append(paths, p.Path)
		if p.Path == "dockerfiles:services" {
			group = p
		}
	}
	if !slices.Equal(paths, []string{"dockerfiles:services", "services/api", "services/web", "tools/cli"}) {
		t.Fatalf("projects %v", paths)
	}
	if group.Name != "services" || group.Services != 2 || !slices.Equal(group.Members, []string{"services/api", "services/web"}) ||
		!slices.Equal(group.Dockerfiles, []string{"api/Dockerfile", "web/Dockerfile.prod"}) {
		t.Errorf("group %+v", group)
	}
	var res model.ImportResult
	e.mustCall(http.StatusCreated, "POST", "/import", map[string]any{"dir": dir, "projects": []string{"dockerfiles:services"}}, &res)
	wf := res.Workflows[0]
	want := "# Generated by Nimbo from api/Dockerfile, web/Dockerfile.prod.\nservices:\n" +
		"  api:\n    build:\n      context: ./api\n    ports:\n      - \"3000:3000\"\n" +
		"  web:\n    build:\n      context: ./web\n      dockerfile: Dockerfile.prod\n    ports:\n      - \"3001:3000\"\n"
	if got := e.file(wf.ID, "compose.yaml"); got != want {
		t.Errorf("generated compose:\n%s", got)
	}
	if e.file(wf.ID, "web/index.js") != "// web\n" || len(wf.Compose.Graph.Nodes) < 2 {
		t.Errorf("group workflow %+v", wf.Compose)
	}
	for _, d := range wf.Diagnostics {
		if d.Severity == model.SeverityError {
			t.Errorf("workflow error %+v", d)
		}
	}
}
