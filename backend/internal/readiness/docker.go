package readiness

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/monitoring"
	"github.com/ismaelosuna7824/nimbo/backend/internal/scenario"
)

// Docker checks every environment of the docker workflow wf, whose files
// are in dir (only: just that environment): the base ("" — checked as
// production when the workflow has no environments) and each environment,
// opened on its own like docker compose -f … --env-file … would, so the
// workflow's active environment is untouched. cfg are the best-practice
// settings: rules turned off stay off, opt-in rules are all evaluated.
func Docker(ctx context.Context, dir string, wf *model.Workflow, cfg model.BestPracticeSettings, src Sources, only *string) model.WorkflowReadiness {
	out := model.WorkflowReadiness{WorkflowID: wf.ID, Kind: string(model.WorkflowDocker), ActiveEnvironment: wf.Compose.Environment, Environments: []model.EnvironmentReadiness{}}
	envs := []string{""}
	for _, e := range wf.Compose.Environments {
		envs = append(envs, e.Name)
	}
	for _, env := range envs {
		if only != nil && *only != env {
			continue
		}
		p := compose.Open(ctx, dir, wf.Compose.Files, wf.Compose.Profiles, compose.Options{Environment: env})
		prodLike := model.ProdLike(env) || env == "" && len(envs) == 1
		out.Environments = append(out.Environments, DockerEnvironment(p, cfg, src.For(env, prodLike, DockerCatalog(wf, p))))
	}
	return out
}

// DockerCatalog is the incident scenarios that apply to project p (one
// environment of wf, opened on its own): scenario.Catalog on its graph.
func DockerCatalog(wf *model.Workflow, p *compose.Project) model.ScenarioCatalog {
	c := *wf.Compose
	c.Graph, c.Environment = p.Graph(nil), p.Environment
	w := *wf
	w.Compose = &c
	return scenario.Catalog(&w)
}

var dockerReliability = []Check{
	{ID: "project", Title: "Project loads", Pass: "The compose files, .env files and Dockerfiles load and follow the Compose specification."},
	{ID: "configuration", Title: "Configuration", Codes: []string{"dk_unresolved_var", "dk_missing_env_file", "dk_environment_file"},
		Pass: "Every variable has a value and every env_file exists."},
	{ID: "healthchecks", Title: "Healthchecks", Codes: []string{"dk_missing_healthcheck", "dk_no_healthcheck", "dk_healthcheck_timing", "dk_depends_healthy"},
		Pass: "Services report their health and wait for healthy dependencies."},
	{ID: "restart", Title: "Restart and shutdown", Codes: []string{"dk_no_restart", "dk_no_init"},
		Pass: "Crashed containers restart and stop cleanly."},
	{ID: "resources", Title: "Resource limits", Codes: []string{"dk_no_limits"},
		Pass: "Containers cap their CPU and memory."},
	{ID: "data", Title: "Persistent data", Codes: []string{"dk_anon_volume", "dk_bind_deps"},
		Pass: "Data lives on named volumes."},
}

var dockerSecurity = []Check{
	{ID: "secrets", Title: "Secrets", Codes: []string{"dk_secret_env", "dk_secret_build"},
		Pass: "Credentials come from .env files or secrets, not from the compose files or build arguments."},
	{ID: "privileges", Title: "Container privileges", Codes: []string{"dk_root_user", "dk_docker_sock", "dk_privileged", "dk_hardening",
		"dk_cap_add", "dk_net_raw", "dk_read_only", "dk_no_new_privileges", "dk_apparmor_unconfined", "dk_selinux_disabled", "dk_seccomp_unconfined", "dk_pids_limit",
		"dk_host_dirs", "dk_host_ipc", "dk_host_uts", "dk_host_userns", "dk_devices", "dk_mount_propagation"},
		Pass: "Containers run unprivileged, as non-root, with Docker's security profiles and a process limit, without the Docker socket or host access."},
	{ID: "exposure", Title: "Published ports", Codes: []string{"dk_db_published", "dk_port_interface", "dk_privileged_port", "dk_default_bridge"},
		Pass: "Ports are published on a specific interface and unprivileged host ports, databases stay inside the project network and services use the project's networks."},
}

var dockerHygiene = []Check{
	{ID: "images", Title: "Image pinning", Codes: []string{"dk_latest_tag"}, Pass: "Images are pinned."},
	{ID: "builds", Title: "Dockerfiles", Codes: []string{"dk_single_stage", "dk_heavy_base", "dk_dockerignore", "dk_layer_order", "dk_cache_mount", "dk_apt_install", "dk_pip_cache", "dk_apk_cache", "dk_add", "dk_add_any", "dk_update_alone", "dk_shell_form", "dk_workdir"},
		Pass: "Builds are small, cached and use exec-form commands."},
	{ID: "logs", Title: "Log rotation", Codes: []string{"dk_log_rotation"}, Pass: "Container logs are rotated."},
	{ID: "ports", Title: "Port wiring", Codes: []string{"dk_expose_mismatch"}, Pass: "Published ports match what the images listen on."},
}

