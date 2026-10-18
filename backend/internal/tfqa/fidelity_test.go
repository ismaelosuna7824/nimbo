package tfqa

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Vendored subset of the corpus (testdata/vendor, licenses kept):
//   - learn-terraform-modules and learn-terraform-lambda-api-gateway from
//     github.com/hashicorp-education (MPL-2.0), at the commits in corpus_test.go;
//   - terraform-aws-sqs (module root + examples/complete) from
//     github.com/terraform-aws-modules (Apache-2.0).
//
// testdata/edge is a hand-written root covering the edge cases (comments,
// heredocs, templates, dynamic blocks, provisioners, lifecycle conditions,
// check/import/moved/removed blocks, provider functions, override and JSON
// files, assets, nested local modules, variable files).

// validateEnabled reports whether terraform init/validate runs: on by
// default with the corpus build tag, otherwise only with TFQA_VALIDATE=1.
func validateEnabled() bool {
	switch os.Getenv("TFQA_VALIDATE") {
	case "1":
		return terraformBin() != ""
	case "0":
		return false
	}
	return corpusBuild && terraformBin() != ""
}

func localOptions() options {
	return options{fmt: terraformBin() != "", validate: validateEnabled()}
}

func localRoots(t *testing.T) []root {
	t.Helper()
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	var roots []root
	for _, tpl := range cat.Templates() {
		roots = append(roots, root{Name: "template/" + tpl.ID, Template: tpl.ID})
	}
	for _, f := range []string{"serverless", "network", "multicloud"} {
		roots = append(roots, root{Name: "hclcodec/testdata/" + f, Scan: abs("../hclcodec/testdata/" + f), Project: "."})
	}
	for _, p := range []string{"envs/dev", "envs/prod"} {
		roots = append(roots, root{Name: "api/testdata/repo/" + p, Scan: abs("../api/testdata/repo"), Project: p})
	}
	roots = append(roots,
		root{Name: "vendor/learn-terraform-modules", Scan: abs("testdata/vendor/learn-terraform-modules"), Project: "."},
		root{Name: "vendor/learn-terraform-lambda-api-gateway", Scan: abs("testdata/vendor/learn-terraform-lambda-api-gateway"), Project: "."},
		root{Name: "vendor/terraform-aws-sqs/examples/complete", Scan: abs("testdata/vendor/terraform-aws-sqs"), Project: "examples/complete"},
		root{Name: "edge", Scan: abs("testdata/edge"), Project: "."},
	)
	return roots
}

// TestFidelity runs every check on the catalog templates, the existing
// fixtures, the vendored corpus subset and the edge-case root.
func TestFidelity(t *testing.T) {
	var reps []*report
	for _, r := range localRoots(t) {
		t.Run(r.Name, func(t *testing.T) {
			rep, _, _ := checkRoot(t, r, localOptions())
			reps = append(reps, rep)
		})
	}
	for _, v := range edgeVariants(t) {
		t.Run(v.Name, func(t *testing.T) {
			rep, _, _ := checkRoot(t, v, localOptions())
			reps = append(reps, rep)
		})
	}
	t.Log("\n" + formatTable(reps))
	if p := os.Getenv("TFQA_REPORT_LOCAL"); p != "" {
		if err := os.WriteFile(p, []byte(formatTable(reps)), 0o644); err != nil {
			t.Error(err)
		}
	}
}

// edgeVariants derives roots from testdata/edge: CRLF line endings, tab
// indentation, and a very long file.
func edgeVariants(t *testing.T) []root {
	t.Helper()
	src, err := filepath.Abs("testdata/edge")
	if err != nil {
		t.Fatal(err)
	}
	leading := regexp.MustCompile(`(?m)^((?:  )+)`)
	variant := func(name string, edit func(name string, data []byte) []byte, extra map[string][]byte) root {
		dir := filepath.Join(t.TempDir(), name)
		copyTree(t, src, dir)
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".tf") && edit != nil {
				data, _ := os.ReadFile(p)
				os.WriteFile(p, edit(p, data), 0o644)
			}
			return nil
		})
		writeTree(t, dir, extra)
		return root{Name: "edge-" + name, Scan: dir, Project: "."}
	}
	var long strings.Builder
	long.WriteString("# A very long generated file.\n")
	for i := range 1500 {
		fmt.Fprintf(&long, "\n# Parameter %d\nresource \"aws_ssm_parameter\" \"p%04d\" {\n  name  = \"/edge/${var.stage}/p%04d\" # path\n  type  = \"String\"\n  value = jsonencode({ index = %d, queues = local.queue_names })\n}\n", i, i, i, i)
	}
	return []root{
		variant("crlf", func(_ string, data []byte) []byte {
			return []byte(strings.ReplaceAll(string(NormalizeNewlines(data)), "\n", "\r\n"))
		}, nil),
		variant("tabs", func(_ string, data []byte) []byte {
			// Heredoc bodies keep their indentation; only block indentation changes.
			if strings.Contains(string(data), "<<") {
				return data
			}
			return leading.ReplaceAllFunc(data, func(m []byte) []byte { return []byte(strings.Repeat("\t", len(m)/2)) })
		}, nil),
		variant("long", nil, map[string][]byte{"long.tf": []byte(long.String())}),
	}
}

