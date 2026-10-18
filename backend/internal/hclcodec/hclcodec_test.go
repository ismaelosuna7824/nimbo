package hclcodec

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func readFixture(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	root := filepath.Join("testdata", dir)
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = b
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func mustImport(t *testing.T, files map[string][]byte) (*model.Workflow, []model.Diagnostic) {
	t.Helper()
	wf, diags, err := Import("test", files)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Fatalf("unexpected error diagnostic: %+v", d)
		}
	}
	return wf, diags
}

func mustExport(t *testing.T, wf *model.Workflow, opts ExportOptions) map[string][]byte {
	t.Helper()
	files, err := Export(wf, opts)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	return files
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func node(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("node %s not found", addr)
	return nil
}

func attr(t *testing.T, b *model.Body, name string) *model.Value {
	t.Helper()
	for i := range b.Attrs {
		if b.Attrs[i].Name == name {
			return &b.Attrs[i].Value
		}
	}
	t.Fatalf("attribute %s not found", name)
	return nil
}

func TestImportExportRoundTrip(t *testing.T) {
	tests := []struct {
		fixture   string
		files     []string
		nodes     []string
		providers []string
		clouds    []model.Cloud
		envs      []string
	}{
		{
			fixture: "serverless",
			files:   []string{"main.tf", "outputs.tf", "providers.tf", "variables.tf", "versions.tf"},
			nodes: []string{
				"data.archive_file.handler", "data.aws_iam_policy_document.assume",
				"aws_dynamodb_table.orders", "aws_apigatewayv2_api.http", "aws_apigatewayv2_integration.lambda",
				"aws_apigatewayv2_route.orders", "aws_apigatewayv2_stage.default", "aws_lambda_permission.apigw",
				"aws_iam_role.lambda", "aws_iam_role_policy.table_access", "aws_lambda_function.api",
			},
			providers: []string{"aws"},
			clouds:    []model.Cloud{model.CloudAWS},
			envs:      []string{"prod", "default"},
		},
		{
			fixture: "network",
			files:   []string{"main.tf", "outputs.tf", "providers.tf", "variables.tf", "versions.tf"},
			nodes: []string{
				"aws_security_group.web", "aws_instance.web", "aws_eip.web", "aws_lb_target_group_attachment.web",
				"aws_lb_target_group.web", "aws_cloudwatch_log_group.flow", "aws_iam_role.logs", "aws_iam_role_policy.logs",
				"aws_route53_record.web", "module.vpc",
			},
			providers: []string{"aws"},
			clouds:    []model.Cloud{model.CloudAWS},
			envs:      []string{},
		},
		{
			fixture: "multicloud",
			files:   []string{"main.tf", "providers.tf", "variables.tf", "versions.tf"},
			nodes: []string{
				"aws_s3_bucket.east", "aws_s3_bucket.west", "aws_s3_bucket_replication_configuration.east_to_west",
				"google_storage_bucket.archive", "oci_objectstorage_bucket.backup",
			},
			providers: []string{"aws", "aws", "google", "azurerm"},
			clouds:    []model.Cloud{model.CloudAWS, model.CloudAzure, model.CloudGCP, model.CloudOCI},
			envs:      []string{"dev"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			wf1, diags1 := mustImport(t, readFixture(t, tc.fixture))

			var addrs []string
			for i := range wf1.Nodes {
				n := &wf1.Nodes[i]
				addrs = append(addrs, Address(n))
				if n.ID != Address(n) || n.Positioned {
					t.Errorf("node %s: id %q positioned %v", Address(n), n.ID, n.Positioned)
				}
			}
			if !slices.Equal(addrs, tc.nodes) {
				t.Errorf("nodes = %v, want %v", addrs, tc.nodes)
			}
			var provs []string
			for _, p := range wf1.Providers {
				provs = append(provs, p.Name)
			}
			if !slices.Equal(provs, tc.providers) {
				t.Errorf("providers = %v, want %v", provs, tc.providers)
			}
			if !slices.Equal(wf1.Clouds, tc.clouds) {
				t.Errorf("clouds = %v, want %v", wf1.Clouds, tc.clouds)
			}
			var envs []string
			for _, e := range wf1.Environments {
				envs = append(envs, e.Name)
			}
			if !slices.Equal(envs, tc.envs) && !(len(envs) == 0 && len(tc.envs) == 0) {
				t.Errorf("environments = %v, want %v", envs, tc.envs)
			}

			files := mustExport(t, wf1, ExportOptions{})
			var names []string
			for name := range files {
				names = append(names, name)
			}
			slices.Sort(names)
			if !slices.Equal(names, tc.files) {
				t.Errorf("exported files = %v, want %v", names, tc.files)
			}

			again := mustExport(t, wf1, ExportOptions{})
			if !reflect.DeepEqual(files, again) {
				t.Error("Export is not byte-stable")
			}

			wf2, diags2 := mustImport(t, files)
			wf1.Environments, wf2.Environments = nil, nil
			wf1.ActiveEnvironment, wf2.ActiveEnvironment = "", ""
			if a, b := toJSON(t, wf1), toJSON(t, wf2); a != b {
				t.Errorf("Import(Export(wf)) differs from wf\nfirst:\n%s\nsecond:\n%s", a, b)
			}
			if !reflect.DeepEqual(diags1, diags2) {
				t.Errorf("diagnostics differ after round trip:\n%v\n%v", diags1, diags2)
			}
			if !reflect.DeepEqual(files, mustExport(t, wf2, ExportOptions{})) {
				t.Error("re-exported files differ")
			}

			checkTerraformFmt(t, files)
		})
	}
}

// checkTerraformFmt runs the host terraform binary when available.
func checkTerraformFmt(t *testing.T, files map[string][]byte) {
	t.Helper()
	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Log("terraform not on PATH; skipping fmt check")
		return
	}
	dir := t.TempDir()
	for name, content := range files {
		if !strings.HasSuffix(name, ".tf") && !strings.HasSuffix(name, ".tfvars") {
			continue
		}
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(tf, "fmt", "-check", "-recursive", "-diff", dir).CombinedOutput()
	if err != nil {
		t.Errorf("terraform fmt -check failed: %v\n%s", err, out)
	}
}

func TestExportEnvironments(t *testing.T) {
	wf, _ := mustImport(t, readFixture(t, "serverless"))
	if wf.ActiveEnvironment != "default" {
		t.Errorf("active environment = %q, want default", wf.ActiveEnvironment)
	}
	for i := range wf.Environments {
		if wf.Environments[i].Name == "prod" {
			wf.Environments[i].EnvVars = []model.EnvVar{
				{Key: "TF_LOG", Value: "INFO"},
				{Key: "API_TOKEN", Value: "s3cr3t", Secret: true},
				{Key: "GREETING", Value: "hello world"},
			}
		}
	}
	files := mustExport(t, wf, ExportOptions{Environments: true})

	wantDefault := "region    = \"us-east-1\"\nmemory_mb = 256\n"
	if got := string(files["environments/default.tfvars"]); got != wantDefault {
		t.Errorf("default.tfvars =\n%s\nwant\n%s", got, wantDefault)
	}
	wantProd := "stage     = \"prod\"\nmemory_mb = 1024\n"
	if got := string(files["environments/prod.tfvars"]); got != wantProd {
		t.Errorf("prod.tfvars =\n%s\nwant\n%s", got, wantProd)
	}
	wantEnv := "TF_LOG=INFO\nAPI_TOKEN=\nGREETING=\"hello world\"\n"
	if got := string(files["environments/prod.env.example"]); got != wantEnv {
		t.Errorf("prod.env.example =\n%s\nwant\n%s", got, wantEnv)
	}
	if _, ok := files["environments/default.env.example"]; ok {
		t.Error("env.example written for environment without env vars")
	}
	checkTerraformFmt(t, files)

	// An exported folder imports back with its environments (env vars are
	// app-only and not part of the Terraform files).
	wf2, _ := mustImport(t, files)
	want := map[string]map[string]model.Value{}
	for _, e := range wf.Environments {
		want[e.Name] = e.Values
	}
	got := map[string]map[string]model.Value{}
	for _, e := range wf2.Environments {
		got[e.Name] = e.Values
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environments after re-import = %s, want %s", toJSON(t, got), toJSON(t, want))
	}
	if wf2.ActiveEnvironment != "default" {
		t.Errorf("active environment after re-import = %q", wf2.ActiveEnvironment)
	}
}

func edgeSet(edges []model.Edge) map[string]model.EdgeKind {
	out := map[string]model.EdgeKind{}
	for _, e := range edges {
		out[e.Source+" -> "+e.Target+" @"+e.Attribute+" ."+e.SourceAttr] = e.Kind
	}
	return out
}

func TestEdgeDerivation(t *testing.T) {
	ref, dep := model.EdgeReference, model.EdgeDependsOn
	tests := []struct {
		fixture string
		exact   bool
		want    map[string]model.EdgeKind
	}{
		{
			fixture: "network",
			exact:   true,
			want: map[string]model.EdgeKind{
				// module output read directly and through a dynamic index
				"module.vpc -> aws_security_group.web @vpc_id .vpc_id":                   ref,
				"module.vpc -> aws_instance.web @count .private_subnets":                 ref,
				"module.vpc -> aws_instance.web @subnet_id .private_subnets":             ref,
				"module.vpc -> aws_lb_target_group.web @vpc_id .vpc_id":                  ref,
				"aws_security_group.web -> aws_instance.web @vpc_security_group_ids .id": ref,
				// depends_on entries
				"aws_iam_role_policy.logs -> aws_instance.web @depends_on .": dep,
				"module.vpc -> aws_instance.web @depends_on .":               dep,
				// for expression over a counted resource
				"aws_instance.web -> aws_eip.web @for_each .": ref,
				// index step skipped
				"aws_instance.web -> aws_lb_target_group_attachment.web @target_id .id":                ref,
				"aws_lb_target_group.web -> aws_lb_target_group_attachment.web @target_group_arn .arn": ref,
				"aws_iam_role.logs -> aws_iam_role_policy.logs @role .name":                            ref,
				// splat inside jsonencode
				"aws_cloudwatch_log_group.flow -> aws_iam_role_policy.logs @policy .arn": ref,
				// string key index, referenced twice in one conditional
				"aws_eip.web -> aws_route53_record.web @records .public_ip": ref,
			},
		},
		{
			fixture: "serverless",
			want: map[string]model.EdgeKind{
				"data.aws_iam_policy_document.assume -> aws_iam_role.lambda @assume_role_policy .json":        ref,
				"data.archive_file.handler -> aws_lambda_function.api @filename .output_path":                 ref,
				"aws_dynamodb_table.orders -> aws_lambda_function.api @environment[0].variables .name":        ref,
				"aws_dynamodb_table.orders -> aws_iam_role_policy.table_access @policy .arn":                  ref,
				"aws_apigatewayv2_integration.lambda -> aws_apigatewayv2_route.orders @target .id":            ref,
				"aws_apigatewayv2_api.http -> aws_lambda_permission.apigw @source_arn .execution_arn":         ref,
				"aws_lambda_function.api -> aws_apigatewayv2_integration.lambda @integration_uri .invoke_arn": ref,
			},
		},
		{
			fixture: "multicloud",
			exact:   true,
			want: map[string]model.EdgeKind{
				"aws_s3_bucket.east -> aws_s3_bucket_replication_configuration.east_to_west @bucket .id":                         ref,
				"aws_s3_bucket.west -> aws_s3_bucket_replication_configuration.east_to_west @rule[0].destination[0].bucket .arn": ref,
				"aws_s3_bucket.east -> google_storage_bucket.archive @name .bucket":                                              ref,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			wf, _ := mustImport(t, readFixture(t, tc.fixture))
			got := edgeSet(wf.Edges)
			for k, kind := range tc.want {
				if gk, ok := got[k]; !ok || gk != kind {
					t.Errorf("missing edge %s (%s)", k, kind)
				}
			}
			if tc.exact && len(got) != len(tc.want) {
				t.Errorf("got %d edges, want %d:\n%s", len(got), len(tc.want), toJSON(t, got))
			}
			ids := map[string]bool{}
			for _, e := range wf.Edges {
				if ids[e.ID] {
					t.Errorf("duplicate edge id %s", e.ID)
				}
				ids[e.ID] = true
			}
			if !slices.IsSortedFunc(wf.Edges, func(a, b model.Edge) int {
				return strings.Compare(a.Source+"\x00"+a.Target+"\x00"+a.Attribute+"\x00"+a.SourceAttr,
					b.Source+"\x00"+b.Target+"\x00"+b.Attribute+"\x00"+b.SourceAttr)
			}) {
				t.Error("edges are not sorted")
			}
		})
	}
}

func TestImportDiagnostics(t *testing.T) {
	wf, diags := mustImport(t, readFixture(t, "network"))
	var codes []string
	for _, d := range diags {
		codes = append(codes, d.Code+":"+d.Attribute)
	}
	want := []string{"unknown_reference:output.unknown.value"}
	if !slices.Equal(codes, want) {
		t.Errorf("diagnostics = %v, want %v", codes, want)
	}
	if len(wf.Other) != 1 || wf.Other[0].Type != "moved" {
		t.Errorf("other blocks = %+v", wf.Other)
	}

	files := map[string][]byte{
		"good.tf":       []byte("resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"x\"\n}\n"),
		"broken.tf":     []byte("resource \"aws_sqs_queue\" \"q\" {\n  name = \"q\"\n\n  oops = \n}\n"),
		"extra.tf.json": []byte(`{"resource": {}}`),
		"nested/x.tf":   []byte("resource \"aws_sns_topic\" \"t\" {}\n"),
	}
	wf, diags, err := Import("partial", files)
	if err != nil {
		t.Fatal(err)
	}
	var parseErr, skipped bool
	for _, d := range diags {
		if d.Code == "parse_error" && d.Severity == model.SeverityError && strings.HasPrefix(d.Detail, "broken.tf:") {
			parseErr = true
		}
		if d.Code == "unsupported_file" && d.Severity == model.SeverityWarning {
			skipped = true
		}
	}
	if !parseErr || !skipped {
		t.Errorf("diagnostics = %+v", diags)
	}
	node(t, wf, "aws_s3_bucket.b")
	for _, n := range wf.Nodes {
		if n.Type == "aws_sns_topic" {
			t.Error("nested directory file was imported")
		}
	}

	if _, _, err := Import("empty", map[string][]byte{"terraform.tfvars": []byte("a = 1\n")}); err == nil {
		t.Error("Import without .tf files succeeded")
	}
}

func TestImportPreservesExpressionSource(t *testing.T) {
	wf, _ := mustImport(t, readFixture(t, "network"))
	userData := attr(t, &node(t, wf, "aws_instance.web").Body, "user_data")
	wantUD := "<<-EOT\n    #!/bin/bash\n    echo \"hello from ${var.azs[0]}\" > /tmp/hello\n  EOT"
	if userData.Expr != wantUD || userData.Literal != nil {
		t.Errorf("user_data = %q literal %s", userData.Expr, userData.Literal)
	}
	policy := attr(t, &node(t, wf, "aws_iam_role.logs").Body, "assume_role_policy")
	if !strings.HasPrefix(policy.Expr, "<<EOF\n{") || !strings.HasSuffix(policy.Expr, "}\nEOF") {
		t.Errorf("assume_role_policy expr = %q", policy.Expr)
	}
	var doc map[string]any
	var s string
	if err := json.Unmarshal(policy.Literal, &s); err != nil || json.Unmarshal([]byte(s), &doc) != nil {
		t.Errorf("heredoc literal = %s", policy.Literal)
	}
	sg := node(t, wf, "aws_security_group.web")
	if len(sg.Body.Blocks) != 2 || sg.Body.Blocks[0].Type != "dynamic" || !slices.Equal(sg.Body.Blocks[0].Labels, []string{"ingress"}) {
		t.Fatalf("security group blocks = %+v", sg.Body.Blocks)
	}
	content := sg.Body.Blocks[0].Body.Blocks[0]
	var names []string
	for _, a := range content.Body.Attrs {
		names = append(names, a.Name)
	}
	if want := []string{"description", "from_port", "to_port", "protocol", "cidr_blocks"}; content.Type != "content" || !slices.Equal(names, want) {
		t.Errorf("dynamic content = %s %v", content.Type, names)
	}
	lifecycle := sg.Body.Blocks[1]
	if v := attr(t, &lifecycle.Body, "create_before_destroy"); string(v.Literal) != "true" {
		t.Errorf("create_before_destroy literal = %s", v.Literal)
	}
	vpc := node(t, wf, "module.vpc")
	if v := attr(t, &vpc.Body, "private_subnets"); string(v.Literal) != `["10.0.1.0/24","10.0.2.0/24"]` {
		t.Errorf("private_subnets literal = %s", v.Literal)
	}
}

func TestLiteralExprConversions(t *testing.T) {
	tests := []struct {
		name    string
		literal string
		expr    string
	}{
		{"string", `"hello"`, `"hello"`},
		{"escaped string", `"a \"q\" ${x} %{y}\n"`, `"a \"q\" $${x} %%{y}\n"`},
		{"unicode", `"héllo ✓"`, `"héllo ✓"`},
		{"integer", `42`, `42`},
		{"negative float", `-1.5`, `-1.5`},
		{"large number", `12345678901234567890`, `12345678901234567890`},
		{"true", `true`, `true`},
		{"false", `false`, `false`},
		{"null", `null`, `null`},
		{"list", `["a","b"]`, `["a", "b"]`},
		{"empty list", `[]`, `[]`},
		{"mixed list", `[1,"a",true,null]`, `[1, "a", true, null]`},
		{"map", `{"Name":"web","Env":"dev"}`, "{\n  Env  = \"dev\"\n  Name = \"web\"\n}"},
		{"empty object", `{}`, `{}`},
		{"nested", `{"a":{"b":[1,2],"c":{"d":null}},"key with space":true}`,
			"{\n  a = {\n    b = [1, 2]\n    c = {\n      d = null\n    }\n  }\n  \"key with space\" = true\n}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			expr, err := ExprFromLiteral(json.RawMessage(tc.literal))
			if err != nil {
				t.Fatalf("ExprFromLiteral: %v", err)
			}
			if expr != tc.expr {
				t.Errorf("ExprFromLiteral = %q, want %q", expr, tc.expr)
			}
			lit, ok := LiteralFromExpr(expr)
			if !ok {
				t.Fatalf("LiteralFromExpr(%q) not literal", expr)
			}
			var want, got any
			if err := json.Unmarshal([]byte(tc.literal), &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(lit, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip literal = %s, want %s", lit, tc.literal)
			}
		})
	}

	if _, err := ExprFromLiteral(json.RawMessage(`{bad`)); err == nil {
		t.Error("ExprFromLiteral accepted invalid JSON")
	}
}

func TestLiteralFromExpr(t *testing.T) {
	tests := []struct {
		expr    string
		literal string // "" = not a literal
	}{
		{`"plain"`, `"plain"`},
		{"<<EOT\nline one\nline two\nEOT", `"line one\nline two\n"`},
		{"<<-EOT\n    indented\n      more\n  EOT", `"indented\n  more\n"`},
		{`-3`, `-3`},
		{`{ a = 1, "b c" = [true, null] }`, `{"a":1,"b c":[true,null]}`},
		{`var.name`, ``},
		{`"prefix-${var.name}"`, ``},
		{`"${"constant"}"`, ``},
		{`"%{if true}yes%{endif}"`, ``},
		{"<<EOT\nhello ${var.name}\nEOT", ``},
		{`upper("a")`, ``},
		{`[for s in var.list : upper(s)]`, ``},
		{`aws_s3_bucket.b.arn`, ``},
		{`{ a = local.x }`, ``},
		{`1 +`, ``},
	}
	for _, tc := range tests {
		lit, ok := LiteralFromExpr(tc.expr)
		if tc.literal == "" {
			if ok {
				t.Errorf("LiteralFromExpr(%q) = %s, want non-literal", tc.expr, lit)
			}
			continue
		}
		if !ok || string(lit) != tc.literal {
			t.Errorf("LiteralFromExpr(%q) = %s, %v; want %s", tc.expr, lit, ok, tc.literal)
		}
	}
}

func TestValidateExpr(t *testing.T) {
	for _, ok := range []string{`1`, `aws_s3_bucket.b.arn`, "<<EOT\nx\nEOT", `{ a = [1, 2] }`, `x ? 1 : 2`} {
		if err := ValidateExpr(ok); err != nil {
			t.Errorf("ValidateExpr(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{``, `1 2`, `{ a = }`, `"unterminated`, "a\nb = 2"} {
		if err := ValidateExpr(bad); err == nil {
			t.Errorf("ValidateExpr(%q) accepted invalid expression", bad)
		}
	}
}

func TestNormalize(t *testing.T) {
	wf := &model.Workflow{
		Variables: []model.NamedBlock{{Name: "env", Body: model.Body{Attrs: []model.Attr{{Name: "default", Value: model.Value{Literal: json.RawMessage(`"dev"`)}}}}}},
		Nodes: []model.Node{
			{Kind: model.KindResource, Type: "aws_sqs_queue", Name: "jobs", Body: model.Body{Attrs: []model.Attr{
				{Name: "name", Value: model.Value{Expr: `"jobs-${var.env}"`, Literal: json.RawMessage(`"stale"`)}},
				{Name: "tags", Value: model.Value{Literal: json.RawMessage(`{"Team":"core"}`)}},
			}}},
			{ID: "aws_sqs_queue.jobs", Kind: model.KindResource, Type: "aws_sns_topic", Name: "events", Body: model.Body{Attrs: []model.Attr{
				{Name: "name", Value: model.Value{Expr: `var.missing`}},
				{Name: "kms_master_key_id", Value: model.Value{Expr: `aws_kms_key.absent.id`}},
				{Name: "policy", Value: model.Value{Expr: `jsonencode({ a = `}},
			}}},
			{Kind: model.KindResource, Type: "aws_sqs_queue", Name: "jobs"},
			{Kind: model.KindResource, Type: "aws_s3_bucket", Name: "1bad"},
			{Kind: model.KindResource, Type: "aws_lambda_event_source_mapping", Name: "m", Body: model.Body{Attrs: []model.Attr{
				{Name: "event_source_arn", Value: model.Value{Expr: `aws_sqs_queue.jobs.arn`}},
			}}},
		},
		Environments: []model.Environment{{Name: "prod", Values: map[string]model.Value{
			"env": {Literal: json.RawMessage(`"prod"`)},
		}}},
	}
	diags := Normalize(wf)

	var ids []string
	for _, n := range wf.Nodes {
		ids = append(ids, n.ID)
	}
	wantIDs := []string{"aws_sqs_queue.jobs-2", "aws_sqs_queue.jobs", "aws_sqs_queue.jobs-3", "aws_s3_bucket.1bad", "aws_lambda_event_source_mapping.m"}
	if !slices.Equal(ids, wantIDs) {
		t.Errorf("ids = %v, want %v", ids, wantIDs)
	}

	q := &wf.Nodes[0]
	if v := attr(t, &q.Body, "name"); v.Literal != nil {
		t.Errorf("stale literal kept for template: %s", v.Literal)
	}
	if v := attr(t, &q.Body, "tags"); v.Expr != "{\n  Team = \"core\"\n}" || string(v.Literal) != `{"Team":"core"}` {
		t.Errorf("tags = %+v", v)
	}
	if v := wf.Variables[0].Body.Attrs[0].Value; v.Expr != `"dev"` {
		t.Errorf("variable default expr = %q", v.Expr)
	}
	if v := wf.Environments[0].Values["env"]; v.Expr != `"prod"` || string(v.Literal) != `"prod"` {
		t.Errorf("environment value = %+v", v)
	}

	type key struct{ code, node, attr string }
	got := map[key]model.Severity{}
	for _, d := range diags {
		got[key{d.Code, d.NodeID, d.Attribute}] = d.Severity
	}
	want := map[key]model.Severity{
		{"unknown_variable", "aws_sqs_queue.jobs", "name"}:               model.SeverityWarning,
		{"unknown_reference", "aws_sqs_queue.jobs", "kms_master_key_id"}: model.SeverityWarning,
		{"invalid_expression", "aws_sqs_queue.jobs", "policy"}:           model.SeverityError,
		{"duplicate_address", "aws_sqs_queue.jobs-3", ""}:                model.SeverityError,
		{"invalid_name", "aws_s3_bucket.1bad", ""}:                       model.SeverityError,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("diagnostics = %s", toJSON(t, diags))
	}

	// The first node owning the address is the reference target.
	if len(wf.Edges) != 1 || wf.Edges[0].Source != "aws_sqs_queue.jobs-2" || wf.Edges[0].Target != "aws_lambda_event_source_mapping.m" ||
		wf.Edges[0].Attribute != "event_source_arn" || wf.Edges[0].SourceAttr != "arn" {
		t.Errorf("edges = %s", toJSON(t, wf.Edges))
	}
	if !slices.Equal(wf.Clouds, []model.Cloud{model.CloudAWS}) {
		t.Errorf("clouds = %v", wf.Clouds)
	}

	// Normalize is idempotent.
	before := toJSON(t, wf)
	Normalize(wf)
	if after := toJSON(t, wf); after != before {
		t.Errorf("second Normalize changed the workflow:\n%s\n%s", before, after)
	}
}

func TestMergeImported(t *testing.T) {
	prev, _ := mustImport(t, readFixture(t, "serverless"))
	prev.ID = "orders-abc123"
	prev.Name = "Orders"
	prev.Description = "Orders API"
	prev.CreatedAt = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	prev.ActiveEnvironment = "prod"
	for i := range prev.Nodes {
		n := &prev.Nodes[i]
		n.ID = "n" + string(rune('a'+i))
		n.Position = model.Position{X: float64(i * 100), Y: 50}
		n.Positioned = true
	}
	table := node(t, prev, "aws_dynamodb_table.orders")
	table.Label = "Orders table"
	fn := node(t, prev, "aws_lambda_function.api")
	fn.Parent = table.ID
	Normalize(prev)

	// Code edit: rename the stage and add a queue whose fresh ID collides
	// with nothing, plus one whose address equals a carried ID.
	files := mustExport(t, prev, ExportOptions{})
	main := string(files["main.tf"])
	main = strings.Replace(main, `resource "aws_apigatewayv2_stage" "default"`, `resource "aws_apigatewayv2_stage" "live"`, 1)
	main += "\nresource \"aws_sqs_queue\" \"dlq\" {}\n"
	files["main.tf"] = []byte(main)
	next, _ := mustImport(t, files)
	next.Nodes = append(next.Nodes, model.Node{ID: table.ID, Kind: model.KindResource, Type: "na", Name: "x"})
	MergeImported(prev, next)

	if next.ID != prev.ID || next.Name != prev.Name || next.Description != prev.Description || !next.CreatedAt.Equal(prev.CreatedAt) {
		t.Errorf("workflow metadata not carried: %+v", next)
	}
	if next.ActiveEnvironment != "prod" || !reflect.DeepEqual(next.Environments, prev.Environments) {
		t.Errorf("environments not carried")
	}
	for i := range prev.Nodes {
		p := &prev.Nodes[i]
		if p.Type == "aws_apigatewayv2_stage" {
			continue
		}
		n := node(t, next, Address(p))
		if n.ID != p.ID || n.Position != p.Position || !n.Positioned || n.Label != p.Label || n.Parent != p.Parent {
			t.Errorf("node %s not merged: %+v vs %+v", Address(p), n, p)
		}
	}
	live := node(t, next, "aws_apigatewayv2_stage.live")
	if live.Positioned || live.ID != "aws_apigatewayv2_stage.live" {
		t.Errorf("renamed node = %+v", live)
	}
	if extra := node(t, next, "na.x"); extra.ID == table.ID {
		t.Errorf("colliding fresh node kept carried ID %s", extra.ID)
	}
	ids := map[string]bool{}
	for _, n := range next.Nodes {
		if ids[n.ID] {
			t.Errorf("duplicate id %s", n.ID)
		}
		ids[n.ID] = true
	}
	for _, e := range next.Edges {
		if !ids[e.Source] || !ids[e.Target] {
			t.Errorf("edge %s references unknown node", e.ID)
		}
	}
	if got := edgeSet(next.Edges); got[table.ID+" -> "+fn.ID+" @environment[0].variables .name"] != model.EdgeReference {
		t.Errorf("edges not re-derived with carried ids: %s", toJSON(t, next.Edges))
	}
}

func TestRenameNode(t *testing.T) {
	wf, _ := mustImport(t, readFixture(t, "network"))
	edgesBefore := append([]model.Edge(nil), wf.Edges...)
	inst := node(t, wf, "aws_instance.web")
	if err := RenameNode(wf, inst.ID, "server"); err != nil {
		t.Fatal(err)
	}
	if err := RenameNode(wf, "module.vpc", "network"); err != nil {
		t.Fatal(err)
	}
	if err := RenameNode(wf, node(t, wf, "aws_iam_role_policy.logs").ID, "flow_logs"); err != nil {
		t.Fatal(err)
	}

	checks := []struct{ addr, attr, want string }{
		{"aws_eip.web", "for_each", "{ for idx, inst in aws_instance.server : idx => inst }"},
		{"aws_lb_target_group_attachment.web", "target_id", "aws_instance.server[count.index].id"},
		{"aws_instance.server", "subnet_id", "module.network.private_subnets[count.index]"},
		{"aws_instance.server", "depends_on", "[aws_iam_role_policy.flow_logs, module.network]"},
		{"aws_route53_record.web", "records", `aws_eip.web["0"].public_ip == null ? [] : [aws_eip.web["0"].public_ip]`},
	}
	for _, c := range checks {
		if got := attr(t, &node(t, wf, c.addr).Body, c.attr).Expr; got != c.want {
			t.Errorf("%s.%s = %q, want %q", c.addr, c.attr, got, c.want)
		}
	}
	if got := wf.Outputs[0].Body.Attrs[0].Value.Expr; got != "aws_instance.server[*].id" {
		t.Errorf("output value = %q", got)
	}
	if got := wf.Outputs[0].Body.Attrs[1].Value.Expr; got != `"IDs of ${length(aws_instance.server)} instances"` {
		t.Errorf("output description = %q", got)
	}
	moved := wf.Other[0].Body
	if from, to := attr(t, &moved, "from").Expr, attr(t, &moved, "to").Expr; from != "aws_instance.app" || to != "aws_instance.server" {
		t.Errorf("moved block = from %q to %q", from, to)
	}

	diags := Normalize(wf)
	for _, d := range diags {
		if d.Code == "unknown_reference" && d.Attribute != "output.unknown.value" {
			t.Errorf("dangling reference after rename: %+v", d)
		}
	}
	if !reflect.DeepEqual(wf.Edges, edgesBefore) {
		t.Errorf("edges changed after rename:\n%s\n%s", toJSON(t, edgesBefore), toJSON(t, wf.Edges))
	}

	if err := RenameNode(wf, inst.ID, "1x"); err == nil {
		t.Error("invalid name accepted")
	}
	if err := RenameNode(wf, inst.ID, "server"); err != nil {
		t.Errorf("renaming to the current name: %v", err)
	}
	wf.Nodes = append(wf.Nodes, model.Node{ID: "spare", Kind: model.KindResource, Type: "aws_instance", Name: "spare"})
	if err := RenameNode(wf, "spare", "server"); err == nil {
		t.Error("rename to a taken address accepted")
	}
	if err := RenameNode(wf, "nope", "x"); err == nil {
		t.Error("unknown node accepted")
	}
}

func TestExportClientValues(t *testing.T) {
	wf := &model.Workflow{
		Terraform: &model.Body{},
		Nodes: []model.Node{{
			Kind: model.KindResource, Type: "aws_s3_bucket", Name: "site",
			Body: model.Body{
				Attrs: []model.Attr{
					{Name: "bucket", Value: model.Value{Literal: json.RawMessage(`"site-${env}"`)}},
					{Name: "tags", Value: model.Value{Literal: json.RawMessage(`{"Team":"web","Cost Center":"42"}`)}},
				},
				Blocks: []model.Block{
					{Type: "lifecycle", Body: model.Body{Attrs: []model.Attr{{Name: "prevent_destroy", Value: model.Value{Literal: json.RawMessage(`true`)}}}}},
					{Type: "timeouts"},
				},
			},
		}},
		Other: []model.Block{{Type: "check", Labels: []string{`say "hi"`}, Body: model.Body{Blocks: []model.Block{{
			Type: "assert", Body: model.Body{Attrs: []model.Attr{
				{Name: "condition", Value: model.Value{Expr: "aws_s3_bucket.site.bucket != \"\""}},
				{Name: "error_message", Value: model.Value{Expr: "<<EOT\nBucket must be named.\nEOT"}},
			}},
		}}}}},
	}
	files := mustExport(t, wf, ExportOptions{})
	want := `resource "aws_s3_bucket" "site" {
  bucket = "site-$${env}"
  tags = {
    "Cost Center" = "42"
    Team          = "web"
  }

  lifecycle {
    prevent_destroy = true
  }

  timeouts {}
}

check "say \"hi\"" {
  assert {
    condition     = aws_s3_bucket.site.bucket != ""
    error_message = <<EOT
Bucket must be named.
EOT
  }
}
`
	if got := string(files["main.tf"]); got != want {
		t.Errorf("main.tf =\n%s\nwant\n%s", got, want)
	}
	if got := string(files["versions.tf"]); got != "terraform {}\n" {
		t.Errorf("versions.tf = %q", got)
	}
	checkTerraformFmt(t, files)

	back, _ := mustImport(t, files)
	if got := attr(t, &back.Nodes[0].Body, "bucket").Literal; string(got) != `"site-${env}"` {
		t.Errorf("bucket literal after round trip = %s", got)
	}

	wf.Nodes[0].Body.Attrs[0].Value = model.Value{Expr: `"a" b = 1`}
	if _, err := Export(wf, ExportOptions{}); err == nil || !strings.Contains(err.Error(), "aws_s3_bucket.site") {
		t.Errorf("Export with invalid expression: err = %v", err)
	}
	wf.Nodes[0].Body.Attrs[0].Value = model.Value{}
	if _, err := Export(wf, ExportOptions{}); err == nil {
		t.Error("Export with empty value succeeded")
	}
}

func TestEnvVarsOnlyEnvironmentRoundTrip(t *testing.T) {
	wf, _ := mustImport(t, readFixture(t, "network"))
	wf.Environments = []model.Environment{{
		Name:   "ci",
		Values: map[string]model.Value{},
		EnvVars: []model.EnvVar{
			{Key: "TF_LOG", Value: "DEBUG"},
			{Key: "AWS_SECRET_ACCESS_KEY", Value: "hunter2", Secret: true},
			{Key: "BANNER", Value: `say "hi" #1`},
		},
	}}
	files := mustExport(t, wf, ExportOptions{Environments: true})
	if _, ok := files["environments/ci.tfvars"]; ok {
		t.Error("tfvars written for environment without values")
	}

	back, _ := mustImport(t, files)
	want := []model.Environment{{
		Name:   "ci",
		Values: map[string]model.Value{},
		EnvVars: []model.EnvVar{
			{Key: "TF_LOG", Value: "DEBUG"},
			{Key: "AWS_SECRET_ACCESS_KEY", Secret: true},
			{Key: "BANNER", Value: `say "hi" #1`},
		},
	}}
	if !reflect.DeepEqual(back.Environments, want) {
		t.Errorf("environments = %s, want %s", toJSON(t, back.Environments), toJSON(t, want))
	}

	// Env vars join the environment of the same name created from tfvars.
	files["environments/ci.tfvars"] = []byte("enable_logs = true\n")
	back, _ = mustImport(t, files)
	if len(back.Environments) != 1 || string(back.Environments[0].Values["enable_logs"].Literal) != "true" || len(back.Environments[0].EnvVars) != 3 {
		t.Errorf("merged environment = %s", toJSON(t, back.Environments))
	}

	_, diags, err := Import("bad", map[string][]byte{"main.tf": files["main.tf"], "environments/x.env.example": []byte("# comment\nNOVALUE\nOK=1\n")})
	if err != nil {
		t.Fatal(err)
	}
	var invalid []string
	for _, d := range diags {
		if d.Code == "invalid_env_line" {
			invalid = append(invalid, d.Detail)
		}
	}
	if !slices.Equal(invalid, []string{"x.env.example:2: expected KEY=VALUE"}) {
		t.Errorf("env line diagnostics = %v", invalid)
	}
}
