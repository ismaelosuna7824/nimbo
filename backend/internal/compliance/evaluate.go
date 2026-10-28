package compliance

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Status is the grade of one control.
type Status string

const (
	// StatusMet: no finding of the control's rules on the design.
	StatusMet Status = "met"
	// StatusViolated: a finding of the control's rules on any applicable
	// resource (or on the design as a whole): an auditor fails a control
	// when one resource fails it. Reason says how many resources fail.
	StatusViolated Status = "violated"
	// StatusNotApplicable: the design has none of the resources the control
	// is about.
	StatusNotApplicable Status = "not_applicable"
	// StatusNotEvaluated: one of the control's rules is off in Settings, so
	// its absence of findings proves nothing.
	StatusNotEvaluated Status = "not_evaluated"
	// StatusManual: the design can't show it; the user hasn't attested it.
	StatusManual Status = "manual"
	// StatusAttested: a manual control the user marked as done.
	StatusAttested Status = "attested"
)

// Statuses lists every status in display order: what needs attention first.
var Statuses = []Status{StatusViolated, StatusManual, StatusMet, StatusAttested, StatusNotEvaluated, StatusNotApplicable}

// Resource is one design object a control applies to.
type Resource struct {
	NodeID string `json:"nodeId"`
	// Address names it for people: "aws_s3_bucket.logs" (Terraform), the
	// service/network/volume name (docker), "Deployment/shop/api"
	// (Kubernetes).
	Address string `json:"address"`
	Type    string `json:"type"`
	Failing bool   `json:"failing,omitempty"`
	// node is the Terraform node (nil for docker and Kubernetes), for Control.AppliesToNode.
	node *model.Node
}

// Finding is the evidence against a control: one finding of its rules.
type Finding struct {
	NodeID   string         `json:"nodeId,omitempty"`
	Address  string         `json:"address,omitempty"`
	Code     string         `json:"code"`
	Severity model.Severity `json:"severity"`
	Message  string         `json:"message"`
	Fix      string         `json:"fix,omitempty"`
}

// ControlResult is the grade of one control with its evidence.
type ControlResult struct {
	Control
	Status Status `json:"status"`
	// Reason explains statuses that aren't a verdict (not applicable, not
	// evaluated) in plain words.
	Reason      string       `json:"reason,omitempty"`
	Resources   []Resource   `json:"resources"`
	Findings    []Finding    `json:"findings"`
	Attestation *Attestation `json:"attestation,omitempty"`
}

// FrameworkInfo is a framework without its controls.
type FrameworkInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Source   string `json:"source"`
}

// Info returns f without its controls.
func (f Framework) Info() FrameworkInfo {
	return FrameworkInfo{ID: f.ID, Name: f.Name, Version: f.Version, Platform: f.Platform, Source: f.Source}
}

// Result grades a workflow against one framework.
type Result struct {
	Framework FrameworkInfo   `json:"framework"`
	Controls  []ControlResult `json:"controls"`
	Counts    map[Status]int  `json:"counts"`
	// Graded counts the automatic controls with a verdict (met, violated);
	// Met those met: the "Met / Graded automatic controls met" summary. Not
	// applicable and not evaluated ones are left out.
	Graded int `json:"graded"`
	Met    int `json:"met"`
}

// Platforms returns the framework platforms that apply to wf: its clouds'
// Terraform providers, or "docker" / "kubernetes".
func Platforms(wf *model.Workflow) []string {
	switch {
	case wf.IsDocker():
		return []string{"docker"}
	case wf.IsKubernetes():
		return []string{"kubernetes"}
	}
	out := []string{}
	for _, c := range wf.Clouds {
		if p, ok := cloudPlatform[c]; ok {
			out = append(out, p)
		}
	}
	return out
}

