package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes workflows: manifests, Kustomize trees and Helm charts (plus
// the Dockerfiles of locally built images) edited through the daemon's
// /kubernetes endpoints or as code, deployed to a local k3s cluster.

const groupKubernetes = "Kubernetes"

func registerKubeTools(r *registry, t *tools) {
	r.group = groupKubernetes
	addTool(r, "list_kubernetes_catalog", "List Kubernetes palette", "List the Kubernetes palette: starters (deployment-service, statefulset, daemonset, job, cronjob, ingress, httproute, hpa, pdb, configmap, secret, networkpolicy, serviceaccount-role), image:<id> entries (a Deployment or StatefulSet + Service from the curated Docker images, with probes, resources, claims and a Secret for credentials) and chart:<id> entries (pinned Helm charts: Bitnami postgresql/redis/rabbitmq/mongodb/mysql/keycloak, ingress-nginx, traefik, cert-manager, external-secrets, kube-prometheus-stack, grafana, argo-cd, opentelemetry-collector). Pass the id to add_kubernetes_object.", t.listKubeCatalog)
	addTool(r, "get_kubernetes", "Get Kubernetes files", "Show a Kubernetes workflow's sources (manifests | kustomize | helm, path, release, namespace, environments), image builds and the content of its manifests, kustomizations, values files and chart templates; rendered=true adds the rendered YAML of every source for an environment (what deploy applies). Edit the files with apply_terraform_code (it is kind-aware) or the object tools.", t.getKube)
	addTool(r, "add_kubernetes_object", "Add Kubernetes object", "Add objects to a Kubernetes workflow: catalog_id from list_kubernetes_catalog (starters and images append YAML documents to a manifests/kustomize source; charts add a helm source with the pinned chart vendored), or yaml (one or more documents you write). name names the objects, namespace places them (default: the source's). Returns the node ids added and the compact workflow with the best-practice findings the change introduced.", t.addKubeObject)
	addTool(r, "update_kubernetes_object", "Update Kubernetes object", "Change fields of an object (node id \"Deployment/shop/api\"): set {path: value} and unset [path]; paths like spec.replicas, spec.template.spec.containers[name=api].image, metadata.labels[\"app.kubernetes.io/name\"]. Each field is written where it comes from (the manifest, a Kustomize patch or images/replicas transformer, the Helm values key the template reads, or a literal in a template) keeping comments and formatting; get_kubernetes_fields says where and why a field is read-only.", t.updateKubeObject)
	addTool(r, "remove_kubernetes_object", "Remove Kubernetes object", "Remove an object's document (and its kustomization resources entry when the file becomes empty). Helm objects: only when their template renders just that object.", t.removeKubeObject)
	addTool(r, "get_kubernetes_fields", "Get Kubernetes fields", "List every field of an object with its value, where it is written (file:line, via manifest | patch | kustomization | values | template, the Helm values keys) and whether update_kubernetes_object can change it (else the reason).", t.getKubeFields)
	addTool(r, "connect_kubernetes", "Connect Kubernetes objects", "Wire objects. Without recipe_id/kind it lists the options from from to to. Recipes: expose (workload → new Service; omit to), selects (Service → workload: selector set to its pod labels), route (Ingress → Service: add a rule; host, path, port), envFrom / volume (workload → ConfigMap, Secret or PVC), env (workload → Service: <SVC>_URL variable; key), serviceaccount (workload → ServiceAccount), scale (HPA → workload, or workload → new HPA).", t.connectKube)
	addTool(r, "review_kubernetes", "Review Kubernetes", "Check a Kubernetes workflow against Kubernetes best practices for its active environment and return the findings grouped by severity, each with file:line, the field, why it matters, how to fix it and a docs link (warnings: missing requests, readiness probes, latest/untagged or pulled local images, literal secrets in env or ConfigMaps, privileged/host access, selectors matching no pods, Ingress backends and references to missing objects, deprecated APIs, autoscalers without requests, PodDisruptionBudgets that block every eviction; in production-like environments single replicas, replicas not spread over nodes and rollouts that stop every pod; info: memory limits, liveness probes, non-root, storage classes, namespaces, recommended app.kubernetes.io labels), plus the opt-in rules (hardening, NetworkPolicies, default namespace, namespace quotas, image digests). Schema errors (Kubernetes 1.36 OpenAPI: unknown fields, wrong types such as unquoted numbers in env values) are diagnostics of get_workflow. Call it before finishing any Kubernetes change; mutating tools already return the findings they introduced.", t.reviewKube)
	addTool(r, "get_kubernetes_logs", "Get Kubernetes logs", "Recent log lines of a workload's pods (workload node id, e.g. \"Deployment/shop/api\") or of one pod; container picks one container (default: all).", t.getKubeLogs)
	addTool(r, "restart_workload", "Restart workload", "Rollout-restart a deployed workload (node id) or, without workload, every Deployment, StatefulSet and DaemonSet of the workflow; waits for the rollout.", t.restartWorkload)
	addTool(r, "kubernetes_cluster", "Kubernetes cluster", "Stop (keeps everything), start or delete the workflow's local cluster (nimbo-k8s-<id>; delete removes the cluster, its volumes and loaded images). deploy creates or starts it as needed.", t.kubeCluster)
}

// ---------------------------------------------------------------------------
// Views

type kubeObjectView struct {
	ID       string   `json:"id"`
	At       string   `json:"at,omitempty"`
	Source   string   `json:"source,omitempty"`
	Replicas *int     `json:"replicas,omitempty"`
	Images   []string `json:"images,omitempty"`
	Ports    []string `json:"ports,omitempty"`
	Probes   []string `json:"probes,omitempty"`
	Requests []string `json:"requests,omitempty"`
	Env      []string `json:"env,omitempty"`
	Mounts   []string `json:"mounts,omitempty"`
	Routes   []string `json:"routes,omitempty"`
	Selector string   `json:"selector,omitempty"`
	Scale    string   `json:"scale,omitempty"`
	Storage  string   `json:"storage,omitempty"`
	Data     []string `json:"data,omitempty"`
	ReadOnly bool     `json:"read_only,omitempty"`
}

type kubeSourceView struct {
	Index        int               `json:"index"`
	Kind         string            `json:"kind"`
	Path         string            `json:"path"`
	Release      string            `json:"release,omitempty"`
	Namespace    string            `json:"namespace,omitempty"`
	Environments map[string]string `json:"environments,omitempty"`
	Chart        string            `json:"chart,omitempty"`
	Objects      int               `json:"objects"`
	Error        string            `json:"error,omitempty"`
}

type kubeView struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	Kind         string           `json:"kind"`
	Cluster      string           `json:"cluster"`
	Environment  string           `json:"environment,omitempty"`
	Environments []string         `json:"environments,omitempty"`
	Sources      []kubeSourceView `json:"sources"`
	Builds       []string         `json:"builds,omitempty"`
	Objects      []kubeObjectView `json:"objects"`
	Edges        []string         `json:"edges,omitempty"`
	Diagnostics  []string         `json:"diagnostics,omitempty"`
	Review       string           `json:"review,omitempty"`
	// BestPractices are the findings the change introduced (mutating tools).
	BestPractices []bpView `json:"best_practices,omitempty"`
}

