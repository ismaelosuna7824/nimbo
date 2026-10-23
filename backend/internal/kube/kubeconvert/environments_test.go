package kubeconvert

import (
	"context"
	"encoding/base64"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube"
	"github.com/ismaelosuna7824/nimbo/backend/internal/kube/kuberules"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The node/postgres/redis template with a prod environment (2 api
// replicas, bigger limits, another log level and database password, a
// newer redis) and a dev one (debug logging).
var envOverrides = map[string]string{
	"compose.prod.yaml": `services:
  api:
    ports: !override ["3100-3101:3000"]
    environment:
      LOG_LEVEL: warn
    deploy:
      replicas: 2
      resources:
        limits:
          memory: 512M
  cache:
    image: redis:7.4-alpine
`,
	".env.prod":        "POSTGRES_PASSWORD=prod-only-secret\n",
	"compose.dev.yaml": "services:\n  api:\n    environment:\n      LOG_LEVEL: debug\n",
}

// openEnvironments writes the template plus the overrides and opens the
// base and each environment.
func openEnvironments(t *testing.T) (*compose.Project, []Environment) {
	t.Helper()
	files, ok := compose.TemplateFiles("docker-node-postgres-redis")
	if !ok {
		t.Fatal("no template")
	}
	files = maps.Clone(files)
	for k, v := range envOverrides {
		files[k] = []byte(v)
	}
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
	open := func(env string) *compose.Project {
		p := compose.Open(context.Background(), dir, []string{"compose.yaml"}, nil, compose.Options{Environment: env})
		if p.Model == nil {
			t.Fatalf("%q: %+v", env, p.Diagnostics)
		}
		return p
	}
	return open(""), []Environment{{Name: "dev", Project: open("dev")}, {Name: "prod", Project: open("prod")}}
}

func TestConvertMapsDockerEnvironments(t *testing.T) {
	for _, f := range []string{model.ConvertKustomize, model.ConvertHelm} {
		base, envs := openEnvironments(t)
		res, err := Convert(base, Options{Format: f, Name: "app", Environments: envs})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if !slices.Equal(res.Report.Environments, []string{"dev", "prod"}) {
			t.Errorf("%s environments %v", f, res.Report.Environments)
		}
		if len(res.Report.Autoscaling) == 0 || res.Report.Autoscaling[0].Environment != "prod" {
			t.Errorf("%s autoscaling %+v", f, res.Report.Autoscaling)
		}
		// No credential is written into a manifest or kustomization.
		for rel, data := range res.Files {
			if strings.Contains(string(data), "prod-only-secret") && !strings.HasSuffix(rel, ".secret.env") && !strings.HasSuffix(rel, "values-prod.yaml") {
				t.Errorf("%s: secret value in %s", f, rel)
			}
		}
		if f == model.ConvertKustomize {
			if _, ok := res.Files["k8s/overlays/prod/env/api.secret.env"]; !ok {
				t.Errorf("no prod secret env file: %v", slices.Sorted(maps.Keys(res.Files)))
			}
			if string(res.Files["k8s/overlays/dev/env/api.env"]) != "LOG_LEVEL=debug\n" {
				t.Errorf("dev ConfigMap values:\n%s", res.Files["k8s/overlays/dev/env/api.env"])
			}
		}
		dir := writeFiles(t, res.Files)
		render := func(env string) (*kube.Project, []model.Diagnostic) {
			wf := &model.Workflow{ID: "conv", Kind: model.WorkflowKubernetes, Kubernetes: cloneModel(res.Model), ActiveEnvironment: env}
			p, diags := kube.Derive(context.Background(), dir, wf)
			return p, slices.Concat(diags, kuberules.Check(p, wf, model.BestPracticeSettings{}))
		}
		for _, env := range []string{"", "dev", "prod"} {
			p, diags := render(env)
			for _, d := range diags {
				if d.Severity != model.SeverityInfo {
					t.Errorf("%s %q: %s %s %s:%d %s", f, env, d.Severity, d.Code, d.File, d.Line, d.Message)
				}
			}
			objs := map[string]map[string]any{}
			for _, o := range p.Objects {
				name := o.Name
				if i := strings.LastIndex(name, "-"); (o.Kind == "ConfigMap" || o.Kind == "Secret") && f == model.ConvertKustomize && i > 0 && len(name)-i == 11 {
					name = name[:i] // generated hash suffix
				}
				objs[o.Kind+"/"+name] = o.Obj
			}
			api := objs["Deployment/api"]
			if api == nil {
				t.Fatalf("%s %q: no api Deployment: %v", f, env, slices.Sorted(maps.Keys(objs)))
			}
			c := dig(api, "spec", "template", "spec", "containers").([]any)[0].(map[string]any)
			mem := fmt.Sprint(dig(c, "resources", "limits", "memory"))
			cm := objs["ConfigMap/api-env"]
			level := fmt.Sprint(dig(cm, "data", "LOG_LEVEL"))
			secret := fmt.Sprint(dig(objs["Secret/api-secret"], "stringData", "DATABASE_URL"))
			if b, err := base64.StdEncoding.DecodeString(fmt.Sprint(dig(objs["Secret/api-secret"], "data", "DATABASE_URL"))); err == nil {
				secret += string(b)
			}
			redis := fmt.Sprint(dig(objs["StatefulSet/cache"], "spec", "template", "spec", "containers").([]any)[0].(map[string]any)["image"])
			_, hpa := objs["HorizontalPodAutoscaler/api"]
			switch env {
			case "prod":
				if mem != "512Mi" || level != "warn" || !strings.Contains(redis, "7.4-alpine") || !hpa || objs["PodDisruptionBudget/api"] == nil {
					t.Errorf("%s prod: memory %s LOG_LEVEL %s redis %s hpa %v", f, mem, level, redis, hpa)
				}
				if min := fmt.Sprint(dig(objs["HorizontalPodAutoscaler/api"], "spec", "minReplicas")); min != "2" {
					t.Errorf("%s prod: HPA min %s", f, min)
				}
				if !strings.Contains(secret, "prod-only-secret") {
					t.Errorf("%s prod: api secret DATABASE_URL %s lacks the prod password", f, secret)
				}
			case "dev":
				if mem != "256Mi" || level != "debug" || hpa || !strings.Contains(redis, "7-alpine") || strings.Contains(redis, "7.4") {
					t.Errorf("%s dev: memory %s LOG_LEVEL %s redis %s hpa %v", f, mem, level, redis, hpa)
				}
				if r := fmt.Sprint(dig(api, "spec", "replicas")); r != "1" {
					t.Errorf("%s dev: replicas %s", f, r)
				}
			case "":
				if hpa || mem != "256Mi" {
					t.Errorf("%s base: memory %s hpa %v", f, mem, hpa)
				}
			}
		}
	}
}

// Without docker environments the converter keeps writing dev and prod.
func TestConvertWithoutEnvironmentsKeepsDevProd(t *testing.T) {
	res, err := Convert(openTemplate(t, "docker-node-postgres-redis"), Options{Format: model.ConvertKustomize, Name: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Report.Environments, []string{"dev", "prod"}) || res.Files["k8s/overlays/prod/hpa.yaml"] == nil || res.Files["k8s/overlays/dev/kustomization.yaml"] == nil {
		t.Errorf("environments %v files %v", res.Report.Environments, slices.Sorted(maps.Keys(res.Files)))
	}
	// Plain manifests report the environments they can't carry.
	base, envs := openEnvironments(t)
	res, err = Convert(base, Options{Format: model.ConvertManifests, Name: "app", Environments: envs})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(res.Report.Unmapped, func(i model.KubeConvertIssue) bool { return i.Feature == "environments" }) {
		t.Errorf("manifests: no environments issue: %+v", res.Report.Unmapped)
	}
}

func dig(m any, keys ...string) any {
	for _, k := range keys {
		mm, ok := m.(map[string]any)
		if !ok {
			return nil
		}
		m = mm[k]
	}
	return m
}
