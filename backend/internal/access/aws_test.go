package access

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func awsImport(t *testing.T, src string) *model.Workflow {
	t.Helper()
	wf, diags, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Fatalf("import: %+v", d)
		}
	}
	return wf
}

func awsNode(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("no node %s", addr)
	return nil
}

func awsView(t *testing.T, wf *model.Workflow, addr string) *model.AccessView {
	t.Helper()
	v, err := View(wf, awsNode(t, wf, addr))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func awsDoc(t *testing.T, v *model.AccessView, path string) model.AWSPolicyDocument {
	t.Helper()
	for _, d := range v.AWS {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("no document %q in %+v", path, v.AWS)
	return model.AWSPolicyDocument{}
}

// awsSave applies doc, normalizes, exports and re-imports (the save
// pipeline), returning the re-imported workflow and the exported main.tf.
func awsSave(t *testing.T, wf *model.Workflow, addr string, doc model.AWSPolicyDocument) (*model.Workflow, string) {
	t.Helper()
	n := awsNode(t, wf, addr)
	if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, AWS: &doc}); err != nil {
		t.Fatal(err)
	}
	for _, d := range hclcodec.Normalize(wf) {
		if d.Severity == model.SeverityError {
			t.Fatalf("normalize: %+v", d)
		}
	}
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return awsImport(t, string(files["main.tf"])), string(files["main.tf"])
}

func attrSrc(t *testing.T, wf *model.Workflow, addr, attr string) string {
	t.Helper()
	a := GetAttr(&awsNode(t, wf, addr).Body, attr)
	if a == nil {
		t.Fatalf("%s.%s unset", addr, attr)
	}
	return Source(a.Value)
}

const awsJSONEncodeSrc = `resource "aws_s3_bucket" "b" {
  bucket = "b"
}

resource "aws_iam_role" "app" {
  name = "app"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "lambda.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "app" {
  name = "app"
  role = aws_iam_role.app.id
  policy = jsonencode({
    Version = "2012-10-17"
    Id      = "keep-me"
    Statement = [
      {
        Sid      = "ReadObjects"
        Effect   = "Allow"
        Action   = "s3:GetObject"
        Resource = "${aws_s3_bucket.b.arn}/*"
        Condition = {
          Bool = { "aws:SecureTransport" = "true" }
        }
      },
      {
        Effect   = "Allow"
        Action   = ["s3:ListBucket"]
        Resource = aws_s3_bucket.b.arn
      },
    ]
  })
}
`