func quantities(q model.KubeQuantities) string {
	return strings.Trim(strings.Join([]string{orNone(q.CPU), orNone(q.Memory)}, "/"), "/")
}

func orNone(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func viewKube(wf *model.Workflow) kubeView {
	v := kubeView{ID: wf.ID, Name: wf.Name, Description: wf.Description, Kind: wf.Kind, Cluster: model.KubeClusterName(wf.ID), Sources: []kubeSourceView{}, Objects: []kubeObjectView{}}
	for _, e := range wf.Environments {
		v.Environments = append(v.Environments, e.Name)
	}
	if k := wf.Kubernetes; k != nil {
		v.Environment = k.Environment
		for i, s := range k.Sources {
			sv := kubeSourceView{Index: i, Kind: s.Kind, Path: s.Path, Release: s.Release, Namespace: s.Namespace, Environments: s.EnvPaths, Objects: s.Objects, Error: s.Error}
			if s.Chart != nil {
				sv.Chart = s.Chart.Name + " " + s.Chart.Version
			}
			v.Sources = append(v.Sources, sv)
		}
		for _, b := range k.Builds {
			v.Builds = append(v.Builds, b.Image+" ← "+strings.TrimSuffix(b.Context+"/"+b.Dockerfile, "/"))
		}
		for _, n := range k.Graph.Nodes {
			v.Objects = append(v.Objects, viewKubeNode(n))
		}
		for _, e := range k.Graph.Edges {
			label := e.Kind
			if e.Label != "" {
				label += " " + e.Label
			}
			v.Edges = append(v.Edges, e.Source+" -> "+e.Target+" ("+label+")")
		}
	}
	for _, d := range wf.Diagnostics {
		if isBestPractice(d) {
			continue // review_kubernetes lists them; mutations return the new ones
		}
		s := fmt.Sprintf("%s %s", d.Severity, d.Code)
		if at := findingAt(d); at != "" {
			s += " " + at
		}
		s += ": " + d.Message
		if d.Fix != "" {
			s += " — fix: " + d.Fix
		}
		v.Diagnostics = append(v.Diagnostics, s)
	}
	v.Review = reviewSummary(wf)
	return v
}

func viewKubeNode(n model.KubeNode) kubeObjectView {
	ov := kubeObjectView{ID: n.ID, Source: fmt.Sprintf("%d %s", n.Source, n.SourcePath), ReadOnly: !n.Editable, Data: n.Data}
	if n.File != "" {
		ov.At = fmt.Sprintf("%s:%d", n.File, n.Line)
	}
	if w := n.Workload; w != nil {
		ov.Replicas = w.Replicas
		for _, c := range slices.Concat(w.InitContainers, w.Containers) {
			ov.Images = append(ov.Images, c.Name+"="+c.Image)
			for _, p := range c.Ports {
				ov.Ports = append(ov.Ports, strings.TrimPrefix(p.Name+":", ":")+strconv.Itoa(p.ContainerPort))
			}
			probes := []string{}
			for name, p := range map[string]*model.KubeProbe{"readiness": c.Probes.Readiness, "liveness": c.Probes.Liveness, "startup": c.Probes.Startup} {
				if p != nil {
					probes = append(probes, name+" "+p.Type+" "+strings.TrimSpace(p.Path+" "+p.Port))
				}
			}
			sort.Strings(probes)
			if len(probes) > 0 {
				ov.Probes = append(ov.Probes, c.Name+": "+strings.Join(probes, ", "))
			}
			ov.Requests = append(ov.Requests, fmt.Sprintf("%s: requests %s limits %s", c.Name, quantities(c.Resources.Requests), quantities(c.Resources.Limits)))
			for _, e := range c.Env {
				switch {
				case e.From != nil:
					ov.Env = append(ov.Env, e.Name+" <- "+e.From.Kind+" "+strings.Trim(e.From.Name+"/"+e.From.Key, "/"))
				case e.Secret || e.Value == nil:
					ov.Env = append(ov.Env, e.Name+"=<redacted>")
				default:
					ov.Env = append(ov.Env, e.Name+"="+*e.Value)
				}
			}
			for _, e := range c.EnvFrom {
				ov.Env = append(ov.Env, "envFrom "+e.Kind+" "+e.Name)
			}
			for _, m := range c.VolumeMounts {
				ov.Mounts = append(ov.Mounts, m.Name+" -> "+m.MountPath)
			}
		}
		for _, t := range w.VolumeClaimTemplates {
			ov.Mounts = append(ov.Mounts, "claim template "+t.Name+" "+t.Size+" "+t.StorageClass)
		}
		if len(w.Selector) > 0 {
			ov.Selector = labelsString(w.Selector)
		}
		if w.Schedule != "" {
			ov.Scale = "schedule " + w.Schedule
		}
	}
	if s := n.Service; s != nil {
		for _, p := range s.Ports {
			ov.Ports = append(ov.Ports, fmt.Sprintf("%s%d->%s", strings.TrimPrefix(p.Name+":", ":"), p.Port, p.TargetPort))
		}
		ov.Selector = labelsString(s.Selector)
		if s.Unmatched {
			ov.Selector += " (matches no pods)"
		}
	}
	for _, r := range n.Routes {
		ov.Routes = append(ov.Routes, fmt.Sprintf("%s%s -> %s:%s", r.Host, r.Path, r.Service, r.Port))
	}
	if s := n.Scaler; s != nil {
		ov.Scale = fmt.Sprintf("%s %d-%d", s.Target, s.MinReplicas, s.MaxReplicas)
		if s.CPU > 0 {
			ov.Scale += fmt.Sprintf(" cpu %d%%", s.CPU)
		}
	}
	if d := n.Disruption; d != nil {
		ov.Scale = strings.TrimSpace("minAvailable " + d.MinAvailable + " maxUnavailable " + d.MaxUnavailable)
		ov.Selector = labelsString(d.Selector)
	}
	if s := n.Storage; s != nil {
		ov.Storage = strings.TrimSpace(s.Size + " " + s.StorageClass + " " + strings.Join(s.AccessModes, ","))
	}
	return ov
}

func labelsString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = k + "=" + m[k]
	}
	return strings.Join(keys, ",")
}

