package hclcodec

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type fakeSchema struct{}

func (fakeSchema) Attribute(kind model.NodeKind, typ, attr string) (AttrInfo, bool) {
	switch typ + "." + attr {
	case "aws_iam_role.arn":
		return AttrInfo{Type: "string", Description: "Amazon Resource Name (ARN) specifying the role."}, true
	case "aws_sqs_queue.arn":
		return AttrInfo{Type: "string", Description: "ARN of the SQS queue."}, true
	case "aws_sqs_queue.url":
		return AttrInfo{Type: "string", Description: "Same as `id`: The URL for the created Amazon SQS queue."}, true
	case "aws_sqs_queue.tags":
		return AttrInfo{Type: "map(string)", Description: "Map of tags."}, true
	case "aws_db_instance.password":
		return AttrInfo{Type: "string", Sensitive: true}, true
	}
	return AttrInfo{}, false
}

func (fakeSchema) HasSensitive(_ model.NodeKind, typ string) bool { return typ == "aws_db_instance" }

const extractRoot = `
terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

provider "aws" {
  alias  = "east"
  region = "us-east-2"
}

variable "env" {
  type        = string
  description = "Deployment environment."
  validation {
    condition     = length(var.env) > 0
    error_message = "env must not be empty."
  }
}

locals {
  tags   = { env = var.env }
  prefix = "${var.env}-app"
}

resource "aws_iam_role" "lambda" {
  name               = "r"
  assume_role_policy = "{}"
}

# The work queue.
resource "aws_sqs_queue" "jobs" {
  name = "${var.env}-jobs"
  tags = local.tags
}

resource "aws_sqs_queue" "dlq" {
  count    = 2
  provider = aws.east
  name     = "dlq-${count.index}"
}

resource "aws_lambda_function" "worker" {
  function_name = "${local.prefix}-worker"
  role          = aws_iam_role.lambda.arn
  filename      = "${path.module}/build/fn.zip"
  depends_on    = [aws_iam_role.lambda]
}

resource "aws_lambda_event_source_mapping" "m" {
  event_source_arn = aws_sqs_queue.jobs.arn
  function_name    = aws_lambda_function.worker.arn
}

resource "aws_cloudwatch_log_group" "lg" {
  name       = "x"
  depends_on = [aws_sqs_queue.jobs]
}

import {
  to = aws_sqs_queue.jobs
  id = "https://sqs/jobs"
}

output "queue_url" {
  value = aws_sqs_queue.jobs.url
}

output "dlq_ids" {
  value = aws_sqs_queue.dlq[*].id
}
`

func extractFixture(t *testing.T) *model.Workflow {
	t.Helper()
	wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(extractRoot)})
	if diags := Normalize(wf); hasErrors(diags) {
		t.Fatalf("fixture diagnostics: %+v", diags)
	}
	for i := range wf.Nodes {
		wf.Nodes[i].Position = model.Position{X: float64(100 * i), Y: 32}
		wf.Nodes[i].Positioned = true
	}
	return wf
}

func hasErrors(diags []model.Diagnostic) bool {
	return slices.ContainsFunc(diags, func(d model.Diagnostic) bool { return d.Severity == model.SeverityError })
}

func expr(t *testing.T, b *model.Body, name string) string {
	t.Helper()
	return strings.TrimSpace(attr(t, b, name).Expr)
}

func namedBody(t *testing.T, list []model.NamedBlock, name string) *model.Body {
	t.Helper()
	for i := range list {
		if list[i].Name == name {
			return &list[i].Body
		}
	}
	t.Fatalf("%s not found", name)
	return nil
}

func movedBlocks(wf *model.Workflow) []model.MovedAddress {
	var out []model.MovedAddress
	for _, b := range wf.Other {
		if b.Type == "moved" {
			out = append(out, model.MovedAddress{From: getAttrValue(&b.Body, "from"), To: getAttrValue(&b.Body, "to")})
		}
	}
	return out
}

// moduleFiles strips the modules/<name>/ prefix of an extraction's files.
func moduleFiles(ex *Extraction) map[string][]byte {
	out := map[string][]byte{}
	for p, data := range ex.Contents {
		out[p[strings.LastIndex(p, "/")+1:]] = data
	}
	return out
}

