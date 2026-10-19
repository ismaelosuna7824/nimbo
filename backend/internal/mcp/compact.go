package mcp

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Compact, LLM-oriented views of daemon types. Attribute values are HCL
// expressions (the same text that ends up in the .tf files).

type nodeView struct {
	ID         string            `json:"id"`
	Address    string            `json:"address"`
	Kind       model.NodeKind    `json:"kind"`
	Type       string            `json:"type"`
	Name       string            `json:"name"`
	Label      string            `json:"label,omitempty"`
	File       string            `json:"file,omitempty"` // root .tf file export writes it to
	Attributes map[string]string `json:"attributes,omitempty"`
	Blocks     []blockView       `json:"blocks,omitempty"`
}

type blockView struct {
	Type       string            `json:"type"`
	Labels     []string          `json:"labels,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Blocks     []blockView       `json:"blocks,omitempty"`
}

type namedView struct {
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes,omitempty"`
	Blocks     []blockView       `json:"blocks,omitempty"`
}

type envView struct {
	Name    string            `json:"name"`
	Active  bool              `json:"active,omitempty"`
	Values  map[string]string `json:"values,omitempty"`
	EnvVars map[string]string `json:"env_vars,omitempty"`
}

type diagView struct {
	Severity  model.Severity `json:"severity"`
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Detail    string         `json:"detail,omitempty"`
	Node      string         `json:"node,omitempty"` // address
	Attribute string         `json:"attribute,omitempty"`
}

type workflowView struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Clouds       []model.Cloud     `json:"clouds,omitempty"`
	Nodes        []nodeView        `json:"nodes"`
	Edges        []string          `json:"edges,omitempty"`
	Variables    []namedView       `json:"variables,omitempty"`
	Outputs      []namedView       `json:"outputs,omitempty"`
	Locals       map[string]string `json:"locals,omitempty"`
	Providers    []namedView       `json:"providers,omitempty"`
	Environments []envView         `json:"environments,omitempty"`
	Diagnostics  []diagView        `json:"diagnostics,omitempty"`
	// Review summarizes the best-practice findings (review_terraform lists them).
	Review string `json:"review,omitempty"`
	// BestPractices are the findings a mutation introduced.
	BestPractices []bpView `json:"best_practices,omitempty"`
}

const redacted = "(redacted)"

func address(n *model.Node) string {
	switch n.Kind {
	case model.KindData:
		return "data." + n.Type + "." + n.Name
	case model.KindModule:
		return "module." + n.Name
	default:
		return n.Type + "." + n.Name
	}
}

func exprOf(v model.Value) string {
	if v.Expr != "" {
		return v.Expr
	}
	return string(v.Literal)
}

func attrsView(attrs []model.Attr) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[a.Name] = exprOf(a.Value)
	}
	return m
}

func blocksView(blocks []model.Block) []blockView {
	var out []blockView
	for _, b := range blocks {
		out = append(out, blockView{Type: b.Type, Labels: b.Labels, Attributes: attrsView(b.Body.Attrs), Blocks: blocksView(b.Body.Blocks)})
	}
	return out
}

func viewNode(wf *model.Workflow, n *model.Node) nodeView {
	file := wf.NodeFiles[n.ID]
	if file == "" {
		file = n.File
	}
	return nodeView{
		ID: n.ID, Address: address(n), Kind: n.Kind, Type: n.Type, Name: n.Name, Label: n.Label, File: file,
		Attributes: attrsView(n.Body.Attrs), Blocks: blocksView(n.Body.Blocks),
	}
}

func namedViews(nb []model.NamedBlock) []namedView {
	var out []namedView
	for _, b := range nb {
		out = append(out, namedView{Name: b.Name, Attributes: attrsView(b.Body.Attrs), Blocks: blocksView(b.Body.Blocks)})
	}
	return out
}

func addressByID(wf *model.Workflow) map[string]string {
	m := make(map[string]string, len(wf.Nodes))
	for i := range wf.Nodes {
		m[wf.Nodes[i].ID] = address(&wf.Nodes[i])
	}
	return m
}

// viewDiagnostics lists diags without best-practice findings (see
// review_terraform and bpDelta).
func viewDiagnostics(wf *model.Workflow, diags []model.Diagnostic) []diagView {
	addrs := addressByID(wf)
	out := []diagView{}
	for _, d := range diags {
		if isBestPractice(d) {
			continue
		}
		node := addrs[d.NodeID]
		if node == "" {
			node = d.NodeID
		}
		out = append(out, diagView{Severity: d.Severity, Code: d.Code, Message: d.Message, Detail: d.Detail, Node: node, Attribute: d.Attribute})
	}
	return out
}

