// Package kubecatalog is the Kubernetes palette and templates: workload
// starters, Deployments/StatefulSets from the Docker image catalog, pinned
// Helm charts, and the starter workflows (GET /templates kind "kubernetes").
package kubecatalog

import (
	"embed"
	"io/fs"
	"maps"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Templates live in a directory the Go tool ignores (leading "_").
//
//go:embed all:_templates
var assets embed.FS

// template is a starter workflow and how it deploys.
type template struct {
	model.Template
	k model.KubernetesModel
}

var templates = []template{
	{model.Template{ID: "k8s-webapp-postgres", Name: "Web app + PostgreSQL", Description: "A Node.js app built from web/ and PostgreSQL in a StatefulSet, wired with a ConfigMap, Secrets and an Ingress (plain manifests)", Difficulty: "beginner", Tags: []string{"node", "postgres", "manifests", "ingress"}},
		model.KubernetesModel{
			Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}},
			Builds:  []model.KubeBuild{{Image: "web:dev", Context: "web"}},
		}},
	{model.Template{ID: "k8s-kustomize-app", Name: "Kustomize base + dev/prod overlays", Description: "NGINX serving a generated ConfigMap; overlays change the namespace, replicas, resources and add a PodDisruptionBudget and an autoscaler in prod", Difficulty: "intermediate", Tags: []string{"kustomize", "nginx", "environments"}},
		model.KubernetesModel{
			Sources: []model.KubeSource{{Kind: model.KubeKustomize, Path: "base", Environments: map[string]string{"dev": "overlays/dev", "prod": "overlays/prod"}}},
		}},
	{model.Template{ID: "k8s-helm-app", Name: "Helm chart with dev/prod values", Description: "A Helm chart for podinfo with values-dev.yaml and values-prod.yaml (replicas, autoscaler, PodDisruptionBudget, Ingress host)", Difficulty: "intermediate", Tags: []string{"helm", "go", "environments"}},
		model.KubernetesModel{
			Sources: []model.KubeSource{{Kind: model.KubeHelm, Path: "chart", Release: "podinfo", Namespace: "podinfo",
				Environments: map[string]string{"dev": "chart/values-dev.yaml", "prod": "chart/values-prod.yaml"}}},
		}},
	{model.Template{ID: "k8s-microservices", Name: "Microservices with Ingress + autoscaling", Description: "A frontend and a backend (podinfo) behind one Ingress, each scaled by a HorizontalPodAutoscaler, with a Redis cache", Difficulty: "intermediate", Tags: []string{"microservices", "ingress", "hpa", "redis"}},
		model.KubernetesModel{
			Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}},
		}},
	{model.Template{ID: "k8s-cronjob-worker", Name: "CronJob producer + queue worker", Description: "A CronJob queues jobs in Redis every minute and a worker Deployment processes them", Difficulty: "beginner", Tags: []string{"cronjob", "worker", "redis", "queue"}},
		model.KubernetesModel{
			Sources: []model.KubeSource{{Kind: model.KubeManifests, Path: "k8s"}},
		}},
}

func init() {
	for i := range templates {
		templates[i].Kind = model.WorkflowKubernetes
	}
}

// Templates lists the Kubernetes templates.
func Templates() []model.Template {
	out := make([]model.Template, len(templates))
	for i, t := range templates {
		out[i] = t.Template
	}
	return out
}

// TemplateFiles returns the files of a Kubernetes template and its sources
// and builds (nil, nil, false for an unknown ID).
func TemplateFiles(id string) (map[string][]byte, *model.KubernetesModel, bool) {
	for _, t := range templates {
		if t.ID != id {
			continue
		}
		k := t.k
		k.Sources = append([]model.KubeSource(nil), t.k.Sources...)
		for i := range k.Sources {
			k.Sources[i].Environments = maps.Clone(k.Sources[i].Environments)
		}
		k.Builds = append([]model.KubeBuild(nil), t.k.Builds...)
		return readTree("_templates/" + id), &k, true
	}
	return nil, nil, false
}

func readTree(root string) map[string][]byte {
	files := map[string][]byte{}
	_ = fs.WalkDir(assets, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		files[strings.TrimPrefix(p, root+"/")] = data
		return nil
	})
	return files
}