func TestExtractModule(t *testing.T) {
	wf := extractFixture(t)
	ex, err := ExtractModule(wf, []string{"aws_sqs_queue.jobs", "aws_sqs_queue.dlq", "aws_lambda_function.worker"}, "queue_worker", ExtractOptions{Schema: fakeSchema{}})
	if err != nil {
		t.Fatal(err)
	}
	if diags := Normalize(wf); hasErrors(diags) {
		t.Fatalf("root diagnostics after extraction: %+v", diags)
	}

	// Inputs: root variable (type, description, validation kept), locals
	// (typed from the attribute they feed, or from a string value), node
	// attribute (typed from the provider schema).
	gotIn := map[string]model.ModuleIO{}
	for _, in := range ex.Inputs {
		gotIn[in.Name] = in
	}
	wantIn := map[string]model.ModuleIO{
		"env":        {Name: "env", Type: "string", Description: "Deployment environment.", Value: "var.env"},
		"tags":       {Name: "tags", Type: "map(string)", Description: "Value of local.tags in the root module.", Value: "local.tags"},
		"prefix":     {Name: "prefix", Type: "string", Description: "Value of local.prefix in the root module.", Value: "local.prefix"},
		"lambda_arn": {Name: "lambda_arn", Type: "string", Description: "Amazon Resource Name (ARN) specifying the role (aws_iam_role.lambda.arn in the root module).", Value: "aws_iam_role.lambda.arn"},
	}
	if !maps.Equal(gotIn, wantIn) {
		t.Errorf("inputs = %+v", ex.Inputs)
	}
	gotOut := map[string]string{}
	for _, o := range ex.Outputs {
		gotOut[o.Name] = o.Value
	}
	wantOut := map[string]string{
		"jobs_arn": "aws_sqs_queue.jobs.arn", "worker_arn": "aws_lambda_function.worker.arn",
		"jobs_url": "aws_sqs_queue.jobs.url", "dlq": "aws_sqs_queue.dlq",
	}
	if !maps.Equal(gotOut, wantOut) {
		t.Errorf("outputs = %+v", ex.Outputs)
	}
	wantMoved := []model.MovedAddress{
		{From: "aws_sqs_queue.jobs", To: "module.queue_worker.aws_sqs_queue.jobs"},
		{From: "aws_sqs_queue.dlq", To: "module.queue_worker.aws_sqs_queue.dlq"},
		{From: "aws_lambda_function.worker", To: "module.queue_worker.aws_lambda_function.worker"},
	}
	if !slices.Equal(ex.Moved, wantMoved) || !slices.Equal(movedBlocks(wf), wantMoved) {
		t.Errorf("moved = %+v, blocks %+v", ex.Moved, movedBlocks(wf))
	}
	if !slices.Equal(ex.Providers, []string{"aws", "aws.east"}) || !slices.Equal(ex.DependsOn, []string{"aws_iam_role.lambda"}) {
		t.Errorf("providers %v, depends_on %v", ex.Providers, ex.DependsOn)
	}

	// The call replaces the moved nodes at their centroid.
	call := node(t, wf, "module.queue_worker")
	if call.ID != "module.queue_worker" || !call.Positioned || call.Position != (model.Position{X: 208, Y: 32}) {
		t.Errorf("call node = %+v", call)
	}
	for _, addr := range []string{"aws_sqs_queue.jobs", "aws_sqs_queue.dlq", "aws_lambda_function.worker"} {
		if findNodeRef(wf, addr) != nil {
			t.Errorf("%s still in the root", addr)
		}
	}
	for name, want := range map[string]string{
		"source": `"./modules/queue_worker"`, "env": "var.env", "tags": "local.tags", "prefix": "local.prefix",
		"lambda_arn": "aws_iam_role.lambda.arn", "depends_on": "[aws_iam_role.lambda]",
	} {
		if got := expr(t, &call.Body, name); got != want {
			t.Errorf("call %s = %s, want %s", name, got, want)
		}
	}
	if got := compactExpr(expr(t, &call.Body, "providers")); got != "{aws=awsaws.east=aws.east}" {
		t.Errorf("call providers = %s", got)
	}

	// Root references now read the module's outputs.
	m := node(t, wf, "aws_lambda_event_source_mapping.m")
	if expr(t, &m.Body, "event_source_arn") != "module.queue_worker.jobs_arn" || expr(t, &m.Body, "function_name") != "module.queue_worker.worker_arn" {
		t.Errorf("mapping = %+v", m.Body.Attrs)
	}
	if got := expr(t, &node(t, wf, "aws_cloudwatch_log_group.lg").Body, "depends_on"); got != "[module.queue_worker]" {
		t.Errorf("lg depends_on = %s", got)
	}
	if got := expr(t, namedBody(t, wf.Outputs, "queue_url"), "value"); got != "module.queue_worker.jobs_url" {
		t.Errorf("queue_url = %s", got)
	}
	if got := expr(t, namedBody(t, wf.Outputs, "dlq_ids"), "value"); got != "module.queue_worker.dlq[*].id" {
		t.Errorf("dlq_ids = %s", got)
	}
	for _, b := range wf.Other {
		if b.Type == "import" && getAttrValue(&b.Body, "to") != "module.queue_worker.aws_sqs_queue.jobs" {
			t.Errorf("import to = %s", getAttrValue(&b.Body, "to"))
		}
	}
	// Edges: the mapping now depends on the module call.
	found := false
	for _, e := range wf.Edges {
		if e.Source == call.ID && e.Target == m.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("no edge module.queue_worker -> mapping: %+v", wf.Edges)
	}

	// Module files: recommended layout, parseable, self-contained.
	files := moduleFiles(ex)
	for _, f := range []string{"versions.tf", "variables.tf", "outputs.tf", "README.md"} {
		if _, ok := files[f]; !ok {
			t.Errorf("module file %s missing: %v", f, ex.Files)
		}
	}
	versions := string(files["versions.tf"])
	for _, want := range []string{`required_version = ">= 1.5"`, `source                = "hashicorp/aws"`, `configuration_aliases = [aws.east]`} {
		if !strings.Contains(versions, want) {
			t.Errorf("versions.tf lacks %q:\n%s", want, versions)
		}
	}
	mod, diags, err := Import("mod", files)
	if err != nil {
		t.Fatal(err)
	}
	if diags = append(diags, Normalize(mod)...); hasErrors(diags) {
		t.Fatalf("module diagnostics: %+v", diags)
	}
	worker := node(t, mod, "aws_lambda_function.worker")
	if expr(t, &worker.Body, "role") != "var.lambda_arn" || expr(t, &worker.Body, "filename") != `"${path.root}/build/fn.zip"` {
		t.Errorf("worker = %+v", worker.Body.Attrs)
	}
	if attrOf(&worker.Body, "depends_on") != nil {
		t.Errorf("worker keeps depends_on on a root resource")
	}
	if expr(t, &node(t, mod, "aws_sqs_queue.jobs").Body, "name") != `"${var.env}-jobs"` {
		t.Errorf("jobs name = %s", expr(t, &node(t, mod, "aws_sqs_queue.jobs").Body, "name"))
	}
	if c := node(t, mod, "aws_sqs_queue.jobs").Body.Comments; c == nil || !slices.Contains(c.Lead, "# The work queue.") {
		t.Errorf("jobs comments lost: %+v", c)
	}
	env := namedBody(t, mod.Variables, "env")
	if len(env.Blocks) != 1 || env.Blocks[0].Type != "validation" {
		t.Errorf("env validation not copied: %+v", env.Blocks)
	}
	if !strings.Contains(string(files["README.md"]), "queue_worker") {
		t.Errorf("README:\n%s", files["README.md"])
	}

	// The root still exports and re-imports.
	root := mustExport(t, wf, ExportOptions{})
	again, _ := mustImport(t, root)
	if diags := Normalize(again); hasErrors(diags) {
		t.Fatalf("re-imported root: %+v", diags)
	}
	checkTerraformFmt(t, root)
	checkTerraformFmt(t, files)
}

