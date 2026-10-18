package hclcodec

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Import parses Terraform files (path -> content; only root-level *.tf and
// *.tfvars plus environments/*.tfvars and environments/*.env.example are
// interpreted) into a new workflow. *.tfvars become environments named after
// the file (terraform.tfvars -> "default", prod.auto.tfvars and
// environments/prod.tfvars -> "prod"); environments/<name>.env.example
// fills that environment's EnvVars (blank values are secrets, as exported).
// Node positions are left with Positioned=false. Comments are dropped.
func Import(name string, files map[string][]byte) (*model.Workflow, []model.Diagnostic, error) {
	wf := &model.Workflow{
		Name:         name,
		Nodes:        []model.Node{},
		Variables:    []model.NamedBlock{},
		Outputs:      []model.NamedBlock{},
		Locals:       []model.Attr{},
		Providers:    []model.NamedBlock{},
		Other:        []model.Block{},
		Environments: []model.Environment{},
		Edges:        []model.Edge{},
		Clouds:       []model.Cloud{},
		Diagnostics:  []model.Diagnostic{},
	}
	var diags []model.Diagnostic

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var tfFiles, varFiles, envFiles []string
	for _, p := range paths {
		clean := path.Clean(strings.ReplaceAll(p, "\\", "/"))
		if dir, file, nested := strings.Cut(clean, "/"); nested {
			// Export writes environments/<name>.tfvars and
			// environments/<name>.env.example; read them back.
			if dir == "environments" && !strings.Contains(file, "/") {
				switch {
				case strings.HasSuffix(file, ".tfvars"):
					varFiles = append(varFiles, p)
				case strings.HasSuffix(file, ".env.example"):
					envFiles = append(envFiles, p)
				}
			}
			continue
		}
		switch {
		case strings.HasSuffix(clean, ".tf"):
			tfFiles = append(tfFiles, p)
		case strings.HasSuffix(clean, ".tfvars"):
			varFiles = append(varFiles, p)
		case strings.HasSuffix(clean, ".tf.json"), strings.HasSuffix(clean, ".tfvars.json"):
			diags = append(diags, model.Diagnostic{
				Severity: model.SeverityWarning,
				Code:     "unsupported_file",
				Message:  fmt.Sprintf("Skipped %s: JSON Terraform files are not supported", clean),
			})
		}
	}
	if len(tfFiles) == 0 {
		return nil, diags, errors.New("no root-level .tf files to import")
	}

	var nodes [3][]model.Node // data, resources, modules
	for _, p := range tfFiles {
		src := hclwrite.Format(files[p])
		f, hd := hclsyntax.ParseConfig(src, path.Base(p), hcl.InitialPos)
		diags = append(diags, parseDiags(hd)...)
		if f == nil {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		for _, b := range body.Blocks {
			mb := convertBody(src, b.Body)
			labels := b.Labels
			switch {
			case b.Type == "resource" && len(labels) == 2:
				nodes[1] = append(nodes[1], model.Node{Kind: model.KindResource, Type: labels[0], Name: labels[1], Body: mb})
			case b.Type == "data" && len(labels) == 2:
				nodes[0] = append(nodes[0], model.Node{Kind: model.KindData, Type: labels[0], Name: labels[1], Body: mb})
			case b.Type == "module" && len(labels) == 1:
				nodes[2] = append(nodes[2], model.Node{Kind: model.KindModule, Type: "module", Name: labels[0], Body: mb})
			case b.Type == "variable" && len(labels) == 1:
				wf.Variables = append(wf.Variables, model.NamedBlock{Name: labels[0], Body: mb})
			case b.Type == "output" && len(labels) == 1:
				wf.Outputs = append(wf.Outputs, model.NamedBlock{Name: labels[0], Body: mb})
			case b.Type == "provider" && len(labels) == 1:
				wf.Providers = append(wf.Providers, model.NamedBlock{Name: labels[0], Body: mb})
			case b.Type == "locals" && len(labels) == 0:
				wf.Locals = append(wf.Locals, mb.Attrs...)
			case b.Type == "terraform" && len(labels) == 0:
				if wf.Terraform == nil {
					wf.Terraform = &model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}
				}
				wf.Terraform.Attrs = append(wf.Terraform.Attrs, mb.Attrs...)
				wf.Terraform.Blocks = append(wf.Terraform.Blocks, mb.Blocks...)
			default:
				switch b.Type {
				case "resource", "data", "module", "variable", "output", "provider", "locals", "terraform":
					diags = append(diags, model.Diagnostic{
						Severity: model.SeverityError,
						Code:     "invalid_block",
						Message:  fmt.Sprintf("%s block has %d labels", b.Type, len(labels)),
						Detail:   fmt.Sprintf("%s:%d: kept as an unrecognized block", b.TypeRange.Filename, b.TypeRange.Start.Line),
					})
				}
				wf.Other = append(wf.Other, model.Block{Type: b.Type, Labels: labelsOrNil(labels), Body: mb})
			}
		}
	}
	// main.tf groups data sources, resources and module calls; ordering the
	// imported nodes the same way keeps Import(Export(wf)) stable.
	for _, group := range nodes {
		for _, nd := range group {
			nd.ID = Address(&nd)
			wf.Nodes = append(wf.Nodes, nd)
		}
	}

	for _, p := range varFiles {
		env, d := importTfvars(path.Base(strings.ReplaceAll(p, "\\", "/")), files[p])
		diags = append(diags, d...)
		if i := envIndex(wf.Environments, env.Name); i >= 0 {
			for k, v := range env.Values {
				wf.Environments[i].Values[k] = v
			}
			continue
		}
		wf.Environments = append(wf.Environments, env)
	}
	for _, p := range envFiles {
		file := path.Base(strings.ReplaceAll(p, "\\", "/"))
		name := strings.TrimSuffix(file, ".env.example")
		vars, d := importEnvExample(file, files[p])
		diags = append(diags, d...)
		i := envIndex(wf.Environments, name)
		if i < 0 {
			wf.Environments = append(wf.Environments, model.Environment{Name: name, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}})
			i = len(wf.Environments) - 1
		}
		wf.Environments[i].EnvVars = append(wf.Environments[i].EnvVars, vars...)
	}
	if envIndex(wf.Environments, "default") >= 0 {
		wf.ActiveEnvironment = "default"
	}

	diags = append(diags, Normalize(wf)...)
	return wf, diags, nil
}

