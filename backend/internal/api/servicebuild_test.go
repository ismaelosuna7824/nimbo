package api

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
)

func exportZipFiles(t *testing.T, e *testEnv, id, query string) map[string]string {
	t.Helper()
	res, err := http.Get(e.srv.URL + "/api/v1/workflows/" + id + "/export.zip?" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("export.zip: %v (%s)", err, data)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

// An export whose Terraform packages a service build documents and runs
// the build (README, .gitignore, CI/CD pipeline) and never ships dist.
func TestExportServiceBuilds(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Fn")
	applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"main.tf": new(`data "archive_file" "api" {
  type        = "zip"
  source_dir  = "${path.module}/services/api/dist"
  output_path = "${path.module}/build/api.zip"
}
`),
		"services/api/Dockerfile":     new("FROM node:22-alpine AS build\nRUN true\nFROM scratch AS package\nCOPY --from=build /out/ /\n"),
		"services/api/src/index.ts":   new("export const handler = async () => ({ statusCode: 200 });\n"),
		"services/api/dist/index.mjs": new("stale build output"),
	}})

	cmd := "docker build --target package --output type=local,dest=services/api/dist services/api"
	files := exportZipFiles(t, e, wf.ID, "ci=github")
	if _, ok := files["services/api/dist/index.mjs"]; ok {
		t.Error("export ships build output")
	}
	if _, ok := files["services/api/src/index.ts"]; !ok {
		t.Error("export lacks the service sources")
	}
	if r := files["README.md"]; !strings.Contains(r, "## Build the services") || !strings.Contains(r, cmd) {
		t.Errorf("README:\n%s", r)
	}
	if g := files[".gitignore"]; !strings.Contains(g, "**/services/*/dist/") {
		t.Errorf(".gitignore:\n%s", g)
	}
	if p := files[".github/workflows/fn.yml"]; !strings.Contains(p, "--output type=local,dest=services/api/dist services/api") {
		t.Errorf("pipeline:\n%s", p)
	}

	// The sources alone (no package stage referenced) build nothing.
	applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{"main.tf": new("locals {\n  a = 1\n}\n")}})
	for name, data := range exportZipFiles(t, e, wf.ID, "ci=gitlab") {
		if strings.Contains(data, "docker build") || strings.Contains(data, "services/*/dist") {
			t.Errorf("%s mentions service builds without any:\n%s", name, data)
		}
	}
}

// A nimbo.local image the Terraform runs gets build-and-push commands in
// the README and a push job in the pipeline.
func TestExportServiceImages(t *testing.T) {
	e := newEnv(t)
	wf := e.createEmpty("Web")
	applyChanges(t, e, wf.ID, map[string]any{"changes": map[string]*string{
		"main.tf": new(`variable "web_image" {
  type    = string
  default = "nimbo.local/web:latest"
}

resource "google_cloud_run_v2_service" "web" {
  name     = "web"
  location = "us-central1"
  template {
    containers {
      image = var.web_image
    }
  }
}
`),
		"services/web/Dockerfile": new("FROM golang:1.25-alpine AS build\nRUN true\nFROM alpine:3 AS runtime\nCOPY --from=build /web /web\n"),
		"services/web/main.go":    new("package main\n\nfunc main() {}\n"),
	}})
	files := exportZipFiles(t, e, wf.ID, "ci=gitlab")
	if r := files["README.md"]; !strings.Contains(r, "docker build --target runtime -t $REGISTRY/web:$TAG services/web") ||
		!strings.Contains(r, "export TF_VAR_web_image=$REGISTRY/web:$TAG") || strings.Contains(r, "Declare the variable") {
		t.Errorf("README:\n%s", r)
	}
	if p := files[".gitlab-ci.yml"]; !strings.Contains(p, `docker push "$IMAGE_REGISTRY/web:$CI_COMMIT_SHA"`) || !strings.Contains(p, `export TF_VAR_web_image="$IMAGE_REGISTRY/web:$CI_COMMIT_SHA"`) {
		t.Errorf("pipeline:\n%s", p)
	}
	if g := files[".gitignore"]; strings.Contains(g, "services/") {
		t.Errorf(".gitignore:\n%s", g)
	}
}