func attrOf(b *model.Body, name string) *model.Attr {
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i]
		}
	}
	return nil
}

func TestInlineModuleRoundTrip(t *testing.T) {
	orig := extractFixture(t)
	wf := extractFixture(t)
	ex, err := ExtractModule(wf, []string{"aws_sqs_queue.jobs", "aws_sqs_queue.dlq", "aws_lambda_function.worker"}, "queue_worker", ExtractOptions{Schema: fakeSchema{}})
	if err != nil {
		t.Fatal(err)
	}
	Normalize(wf)
	res, err := InlineModule(wf, "module.queue_worker", moduleFiles(ex))
	if err != nil {
		t.Fatal(err)
	}
	if diags := Normalize(wf); hasErrors(diags) {
		t.Fatalf("diagnostics after inline: %+v", diags)
	}
	if res.Dir != "modules/queue_worker" {
		t.Errorf("dir = %s", res.Dir)
	}
	addrs := func(w *model.Workflow) []string {
		var out []string
		for i := range w.Nodes {
			out = append(out, Address(&w.Nodes[i]))
		}
		slices.Sort(out)
		return out
	}
	if !slices.Equal(addrs(wf), addrs(orig)) {
		t.Fatalf("nodes = %v, want %v", addrs(wf), addrs(orig))
	}
	// Extraction's moved blocks cancel out; the reverse ones remain so state
	// moved into the module comes back.
	wantMoved := []model.MovedAddress{
		{From: "module.queue_worker.aws_lambda_function.worker", To: "aws_lambda_function.worker"},
		{From: "module.queue_worker.aws_sqs_queue.dlq", To: "aws_sqs_queue.dlq"},
		{From: "module.queue_worker.aws_sqs_queue.jobs", To: "aws_sqs_queue.jobs"},
	}
	gotMoved := movedBlocks(wf)
	slices.SortFunc(gotMoved, func(a, b model.MovedAddress) int { return strings.Compare(a.From, b.From) })
	if !slices.Equal(gotMoved, wantMoved) {
		t.Errorf("moved = %+v", movedBlocks(wf))
	}
	for addr, attrs := range map[string]map[string]string{
		"aws_sqs_queue.jobs":                {"name": `"${var.env}-jobs"`, "tags": "local.tags"},
		"aws_sqs_queue.dlq":                 {"provider": "aws.east", "count": "2"},
		"aws_lambda_function.worker":        {"role": "aws_iam_role.lambda.arn", "filename": `"${path.root}/build/fn.zip"`, "depends_on": "[aws_iam_role.lambda]"},
		"aws_lambda_event_source_mapping.m": {"event_source_arn": "aws_sqs_queue.jobs.arn", "function_name": "aws_lambda_function.worker.arn"},
	} {
		n := node(t, wf, addr)
		for name, want := range attrs {
			if got := expr(t, &n.Body, name); got != want {
				t.Errorf("%s.%s = %s, want %s", addr, name, got, want)
			}
		}
	}
	if got := expr(t, namedBody(t, wf.Outputs, "dlq_ids"), "value"); got != "aws_sqs_queue.dlq[*].id" {
		t.Errorf("dlq_ids = %s", got)
	}
	if got := expr(t, &node(t, wf, "aws_cloudwatch_log_group.lg").Body, "depends_on"); got != "[aws_lambda_function.worker, aws_sqs_queue.dlq, aws_sqs_queue.jobs]" {
		t.Errorf("lg depends_on = %s", got)
	}
	for _, b := range wf.Other {
		if b.Type == "import" && getAttrValue(&b.Body, "to") != "aws_sqs_queue.jobs" {
			t.Errorf("import to = %s", getAttrValue(&b.Body, "to"))
		}
	}

	// Extracting again replaces the reverse moved blocks instead of chaining a cycle.
	if _, err := ExtractModule(wf, []string{"aws_sqs_queue.jobs"}, "queue_worker", ExtractOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, m := range movedBlocks(wf) {
		if m.From == "module.queue_worker.aws_sqs_queue.jobs" {
			t.Errorf("reverse moved block kept: %+v", movedBlocks(wf))
		}
	}
}

func TestExtractModuleCallAndAliases(t *testing.T) {
	wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(`
module "net" {
  source = "./modules/net"
  cidr   = "10.0.0.0/16"
}

resource "aws_instance" "web" {
  for_each  = toset(["a", "b"])
  subnet_id = module.net.subnet_id
}

resource "aws_eip" "ip" {
  for_each = aws_instance.web
  instance = each.value.id
}
`)})
	Normalize(wf)
	ex, err := ExtractModule(wf, []string{"module.net", "aws_instance.web"}, "app", ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Inputs) != 0 || len(ex.Providers) != 0 {
		t.Errorf("inputs %+v providers %v", ex.Inputs, ex.Providers)
	}
	if len(ex.Outputs) != 1 || ex.Outputs[0].Name != "web" || ex.Outputs[0].Value != "aws_instance.web" {
		t.Errorf("outputs = %+v", ex.Outputs)
	}
	want := []model.MovedAddress{{From: "module.net", To: "module.app.module.net"}, {From: "aws_instance.web", To: "module.app.aws_instance.web"}}
	if !slices.Equal(ex.Moved, want) {
		t.Errorf("moved = %+v", ex.Moved)
	}
	if got := expr(t, &node(t, wf, "aws_eip.ip").Body, "for_each"); got != "module.app.web" {
		t.Errorf("eip for_each = %s", got)
	}
	mod, _ := mustImport(t, moduleFiles(ex))
	if got := expr(t, &node(t, mod, "module.net").Body, "source"); got != `"../net"` {
		t.Errorf("nested source = %s", got)
	}
	if strings.Contains(string(ex.Contents["modules/app/versions.tf"]), "configuration_aliases") {
		t.Errorf("unexpected aliases:\n%s", ex.Contents["modules/app/versions.tf"])
	}
}

