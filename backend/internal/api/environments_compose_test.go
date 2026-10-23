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

// Docker environments end to end through the API: create, settings and
// variables written to compose.<env>.yaml / .env.<env>, duplicate, rename,
// switch (graph, edits and run arguments follow), delete.
func TestComposeEnvironments(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a shell script as docker")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	docker := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$*\" in\n*config*) echo '{\"services\":{}}' ;;\nesac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEnvDocker(t, docker)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	base := "/workflows/" + wf.ID
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(e.st.AssetsDir(wf.ID), rel))
		return err == nil
	}

	var view model.EnvironmentsView
	e.mustCall(http.StatusOK, "GET", base+"/environments", nil, &view)
	if view.Kind != model.WorkflowDocker || len(view.Environments) != 0 {
		t.Fatalf("view %+v", view)
	}

	var got model.Workflow
	e.mustCall(http.StatusOK, "POST", base+"/environments", map[string]string{"name": "prod"}, &got)
	if len(got.Environments) != 1 || got.Environments[0].Name != "prod" || len(got.Compose.Environments) != 1 || got.Compose.Environments[0].File != "compose.prod.yaml" {
		t.Fatalf("after create: %+v / %+v", got.Environments, got.Compose.Environments)
	}
	if f := e.file(wf.ID, "compose.prod.yaml"); !strings.HasSuffix(f, "services: {}\n") || !strings.Contains(f, "--env-file .env.prod") {
		t.Errorf("new override:\n%s", f)
	}
	e.mustCall(http.StatusConflict, "POST", base+"/environments", map[string]string{"name": "prod"}, nil)

	// Settings go to the override only (with the fixed host port as a range).
	e.mustCall(http.StatusOK, "PATCH", base+"/environments/prod/settings", map[string]any{"target": "api", "values": map[string]string{"replicas": "2", "memory": "512M"}}, &got)
	if f := e.file(wf.ID, "compose.prod.yaml"); !strings.Contains(f, "replicas: 2") || !strings.Contains(f, `"3100-3101:3000"`) || !strings.Contains(f, "memory: 512M") {
		t.Errorf("override after settings:\n%s", f)
	}
	if strings.Contains(e.file(wf.ID, "compose.yaml"), "replicas") {
		t.Error("base changed")
	}
	var st model.EnvSettings
	e.mustCall(http.StatusOK, "GET", base+"/environments/prod/settings?target=api", nil, &st)
	if st.File != "compose.prod.yaml" || len(st.Targets) != 3 {
		t.Errorf("settings %+v", st)
	}
	for _, s := range st.Settings {
		if s.Key == "replicas" && (s.Value != "2" || s.Base != "1" || !s.Overridden) {
			t.Errorf("replicas setting %+v", s)
		}
	}

	// Variables: .env.<env> is created on the first one.
	e.mustCall(http.StatusOK, "PUT", base+"/environments/prod/variables/LOG_LEVEL", map[string]string{"value": "warn"}, &got)
	e.mustCall(http.StatusOK, "PUT", base+"/environments/prod/variables/POSTGRES_PASSWORD", map[string]any{"value": "prod pass", "secret": true}, &got)
	if f := e.file(wf.ID, ".env.prod"); f != "LOG_LEVEL=warn\nPOSTGRES_PASSWORD=\"prod pass\"\n" {
		t.Errorf(".env.prod:\n%s", f)
	}
	if !slices.Equal(got.Compose.Environments[0].Files, []string{"compose.prod.yaml", ".env.prod"}) {
		t.Errorf("files %v", got.Compose.Environments[0].Files)
	}
	e.mustCall(http.StatusOK, "GET", base+"/environments", nil, &view)
	prod := view.Environments[0]
	vars := map[string]model.EnvironmentVariable{}
	for _, v := range prod.Variables {
		vars[v.Key] = v
	}
	if v := vars["POSTGRES_PASSWORD"]; !v.Secret || v.Value != nil || !v.Overridden {
		t.Errorf("secret variable %+v", v)
	}
	if v := vars["POSTGRES_USER"]; v.Overridden || v.Value == nil || *v.Value != "app" {
		t.Errorf("inherited variable %+v", v)
	}
	if !prod.ProdLike || !slices.ContainsFunc(prod.Changes, func(c model.EnvironmentChange) bool {
		return c.Target == "api" && c.Field == "Replicas" && c.Value == "2"
	}) {
		t.Errorf("prod %+v", prod)
	}
	var cmp model.EnvCompare
	e.mustCall(http.StatusOK, "GET", base+"/environments/compare", nil, &cmp)
	if len(cmp.Columns) != 2 || len(cmp.Groups) != 3 {
		t.Errorf("compare %+v", cmp)
	}

	// Duplicate, then rename the copy.
	e.mustCall(http.StatusOK, "POST", base+"/environments", map[string]string{"name": "dev", "from": "prod"}, &got)
	if !exists("compose.dev.yaml") || e.file(wf.ID, ".env.dev") != e.file(wf.ID, ".env.prod") || !strings.Contains(e.file(wf.ID, "compose.dev.yaml"), "# Environment dev:") {
		t.Errorf("duplicate:\n%s", e.file(wf.ID, "compose.dev.yaml"))
	}
	e.mustCall(http.StatusOK, "PATCH", base+"/environments/dev", map[string]string{"name": "qa"}, &got)
	if exists("compose.dev.yaml") || exists(".env.dev") || !exists("compose.qa.yaml") || !exists(".env.qa") || !strings.Contains(e.file(wf.ID, "compose.qa.yaml"), "--env-file .env.qa") {
		t.Errorf("rename: %+v", got.Compose.Environments)
	}

	// Switching renders the environment; edits of what it overrides go to its file.
	got.ActiveEnvironment = "prod"
	e.mustCall(http.StatusOK, "PUT", base, got, &got)
	if got.Compose.Environment != "prod" {
		t.Fatalf("active %q", got.Compose.Environment)
	}
	for _, n := range got.Compose.Graph.Nodes {
		if n.Name == "api" && n.Service != nil && (n.Service.Replicas == nil || *n.Service.Replicas != 2 || !slices.Contains(n.Service.Overrides, "deploy.replicas")) {
			t.Errorf("api facts %+v", n.Service)
		}
	}
	e.mustCall(http.StatusOK, "PATCH", base+"/compose/services/api", map[string]any{"replicas": 1, "restart": "always"}, &got)
	if f := e.file(wf.ID, "compose.prod.yaml"); !strings.Contains(f, "replicas: 1") {
		t.Errorf("override after edit:\n%s", f)
	}
	if f := e.file(wf.ID, "compose.yaml"); strings.Contains(f, "replicas") || !strings.Contains(f, "restart: always") {
		t.Errorf("base after edit:\n%s", f)
	}
	bad := got
	bad.ActiveEnvironment = "nope"
	e.mustCall(http.StatusBadRequest, "PUT", base, bad, nil)

	// Runs use the base files plus the override and both env files.
	e.mustCall(http.StatusOK, "GET", base+"/compose/ports", nil, nil)
	data, _ := os.ReadFile(log)
	assets := e.st.AssetsDir(wf.ID)
	want := "-f " + filepath.Join(assets, "compose.yaml") + " -f " + filepath.Join(assets, "compose.prod.yaml") +
		" --env-file " + filepath.Join(assets, ".env") + " --env-file " + filepath.Join(assets, ".env.prod")
	if !strings.Contains(string(data), want) {
		t.Errorf("docker calls lack %q:\n%s", want, data)
	}

	// Unset variables; delete the active environment: back to the base.
	e.mustCall(http.StatusOK, "DELETE", base+"/environments/qa/variables/LOG_LEVEL", nil, &got)
	e.mustCall(http.StatusOK, "DELETE", base+"/environments/qa/variables/POSTGRES_PASSWORD", nil, &got)
	if exists(".env.qa") {
		t.Error(".env.qa kept without variables")
	}
	got = model.Workflow{} // omitempty fields would keep their old values
	e.mustCall(http.StatusOK, "DELETE", base+"/environments/prod", nil, &got)
	if got.ActiveEnvironment != "" || got.Compose.Environment != "" || exists("compose.prod.yaml") || exists(".env.prod") || len(got.Environments) != 1 {
		t.Errorf("after delete: active %q envs %+v", got.ActiveEnvironment, got.Environments)
	}
}