func sensitiveVariables(wf *model.Workflow) map[string]bool {
	m := map[string]bool{}
	for _, v := range wf.Variables {
		for _, a := range v.Body.Attrs {
			if a.Name == "sensitive" && exprOf(a.Value) == "true" {
				m[v.Name] = true
			}
		}
	}
	return m
}

func viewEnvironments(wf *model.Workflow) []envView {
	sensitive := sensitiveVariables(wf)
	var out []envView
	for _, e := range wf.Environments {
		ev := envView{Name: e.Name, Active: e.Name == wf.ActiveEnvironment}
		if len(e.Values) > 0 {
			ev.Values = map[string]string{}
			for k, v := range e.Values {
				if sensitive[k] {
					ev.Values[k] = redacted
				} else {
					ev.Values[k] = exprOf(v)
				}
			}
		}
		if len(e.EnvVars) > 0 {
			ev.EnvVars = map[string]string{}
			for _, kv := range e.EnvVars {
				if kv.Secret {
					ev.EnvVars[kv.Key] = redacted
				} else {
					ev.EnvVars[kv.Key] = kv.Value
				}
			}
		}
		out = append(out, ev)
	}
	return out
}

func viewEdges(wf *model.Workflow) []string {
	addrs := addressByID(wf)
	var out []string
	for _, e := range wf.Edges {
		src := addrs[e.Source]
		if e.SourceAttr != "" {
			src += "." + e.SourceAttr
		}
		out = append(out, fmt.Sprintf("%s -> %s.%s", src, addrs[e.Target], e.Attribute))
	}
	return out
}

func viewWorkflow(wf *model.Workflow) workflowView {
	v := workflowView{
		ID: wf.ID, Name: wf.Name, Description: wf.Description, Clouds: wf.Clouds,
		Nodes: []nodeView{}, Edges: viewEdges(wf),
		Variables: namedViews(wf.Variables), Outputs: namedViews(wf.Outputs),
		Locals: attrsView(wf.Locals), Providers: namedViews(wf.Providers),
		Environments: viewEnvironments(wf), Diagnostics: viewDiagnostics(wf, wf.Diagnostics), Review: reviewSummary(wf),
	}
	for i := range wf.Nodes {
		v.Nodes = append(v.Nodes, viewNode(wf, &wf.Nodes[i]))
	}
	return v
}

// ---------------------------------------------------------------------------
// Resource schemas