// kubeResult is the view of a Kubernetes workflow after a change, with the
// best-practice findings the change introduced.
func kubeResult(before, after *model.Workflow) kubeView {
	v := viewKube(after)
	v.BestPractices = bpDelta(before, after)
	return v
}

// kubeWorkflow resolves a workflow that must be a Kubernetes one.
func (t *tools) kubeWorkflow(ctx context.Context, ref string) (*model.Workflow, error) {
	wf, err := t.c.workflow(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !wf.IsKubernetes() {
		return nil, fmt.Errorf("%s is a %s workflow; this tool is for Kubernetes workflows (create_workflow kind \"kubernetes\")", wf.Name, model.KindLabel(wf.Kind))
	}
	return wf, nil
}

func kubePath(wf *model.Workflow, rest string) string {
	return "/workflows/" + pathEscape(wf.ID) + "/kubernetes" + rest
}

// ---------------------------------------------------------------------------
// Tools

type listKubeCatalogIn struct {
	Query    string `json:"query,omitempty" jsonschema:"filter by text (id, label, description, image, chart)"`
	Group    string `json:"group,omitempty" jsonschema:"starter, image or chart"`
	Category string `json:"category,omitempty" jsonschema:"starters: workload, network, config, autoscaling, rbac; images and charts: the Docker catalog categories (database, cache, messaging, web, observability…)"`
}

func (t *tools) listKubeCatalog(ctx context.Context, in listKubeCatalogIn) (any, error) {
	var items []map[string]any
	if err := t.c.get(ctx, "/catalog/kubernetes", &items); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := []map[string]any{}
	for _, it := range items {
		if in.Group != "" && it["group"] != in.Group || in.Category != "" && it["category"] != in.Category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(fmt.Sprint(it["id"], " ", it["label"], " ", it["description"], " ", it["image"], " ", it["chart"])), q) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

type getKubeIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Rendered    bool   `json:"rendered,omitempty" jsonschema:"also return the rendered YAML of every source"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to render (default: the active one; \"\" base)"`
}

// kubeSourceFile kinds whose content get_kubernetes returns.
var kubeSourceFile = map[model.FileKind]bool{
	model.FileManifest: true, model.FileKustomization: true, model.FileChart: true, model.FileValues: true, model.FileHelmTemplate: true,
}

func (t *tools) getKube(ctx context.Context, in getKubeIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var list struct {
		Files []model.WorkflowFile `json:"files"`
	}
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/files", &list); err != nil {
		return nil, err
	}
	files := map[string]string{}
	for _, f := range list.Files {
		if !kubeSourceFile[f.Kind] && f.Kind != model.FileEnv || f.Binary {
			continue
		}
		var fc model.WorkflowFileContent
		if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/files/content?path="+url.QueryEscape(f.Path), &fc); err != nil {
			return nil, err
		}
		if f.Kind == model.FileEnv {
			fc.Content = redactEnv(fc.Content)
		}
		files[f.Path] = fc.Content
	}
	out := map[string]any{"files": files, "workflow": viewKube(wf)}
	if in.Rendered {
		env := in.Environment
		if env == "" {
			env = wf.ActiveEnvironment
		}
		var rendered map[string]any
		if err := t.c.get(ctx, kubePath(wf, "/render?environment="+url.QueryEscape(env)), &rendered); err != nil {
			return nil, err
		}
		out["rendered"] = rendered
	}
	return out, nil
}

