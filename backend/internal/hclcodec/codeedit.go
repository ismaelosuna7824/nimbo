package hclcodec

import (
	"slices"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// IsHCLFile reports whether a file holds native HCL syntax that Format and
// CheckSyntax understand: Terraform code, variable files and *.hcl.
func IsHCLFile(name string) bool {
	return strings.HasSuffix(name, ".tf") || strings.HasSuffix(name, ".tfvars") || strings.HasSuffix(name, ".hcl")
}

// CheckSyntax parses src (CRLF read as LF) and returns its syntax errors as
// "parse_error" diagnostics located in name (nil when it parses).
func CheckSyntax(name string, src []byte) []model.Diagnostic {
	_, hd := hclsyntax.ParseConfig(normalizeNewlines(src), name, hcl.InitialPos)
	if !hd.HasErrors() {
		return nil
	}
	return parseDiags(hd)
}

// Format formats HCL source like `terraform fmt` (hclwrite.Format with LF
// line endings) once it parses; syntax errors come back as CheckSyntax's
// diagnostics and src is not formatted.
func Format(name string, src []byte) ([]byte, []model.Diagnostic) {
	if d := CheckSyntax(name, src); d != nil {
		return nil, d
	}
	return hclwrite.Format(normalizeNewlines(src)), nil
}

// NewModule renders the skeleton of a new local module modules/<name>/
// (paths relative to the module folder): versions.tf with the workflow's
// required_version and required_providers as minimums (">= 6.0", what a
// reusable module declares; providers lists the catalog entries used for a
// cloud of the workflow the root does not require yet), variables.tf,
// outputs.tf, main.tf and README.md. call is the block that calls it from
// a module whose folder reaches it through source.
func NewModule(wf *model.Workflow, name, source string, providers []model.ProviderInfo) (files map[string][]byte, call string) {
	var tf strings.Builder
	tf.WriteString("terraform {\n")
	if v := minimumConstraint(requiredVersion(wf)); v != "" {
		tf.WriteString("required_version = " + strconv.Quote(v) + "\n")
	}
	type req struct{ local, source, version string }
	var reqs []req
	for _, local := range requiredProviderNames(wf) {
		if slices.ContainsFunc(reqs, func(r req) bool { return r.local == local }) {
			continue
		}
		src, version := requiredProvider(wf, local)
		if src == "" {
			src = strconv.Quote("hashicorp/" + local)
		}
		r := req{local: local, source: src}
		if c, ok := constStringExpr(version); ok {
			r.version = minimumConstraint(c)
		}
		reqs = append(reqs, r)
	}
	for _, p := range providers {
		if !slices.Contains(wf.Clouds, p.Cloud) || slices.ContainsFunc(reqs, func(r req) bool { return r.local == p.Name }) {
			continue
		}
		reqs = append(reqs, req{local: p.Name, source: strconv.Quote(p.Source), version: minimumConstraint(p.Version)})
	}
	if len(reqs) > 0 {
		tf.WriteString("\nrequired_providers {\n")
		for _, r := range reqs {
			tf.WriteString(r.local + " = {\nsource = " + r.source + "\n")
			if r.version != "" {
				tf.WriteString("version = " + strconv.Quote(r.version) + "\n")
			}
			tf.WriteString("}\n")
		}
		tf.WriteString("}\n")
	}
	tf.WriteString("}\n")

	call = string(hclwrite.Format([]byte("module " + strconv.Quote(name) + " {\nsource = " + strconv.Quote(source) + "\n}\n")))
	files = map[string][]byte{
		"versions.tf":  hclwrite.Format([]byte(tf.String())),
		"variables.tf": []byte("# Inputs of the " + name + " module: one variable block per value its callers pass.\n"),
		"outputs.tf":   []byte("# Outputs of the " + name + " module: what its callers read as module." + name + ".<output>.\n"),
		"main.tf":      []byte("# Resources of the " + name + " module.\n"),
		"README.md": []byte("# " + name + "\n\nLocal Terraform module.\n\n## Usage\n\n```hcl\n" + call + "```\n\n" +
			"## Files\n\n| File | Content |\n|---|---|\n| `versions.tf` | Terraform and provider requirements (minimums) |\n" +
			"| `variables.tf` | Inputs |\n| `main.tf` | Resources |\n| `outputs.tf` | Outputs |\n"),
	}
	return files, call
}
