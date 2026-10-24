package kube

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// NetworkPolicy recipes: drag a workload onto another to let the first
// open connections to the second, or pick "Accept traffic from anywhere"
// on a workload alone. Each workload's rules live in the NetworkPolicy
// selecting exactly its pods (created, named after it, when missing), so
// a namespace-wide default deny (palette "Default deny + DNS") plus these
// allows is least privilege for pod traffic.

// Recipe kinds (ConnectRecipe.Kind).
const (
	RecipeAllowTraffic = "allowTraffic" // workload -> workload: egress from src, ingress to dst
	RecipeAllowAll     = "allowAll"     // workload: ingress from anywhere on its ports
)

// policyPort is a container port a rule opens.
type policyPort struct {
	port     int
	protocol string
}

// policyPorts are the declared container ports of a workload's pods.
func policyPorts(o *Object) []policyPort {
	spec, _ := podSpec(o)
	var out []policyPort
	for _, c := range list(spec["containers"]) {
		for _, pt := range list(dig(c, "ports")) {
			n, ok := toInt(dig(pt, "containerPort"))
			if !ok {
				continue
			}
			pp := policyPort{n, firstNonEmpty(str(pt, "protocol"), "TCP")}
			if !slices.Contains(out, pp) {
				out = append(out, pp)
			}
		}
	}
	return out
}

// portsText is "port 8080" or "ports 9898, 9999/UDP".
func portsText(ps []policyPort) string {
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = strconv.Itoa(p.port)
		if p.protocol != "TCP" {
			parts[i] += "/" + p.protocol
		}
	}
	if len(parts) == 1 {
		return "port " + parts[0]
	}
	return "ports " + strings.Join(parts, ", ")
}

func portsValue(ps []policyPort) []any {
	out := make([]any, len(ps))
	for i, p := range ps {
		out[i] = map[string]any{"protocol": p.protocol, "port": p.port}
	}
	return out
}

// policyFor is the NetworkPolicy of w's namespace whose podSelector selects
// exactly w's pod labels (nil when there is none).
func (p *Project) policyFor(w *Object) *Object {
	labels := PodTemplateLabels(w)
	for _, o := range p.Objects {
		if o.Kind != "NetworkPolicy" || o.Namespace != w.Namespace || dig(o.Obj, "spec", "podSelector", "matchExpressions") != nil {
			continue
		}
		if maps.Equal(strMap(dig(o.Obj, "spec", "podSelector", "matchLabels")), labels) {
			return o
		}
	}
	return nil
}

// policyName names the policy a recipe writes w's rules to.
func (p *Project) policyName(w *Object) string {
	if pol := p.policyFor(w); pol != nil {
		return pol.Name
	}
	return w.Name
}

// peer selects w's pods from a policy of namespace ns.
func peer(w *Object, ns string) map[string]any {
	out := map[string]any{"podSelector": map[string]any{"matchLabels": labelValues(PodTemplateLabels(w))}}
	if w.Namespace != ns {
		out["namespaceSelector"] = map[string]any{"matchLabels": map[string]any{"kubernetes.io/metadata.name": w.Namespace}}
	}
	return out
}

func labelValues(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// netpolRecipes lists the NetworkPolicy recipes of src (and dst).
func (p *Project) netpolRecipes(src, dst *Object) []ConnectRecipe {
	if len(PodTemplateLabels(src)) == 0 {
		return nil
	}
	if dst == nil {
		ports := policyPorts(src)
		if len(ports) == 0 {
			return nil
		}
		return []ConnectRecipe{{Kind: RecipeAllowAll, Label: "Accept traffic from anywhere",
			Description: fmt.Sprintf("NetworkPolicy %s: %s's pods accept connections from any source on %s.", p.policyName(src), src.Name, portsText(ports))}}
	}
	ports := policyPorts(dst)
	if len(ports) == 0 || len(PodTemplateLabels(dst)) == 0 {
		return nil
	}
	return []ConnectRecipe{{Kind: RecipeAllowTraffic, Label: fmt.Sprintf("Allow %s → %s traffic", src.Name, dst.Name),
		Description: fmt.Sprintf("NetworkPolicy %s: %s accepts connections from %s on %s. NetworkPolicy %s: %s may connect to %s on the same ports.",
			p.policyName(dst), dst.Name, src.Name, portsText(ports), p.policyName(src), src.Name, dst.Name)}}
}

// allowRule adds rule to the ingress or egress list of w's NetworkPolicy,
// listing the direction in its policyTypes; a missing policy is created.
func (e *Editor) allowRule(w *Object, direction string, rule map[string]any) ([]string, error) {
	typ := "Ingress"
	if direction == "egress" {
		typ = "Egress"
	}
	pol := e.p.policyFor(w)
	if pol == nil {
		meta := map[string]any{"name": w.Name}
		if w.Namespace != "" && w.Namespace != firstNonEmpty(e.p.Source(w).Source.Namespace, "default") {
			meta["namespace"] = w.Namespace
		}
		doc, err := yaml.Marshal(map[string]any{
			"apiVersion": "networking.k8s.io/v1",
			"kind":       "NetworkPolicy",
			"metadata":   meta,
			"spec": map[string]any{
				"podSelector": map[string]any{"matchLabels": labelValues(PodTemplateLabels(w))},
				"policyTypes": []any{typ},
				direction:     []any{rule},
			},
		})
		if err != nil {
			return nil, err
		}
		return e.addFor(w, string(doc))
	}
	types := []string{}
	if declared := list(dig(pol.Obj, "spec", "policyTypes")); declared != nil {
		for _, t := range declared {
			types = append(types, fmt.Sprint(t))
		}
	} else {
		// Implicit: Ingress, plus Egress when the policy has egress rules.
		types = append(types, "Ingress")
		if dig(pol.Obj, "spec", "egress") != nil {
			types = append(types, "Egress")
		}
	}
	if !slices.Contains(types, typ) {
		v := make([]any, 0, len(types)+1)
		for _, t := range append(types, typ) {
			v = append(v, t)
		}
		if err := e.Set(pol, "spec.policyTypes", v); err != nil {
			return nil, err
		}
	}
	return nil, e.Append(pol, "spec."+direction, rule)
}

// connectNetpol applies RecipeAllowTraffic (src may connect to dst) and
// RecipeAllowAll (src accepts any source).
func (e *Editor) connectNetpol(kind string, src, dst *Object) ([]string, error) {
	if kind == RecipeAllowAll {
		return e.allowRule(src, "ingress", map[string]any{"ports": portsValue(policyPorts(src))})
	}
	ports := portsValue(policyPorts(dst))
	added, err := e.allowRule(dst, "ingress", map[string]any{"from": []any{peer(src, dst.Namespace)}, "ports": ports})
	if err != nil {
		return nil, err
	}
	more, err := e.allowRule(src, "egress", map[string]any{"to": []any{peer(dst, src.Namespace)}, "ports": ports})
	return append(added, more...), err
}
