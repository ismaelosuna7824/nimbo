package filetemplates

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/dotenv"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"go.yaml.in/yaml/v4"
)

func byID(t *testing.T, id string) Template {
	t.Helper()
	for _, tmpl := range List() {
		if tmpl.ID == id {
			return tmpl
		}
	}
	t.Fatalf("no template %s", id)
	return Template{}
}

func TestFileTemplatesInvariants(t *testing.T) {
	list := List()
	seen := map[string]bool{}
	for _, tmpl := range list {
		if seen[tmpl.ID] {
			t.Errorf("duplicate id %s", tmpl.ID)
		}
		seen[tmpl.ID] = true
		if tmpl.Kind != kindDocker && tmpl.Kind != kindKube {
			t.Errorf("%s: kind %q", tmpl.ID, tmpl.Kind)
		}
		if tmpl.Group == "" || tmpl.Label == "" || tmpl.Description == "" || tmpl.FileName == "" || strings.TrimSpace(tmpl.Content) == "" {
			t.Errorf("%s: empty field: %+v", tmpl.ID, tmpl)
		}
		if !strings.HasSuffix(tmpl.Content, "\n") {
			t.Errorf("%s: content does not end with a newline", tmpl.ID)
		}
	}
	if !slices.IsSortedFunc(list, func(a, b Template) int {
		return strings.Compare(a.Kind+"\x00"+a.Group+"\x00"+a.Label, b.Kind+"\x00"+b.Group+"\x00"+b.Label)
	}) {
		t.Error("templates are not sorted by kind, group, label")
	}
	// List hands out a copy.
	list[0].ID = "changed"
	if List()[0].ID == "changed" {
		t.Error("List shares its slice")
	}
}

// parseYAML decodes every document of src.
func parseYAML(src string) error {
	dec := yaml.NewDecoder(strings.NewReader(src))
	for {
		var v any
		if err := dec.Decode(&v); errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
	}
}

func TestFileTemplatesParse(t *testing.T) {
	for _, tmpl := range List() {
		switch {
		case tmpl.Snippet:
			if !strings.HasPrefix(tmpl.Content, "  ") {
				t.Errorf("%s: a compose service snippet must be indented under services:", tmpl.ID)
			}
			if err := parseYAML("services:\n" + tmpl.Content); err != nil {
				t.Errorf("%s: %v", tmpl.ID, err)
			}
		case strings.HasSuffix(tmpl.FileName, ".yaml"):
			if err := parseYAML(tmpl.Content); err != nil {
				t.Errorf("%s: %v", tmpl.ID, err)
			}
		case tmpl.FileName == "Dockerfile":
			if _, problems := compose.ParseDockerfile([]byte(tmpl.Content)); len(problems) > 0 {
				t.Errorf("%s: %v", tmpl.ID, problems)
			}
		case tmpl.FileName == ".env":
			if _, err := dotenv.ParseWithLookup(strings.NewReader(tmpl.Content), nil); err != nil {
				t.Errorf("%s: %v", tmpl.ID, err)
			}
		}
	}
}

// stackFiles are the files each Dockerfile stack COPYs, enough for the
// checks to see a real project.
var stackFiles = map[string]map[string]string{
	"node":        {"package.json": `{"name": "app", "version": "1.0.0", "main": "server.js"}`, "package-lock.json": `{"lockfileVersion": 3}`, "server.js": "require('http').createServer((q, s) => s.end('ok')).listen(8080);\n"},
	"python":      {"requirements.txt": "\n", "app.py": "print('ok')\n"},
	"go":          {"go.mod": "module app\n\ngo 1.23\n", "main.go": "package main\n\nfunc main() {}\n"},
	"java-maven":  {"pom.xml": "<project/>\n", "src/main/java/App.java": "class App {}\n"},
	"java-gradle": {"build.gradle": "plugins { id 'java' }\n", "settings.gradle": "rootProject.name = 'app'\n", "src/main/java/App.java": "class App {}\n"},
	"dotnet":      {"App.csproj": "<Project Sdk=\"Microsoft.NET.Sdk.Web\"/>\n", "Program.cs": "\n"},
	"rust":        {"Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n", "Cargo.lock": "version = 4\n", "src/main.rs": "fn main() {}\n"},
	"php":         {"composer.json": "{}\n", "composer.lock": "{}\n", "index.php": "<?php echo 'ok';\n"},
	"ruby":        {"Gemfile": "source 'https://rubygems.org'\n", "Gemfile.lock": "\n", "app.rb": "puts 'ok'\n"},
	"static":      {"index.html": "<!doctype html><title>ok</title>\n"},
}

