package hclcodec

import (
	"bytes"
	"cmp"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// testResourceFile stands in for catalog.ResourceFile.
func testResourceFile(typ string) string {
	for prefix, file := range map[string]string{
		"aws_vpc": "network.tf", "aws_subnet": "network.tf", "aws_security_group": "network.tf",
		"aws_lambda": "compute.tf", "aws_instance": "compute.tf",
		"aws_sqs": "messaging.tf", "aws_sns": "messaging.tf",
		"aws_s3": "storage.tf", "aws_iam": "iam.tf", "aws_dynamodb": "database.tf",
		"aws_apigatewayv2": "api.tf",
	} {
		if strings.HasPrefix(typ, prefix) {
			return file
		}
	}
	return ""
}

func lxExpr(e string) model.Value { return model.Value{Expr: e} }

func lxNode(typ, name string, attrs ...model.Attr) model.Node {
	return model.Node{Kind: model.KindResource, Type: typ, Name: name, Body: model.Body{Attrs: attrs, Blocks: []model.Block{}}}
}

func lxAttr(name, e string) model.Attr { return model.Attr{Name: name, Value: lxExpr(e)} }

// newProject is a workflow built on the canvas: nothing has a file.
func newProject(nodes ...model.Node) *model.Workflow {
	wf := &model.Workflow{
		Name:        "Orders",
		Description: "Takes orders over HTTP and queues them for processing.",
		Terraform: &model.Body{Attrs: []model.Attr{lxAttr("required_version", `">= 1.6"`)}, Blocks: []model.Block{{
			Type: "required_providers",
			Body: model.Body{Attrs: []model.Attr{lxAttr("aws", `{ source = "hashicorp/aws", version = "~> 6.0" }`)}},
		}}},
		Providers: []model.NamedBlock{{Name: "aws", Body: model.Body{
			Attrs:  []model.Attr{lxAttr("region", "var.region")},
			Blocks: []model.Block{{Type: "default_tags", Body: model.Body{Attrs: []model.Attr{lxAttr("tags", "local.common_tags")}}}},
		}}},
		Variables: []model.NamedBlock{
			{Name: "region", Body: model.Body{Attrs: []model.Attr{lxAttr("description", `"AWS region"`), lxAttr("type", "string"), lxAttr("default", `"us-east-1"`)}}},
			{Name: "environment", Body: model.Body{Attrs: []model.Attr{lxAttr("description", `"Deployment environment (dev, prod)"`), lxAttr("type", "string")}}},
			{Name: "db_password", Body: model.Body{Attrs: []model.Attr{lxAttr("type", "string"), lxAttr("default", `"hunter2"`), lxAttr("sensitive", "true")}}},
		},
		Locals:  []model.Attr{lxAttr("common_tags", `{ Project = "orders", Environment = var.environment }`)},
		Outputs: []model.NamedBlock{{Name: "queue_url", Body: model.Body{Attrs: []model.Attr{lxAttr("description", `"URL of the orders queue"`), lxAttr("value", "aws_sqs_queue.orders.url")}}}},
		Nodes:   nodes,
	}
	Normalize(wf)
	return wf
}

func largeProject() *model.Workflow {
	return newProject(
		lxNode("aws_sqs_queue", "orders", lxAttr("name", `"orders"`)),
		lxNode("aws_subnet", "a", lxAttr("vpc_id", "aws_vpc.main.id")),
		lxNode("aws_vpc", "main", lxAttr("cidr_block", `"10.0.0.0/16"`)),
		lxNode("aws_lambda_function", "worker", lxAttr("role", "aws_iam_role.worker.arn")),
		lxNode("aws_iam_role", "worker", lxAttr("assume_role_policy", "data.aws_iam_policy_document.assume.json")),
		lxNode("aws_s3_bucket", "uploads"),
		lxNode("random_id", "suffix", lxAttr("byte_length", "4")),
		model.Node{Kind: model.KindData, Type: "aws_iam_policy_document", Name: "assume", Body: model.Body{}},
		model.Node{Kind: model.KindModule, Type: "module", Name: "vpc_endpoints", Body: model.Body{Attrs: []model.Attr{lxAttr("source", `"./modules/endpoints"`)}}},
		model.Node{Kind: model.KindResource, Type: "aws_sqs_queue", Name: "dlq", Body: model.Body{}},
	)
}

func blockOrder(t *testing.T, src []byte) []string {
	t.Helper()
	wf, _ := mustImport(t, map[string][]byte{"x.tf": src})
	var out []string
	for _, p := range groupFiles(wf, layout(wf, nil))["x.tf"] {
		out = append(out, p.owner)
	}
	return out
}

func TestLayoutRecommended(t *testing.T) {
	wf := largeProject()
	files := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	want := map[string][]string{
		"versions.tf":  {"terraform"},
		"providers.tf": {"provider aws"},
		"variables.tf": {"variable region", "variable environment", "variable db_password"},
		"locals.tf":    {"locals"},
		"outputs.tf":   {"output queue_url"},
		"data.tf":      {"data.aws_iam_policy_document.assume"},
		"network.tf":   {"aws_vpc.main", "aws_subnet.a"}, // dependency order beats the name
		"compute.tf":   {"aws_lambda_function.worker"},
		"iam.tf":       {"aws_iam_role.worker"},
		"messaging.tf": {"aws_sqs_queue.dlq", "aws_sqs_queue.orders"},
		"storage.tf":   {"aws_s3_bucket.uploads"},
		"main.tf":      {"random_id.suffix", "module.vpc_endpoints"},
	}
	if got := keysOf(files); !slices.Equal(got, keysOf(want)) {
		t.Fatalf("files = %v, want %v", got, keysOf(want))
	}
	for name, blocks := range want {
		if got := blockOrder(t, files[name]); !slices.Equal(got, blocks) {
			t.Errorf("%s blocks = %v, want %v", name, got, blocks)
		}
	}
	if !strings.Contains(string(files["providers.tf"]), "default_tags {\n    tags = local.common_tags\n  }") {
		t.Errorf("providers.tf =\n%s", files["providers.tf"])
	}
	checkTerraformFmt(t, files)

	// NodeFiles reports the same placement.
	nf := NodeFiles(wf, testResourceFile)
	if nf["aws_vpc.main"] != "network.tf" || nf["data.aws_iam_policy_document.assume"] != "data.tf" || nf["module.vpc_endpoints"] != "main.tf" {
		t.Errorf("NodeFiles = %v", nf)
	}
}

func TestLayoutSmallProjectSingleMain(t *testing.T) {
	wf := newProject(
		lxNode("aws_sqs_queue", "orders"),
		lxNode("aws_s3_bucket", "uploads"),
		lxNode("aws_lambda_function", "worker", lxAttr("role", "aws_iam_role.worker.arn")),
		lxNode("aws_iam_role", "worker", lxAttr("assume_role_policy", "data.aws_iam_policy_document.assume.json")),
		lxNode("aws_vpc", "main"),
		model.Node{Kind: model.KindData, Type: "aws_iam_policy_document", Name: "assume"},
	)
	files := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	if got := keysOf(files); !slices.Equal(got, []string{"locals.tf", "main.tf", "outputs.tf", "providers.tf", "variables.tf", "versions.tf"}) {
		t.Fatalf("files = %v", got)
	}
	want := []string{
		"data.aws_iam_policy_document.assume", "aws_iam_role.worker", "aws_lambda_function.worker",
		"aws_s3_bucket.uploads", "aws_sqs_queue.orders", "aws_vpc.main",
	}
	if got := blockOrder(t, files["main.tf"]); !slices.Equal(got, want) {
		t.Errorf("main.tf blocks = %v, want %v", got, want)
	}

	// A sixth resource splits the project by category.
	wf.Nodes = append(wf.Nodes, lxNode("aws_sns_topic", "events"))
	Normalize(wf)
	files = mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	for _, name := range []string{"compute.tf", "data.tf", "iam.tf", "messaging.tf", "network.tf", "storage.tf"} {
		if _, ok := files[name]; !ok {
			t.Errorf("%s missing after growing past %d resources: %v", name, smallProject, keysOf(files))
		}
	}
	if _, ok := files["main.tf"]; ok {
		t.Errorf("main.tf still written:\n%s", files["main.tf"])
	}
}

func TestLayoutDeterministic(t *testing.T) {
	wf := largeProject()
	first := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile, Environments: true})
	if again := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile, Environments: true}); !reflect.DeepEqual(first, again) {
		t.Error("two exports differ")
	}
	// The order nodes were added in does not matter for unrecorded files.
	slices.Reverse(wf.Nodes)
	Normalize(wf)
	if reversed := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile, Environments: true}); !reflect.DeepEqual(first, reversed) {
		for name := range first {
			if !bytes.Equal(first[name], reversed[name]) {
				t.Errorf("%s depends on node order:\n%s\nvs\n%s", name, first[name], reversed[name])
			}
		}
	}
	a, b := Scaffold(wf, testResourceFile), Scaffold(wf, testResourceFile)
	if !reflect.DeepEqual(a, b) {
		t.Error("Scaffold is not deterministic")
	}
}

