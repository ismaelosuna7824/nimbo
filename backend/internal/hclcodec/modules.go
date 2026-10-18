package hclcodec

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ModuleCall is a `module` block found by Summarize.
type ModuleCall struct {
	Name    string
	Source  string // "" when source is missing or not a constant string
	Version string
}

// Summary describes one Terraform module directory without building a
// workflow (cheap enough to run over every directory of a repository).
type Summary struct {
	Resources   int
	DataSources int
	Modules     []ModuleCall
	Providers   []string // sorted local names from required_providers, provider blocks and resource types
	Clouds      []model.Cloud
	HasBackend  bool // terraform { backend "…" {} } or terraform { cloud {} }
	Diagnostics []model.Diagnostic
}

// Summarize parses the *.tf files (name -> content; names are only used in
// diagnostics) of one directory.
func Summarize(files map[string][]byte) Summary {
	var s Summary
	providers := map[string]bool{}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		f, hd := hclsyntax.ParseConfig(files[name], name, hcl.InitialPos)
		s.Diagnostics = append(s.Diagnostics, parseDiags(hd)...)
		if f == nil {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		s.Diagnostics = append(s.Diagnostics, outsidePaths(name, body)...)
		for _, b := range body.Blocks {
			switch {
			case b.Type == "resource" && len(b.Labels) == 2:
				s.Resources++
				providers[providerOfType(b.Labels[0])] = true
			case b.Type == "data" && len(b.Labels) == 2:
				s.DataSources++
				providers[providerOfType(b.Labels[0])] = true
			case b.Type == "module" && len(b.Labels) == 1:
				s.Modules = append(s.Modules, ModuleCall{
					Name:    b.Labels[0],
					Source:  constString(b.Body, "source"),
					Version: constString(b.Body, "version"),
				})
			case b.Type == "provider" && len(b.Labels) == 1:
				providers[b.Labels[0]] = true
			case b.Type == "terraform":
				for _, tb := range b.Body.Blocks {
					switch tb.Type {
					case "backend", "cloud":
						s.HasBackend = true
					case "required_providers":
						for name := range tb.Body.Attributes {
							providers[name] = true
						}
					}
				}
			}
		}
	}
	delete(providers, "")
	clouds := map[model.Cloud]bool{}
	for p := range providers {
		s.Providers = append(s.Providers, p)
		if c, ok := cloudOfType(p); ok {
			clouds[c] = true
		}
	}
	slices.Sort(s.Providers)
	for _, c := range model.AllClouds {
		if clouds[c] {
			s.Clouds = append(s.Clouds, c)
		}
	}
	return s
}

// fileFunctions read a path relative to the working directory.
var fileFunctions = map[string]bool{
	"file": true, "filebase64": true, "filebase64sha256": true, "filebase64sha512": true, "fileexists": true,
	"filemd5": true, "filesha1": true, "filesha256": true, "filesha512": true, "fileset": true, "templatefile": true,
}

