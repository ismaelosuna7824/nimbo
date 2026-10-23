// Package filetemplates holds the file templates of the Code tab's
// "New from template…" menu (GET /catalog/file-templates): Dockerfiles and
// .dockerignore files per stack, .env, compose and nginx files for docker
// workflows, Kustomize and Helm files for Kubernetes ones.
package filetemplates

import (
	"cmp"
	"embed"
	"slices"
)

//go:embed all:files
var files embed.FS

// Template is one file template.
type Template struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`  // "docker" | "kubernetes"
	Group       string `json:"group"` // menu group: Dockerfile, .dockerignore, Environment, Compose, Config, Kustomize, Helm, Manifest
	Label       string `json:"label"`
	Description string `json:"description"`
	FileName    string `json:"fileName"` // default name of the new file (the file a snippet goes into)
	Stack       string `json:"stack,omitempty"`
	// Snippet templates are inserted into an existing file (a compose
	// service under services:) instead of creating a new one.
	Snippet bool   `json:"snippet,omitempty"`
	Content string `json:"content"`
}

const (
	kindDocker = "docker"
	kindKube   = "kubernetes"
)

// entry is a template's metadata and the embedded file holding its content.
type entry struct {
	Template
	file string
}

func dockerfile(stack, label, desc string) entry {
	return entry{Template{ID: "dockerfile-" + stack, Kind: kindDocker, Group: "Dockerfile", Label: label, Description: desc, FileName: "Dockerfile", Stack: stack}, "docker/dockerfile/" + stack + ".Dockerfile"}
}

func dockerignore(stack, label, desc string) entry {
	return entry{Template{ID: "dockerignore-" + stack, Kind: kindDocker, Group: ".dockerignore", Label: label, Description: desc, FileName: ".dockerignore", Stack: stack}, "docker/dockerignore/" + stack + ".dockerignore"}
}

var entries = []entry{
	dockerfile("node", "Node.js (npm)", "node:22-alpine, npm ci with a cache mount before the sources, non-root node user."),
	dockerfile("python", "Python (pip)", "python:3.12-slim, pip install with a cache mount before the sources, non-root user."),
	dockerfile("go", "Go", "Multi-stage: static binary built on golang:1.23-alpine, run on distroless as nonroot."),
	dockerfile("java-maven", "Java (Maven)", "Multi-stage: jar built with Maven, run on eclipse-temurin:21-jre-alpine as a non-root user."),
	dockerfile("java-gradle", "Java (Gradle)", "Multi-stage: jar built with Gradle, run on eclipse-temurin:21-jre-alpine as a non-root user."),
	dockerfile("dotnet", ".NET (ASP.NET Core)", "Multi-stage: dotnet publish on the SDK, run on aspnet:8.0-alpine as the app user."),
	dockerfile("rust", "Rust (Cargo)", "Multi-stage: release binary built on rust:1.82-slim, run on distroless cc as nonroot."),
	dockerfile("php", "PHP (Apache + Composer)", "Composer stage for vendor/, php:8.3-apache on port 8080 as www-data."),
	dockerfile("ruby", "Ruby (Bundler)", "Gems built in a stage with compilers, run on ruby:3.3-slim as a non-root user."),
	dockerfile("static", "Static site (NGINX)", "nginx-unprivileged serving the folder on port 8080 as a non-root user."),

	dockerignore("node", "Node.js", "Excludes node_modules, build output, .git and .env files."),
	dockerignore("python", "Python", "Excludes virtualenvs, __pycache__, .git and .env files."),
	dockerignore("go", "Go", "Excludes bin, vendor, .git and .env files."),
	dockerignore("java", "Java (Maven/Gradle)", "Excludes target, build, .gradle, .git and .env files."),
	dockerignore("dotnet", ".NET", "Excludes bin, obj, .git and .env files."),
	dockerignore("rust", "Rust", "Excludes target, .git and .env files."),
	dockerignore("php", "PHP", "Excludes vendor, caches, .git and .env files."),
	dockerignore("ruby", "Ruby", "Excludes .bundle, vendor/bundle, log, tmp, .git and .env files."),
	dockerignore("static", "Static site", "Excludes node_modules, .git and .env files."),

	{Template{ID: "env", Kind: kindDocker, Group: "Environment", Label: ".env", Description: "Example variables for compose interpolation and env_file (keep it out of git).", FileName: ".env"}, "docker/env.env"},
	{Template{ID: "compose-service", Kind: kindDocker, Group: "Compose", Label: "Service (build)", Description: "A service built from this folder: restart, init, healthcheck, limits and rotated logs. Goes under services:.", FileName: "compose.yaml", Snippet: true}, "docker/compose-service.yaml"},
	{Template{ID: "compose-project", Kind: kindDocker, Group: "Compose", Label: "compose.yaml", Description: "A minimal project with one service built from this folder.", FileName: "compose.yaml"}, "docker/compose.yaml"},
	{Template{ID: "nginx-reverse-proxy", Kind: kindDocker, Group: "Config", Label: "nginx.conf (reverse proxy)", Description: "NGINX reverse proxy to app:8080 with a /healthz endpoint.", FileName: "nginx.conf"}, "docker/nginx.conf"},

	{Template{ID: "kustomize-base", Kind: kindKube, Group: "Kustomize", Label: "Base kustomization.yaml", Description: "Resources shared by every environment, with common labels.", FileName: "kustomization.yaml"}, "kubernetes/kustomize-base.yaml"},
	{Template{ID: "kustomize-overlay", Kind: kindKube, Group: "Kustomize", Label: "Overlay kustomization.yaml", Description: "An environment on top of ../../base: namespace, replica patch and image tag.", FileName: "kustomization.yaml"}, "kubernetes/kustomize-overlay.yaml"},
	{Template{ID: "helm-chart", Kind: kindKube, Group: "Helm", Label: "Chart.yaml", Description: "Helm v2 application chart metadata.", FileName: "Chart.yaml"}, "kubernetes/chart.yaml"},
	{Template{ID: "helm-values", Kind: kindKube, Group: "Helm", Label: "values.yaml", Description: "Default values: pinned image, replicas, service, resources, probes and a non-root security context.", FileName: "values.yaml"}, "kubernetes/values.yaml"},
	{Template{ID: "helm-values-dev", Kind: kindKube, Group: "Helm", Label: "values-dev.yaml", Description: "Development overrides: one small replica, debug logs.", FileName: "values-dev.yaml"}, "kubernetes/values-dev.yaml"},
	{Template{ID: "helm-values-prod", Kind: kindKube, Group: "Helm", Label: "values-prod.yaml", Description: "Production overrides: more replicas, a released tag, larger resources.", FileName: "values-prod.yaml"}, "kubernetes/values-prod.yaml"},
	{Template{ID: "k8s-deployment", Kind: kindKube, Group: "Manifest", Label: "Deployment + Service", Description: "A Deployment with probes, resources and a locked-down security context, and its Service.", FileName: "deployment.yaml"}, "kubernetes/deployment.yaml"},
}

var templates = func() []Template {
	out := make([]Template, len(entries))
	for i, e := range entries {
		b, err := files.ReadFile("files/" + e.file)
		if err != nil {
			panic("filetemplates: " + err.Error())
		}
		out[i] = e.Template
		out[i].Content = string(b)
	}
	slices.SortStableFunc(out, func(a, b Template) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Group, b.Group), cmp.Compare(a.Label, b.Label))
	})
	return out
}()

// List returns the file templates sorted by Kind, Group and Label.
func List() []Template { return slices.Clone(templates) }