func TestLayoutImportedKeepsFilesAndPlacesNewBlocks(t *testing.T) {
	src := readFixture(t, "serverless")
	wf, _ := mustImport(t, src)
	for _, n := range wf.Nodes {
		if n.File == "" || n.Order == 0 {
			t.Errorf("node %s: file %q order %d not recorded", n.ID, n.File, n.Order)
		}
	}
	files := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	for name := range files {
		if _, ok := src[name]; !ok {
			t.Errorf("export added %s to an imported project", name)
		}
	}
	// Every block is written back to its own file in source order.
	for _, name := range []string{"api.tf", "lambda.tf"} {
		if got, want := blockOrder(t, files[name]), blockOrder(t, src[name]); !slices.Equal(got, want) {
			t.Errorf("%s blocks = %v, want %v", name, got, want)
		}
	}

	// New blocks: an IAM role joins the file holding the other IAM
	// resources, a queue (no messaging file yet) gets the recommended file,
	// a variable joins the other variables.
	wf.Nodes = append(wf.Nodes,
		lxNode("aws_iam_role", "worker"),
		lxNode("aws_sqs_queue", "jobs"),
	)
	wf.Variables = append(wf.Variables, model.NamedBlock{Name: "retention", Body: model.Body{Attrs: []model.Attr{lxAttr("type", "number")}}})
	Normalize(wf)
	nf := NodeFiles(wf, testResourceFile)
	if nf["aws_iam_role.worker"] != "lambda.tf" || nf["aws_sqs_queue.jobs"] != "messaging.tf" {
		t.Errorf("new nodes placed in %q and %q", nf["aws_iam_role.worker"], nf["aws_sqs_queue.jobs"])
	}
	files = mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	lambda := blockOrder(t, files["lambda.tf"])
	if lambda[len(lambda)-1] != "aws_iam_role.worker" {
		t.Errorf("new role not appended to lambda.tf: %v", lambda)
	}
	if v := blockOrder(t, files["variables.tf"]); v[len(v)-1] != "variable retention" {
		t.Errorf("variables.tf = %v", v)
	}

	// Moving a node to another file.
	node(t, wf, "aws_sqs_queue.jobs").File = "queues.tf"
	if got := NodeFiles(wf, testResourceFile)["aws_sqs_queue.jobs"]; got != "queues.tf" {
		t.Errorf("moved node in %s", got)
	}
}

