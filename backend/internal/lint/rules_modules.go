package lint

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Module rules look into the local modules the workflow calls (read by
// LocalModules). Findings sit on the root module call the module is reached
// through; registry and remote modules are third-party code and only their
// call (source, version) is checked.

const maxModuleNesting = 2 // AWS: keep module trees 1–2 levels deep

// moduleName names a module directory in messages ("modules/app").
func moduleName(c moduleCall) string {
	if c.dir == "." {
		return "the module in the root directory"
	}
	return c.dir
}

func checkProviderInModule(p *pass) {
	for _, c := range p.localModules() {
		var names []string
		for _, pb := range c.mod.Providers {
			if proxyProvider(&pb.Body) {
				continue
			}
			name := pb.Name
			if alias, ok := attrString(&pb.Body, "alias"); ok && alias != "" {
				name += "." + alias
			}
			names = append(names, fmt.Sprintf("%q", name))
		}
		if len(names) == 0 {
			continue
		}
		p.report(finding{
			node:    c.root,
			message: fmt.Sprintf("%s configures provider %s", moduleName(c), strings.Join(names, ", ")),
			why:     "Provider configuration belongs to the root (HashiCorp, AWS Prescriptive Guidance): a module with its own provider block can't be reused for another region or account, can't be called with count, for_each or depends_on, and can't be removed from the configuration without breaking the destroy of its resources.",
			fix:     fmt.Sprintf("Move the provider block(s) of %s to the root (providers.tf). In the module keep only required_providers with a minimum version (>=), adding configuration_aliases for aliased providers, and pass aliases from the call with providers = { aws.west = aws.west }.", moduleName(c)),
		})
	}
}

// proxyProvider is an empty provider block or one with only an alias (the
// pre-0.15 way to declare an alias the caller passes in).
func proxyProvider(b *model.Body) bool {
	if len(b.Blocks) > 0 {
		return false
	}
	for _, a := range b.Attrs {
		if a.Name != "alias" {
			return false
		}
	}
	return true
}

// branchRefs are git refs that name a moving branch rather than a release.
var branchRefs = map[string]bool{"main": true, "master": true, "HEAD": true, "develop": true, "trunk": true}

func checkModuleVersionUnpinned(p *pass) {
	check := func(nodes []model.Node, where string, report func(nd *model.Node, msg, attr string)) {
		for i := range nodes {
			nd := &nodes[i]
			if nd.Kind != model.KindModule {
				continue
			}
			src, version := moduleSource(nd)
			switch {
			case src == "":
			case isRegistrySource(src) && strings.TrimSpace(version) == "":
				report(nd, fmt.Sprintf("module.%s%s uses registry module %s without a version", nd.Name, where, src), "version")
			case isGitSource(src):
				ref := gitRef(src)
				if ref == "" {
					report(nd, fmt.Sprintf("module.%s%s uses %s without ?ref=", nd.Name, where, src), "source")
				} else if branchRefs[ref] {
					report(nd, fmt.Sprintf("module.%s%s pins %s to the branch %q", nd.Name, where, src, ref), "source")
				}
			}
		}
	}
	const why = "An unpinned module changes under you: the next init downloads whatever the registry or branch has now, so a plan can suddenly replace resources and two people (or CI) can run different module code (HashiCorp style guide: pin module versions)."
	const fix = "Registry modules: add version = \"~> X.Y\" (a minor-version pin of the release you tested). Git modules: append ?ref=<tag> (e.g. ?ref=v1.4.0) or a commit SHA, never a branch."
	check(p.wf.Nodes, "", func(nd *model.Node, msg, attr string) {
		p.report(finding{node: nd, attribute: attr, message: msg, why: why, fix: fix})
	})
	for _, c := range p.localModules() {
		check(c.mod.Nodes, " in "+moduleName(c), func(_ *model.Node, msg, _ string) {
			p.report(finding{node: c.root, message: msg, why: why, fix: fix})
		})
	}
}

// gitRef returns the ref query parameter of a git module source.
func gitRef(src string) string {
	_, query, ok := strings.Cut(src, "?")
	if !ok {
		return ""
	}
	q, err := url.ParseQuery(query)
	if err != nil {
		return ""
	}
	return q.Get("ref")
}

// checkModuleThinWrapper flags single-resource modules the root calls.
// Submodules a module composes internally (terraform-aws-rds wraps its
// parameter, option and subnet groups) are that module's design: the
// caller gets the composition.
func checkModuleThinWrapper(p *pass) {
	fromRoot := map[string]bool{}
	for _, c := range p.localCalls() {
		if c.depth == 1 {
			fromRoot[c.dir] = true
		}
	}
	for _, c := range p.localModules() {
		if !fromRoot[c.dir] {
			continue
		}
		var res []*model.Node
		calls := 0
		for i := range c.mod.Nodes {
			switch c.mod.Nodes[i].Kind {
			case model.KindResource:
				res = append(res, &c.mod.Nodes[i])
			case model.KindModule:
				calls++
			}
		}
		if len(res) != 1 || calls > 0 {
			continue
		}
		p.report(finding{
			node:    c.root,
			message: fmt.Sprintf("%s wraps a single resource (%s)", moduleName(c), addr(res[0])),
			why:     "A module around one resource only renames its arguments: it adds variables and outputs to keep in sync without grouping anything (AWS Prescriptive Guidance: don't wrap single resources; encapsulate logical groups such as the network foundation, the data tier, security controls or an application).",
			fix:     fmt.Sprintf("Either move the resources that work with %s (its policies, alarms, attachments) into %s so it becomes a logical group, or bring the resource back to the root with inline_module (single call without count/for_each; moved blocks keep it deployed).", addr(res[0]), moduleName(c)),
		})
	}
}

// checkModuleNestingDepth flags local module trees deeper than
// maxModuleNesting levels as exported: the environments layout wraps the
// workflow in modules/<service>, called from each environment's root, so
// there every level of the workflow is one level deeper.
func checkModuleNestingDepth(p *pass) {
	layout, _ := exportLayout(p.wf)
	wrapped := layout == model.LayoutEnvironments
	limit := maxModuleNesting
	if wrapped {
		limit--
	}
	deepest := map[string]moduleCall{}
	var order []string
	for _, c := range p.localCalls() {
		if c.depth <= limit || c.root == nil {
			continue
		}
		root := rootCall(c.path)
		prev, seen := deepest[root]
		if !seen {
			order = append(order, root)
		}
		if !seen || c.depth > prev.depth {
			deepest[root] = c
		}
	}
	for _, root := range order {
		c := deepest[root]
		msg := fmt.Sprintf("%s nests local modules %d levels deep (%s)", root, c.depth, c.path)
		if wrapped {
			msg = fmt.Sprintf("%s nests local modules %d levels deep (%s), %d once the environments layout wraps the workflow in its service module", root, c.depth, c.path, c.depth+1)
		}
		p.report(finding{
			node:    c.root,
			message: msg,
			why:     fmt.Sprintf("Deep module trees hide where a resource lives, make addresses long and turn every refactor into moved blocks at several levels (AWS Prescriptive Guidance: keep nesting to 1–2 levels and compose modules from the root). Nimbo recommends at most %d levels below a root.", maxModuleNesting),
			fix:     "Flatten the tree: call the inner modules from the workflow's root and pass the outputs of one as the inputs of the next.",
		})
	}
}

// checkModuleOutputMissing flags resources of a local module that callers
// can't reach: no output references them, nor anything they are attached
// to. A security group rule, a topic policy or a route table association
// counts as reached through the group, topic or table it attaches to
// (published modules output the parent, not each attachment).
func checkModuleOutputMissing(p *pass) {
	for _, c := range p.localModules() {
		reached := reachedFromOutputs(c.mod)
		var missing []*model.Node
		for i := range c.mod.Nodes {
			n := &c.mod.Nodes[i]
			if n.Kind != model.KindResource || reached[n.ID] || !p.exportsAttributes(n) {
				continue
			}
			missing = append(missing, n)
		}
		if len(missing) == 0 {
			continue
		}
		first := missing[0]
		p.report(finding{
			node:    c.root,
			message: fmt.Sprintf("%s: no output reaches %s", moduleName(c), list(missing)),
			why:     "Callers can only build on what a module outputs: without an output for a resource (or for the resource it is attached to) they can't read its ID or ARN, and can't depend on it implicitly (AWS Prescriptive Guidance: every resource in a module is referenced by at least one output).",
			fix:     fmt.Sprintf("Add a described output for each, e.g. in %s/outputs.tf: output \"%s_id\" { value = %s.id  description = \"...\" } (ARNs or names when those are what callers use).", moduleName(c), first.Name, addr(first)),
		})
	}
}

// reachedFromOutputs returns the IDs of mod's nodes connected to what its
// outputs reference. References (direct or through locals) between managed
// resources link them both ways, and so do data sources that read a
// managed resource (a policy document built from a bucket ARN). Data
// sources that read none (data.aws_region) don't connect anything.
func reachedFromOutputs(mod *model.Workflow) map[string]bool {
	refs := newRefResolver(mod)
	byAddr := map[string]*model.Node{}
	for i := range mod.Nodes {
		byAddr[addr(&mod.Nodes[i])] = &mod.Nodes[i]
	}
	uses := map[string][]*model.Node{} // node ID -> nodes it references
	connector := map[string]bool{}
	for i := range mod.Nodes {
		n := &mod.Nodes[i]
		for a := range refs.body(&n.Body) {
			if m := byAddr[a]; m != nil && m != n {
				uses[n.ID] = append(uses[n.ID], m)
				if n.Kind == model.KindData && m.Kind == model.KindResource {
					connector[n.ID] = true
				}
			}
		}
	}
	links := func(n *model.Node) bool { return n.Kind == model.KindResource || connector[n.ID] }
	adj := map[string][]string{}
	for i := range mod.Nodes {
		n := &mod.Nodes[i]
		for _, m := range uses[n.ID] {
			if links(n) && links(m) {
				adj[n.ID] = append(adj[n.ID], m.ID)
				adj[m.ID] = append(adj[m.ID], n.ID)
			}
		}
	}
	reached := map[string]bool{}
	var queue []string
	for _, o := range mod.Outputs {
		for a := range refs.body(&o.Body) {
			if n := byAddr[a]; n != nil && !reached[n.ID] {
				reached[n.ID] = true
				queue = append(queue, n.ID)
			}
		}
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, next := range adj[id] {
			if !reached[next] {
				reached[next] = true
				queue = append(queue, next)
			}
		}
	}
	return reached
}

// refResolver turns the references of a body into the block addresses they
// read ("aws_s3_bucket.b", "data.aws_iam_policy_document.p",
// "module.db"), following locals.
type refResolver struct {
	locals map[string][]string
	cache  map[string]map[string]bool // local name -> addresses
}

func newRefResolver(wf *model.Workflow) *refResolver {
	r := &refResolver{locals: map[string][]string{}, cache: map[string]map[string]bool{}}
	for _, l := range wf.Locals {
		r.locals[l.Name] = references(l.Value)
	}
	return r
}

// body returns the addresses body reads (nested blocks included).
func (r *refResolver) body(b *model.Body) map[string]bool {
	out := map[string]bool{}
	walkAttrs(b, "", func(_ string, a *model.Attr) {
		for _, ref := range references(a.Value) {
			r.add(out, ref, map[string]bool{})
		}
	})
	return out
}

func (r *refResolver) add(out map[string]bool, ref string, visiting map[string]bool) {
	parts := strings.Split(ref, ".")
	switch {
	case parts[0] == "local" && len(parts) >= 2:
		name := parts[1]
		if cached, ok := r.cache[name]; ok {
			for a := range cached {
				out[a] = true
			}
			return
		}
		if visiting[name] {
			return
		}
		visiting[name] = true
		got := map[string]bool{}
		for _, inner := range r.locals[name] {
			r.add(got, inner, visiting)
		}
		r.cache[name] = got
		for a := range got {
			out[a] = true
		}
	case parts[0] == "data" && len(parts) >= 3:
		out[strings.Join(parts[:3], ".")] = true
	case parts[0] == "var" || parts[0] == "path" || parts[0] == "terraform" || parts[0] == "each" || parts[0] == "count" || parts[0] == "self":
	case len(parts) >= 2:
		out[parts[0]+"."+parts[1]] = true
	}
}

// exportsAttributes reports whether a resource type exports something worth
// an output: a computed-only attribute besides id and tags_all (an ARN, a
// URL, a name). Attachments and associations that only export id don't.
func (p *pass) exportsAttributes(n *model.Node) bool {
	s, ok := p.cat.Schema(n.Kind, n.Type)
	if !ok {
		return false
	}
	for _, a := range s.Block.Attributes {
		if a.Computed && !a.Optional && !a.Required && a.Name != "id" && a.Name != "tags_all" {
			return true
		}
	}
	return false
}