// ignoreOf is the .dockerignore stack of a Dockerfile stack.
func ignoreOf(stack string) string {
	if strings.HasPrefix(stack, "java-") {
		return "java"
	}
	return stack
}

func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// allRules turns on the opt-in rules too. dk_hardening (read_only,
// cap_drop: ALL) depends on what the app writes, so it stays advice.
var allRules = model.BestPracticeSettings{EnabledRules: []string{"dk_no_limits", "dk_cache_mount", "dk_hardening"}}

func findings(t *testing.T, files map[string]string) []string {
	t.Helper()
	p := compose.Open(context.Background(), writeTree(t, files), []string{"compose.yaml"}, nil, compose.Options{})
	var out []string
	for _, d := range compose.FilterSettings(slices.Clone(p.Diagnostics), allRules) {
		if d.Code == "dk_hardening" {
			continue
		}
		out = append(out, d.Code+"@"+d.File+":"+strconv.Itoa(d.Line)+" "+d.Message)
	}
	if len(p.Model.Services) != 1 || p.Dockerfiles["Dockerfile"] == nil {
		t.Errorf("services = %d, Dockerfile loaded = %v", len(p.Model.Services), p.Dockerfiles["Dockerfile"] != nil)
	}
	return out
}

// Every Dockerfile, with its .dockerignore and the compose service snippet,
// passes every docker best-practice rule.
func TestFileTemplatesDockerfilesFollowThePractices(t *testing.T) {
	snippet := byID(t, "compose-service").Content
	env := byID(t, "env").Content
	stacks := 0
	for _, tmpl := range List() {
		if tmpl.Group != "Dockerfile" {
			continue
		}
		stacks++
		t.Run(tmpl.Stack, func(t *testing.T) {
			extra, ok := stackFiles[tmpl.Stack]
			if !ok {
				t.Fatalf("no project files for stack %s", tmpl.Stack)
			}
			files := map[string]string{
				"compose.yaml":  "services:\n" + snippet,
				".env":          env,
				"Dockerfile":    tmpl.Content,
				".dockerignore": byID(t, "dockerignore-"+ignoreOf(tmpl.Stack)).Content,
			}
			for k, v := range extra {
				files[k] = v
			}
			for _, f := range findings(t, files) {
				t.Error(f)
			}
		})
	}
	if stacks != len(stackFiles) {
		t.Errorf("%d Dockerfile templates, %d stacks with project files", stacks, len(stackFiles))
	}
}

// The compose.yaml template is a clean project on its own.
func TestFileTemplatesComposeProject(t *testing.T) {
	files := map[string]string{
		"compose.yaml":  byID(t, "compose-project").Content,
		".env":          byID(t, "env").Content,
		"Dockerfile":    byID(t, "dockerfile-node").Content,
		".dockerignore": byID(t, "dockerignore-node").Content,
	}
	for k, v := range stackFiles["node"] {
		files[k] = v
	}
	for _, f := range findings(t, files) {
		t.Error(f)
	}
}

// Every Dockerfile stack has a .dockerignore.
func TestFileTemplatesDockerignorePerStack(t *testing.T) {
	ids := map[string]bool{}
	for _, tmpl := range List() {
		ids[tmpl.ID] = true
	}
	for _, tmpl := range List() {
		if tmpl.Group == "Dockerfile" && !ids["dockerignore-"+ignoreOf(tmpl.Stack)] {
			t.Errorf("no .dockerignore for %s", tmpl.Stack)
		}
	}
}

// Kubernetes templates pin their images and keep the kustomize/helm shape.
func TestFileTemplatesKubernetes(t *testing.T) {
	for _, tmpl := range List() {
		if tmpl.Kind != kindKube {
			continue
		}
		if strings.Contains(tmpl.Content, ":latest") || strings.Contains(tmpl.Content, "tag: latest") {
			t.Errorf("%s uses a latest tag", tmpl.ID)
		}
	}
	var chart struct {
		APIVersion string `yaml:"apiVersion"`
		Type       string `yaml:"type"`
		Version    string `yaml:"version"`
		AppVersion string `yaml:"appVersion"`
	}
	if err := yaml.Unmarshal([]byte(byID(t, "helm-chart").Content), &chart); err != nil || chart.APIVersion != "v2" || chart.Type != "application" || chart.Version != "0.1.0" || chart.AppVersion == "" {
		t.Errorf("Chart.yaml: %+v %v", chart, err)
	}
	var base map[string]any
	if err := yaml.Unmarshal([]byte(byID(t, "kustomize-base").Content), &base); err != nil {
		t.Fatal(err)
	}
	if _, ok := base["commonLabels"]; ok || base["labels"] == nil || base["resources"] == nil {
		t.Errorf("base kustomization: %v", base)
	}
}
