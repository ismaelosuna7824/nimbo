package kubeconvert

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// renderConverted writes a conversion and renders it with the Kubernetes
// engine for env.
func renderConverted(t *testing.T, res *Result, env string) *kube.Project {
	t.Helper()
	dir := writeFiles(t, res.Files)
	wf := &model.Workflow{ID: "conv", Kind: model.WorkflowKubernetes, Kubernetes: res.Model}
	return kube.Open(context.Background(), dir, wf, kube.Options{Environment: env})
}

// Every template converts to files the engine renders in every format and
// environment, with the same objects.
func TestConvertedTemplatesRender(t *testing.T) {
	for _, tmpl := range compose.Templates() {
		var want map[string]bool
		for _, f := range []string{model.ConvertManifests, model.ConvertKustomize, model.ConvertHelm} {
			res, err := Convert(openTemplate(t, tmpl.ID), Options{Format: f, Name: "app"})
			if err != nil {
				t.Fatalf("%s %s: %v", tmpl.ID, f, err)
			}
			envs := []string{""}
			if f != model.ConvertManifests {
				envs = append(envs, "dev", "prod")
			}
			for _, env := range envs {
				p := renderConverted(t, res, env)
				for _, d := range p.Diagnostics {
					if d.Severity == model.SeverityError {
						t.Errorf("%s %s %q: %s %s:%d %s", tmpl.ID, f, env, d.Code, d.File, d.Line, d.Message)
					}
				}
				got := map[string]bool{}
				for _, o := range p.Objects {
					if o.Kind == "PodDisruptionBudget" || o.Kind == "HorizontalPodAutoscaler" || o.Kind == "Namespace" {
						continue // prod only / not in Helm (release namespace)
					}
					got[o.Kind+"/"+o.Namespace+"/"+o.Name] = true
					if o.Namespace != "app" && o.Namespace != "" {
						t.Errorf("%s %s %q: %s in namespace %q", tmpl.ID, f, env, o.ID, o.Namespace)
					}
				}
				if len(got) == 0 {
					t.Errorf("%s %s %q: nothing rendered", tmpl.ID, f, env)
				}
				hpas := map[string]map[string]any{}
				for _, o := range p.Objects {
					if o.Kind == "HorizontalPodAutoscaler" {
						spec := o.Obj["spec"].(map[string]any)
						hpas[spec["scaleTargetRef"].(map[string]any)["name"].(string)] = spec
					}
				}
				if env != "prod" && len(hpas) > 0 {
					t.Errorf("%s %s %q: HPAs outside prod", tmpl.ID, f, env)
				}
				if env == "prod" {
					// Stateless Deployments (RollingUpdate) scale out behind a
					// PDB: an HPA from 2+ replicas without spec.replicas (it
					// owns the count), or 2+ fixed replicas.
					pdbs, stateless := 0, 0
					for _, o := range p.Objects {
						if o.Kind == "PodDisruptionBudget" {
							pdbs++
						}
						if o.Kind != "Deployment" {
							continue
						}
						spec := o.Obj["spec"].(map[string]any)
						if fmt.Sprint(spec["strategy"].(map[string]any)["type"]) != "RollingUpdate" {
							continue
						}
						stateless++
						r, hasReplicas := spec["replicas"]
						if h := hpas[o.Name]; h != nil {
							if hasReplicas {
								t.Errorf("%s %s prod: %s sets replicas %v under an HPA", tmpl.ID, f, o.Name, r)
							}
							if n, _ := strconv.Atoi(fmt.Sprint(h["minReplicas"])); n < 2 {
								t.Errorf("%s %s prod: %s HPA minReplicas %v", tmpl.ID, f, o.Name, h["minReplicas"])
							}
							continue
						}
						if n, _ := strconv.Atoi(fmt.Sprint(r)); n < 2 {
							t.Errorf("%s %s prod: %s replicas %v", tmpl.ID, f, o.Name, r)
						}
					}
					if pdbs != stateless {
						t.Errorf("%s %s prod: %d PDBs for %d stateless Deployments", tmpl.ID, f, pdbs, stateless)
					}
					if stateless > 0 && len(hpas) == 0 {
						t.Errorf("%s %s prod: no HPA", tmpl.ID, f)
					}
				}
				if env == "dev" {
					// Dev runs what compose runs: every Deployment sets replicas.
					for _, o := range p.Objects {
						if o.Kind != "Deployment" {
							continue
						}
						if _, ok := o.Obj["spec"].(map[string]any)["replicas"]; !ok {
							t.Errorf("%s %s dev: %s without replicas", tmpl.ID, f, o.Name)
						}
					}
				}
				for k := range want {
					if !got[k] && !generatedName(k, got) {
						t.Errorf("%s %s %q: missing %s", tmpl.ID, f, env, k)
					}
				}
			}
		}
	}
}

// generatedName: Kustomize appends a hash to generated ConfigMaps/Secrets.
func generatedName(k string, got map[string]bool) bool {
	for g := range got {
		if len(g) > len(k)+1 && g[:len(k)+1] == k+"-" {
			return true
		}
	}
	return false
}

// Converted templates pass the k8s_* rules without warnings in every
// format and environment.
func TestConvertedTemplatesPassRules(t *testing.T) {
	for _, tmpl := range compose.Templates() {
		for _, f := range []string{model.ConvertManifests, model.ConvertKustomize, model.ConvertHelm} {
			res, err := Convert(openTemplate(t, tmpl.ID), Options{Format: f, Name: "app"})
			if err != nil {
				t.Fatalf("%s %s: %v", tmpl.ID, f, err)
			}
			dir := writeFiles(t, res.Files)
			envs := []string{""}
			if f != model.ConvertManifests {
				envs = append(envs, "dev", "prod")
			}
			for _, env := range envs {
				wf := &model.Workflow{ID: "conv", Kind: model.WorkflowKubernetes, Kubernetes: cloneModel(res.Model), ActiveEnvironment: env}
				p, diags := kube.Derive(context.Background(), dir, wf)
				all := slices.Concat(diags, kuberules.Check(p, wf, model.BestPracticeSettings{}))
				t.Logf("%s %s %q: %d objects, %d info findings", tmpl.ID, f, env, len(p.Objects), len(all))
				for _, d := range all {
					if d.Severity != model.SeverityInfo {
						t.Errorf("%s %s %q: %s %s %s:%d %s", tmpl.ID, f, env, d.Severity, d.Code, d.File, d.Line, d.Message)
					}
				}
			}
		}
	}
}

func cloneModel(k *model.KubernetesModel) *model.KubernetesModel {
	c := *k
	c.Sources = slices.Clone(k.Sources)
	c.Builds = slices.Clone(k.Builds)
	return &c
}

func writeFiles(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
