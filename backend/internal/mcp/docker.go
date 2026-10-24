package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker workflows: a docker-compose project (compose files, Dockerfiles,
// .env) edited through the daemon's /compose endpoints or as code.

const groupDocker = "Docker"

func registerDockerTools(r *registry, t *tools) {
	r.group = groupDocker
	addTool(r, "list_docker_images", "List Docker images", "List the curated image palette for docker workflows (id, image:tag, category, icon, default ports, env, volumes, healthcheck, connection variables). Pass catalog_id to add_service.", t.listDockerImages)
	addTool(r, "get_compose", "Get compose files", "Show a docker workflow's compose files (in -f order), Dockerfiles and .env (secret values redacted) with their content. Edit them with apply_terraform_code (it is kind-aware) or the service tools.", t.getCompose)
	addTool(r, "add_service", "Add service", "Add a service to a docker workflow: from catalog_id (list_docker_images: image, ports, env with secrets moved to .env, named volume, healthcheck), an image, or build_context (a folder; when it has no Dockerfile a buildable starter is created: node, python, go, static or alpine). Returns the compact docker workflow.", t.addService)
	addTool(r, "update_service", "Update service", "Change a service: set is a partial patch (null deletes a key; lists replace the whole value): image, build {context, dockerfile?, target?}, ports [{host?, container, protocol?, hostIp?}], environment [{key, value?}], volumes [{type?, source?, target, readOnly?}], dependsOn [{service, condition?}], healthcheck {test, interval?, timeout?, retries?, startPeriod?}, restart, replicas, resources {cpus?, memory?, pids?} (deploy.resources.limits; empty fields removed), logging {driver?, maxSize?, maxFile?} (json-file/local rotation options; empty fields removed), labels {name: value} (exactly these), networks [names], command, entrypoint, profiles, user, containerName, expose, envFiles, name (rename; references follow). Hardening: readOnly, privileged, init (true sets read_only/privileged/init, false removes), capDrop, capAdd, securityOpt, tmpfs [absolute paths] (lists), secrets [declared top-level secret names]; secretFromEnv {key, secret?, fileEnv?} (send alone) moves an environment variable into ./secrets/<secret>.txt, declares and grants the secret, removes the variable and, with fileEnv, sets <key>_FILE=/run/secrets/<secret> (postgres, mysql, mariadb and mongo images read it). With an active environment, a key its override file (compose.<env>.yaml) sets is written there (lists tagged !override) and null removes it from the override only; other keys edit the base files, unless scope is \"environment\": then every key goes to the override only (lists tagged !override, null resets the key there; not for name, environment, dependsOn, networks, labels, secrets); resetOverrides [\"image\", \"ports\", \"deploy.replicas\"…] drops keys from the override so the base values apply. Comments and formatting of the files are kept.", t.updateService)
	addTool(r, "remove_service", "Remove service", "Remove a service from a docker workflow (other services' depends_on/links/volumes_from to it are removed too).", t.removeService)
	addTool(r, "connect_services", "Connect services", "Connect service from to to (a service, \"network:<name>\" or \"volume:<name>\"). Without kind and recipe_id it lists the options (depends_on started/healthy, env connection variables from the catalog, shared network, volume mount). kind: depends_on (condition), network (network name; created if missing), volume (target path), env; or recipe_id from the options.", t.connectServices)
	addTool(r, "review_docker", "Review Docker", "Check a docker workflow against Docker best practices and return the findings grouped by severity (warnings: databases published on every interface, secrets in builds, single-stage builds of compiled code, cache-busting layer order, missing .dockerignore for heavy contexts, shell-form CMD, depends_on without service_healthy, anonymous data volumes, docker.sock/privileged; info: pinned tags, slim runtime bases, apt/pip/apk caches, ADD, WORKDIR, healthchecks and their timing, restart policies, log rotation, init for node/python, published ports vs EXPOSE), each with file:line, why it matters, how to fix it and a docs link; plus the opt-in rules the user can turn on in Settings. Call it before finishing any docker change; mutating docker tools already return the findings their change introduced as best_practices. Findings never block runs.", t.reviewDocker)
	addTool(r, "get_docker_metrics", "Get docker metrics", "Performance of a docker workflow (optionally one service): live container usage from docker stats (CPU % of its limit, memory vs limit, network/block I/O, PIDs, last-60 s CPU), startup time (start → healthy), problems (OOM kills, restarts, non-zero exit codes, unhealthy), the latest build of each service (build time, cache hits/misses, image size split into base image and own layers, biggest layers, slowest uncached steps, delta vs the previous build, findings such as an image too big for its stack with the dk_* best-practice rules to apply), and the bottleneck verdict of the latest load test against the workflow.", t.getDockerMetrics)
	registerConvertTool(r, t)
}

type dockerServiceView struct {
	Name        string   `json:"name"`
	Image       string   `json:"image,omitempty"`
	Build       string   `json:"build,omitempty"`
	Stack       []string `json:"stack,omitempty"`
	BaseImages  []string `json:"base_images,omitempty"`
	Ports       []string `json:"ports,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty"`
	Networks    []string `json:"networks,omitempty"`
	Volumes     []string `json:"volumes,omitempty"`
	Environment []string `json:"environment,omitempty"`
	Healthcheck string   `json:"healthcheck,omitempty"`
	Inactive    bool     `json:"inactive,omitempty"`
	// Overrides are the attributes the active environment's override file
	// sets (update_service writes those there; resetOverrides drops them).
	Overrides []string `json:"overrides,omitempty"`
	At        string   `json:"at,omitempty"`
}

type dockerView struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Kind         string   `json:"kind"`
	Project      string   `json:"project"`
	ComposeFiles []string `json:"compose_files"`
	Profiles     []string `json:"profiles,omitempty"`
	// Environment is the active environment the services below are
	// rendered for (its override file is applied after compose_files);
	// Environments are all of them (list_environments for details).
	Environment  string              `json:"environment,omitempty"`
	Environments []string            `json:"environments,omitempty"`
	Services     []dockerServiceView `json:"services"`
	Networks     []string            `json:"networks,omitempty"`
	Volumes      []string            `json:"volumes,omitempty"`
	Edges        []string            `json:"edges,omitempty"`
	Unresolved   []string            `json:"unresolved_variables,omitempty"`
	Diagnostics  []string            `json:"diagnostics,omitempty"`
	// Review summarizes the best-practice findings (review_docker).
	Review string `json:"review,omitempty"`
	// BestPractices are the findings the change introduced (mutating tools).
	BestPractices []bpView `json:"best_practices,omitempty"`
}