type addKubeObjectIn struct {
	Workflow  string   `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	CatalogID string   `json:"catalog_id,omitempty" jsonschema:"palette id from list_kubernetes_catalog (deployment-service, image:postgres, chart:redis…)"`
	YAML      string   `json:"yaml,omitempty" jsonschema:"one or more YAML documents to add (instead of catalog_id)"`
	Name      string   `json:"name,omitempty" jsonschema:"catalog entries: name of the objects (DNS label; default from the entry)"`
	Namespace string   `json:"namespace,omitempty" jsonschema:"catalog entries: namespace of the objects (default: the source's); charts: the release namespace"`
	Source    *int     `json:"source,omitempty" jsonschema:"index of the source to add to (default: the first manifests or kustomize source)"`
	File      string   `json:"file,omitempty" jsonschema:"yaml: file to append to (default <source dir>/<kind>-<name>.yaml)"`
	X         *float64 `json:"x,omitempty"`
	Y         *float64 `json:"y,omitempty"`
}

func (t *tools) addKubeObject(ctx context.Context, in addKubeObjectIn) (any, error) {
	if (in.CatalogID == "") == (in.YAML == "") {
		return nil, errors.New("pass catalog_id or yaml")
	}
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeIDs  []string       `json:"nodeIds"`
	}
	if in.CatalogID != "" {
		body := map[string]any{"itemId": in.CatalogID, "name": in.Name, "namespace": in.Namespace, "source": in.Source}
		if in.X != nil && in.Y != nil {
			body["position"] = model.Position{X: *in.X, Y: *in.Y}
		}
		if err := t.c.do(ctx, http.MethodPost, kubePath(wf, "/catalog"), body, &res); err != nil {
			return nil, err
		}
	} else {
		body := map[string]any{"yaml": in.YAML, "file": in.File}
		if in.Source != nil {
			body["source"] = *in.Source
		}
		if err := t.c.do(ctx, http.MethodPost, kubePath(wf, "/objects"), body, &res.Workflow); err != nil {
			return nil, err
		}
		res.NodeIDs = newNodes(wf, &res.Workflow)
	}
	return map[string]any{"nodes": res.NodeIDs, "workflow": kubeResult(wf, &res.Workflow)}, nil
}

// newNodes are the graph nodes of after that before did not have.
func newNodes(before, after *model.Workflow) []string {
	had := map[string]bool{}
	if before.Kubernetes != nil {
		for _, n := range before.Kubernetes.Graph.Nodes {
			had[n.ID] = true
		}
	}
	out := []string{}
	if after.Kubernetes != nil {
		for _, n := range after.Kubernetes.Graph.Nodes {
			if !had[n.ID] {
				out = append(out, n.ID)
			}
		}
	}
	return out
}

type updateKubeObjectIn struct {
	Workflow string         `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string         `json:"node" jsonschema:"object node id, e.g. Deployment/shop/api"`
	Set      map[string]any `json:"set,omitempty" jsonschema:"field path -> value (YAML/JSON value: number, string, list, object)"`
	Unset    []string       `json:"unset,omitempty" jsonschema:"field paths to delete"`
}

