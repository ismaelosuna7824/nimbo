package api

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v4"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes gestures and form paths of the no-code harness beyond the
// base ones of nocode_ops_test.go: deleting an environment, the inspector
// header's rename, and the inspector sections added for no-code
// challenges (workload rollout, spread and hardening, Namespace labels,
// PodDisruptionBudget, ConfigMap/Secret keys).

func init() {
	// The environment switch is envActivate (nocode_docker_test.go).
	nocodeOps["envDelete"] = (*nocodeRun).envDelete
	nocodeOps["kubeRename"] = (*nocodeRun).kubeRename
}

var dnsLabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// kubeRename: the inspector header's name field (kube-inspector.tsx
// ObjectName), for objects of plain manifests whose metadata.name the
// daemon reports writable.
func (r *nocodeRun) kubeRename(n *yaml.Node) error {
	var a struct{ Node, Name string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	node, err := r.kubeNode(a.Node)
	if err != nil {
		return err
	}
	if node.SourceKind != model.KubeManifests {
		return fmt.Errorf("%s: only objects of plain manifests are renamed in the inspector", a.Node)
	}
	if !dnsLabelRE.MatchString(a.Name) {
		return fmt.Errorf("%q: the name field takes lowercase letters, digits and -", a.Name)
	}
	var fields struct {
		Fields []model.KubeField `json:"fields"`
	}
	if err := r.call("GET", r.wfPath("/kubernetes/fields?node="+url.QueryEscape(node.ID)), nil, &fields); err != nil {
		return err
	}
	if !slices.ContainsFunc(fields.Fields, func(f model.KubeField) bool { return f.Path == "metadata.name" && f.Editable }) {
		return fmt.Errorf("%s: its name is not writable", a.Node)
	}
	return r.call("PATCH", r.wfPath("/kubernetes/objects"), map[string]any{"node": node.ID, "set": map[string]any{"metadata.name": a.Name}}, nil)
}

// envDelete: Delete in an environment's menu (environments/dialogs.tsx).
func (r *nocodeRun) envDelete(n *yaml.Node) error {
	var a struct{ Env string }
	if err := decodeArg(n, &a); err != nil {
		return err
	}
	return r.call("DELETE", r.wfPath("/environments/"+a.Env), nil, nil)
}

var (
	intOrPercentRE = regexp.MustCompile(`^\d+%$`)
	spreadKeys     = []string{"kubernetes.io/hostname", "topology.kubernetes.io/zone"}
)

// intOrPercent: the rollout and disruption budget inputs (a number, or a
// percentage written as text).
func intOrPercent(path string, v any, set bool) error {
	if !set {
		return nil
	}
	if _, ok := v.(int); ok {
		return nil
	}
	if s, ok := v.(string); ok && intOrPercentRE.MatchString(s) {
		return nil
	}
	return fmt.Errorf("%s: a number or a percentage", path)
}

// wantValue: a switch that writes exactly want when on (and unsets when
// off).
func wantValue(path string, v any, set bool, want any) error {
	if set && fmt.Sprint(v) != fmt.Sprint(want) {
		return fmt.Errorf("%s: the switch writes %v (off removes it)", path, want)
	}
	return nil
}

// kubeWorkloadExtra: the workload form's Rollout, Spread and Hardening
// sections (workload-form.tsx, workload-extras.ts). handled is false for
// other paths.
func kubeWorkloadExtra(node *model.KubeNode, ps, path string, v any, set bool) (bool, error) {
	w := node.Workload
	switch {
	case path == "spec.strategy.type" && node.Kind == "Deployment":
		if s, _ := v.(string); set && s != "RollingUpdate" && s != "Recreate" {
			return true, fmt.Errorf("%s: the select offers RollingUpdate and Recreate", path)
		}
		return true, nil
	case path == "spec.strategy.rollingUpdate" && node.Kind == "Deployment":
		if set {
			return true, fmt.Errorf("%s: the form clears it (Recreate) or sets its fields", path)
		}
		return true, nil
	case (path == "spec.strategy.rollingUpdate.maxUnavailable" || path == "spec.strategy.rollingUpdate.maxSurge") && node.Kind == "Deployment":
		return true, intOrPercent(path, v, set)
	case path == ps+".topologySpreadConstraints" && (node.Kind == "Deployment" || node.Kind == "StatefulSet"):
		if !set {
			return true, nil
		}
		// The preset: one constraint over nodes or zones selecting the
		// workload's own pods.
		list, ok := v.([]any)
		if !ok || len(list) != 1 {
			return true, fmt.Errorf("%s: the spread select writes one constraint", path)
		}
		c, _ := list[0].(map[string]any)
		key, _ := c["topologyKey"].(string)
		want := map[string]any{"maxSkew": 1, "topologyKey": key, "whenUnsatisfiable": "ScheduleAnyway", "labelSelector": map[string]any{"matchLabels": anyMap(w.PodLabels)}}
		if !slices.Contains(spreadKeys, key) || fmt.Sprint(c) != fmt.Sprint(want) {
			return true, fmt.Errorf("%s: the spread select writes %v", path, want)
		}
		return true, nil
	case path == ps+".securityContext.runAsNonRoot":
		return true, wantValue(path, v, set, true)
	case path == ps+".securityContext.runAsUser":
		if n, ok := v.(int); set && (!ok || n < 0) {
			return true, fmt.Errorf("%s: a user ID", path)
		}
		return true, nil
	case path == ps+".securityContext.seccompProfile":
		return true, wantValue(path, v, set, map[string]any{"type": "RuntimeDefault"})
	case strings.HasPrefix(path, ps+".containers[name="):
		name, field, ok := strings.Cut(strings.TrimPrefix(path, ps+".containers[name="), "]")
		if !ok || !slices.ContainsFunc(w.Containers, func(c model.KubeContainer) bool { return c.Name == name }) {
			return false, nil
		}
		switch strings.TrimPrefix(field, ".") {
		case "securityContext.allowPrivilegeEscalation":
			return true, wantValue(path, v, set, false)
		case "securityContext.readOnlyRootFilesystem":
			return true, wantValue(path, v, set, true)
		case "securityContext.capabilities.drop":
			return true, wantValue(path, v, set, []any{"ALL"})
		case "securityContext.privileged":
			if set {
				return true, fmt.Errorf("%s: the Privileged switch only turns it off (removes it)", path)
			}
			return true, nil
		case "securityContext.capabilities.add":
			if !set {
				return true, nil
			}
			list, ok := v.([]any)
			if !ok || len(list) == 0 || slices.ContainsFunc(list, func(x any) bool { s, ok := x.(string); return !ok || s == "" }) {
				return true, fmt.Errorf("%s: capability chips", path)
			}
			return true, nil
		}
	}
	return false, nil
}

// kubeObjectExtra: the Namespace, PodDisruptionBudget and ConfigMap/
// Secret key forms, and the HPA form's metric targets (object-forms.tsx).
// handled is false for other objects or paths.
func kubeObjectExtra(node *model.KubeNode, path string, v any, set bool) (bool, error) {
	switch {
	case node.Kind == "Namespace" && path == "metadata.labels":
		return true, stringMap(path, v, set)
	case node.Kind == "PodDisruptionBudget" && node.APIVersion == "policy/v1":
		switch path {
		case "spec.minAvailable", "spec.maxUnavailable":
			return true, intOrPercent(path, v, set)
		case "spec.selector.matchLabels":
			return true, stringMap(path, v, set)
		}
	case node.Kind == "HorizontalPodAutoscaler" && node.Scaler != nil && path == "spec.metrics":
		return true, hpaMetricsShape(path, v, set)
	case (node.Kind == "ConfigMap" || node.Kind == "Secret") && node.APIVersion == "v1":
		m := dataKeyRE.FindStringSubmatch(path)
		if m == nil {
			return false, nil
		}
		// ConfigMap values under data; Secret values typed as plain text
		// under stringData (a base64 data entry is only removed).
		switch {
		case m[1] == "stringData" && node.Kind != "Secret":
			return true, fmt.Errorf("%s: a ConfigMap has no stringData", path)
		case m[1] == "data" && node.Kind == "Secret" && set:
			return true, fmt.Errorf("%s: the Secret form writes typed values under stringData", path)
		}
		return true, wantString(path, v, set)
	}
	return false, nil
}

// dataKeyRE is a key of a ConfigMap or Secret as the key form writes it
// (workload-extras.ts keyPath, DATA_KEY_RE).
var dataKeyRE = regexp.MustCompile(`^(data|stringData)(?:\.([-_a-zA-Z0-9]+)|\["([-._a-zA-Z0-9]+)"\])$`)

// hpaMetricsShape: what the CPU and memory target inputs write
// (paths.ts hpaMetrics): a utilization target per resource, CPU first.
func hpaMetricsShape(path string, v any, set bool) error {
	if !set {
		return nil
	}
	list, ok := v.([]any)
	if !ok || len(list) == 0 || len(list) > 2 {
		return fmt.Errorf("%s: the CPU and memory target inputs write one or two metrics", path)
	}
	names := []string{}
	for _, x := range list {
		m, _ := x.(map[string]any)
		res, _ := m["resource"].(map[string]any)
		name, _ := res["name"].(string)
		target, _ := res["target"].(map[string]any)
		pct, isInt := target["averageUtilization"].(int)
		want := map[string]any{"type": "Resource", "resource": map[string]any{"name": name, "target": map[string]any{"type": "Utilization", "averageUtilization": pct}}}
		if !isInt || pct < 1 || pct > 100 || (name != "cpu" && name != "memory") || fmt.Sprint(m) != fmt.Sprint(want) {
			return fmt.Errorf("%s: the target inputs write {type: Resource, resource: {name: cpu|memory, target: {type: Utilization, averageUtilization: 1-100}}}", path)
		}
		names = append(names, name)
	}
	if len(names) == 2 && (names[0] != "cpu" || names[1] != "memory") {
		return fmt.Errorf("%s: CPU first, then memory", path)
	}
	return nil
}

func anyMap(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
