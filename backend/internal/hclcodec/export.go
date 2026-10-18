package hclcodec

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ExportOptions controls optional Export output.
type ExportOptions struct{ Environments bool }

// Export renders wf as Terraform files: "versions.tf" (terraform block),
// "providers.tf", "variables.tf", "main.tf" (locals, data, resources,
// modules, other), "outputs.tf". Files with no content are omitted.
// Comments are written back where Import found them (see model.Comments).
// With opts.Environments, also writes each environment's values to its
// Environment.File (a root variable file such as "terraform.tfvars") or
// "environments/<name>.tfvars", and "environments/<name>.env.example"
// (keys; secret values blank).
func Export(wf *model.Workflow, opts ExportOptions) (map[string][]byte, error) {
	files := map[string][]byte{}
	add := func(name string, w *writer) error {
		w.finish(name)
		if w.err != nil {
			return fmt.Errorf("%s: %w", name, w.err)
		}
		if w.buf.Len() == 0 {
			return nil
		}
		out, err := formatFile(name, w.buf.String())
		if err != nil {
			return err
		}
		files[name] = out
		return nil
	}

	for i := range wf.Nodes {
		switch k := wf.Nodes[i].Kind; k {
		case model.KindData, model.KindResource, model.KindModule:
		default:
			return nil, fmt.Errorf("node %s: unknown kind %q", wf.Nodes[i].ID, k)
		}
	}

	versions := &writer{}
	if wf.Terraform != nil {
		versions.block("terraform", "terraform", nil, wf.Terraform)
	}
	providers := &writer{}
	for i := range wf.Providers {
		p := &wf.Providers[i]
		providers.block("provider "+p.Name, "provider", []string{p.Name}, &p.Body)
	}
	variables := &writer{}
	for i := range wf.Variables {
		v := &wf.Variables[i]
		variables.block("variable "+v.Name, "variable", []string{v.Name}, &v.Body)
	}
	main := &writer{}
	if len(wf.Locals) > 0 || !emptyComments(wf.LocalsComments) {
		main.block("locals", "locals", nil, &model.Body{Attrs: wf.Locals, Comments: wf.LocalsComments})
	}
	for _, kind := range []model.NodeKind{model.KindData, model.KindResource, model.KindModule} {
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if n.Kind != kind {
				continue
			}
			switch kind {
			case model.KindModule:
				main.block(Address(n), "module", []string{n.Name}, &n.Body)
			default:
				main.block(Address(n), string(kind), []string{n.Type, n.Name}, &n.Body)
			}
		}
	}
	for i := range wf.Other {
		b := &wf.Other[i]
		main.block(b.Type, b.Type, b.Labels, &b.Body)
	}
	outputs := &writer{}
	for i := range wf.Outputs {
		o := &wf.Outputs[i]
		outputs.block("output "+o.Name, "output", []string{o.Name}, &o.Body)
	}

	for _, f := range []struct {
		name string
		w    *writer
	}{
		{"versions.tf", versions}, {"providers.tf", providers}, {"variables.tf", variables},
		{"main.tf", main}, {"outputs.tf", outputs},
	} {
		if err := add(f.name, f.w); err != nil {
			return nil, err
		}
	}

	if opts.Environments {
		usedFiles := map[string]bool{}
		for i := range wf.Environments {
			env := &wf.Environments[i]
			if env.Name == "" || strings.ContainsAny(env.Name, `/\`) || env.Name == "." || env.Name == ".." {
				return nil, fmt.Errorf("environment name %q cannot be used as a file name", env.Name)
			}
			file := "environments/" + env.Name + ".tfvars"
			if env.File != "" {
				if strings.ContainsAny(env.File, `/\`) || !strings.HasSuffix(env.File, ".tfvars") || env.File == ".tfvars" {
					return nil, fmt.Errorf("environment %s: file %q must be a *.tfvars file name", env.Name, env.File)
				}
				file = env.File
			}
			if usedFiles[file] {
				return nil, fmt.Errorf("environment %s: more than one environment is written to %s", env.Name, file)
			}
			usedFiles[file] = true
			vars := &writer{}
			for _, k := range envValueOrder(wf, env) {
				vars.attr("environment "+env.Name, model.Attr{Name: k, Value: env.Values[k]})
			}
			if err := add(file, vars); err != nil {
				return nil, err
			}
			if len(env.EnvVars) > 0 {
				files["environments/"+env.Name+".env.example"] = envExample(env.EnvVars)
			}
		}
	}
	return files, nil
}

// envValueOrder lists env values in variable declaration order, then the
// remaining (undeclared) names alphabetically.
func envValueOrder(wf *model.Workflow, env *model.Environment) []string {
	keys := make([]string, 0, len(env.Values))
	done := map[string]bool{}
	for _, v := range wf.Variables {
		if _, ok := env.Values[v.Name]; ok && !done[v.Name] {
			keys = append(keys, v.Name)
			done[v.Name] = true
		}
	}
	var rest []string
	for k := range env.Values {
		if !done[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func envExample(vars []model.EnvVar) []byte {
	var b strings.Builder
	for _, v := range vars {
		b.WriteString(v.Key)
		b.WriteByte('=')
		if !v.Secret {
			b.WriteString(envQuote(v.Value))
		}
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

func envQuote(s string) string {
	if s == "" || !strings.ContainsAny(s, " \t\r\n\"'#$\\`") {
		return s
	}
	return strconv.Quote(s)
}

func formatFile(name, src string) ([]byte, error) {
	out := hclwrite.Format([]byte(src))
	if _, diags := hclsyntax.ParseConfig(out, name, hcl.InitialPos); diags.HasErrors() {
		return nil, fmt.Errorf("%s: generated invalid HCL: %w", name, diagsError(diags))
	}
	return out, nil
}

// writer accumulates unformatted HCL; the result is formatted per file. The
// first error is kept and stops further output.
type writer struct {
	buf strings.Builder
	err error
	// after collects the end-of-file comments of the blocks written so
	// far (model.Comments.After); finish writes them last.
	after []string
}

func (w *writer) fail(format string, args ...any) {
	if w.err == nil {
		w.err = fmt.Errorf(format, args...)
	}
}

// block writes a top-level block separated from the previous one by a blank line.
func (w *writer) block(owner, typ string, labels []string, body *model.Body) {
	if w.buf.Len() > 0 {
		w.buf.WriteByte('\n')
	}
	w.nested(owner, typ, labels, body)
	if body.Comments != nil {
		w.after = joinAfter(w.after, body.Comments.After)
	}
}

// finish writes the end-of-file comments.
func (w *writer) finish(name string) {
	w.comments(name, w.after)
	w.after = nil
}

func (w *writer) nested(owner, typ string, labels []string, body *model.Body) {
	if !hclsyntax.ValidIdentifier(typ) {
		w.fail("%s: invalid block type %q", owner, typ)
		return
	}
	cms := body.Comments
	if cms == nil {
		cms = &model.Comments{}
	}
	w.comments(owner, cms.Lead)
	w.buf.WriteString(typ)
	for _, l := range labels {
		w.buf.WriteByte(' ')
		w.buf.Write(hclwrite.TokensForValue(cty.StringVal(l)).Bytes())
	}
	if len(body.Attrs) == 0 && len(body.Blocks) == 0 && cms.Line == "" && len(cms.Tail) == 0 {
		w.buf.WriteString(" {}")
		w.lineComment(owner, cms.End)
		return
	}
	w.buf.WriteString(" {")
	w.lineComment(owner, cms.Line)
	for _, a := range body.Attrs {
		w.attr(owner, a)
	}
	for i := range body.Blocks {
		b := &body.Blocks[i]
		if i > 0 || len(body.Attrs) > 0 {
			w.buf.WriteByte('\n')
		}
		w.nested(owner, b.Type, b.Labels, &b.Body)
	}
	w.comments(owner, cms.Tail)
	w.buf.WriteString("}")
	w.lineComment(owner, cms.End)
}

func (w *writer) attr(owner string, a model.Attr) {
	if !hclsyntax.ValidIdentifier(a.Name) {
		w.fail("%s: invalid attribute name %q", owner, a.Name)
		return
	}
	expr, err := exprOf(a.Value)
	if err != nil {
		w.fail("%s: %s: %w", owner, a.Name, err)
		return
	}
	if a.Comments != nil {
		w.comments(owner, a.Comments.Lead)
	}
	w.buf.WriteString(a.Name)
	w.buf.WriteString(" = ")
	w.buf.WriteString(expr)
	if a.Comments == nil || a.Comments.Line == "" {
		w.buf.WriteByte('\n')
		return
	}
	if endsWithHeredoc(expr) {
		// Nothing may follow a heredoc's closing marker on its line.
		w.buf.WriteByte('\n')
		w.comments(owner, []string{a.Comments.Line})
		return
	}
	w.lineComment(owner, a.Comments.Line)
}

// comments writes whole-line comments; "" is a blank line.
func (w *writer) comments(owner string, lines []string) {
	for _, l := range lines {
		if l != "" {
			if err := validComment(l); err != nil {
				w.fail("%s: %w", owner, err)
				return
			}
			w.buf.WriteString(l)
		}
		w.buf.WriteByte('\n')
	}
}

// lineComment ends the current line with an optional comment.
func (w *writer) lineComment(owner, c string) {
	if c != "" {
		if err := validComment(c); err != nil {
			w.fail("%s: %w", owner, err)
			return
		}
		w.buf.WriteByte(' ')
		w.buf.WriteString(c)
	}
	w.buf.WriteByte('\n')
}

// endsWithHeredoc reports whether expression source ends with a heredoc's
// closing marker.
func endsWithHeredoc(expr string) bool {
	toks, _ := hclsyntax.LexExpression([]byte(expr+"\n"), "", hcl.InitialPos)
	for i := len(toks) - 1; i >= 0; i-- {
		switch toks[i].Type {
		case hclsyntax.TokenEOF, hclsyntax.TokenNewline:
			continue
		case hclsyntax.TokenCHeredoc:
			return true
		}
		return false
	}
	return false
}

// exprOf returns the validated expression source of v, deriving it from the
// literal when the expression is missing.
func exprOf(v model.Value) (string, error) {
	expr := strings.TrimSpace(v.Expr)
	if expr == "" && len(v.Literal) > 0 {
		return ExprFromLiteral(v.Literal)
	}
	if err := ValidateExpr(expr); err != nil {
		return "", err
	}
	return expr, nil
}