func (t *tools) updateKubeObject(ctx context.Context, in updateKubeObjectIn) (any, error) {
	if len(in.Set) == 0 && len(in.Unset) == 0 {
		return nil, errors.New("set or unset is required")
	}
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPatch, kubePath(wf, "/objects"), map[string]any{"node": in.Node, "set": in.Set, "unset": in.Unset}, &out); err != nil {
		return nil, err
	}
	return kubeResult(wf, &out), nil
}

type kubeNodeIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string `json:"node" jsonschema:"object node id, e.g. Deployment/shop/api"`
}

func (t *tools) removeKubeObject(ctx context.Context, in kubeNodeIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodDelete, kubePath(wf, "/objects?node="+url.QueryEscape(in.Node)), nil, &out); err != nil {
		return nil, err
	}
	return kubeResult(wf, &out), nil
}

func (t *tools) getKubeFields(ctx context.Context, in kubeNodeIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.KubeFields
	err = t.c.get(ctx, kubePath(wf, "/fields?node="+url.QueryEscape(in.Node)), &out)
	return out, err
}

type connectKubeIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	From     string `json:"from" jsonschema:"node id of the object that uses to (a workload, Service, Ingress or HPA)"`
	To       string `json:"to,omitempty" jsonschema:"node id it connects to (omit for expose)"`
	RecipeID string `json:"recipe_id,omitempty" jsonschema:"an option id from the listing"`
	Kind     string `json:"kind,omitempty" jsonschema:"expose, selects, route, envFrom, volume, env, serviceaccount or scale"`
	Port     string `json:"port,omitempty" jsonschema:"expose/route: port number"`
	Path     string `json:"path,omitempty" jsonschema:"route: URL path prefix; volume: mount path"`
	Host     string `json:"host,omitempty" jsonschema:"route: host name (e.g. shop.localhost)"`
	Key      string `json:"key,omitempty" jsonschema:"env: variable name"`
}

