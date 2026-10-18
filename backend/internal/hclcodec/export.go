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
// With opts.Environments, also writes "environments/<name>.tfvars"
// (values of every variable in that env) and
// "environments/<name>.env.example" (keys; secret values blank).
func Export(wf *model.Workflow, opts ExportOptions) (map[string][]byte, error) {
	files := map[string][]byte{}
	add := func(name string, w *writer) error {
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
	if len(wf.Locals) > 0 {
		main.block("locals", "locals", nil, &model.Body{Attrs: wf.Locals})
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
		for i := range wf.Environments {
			env := &wf.Environments[i]
			if env.Name == "" || strings.ContainsAny(env.Name, `/\`) || env.Name == "." || env.Name == ".." {
				return nil, fmt.Errorf("environment name %q cannot be used as a file name", env.Name)
			}
			vars := &writer{}
			for _, k := range envValueOrder(wf, env) {
				vars.attr("environment "+env.Name, k, env.Values[k])
			}
			if err := add("environments/"+env.Name+".tfvars", vars); err != nil {
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
}

func (w *writer) nested(owner, typ string, labels []string, body *model.Body) {
	if !hclsyntax.ValidIdentifier(typ) {
		w.fail("%s: invalid block type %q", owner, typ)
		return
	}
	w.buf.WriteString(typ)
	for _, l := range labels {
		w.buf.WriteByte(' ')
		w.buf.Write(hclwrite.TokensForValue(cty.StringVal(l)).Bytes())
	}
	if len(body.Attrs) == 0 && len(body.Blocks) == 0 {
		w.buf.WriteString(" {}\n")
		return
	}
	w.buf.WriteString(" {\n")
	for _, a := range body.Attrs {
		w.attr(owner, a.Name, a.Value)
	}
	for i := range body.Blocks {
		b := &body.Blocks[i]
		if i > 0 || len(body.Attrs) > 0 {
			w.buf.WriteByte('\n')
		}
		w.nested(owner, b.Type, b.Labels, &b.Body)
	}
	w.buf.WriteString("}\n")
}

func (w *writer) attr(owner, name string, v model.Value) {
	if !hclsyntax.ValidIdentifier(name) {
		w.fail("%s: invalid attribute name %q", owner, name)
		return
	}
	expr, err := exprOf(v)
	if err != nil {
		w.fail("%s: %s: %w", owner, name, err)
		return
	}
	w.buf.WriteString(name)
	w.buf.WriteString(" = ")
	w.buf.WriteString(expr)
	w.buf.WriteByte('\n')
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