// Env files alone are environments in both styles (.env.<env>, <env>.env):
// listed with the base files, variables and settings edit the right files,
// rename keeps the style and runs read the env file without an override.
func TestComposeEnvFileEnvironments(t *testing.T) {
	if testing.Short() {
		t.Skip("uses a shell script as docker")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "docker.log")
	docker := filepath.Join(dir, "docker")
	script := "#!/bin/sh\necho \"$*\" >> '" + log + "'\ncase \"$*\" in\n*config*) echo '{\"services\":{}}' ;;\nesac\n"
	if err := os.WriteFile(docker, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	e := newEnvDocker(t, docker)
	wf := e.createDocker(map[string]string{"templateId": "docker-node-postgres-redis"})
	base := "/workflows/" + wf.ID
	assets := e.st.AssetsDir(wf.ID)
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(assets, rel))
		return err == nil
	}
	for name, content := range map[string]string{"dev.env": "LOG_LEVEL=debug\n", ".env.production": "LOG_LEVEL=warn\n", ".env.example": "LOG_LEVEL=\n"} {
		if err := os.WriteFile(filepath.Join(assets, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var saved model.Workflow
	e.mustCall(http.StatusOK, "GET", base, nil, &saved)
	e.mustCall(http.StatusOK, "PUT", base, saved, &saved) // derives the files written behind its back

	var view model.EnvironmentsView
	e.mustCall(http.StatusOK, "GET", base+"/environments", nil, &view)
	names := []string{}
	for _, env := range view.Environments {
		names = append(names, env.Name+"="+strings.Join(env.Files, ","))
	}
	if !slices.Equal(names, []string{"dev=dev.env", "production=.env.production"}) || !slices.Equal(view.BaseFiles, []string{"compose.yaml", ".env"}) {
		t.Fatalf("environments %v base %v", names, view.BaseFiles)
	}

	// A variable of dev goes to dev.env; rename keeps that style.
	var got model.Workflow
	e.mustCall(http.StatusOK, "PUT", base+"/environments/dev/variables/REGION", map[string]string{"value": "eu"}, &got)
	if f := e.file(wf.ID, "dev.env"); f != "LOG_LEVEL=debug\nREGION=eu\n" || exists(".env.dev") {
		t.Errorf("dev.env:\n%s", f)
	}
	e.mustCall(http.StatusOK, "PATCH", base+"/environments/dev", map[string]string{"name": "qa"}, &got)
	if exists("dev.env") || !exists("qa.env") || exists("compose.qa.yaml") {
		t.Errorf("rename: %+v", got.Compose.Environments)
	}
	// A copy is named the way Nimbo names new environments.
	e.mustCall(http.StatusOK, "POST", base+"/environments", map[string]string{"name": "uat", "from": "qa"}, &got)
	if e.file(wf.ID, ".env.uat") != e.file(wf.ID, "qa.env") || exists("compose.uat.yaml") {
		t.Errorf("duplicate: %+v", got.Compose.Environments)
	}

	// Settings of an env-only environment create its override file.
	e.mustCall(http.StatusOK, "PATCH", base+"/environments/production/settings", map[string]any{"target": "api", "values": map[string]string{"replicas": "2"}}, &got)
	if f := e.file(wf.ID, "compose.production.yaml"); !strings.Contains(f, "replicas: 2") {
		t.Errorf("override:\n%s", f)
	}

	// Runs of qa: the base files only, .env then qa.env.
	got.ActiveEnvironment = "qa"
	e.mustCall(http.StatusOK, "PUT", base, got, &got)
	if got.Compose.Environment != "qa" {
		t.Fatalf("active %q", got.Compose.Environment)
	}
	e.mustCall(http.StatusOK, "GET", base+"/compose/ports", nil, nil)
	data, _ := os.ReadFile(log)
	want := "-f " + filepath.Join(assets, "compose.yaml") + " --env-file " + filepath.Join(assets, ".env") + " --env-file " + filepath.Join(assets, "qa.env")
	if !strings.Contains(string(data), want) {
		t.Errorf("docker calls lack %q:\n%s", want, data)
	}
	// Removing the last variable of qa deletes qa.env and with it the environment.
	e.mustCall(http.StatusOK, "DELETE", base+"/environments/qa/variables/LOG_LEVEL", nil, &got)
	e.mustCall(http.StatusOK, "DELETE", base+"/environments/qa/variables/REGION", nil, &got)
	if exists("qa.env") || slices.ContainsFunc(got.Compose.Environments, func(env model.ComposeEnvironment) bool { return env.Name == "qa" }) {
		t.Errorf("qa kept: %+v", got.Compose.Environments)
	}
}