func (t *tools) connectKube(ctx context.Context, in connectKubeIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if in.Kind == "" && in.RecipeID == "" {
		var options []map[string]any
		err := t.c.get(ctx, kubePath(wf, "/connect?from="+url.QueryEscape(in.From)+"&to="+url.QueryEscape(in.To)), &options)
		return map[string]any{"options": options, "hint": "call connect_kubernetes again with recipe_id (or kind)"}, err
	}
	body := map[string]any{"from": in.From, "to": in.To, "recipeId": in.RecipeID, "kind": in.Kind, "path": in.Path, "host": in.Host, "key": in.Key}
	if in.Port != "" {
		n, err := strconv.Atoi(in.Port)
		if err != nil {
			return nil, fmt.Errorf("port must be a number (the Service port), not %q", in.Port)
		}
		body["port"] = n
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPost, kubePath(wf, "/connect"), body, &out); err != nil {
		return nil, err
	}
	return kubeResult(wf, &out), nil
}

func (t *tools) reviewKube(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	return t.reviewKubeWorkflow(ctx, wf)
}

func (t *tools) reviewKubeWorkflow(ctx context.Context, wf *model.Workflow) (any, error) {
	res, err := t.ruleReview(ctx, wf, k8sPrefix)
	if err != nil {
		return nil, err
	}
	if res.Summary != "" {
		return res, nil
	}
	env := "the base"
	if wf.Kubernetes != nil && wf.Kubernetes.Environment != "" {
		env = "environment " + wf.Kubernetes.Environment
	}
	res.findings(wf)
	if len(res.Warnings)+len(res.Info) == 0 {
		res.Summary = "No best-practice findings for " + env + ": the manifests follow Nimbo's recommended Kubernetes practices."
	} else {
		res.Summary = countFindings(len(res.Warnings), len(res.Info)) + " for " + env
		res.Next = "Fix warnings first (requests, readiness probes, pinned tags, secrets in Secrets, selectors and backends that match, privileges), then info. Change fields with update_kubernetes_object (it writes the manifest, patch or Helm values key the field comes from), add missing objects with add_kubernetes_object, wire them with connect_kubernetes, or edit files with get_kubernetes + apply_terraform_code; then call review_kubernetes again. Review every environment that matters (update_workflow environment switches the active one: production-like ones also check replicas, PodDisruptionBudgets, topology spread and rollout strategy), or call production_readiness for every environment at once. Findings never block deploys; leave one only when the user wants it that way."
	}
	if len(wf.Environments) > 0 {
		res.Next = strings.TrimSpace(res.Next + " Environments: " + strings.Join(envNames(wf), ", ") + ".")
	}
	return res, nil
}