func TestMergeImportedKeepsAutomaticFiles(t *testing.T) {
	prev := largeProject()
	files := mustExport(t, prev, ExportOptions{ResourceFile: testResourceFile})
	if _, ok := files["custom.tf"]; ok {
		t.Fatal("custom.tf in the recommended layout")
	}

	// Code edit: blocks written where Nimbo would put them, one elsewhere.
	files["storage.tf"] = append(files["storage.tf"], []byte("\nresource \"aws_s3_bucket\" \"extra\" {}\n")...)
	files["custom.tf"] = []byte("resource \"aws_sqs_queue\" \"custom\" {}\n")
	next, _ := mustImport(t, files)
	MergeImported(prev, next, testResourceFile)

	// The queue written to custom.tf keeps that file; the queues written to
	// messaging.tf keep theirs instead of following it; the rest stays
	// automatic.
	for _, n := range next.Nodes {
		want := ""
		switch n.Type {
		case "aws_sqs_queue":
			want = "messaging.tf"
			if n.Name == "custom" {
				want = "custom.tf"
			}
		}
		if n.File != want {
			t.Errorf("node %s file = %q, want %q", n.ID, n.File, want)
		}
	}
	if len(next.TerraformBlocks) != 0 || len(next.LocalsBlocks) != 0 || next.Variables[0].File != "" {
		t.Error("unchanged blocks kept a recorded file")
	}
	if got := mustExport(t, next, ExportOptions{ResourceFile: testResourceFile}); !reflect.DeepEqual(keysOf(got), keysOf(files)) {
		t.Errorf("files = %v, written %v", keysOf(got), keysOf(files))
	} else {
		for name := range files {
			if a, b := blockSet(t, got[name]), blockSet(t, files[name]); !slices.Equal(a, b) {
				t.Errorf("%s holds %v, written %v", name, a, b)
			}
		}
	}
}