func TestAWSJSONEncodeRoundTrip(t *testing.T) {
	wf := awsImport(t, awsJSONEncodeSrc)
	v := awsView(t, wf, "aws_iam_role_policy.app")
	if v.Provider != "aws" || len(v.AWS) != 1 {
		t.Fatalf("view = %+v", v)
	}
	d := v.AWS[0]
	if d.Encoding != "jsonencode" || !d.Editable || d.Version != "2012-10-17" || len(d.Statements) != 2 {
		t.Fatalf("doc = %+v", d)
	}
	s0 := d.Statements[0]
	if s0.Sid != "ReadObjects" || s0.Effect != "Allow" || !slices.Equal(s0.Actions, []string{"s3:GetObject"}) ||
		!slices.Equal(s0.Resources, []string{`"${aws_s3_bucket.b.arn}/*"`}) || len(s0.Extra) != 1 || s0.Extra[0].Key != "Condition" || !s0.Editable {
		t.Fatalf("statement 0 = %+v", s0)
	}
	if got := d.Statements[1].Resources; !slices.Equal(got, []string{"aws_s3_bucket.b.arn"}) {
		t.Fatalf("statement 1 resources = %v", got)
	}

	// Nothing changed: the attribute is not touched.
	before := attrSrc(t, wf, "aws_iam_role_policy.app", "policy")
	if err := Apply(wf, awsNode(t, wf, "aws_iam_role_policy.app"), model.AccessEdit{AWS: &d}); err != nil {
		t.Fatal(err)
	}
	if after := attrSrc(t, wf, "aws_iam_role_policy.app", "policy"); after != before {
		t.Fatalf("unchanged edit rewrote the policy:\n%s", after)
	}

	// Change statement 1, add one: statement 0 (with its Condition) stays byte for byte.
	d.Statements[1].Actions = []string{"s3:ListBucket", "s3:GetBucketLocation"}
	d.Statements = append(d.Statements, model.AWSStatement{Sid: "Queue", Effect: "Allow", Actions: []string{"sqs:SendMessage"}, Resources: []string{"aws_sqs_queue.q.arn"}, Editable: true})
	wf2, out := awsSave(t, wf, "aws_iam_role_policy.app", d)
	pol := attrSrc(t, wf2, "aws_iam_role_policy.app", "policy")
	if !strings.Contains(out, s0.Source) && !strings.Contains(pol, strings.TrimSpace(strings.Split(s0.Source, "\n")[0])) {
		t.Fatalf("statement 0 lost:\n%s", out)
	}
	for _, want := range []string{`Id      = "keep-me"`, `"aws:SecureTransport" = "true"`, `["s3:ListBucket", "s3:GetBucketLocation"]`, `Sid      = "Queue"`, `Resource = aws_sqs_queue.q.arn`} {
		if !strings.Contains(out, want) {
			t.Errorf("exported policy missing %s:\n%s", want, out)
		}
	}
	d2 := awsDoc(t, awsView(t, wf2, "aws_iam_role_policy.app"), "policy")
	if len(d2.Statements) != 3 || d2.Statements[0].Source != awsDoc(t, awsView(t, awsImport(t, out), "aws_iam_role_policy.app"), "policy").Statements[0].Source {
		t.Fatalf("after save = %+v", d2.Statements)
	}
	if got := d2.Statements[0]; got.Sid != "ReadObjects" || len(got.Extra) != 1 {
		t.Fatalf("statement 0 after save = %+v", got)
	}

	// Delete the first statement: the others keep their source.
	src1 := d2.Statements[1].Source
	d2.Statements = d2.Statements[1:]
	wf3, _ := awsSave(t, wf2, "aws_iam_role_policy.app", d2)
	d3 := awsDoc(t, awsView(t, wf3, "aws_iam_role_policy.app"), "policy")
	if len(d3.Statements) != 2 || d3.Statements[0].Source != src1 {
		t.Fatalf("after delete = %+v", d3.Statements)
	}
}

func TestAWSUntouchedStatementBytes(t *testing.T) {
	wf := awsImport(t, awsJSONEncodeSrc)
	n := awsNode(t, wf, "aws_iam_role_policy.app")
	d := awsDoc(t, awsView(t, wf, "aws_iam_role_policy.app"), "policy")
	orig := d.Statements[0].Source
	d.Statements[1].Effect = "Deny"
	if err := Apply(wf, n, model.AccessEdit{AWS: &d}); err != nil {
		t.Fatal(err)
	}
	pol := attrSrc(t, wf, "aws_iam_role_policy.app", "policy")
	if !strings.Contains(pol, orig) {
		t.Fatalf("statement 0 not byte-identical:\n--- want substring\n%s\n--- got\n%s", orig, pol)
	}
	// Unchanged fields of the edited statement keep their style.
	if !strings.Contains(pol, `Action   = ["s3:ListBucket"]`) && !strings.Contains(pol, `Action = ["s3:ListBucket"]`) {
		t.Fatalf("edited statement lost its unchanged Action:\n%s", pol)
	}
	if !strings.Contains(pol, `"Deny"`) {
		t.Fatalf("effect not written:\n%s", pol)
	}
}