func TestExtractModuleSensitive(t *testing.T) {
	wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(`
resource "aws_db_instance" "db" {
  password = "x"
}

output "pw" {
  value     = aws_db_instance.db.password
  sensitive = true
}

output "whole" {
  value     = aws_db_instance.db
  sensitive = true
}
`)})
	Normalize(wf)
	ex, err := ExtractModule(wf, []string{"aws_db_instance.db"}, "db", ExtractOptions{Schema: fakeSchema{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range ex.Outputs {
		if !o.Sensitive {
			t.Errorf("output %s not sensitive", o.Name)
		}
	}
}

func TestExtractModuleRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		src, module string
		nodes       []string
		want        string
	}{
		"bad name":        {extractRoot, "QueueWorker", []string{"aws_sqs_queue.jobs"}, "snake_case"},
		"no nodes":        {extractRoot, "q", nil, "select at least one"},
		"unknown node":    {extractRoot, "q", []string{"aws_sqs_queue.nope"}, "not found"},
		"taken name":      {`module "q" { source = "./x" }` + "\n" + `resource "aws_sqs_queue" "a" {}`, "q", []string{"aws_sqs_queue.a"}, "already exists"},
		"trigger inside":  {"resource \"aws_sqs_queue\" \"a\" {}\nresource \"aws_sqs_queue\" \"b\" {\n  lifecycle {\n    replace_triggered_by = [aws_sqs_queue.a]\n  }\n}\n", "q", []string{"aws_sqs_queue.b"}, "replace_triggered_by references aws_sqs_queue.a"},
		"trigger outside": {"resource \"aws_sqs_queue\" \"a\" {}\nresource \"aws_sqs_queue\" \"b\" {\n  lifecycle {\n    replace_triggered_by = [aws_sqs_queue.a]\n  }\n}\n", "q", []string{"aws_sqs_queue.a"}, "replace_triggered_by references aws_sqs_queue.a"},
		"depends_on cycle": {`
resource "aws_sqs_queue" "a" {}
resource "aws_sqs_queue" "x" {
  depends_on = [aws_sqs_queue.a]
}
resource "aws_sqs_queue" "b" {
  name = aws_sqs_queue.x.name
}
`, "q", []string{"aws_sqs_queue.a", "aws_sqs_queue.b"}, "would form a cycle"},
		"call depends_on cycle": {`
resource "aws_sqs_queue" "a" {
  depends_on = [aws_sqs_queue.x]
}
resource "aws_sqs_queue" "x" {
  name = aws_sqs_queue.b.name
}
resource "aws_sqs_queue" "b" {}
`, "q", []string{"aws_sqs_queue.a", "aws_sqs_queue.b"}, "form a cycle"},
	} {
		t.Run(name, func(t *testing.T) {
			wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(tc.src)})
			Normalize(wf)
			before := toJSON(t, wf)
			_, err := ExtractModule(wf, tc.nodes, tc.module, ExtractOptions{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if toJSON(t, wf) != before {
				t.Errorf("workflow changed on error")
			}
		})
	}
}