// blockSet lists the blocks of a file, sorted.
func blockSet(t *testing.T, src []byte) []string {
	t.Helper()
	return slices.Sorted(slices.Values(blockOrder(t, src)))
}

// A first code edit of a small project that splits it into files (and
// deletes main.tf) keeps every block in its file, although blocks of one
// category are written to several files; applying it again changes nothing.
func TestMergeImportedKeepsWrittenFiles(t *testing.T) {
	prev := newProject(lxNode("aws_vpc", "main"), lxNode("aws_s3_bucket", "site"))
	written := mustExport(t, prev, ExportOptions{ResourceFile: testResourceFile})
	delete(written, "main.tf")
	written["network.tf"] = []byte("resource \"aws_vpc\" \"main\" {}\n\nresource \"aws_subnet\" \"public\" {}\n\nresource \"aws_subnet\" \"app\" {}\n\nresource \"aws_security_group\" \"app\" {}\n")
	written["alb.tf"] = []byte("resource \"aws_security_group\" \"alb\" {}\n\nresource \"aws_lb\" \"main\" {}\n")
	written["dns.tf"] = []byte("resource \"aws_subnet\" \"dns\" {}\n")
	written["storage.tf"] = []byte("resource \"aws_s3_bucket\" \"site\" {}\n\nresource \"aws_s3_bucket\" \"logs\" {}\n")
	written["iam.tf"] = []byte("resource \"aws_iam_role\" \"api\" {}\n\nresource \"aws_sqs_queue_policy\" \"orders\" {}\n")
	written["messaging.tf"] = []byte("resource \"aws_sqs_queue\" \"orders\" {}\n")

	apply := func(prev *model.Workflow) (*model.Workflow, map[string][]byte) {
		t.Helper()
		next, _ := mustImport(t, written)
		MergeImported(prev, next, testResourceFile)
		got := mustExport(t, next, ExportOptions{ResourceFile: testResourceFile})
		if !reflect.DeepEqual(keysOf(got), keysOf(written)) {
			t.Fatalf("files = %v, written %v", keysOf(got), keysOf(written))
		}
		for name := range written {
			if a, b := blockSet(t, got[name]), blockSet(t, written[name]); !slices.Equal(a, b) {
				t.Errorf("%s holds %v, written %v", name, a, b)
			}
		}
		return next, got
	}
	first, files := apply(prev)
	if node(t, first, "aws_vpc.main").ID != node(t, prev, "aws_vpc.main").ID {
		t.Error("the VPC moved out of the deleted main.tf lost its node")
	}
	second, again := apply(first)
	if !reflect.DeepEqual(files, again) {
		t.Error("applying the same code again changed the export")
	}
	for i := range first.Nodes {
		if a, b := first.Nodes[i], second.Nodes[i]; a.ID != b.ID || a.File != b.File || a.Order != b.Order {
			t.Errorf("node %s changed on re-apply: %q#%d -> %q#%d", a.ID, a.File, a.Order, b.File, b.Order)
		}
	}
}

func TestPinAndUnpinFiles(t *testing.T) {
	wf, _ := mustImport(t, readFixture(t, "serverless"))
	UnpinFiles(wf, testResourceFile)
	files := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	if _, ok := files["database.tf"]; !ok {
		t.Fatalf("unpinned workflow not laid out: %v", keysOf(files))
	}
	// Import(Export(wf)) == PinFiles(wf).
	back, _ := mustImport(t, files)
	PinFiles(wf, testResourceFile)
	wf.Environments, wf.ActiveEnvironment = back.Environments, back.ActiveEnvironment
	if a, b := toJSON(t, wf), toJSON(t, back); a != b {
		t.Errorf("pinned workflow differs from its re-import\nfirst:\n%s\nsecond:\n%s", a, b)
	}
}