func TestAWSTrustPolicyAndRelated(t *testing.T) {
	wf := awsImport(t, awsJSONEncodeSrc+`
resource "aws_iam_policy" "managed" {
  name   = "managed"
  policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Action = "s3:GetObject", Resource = "*" }] })
}

resource "aws_iam_role_policy_attachment" "managed" {
  role       = aws_iam_role.app.name
  policy_arn = aws_iam_policy.managed.arn
}
`)
	v := awsView(t, wf, "aws_iam_role.app")
	trust := awsDoc(t, v, "assume_role_policy")
	if trust.Label != "Trust policy" || len(trust.Statements) != 1 {
		t.Fatalf("trust = %+v", trust)
	}
	st := trust.Statements[0]
	if len(st.Principals) != 1 || st.Principals[0].Type != "Service" || !slices.Equal(st.Principals[0].Values, []string{`"lambda.amazonaws.com"`}) ||
		!slices.Equal(st.Actions, []string{"sts:AssumeRole"}) {
		t.Fatalf("trust statement = %+v", st)
	}
	var labels []string
	for _, r := range v.Related {
		labels = append(labels, r.Address+"="+r.Label)
	}
	for _, want := range []string{"aws_iam_role_policy.app=Inline policy", "aws_iam_role_policy_attachment.managed=Managed policy attachment", "aws_iam_policy.managed=Managed policy"} {
		if !slices.Contains(labels, want) {
			t.Errorf("related %v missing %s", labels, want)
		}
	}

	// Add an AWS principal: the single Action string stays a string.
	st.Principals = append(st.Principals, model.AWSPrincipal{Type: "AWS", Values: []string{`"arn:aws:iam::123456789012:root"`}})
	trust.Statements[0] = st
	wf2, out := awsSave(t, wf, "aws_iam_role.app", trust)
	if !strings.Contains(out, `Action    = "sts:AssumeRole"`) || !strings.Contains(out, `AWS = "arn:aws:iam::123456789012:root"`) {
		t.Fatalf("trust policy after save:\n%s", out)
	}
	if got := awsDoc(t, awsView(t, wf2, "aws_iam_role.app"), "assume_role_policy").Statements[0].Principals; len(got) != 2 {
		t.Fatalf("principals after save = %+v", got)
	}
}

