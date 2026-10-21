package tfqa

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Environments layout checks, per catalog template with dev and prod
// environments: the export is one service module plus one root per
// environment, two exports are identical, a folder import of the export is
// one workflow whose export is the same tree, and (with TFQA_VALIDATE=1 or
// the corpus build) `terraform init -backend=false && terraform validate`
// pass in every environment root.
const (
	checkEnvTree   = "tree"
	checkEnvImport = "reimport"
	checkEnvStable = "stable"
	checkEnvValid  = "validate"
)

func TestEnvironmentsLayout(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	base := nimbo(t)
	var reps []*report
	for _, tpl := range cat.Templates() {
		t.Run(tpl.ID, func(t *testing.T) {
			rep := &report{Name: "template/" + tpl.ID, Status: map[string]string{}}
			reps = append(reps, rep)
			defer func() {
				for _, f := range rep.Failures {
					t.Error(f)
				}
			}()
			var wf model.Workflow
			if err := call(base, "POST", "/workflows", map[string]string{"name": tpl.Name, "templateId": tpl.ID}, &wf); err != nil {
				t.Fatal(err)
			}
			wf.Environments = nil
			for _, env := range []string{"dev", "prod"} {
				values := map[string]model.Value{}
				for _, v := range wf.Variables {
					if v.Name == "environment" {
						values[v.Name] = model.Value{Expr: `"` + env + `"`}
					}
				}
				wf.Environments = append(wf.Environments, model.Environment{Name: env, Values: values, EnvVars: []model.EnvVar{}})
			}
			if err := call(base, "PUT", "/workflows/"+wf.ID, wf, &wf); err != nil {
				t.Fatal(err)
			}
			if wf.EffectiveLayout() != model.LayoutEnvironments {
				t.Fatalf("layout %q", wf.EffectiveLayout())
			}

			exp := filepath.Join(t.TempDir(), "export")
			if err := exportTo(base, wf.ID, exp); err != nil {
				t.Fatal(err)
			}
			tree := readTree(exp)
			rep.set(checkEnvTree, checkEnvTreeShape(tree, hclcodec.ServiceName(&wf))...)

			again := filepath.Join(t.TempDir(), "again")
			if err := exportTo(base, wf.ID, again); err != nil {
				t.Fatal(err)
			}
			rep.set(checkEnvStable, diffTrees(exp, again)...)

			var back model.ImportResult
			if err := call(base, "POST", "/import", map[string]any{"dir": exp, "name": wf.Name}, &back); err != nil {
				rep.set(checkEnvImport, "re-import: "+err.Error())
			} else if len(back.Workflows) != 1 || back.Workflows[0].Layout != model.LayoutEnvironments || len(back.Workflows[0].Environments) != 2 {
				rep.set(checkEnvImport, "re-import is not one workflow with two environments")
			} else {
				round := filepath.Join(t.TempDir(), "round")
				if err := exportTo(base, back.Workflows[0].ID, round); err != nil {
					t.Fatal(err)
				}
				var diffs []string
				got := readTree(round)
				for _, name := range slices.Sorted(maps.Keys(tree)) {
					// READMEs show the description, app data the tree does not carry.
					if filepath.Base(name) != hclcodec.ReadmeFile && !bytes.Equal(tree[name], got[name]) {
						diffs = append(diffs, name+" differs after import")
					}
				}
				if a, b := slices.Sorted(maps.Keys(tree)), slices.Sorted(maps.Keys(got)); !slices.Equal(a, b) {
					diffs = append(diffs, "file lists differ after import")
				}
				rep.set(checkEnvImport, diffs...)
			}

			if validateEnabled() {
				var failures []string
				for _, env := range []string{"dev", "prod"} {
					if ok, out := validateEnvRoot(t, exp, env); !ok {
						failures = append(failures, env+":\n"+lastLines(out, 30))
					}
				}
				rep.set(checkEnvValid, failures...)
			}
		})
	}
	t.Log("\n" + formatEnvTable(reps))
}

// checkEnvTreeShape verifies the layout: no root module at the top, a
// service module without provider configuration, and per environment a
// state backend, a call of the service module and its values.
func checkEnvTreeShape(tree map[string][]byte, svc string) []string {
	var out []string
	has := func(name string) bool { _, ok := tree[name]; return ok }
	for _, name := range []string{"README.md", ".gitignore", "Makefile", "modules/" + svc + "/versions.tf", "modules/" + svc + "/variables.tf"} {
		if !has(name) {
			out = append(out, name+" missing")
		}
	}
	for name, data := range tree {
		switch {
		case !strings.Contains(name, "/") && strings.HasSuffix(name, ".tf"):
			out = append(out, name+": root module file outside environments/")
		case strings.HasPrefix(name, "modules/") && strings.HasSuffix(name, ".tf") && bytes.Contains(data, []byte("\nprovider \"")):
			out = append(out, name+": provider configuration in a module")
		}
	}
	for _, env := range []string{"dev", "prod"} {
		root := "environments/" + env + "/"
		for _, name := range []string{"backend.tf", "versions.tf", "providers.tf", "main.tf", "terraform.tfvars"} {
			if !has(root + name) {
				out = append(out, root+name+" missing")
			}
		}
		if !bytes.Contains(tree[root+"main.tf"], []byte(`source = "../../modules/`+svc+`"`)) {
			out = append(out, root+"main.tf does not call the service module")
		}
		if !bytes.Contains(tree[root+"backend.tf"], []byte("/"+env)) {
			out = append(out, root+"backend.tf has no per-environment state key")
		}
	}
	slices.Sort(out)
	return out
}

// validateEnvRoot runs terraform init -backend=false and validate in one
// environment root of a copy of the export (its ../../modules sources
// resolve inside the copy).
func validateEnvRoot(t *testing.T, exp, env string) (bool, string) {
	work := t.TempDir()
	copyTree(t, exp, work)
	target := filepath.Join(work, "environments", env)
	var envVars []string
	if _, err := os.Stat(filepath.Join(target, ".terraform.lock.hcl")); err == nil {
		envVars = []string{"TF_PLUGIN_CACHE_DIR="}
	}
	var log strings.Builder
	for _, args := range [][]string{{"init", "-backend=false", "-input=false", "-no-color"}, {"validate", "-no-color"}} {
		var out string
		var err error
		for attempt := range 3 {
			if out, err = terraform(target, envVars, args...); err == nil || args[0] != "init" || attempt == 2 {
				break
			}
		}
		log.WriteString(out)
		if err != nil {
			return false, log.String()
		}
	}
	return true, log.String()
}

func formatEnvTable(reps []*report) string {
	cols := []string{checkEnvTree, checkEnvStable, checkEnvImport, checkEnvValid}
	var sb strings.Builder
	sb.WriteString("| root | " + strings.Join(cols, " | ") + " |\n|---|" + strings.Repeat("---|", len(cols)) + "\n")
	for _, r := range reps {
		sb.WriteString("| " + r.Name)
		for _, c := range cols {
			s := r.Status[c]
			if s == "" {
				s = "–"
			}
			sb.WriteString(" | " + s)
		}
		sb.WriteString(" |\n")
	}
	return sb.String()
}