func envIndex(envs []model.Environment, name string) int {
	for i, e := range envs {
		if e.Name == name {
			return i
		}
	}
	return -1
}

func labelsOrNil(labels []string) []string {
	if len(labels) == 0 {
		return nil
	}
	return append([]string(nil), labels...)
}

func importTfvars(file string, content []byte) (model.Environment, []model.Diagnostic) {
	name := strings.TrimSuffix(file, ".tfvars")
	name = strings.TrimSuffix(name, ".auto")
	if name == "terraform" {
		name = "default"
	}
	env := model.Environment{Name: name, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}}
	src := hclwrite.Format(content)
	f, hd := hclsyntax.ParseConfig(src, file, hcl.InitialPos)
	diags := parseDiags(hd)
	if f == nil {
		return env, diags
	}
	body, ok := f.Body.(*hclsyntax.Body)
	if !ok {
		return env, diags
	}
	for name, a := range body.Attributes {
		env.Values[name] = model.Value{Expr: exprSource(src, a.Expr)}
	}
	for _, b := range body.Blocks {
		diags = append(diags, model.Diagnostic{
			Severity: model.SeverityWarning,
			Code:     "unsupported_block",
			Message:  fmt.Sprintf("Ignored %s block in %s", b.Type, file),
			Detail:   fmt.Sprintf("%s:%d: variable files may only contain attributes", file, b.TypeRange.Start.Line),
		})
	}
	return env, diags
}

// importEnvExample parses KEY=VALUE lines as written by Export. Values quoted
// by Export are unquoted; blank values are secrets whose value was withheld.
func importEnvExample(file string, content []byte) ([]model.EnvVar, []model.Diagnostic) {
	vars := []model.EnvVar{}
	var diags []model.Diagnostic
	for i, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if !ok || key == "" {
			diags = append(diags, model.Diagnostic{
				Severity: model.SeverityWarning,
				Code:     "invalid_env_line",
				Message:  fmt.Sprintf("Ignored line %d in %s", i+1, file),
				Detail:   fmt.Sprintf("%s:%d: expected KEY=VALUE", file, i+1),
			})
			continue
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"`) {
			if u, err := strconv.Unquote(value); err == nil {
				value = u
			}
		}
		vars = append(vars, model.EnvVar{Key: key, Value: value, Secret: value == ""})
	}
	return vars, diags
}

func parseDiags(hd hcl.Diagnostics) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range hd {
		sev := model.SeverityError
		if d.Severity == hcl.DiagWarning {
			sev = model.SeverityWarning
		}
		detail := d.Detail
		if d.Subject != nil {
			detail = fmt.Sprintf("%s:%d: %s", d.Subject.Filename, d.Subject.Start.Line, d.Detail)
		}
		out = append(out, model.Diagnostic{Severity: sev, Code: "parse_error", Message: d.Summary, Detail: detail})
	}
	return out
}

func exprSource(src []byte, e hclsyntax.Expression) string {
	r := e.Range()
	return string(src[r.Start.Byte:r.End.Byte])
}

// convertBody keeps attributes in source order; hclsyntax stores them in a map.
func convertBody(src []byte, b *hclsyntax.Body) model.Body {
	attrs := make([]*hclsyntax.Attribute, 0, len(b.Attributes))
	for _, a := range b.Attributes {
		attrs = append(attrs, a)
	}
	sort.Slice(attrs, func(i, j int) bool { return attrs[i].SrcRange.Start.Byte < attrs[j].SrcRange.Start.Byte })
	out := model.Body{Attrs: make([]model.Attr, 0, len(attrs)), Blocks: make([]model.Block, 0, len(b.Blocks))}
	for _, a := range attrs {
		out.Attrs = append(out.Attrs, model.Attr{Name: a.Name, Value: model.Value{Expr: exprSource(src, a.Expr)}})
	}
	for _, blk := range b.Blocks {
		out.Blocks = append(out.Blocks, model.Block{Type: blk.Type, Labels: labelsOrNil(blk.Labels), Body: convertBody(src, blk.Body)})
	}
	return out
}