func viewDocker(wf *model.Workflow) dockerView {
	v := dockerView{ID: wf.ID, Name: wf.Name, Description: wf.Description, Kind: wf.Kind, Services: []dockerServiceView{}}
	if wf.Compose != nil {
		c := wf.Compose
		v.Project, v.ComposeFiles, v.Profiles, v.Unresolved = c.Project, c.Files, c.Profiles, c.Unresolved
		v.Environment = c.Environment
		for _, e := range c.Environments {
			v.Environments = append(v.Environments, e.Name)
		}
		for _, n := range c.Graph.Nodes {
			switch n.Kind {
			case model.ComposeNetwork:
				v.Networks = append(v.Networks, n.Name)
			case model.ComposeVolume:
				v.Volumes = append(v.Volumes, n.Name)
			case model.ComposeExternal:
				v.Networks = append(v.Networks, n.Name+" (external "+n.ExternalKind+")")
			case model.ComposeService:
				s := n.Service
				sv := dockerServiceView{Name: n.Name, Inactive: n.Inactive, Stack: s.Stack, BaseImages: s.BaseImages, Networks: s.Networks, Overrides: s.Overrides}
				if n.File != "" {
					sv.At = fmt.Sprintf("%s:%d", n.File, n.Line)
				}
				if s.Image != nil {
					sv.Image = s.Image.Ref
				}
				if s.Build != nil {
					sv.Build = s.Build.Context + " (" + s.Build.Dockerfile + ")"
				}
				for _, p := range s.Ports {
					sv.Ports = append(sv.Ports, strings.Trim(p.Host+":"+p.Container, ":")+protoSuffix(p.Protocol))
				}
				for _, d := range s.DependsOn {
					sv.DependsOn = append(sv.DependsOn, d.Service+" ("+strings.TrimPrefix(d.Condition, "service_")+")")
				}
				for _, m := range s.Volumes {
					sv.Volumes = append(sv.Volumes, strings.TrimPrefix(m.Source+":"+m.Target, ":")+" ("+m.Type+")")
				}
				for _, e := range s.Environment {
					switch {
					case e.Secret:
						sv.Environment = append(sv.Environment, e.Key+"=<redacted>")
					case e.Value != nil:
						sv.Environment = append(sv.Environment, e.Key+"="+*e.Value)
					default:
						sv.Environment = append(sv.Environment, e.Key)
					}
				}
				if s.Healthcheck != nil {
					sv.Healthcheck = s.Healthcheck.Test
				}
				v.Services = append(v.Services, sv)
			}
		}
		for _, e := range c.Graph.Edges {
			label := e.Kind
			if e.Label != "" {
				label += " " + e.Label
			}
			v.Edges = append(v.Edges, e.Source+" -> "+e.Target+" ("+label+")")
		}
	}
	for _, d := range wf.Diagnostics {
		if isBestPractice(d) {
			continue // review_docker lists them; mutations return the new ones
		}
		s := fmt.Sprintf("%s %s", d.Severity, d.Code)
		if d.File != "" {
			s += fmt.Sprintf(" %s:%d", d.File, d.Line)
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

// dockerResult is the view of a docker workflow after a change, with the
// best-practice findings the change introduced.
func dockerResult(before, after *model.Workflow) dockerView {
	v := viewDocker(after)
	v.BestPractices = bpDelta(before, after)
	return v
}

func protoSuffix(p string) string {
	if p == "" || p == "tcp" {
		return ""
	}
	return "/" + p
}

type listDockerImagesIn struct {
	Query    string `json:"query,omitempty" jsonschema:"filter by text (id, image, label, description)"`
	Category string `json:"category,omitempty" jsonschema:"web, gateway, language, database, cache, search, messaging, storage, observability, auth, workflow, ai, devtools, apps, cms, network or tools"`
}

func (t *tools) listDockerImages(ctx context.Context, in listDockerImagesIn) (any, error) {
	var list []model.DockerImage
	if err := t.c.get(ctx, "/catalog/docker/images", &list); err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := []model.DockerImage{}
	for _, im := range list {
		if in.Category != "" && im.Category != in.Category {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(im.ID+" "+im.Image+" "+im.Label+" "+im.Description), q) {
			continue
		}
		out = append(out, im)
	}
	return out, nil
}

// dockerWorkflow resolves a workflow that must be a docker one.
func (t *tools) dockerWorkflow(ctx context.Context, ref string) (*model.Workflow, error) {
	wf, err := t.c.workflow(ctx, ref)
	if err != nil {
		return nil, err
	}
	if !wf.IsDocker() {
		return nil, fmt.Errorf("%s is a Terraform workflow; this tool is for docker workflows (create_workflow kind \"docker\")", wf.Name)
	}
	return wf, nil
}

func (t *tools) getCompose(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
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
		if f.Kind != model.FileCompose && f.Kind != model.FileDockerfile && f.Kind != model.FileEnv || f.Binary {
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
	return map[string]any{"compose_files": wf.Compose.Files, "files": files, "workflow": viewDocker(wf)}, nil
}

// redactEnv hides the values of secret-looking keys of a .env file.
func redactEnv(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		k, _, ok := strings.Cut(l, "=")
		up := strings.ToUpper(strings.TrimSpace(k))
		if ok && !strings.HasPrefix(strings.TrimSpace(l), "#") && (strings.Contains(up, "PASS") || strings.Contains(up, "SECRET") || strings.Contains(up, "TOKEN") || strings.Contains(up, "KEY")) {
			lines[i] = k + "=<redacted>"
		}
	}
	return strings.Join(lines, "\n")
}

// applyFileChanges writes files of a docker or Kubernetes workflow (PUT
// /terraform changes): "" deletes a file.
func (t *tools) applyFileChanges(ctx context.Context, wf *model.Workflow, files map[string]string) (any, error) {
	changes := map[string]*string{}
	for p, c := range files {
		if c == "" {
			changes[p] = nil
			continue
		}
		c := c
		changes[p] = &c
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID)+"/terraform", map[string]any{"changes": changes}, &out); err != nil {
		return nil, err
	}
	if out.IsKubernetes() {
		return kubeResult(wf, &out), nil
	}
	return dockerResult(wf, &out), nil
}

type addServiceIn struct {
	Workflow     string   `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name         string   `json:"name,omitempty" jsonschema:"service name (default from the image or folder)"`
	CatalogID    string   `json:"catalog_id,omitempty" jsonschema:"image id from list_docker_images"`
	Image        string   `json:"image,omitempty" jsonschema:"image reference, e.g. nginx:1.27-alpine"`
	BuildContext string   `json:"build_context,omitempty" jsonschema:"folder to build (relative to the workflow files)"`
	Dockerfile   string   `json:"dockerfile,omitempty"`
	Starter      string   `json:"starter,omitempty" jsonschema:"when the folder has no Dockerfile: node, python, go, static or alpine (default)"`
	X            *float64 `json:"x,omitempty"`
	Y            *float64 `json:"y,omitempty"`
}

func (t *tools) addService(ctx context.Context, in addServiceIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"name": in.Name, "catalogId": in.CatalogID, "image": in.Image}
	if in.BuildContext != "" {
		body["build"] = map[string]string{"context": in.BuildContext, "dockerfile": in.Dockerfile, "starter": in.Starter}
	}
	if in.X != nil && in.Y != nil {
		body["position"] = model.Position{X: *in.X, Y: *in.Y}
	}
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/compose/services", body, &res); err != nil {
		return nil, err
	}
	return map[string]any{"node": res.NodeID, "workflow": dockerResult(wf, &res.Workflow)}, nil
}

type updateServiceIn struct {
	Workflow string         `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Service  string         `json:"service" jsonschema:"service name"`
	Set      map[string]any `json:"set" jsonschema:"partial patch; null deletes a key"`
}

func (t *tools) updateService(ctx context.Context, in updateServiceIn) (any, error) {
	if len(in.Set) == 0 {
		return nil, errors.New("set is required")
	}
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPatch, "/workflows/"+pathEscape(wf.ID)+"/compose/services/"+pathEscape(in.Service), in.Set, &out); err != nil {
		return nil, err
	}
	return dockerResult(wf, &out), nil
}

type serviceRefIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Service  string `json:"service" jsonschema:"service name"`
}

