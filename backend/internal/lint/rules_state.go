package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Google Cloud: keep a state under 100 resources, ideally a few dozen.
const (
	stateResourcesInfo    = 50
	stateResourcesWarning = 100
)

// Structure summarizes how wf's Terraform is organized: layout, states,
// module tree (mods from LocalModules; nil = no local module content) and
// backend.
func Structure(wf *model.Workflow, mods *model.ModuleTree) model.StructureSummary {
	layout, note := exportLayout(wf)
	s := model.StructureSummary{
		Layout:        layout,
		LayoutSetting: wf.Layout,
		LayoutNote:    note,
		Environments:  []string{},
		Modules:       []model.StructureModule{},
		States:        1,
		Backend:       backendType(wf),
	}
	for _, e := range wf.Environments {
		s.Environments = append(s.Environments, e.Name)
	}
	if layout == model.LayoutEnvironments {
		s.Service = hclcodec.ServiceName(wf)
		s.States = len(hclcodec.EnvironmentNames(wf))
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].Kind == model.KindResource {
			s.RootResources += instances(&wf.Nodes[i].Body)
		}
	}
	s.ResourcesPerState = countResources(wf.Nodes, "", mods, func(call string, c model.ModuleCallSource, inside int) {
		m := model.StructureModule{Call: call, Source: c.Source, Version: c.Version, Kind: moduleKind(c), Depth: callDepth(call)}
		if c.Local && c.Dir != "" {
			m.Dir, m.Resources = c.Dir, inside
			s.MaxNesting = max(s.MaxNesting, m.Depth)
		}
		s.Modules = append(s.Modules, m)
	})
	sort.Slice(s.Modules, func(i, j int) bool { return s.Modules[i].Call < s.Modules[j].Call })
	switch {
	case s.Layout == model.LayoutEnvironments:
		backend := "a remote backend per cloud (partial configuration: fill backend.hcl)"
		if s.Backend != "" {
			backend = fmt.Sprintf("the workflow's %s backend with a key per environment", s.Backend)
		}
		s.State = fmt.Sprintf("Export writes modules/%s and environments/<env>/ roots: one state per environment, each with backend.tf using %s.", s.Service, backend)
	case s.Backend != "":
		s.State = fmt.Sprintf("One root with the %s backend: every environment uses the same state unless each init passes its own -backend-config key.", s.Backend)
	case len(wf.Environments) > 1:
		s.State = "One root without a backend: every environment shares one local terraform.tfstate. Use the environments layout to give each its own state."
	default:
		s.State = "One root without a backend: local terraform.tfstate (add a remote backend before sharing the code)."
	}
	return s
}

// exportLayout is the layout export writes for wf (hclcodec.ResolveLayout)
// and, when that is not what the setting asks for, why: auto falls back to
// the single layout when the workflow can't be split into environments, and
// an explicit environments layout that can't be written fails the export.
func exportLayout(wf *model.Workflow) (layout, note string) {
	layout, err := hclcodec.ResolveLayout(wf)
	if err != nil {
		return model.LayoutEnvironments, "export fails until this is fixed: " + err.Error()
	}
	if layout != wf.EffectiveLayout() {
		if err := hclcodec.CheckEnvironmentsLayout(wf); err != nil {
			return layout, "auto uses the single layout because " + err.Error()
		}
	}
	return layout, ""
}

func moduleKind(c model.ModuleCallSource) string {
	switch {
	case c.Local:
		return "local"
	case isRegistrySource(c.Source):
		return "registry"
	}
	return "remote"
}

// countResources returns the managed resource instances of nodes plus those
// of the local modules they call (path prefix callPrefix, "" for the root).
// visit sees every module call with the instances inside one call.
func countResources(nodes []model.Node, callPrefix string, mods *model.ModuleTree, visit func(call string, c model.ModuleCallSource, inside int)) int {
	total := 0
	for i := range nodes {
		nd := &nodes[i]
		switch nd.Kind {
		case model.KindResource:
			total += instances(&nd.Body)
		case model.KindModule:
			call := callPrefix + "module." + nd.Name
			var c model.ModuleCallSource
			if mods != nil {
				c = mods.Calls[call]
			}
			if c.Source == "" {
				src, version := moduleSource(nd)
				c = model.ModuleCallSource{Source: src, Version: version, Local: hclcodec.IsLocalModuleSource(src)}
			}
			inside := 0
			if c.Local && c.Dir != "" && mods.Modules[c.Dir] != nil {
				inside = countResources(mods.Modules[c.Dir].Nodes, call+".", mods, visit)
			}
			if visit != nil {
				visit(call, c, inside)
			}
			total += instances(&nd.Body) * inside
		}
	}
	return total
}

// backendType is the backend of the root's terraform block ("s3"; "cloud"
// for HCP Terraform), "" when there is none.
func backendType(wf *model.Workflow) string {
	if wf.Terraform == nil {
		return ""
	}
	for _, b := range wf.Terraform.Blocks {
		switch {
		case b.Type == "backend" && len(b.Labels) > 0:
			return b.Labels[0]
		case b.Type == "cloud":
			return "cloud"
		}
	}
	return ""
}