func TestValidFileName(t *testing.T) {
	for _, ok := range []string{"main.tf", "network.tf", "my-stack_v2.tf", "a b.tf"} {
		if err := ValidFileName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".tf", "main.tf.json", "main", "modules/x.tf", `a\b.tf`, ".hidden.tf", "override.tf", "x_override.tf", "a\nb.tf"} {
		if ValidFileName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	wf := newProject(lxNode("aws_sqs_queue", "q"))
	wf.Nodes[0].File = "../q.tf"
	if diags := Normalize(wf); !slices.ContainsFunc(diags, func(d model.Diagnostic) bool { return d.Code == "invalid_file" && d.NodeID == wf.Nodes[0].ID }) {
		t.Errorf("no invalid_file diagnostic: %v", diags)
	}
	if _, err := Export(wf, ExportOptions{}); err == nil {
		t.Error("export accepted an invalid file name")
	}
}

func TestScaffold(t *testing.T) {
	wf := largeProject()
	wf.Environments = []model.Environment{{Name: "dev"}, {Name: "default", File: "terraform.tfvars"}}
	out := Scaffold(wf, testResourceFile)
	if got := keysOf(out); !slices.Equal(got, []string{GitignoreFile, ReadmeFile, TfvarsSampleFile}) {
		t.Fatalf("scaffold files = %v", got)
	}
	readme := string(out[ReadmeFile])
	for _, want := range []string{
		"# Orders\n\nTakes orders over HTTP and queues them for processing.\n",
		"terraform init\n",
		"`-var-file=environments/dev.tfvars`",
		"| terraform | `>= 1.6` |",
		"| aws | hashicorp/aws | `~> 6.0` |",
		"| vpc_endpoints | ./modules/endpoints | n/a | main.tf |",
		"| `aws_vpc.main` | resource | network.tf |",
		"| `data.aws_iam_policy_document.assume` | data source | data.tf |",
		"| db_password | n/a | `string` | (sensitive) | no |",
		"| environment | Deployment environment (dev, prod) | `string` | n/a | yes |",
		"| region | AWS region | `string` | `\"us-east-1\"` | no |",
		"| queue_url | URL of the orders queue | no |",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("README lacks %q:\n%s", want, readme)
		}
	}
	if strings.Contains(readme, "hunter2") {
		t.Error("README shows a sensitive default")
	}
	sample := string(out[TfvarsSampleFile])
	for _, want := range []string{
		"# AWS region\nregion = \"us-east-1\"\n",
		"# Deployment environment (dev, prod) (required)\nenvironment = \"\"\n",
		"db_password = \"\"\n",
	} {
		if !strings.Contains(sample, want) {
			t.Errorf("tfvars example lacks %q:\n%s", want, sample)
		}
	}
	if strings.Contains(sample, "hunter2") {
		t.Error("tfvars example shows a sensitive default")
	}
	for name, data := range out {
		if !IsScaffold(name, data) {
			t.Errorf("%s not recognized as scaffold", name)
		}
		if IsScaffold(name, []byte("# My project\n")) {
			t.Errorf("user %s taken for scaffold", name)
		}
	}
	ignore := strings.Split(string(out[GitignoreFile]), "\n")
	for _, want := range []string{"**/.terraform/*", "*.tfstate", "*.tfstate.*", "crash.log", "crash.*.log", "*.tfvars", "*.tfvars.json", "!*.tfvars.example"} {
		if !slices.Contains(ignore, want) {
			t.Errorf(".gitignore lacks %q:\n%s", want, out[GitignoreFile])
		}
	}

	// No variables: no tfvars example.
	wf.Variables = nil
	if _, ok := Scaffold(wf, testResourceFile)[TfvarsSampleFile]; ok {
		t.Error("tfvars example without variables")
	}

	// Module README: usage is a module call with the required inputs,
	// aligned like terraform fmt.
	mod := newProject(lxNode("aws_sqs_queue", "q"))
	mod.Name = "queue"
	readme = string(Readme(mod, ReadmeOptions{ModuleSource: "./modules/queue"}))
	if !strings.Contains(readme, "module \"queue\" {\n  source      = \"./modules/queue\"\n  environment = \"\"\n}") {
		t.Errorf("module README usage:\n%s", readme)
	}
}

