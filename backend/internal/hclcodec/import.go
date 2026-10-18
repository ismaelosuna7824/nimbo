package hclcodec

import (
	"bytes"
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
// interpreted) into a new workflow. Each *.tfvars file becomes an
// environment named after the file (terraform.tfvars -> "default",
// prod.auto.tfvars and environments/prod.tfvars -> "prod"; a name already
// taken by another file falls back to the file name without ".tfvars");
// root-level files remember their name in Environment.File.
// environments/<name>.env.example fills that environment's EnvVars (blank
// values are secrets, as exported). Override files (override.tf,
// *_override.tf) and JSON files are not interpreted (see IsOverrideFile).
// CRLF line endings are converted to LF. Comments are kept (see
// model.Comments). Node positions are left with Positioned=false.
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
		if d, ok := UnsupportedFile(clean); ok {
			diags = append(diags, d)
			continue
		}
		switch {
		case strings.HasSuffix(clean, ".tf"):
			tfFiles = append(tfFiles, p)
		case strings.HasSuffix(clean, ".tfvars"):
			varFiles = append(varFiles, p)
		}
	}
	if len(tfFiles) == 0 {
		return nil, diags, errors.New("no root-level .tf files to import")
	}

	var nodes [3][]model.Node       // data, resources, modules
	var carry []string              // comments of comment-only files
	var lastBody func() *model.Body // the last top-level block, for trailing comments
	var localsCarry, terraformCarry []string
	localsSeen := false
	for _, p := range tfFiles {
		src := hclwrite.Format(normalizeNewlines(files[p]))
		f, hd := hclsyntax.ParseConfig(src, path.Base(p), hcl.InitialPos)
		diags = append(diags, parseDiags(hd)...)
		if f == nil {
			continue
		}
		body, ok := f.Body.(*hclsyntax.Body)
		if !ok {
			continue
		}
		var blocks []topBlock
		blocks, carry = newConverter(src).file(body, carry)
		for i := range blocks {
			b, mb := blocks[i].hb, blocks[i].body
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
				// Every locals block merges into one; its comments stay
				// next to the same attributes.
				first := len(wf.Locals)
				wf.Locals = append(wf.Locals, mb.Attrs...)
				if !localsSeen {
					localsSeen = true
					if mb.Comments != nil {
						wf.LocalsComments = &model.Comments{Lead: mb.Comments.Lead, Line: mb.Comments.Line, End: mb.Comments.End, After: mb.Comments.After}
						localsCarry = mb.Comments.Tail
					}
					continue
				}
				merged := func() *model.Comments {
					if wf.LocalsComments == nil {
						wf.LocalsComments = &model.Comments{}
					}
					return wf.LocalsComments
				}
				localsCarry = mergeBlockComments(merged, mb.Comments, localsCarry, func() *model.Comments {
					if first == len(wf.Locals) {
						return nil
					}
					return firstItemComments(&model.Body{Attrs: wf.Locals[first:]})
				})
			case b.Type == "terraform" && len(labels) == 0:
				if wf.Terraform == nil {
					wf.Terraform = &model.Body{Attrs: mb.Attrs, Blocks: mb.Blocks}
					if mb.Comments != nil {
						wf.Terraform.Comments = &model.Comments{Lead: mb.Comments.Lead, Line: mb.Comments.Line, End: mb.Comments.End, After: mb.Comments.After}
						terraformCarry = mb.Comments.Tail
					}
					continue
				}
				merged := func() *model.Comments { return bodyComments(wf.Terraform) }
				terraformCarry = mergeBlockComments(merged, mb.Comments, terraformCarry, func() *model.Comments { return firstItemComments(&mb) })
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
		if len(blocks) > 0 {
			if loc := lastImported(wf, &nodes, blocks[len(blocks)-1].hb); loc != nil {
				lastBody = loc
			}
		}
	}
	if len(localsCarry) > 0 {
		if wf.LocalsComments == nil {
			wf.LocalsComments = &model.Comments{}
		}
		wf.LocalsComments.Tail = localsCarry
	}
	if len(terraformCarry) > 0 {
		bodyComments(wf.Terraform).Tail = terraformCarry
	}
	if len(carry) > 0 { // trailing comment-only files
		var cms *model.Comments
		switch {
		case lastBody != nil:
			cms = bodyComments(lastBody())
		case localsSeen:
			if wf.LocalsComments == nil {
				wf.LocalsComments = &model.Comments{}
			}
			cms = wf.LocalsComments
		}
		if cms != nil {
			cms.After = joinAfter(append(cms.After, ""), carry)
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
	gatherAfter(wf)

	// Plain files claim environment names first: terraform.tfvars is
	// "default" even next to terraform.auto.tfvars.
	sort.SliceStable(varFiles, func(i, j int) bool {
		return !strings.HasSuffix(varFiles[i], ".auto.tfvars") && strings.HasSuffix(varFiles[j], ".auto.tfvars")
	})
	for _, p := range varFiles {
		clean := path.Clean(strings.ReplaceAll(p, "\\", "/"))
		env, d := importTfvars(path.Base(clean), files[p])
		diags = append(diags, d...)
		if !strings.Contains(clean, "/") {
			env.File = clean
		}
		if envIndex(wf.Environments, env.Name) >= 0 {
			// Each file stays its own environment so export writes it back.
			base := strings.TrimSuffix(path.Base(clean), ".tfvars")
			env.Name = base
			for i := 2; envIndex(wf.Environments, env.Name) >= 0; i++ {
				env.Name = fmt.Sprintf("%s-%d", base, i)
			}
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

// IsOverrideFile reports whether Terraform treats a *.tf file as an override
// file (override.tf, *_override.tf), merged into the blocks of the other
// files instead of declaring its own.
func IsOverrideFile(name string) bool {
	base := path.Base(strings.ReplaceAll(name, "\\", "/"))
	if !strings.HasSuffix(base, ".tf") {
		return false
	}
	base = strings.TrimSuffix(base, ".tf")
	return base == "override" || strings.HasSuffix(base, "_override")
}

// UnsupportedFile returns the warning for a root-level Terraform file that
// Import does not interpret: override files and JSON configuration or
// variables. ok is false for every other file.
func UnsupportedFile(name string) (d model.Diagnostic, ok bool) {
	var why string
	switch {
	case strings.HasSuffix(name, ".tf") && IsOverrideFile(name):
		why = "override files are merged by Terraform into other blocks and are not imported"
	case strings.HasSuffix(name, ".tf.json"), strings.HasSuffix(name, ".tfvars.json"):
		why = "JSON configuration is not supported"
	default:
		return d, false
	}
	return model.Diagnostic{
		Severity: model.SeverityWarning,
		Code:     "unsupported_file",
		Message:  fmt.Sprintf("%s is not shown on the canvas: %s", name, why),
		Detail:   "A folder import keeps the file as is: it is exported unchanged and Terraform still reads it. Edit it outside Nimbo.",
	}, true
}

// normalizeNewlines converts CRLF line endings to LF.
func normalizeNewlines(src []byte) []byte {
	return bytes.ReplaceAll(src, []byte("\r\n"), []byte("\n"))
}

// lastImported locates the body that holds the comments of hb, the block
// just imported (nil for locals blocks, whose comments live elsewhere). The
// locator is evaluated later, after the slices may have grown.
func lastImported(wf *model.Workflow, nodes *[3][]model.Node, hb *hclsyntax.Block) func() *model.Body {
	at := func(group, i int) func() *model.Body { return func() *model.Body { return &nodes[group][i].Body } }
	switch {
	case hb.Type == "resource" && len(hb.Labels) == 2:
		return at(1, len(nodes[1])-1)
	case hb.Type == "data" && len(hb.Labels) == 2:
		return at(0, len(nodes[0])-1)
	case hb.Type == "module" && len(hb.Labels) == 1:
		return at(2, len(nodes[2])-1)
	case hb.Type == "variable" && len(hb.Labels) == 1:
		i := len(wf.Variables) - 1
		return func() *model.Body { return &wf.Variables[i].Body }
	case hb.Type == "output" && len(hb.Labels) == 1:
		i := len(wf.Outputs) - 1
		return func() *model.Body { return &wf.Outputs[i].Body }
	case hb.Type == "provider" && len(hb.Labels) == 1:
		i := len(wf.Providers) - 1
		return func() *model.Body { return &wf.Providers[i].Body }
	case hb.Type == "terraform" && len(hb.Labels) == 0:
		return func() *model.Body { return wf.Terraform }
	case hb.Type == "locals" && len(hb.Labels) == 0:
		return nil
	}
	i := len(wf.Other) - 1
	return func() *model.Body { return &wf.Other[i].Body }
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
	src := hclwrite.Format(normalizeNewlines(content))
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