func checkNoRemoteBackend(p *pass) {
	envs := p.wf.Environments
	if len(envs) < 2 || backendType(p.wf) != "" {
		return
	}
	layout, note := exportLayout(p.wf)
	if layout != model.LayoutSingle {
		return
	}
	names := make([]string, 0, len(envs))
	for _, e := range envs {
		names = append(names, e.Name)
	}
	fix := "Switch to the environments layout (update_workflow with layout \"environments\", or \"auto\"): export writes modules/<service> plus environments/<env>/ roots, each with its own backend.tf and state. Or add a remote backend (terraform { backend \"s3\" {} }) and give every environment its own key with -backend-config."
	if note != "" {
		fix = "The environments layout is not possible yet: " + note + ". Fix that (rename the environment, or move what the provider configuration reads into variables) so auto picks the environments layout, or add a remote backend (terraform { backend \"s3\" {} }) and give every environment its own key with -backend-config."
	}
	p.report(finding{
		message: fmt.Sprintf("%d environments (%s) share one root without a backend", len(envs), strings.Join(names, ", ")),
		why:     "In the single layout every environment runs the same root: without a backend, plans and applies for each environment read and write one local terraform.tfstate, so applying one environment can replace or destroy the resources of another; local state also has no locking, encryption or history (HashiCorp, Google: one state per environment).",
		fix:     fix,
	})
}

func checkStateTooLarge(p *pass) {
	n := countResources(p.wf.Nodes, "", p.mods, nil)
	if n < stateResourcesInfo {
		return
	}
	sev := model.SeverityInfo
	if n >= stateResourcesWarning {
		sev = model.SeverityWarning
	}
	scope := "This workflow's state"
	if layout, _ := exportLayout(p.wf); layout == model.LayoutEnvironments && len(p.wf.Environments) > 1 {
		scope = "Each environment's state"
	}
	var node *model.Node
	if res := p.resources(); len(res) > 0 {
		node = res[0]
	}
	p.report(finding{
		severity: sev,
		node:     node,
		message:  fmt.Sprintf("%s manages about %d resource instances", scope, n),
		why:      fmt.Sprintf("Google Cloud recommends at most %d resources per state, ideally a few dozen: big states make every plan slow, lock everyone on one state and widen the blast radius of each apply (a network change and an app deploy share one plan).", stateResourcesWarning),
		fix:      "Split by layer into separate workflows, each with its own state: network (VPC, subnets, gateways), data (databases, caches, buckets) and app (compute, APIs, queues). Upper layers find the lower ones with data sources by tags or IDs (data.aws_vpc / data.aws_subnets with tag filters) instead of sharing state.",
	})
}

func checkWorkspaceBranching(p *pass) {
	var places []string
	var node *model.Node
	usesWorkspace := func(b *model.Body) bool {
		found := false
		walkAttrs(b, "", func(_ string, a *model.Attr) {
			if !found && referencesWorkspace(a.Value) {
				found = true
			}
		})
		return found
	}
	scan := func(wf *model.Workflow, prefix string, onNode func(*model.Node)) {
		for _, l := range wf.Locals {
			if referencesWorkspace(l.Value) {
				places = append(places, prefix+"local."+l.Name)
			}
		}
		for i := range wf.Nodes {
			if n := &wf.Nodes[i]; usesWorkspace(&n.Body) {
				places = append(places, prefix+addr(n))
				onNode(n)
			}
		}
		for _, list := range [][]model.NamedBlock{wf.Outputs, wf.Providers} {
			for i := range list {
				if usesWorkspace(&list[i].Body) {
					places = append(places, prefix+list[i].Name)
				}
			}
		}
	}
	scan(p.wf, "", func(n *model.Node) {
		if node == nil {
			node = n
		}
	})
	for _, c := range p.localModules() {
		scan(c.mod, moduleName(c)+": ", func(*model.Node) {
			if node == nil {
				node = c.root
			}
		})
	}
	if len(places) == 0 {
		return
	}
	shown := places
	if len(shown) > 3 {
		shown = append(append([]string{}, places[:3]...), fmt.Sprintf("%d more", len(places)-3))
	}
	p.report(finding{
		node:    node,
		message: "terraform.workspace is used in " + strings.Join(shown, ", "),
		why:     "CLI workspaces share one configuration and backend, so environments differ only through hidden state and conditionals on terraform.workspace: reviewers can't see what each environment gets, and a run in the wrong workspace changes the wrong environment (Google Cloud: use only the default workspace; HashiCorp: a directory per environment).",
		fix:     "Give each environment its values with set_environment (one terraform.tfvars per environment) and read var.environment instead of terraform.workspace; the environments layout gives each environment its own root and state.",
	})
}

func referencesWorkspace(v model.Value) bool {
	for _, r := range references(v) {
		if r == "terraform.workspace" {
			return true
		}
	}
	return false
}
