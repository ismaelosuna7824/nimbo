package lint

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// maxModuleDepth bounds nested local module calls (modules calling each
// other in a cycle would never end).
const maxModuleDepth = 8

// LocalModules reads the local modules wf calls, nested calls included, into
// a model.ModuleTree: Calls maps every call path ("module.app",
// "module.app.module.db") to its source, Modules holds each local module
// directory read (slash path relative to read's base, "modules/app"), parsed
// once and normalized. dir is the root's own directory relative to that base
// ("." for a workflow's files). read returns a directory's top-level *.tf
// files (none when it does not exist). Local sources that leave the base are
// recorded without content; registry and remote modules are never read.
func LocalModules(wf *model.Workflow, dir string, read func(dir string) (map[string][]byte, error)) (*model.ModuleTree, error) {
	tree := &model.ModuleTree{Calls: map[string]model.ModuleCallSource{}, Modules: map[string]*model.Workflow{}}
	parsed := map[string]bool{}
	var walk func(nodes []model.Node, callPrefix, from string, depth int) error
	walk = func(nodes []model.Node, callPrefix, from string, depth int) error {
		for i := range nodes {
			nd := &nodes[i]
			if nd.Kind != model.KindModule {
				continue
			}
			src, version := moduleSource(nd)
			call := model.ModuleCallSource{Source: src, Version: version, Local: hclcodec.IsLocalModuleSource(src)}
			callPath := callPrefix + "module." + nd.Name
			if call.Local {
				d := hclcodec.ResolveLocalSource(from, src)
				if d != ".." && !strings.HasPrefix(d, "../") {
					if !parsed[d] {
						parsed[d] = true
						files, err := read(d)
						if err != nil {
							return err
						}
						if len(files) > 0 {
							m, _, err := hclcodec.Import(callPath, files)
							if err != nil {
								return err
							}
							hclcodec.Normalize(m)
							tree.Modules[d] = m
						}
					}
					if m := tree.Modules[d]; m != nil {
						call.Dir = d
						if depth < maxModuleDepth {
							if err := walk(m.Nodes, callPath+".", d, depth+1); err != nil {
								return err
							}
						}
					}
				}
			}
			tree.Calls[callPath] = call
		}
		return nil
	}
	if err := walk(wf.Nodes, "", path.Clean(dir), 1); err != nil {
		return nil, err
	}
	return tree, nil
}

// HasLocalModules reports whether wf calls a local module (so LocalModules
// has something to read).
func HasLocalModules(wf *model.Workflow) bool {
	for i := range wf.Nodes {
		if nd := &wf.Nodes[i]; nd.Kind == model.KindModule {
			if src, _ := moduleSource(nd); hclcodec.IsLocalModuleSource(src) {
				return true
			}
		}
	}
	return false
}

// moduleSource returns the constant source and version of a module call.
func moduleSource(nd *model.Node) (source, version string) {
	source, _ = attrString(&nd.Body, "source")
	version, _ = attrString(&nd.Body, "version")
	return source, version
}

// moduleCall is one call of a local module whose content was read.
type moduleCall struct {
	path  string          // "module.app.module.db"
	depth int             // 1 = called from the root
	dir   string          // "modules/app"
	mod   *model.Workflow // the module's content
	root  *model.Node     // the root module call the path starts at
}

// localCalls returns the calls of local modules with readable content,
// sorted by path.
func (p *pass) localCalls() []moduleCall {
	if p.mods == nil {
		return nil
	}
	var out []moduleCall
	for callPath, c := range p.mods.Calls {
		m := p.mods.Modules[c.Dir]
		if !c.Local || c.Dir == "" || m == nil {
			continue
		}
		out = append(out, moduleCall{path: callPath, depth: callDepth(callPath), dir: c.Dir, mod: m, root: p.byAddr[rootCall(callPath)]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// localModules returns each local module directory once (its first call by
// path), sorted by directory.
func (p *pass) localModules() []moduleCall {
	seen := map[string]bool{}
	var out []moduleCall
	for _, c := range p.localCalls() {
		if !seen[c.dir] {
			seen[c.dir] = true
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// rootCall is the root module call a call path starts at ("module.app").
func rootCall(callPath string) string {
	parts := strings.SplitN(callPath, ".", 3)
	if len(parts) < 2 {
		return callPath
	}
	return parts[0] + "." + parts[1]
}

// callDepth is how deep a call path is: "module.app" 1,
// "module.app.module.db" 2 (module names never contain dots).
func callDepth(callPath string) int {
	return (strings.Count(callPath, ".") + 1) / 2
}

// registrySource matches Terraform Registry addresses:
// <namespace>/<name>/<provider>, optionally prefixed by a registry host and
// followed by a //subdirectory.
var registrySource = regexp.MustCompile(`^([a-zA-Z0-9.-]+\.[a-z]+/)?[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+/[a-zA-Z0-9_-]+(//.*)?$`)

// isRegistrySource reports whether a module source is a registry address.
func isRegistrySource(src string) bool {
	return registrySource.MatchString(src) && !strings.Contains(src, "::") &&
		!strings.HasPrefix(src, "github.com/") && !strings.HasPrefix(src, "bitbucket.org/")
}

// isGitSource reports whether a module source is fetched from git.
func isGitSource(src string) bool {
	return strings.HasPrefix(src, "git::") || strings.HasPrefix(src, "git@") ||
		strings.HasPrefix(src, "github.com/") || strings.HasPrefix(src, "bitbucket.org/") ||
		strings.Contains(strings.SplitN(src, "?", 2)[0], ".git")
}

// instances estimates how many instances a block with count/for_each
// creates: the constant count or collection size; 0 for a conditional
// whose one branch creates nothing (count = var.create ? 1 : 0, the feature
// flags of published modules); else 1 (unknown sizes are not guessed).
func instances(b *model.Body) int {
	if n, ok := attrNumber(b, "count"); ok {
		if n < 0 {
			return 0
		}
		return int(n)
	}
	if a := getAttr(b, "for_each"); a != nil {
		switch v := literal(a.Value).(type) {
		case []any:
			return len(v)
		case map[string]any:
			return len(v)
		}
		if optional(a.Value) {
			return 0
		}
		return 1
	}
	if a := getAttr(b, "count"); a != nil && optional(a.Value) {
		return 0
	}
	return 1
}

// optional reports whether v is a conditional with a branch that is 0 or an
// empty collection.
func optional(v model.Value) bool {
	c, ok := parse(v).(*hclsyntax.ConditionalExpr)
	return ok && (emptyValue(c.TrueResult) || emptyValue(c.FalseResult))
}

// emptyValue: the constant 0, [], {} or null, or toset/tolist/tomap of an
// empty constant.
func emptyValue(e hclsyntax.Expression) bool {
	if f, ok := e.(*hclsyntax.FunctionCallExpr); ok {
		switch f.Name {
		case "toset", "tolist", "tomap":
			return len(f.Args) == 1 && emptyValue(f.Args[0])
		}
		return false
	}
	if len(e.Variables()) > 0 {
		return false
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsKnown() {
		return false
	}
	if v.IsNull() {
		return true
	}
	if v.Type() == cty.Number {
		return v.Equals(cty.Zero).True()
	}
	return v.CanIterateElements() && v.LengthInt() == 0
}