// cloudPlatform maps a cloud to its Terraform provider name, the
// Framework.Platform of its benchmarks.
var cloudPlatform = map[model.Cloud]string{
	model.CloudAWS:   "aws",
	model.CloudAzure: "azurerm",
	model.CloudGCP:   "google",
	model.CloudOCI:   "oci",
}

// ForWorkflow returns the registered frameworks that apply to wf.
func ForWorkflow(wf *model.Workflow) []Framework {
	platforms := Platforms(wf)
	out := []Framework{}
	for _, f := range Frameworks() {
		if slices.Contains(platforms, f.Platform) {
			out = append(out, f)
		}
	}
	return out
}

// Lookup returns the registered framework id.
func Lookup(id string) (Framework, bool) {
	mu.RLock()
	defer mu.RUnlock()
	i := slices.IndexFunc(frameworks, func(f Framework) bool { return f.ID == id })
	if i < 0 {
		return Framework{}, false
	}
	return frameworks[i], true
}

// Evaluate grades wf against f from diags (wf's diagnostics, computed under
// GradingSettings(cfg, f)), cfg (the user's settings: which rules are off)
// and the workflow's attestations.
func Evaluate(f Framework, wf *model.Workflow, diags []model.Diagnostic, cfg model.BestPracticeSettings, att Attestations) Result {
	res := resources(wf)
	byID := map[string]*Resource{}
	for i := range res.list {
		byID[res.list[i].NodeID] = &res.list[i]
	}
	out := Result{Framework: f.Info(), Controls: make([]ControlResult, 0, len(f.Controls)), Counts: map[Status]int{}}
	for _, s := range Statuses {
		out.Counts[s] = 0
	}
	for _, c := range f.Controls {
		cr := grade(c, res, byID, diags, cfg)
		if cr.Status == StatusManual {
			if a, ok := att[Key(f.ID, c.ID)]; ok {
				cr.Status, cr.Attestation = StatusAttested, &a
			}
		}
		out.Counts[cr.Status]++
		switch cr.Status {
		case StatusMet:
			out.Met++
			out.Graded++
		case StatusViolated:
			out.Graded++
		}
		out.Controls = append(out.Controls, cr)
	}
	return out
}

func grade(c Control, res designResources, byID map[string]*Resource, diags []model.Diagnostic, cfg model.BestPracticeSettings) ControlResult {
	cr := ControlResult{Control: c, Resources: []Resource{}, Findings: []Finding{}}
	typed := 0
	applies := func(r *Resource) bool {
		if len(c.AppliesTo) == 0 {
			return true
		}
		if !slices.Contains(c.AppliesTo, r.Type) {
			return false
		}
		typed++
		return c.AppliesToNode == nil || r.node == nil || c.AppliesToNode(r.node)
	}
	for _, r := range res.list {
		if applies(&r) {
			cr.Resources = append(cr.Resources, r)
		}
	}
	off := ruleOff(c.Rules, cfg)
	switch {
	case len(cr.Resources) == 0 && typed > 0:
		cr.Status = StatusNotApplicable
		cr.Reason = "No " + strings.Join(c.AppliesTo, ", ") + " in this design is the kind this control is about."
		return cr
	case len(cr.Resources) == 0 && len(c.AppliesTo) > 0:
		cr.Status = StatusNotApplicable
		cr.Reason = "Nothing in this design is a " + strings.Join(c.AppliesTo, ", ") + "."
		return cr
	case len(cr.Resources) == 0 && !c.Manual:
		cr.Status = StatusNotApplicable
		cr.Reason = "The design is empty."
		return cr
	case c.Manual:
		cr.Status = StatusManual
		return cr
	case off != "":
		cr.Status = StatusNotEvaluated
		cr.Reason = off
		return cr
	}
	failing := map[string]bool{}
	designWide := false
	for _, d := range diags {
		if !slices.Contains(c.Rules, d.Code) || c.Counts != nil && !c.Counts(d) {
			continue
		}
		owner := res.owner[d.NodeID]
		r := byID[owner]
		switch {
		case d.NodeID == "":
			designWide = true
		case r == nil || !applies(r):
			// A finding on something the control isn't about (a data
			// source, a module call, another resource type): a rule can
			// serve several controls, each for its own types.
			continue
		default:
			failing[owner] = true
		}
		f := Finding{NodeID: owner, Code: d.Code, Severity: d.Severity, Message: d.Message, Fix: d.Fix}
		if r != nil {
			f.Address = r.Address
		}
		cr.Findings = append(cr.Findings, f)
	}
	for i := range cr.Resources {
		cr.Resources[i].Failing = failing[cr.Resources[i].NodeID]
	}
	switch n := len(failing); {
	case n > 0:
		cr.Status = StatusViolated
		cr.Reason = fmt.Sprintf("%d of %d resources have findings.", n, len(cr.Resources))
	case designWide:
		cr.Status = StatusViolated
		cr.Reason = "A finding concerns the design as a whole."
	default:
		cr.Status = StatusMet
	}
	return cr
}

