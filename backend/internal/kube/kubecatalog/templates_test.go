package kubecatalog

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// writeFiles writes files into a fresh directory.
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

// Every template renders in every environment without errors or warnings
// (k8s_* advice included): they are what new users copy.
func TestTemplatesRenderClean(t *testing.T) {
	if len(Templates()) != 5 {
		t.Fatalf("want 5 templates, got %d", len(Templates()))
	}
	for _, tmpl := range Templates() {
		t.Run(tmpl.ID, func(t *testing.T) {
			if tmpl.Kind != model.WorkflowKubernetes {
				t.Errorf("kind %q", tmpl.Kind)
			}
			files, k, ok := TemplateFiles(tmpl.ID)
			if !ok || len(files) == 0 || len(k.Sources) == 0 {
				t.Fatalf("no files or sources")
			}
			dir := writeFiles(t, files)
			wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: k}
			p, _ := kube.Derive(context.Background(), dir, wf)
			envs := []string{""}
			for _, e := range wf.Environments {
				envs = append(envs, e.Name)
			}
			for _, env := range envs {
				wf.ActiveEnvironment = env
				p, diags := kube.Derive(context.Background(), dir, wf)
				if len(p.Objects) == 0 {
					t.Errorf("env %q rendered nothing", env)
				}
				for _, d := range slices.Concat(diags, kuberules.Check(p, wf, model.BestPracticeSettings{})) {
					if d.Severity != model.SeverityInfo {
						t.Errorf("env %q: %s %s %s:%d %s", env, d.Severity, d.Code, d.File, d.Line, d.Message)
					}
				}
			}
			_ = p
		})
	}
}

func TestTemplateEnvironments(t *testing.T) {
	for id, want := range map[string][]string{"k8s-kustomize-app": {"dev", "prod"}, "k8s-helm-app": {"dev", "prod"}} {
		files, k, _ := TemplateFiles(id)
		wf := &model.Workflow{ID: "t", Kind: model.WorkflowKubernetes, Kubernetes: k}
		kube.Derive(context.Background(), writeFiles(t, files), wf)
		var got []string
		for _, e := range wf.Environments {
			got = append(got, e.Name)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s environments %v, want %v", id, got, want)
		}
	}
}

func TestTemplateFilesAreCopies(t *testing.T) {
	_, k, _ := TemplateFiles("k8s-kustomize-app")
	k.Sources[0].Environments["dev"] = "changed"
	_, k2, _ := TemplateFiles("k8s-kustomize-app")
	if k2.Sources[0].Environments["dev"] != "overlays/dev" {
		t.Error("TemplateFiles returned shared state")
	}
}
