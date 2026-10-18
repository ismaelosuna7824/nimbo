package hclcodec

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestCommentsRoundTrip(t *testing.T) {
	files := map[string][]byte{
		"main.tf": []byte(`# File header.
# Second header line.

// Queue for jobs.
resource "aws_sqs_queue" "jobs" { # opening
  name = "jobs" # the name

  /* Delay
     in seconds */
  delay_seconds = 5

  lifecycle {
    # keep it
    prevent_destroy = true
  } # end lifecycle

  # commented out:
  # tags = {}
} # end jobs

variable "stage" {
  default = "dev"
}

# End of main.tf.
`),
		"locals.tf": []byte(`locals {
  a = 1
  # tail a
}

# second block
locals {
  b = 2
}
`),
		"notes.tf": []byte("# only comments here\n"),
	}
	wf, _ := mustImport(t, files)

	q := wf.Nodes[0]
	want := &model.Comments{
		Lead: []string{"# File header.", "# Second header line.", "", "// Queue for jobs."},
		Line: "# opening",
		Tail: []string{"", "# commented out:", "# tags = {}"},
		End:  "# end jobs",
	}
	if !reflect.DeepEqual(q.Body.Comments, want) {
		t.Errorf("queue comments = %s", toJSON(t, q.Body.Comments))
	}
	if c := q.Body.Attrs[0].Comments; c == nil || c.Line != "# the name" || len(c.Lead) != 0 {
		t.Errorf("name comments = %s", toJSON(t, c))
	}
	if c := q.Body.Attrs[1].Comments; c == nil || !slices.Equal(c.Lead, []string{"", "/* Delay\n     in seconds */"}) {
		t.Errorf("delay_seconds comments = %s", toJSON(t, c))
	}
	lc := q.Body.Blocks[0].Body
	if lc.Comments == nil || lc.Comments.End != "# end lifecycle" || lc.Attrs[0].Comments == nil || lc.Attrs[0].Comments.Lead[0] != "# keep it" {
		t.Errorf("lifecycle comments = %s", toJSON(t, lc))
	}
	// Locals: the second block's header leads the merged block, the first
	// block's tail leads the second block's first attribute.
	if !reflect.DeepEqual(wf.LocalsComments, &model.Comments{Lead: []string{"# second block"}}) {
		t.Errorf("locals comments = %s", toJSON(t, wf.LocalsComments))
	}
	if c := wf.Locals[1].Comments; c == nil || !slices.Equal(c.Lead, []string{"# tail a"}) {
		t.Errorf("local b comments = %s", toJSON(t, c))
	}
	// End-of-file comments (also of comment-only files) stay after the
	// block they followed, which Export writes last in variables.tf.
	if c := q.Body.Comments; len(c.After) != 0 {
		t.Errorf("queue After = %v", c.After)
	}
	if v := wf.Variables[0].Body.Comments; v == nil || !slices.Equal(v.After, []string{"", "# End of main.tf.", "", "# only comments here"}) {
		t.Errorf("variable comments = %s", toJSON(t, v))
	}

	out := mustExport(t, wf, ExportOptions{})
	wantMain := `# second block
locals {
  a = 1
  # tail a
  b = 2
}

# File header.
# Second header line.

// Queue for jobs.
resource "aws_sqs_queue" "jobs" { # opening
  name = "jobs"                   # the name

  /* Delay
     in seconds */
  delay_seconds = 5

  lifecycle {
    # keep it
    prevent_destroy = true
  } # end lifecycle

  # commented out:
  # tags = {}
} # end jobs
`
	if got := string(out["main.tf"]); got != wantMain {
		t.Errorf("main.tf =\n%s\nwant\n%s", got, wantMain)
	}
	wantVars := "variable \"stage\" {\n  default = \"dev\"\n}\n\n# End of main.tf.\n\n# only comments here\n"
	if got := string(out["variables.tf"]); got != wantVars {
		t.Errorf("variables.tf =\n%q\nwant\n%q", got, wantVars)
	}
	checkTerraformFmt(t, out)

	// Import(Export(x)) == x, and exporting again is byte-identical.
	back, _ := mustImport(t, out)
	if a, b := toJSON(t, wf), toJSON(t, back); a != b {
		t.Errorf("re-imported workflow differs\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	if !reflect.DeepEqual(out, mustExport(t, back, ExportOptions{})) {
		t.Error("re-exported files differ")
	}
}

func TestCommentsSurviveEdits(t *testing.T) {
	src := map[string][]byte{"main.tf": []byte(`# The bucket.
resource "aws_s3_bucket" "site" {
  bucket = "site" # name
}

resource "aws_s3_bucket_website_configuration" "site" {
  bucket = aws_s3_bucket.site.id # reference
}
`)}
	wf, _ := mustImport(t, src)
	// Renaming rewrites references, not comments.
	if err := RenameNode(wf, "aws_s3_bucket.site", "www"); err != nil {
		t.Fatal(err)
	}
	// Code edits re-import the code and merge app-only data.
	next, _ := mustImport(t, mustExport(t, wf, ExportOptions{}))
	MergeImported(wf, next)
	main := string(mustExport(t, next, ExportOptions{})["main.tf"])
	for _, want := range []string{"# The bucket.\nresource \"aws_s3_bucket\" \"www\" {\n  bucket = \"site\" # name\n", "  bucket = aws_s3_bucket.www.id # reference\n"} {
		if !strings.Contains(main, want) {
			t.Errorf("main.tf lacks %q:\n%s", want, main)
		}
	}
}

func TestExportRejectsInvalidComments(t *testing.T) {
	wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte("resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"b\"\n}\n")})
	for _, c := range []*model.Comments{
		{Lead: []string{"not a comment"}},
		{Line: "# one\n# two"},
		{Tail: []string{"# ok", "resource \"x\" \"y\" {}"}},
		{End: "*/"},
	} {
		wf.Nodes[0].Body.Comments = c
		if _, err := Export(wf, ExportOptions{}); err == nil || !strings.Contains(err.Error(), "invalid comment") {
			t.Errorf("Export with comments %+v: err = %v", c, err)
		}
	}
	// A line comment after a heredoc goes on the next line.
	wf.Nodes[0].Body.Comments = nil
	wf.Nodes[0].Body.Attrs[0] = model.Attr{Name: "bucket", Value: model.Value{Expr: "<<EOT\nb\nEOT"}, Comments: &model.Comments{Line: "# heredoc"}}
	out := mustExport(t, wf, ExportOptions{})
	if got := string(out["main.tf"]); !strings.Contains(got, "  bucket = <<EOT\nb\nEOT\n  # heredoc\n}") {
		t.Errorf("main.tf =\n%s", got)
	}
}