type attrView struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Sensitive   bool     `json:"sensitive,omitempty"`
	Suggested   string   `json:"suggested,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type schemaBlockView struct {
	Required []attrView        `json:"required,omitempty"`
	Optional []attrView        `json:"optional,omitempty"`
	Exported []string          `json:"exported,omitempty"` // computed-only, readable as <address>.<name>
	Blocks   []nestedBlockView `json:"blocks,omitempty"`
}

type nestedBlockView struct {
	Name     string `json:"name"`
	Nesting  string `json:"nesting"`
	MinItems int    `json:"min_items,omitempty"`
	MaxItems int    `json:"max_items,omitempty"`
	schemaBlockView
}

type schemaView struct {
	Type           string         `json:"type"`
	Kind           model.NodeKind `json:"kind"`
	Cloud          model.Cloud    `json:"cloud"`
	Label          string         `json:"label"`
	Category       string         `json:"category"`
	FlociSupported bool           `json:"floci_supported"`
	Service        string         `json:"floci_service,omitempty"`
	Description    string         `json:"description,omitempty"`
	DocsURL        string         `json:"docs_url,omitempty"`
	schemaBlockView
}

func viewSchema(s *model.ResourceSchema) schemaView {
	return schemaView{
		Type: s.Type, Kind: s.Kind, Cloud: s.Cloud, Label: s.Label, Category: s.Category,
		FlociSupported: s.FlociSupported, Service: s.Service, Description: truncate(s.Description, 300),
		DocsURL: s.DocsURL, schemaBlockView: viewSchemaBlock(s.Block),
	}
}

func viewSchemaBlock(b model.SchemaBlock) schemaBlockView {
	var v schemaBlockView
	for _, a := range b.Attributes {
		if a.Deprecated {
			continue
		}
		av := attrView{Name: a.Name, Type: ctyType(a.Type), Description: truncate(a.Description, 160), Sensitive: a.Sensitive, Enum: a.Enum}
		if len(a.Default) > 0 {
			av.Suggested = string(a.Default)
		}
		switch {
		case a.Required:
			v.Required = append(v.Required, av)
		case a.Optional:
			v.Optional = append(v.Optional, av)
		case a.Computed:
			v.Exported = append(v.Exported, a.Name)
		}
	}
	for _, nb := range b.Blocks {
		v.Blocks = append(v.Blocks, nestedBlockView{Name: nb.Name, Nesting: nb.Nesting, MinItems: nb.MinItems, MaxItems: nb.MaxItems, schemaBlockView: viewSchemaBlock(nb.Block)})
	}
	return v
}

// ctyType renders a cty type JSON ("string", ["list","string"],
// ["object",{"a":"string"}]) in Terraform type syntax.
func ctyType(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "any"
	}
	var t any
	if err := json.Unmarshal(raw, &t); err != nil {
		return string(raw)
	}
	return renderCty(t)
}

func renderCty(t any) string {
	switch v := t.(type) {
	case string:
		if v == "dynamic" {
			return "any"
		}
		return v
	case []any:
		if len(v) != 2 {
			break
		}
		kind, _ := v[0].(string)
		switch kind {
		case "list", "set", "map":
			return kind + "(" + renderCty(v[1]) + ")"
		case "object":
			fields, _ := v[1].(map[string]any)
			names := make([]string, 0, len(fields))
			for k := range fields {
				names = append(names, k)
			}
			sort.Strings(names)
			parts := make([]string, len(names))
			for i, k := range names {
				parts[i] = k + "=" + renderCty(fields[k])
			}
			return "object({" + strings.Join(parts, ", ") + "})"
		case "tuple":
			elems, _ := v[1].([]any)
			parts := make([]string, len(elems))
			for i, e := range elems {
				parts[i] = renderCty(e)
			}
			return "tuple([" + strings.Join(parts, ", ") + "])"
		}
	}
	data, _ := json.Marshal(t)
	return string(data)
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// ---------------------------------------------------------------------------
// Runs, state and load tests

type runView struct {
	ID          string             `json:"id"`
	Action      model.RunAction    `json:"action"`
	Environment string             `json:"environment,omitempty"`
	Status      model.RunStatus    `json:"status"`
	ExitCode    int                `json:"exit_code"`
	Plan        *model.PlanSummary `json:"plan,omitempty"`
	Error       string             `json:"error,omitempty"`
	Log         []string           `json:"log_tail,omitempty"`
	Deployed    *stateView         `json:"deployed,omitempty"`
	Hint        string             `json:"hint,omitempty"`
}

const logTail = 60

func viewRun(r *model.Run) runView {
	v := runView{ID: r.ID, Action: r.Action, Environment: r.Environment, Status: r.Status, ExitCode: r.ExitCode, Plan: r.Plan, Error: r.Error}
	lines := r.Log
	if len(lines) > logTail {
		lines = lines[len(lines)-logTail:]
	}
	for _, l := range lines {
		v.Log = append(v.Log, l.Text)
	}
	return v
}

type resourceView struct {
	Address string         `json:"address"`
	Values  map[string]any `json:"values,omitempty"`
}

type stateView struct {
	Resources []resourceView `json:"resources"`
	Outputs   map[string]any `json:"outputs,omitempty"`
}

// keyValues are the state attributes worth showing in summaries.
var keyValues = []string{"id", "arn", "name", "bucket", "url", "function_name", "invoke_url", "api_endpoint", "endpoint", "fqdn", "repository_url", "dns_name"}

// viewState summarizes deployed resources. With full set, resources whose
// address starts with filter carry all values.
func viewState(st *model.DeployedState, filter string, full bool) stateView {
	v := stateView{Resources: []resourceView{}, Outputs: st.Outputs}
	for _, r := range st.Resources {
		if filter != "" && !strings.HasPrefix(r.Address, filter) {
			continue
		}
		rv := resourceView{Address: r.Address}
		if full {
			rv.Values = r.Values
		} else {
			for _, k := range keyValues {
				if s, ok := r.Values[k].(string); ok && s != "" {
					if rv.Values == nil {
						rv.Values = map[string]any{}
					}
					rv.Values[k] = s
				}
			}
		}
		v.Resources = append(v.Resources, rv)
	}
	return v
}

type loadRunView struct {
	ID         string                  `json:"id"`
	Status     model.LoadTestStatus    `json:"status"`
	Target     string                  `json:"target"`
	Profile    string                  `json:"profile"`
	Summary    *model.LoadSummary      `json:"summary,omitempty"`
	Thresholds []model.ThresholdResult `json:"thresholds,omitempty"`
	Error      string                  `json:"error,omitempty"`
	Hint       string                  `json:"hint,omitempty"`
}

func viewLoadRun(r *model.LoadTestRun) loadRunView {
	profile := r.Spec.Profile.Preset
	if r.Spec.Profile.Label != "" {
		profile = r.Spec.Profile.Label
	}
	return loadRunView{ID: r.ID, Status: r.Status, Target: r.Spec.Target.Label, Profile: profile, Summary: r.Summary, Thresholds: r.Thresholds, Error: r.Error}
}