func TestAWSHeredocJSON(t *testing.T) {
	wf := awsImport(t, `resource "aws_s3_bucket" "b" {
  bucket = "b"
}

resource "aws_s3_bucket_policy" "b" {
  bucket = aws_s3_bucket.b.id
  policy = <<-EOT
    {
      "Version": "2012-10-17",
      "Statement": [
        {
          "Sid": "Public",
          "Effect": "Allow",
          "Principal": "*",
          "Action": "s3:GetObject",
          "Resource": "${aws_s3_bucket.b.arn}/*"
        },
        {
          "Effect": "Deny",
          "Principal": {"AWS": "*"},
          "Action": "s3:*",
          "Resource": ["${aws_s3_bucket.b.arn}", "${aws_s3_bucket.b.arn}/*"],
          "Condition": {"Bool": {"aws:SecureTransport": "false"}}
        }
      ]
    }
  EOT
}
`)
	v := awsView(t, wf, "aws_s3_bucket_policy.b")
	d := awsDoc(t, v, "policy")
	if d.Encoding != "json" || !d.Editable || len(d.Statements) != 2 || d.Label != "Bucket policy" {
		t.Fatalf("doc = %+v", d)
	}
	s0, s1 := d.Statements[0], d.Statements[1]
	if !slices.Equal(s0.Resources, []string{`"${aws_s3_bucket.b.arn}/*"`}) || s0.Principals[0].Type != "*" {
		t.Fatalf("statement 0 = %+v", s0)
	}
	if !slices.Equal(s1.Resources, []string{"aws_s3_bucket.b.arn", `"${aws_s3_bucket.b.arn}/*"`}) || len(s1.Extra) != 1 {
		t.Fatalf("statement 1 = %+v", s1)
	}
	if !slices.ContainsFunc(v.Warnings, func(w model.AccessWarning) bool {
		return w.Code == "aws_public_principal" && w.Path == "policy/statements/0"
	}) {
		t.Errorf("no public principal warning: %+v", v.Warnings)
	}
	// The Deny statement has a condition and is a Deny: no warning for it.
	if slices.ContainsFunc(v.Warnings, func(w model.AccessWarning) bool { return w.Path == "policy/statements/1" }) {
		t.Errorf("warning on the Deny statement: %+v", v.Warnings)
	}

	// Restrict statement 0 to a role: statement 1 keeps its text, the heredoc stays a <<-EOT heredoc.
	s0.Principals = []model.AWSPrincipal{{Type: "AWS", Values: []string{"aws_iam_role.r.arn"}}}
	s0.Resources = append(s0.Resources, "aws_s3_bucket.b.arn")
	d.Statements[0] = s0
	n := awsNode(t, wf, "aws_s3_bucket_policy.b")
	if err := Apply(wf, n, model.AccessEdit{AWS: &d}); err != nil {
		t.Fatal(err)
	}
	pol := attrSrc(t, wf, "aws_s3_bucket_policy.b", "policy")
	if !strings.HasPrefix(pol, "<<-EOT\n") || !strings.HasSuffix(pol, "\n  EOT") {
		t.Fatalf("heredoc style lost:\n%s", pol)
	}
	for _, want := range []string{
		`"Principal": {"AWS": "${aws_iam_role.r.arn}"}`,
		`"Resource": ["${aws_s3_bucket.b.arn}/*", "${aws_s3_bucket.b.arn}"]`,
		"        {\n          \"Effect\": \"Deny\",\n          \"Principal\": {\"AWS\": \"*\"},",
	} {
		if !strings.Contains(pol, want) {
			t.Errorf("policy missing %q:\n%s", want, pol)
		}
	}
	d2 := awsDoc(t, awsView(t, wf, "aws_s3_bucket_policy.b"), "policy")
	if d2.Statements[1].Source != s1.Source || !slices.Equal(d2.Statements[0].Resources, s0.Resources) {
		t.Fatalf("after write = %+v", d2.Statements)
	}
	// The written heredoc exports and re-imports as the same document.
	d2.Statements[0].Sid = "RoleReads"
	wf2, out := awsSave(t, wf, "aws_s3_bucket_policy.b", d2)
	if !strings.Contains(out, "policy = <<-EOT\n") || !strings.Contains(out, `"Sid": "RoleReads"`) {
		t.Fatalf("exported heredoc:\n%s", out)
	}
	if d3 := awsDoc(t, awsView(t, wf2, "aws_s3_bucket_policy.b"), "policy"); d3.Encoding != "json" || len(d3.Statements) != 2 || d3.Statements[1].Source != s1.Source {
		t.Fatalf("re-imported = %+v", d3)
	}
}

func TestAWSQuotedJSON(t *testing.T) {
	wf := awsImport(t, `resource "aws_iam_user_policy" "u" {
  name   = "u"
  user   = "bob"
  policy = "{\"Version\":\"2012-10-17\",\"Statement\":{\"Effect\":\"Allow\",\"Action\":\"sqs:SendMessage\",\"Resource\":\"*\"}}"
}
`)
	d := awsDoc(t, awsView(t, wf, "aws_iam_user_policy.u"), "policy")
	if d.Encoding != "json" || len(d.Statements) != 1 || !slices.Equal(d.Statements[0].Resources, []string{`"*"`}) {
		t.Fatalf("doc = %+v", d)
	}
	d.Statements[0].Resources = []string{"aws_sqs_queue.q.arn"}
	_, out := awsSave(t, wf, "aws_iam_user_policy.u", d)
	if !strings.Contains(out, `\"Resource\": \"${aws_sqs_queue.q.arn}\"`) || !strings.Contains(out, `policy = "{`) {
		t.Fatalf("quoted JSON after save:\n%s", out)
	}
}