func TestImportNormalizesAndSkipsFiles(t *testing.T) {
	files := map[string][]byte{
		"main.tf":                []byte("locals {\r\n  motd = <<EOT\r\nhello\r\nEOT\r\n}\r\n"),
		"override.tf":            []byte("locals {\n  motd = \"overridden\"\n}\n"),
		"x_override.tf":          []byte("locals {\n  extra = 1\n}\n"),
		"extra.tf.json":          []byte(`{"locals": {"j": 1}}`),
		"terraform.tfvars":       []byte("a = 1\n"),
		"terraform.auto.tfvars":  []byte("a = 2\n"),
		"environments/qa.tfvars": []byte("a = 3\n"),
	}
	wf, diags := mustImport(t, files)
	if len(wf.Locals) != 1 || wf.Locals[0].Value.Expr != "<<EOT\nhello\nEOT" {
		t.Errorf("locals = %s", toJSON(t, wf.Locals))
	}
	var skipped []string
	for _, d := range diags {
		if d.Code == "unsupported_file" {
			skipped = append(skipped, strings.Fields(d.Message)[0])
		}
	}
	slices.Sort(skipped)
	if !slices.Equal(skipped, []string{"extra.tf.json", "override.tf", "x_override.tf"}) {
		t.Errorf("unsupported files = %v", skipped)
	}
	got := map[string]string{}
	for _, e := range wf.Environments {
		got[e.Name] = e.File + "=" + e.Values["a"].Expr
	}
	want := map[string]string{"default": "terraform.tfvars=1", "terraform.auto": "terraform.auto.tfvars=2", "qa": "=3"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environments = %v, want %v", got, want)
	}
	out := mustExport(t, wf, ExportOptions{Environments: true})
	for _, name := range []string{"terraform.tfvars", "terraform.auto.tfvars", "environments/qa.tfvars"} {
		if _, ok := out[name]; !ok {
			t.Errorf("export lacks %s", name)
		}
	}
}
