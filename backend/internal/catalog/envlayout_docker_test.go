//go:build docker

package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// envLayoutTree renders a template with dev and prod environments in the
// environments layout into a temporary directory.
func envLayoutTree(t *testing.T, c *Catalog, id string) string {
	t.Helper()
	files, ok := c.TemplateFiles(id)
	if !ok {
		t.Fatalf("template %s not found", id)
	}
	wf, _, err := hclcodec.Import(id, files)
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.UnpinFiles(wf, c.ResourceFile)
	for _, env := range []string{"dev", "prod"} {
		values := map[string]model.Value{}
		for _, v := range wf.Variables {
			if v.Name == "environment" {
				values[v.Name] = model.Value{Expr: `"` + env + `"`}
			}
		}
		wf.Environments = append(wf.Environments, model.Environment{Name: env, Values: values})
	}
	hclcodec.Normalize(wf)
	out, err := hclcodec.ExportLayout(wf, hclcodec.ExportOptions{Environments: true, ResourceFile: c.ResourceFile})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "nimbo-envlayout-"+id+"-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	for name, data := range out {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestEnvironmentsLayoutValidate validates every environment root of every
// template exported in the environments layout (the whole tree is mounted
// so ../../modules resolves).
func TestEnvironmentsLayoutValidate(t *testing.T) {
	c := mustLoad(t)
	for _, tpl := range c.Templates() {
		t.Run(tpl.ID, func(t *testing.T) {
			dir := envLayoutTree(t, c, tpl.ID)
			for _, env := range []string{"dev", "prod"} {
				root := "-chdir=environments/" + env
				runTerraform(t, "", dir, nil, root, "init", "-input=false", "-backend=false", "-no-color")
				runTerraform(t, "", dir, nil, root, "validate", "-no-color")
			}
		})
	}
}

// localBackendOverride keeps an environment root's state local on Floci.
const localBackendOverride = `terraform {
  backend "local" {}
}
`

// TestEnvironmentsLayoutApplyOnFloci deploys the dev root of
// aws-serverless-api, exported in the environments layout, to Floci:
// apply, the API answers, a second plan is empty, destroy.
func TestEnvironmentsLayoutApplyOnFloci(t *testing.T) {
	c := mustLoad(t)
	pod, base, env := startFloci(t)
	dir := envLayoutTree(t, c, "aws-serverless-api")
	root := filepath.Join(dir, "environments", "dev")
	for name, src := range map[string]string{"floci_override.tf": flociOverride, "backend_override.tf": localBackendOverride} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	chdir := "-chdir=environments/dev"
	runTerraform(t, pod, dir, env, chdir, "init", "-input=false", "-no-color")
	start := time.Now()
	runTerraform(t, pod, dir, env, chdir, "apply", "-auto-approve", "-input=false", "-no-color")
	t.Logf("apply took %s", time.Since(start).Round(time.Millisecond))
	outputs := map[string]struct {
		Value any `json:"value"`
	}{}
	raw := runTerraform(t, pod, dir, env, chdir, "output", "-json")
	if err := json.Unmarshal([]byte(raw), &outputs); err != nil {
		t.Fatalf("outputs: %v\n%s", err, raw)
	}
	vals := map[string]string{}
	for k, v := range outputs {
		vals[k] = fmt.Sprint(v.Value)
	}
	applyChecks["aws-serverless-api"](t, pod, base, vals)
	planNoChanges(t, pod, dir, env, chdir)
	runTerraform(t, pod, dir, env, chdir, "destroy", "-auto-approve", "-input=false", "-no-color")
}