func TestLayoutKeepsEveryLocalsAndTerraformBlock(t *testing.T) {
	src := map[string][]byte{
		"backend.tf":  []byte("# State.\nterraform {\n  backend \"local\" {\n    path = \"state/terraform.tfstate\"\n  }\n}\n"),
		"versions.tf": []byte("terraform {\n  required_version = \">= 1.6\"\n\n  required_providers {\n    aws = {\n      source  = \"hashicorp/aws\"\n      version = \"~> 6.0\"\n    }\n  }\n}\n"),
		"locals.tf":   []byte("locals {\n  name = \"app\"\n}\n"),
		"main.tf":     []byte("locals {\n  bucket = \"${local.name}-data\" # derived\n}\n\nresource \"aws_s3_bucket\" \"data\" {\n  bucket = local.bucket\n}\n\n# More locals.\nlocals {\n  tags = { App = local.name }\n}\n"),
	}
	wf, _ := mustImport(t, src)
	if len(wf.TerraformBlocks) != 2 || len(wf.LocalsBlocks) != 3 {
		t.Fatalf("parts: terraform %s locals %s", toJSON(t, wf.TerraformBlocks), toJSON(t, wf.LocalsBlocks))
	}
	files := mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	for name, data := range src {
		if got := string(files[name]); got != string(data) {
			t.Errorf("%s =\n%s\nwant\n%s", name, got, data)
		}
	}
	if len(files) != len(src) {
		t.Errorf("files = %v", keysOf(files))
	}

	// New items join the last recorded block of the file holding most
	// blocks; a single backend stays where it is.
	wf.Locals = append(wf.Locals, model.Attr{Name: "env", Value: lxExpr(`"dev"`)})
	wf.Terraform.Attrs = append(wf.Terraform.Attrs, model.Attr{Name: "experiments", Value: lxExpr("[]")})
	Normalize(wf)
	files = mustExport(t, wf, ExportOptions{ResourceFile: testResourceFile})
	if got := blockOrder(t, files["main.tf"]); !slices.Equal(got, []string{"locals", "aws_s3_bucket.data", "locals"}) {
		t.Errorf("main.tf blocks = %v", got)
	}
	if !strings.Contains(string(files["main.tf"]), "tags = { App = local.name }\n  env  = \"dev\"\n}") {
		t.Errorf("new local not in the last locals block of main.tf:\n%s", files["main.tf"])
	}
	if !strings.Contains(string(files["backend.tf"]), "experiments = []") || strings.Count(strings.Join([]string{string(files["backend.tf"]), string(files["versions.tf"])}, ""), "backend \"local\"") != 1 {
		t.Errorf("backend.tf =\n%s\nversions.tf =\n%s", files["backend.tf"], files["versions.tf"])
	}

	// Import(Export(wf)) == PinFiles(wf); UnpinFiles merges the parts.
	back, _ := mustImport(t, files)
	PinFiles(wf, testResourceFile)
	// Re-importing lists the items file by file.
	byPart := func(a, b model.Attr) int { return cmp.Or(strings.Compare(a.File, b.File), a.Order-b.Order) }
	slices.SortStableFunc(wf.Locals, byPart)
	slices.SortStableFunc(wf.Terraform.Attrs, byPart)
	if a, b := toJSON(t, wf), toJSON(t, back); a != b {
		t.Errorf("pinned workflow differs from its re-import\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	UnpinFiles(back, testResourceFile)
	files = mustExport(t, back, ExportOptions{ResourceFile: testResourceFile})
	if strings.Count(string(files["locals.tf"]), "locals {") != 1 || strings.Count(string(files["versions.tf"]), "terraform {") != 1 {
		t.Errorf("unpinned:\nlocals.tf\n%s\nversions.tf\n%s", files["locals.tf"], files["versions.tf"])
	}
}

func TestLayoutMovedNodesFollowImportedBlocks(t *testing.T) {
	wf, _ := mustImport(t, map[string][]byte{
		"main.tf": []byte("resource \"aws_sqs_queue\" \"b\" {}\n\nresource \"aws_sqs_queue\" \"a\" {}\n"),
		"other.tf": []byte("resource \"aws_sns_topic_subscription\" \"s\" {\n  topic_arn = aws_sns_topic.t.arn\n  endpoint  = aws_sqs_queue.a.arn\n}\n\n" +
			"resource \"aws_sns_topic\" \"t\" {}\n"),
	})
	// Move both other.tf resources into main.tf: they follow the imported
	// blocks, the topic before the subscription that references it.
	for _, addr := range []string{"aws_sns_topic_subscription.s", "aws_sns_topic.t"} {
		n := node(t, wf, addr)
		n.File, n.Order = "main.tf", 0
	}
	wf.Nodes = append(wf.Nodes, lxNode("aws_sqs_queue", "c"))
	wf.Nodes[len(wf.Nodes)-1].File = "main.tf"
	Normalize(wf)
	files := mustExport(t, wf, ExportOptions{})
	want := []string{"aws_sqs_queue.b", "aws_sqs_queue.a", "aws_sns_topic.t", "aws_sns_topic_subscription.s", "aws_sqs_queue.c"}
	if got := blockOrder(t, files["main.tf"]); !slices.Equal(got, want) {
		t.Errorf("main.tf blocks = %v, want %v", got, want)
	}
	if _, ok := files["other.tf"]; ok {
		t.Errorf("other.tf still written:\n%s", files["other.tf"])
	}
}