const inlineModuleFiles = `
variable "name" {
  type = string
}

variable "size" {
  type    = number
  default = 2
}

locals {
  prefix = "app-${var.name}"
}

resource "aws_sqs_queue" "q" {
  name   = "${local.prefix}-${var.size}"
  policy = file("${path.module}/policy.json")
}

resource "aws_sns_topic" "t" {
  provider = aws.secondary
  name     = var.name
}

data "aws_caller_identity" "me" {}

output "queue_arn" {
  value = aws_sqs_queue.q.arn
}

output "label" {
  value = "${var.name}-${data.aws_caller_identity.me.account_id}"
}

output "total" {
  value = var.size + 1
}
`

func TestInlineImportedModule(t *testing.T) {
	wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(`
resource "aws_sqs_queue" "dep" {}

module "svc" {
  source = "./modules/svc"
  name   = upper("x")
  providers = {
    aws           = aws.east
    aws.secondary = aws.west
  }
  depends_on = [aws_sqs_queue.dep]
}

resource "aws_ssm_parameter" "p" {
  name  = module.svc.label
  value = module.svc.total * 2
  type  = module.svc.queue_arn
}

moved {
  from = aws_sqs_queue.q
  to   = module.svc.aws_sqs_queue.q
}
`)})
	Normalize(wf)
	wf.Nodes[1].Position = model.Position{X: 400, Y: 200}
	res, err := InlineModule(wf, "module.svc", map[string][]byte{"main.tf": []byte(inlineModuleFiles), "policy.json": []byte("{}")})
	if err != nil {
		t.Fatal(err)
	}
	if diags := Normalize(wf); hasErrors(diags) {
		t.Fatalf("diagnostics: %+v", diags)
	}
	q := node(t, wf, "aws_sqs_queue.q")
	for name, want := range map[string]string{
		"name":       `"${local.prefix}-${2}"`,
		"policy":     `file("${path.module}/modules/svc/policy.json")`,
		"provider":   "aws.east",
		"depends_on": "[aws_sqs_queue.dep]",
	} {
		if got := expr(t, &q.Body, name); got != want {
			t.Errorf("q.%s = %s, want %s", name, got, want)
		}
	}
	if got := expr(t, &node(t, wf, "aws_sns_topic.t").Body, "provider"); got != "aws.west" {
		t.Errorf("topic provider = %s", got)
	}
	if got := expr(t, &node(t, wf, "data.aws_caller_identity.me").Body, "provider"); got != "aws.east" {
		t.Errorf("data provider = %s", got)
	}
	if got := wf.Locals[0]; got.Name != "prefix" || strings.TrimSpace(got.Value.Expr) != `"app-${upper("x")}"` {
		t.Errorf("locals = %+v", wf.Locals)
	}
	p := node(t, wf, "aws_ssm_parameter.p")
	for name, want := range map[string]string{
		"name":  `"${upper("x")}-${data.aws_caller_identity.me.account_id}"`,
		"value": "(2 + 1) * 2",
		"type":  "aws_sqs_queue.q.arn",
	} {
		if got := expr(t, &p.Body, name); got != want {
			t.Errorf("p.%s = %s, want %s", name, got, want)
		}
	}
	want := []model.MovedAddress{
		{From: "module.svc.aws_sqs_queue.q", To: "aws_sqs_queue.q"},
		{From: "module.svc.aws_sns_topic.t", To: "aws_sns_topic.t"},
	}
	if !slices.Equal(movedBlocks(wf), want) || !slices.Equal(res.Moved, want) {
		t.Errorf("moved = %+v / %+v", movedBlocks(wf), res.Moved)
	}
	if q.Position.Y != 200 || !q.Positioned {
		t.Errorf("q position = %+v", q.Position)
	}
}

