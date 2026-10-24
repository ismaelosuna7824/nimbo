package api

import (
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Docker gestures of the no-code scripts beyond the plain service form
// keys (nocode_ops_test.go): the Environment list, the environment switch,
// and the values the service form's widgets can produce
// (docker/inspector: service-form.tsx, hardening-section.tsx,
// service-patch.ts, hardening.ts).

// The harness refuses what the service form's widgets can't produce.
func TestNoCodeServiceFormValues(t *testing.T) {
	wf := &model.Workflow{Compose: &model.ComposeModel{Graph: model.ComposeGraph{Nodes: []model.ComposeNode{
		{Kind: model.ComposeService, Name: "api"}, {Kind: model.ComposeService, Name: "db"},
	}}}}
	pw := "x"
	svc := &model.ServiceFacts{NonRootUser: "node", CapDrop: []string{"NET_RAW"}, Environment: []model.ServiceEnv{
		{Key: "DB_PASSWORD", Value: &pw}, {Key: "PORT", Value: &pw}, {Key: "TOKEN", FromFile: ".env.app"},
	}}
	for _, ok := range []map[string]any{
		{"user": "node"},
		{"readOnly": true, "tmpfs": []any{"/tmp"}},
		{"capDrop": []any{"NET_RAW", "ALL"}, "capAdd": []any{"NET_BIND_SERVICE"}},
		{"securityOpt": []any{"no-new-privileges:true"}},
		{"resources": map[string]any{"cpus": "0.5", "memory": "128M", "pids": 100}},
		{"logging": map[string]any{"driver": "json-file", "maxSize": "10m", "maxFile": "3"}},
		{"labels": map[string]any{"prometheus.io/scrape": "true"}},
		{"secretFromEnv": map[string]any{"key": "DB_PASSWORD", "secret": "db_password", "fileEnv": true}},
		{"dependsOn": []any{map[string]any{"service": "db", "condition": "service_healthy"}}},
	} {
		if err := checkServiceValues(wf, "api", svc, ok); err != nil {
			t.Errorf("%v refused: %v", ok, err)
		}
	}
	for _, bad := range []map[string]any{
		{"user": "root"},
		{"user": "1000:1000"}, // the toggle sets the image's documented user
		{"readOnly": true},    // the toggle also adds tmpfs /tmp
		{"capDrop": []any{"ALL"}},
		{"capAdd": []any{"cap_net_admin"}},
		{"securityOpt": []any{"seccomp=unconfined"}},
		{"resources": map[string]any{"pids": "100"}},
		{"logging": map[string]any{"driver": "syslog"}},
		{"logging": map[string]any{"maxFile": 3}},
		{"labels": map[string]any{"a b": "x"}},
		{"secretFromEnv": map[string]any{"key": "PORT", "secret": "port"}},
		{"secretFromEnv": map[string]any{"key": "TOKEN", "secret": "token"}},
		{"secretFromEnv": map[string]any{"key": "DB_PASSWORD", "secret": "db_password"}, "init": true},
		{"dependsOn": []any{map[string]any{"service": "api"}}},
		{"scope": "environment"},
		{"restart": "sometimes"},
		{"command": []any{"node", "server.js"}},
	} {
		if err := checkServiceValues(wf, "api", svc, bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// composeService is a service of the canvas with the workflow it is in.
func (r *nocodeRun) composeService(name string) (*model.Workflow, *model.ServiceFacts, error) {
	wf, err := r.workflow()
	if err != nil {
		return nil, nil, err
	}
	if wf.Compose == nil {
		return nil, nil, fmt.Errorf("not a docker workflow")
	}
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind == model.ComposeService && n.Name == name && n.Service != nil {
			return wf, n.Service, nil
		}
	}
	return nil, nil, fmt.Errorf("no service %q on the canvas", name)
}

// composeStage: the "Base image (FROM)" field (stage-base-field.tsx) of a
// build stage card (node "stage:<service>:<stage>", not one built FROM
// another stage) or of a single-stage build's service form ("service:<name>").
func (r *nocodeRun) composeStage(n *yaml.Node) error {
	var a struct{ Node, From string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	if a.From == "" || strings.ContainsAny(a.From, " \t") {
		return fmt.Errorf("the field refuses %q", a.From)
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	if wf.Compose == nil {
		return fmt.Errorf("not a docker workflow")
	}
	nodes := wf.Compose.Graph.Nodes
	i := slices.IndexFunc(nodes, func(x model.ComposeNode) bool { return x.ID == a.Node })
	switch {
	case i < 0:
		return fmt.Errorf("no node %q on the canvas", a.Node)
	case nodes[i].Kind == model.ComposeBuildStage:
		if nodes[i].Stage == nil || nodes[i].Stage.FromStage != "" {
			return fmt.Errorf("%s builds on another stage: no base image field", a.Node)
		}
	case nodes[i].Kind == model.ComposeService:
		s := nodes[i].Service
		multi := slices.ContainsFunc(nodes, func(x model.ComposeNode) bool { return x.Kind == model.ComposeBuildStage && x.Parent == a.Node })
		if s == nil || s.Build == nil || len(s.BaseImages) != 1 || multi {
			return fmt.Errorf("the service form shows the base image of single-stage builds only")
		}
	default:
		return fmt.Errorf("%s has no base image field", a.Node)
	}
	return r.call("PATCH", r.wfPath("/compose/stages/"+url.PathEscape(a.Node)), map[string]string{"from": a.From}, nil)
}

// The service form's validation (service-form.tsx, service-patch.ts,
// hardening.ts).
var (
	formServiceName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	formPort        = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?$`)
	formDuration    = regexp.MustCompile(`^$|^(\d+(\.\d+)?(ns|us|ms|s|m|h))+$`)
	formCPUs        = regexp.MustCompile(`^\d+(\.\d+)?$`)
	formMemory      = regexp.MustCompile(`(?i)^\d+(\.\d+)?[bkmg]?b?$`)
	formLogSize     = regexp.MustCompile(`(?i)^\d+[kmg]?$`)
	formLogFiles    = regexp.MustCompile(`^[1-9]\d*$`)
	formEnvKey      = regexp.MustCompile(`^[A-Za-z_][\w.-]*$`)
	formSecretName  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	formSecretKey   = regexp.MustCompile(`(?i)(pass(word)?|secret|token|api[_-]?key|private[_-]?key|credential)`)
	formCapability  = regexp.MustCompile(`^[A-Z0-9_]+$`)
)

// formRootUser mirrors hardening.ts isRootUser.
func formRootUser(u string) bool {
	u = strings.TrimSpace(u)
	return u == "" || u == "root" || u == "0" || strings.HasPrefix(u, "root:") || strings.HasPrefix(u, "0:")
}

func formStrings(what string, v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: a list of strings", what)
	}
	out := make([]string, len(list))
	for i, x := range list {
		s, ok := x.(string)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: a string", what, i)
		}
		out[i] = s
	}
	return out, nil
}

// changedItems are the items added to and removed from cur by next.
func changedItems(cur, next []string) (added, removed []string) {
	for _, s := range next {
		if !slices.Contains(cur, s) {
			added = append(added, s)
		}
	}
	for _, s := range cur {
		if !slices.Contains(next, s) {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// checkServiceValues refuses values the service form's widgets can't
// produce for service name (facts svc) of wf (the keys are checked by
// composePatch).
func checkServiceValues(wf *model.Workflow, name string, svc *model.ServiceFacts, patch map[string]any) error {
	for _, k := range slices.Sorted(maps.Keys(patch)) {
		if err := checkServiceValue(wf, name, svc, patch, k, patch[k]); err != nil {
			return fmt.Errorf("%s: %w", k, err)
		}
	}
	return nil
}

func checkServiceValue(wf *model.Workflow, name string, svc *model.ServiceFacts, patch map[string]any, k string, v any) error {
	str := func(x any) (string, bool) { s, ok := x.(string); return s, ok }
	fields := func(x any) (map[string]any, error) {
		m, ok := x.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("the form writes an object")
		}
		return m, nil
	}
	switch k {
	case "scope":
		// The "Only in <env>" switch, shown while an environment with an
		// override file is active.
		if v != "environment" {
			return fmt.Errorf(`the switch writes "environment"`)
		}
		env := wf.Compose.Environment
		if env == "" || !slices.ContainsFunc(wf.Compose.Environments, func(e model.ComposeEnvironment) bool { return e.Name == env && e.File != "" }) {
			return fmt.Errorf("the switch only shows while an environment with an override file is active")
		}
	case "name":
		if s, _ := str(v); !formServiceName.MatchString(s) {
			return fmt.Errorf("the name field refuses %v", v)
		}
	case "image", "command":
		if _, ok := str(v); !ok && v != nil {
			return fmt.Errorf("a text field (the command is the shell form)")
		}
	case "restart":
		if s, _ := str(v); v != nil && !slices.Contains([]string{"no", "always", "on-failure", "unless-stopped"}, s) {
			return fmt.Errorf("the select offers no, always, on-failure, unless-stopped")
		}
	case "replicas":
		if n, ok := v.(int); v != nil && (!ok || n < 0 || n > 50) {
			return fmt.Errorf("a number 0–50")
		}
	case "ports":
		list, _ := v.([]any)
		for i, it := range list {
			p, err := fields(it)
			if err != nil {
				return err
			}
			host, _ := str(p["host"])
			ctr, _ := str(p["container"])
			if !formPort.MatchString(ctr) || host != "" && !formPort.MatchString(host) || p["protocol"] != nil && p["protocol"] != "udp" && p["protocol"] != "tcp" {
				return fmt.Errorf("[%d]: ports (or ranges) and a tcp/udp protocol", i)
			}
		}
	case "volumes":
		list, _ := v.([]any)
		for i, it := range list {
			m, err := fields(it)
			if err != nil {
				return err
			}
			target, _ := str(m["target"])
			_, ro := m["readOnly"].(bool)
			if m["type"] != nil && !slices.Contains([]any{"volume", "bind", "tmpfs"}, m["type"]) || !strings.HasPrefix(target, "/") || m["readOnly"] != nil && !ro {
				return fmt.Errorf("[%d]: type volume/bind/tmpfs, an absolute target path, the ro checkbox", i)
			}
		}
	case "dependsOn":
		list, _ := v.([]any)
		for i, it := range list {
			d, err := fields(it)
			if err != nil {
				return err
			}
			if d["service"] == name || !slices.Contains(composeServiceNames(wf), fmt.Sprint(d["service"])) ||
				d["condition"] != nil && !slices.Contains([]any{"service_started", "service_healthy", "service_completed_successfully"}, d["condition"]) {
				return fmt.Errorf("[%d]: the selects offer the other services and started/healthy/completed", i)
			}
		}
	case "healthcheck":
		if v == nil {
			return nil
		}
		h, err := fields(v)
		if err != nil {
			return err
		}
		for _, f := range []string{"interval", "timeout", "startPeriod"} {
			if s, _ := str(h[f]); h[f] != nil && !formDuration.MatchString(s) {
				return fmt.Errorf("%s: a duration like 10s", f)
			}
		}
		if n, ok := h["retries"].(int); h["retries"] != nil && (!ok || n < 0) {
			return fmt.Errorf("retries: a number")
		}
	case "resources":
		if v == nil {
			return nil
		}
		m, err := fields(v)
		if err != nil {
			return err
		}
		cpus, _ := str(m["cpus"])
		mem, _ := str(m["memory"])
		pids, isInt := m["pids"].(int)
		if m["cpus"] != nil && !formCPUs.MatchString(cpus) || m["memory"] != nil && !formMemory.MatchString(mem) || m["pids"] != nil && (!isInt || pids < 1) {
			return fmt.Errorf("cpus like 0.5, memory like 512M, a positive process count")
		}
	case "logging":
		if v == nil {
			return nil
		}
		m, err := fields(v)
		if err != nil {
			return err
		}
		size, _ := str(m["maxSize"])
		files, _ := str(m["maxFile"])
		if m["driver"] != nil && !slices.Contains([]any{"json-file", "local", "none"}, m["driver"]) ||
			m["maxSize"] != nil && !formLogSize.MatchString(size) || m["maxFile"] != nil && !formLogFiles.MatchString(files) {
			return fmt.Errorf("the driver select (json-file, local, none), a size like 10m, a file count like \"3\"")
		}
	case "user":
		// "Run as non-root" sets the image's documented user (else
		// 1000:1000); once on, the field takes any non-root user.
		if v == nil {
			return nil
		}
		u, ok := str(v)
		target := svc.NonRootUser
		if target == "" {
			target = "1000:1000"
		}
		if !ok || formRootUser(u) || formRootUser(svc.User) && u != target {
			return fmt.Errorf("the toggle sets %q; the field then takes a non-root user", target)
		}
	case "readOnly", "privileged", "init":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("a switch: true or false")
		}
		if k == "readOnly" && v == true && len(svc.Tmpfs) == 0 && patch["tmpfs"] == nil {
			return fmt.Errorf("the toggle also adds tmpfs [/tmp] when there is none")
		}
	case "capDrop", "securityOpt":
		// Toggles: ALL / no-new-privileges:true in or out, the rest kept.
		cur, toggled := svc.CapDrop, "ALL"
		if k == "securityOpt" {
			cur, toggled = svc.SecurityOpt, "no-new-privileges:true"
		}
		var next []string
		if v != nil {
			var err error
			if next, err = formStrings(k, v); err != nil {
				return err
			}
		}
		added, removed := changedItems(cur, next)
		if slices.ContainsFunc(added, func(s string) bool { return s != toggled }) ||
			slices.ContainsFunc(removed, func(s string) bool { return !strings.EqualFold(s, "ALL") && !strings.HasPrefix(s, "no-new-privileges") }) {
			return fmt.Errorf("the toggle adds or removes %s only", toggled)
		}
	case "capAdd":
		if v == nil {
			return nil
		}
		caps, err := formStrings(k, v)
		if err != nil {
			return err
		}
		drop := svc.CapDrop
		if d, ok := patch["capDrop"]; ok {
			drop, _ = formStrings("capDrop", d)
		}
		if !slices.ContainsFunc(drop, func(c string) bool { return strings.EqualFold(c, "ALL") }) {
			return fmt.Errorf("the chips show once all capabilities are dropped")
		}
		for _, c := range caps {
			if !formCapability.MatchString(c) || strings.HasPrefix(c, "CAP_") {
				return fmt.Errorf("%q: chips write capabilities upper case without CAP_", c)
			}
		}
	case "tmpfs":
		if v == nil {
			return nil
		}
		paths, err := formStrings(k, v)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(paths, func(p string) bool { return !strings.HasPrefix(p, "/") }) {
			return fmt.Errorf("absolute paths in the container")
		}
	case "secretFromEnv":
		// "Move to a secret file" on a credential written in the compose file.
		if len(patch) != 1 {
			return fmt.Errorf("the button sends it alone")
		}
		m, err := fields(v)
		if err != nil {
			return err
		}
		key, _ := str(m["key"])
		secret, _ := str(m["secret"])
		if _, ok := m["fileEnv"].(bool); m["fileEnv"] != nil && !ok {
			return fmt.Errorf("fileEnv: a checkbox")
		}
		if !formSecretName.MatchString(secret) {
			return fmt.Errorf("secret: the name field refuses %q", secret)
		}
		if !slices.ContainsFunc(svc.Environment, func(e model.ServiceEnv) bool {
			return e.Key == key && e.FromFile == "" && !strings.HasSuffix(e.Key, "_FILE") && (e.Secret || formSecretKey.MatchString(e.Key))
		}) {
			return fmt.Errorf("%s is not a credential written in the service's environment", key)
		}
	case "resetOverrides":
		attrs, err := formStrings(k, v)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(attrs, func(a string) bool { return !slices.Contains(svc.Overrides, a) }) {
			return fmt.Errorf("the reset button only shows on attributes the environment overrides (%v)", svc.Overrides)
		}
	case "labels":
		// The Labels list (key and value text fields) or the scrape button.
		if v == nil {
			return nil
		}
		m, err := fields(v)
		if err != nil {
			return err
		}
		for key, val := range m {
			if _, ok := str(val); !ok || key == "" || strings.ContainsAny(key, " \t") {
				return fmt.Errorf("%q: a label name without spaces and a text value", key)
			}
		}
	case "environment":
		list, _ := v.([]any)
		for i, it := range list {
			e, err := fields(it)
			if err != nil {
				return err
			}
			if s, _ := str(e["key"]); !formEnvKey.MatchString(s) {
				return fmt.Errorf("[%d]: the name field refuses %v", i, e["key"])
			}
		}
	}
	return nil
}

func composeServiceNames(wf *model.Workflow) []string {
	var out []string
	for _, n := range wf.Compose.Graph.Nodes {
		if n.Kind == model.ComposeService {
			out = append(out, n.Name)
		}
	}
	return out
}

// composeEnv: rows of the service form's Environment list added, edited
// (set) or removed, sent like ServiceForm's onChange: every row the form
// lists (not the env_file ones), a redacted secret without its value, the
// others as written (raw, ${VAR}s kept).
func (r *nocodeRun) composeEnv(n *yaml.Node) error {
	var a struct {
		Service string            `yaml:"service"`
		Set     map[string]string `yaml:"set"`
		Remove  []string          `yaml:"remove"`
	}
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	_, svc, err := r.composeService(a.Service)
	if err != nil {
		return err
	}
	set := maps.Clone(a.Set)
	var env []map[string]any
	for _, e := range svc.Environment {
		if e.FromFile != "" || slices.Contains(a.Remove, e.Key) {
			continue
		}
		row := map[string]any{"key": e.Key}
		switch v, ok := set[e.Key]; {
		case ok:
			row["value"] = v
			delete(set, e.Key)
		case e.Secret && e.Value == nil:
			row["secret"] = true
		case e.Raw != nil:
			row["value"] = *e.Raw
		case e.Value != nil:
			row["value"] = *e.Value
		}
		env = append(env, row)
	}
	for _, k := range slices.Sorted(maps.Keys(set)) {
		if !formEnvKey.MatchString(k) {
			return fmt.Errorf("the name field refuses %q", k)
		}
		env = append(env, map[string]any{"key": k, "value": set[k]})
	}
	if env == nil {
		env = []map[string]any{}
	}
	return r.call("PATCH", r.wfPath("/compose/services/"+url.PathEscape(a.Service)), map[string]any{"environment": env}, nil)
}

// envActivate: the environment switch (saves activeEnvironment; "" is the
// base).
func (r *nocodeRun) envActivate(n *yaml.Node) error {
	var a struct{ Env string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	wf, err := r.workflow()
	if err != nil {
		return err
	}
	wf.ActiveEnvironment = a.Env
	return r.call("PUT", r.wfPath(""), wf, nil)
}