const awsDocumentSrc = `data "aws_iam_policy_document" "q" {
  statement {
    sid     = "FromTopic"
    effect  = "Allow"
    actions = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.q.arn]
    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [aws_sns_topic.t.arn]
    }
  }
  statement {
    actions   = ["sqs:*"]
    resources = ["*"]
  }
}

resource "aws_sqs_queue" "q" {
  name = "q"
}

resource "aws_sns_topic" "t" {
  name = "t"
}

resource "aws_sqs_queue_policy" "q" {
  queue_url = aws_sqs_queue.q.url
  policy    = data.aws_iam_policy_document.q.json
}
`

func TestAWSPolicyDocumentBlocks(t *testing.T) {
	wf := awsImport(t, awsDocumentSrc)
	v := awsView(t, wf, "data.aws_iam_policy_document.q")
	d := awsDoc(t, v, "")
	if d.Encoding != "document" || len(d.Statements) != 2 {
		t.Fatalf("doc = %+v", d)
	}
	s0 := d.Statements[0]
	if s0.Sid != "FromTopic" || len(s0.Principals) != 1 || s0.Principals[0].Type != "Service" || len(s0.Extra) != 1 || s0.Extra[0].Key != "condition" {
		t.Fatalf("statement 0 = %+v", s0)
	}
	if d.Statements[1].Effect != "Allow" {
		t.Fatalf("default effect = %q", d.Statements[1].Effect)
	}
	if !slices.ContainsFunc(v.Warnings, func(w model.AccessWarning) bool { return w.Code == "aws_wildcard_action" && w.Path == "statements/1" }) {
		t.Errorf("warnings = %+v", v.Warnings)
	}
	if !slices.ContainsFunc(v.Related, func(r model.AccessRelated) bool { return r.Address == "aws_sqs_queue_policy.q" }) {
		t.Errorf("related = %+v", v.Related)
	}

	// Narrow statement 1, add a statement; the condition block survives.
	d.Statements[1].Actions = []string{"sqs:ReceiveMessage"}
	d.Statements[1].Resources = []string{"aws_sqs_queue.q.arn"}
	d.Statements = append(d.Statements, model.AWSStatement{Effect: "Deny", Actions: []string{"sqs:DeleteQueue"}, Resources: []string{"aws_sqs_queue.q.arn"},
		Principals: []model.AWSPrincipal{{Type: "AWS", Values: []string{`"*"`}}}})
	wf2, out := awsSave(t, wf, "data.aws_iam_policy_document.q", d)
	for _, want := range []string{`variable = "aws:SourceArn"`, `actions   = ["sqs:ReceiveMessage"]`, `effect    = "Deny"`, `identifiers = ["*"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("exported document missing %q:\n%s", want, out)
		}
	}
	d2 := awsDoc(t, awsView(t, wf2, "data.aws_iam_policy_document.q"), "")
	if len(d2.Statements) != 3 || d2.Statements[0].Source != s0.Source {
		t.Fatalf("after save = %+v", d2.Statements)
	}
	// A new statement can't carry a condition (only code can add blocks).
	d2.Statements = append(d2.Statements, model.AWSStatement{Effect: "Allow", Actions: []string{"sqs:GetQueueUrl"}, Extra: []model.AccessExtra{{Key: "condition", Source: "condition {}"}}})
	n := awsNode(t, wf2, "data.aws_iam_policy_document.q")
	var ee *EditError
	if err := Apply(wf2, n, model.AccessEdit{AWS: &d2}); !errors.As(err, &ee) {
		t.Fatalf("new statement with a condition: err = %v", err)
	}
}

func TestAWSReferenceAndUnsupported(t *testing.T) {
	wf := awsImport(t, awsDocumentSrc+`
resource "aws_iam_role_policy" "file" {
  name   = "file"
  role   = "r"
  policy = file("${path.module}/policy.json")
}
`)
	v := awsView(t, wf, "aws_sqs_queue_policy.q")
	d := awsDoc(t, v, "policy")
	doc := awsNode(t, wf, "data.aws_iam_policy_document.q")
	if d.Encoding != "reference" || d.Editable || d.Linked != doc.ID || len(d.Statements) != 2 || d.Statements[0].Editable {
		t.Fatalf("reference doc = %+v", d)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("reference warns itself: %+v", v.Warnings)
	}
	var ee *EditError
	n := awsNode(t, wf, "aws_sqs_queue_policy.q")
	if err := Apply(wf, n, model.AccessEdit{AWS: &d}); !errors.As(err, &ee) {
		t.Fatalf("edit of a reference: err = %v", err)
	}

	u := awsDoc(t, awsView(t, wf, "aws_iam_role_policy.file"), "policy")
	if u.Encoding != "unsupported" || u.Editable || u.Source == "" || u.Reason == "" {
		t.Fatalf("unsupported doc = %+v", u)
	}
	before := attrSrc(t, wf, "aws_iam_role_policy.file", "policy")
	u.Statements = []model.AWSStatement{{Effect: "Allow", Actions: []string{"s3:GetObject"}, Resources: []string{`"*"`}}}
	if err := Apply(wf, awsNode(t, wf, "aws_iam_role_policy.file"), model.AccessEdit{AWS: &u}); !errors.As(err, &ee) {
		t.Fatalf("edit of an unsupported policy: err = %v", err)
	}
	if after := attrSrc(t, wf, "aws_iam_role_policy.file", "policy"); after != before {
		t.Fatalf("unsupported policy rewritten: %s", after)
	}
}

func TestAWSReadOnlyStatements(t *testing.T) {
	wf := awsImport(t, `variable "extra" {
  type = list(any)
}

resource "aws_iam_policy" "p" {
  name = "p"
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = [for a in ["GetItem", "Query"] : "dynamodb:${a}"]
        Resource = "*"
      },
      var.extra[0],
      {
        Effect   = "Allow"
        Action   = "sqs:SendMessage"
        Resource = "*"
      },
    ]
  })
}
`)
	d := awsDoc(t, awsView(t, wf, "aws_iam_policy.p"), "policy")
	if len(d.Statements) != 3 || d.Statements[0].Editable || d.Statements[1].Editable || !d.Statements[2].Editable {
		t.Fatalf("statements = %+v", d.Statements)
	}
	n := awsNode(t, wf, "aws_iam_policy.p")
	var ee *EditError
	edit := d
	edit.Statements = slices.Clone(d.Statements)
	edit.Statements[0].Actions = []string{"dynamodb:GetItem"}
	if err := Apply(wf, n, model.AccessEdit{AWS: &edit}); !errors.As(err, &ee) {
		t.Fatalf("edit of a for-expression statement: err = %v", err)
	}
	edit.Statements = d.Statements[1:]
	if err := Apply(wf, n, model.AccessEdit{AWS: &edit}); !errors.As(err, &ee) {
		t.Fatalf("delete of a read-only statement: err = %v", err)
	}
	// Editable statements still change; read-only ones stay verbatim.
	edit.Statements = slices.Clone(d.Statements)
	edit.Statements[2].Resources = []string{"aws_sqs_queue.q.arn"}
	_, out := awsSave(t, wf, "aws_iam_policy.p", edit)
	if !strings.Contains(out, `[for a in ["GetItem", "Query"] : "dynamodb:${a}"]`) || !strings.Contains(out, "var.extra[0]") || !strings.Contains(out, "Resource = aws_sqs_queue.q.arn") {
		t.Fatalf("after save:\n%s", out)
	}
}

func TestAWSFreshPolicy(t *testing.T) {
	wf := awsImport(t, `resource "aws_iam_role_policy" "p" {
  name = "p"
  role = "r"
}
`)
	d := awsDoc(t, awsView(t, wf, "aws_iam_role_policy.p"), "policy")
	if d.Encoding != "jsonencode" || !d.Editable || len(d.Statements) != 0 {
		t.Fatalf("doc = %+v", d)
	}
	d.Statements = []model.AWSStatement{{Sid: "Read", Effect: "Allow", Actions: []string{"dynamodb:GetItem"}, Resources: []string{"aws_dynamodb_table.t.arn", `"${aws_dynamodb_table.t.arn}/index/*"`}}}
	_, out := awsSave(t, wf, "aws_iam_role_policy.p", d)
	for _, want := range []string{`Version = "2012-10-17"`, `Action   = ["dynamodb:GetItem"]`, `Resource = [aws_dynamodb_table.t.arn, "${aws_dynamodb_table.t.arn}/index/*"]`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestAWSWarnings(t *testing.T) {
	doc := model.AWSPolicyDocument{Path: "policy", Encoding: "jsonencode", Statements: []model.AWSStatement{
		{Effect: "Allow", Actions: []string{"*"}, Resources: []string{`"*"`}},
		{Effect: "Allow", Actions: []string{"dynamodb:*"}, Resources: []string{"aws_dynamodb_table.t.arn"}},
		{Effect: "Allow", Actions: []string{"dynamodb:GetItem", "sts:GetCallerIdentity"}, Resources: []string{`"*"`}},
		{Effect: "Allow", Actions: []string{"sts:GetCallerIdentity"}, Resources: []string{`"*"`}},
		{Effect: "Allow", Actions: []string{"s3:GetObject"}, Principals: []model.AWSPrincipal{{Type: "*", Values: []string{`"*"`}}}},
		{Effect: "Allow", Actions: []string{"s3:GetObject"}, Principals: []model.AWSPrincipal{{Type: "AWS", Values: []string{`"*"`}}}, Extra: []model.AccessExtra{{Key: "Condition", Source: "{}"}}},
		{Effect: "Deny", Actions: []string{"*"}, Resources: []string{`"*"`}},
	}}
	got := map[string][]string{}
	for _, w := range AWSWarnings(doc) {
		got[w.Path] = append(got[w.Path], w.Code)
	}
	want := map[string][]string{
		"policy/statements/0": {"aws_admin"},
		"policy/statements/1": {"aws_wildcard_action"},
		"policy/statements/2": {"aws_wildcard_resource"},
		"policy/statements/4": {"aws_public_principal"},
	}
	if len(got) != len(want) {
		t.Fatalf("warnings = %v", got)
	}
	for p, codes := range want {
		if !slices.Equal(got[p], codes) {
			t.Errorf("%s: %v, want %v", p, got[p], codes)
		}
	}
}

func TestAWSCatalog(t *testing.T) {
	c, err := Catalog("aws", CatalogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Source.Retrieved == "" || !strings.HasPrefix(c.Source.URL, "https://docs.aws.amazon.com/service-authorization/") || len(c.Services) < 300 {
		t.Fatalf("catalog = %+v (%d services)", c.Source, len(c.Services))
	}
	c, err = Catalog("aws", CatalogQuery{Service: "dynamodb", Search: "getitem"})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Actions) != 2 || c.Actions[1].Name != "dynamodb:GetItem" || c.Actions[1].Level != "Read" || !slices.Contains(c.Actions[1].ResourceTypes, "table") {
		t.Fatalf("actions = %+v", c.Actions)
	}
	for name, level := range map[string]string{"s3:ListBucket": "List", "iam:PutRolePolicy": "Permissions management", "sqs:TagQueue": "Tagging", "logs:PutLogEvents": "Write"} {
		if a, ok := AWSAction(name); !ok || a.Level != level {
			t.Errorf("%s = %+v, want %s", name, a, level)
		}
	}
	var ee *EditError
	if _, err := Catalog("aws", CatalogQuery{Service: "nope"}); !errors.As(err, &ee) {
		t.Errorf("unknown service: %v", err)
	}
}
