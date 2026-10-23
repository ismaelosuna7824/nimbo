package kube

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// templateDir copies a workflow template's files into a temp directory.
func templateDir(t *testing.T, id string) string {
	t.Helper()
	src := filepath.Join("kubecatalog", "_templates", id)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func kustomizeWorkflow(t *testing.T) (string, *model.Workflow) {
	dir := templateDir(t, "k8s-kustomize-app")
	wf := &model.Workflow{ID: "k", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{
		{Kind: model.KubeKustomize, Path: "base", Environments: map[string]string{"dev": "overlays/dev", "prod": "overlays/prod"}},
	}}}
	Derive(context.Background(), dir, wf)
	return dir, wf
}

func helmWorkflow(t *testing.T) (string, *model.Workflow) {
	dir := templateDir(t, "k8s-helm-app")
	wf := &model.Workflow{ID: "h", Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{
		{Kind: model.KubeHelm, Path: "chart", Release: "podinfo", Namespace: "podinfo"},
	}}}
	Derive(context.Background(), dir, wf)
	return dir, wf
}

// apply writes a plan's files and sources like the API does.
func apply(t *testing.T, dir string, wf *model.Workflow, plan *EnvPlan) {
	t.Helper()
	writeFiles(t, dir, plan.Files)
	wf.Kubernetes.Sources = plan.Sources
	Derive(context.Background(), dir, wf)
}

func writeFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if data == nil {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func envNames(wf *model.Workflow) []string {
	var out []string
	for _, e := range wf.Environments {
		out = append(out, e.Name)
	}
	return out
}

func render(t *testing.T, dir string, wf *model.Workflow, env string) *Project {
	t.Helper()
	p := Open(context.Background(), dir, wf, Options{Environment: env})
	for _, r := range p.Sources {
		if r.Err != nil {
			t.Fatalf("%s does not render: %v", env, r.Err)
		}
	}
	return p
}

func object(t *testing.T, p *Project, id string) *Object {
	t.Helper()
	if o := p.Object(id); o != nil {
		return o
	}
	var ids []string
	for _, o := range p.Objects {
		ids = append(ids, o.ID)
	}
	t.Fatalf("%s not rendered in %q: %v", id, p.Environment, ids)
	return nil
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// setKnobs runs SetEnvSettings for env and writes the result.
func setKnobs(t *testing.T, dir string, wf *model.Workflow, env, target string, values map[string]*string) {
	t.Helper()
	p := Open(context.Background(), dir, wf, Options{Environment: env})
	base := Open(context.Background(), dir, wf, Options{})
	e := p.Edit()
	if err := e.SetEnvSettings(base, wf, target, values); err != nil {
		t.Fatalf("set %v: %v", values, err)
	}
	writeFiles(t, dir, e.Files())
	Derive(context.Background(), dir, wf)
}

func setting(t *testing.T, s model.EnvSettings, key string) model.EnvSetting {
	t.Helper()
	for _, x := range s.Settings {
		if x.Key == key {
			return x
		}
	}
	t.Fatalf("no setting %s in %+v", key, s.Settings)
	return model.EnvSetting{}
}

func TestKustomizeEnvironmentLifecycle(t *testing.T) {
	dir, wf := kustomizeWorkflow(t)

	// Duplicate prod: the overlay is copied with its names rewritten.
	plan, err := CreateEnvironment(dir, wf, "staging", "prod")
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(plan.Files)); !slices.Equal(got, []string{
		"overlays/staging/hpa.yaml", "overlays/staging/index.html", "overlays/staging/kustomization.yaml", "overlays/staging/namespace.yaml",
		"overlays/staging/pdb.yaml", "overlays/staging/resources.yaml", "overlays/staging/spread.yaml"}) {
		t.Fatalf("files %v", got)
	}
	k := string(plan.Files["overlays/staging/kustomization.yaml"])
	if !strings.Contains(k, "namespace: hello-staging") || !strings.Contains(k, "value: hello-staging.localhost") || strings.Contains(k, "prod") {
		t.Fatalf("staging kustomization:\n%s", k)
	}
	if plan.Sources[0].Environments["staging"] != "overlays/staging" || wf.Kubernetes.Sources[0].Environments["staging"] != "" {
		t.Fatalf("sources %v (the workflow's must stay untouched)", plan.Sources[0].Environments)
	}
	apply(t, dir, wf, plan)
	if got := envNames(wf); !slices.Equal(got, []string{"dev", "prod", "staging"}) {
		t.Fatalf("environments %v", got)
	}
	p := render(t, dir, wf, "staging")
	web := object(t, p, "Deployment/hello-staging/web")
	if n, _ := toInt(dig(web.Obj, "spec", "replicas")); n != 2 || p.scalerOf(web) == nil {
		t.Fatalf("staging web: replicas %v, hpa %v", n, p.scalerOf(web))
	}
	if _, err := CreateEnvironment(dir, wf, "staging", ""); err == nil {
		t.Fatal("duplicate name accepted")
	}

	// Knobs: replicas lands in the copied replicas transformer, the image
	// tag in a new images entry, limits in the overlay's resources patch.
	settings, err := EnvSettings(render(t, dir, wf, "staging"), render(t, dir, wf, ""), wf, "Deployment/web")
	if err != nil {
		t.Fatal(err)
	}
	if settings.File != "overlays/staging/kustomization.yaml" || len(settings.Targets) != 1 {
		t.Fatalf("settings %+v", settings)
	}
	if r := setting(t, settings, "replicas"); r.Value != "2" || r.Base != "1" || !r.Overridden || !r.Editable {
		t.Fatalf("replicas %+v", r)
	}
	if img := setting(t, settings, "image:nginx"); img.Value != "1.30-alpine" || img.Overridden {
		t.Fatalf("image %+v", img)
	}
	if m := setting(t, settings, "memory.request:nginx"); m.Value != "64Mi" || m.Base != "32Mi" || !m.Overridden {
		t.Fatalf("memory %+v", m)
	}
	setKnobs(t, dir, wf, "staging", "Deployment/web", map[string]*string{
		"replicas": new("3"), "image:nginx": new("1.29-alpine"), "cpu.limit:nginx": new("200m"), "hpa.max": new("6"),
	})
	k = read(t, dir, "overlays/staging/kustomization.yaml")
	if !strings.Contains(k, "count: 3") || !strings.Contains(k, "newTag: 1.29-alpine") {
		t.Fatalf("staging kustomization:\n%s", k)
	}
	if r := read(t, dir, "overlays/staging/resources.yaml"); !strings.Contains(r, "cpu: 200m") {
		t.Fatalf("resources patch:\n%s", r)
	}
	if h := read(t, dir, "overlays/staging/hpa.yaml"); !strings.Contains(h, "maxReplicas: 6") {
		t.Fatalf("hpa:\n%s", h)
	}
	p = render(t, dir, wf, "staging")
	web = object(t, p, "Deployment/hello-staging/web")
	c := list(dig(web.Obj, "spec", "template", "spec", "containers"))[0]
	if n, _ := toInt(dig(web.Obj, "spec", "replicas")); n != 3 || str(c, "image") != "nginxinc/nginx-unprivileged:1.29-alpine" || str(c, "resources", "limits", "cpu") != "200m" {
		t.Fatalf("staging web after edits: replicas %v, container %v", dig(web.Obj, "spec", "replicas"), c)
	}
	// The base and prod are untouched.
	if prod := object(t, render(t, dir, wf, "prod"), "Deployment/hello-prod/web"); fieldText(prod, "spec.replicas") != "2" ||
		fieldText(prod, "spec.template.spec.containers[name=nginx].image") != "nginxinc/nginx-unprivileged:1.30-alpine" {
		t.Fatalf("prod changed: %v", prod.Obj)
	}
	// Reset: the image entry goes, the memory request falls back to the base.
	setKnobs(t, dir, wf, "staging", "Deployment/web", map[string]*string{"image:nginx": nil, "memory.request:nginx": nil})
	web = object(t, render(t, dir, wf, "staging"), "Deployment/hello-staging/web")
	if fieldText(web, "spec.template.spec.containers[name=nginx].image") != "nginxinc/nginx-unprivileged:1.30-alpine" ||
		fieldText(web, "spec.template.spec.containers[name=nginx].resources.requests.memory") != "32Mi" {
		t.Fatalf("after reset: %v", web.Obj)
	}

	// Compare: the matrix shows each environment's values.
	cmp := CompareEnvironments(context.Background(), dir, wf)
	if len(cmp.Columns) != 4 || cmp.Columns[0].Label != "Base" || len(cmp.Errors) != 0 {
		t.Fatalf("columns %+v errors %v", cmp.Columns, cmp.Errors)
	}
	rows := map[string]model.EnvCompareRow{}
	for _, g := range cmp.Groups {
		if g.ID == "Deployment/web" {
			for _, r := range g.Rows {
				rows[r.Key] = r
			}
		}
	}
	want := map[string][]string{
		"replicas":           {"1", "1", "2", "3"},
		"namespace":          {"default", "hello-dev", "hello-prod", "hello-staging"},
		"autoscaling":        {"", "", "2–5 @ 70% CPU", "2–6 @ 70% CPU"},
		"cpu:nginx":          {"25m / —", "25m / —", "50m / —", "50m / 200m"},
		"pdb":                {"", "", "minAvailable 1", "minAvailable 1"},
		"hosts":              {"hello.localhost", "hello-dev.localhost", "hello-prod.localhost", "hello-staging.localhost"},
		"image:nginx":        {"nginxinc/nginx-unprivileged:1.30-alpine", "nginxinc/nginx-unprivileged:1.30-alpine", "nginxinc/nginx-unprivileged:1.30-alpine", "nginxinc/nginx-unprivileged:1.30-alpine"},
		"cm:site:index.html": nil, // differs, present
	}
	for key, vals := range want {
		r, ok := rows[key]
		if !ok {
			t.Fatalf("no row %s in %v", key, slices.Collect(maps.Keys(rows)))
		}
		if vals != nil && !slices.Equal(r.Values, vals) {
			t.Errorf("%s = %q, want %q", key, r.Values, vals)
		}
		if r.Differs == (key == "image:nginx") {
			t.Errorf("%s differs = %v", key, r.Differs)
		}
	}
	if !strings.Contains(rows["spread"].Values[2], "kubernetes.io/hostname") || rows["spread"].Values[1] != "" {
		t.Errorf("spread %q", rows["spread"].Values)
	}

	// Rename: the overlay moves and its names follow.
	plan, err = RenameEnvironment(dir, wf, "staging", "qa")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files["overlays/staging/kustomization.yaml"] != nil || !strings.Contains(string(plan.Files["overlays/qa/kustomization.yaml"]), "namespace: hello-qa") ||
		plan.Sources[0].Environments["qa"] != "overlays/qa" || plan.Sources[0].Environments["staging"] != "" || !slices.Equal(plan.Dirs, []string{"overlays/staging"}) {
		t.Fatalf("rename plan %v %v %v", slices.Sorted(maps.Keys(plan.Files)), plan.Sources[0].Environments, plan.Dirs)
	}
	apply(t, dir, wf, plan)
	if got := envNames(wf); !slices.Equal(got, []string{"dev", "prod", "qa"}) {
		t.Fatalf("environments %v", got)
	}
	if n := fieldText(object(t, render(t, dir, wf, "qa"), "Deployment/hello-qa/web"), "spec.replicas"); n != "3" {
		t.Fatalf("qa replicas %s", n)
	}

	// Delete: every file of the overlay goes; the base can't be deleted.
	plan, err = DeleteEnvironment(dir, wf, "qa")
	if err != nil {
		t.Fatal(err)
	}
	for f, data := range plan.Files {
		if data != nil || !strings.HasPrefix(f, "overlays/qa/") {
			t.Fatalf("delete touches %s", f)
		}
	}
	if len(plan.Files) != 7 || plan.Sources[0].Environments["qa"] != "" {
		t.Fatalf("delete plan %v %v", slices.Sorted(maps.Keys(plan.Files)), plan.Sources[0].Environments)
	}
	apply(t, dir, wf, plan)
	if got := envNames(wf); !slices.Equal(got, []string{"dev", "prod"}) {
		t.Fatalf("environments %v", got)
	}
	var ee *EditError
	if _, err := DeleteEnvironment(dir, wf, "qa"); !errors.As(err, &ee) || !ee.NotFound {
		t.Fatalf("deleting a missing environment: %v", err)
	}
}

func TestKustomizeEnvironmentFromBase(t *testing.T) {
	dir, wf := kustomizeWorkflow(t)
	plan, err := CreateEnvironment(dir, wf, "qa", "")
	if err != nil {
		t.Fatal(err)
	}
	k := string(plan.Files["overlays/qa/kustomization.yaml"])
	if !strings.Contains(k, "namespace: hello-qa") || !strings.Contains(k, "- ../../base") || !strings.Contains(k, "- namespace.yaml") {
		t.Fatalf("qa kustomization:\n%s", k)
	}
	apply(t, dir, wf, plan)
	p := render(t, dir, wf, "qa")
	web := object(t, p, "Deployment/hello-qa/web")
	object(t, p, "Namespace/hello-qa")
	// A knob the overlay doesn't set yet: replicas transformer and a new
	// strategic-merge patch for the resources and the image.
	setKnobs(t, dir, wf, "qa", "Deployment/web", map[string]*string{"replicas": new("2"), "memory.limit:nginx": new("96Mi")})
	k = read(t, dir, "overlays/qa/kustomization.yaml")
	if !strings.Contains(k, "replicas:\n  - name: web\n    count: 2") || !strings.Contains(k, "- path: deployment-web-patch.yaml") {
		t.Fatalf("qa kustomization:\n%s", k)
	}
	web = object(t, render(t, dir, wf, "qa"), "Deployment/hello-qa/web")
	if fieldText(web, "spec.replicas") != "2" || fieldText(web, "spec.template.spec.containers[name=nginx].resources.limits.memory") != "96Mi" ||
		fieldText(web, "spec.template.spec.containers[name=nginx].resources.requests.cpu") != "25m" {
		t.Fatalf("qa web %v", web.Obj)
	}
	// Plain manifests take no environments.
	wf2 := &model.Workflow{Kind: model.WorkflowKubernetes, Kubernetes: &model.KubernetesModel{Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "base"}}}}
	if _, err := CreateEnvironment(dir, wf2, "dev", ""); err == nil || !strings.Contains(err.Error(), "Kustomize or Helm") {
		t.Fatalf("manifests: %v", err)
	}
}