func envNames(wf *model.Workflow) []string {
	var out []string
	for _, e := range wf.Environments {
		out = append(out, e.Name)
	}
	return out
}

type kubeLogsIn struct {
	Workflow  string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Workload  string `json:"workload,omitempty" jsonschema:"workload node id, e.g. Deployment/shop/api"`
	Pod       string `json:"pod,omitempty" jsonschema:"one pod name (instead of workload)"`
	Container string `json:"container,omitempty"`
	Tail      int    `json:"tail,omitempty" jsonschema:"lines per container (default 100)"`
}

func (t *tools) getKubeLogs(ctx context.Context, in kubeLogsIn) (any, error) {
	if in.Workload == "" && in.Pod == "" {
		return nil, errors.New("workload or pod is required")
	}
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	tail := in.Tail
	if tail <= 0 {
		tail = 100
	}
	q := url.Values{"workload": {in.Workload}, "pod": {in.Pod}, "container": {in.Container}, "tail": {strconv.Itoa(tail)}}
	var res struct {
		Lines []model.KubeLogLine `json:"lines"`
	}
	if err := t.c.get(ctx, kubePath(wf, "/logs?"+q.Encode()), &res); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(res.Lines))
	for _, l := range res.Lines {
		out = append(out, l.Pod+"/"+l.Container+": "+l.Line.Text)
	}
	return map[string]any{"lines": out}, nil
}

type restartWorkloadIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Workload    string `json:"workload,omitempty" jsonschema:"workload node id; omit for every Deployment, StatefulSet and DaemonSet"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"max seconds to wait (default 300)"`
}

func (t *tools) restartWorkload(ctx context.Context, in restartWorkloadIn) (any, error) {
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	return t.kubeRun(ctx, wf, map[string]any{"action": model.ActionRestart, "workload": in.Workload}, in.WaitSeconds)
}

type kubeClusterIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Action   string `json:"action" jsonschema:"stop, start or delete"`
}

func (t *tools) kubeCluster(ctx context.Context, in kubeClusterIn) (any, error) {
	actions := map[string]model.RunAction{"stop": model.ActionClusterStop, "start": model.ActionClusterStart, "delete": model.ActionClusterDelete}
	a, ok := actions[in.Action]
	if !ok {
		return nil, fmt.Errorf("action must be stop, start or delete, not %q", in.Action)
	}
	wf, err := t.kubeWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	return t.kubeRun(ctx, wf, map[string]any{"action": a}, 0)
}