func TestInlineModuleRejects(t *testing.T) {
	mod := map[string][]byte{"main.tf": []byte("variable \"n\" {}\nresource \"aws_sqs_queue\" \"a\" {\n  name = var.n\n}\n")}
	for name, tc := range map[string]struct{ src, want string }{
		"count":      {"module \"m\" {\n  source = \"./modules/m\"\n  count = 2\n  n = \"x\"\n}\n", "uses count"},
		"registry":   {"module \"m\" {\n  source = \"terraform-aws-modules/sqs/aws\"\n}\n", "not a local module"},
		"twice":      {"module \"m\" {\n  source = \"./modules/m\"\n  n = \"x\"\n}\nmodule \"m2\" {\n  source = \"./modules/m\"\n  n = \"y\"\n}\n", "called more than once"},
		"missing":    {"module \"m\" {\n  source = \"./modules/m\"\n}\n", "has no value"},
		"clash":      {"module \"m\" {\n  source = \"./modules/m\"\n  n = \"x\"\n}\nresource \"aws_sqs_queue\" \"a\" {}\n", "exists both"},
		"whole":      {"module \"m\" {\n  source = \"./modules/m\"\n  n = \"x\"\n}\noutput \"o\" {\n  value = module.m\n}\n", "whole module.m"},
		"not module": {"resource \"aws_sqs_queue\" \"m\" {}\n", "not found"},
	} {
		t.Run(name, func(t *testing.T) {
			wf, _ := mustImport(t, map[string][]byte{"main.tf": []byte(tc.src)})
			Normalize(wf)
			before := toJSON(t, wf)
			_, err := InlineModule(wf, "module.m", mod)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if toJSON(t, wf) != before {
				t.Errorf("workflow changed on error")
			}
		})
	}
}