// Component columns of compose services.
var (
	dockerHealthCodes    = []string{"dk_missing_healthcheck", "dk_no_healthcheck", "dk_healthcheck_timing", "dk_depends_healthy", "dk_no_restart", "dk_no_init"}
	dockerResourcesCodes = []string{"dk_no_limits"}
)

func init() {
	// The project item holds every error rule (compose refuses them).
	for _, rl := range compose.Rules() {
		if rl.Severity == model.SeverityError {
			dockerReliability[0].Codes = append(dockerReliability[0].Codes, rl.Code)
		}
	}
}

// DockerEnvironment builds one environment's checklist and component rows.
func DockerEnvironment(p *compose.Project, cfg model.BestPracticeSettings, ev *Evidence) model.EnvironmentReadiness {
	settings := Settings(cfg, compose.Rules())
	f := NewFindings(compose.FilterSettings(slices.Clone(p.Diagnostics), settings), settings.DisabledRules, ev.ProdLike)
	reliability := f.Items(dockerReliability)
	security := f.Items(dockerSecurity)
	hygiene := f.Items(dockerHygiene)
	f.Claim(&reliability[0], dockerReliability[0])
	mon := dockerMonitoring(p, ev.ProdLike)
	e := model.EnvironmentReadiness{Name: ev.Env, ProductionLike: ev.ProdLike, Groups: []model.ReadinessGroup{
		Group(model.ReadinessReliability, reliability),
		Group(model.ReadinessPerformance, []model.ReadinessItem{ev.LoadItem(false), ev.CapacityItem()}),
		Group(model.ReadinessResilience, ev.ResilienceItems()),
		Group(model.ReadinessMonitoring, []model.ReadinessItem{mon}),
		Group(model.ReadinessSecurity, security),
		Group(model.ReadinessHygiene, hygiene),
	}, Components: []model.ReadinessComponent{}}
	if ev.Load != nil {
		e.LoadTestRunID = ev.Load.ID
	}
	health := Column{Codes: dockerHealthCodes, Pass: "Healthcheck and restart policy set."}
	if f.AllDisabled(dockerHealthCodes) {
		health.Off = "Turned off in Settings."
	}
	resources := Column{Codes: dockerResourcesCodes, Pass: "CPU and memory limits set."}
	if f.AllDisabled(dockerResourcesCodes) {
		resources.Off = "Turned off in Settings."
	}
	for _, name := range dockerServices(p) {
		s := p.Model.Services[name]
		role := RoleApp
		if im, ok := compose.ImageFor(s.Image); ok && (im.Category == "database" || im.Category == "cache" || im.Category == "messaging") {
			role = RoleData
		}
		id := compose.ServiceID(name)
		e.Components = append(e.Components, ev.Component(ComponentSpec{
			NodeID: id, Name: name, Kind: "service", Role: role,
			Diagnostics: f.Node(id), Health: health, Resources: resources,
		}, mon))
	}
	return Summarize(e)
}

// dockerServices are the project's active services, without the ones the
// generated monitoring stack declares.
func dockerServices(p *compose.Project) []string {
	var out []string
	mon := monitoring.ComposeFile(p.ProjectDir)
	for _, name := range p.SortedServiceNames() {
		if p.Inactive[name] {
			continue
		}
		if file, _ := p.ServiceLine(name); file == mon {
			continue
		}
		out = append(out, name)
	}
	return out
}

// DockerMonitoring returns what monitors the project: the generated
// monitoring stack, or Prometheus services of its own. Empty means none.
func DockerMonitoring(p *compose.Project) []string {
	if slices.Contains(p.Files, monitoring.ComposeFile(p.ProjectDir)) {
		return []string{"the generated Prometheus, Grafana and cAdvisor stack (" + monitoring.ComposeFile(p.ProjectDir) + ")"}
	}
	if p.Model == nil {
		return nil
	}
	var out []string
	for _, name := range p.SortedServiceNames() {
		if r := compose.ParseImage(p.Model.Services[name].Image); r != nil && strings.HasSuffix(r.Repo, "prometheus") {
			out = append(out, "the "+name+" Prometheus service")
		}
	}
	return out
}

func dockerMonitoring(p *compose.Project, prodLike bool) model.ReadinessItem {
	item := model.ReadinessItem{ID: "monitoring", Title: "Metrics, dashboards and alerts"}
	if found := DockerMonitoring(p); len(found) > 0 {
		item.Status, item.Message = model.ItemPass, fmt.Sprintf("Monitored by %s.", List(found))
		return item
	}
	item.Status = model.ItemInfo
	if prodLike {
		item.Status = model.ItemWarn
	}
	item.Message = "No Prometheus metrics or alerts: CPU saturation, restarts and error spikes reach users before you see them."
	item.Action = &model.ReadinessAction{Kind: model.ActionAddMonitoring, Label: "Add monitoring"}
	return item
}