// ruleOff explains why one of rules doesn't run under cfg ("" when they all
// run): a control graded on a rule that is off would look met with no
// evidence. Opt-in rules don't count as off: GradingSettings runs them.
func ruleOff(rules []string, cfg model.BestPracticeSettings) string {
	if len(rules) == 0 {
		return ""
	}
	if cfg.Disabled {
		return "Best-practice checks are turned off in Settings."
	}
	for _, code := range rules {
		if slices.Contains(cfg.DisabledRules, code) {
			return "Rule " + code + " is turned off in Settings → Best-practice checks."
		}
	}
	return ""
}

// GradingSettings are the settings to compute the diagnostics Evaluate
// grades on: cfg plus every rule the frameworks map, opt-in (DefaultOff)
// ones included. Opt-in rules stay quiet in Problems, but a benchmark
// control needs its rule to run to be graded at all; rules the user turned
// off stay off (their controls are not evaluated).
func GradingSettings(cfg model.BestPracticeSettings, fws ...Framework) model.BestPracticeSettings {
	out := cfg
	out.DisabledRules = slices.Clone(cfg.DisabledRules)
	out.EnabledRules = slices.Clone(cfg.EnabledRules)
	for _, f := range fws {
		for _, c := range f.Controls {
			for _, code := range c.Rules {
				if !slices.Contains(out.EnabledRules, code) {
					out.EnabledRules = append(out.EnabledRules, code)
				}
			}
		}
	}
	return out
}

// designResources are the objects of a design controls apply to, and the
// owner resource of every node ID findings can name.
type designResources struct {
	list []Resource
	// owner maps a node ID to the resource findings on it count against:
	// itself, or the service a Dockerfile build stage belongs to.
	owner map[string]string
}

func resources(wf *model.Workflow) designResources {
	out := designResources{list: []Resource{}, owner: map[string]string{}}
	add := func(r Resource) {
		out.list = append(out.list, r)
		out.owner[r.NodeID] = r.NodeID
	}
	switch {
	case wf.IsDocker():
		if wf.Compose == nil {
			return out
		}
		for _, n := range wf.Compose.Graph.Nodes {
			switch n.Kind {
			case model.ComposeBuildStage:
				out.owner[n.ID] = n.Parent
			case model.ComposeExternal:
			default:
				add(Resource{NodeID: n.ID, Address: n.Name, Type: n.Kind})
			}
		}
	case wf.IsKubernetes():
		if wf.Kubernetes == nil {
			return out
		}
		for _, n := range wf.Kubernetes.Graph.Nodes {
			add(Resource{NodeID: n.ID, Address: n.ID, Type: n.Kind})
		}
	default:
		for i := range wf.Nodes {
			if n := &wf.Nodes[i]; n.Kind == model.KindResource {
				add(Resource{NodeID: n.ID, Address: n.Type + "." + n.Name, Type: n.Type, node: n})
			}
		}
	}
	return out
}