// kubeRunView is a Kubernetes run with the cluster's status after it.
type kubeRunView struct {
	runView
	Status *kubeStatusView `json:"status,omitempty"`
}

// kubeRun starts a run and waits for it, then reports the cluster status.
func (t *tools) kubeRun(ctx context.Context, wf *model.Workflow, body map[string]any, waitSeconds int) (any, error) {
	var run model.Run
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/runs", body, &run); err != nil {
		return nil, err
	}
	wait := 600
	if waitSeconds > 0 {
		wait = waitSeconds
	}
	v, err := t.waitRun(ctx, wf.ID, run.ID, time.Duration(wait)*time.Second)
	if err != nil {
		return v, err
	}
	st, err := t.kubeStatus(ctx, wf)
	if err != nil {
		return v, nil
	}
	return kubeRunView{runView: v, Status: st}, nil
}

// kubeStatusView is the compact live status of a Kubernetes workflow.
type kubeStatusView struct {
	Cluster    string   `json:"cluster"`
	State      string   `json:"state"`
	Kubeconfig string   `json:"kubeconfig,omitempty"`
	Workloads  []string `json:"workloads"`
	Forwards   []string `json:"forwards,omitempty"`
	Ingresses  []string `json:"ingresses,omitempty"`
	Problems   []string `json:"problems,omitempty"`
	Events     []string `json:"warning_events,omitempty"`
	Message    string   `json:"message,omitempty"`
}

func (t *tools) kubeStatus(ctx context.Context, wf *model.Workflow) (*kubeStatusView, error) {
	var st model.KubeStatus
	if err := t.c.get(ctx, kubePath(wf, "/status"), &st); err != nil {
		return nil, err
	}
	v := &kubeStatusView{Cluster: st.Cluster.Name + " " + string(st.Cluster.State) + strings.TrimSuffix(" "+st.Cluster.Version, " "), State: string(st.State), Kubeconfig: st.Cluster.Kubeconfig, Workloads: []string{}, Message: firstNonEmpty(st.Message, st.Cluster.Message)}
	for _, w := range st.Workloads {
		s := fmt.Sprintf("%s %s %d/%d ready", w.NodeID, w.State, w.Ready, w.Desired)
		if w.Rollout != "" {
			s += " (" + w.Rollout + ")"
		}
		for _, p := range w.Pods {
			if p.Reason != "" || p.Restarts > 0 {
				s += fmt.Sprintf("; pod %s %s restarts %d %s", p.Name, p.Phase, p.Restarts, strings.TrimSpace(p.Reason+" "+p.Message))
			}
		}
		v.Workloads = append(v.Workloads, s)
	}
	for _, f := range st.Forwards {
		s := fmt.Sprintf("%s:%d -> 127.0.0.1:%d", f.NodeID, f.Port, f.HostPort)
		if f.URL != "" {
			s += " " + f.URL
		}
		if !f.Ready {
			s += " (not ready: " + f.Message + ")"
		}
		v.Forwards = append(v.Forwards, s)
	}
	for _, in := range st.Ingresses {
		v.Ingresses = append(v.Ingresses, fmt.Sprintf("%s %s (or %s with Host: %s) -> %s", in.NodeID, in.URL, in.Address, in.Host, in.ServiceNodeID))
	}
	for _, p := range st.Problems {
		s := fmt.Sprintf("%s %s %s: %s", p.Severity, p.Kind, p.NodeID, p.Message)
		if p.Fix != "" {
			s += " — fix: " + p.Fix
		}
		v.Problems = append(v.Problems, s)
	}
	for _, e := range st.Events {
		if e.Type == "Warning" {
			v.Events = append(v.Events, fmt.Sprintf("%s %s/%s: %s (x%d)", e.Reason, e.Kind, e.Name, e.Message, e.Count))
		}
	}
	return v, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}