// TestEdgeDetails checks edge-case behavior beyond the generic checks.
func TestEdgeDetails(t *testing.T) {
	base := nimbo(t)
	scan, _ := filepath.Abs("testdata/edge")
	wf, err := importRoot(base, root{Scan: scan, Project: "."})
	if err != nil {
		t.Fatal(err)
	}

	// Files Nimbo does not interpret are reported, not silently dropped.
	var unsupported []string
	for _, d := range wf.Diagnostics {
		if d.Code == "unsupported_file" {
			unsupported = append(unsupported, d.Message)
		}
	}
	for _, want := range []string{"override.tf", "extra.tf.json"} {
		if !slices.ContainsFunc(unsupported, func(m string) bool { return strings.HasPrefix(m, want+" ") }) {
			t.Errorf("no unsupported_file diagnostic for %s: %v", want, unsupported)
		}
	}

	// Variable files: root files keep their name, environments/ files and
	// .env.example become environments.
	envs := map[string]model.Environment{}
	for _, e := range wf.Environments {
		envs[e.Name] = e
	}
	if envs["default"].File != "terraform.tfvars" || envs["prod"].File != "prod.auto.tfvars" || envs["staging"].File != "" ||
		len(envs["staging"].EnvVars) != 2 || wf.ActiveEnvironment != "default" {
		t.Errorf("environments = %+v (active %q)", wf.Environments, wf.ActiveEnvironment)
	}

	exp := filepath.Join(t.TempDir(), "export")
	if err := exportTo(base, wf.ID, exp); err != nil {
		t.Fatal(err)
	}
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join(exp, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	main := read("main.tf")
	for _, want := range []string{
		// Comments stay where they were.
		"  name                       = \"${local.name}-${each.key}${each.value.fifo ? \".fifo\" : \"\"}\"\n",
		"  visibility_timeout_seconds = 30 /* inline block comment */\n",
		"  # Commented-out settings stay in the export:\n  # kms_master_key_id = \"alias/aws/sqs\"\n  # sqs_managed_sse_enabled = false\n}\n",
		"# This file is intentionally commented out: the legacy bucket was removed.\n#\n# resource \"aws_s3_bucket\" \"legacy\" {\n",
		"# Adopt queues created outside Terraform.\nimport {\n",
		// Heredocs and template directives are untouched.
		"      MOTD       = <<EOT\nWelcome to ${local.name}!\n%{if var.stage == \"prod\"~}\n",
		"    command     = <<-EOT\n      echo \"queues changed\"\n",
		// Module calls keep their provider mapping; local sources point at the copy.
		"  source = \"./modules/app\"\n",
		"    aws.replica = aws.west\n",
		"  role_arn    = provider::aws::arn_parse(\"arn:aws:iam::444455556666:role/example\")\n",
	} {
		if !strings.Contains(main, want) {
			t.Errorf("main.tf lacks %q\n---\n%s", want, main)
		}
	}
	// Both terraform blocks merge into one; the file headers of both stay
	// above it and the comments inside stay inside.
	if v := read("versions.tf"); !strings.HasPrefix(v, "# A second terraform block: Terraform merges every terraform block.\n\n/*\n * Edge-case root module") ||
		!strings.Contains(v, "# Toolchain pins.\nterraform {\n") ||
		!strings.Contains(v, "  required_version = \">= 1.10.0\" # ephemeral variables\n") ||
		!strings.Contains(v, "  // the backend is configured per environment\n}\n") || !strings.Contains(v, "  backend \"local\" {") {
		t.Errorf("versions.tf:\n%s", v)
	}
	if o := read("outputs.tf"); !strings.Contains(o, "} # end of outputs\n") {
		t.Errorf("outputs.tf lacks the end-of-block comment:\n%s", o)
	}
	// The nested module call of the copied module points at its flat copy.
	if m := read("modules/app/main.tf"); !strings.Contains(m, `source = "../labels"`) {
		t.Errorf("modules/app/main.tf:\n%s", m)
	}
	if read("override.tf") == "" || read("extra.tf.json") == "" || read("artifacts/bootstrap.zip") == "" {
		t.Error("files Nimbo does not interpret are missing from the export")
	}
}