// outsidePaths reports file paths that leave the directory: templates
// starting with ${path.module}/.. or ${path.root}/.., and "../" literals
// passed to file functions. A folder import copies a directory's own files
// only, so such files are missing from the export.
func outsidePaths(file string, body *hclsyntax.Body) []model.Diagnostic {
	var out []model.Diagnostic
	seen := map[string]bool{}
	report := func(rng hcl.Range, p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		out = append(out, model.Diagnostic{
			Severity: model.SeverityWarning,
			Code:     "path_outside_folder",
			Message:  fmt.Sprintf("%s refers to files outside its folder (%s)", file, p),
			Detail:   fmt.Sprintf("%s:%d: the import copies only the folder's own files, so the export does not include %s. Copy it into the folder and update the path.", file, rng.Start.Line, p),
		})
	}
	hclsyntax.VisitAll(body, func(n hclsyntax.Node) hcl.Diagnostics {
		switch e := n.(type) {
		case *hclsyntax.TemplateExpr:
			if len(e.Parts) < 2 {
				return nil
			}
			st, ok := e.Parts[0].(*hclsyntax.ScopeTraversalExpr)
			lit, ok2 := e.Parts[1].(*hclsyntax.LiteralValueExpr)
			if !ok || !ok2 || len(st.Traversal) != 2 || st.Traversal.RootName() != "path" || lit.Val.Type() != cty.String {
				return nil
			}
			attr, ok := st.Traversal[1].(hcl.TraverseAttr)
			rest := lit.Val.AsString()
			if ok && (attr.Name == "module" || attr.Name == "root") && (rest == "/.." || strings.HasPrefix(rest, "/../")) {
				report(e.SrcRange, "${path."+attr.Name+"}"+rest)
			}
		case *hclsyntax.FunctionCallExpr:
			if !fileFunctions[e.Name] || len(e.Args) == 0 {
				return nil
			}
			if v, d := e.Args[0].Value(nil); !d.HasErrors() && v.Type() == cty.String && v.IsKnown() && !v.IsNull() {
				if p := v.AsString(); p == ".." || strings.HasPrefix(p, "../") {
					report(e.Range(), p)
				}
			}
		}
		return nil
	})
	return out
}

// providerOfType is the implied provider local name of a resource type.
func providerOfType(t string) string {
	p, _, _ := strings.Cut(t, "_")
	return p
}

// constString returns the attribute as a constant string, or "".
func constString(b *hclsyntax.Body, name string) string {
	a, ok := b.Attributes[name]
	if !ok {
		return ""
	}
	v, d := a.Expr.Value(nil)
	if d.HasErrors() || !v.IsKnown() || v.IsNull() || v.Type() != cty.String {
		return ""
	}
	return v.AsString()
}

// IsLocalModuleSource reports whether a module source is a local path
// ("./x", "../x"), which Terraform reads directly instead of downloading.
func IsLocalModuleSource(source string) bool {
	return strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") ||
		strings.HasPrefix(source, `.\`) || strings.HasPrefix(source, `..\`)
}

// ResolveLocalSource joins a local module source onto the slash directory
// dir that contains the call ("." for the root) and cleans the result.
func ResolveLocalSource(dir, source string) string {
	return path.Clean(path.Join(dir, strings.ReplaceAll(source, `\`, "/")))
}

// RewriteModuleSources rewrites the constant `source` of module blocks in
// one .tf file. newSource receives the current source and returns the
// replacement (ok=false keeps it). Only those attribute values change; every
// other byte of the file is preserved. changed is false when nothing was
// rewritten (out is then src).
func RewriteModuleSources(filename string, src []byte, newSource func(source string) (string, bool)) (out []byte, changed bool, err error) {
	f, hd := hclwrite.ParseConfig(src, filename, hcl.InitialPos)
	if hd.HasErrors() {
		return nil, false, fmt.Errorf("%s: %s", filename, hd.Error())
	}
	for _, b := range f.Body().Blocks() {
		if b.Type() != "module" {
			continue
		}
		attr := b.Body().GetAttribute("source")
		if attr == nil {
			continue
		}
		cur, ok := constTokens(attr.Expr().BuildTokens(nil).Bytes())
		if !ok {
			continue
		}
		if next, ok := newSource(cur); ok && next != cur {
			b.Body().SetAttributeValue("source", cty.StringVal(next))
			changed = true
		}
	}
	if !changed {
		return src, false, nil
	}
	return f.Bytes(), true, nil
}

// constTokens evaluates expression source text as a constant string.
func constTokens(src []byte) (string, bool) {
	e, d := hclsyntax.ParseExpression(src, "", hcl.InitialPos)
	if d.HasErrors() {
		return "", false
	}
	v, d := e.Value(nil)
	if d.HasErrors() || !v.IsKnown() || v.IsNull() || v.Type() != cty.String {
		return "", false
	}
	return v.AsString(), true
}