func TestHelmEnvironmentLifecycle(t *testing.T) {
	dir, wf := helmWorkflow(t)
	if got := envNames(wf); !slices.Equal(got, []string{"dev", "prod"}) {
		t.Fatalf("environments %v", got)
	}
	plan, err := CreateEnvironment(dir, wf, "staging", "prod")
	if err != nil {
		t.Fatal(err)
	}
	v := string(plan.Files["chart/values-staging.yaml"])
	if len(plan.Files) != 1 || !strings.Contains(v, "host: podinfo-staging.localhost") || !strings.Contains(v, "Hello from Helm (staging)") {
		t.Fatalf("staging values %v:\n%s", slices.Sorted(maps.Keys(plan.Files)), v)
	}
	apply(t, dir, wf, plan)

	settings, err := EnvSettings(render(t, dir, wf, "staging"), render(t, dir, wf, ""), wf, "")
	if err != nil {
		t.Fatal(err)
	}
	if settings.File != "chart/values-staging.yaml" || settings.Target != "Deployment/podinfo" {
		t.Fatalf("settings %+v", settings)
	}
	if img := setting(t, settings, "image:podinfo"); img.Value != "6.15.0" || !img.Editable || img.Overridden {
		t.Fatalf("image %+v", img)
	}
	if r := setting(t, settings, "replicas"); !r.Editable || r.Value != "2" || !r.Overridden || r.Base != "1" {
		t.Fatalf("replicas next to the autoscaler %+v", r)
	}
	if l := setting(t, settings, "cpu.limit:podinfo"); l.Editable {
		t.Fatalf("a field the chart doesn't render is editable: %+v", l)
	}
	setKnobs(t, dir, wf, "staging", "Deployment/podinfo", map[string]*string{"image:podinfo": new("6.14.0"), "hpa.min": new("3"), "memory.limit:podinfo": new("128Mi")})
	v = read(t, dir, "chart/values-staging.yaml")
	if !strings.Contains(v, "tag: 6.14.0") && !strings.Contains(v, `tag: "6.14.0"`) || !strings.Contains(v, "minReplicas: 3") {
		t.Fatalf("staging values:\n%s", v)
	}
	if strings.Contains(read(t, dir, "chart/values.yaml"), "6.14.0") || strings.Contains(read(t, dir, "chart/values-prod.yaml"), "minReplicas") {
		t.Fatal("the edit leaked into the shared values")
	}
	p := render(t, dir, wf, "staging")
	dep := object(t, p, "Deployment/podinfo/podinfo")
	if img := fieldText(dep, "spec.template.spec.containers[name=podinfo].image"); img != "ghcr.io/stefanprodan/podinfo:6.14.0" {
		t.Fatalf("staging image %s", img)
	}
	if h := p.scalerOf(dep); h == nil || fieldText(h, "spec.minReplicas") != "3" {
		t.Fatalf("staging hpa %v", h)
	}
	// Back to the base value: the override is dropped, not repeated.
	setKnobs(t, dir, wf, "staging", "Deployment/podinfo", map[string]*string{"image:podinfo": new("6.15.0")})
	if v := read(t, dir, "chart/values-staging.yaml"); strings.Contains(v, "tag:") {
		t.Fatalf("staging values keep the tag:\n%s", v)
	}

	plan, err = RenameEnvironment(dir, wf, "staging", "qa")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files["chart/values-staging.yaml"] != nil || !strings.Contains(string(plan.Files["chart/values-qa.yaml"]), "podinfo-qa.localhost") {
		t.Fatalf("rename %v", slices.Sorted(maps.Keys(plan.Files)))
	}
	apply(t, dir, wf, plan)
	plan, err = DeleteEnvironment(dir, wf, "qa")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Files) != 1 || plan.Files["chart/values-qa.yaml"] != nil {
		t.Fatalf("delete %v", plan.Files)
	}
	apply(t, dir, wf, plan)
	if got := envNames(wf); !slices.Equal(got, []string{"dev", "prod"}) {
		t.Fatalf("environments %v", got)
	}

	// From the base: an empty values file next to the others, editable.
	plan, err = CreateEnvironment(dir, wf, "qa", "")
	if err != nil {
		t.Fatal(err)
	}
	apply(t, dir, wf, plan)
	setKnobs(t, dir, wf, "qa", "", map[string]*string{"replicas": new("4")})
	if n := fieldText(object(t, render(t, dir, wf, "qa"), "Deployment/podinfo/podinfo"), "spec.replicas"); n != "4" {
		t.Fatalf("qa replicas %s:\n%s", n, read(t, dir, "chart/values-qa.yaml"))
	}
}

func TestRenameEnvText(t *testing.T) {
	in := "namespace: hello-prod\n# prod overlay, production-grade (prod)\nimage: app:prod\n  - name: x\n    newTag: prod\nhost: prod.example.com\nprodigy: product\n"
	want := "namespace: hello-qa\n# qa overlay, production-grade (qa)\nimage: app:prod\n  - name: x\n    newTag: prod\nhost: qa.example.com\nprodigy: product\n"
	if got := string(renameEnvText([]byte(in), "prod", "qa")); got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if got := envValuesName("chart/values.prod.yaml", "prod", "qa"); got != "chart/values.qa.yaml" {
		t.Fatal(got)
	}
	if got := envValuesName("chart/values/prod.yaml", "prod", "qa"); got != "chart/values/qa.yaml" {
		t.Fatal(got)
	}
}