func (t *tools) removeService(ctx context.Context, in serviceRefIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodDelete, "/workflows/"+pathEscape(wf.ID)+"/compose/services/"+pathEscape(in.Service), nil, &out); err != nil {
		return nil, err
	}
	return dockerResult(wf, &out), nil
}

type connectServicesIn struct {
	Workflow  string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	From      string `json:"from" jsonschema:"the service that uses to"`
	To        string `json:"to" jsonschema:"a service, network:<name> or volume:<name>"`
	Kind      string `json:"kind,omitempty" jsonschema:"depends_on, network, volume or env"`
	Condition string `json:"condition,omitempty" jsonschema:"depends_on: service_started, service_healthy or service_completed_successfully"`
	Network   string `json:"network,omitempty"`
	Target    string `json:"target,omitempty" jsonschema:"volume: mount path in the container"`
	RecipeID  string `json:"recipe_id,omitempty"`
}

func (t *tools) connectServices(ctx context.Context, in connectServicesIn) (any, error) {
	wf, err := t.dockerWorkflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	base := "/workflows/" + pathEscape(wf.ID) + "/compose/connect"
	if in.Kind == "" && in.RecipeID == "" {
		var options []map[string]any
		err := t.c.get(ctx, base+"?from="+url.QueryEscape(in.From)+"&to="+url.QueryEscape(in.To), &options)
		return map[string]any{"options": options, "hint": "call connect_services again with recipe_id (or kind)"}, err
	}
	body := map[string]string{"from": in.From, "to": in.To, "kind": in.Kind, "condition": in.Condition, "network": in.Network, "target": in.Target, "recipeId": in.RecipeID}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPost, base, body, &out); err != nil {
		return nil, err
	}
	return dockerResult(wf, &out), nil
}
