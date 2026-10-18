package endpoints

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const base = "http://127.0.0.1:45000"

func res(addr string, values map[string]any) model.DeployedResource {
	typ, name, _ := strings.Cut(addr, ".")
	return model.DeployedResource{Address: addr, Mode: "managed", Type: typ, Name: name, NodeID: addr, Values: values}
}

func fixture() (*model.Workflow, *model.DeployedState, Sandbox) {
	wf := &model.Workflow{ID: "wf1", Name: "Shop", Nodes: []model.Node{
		{ID: "data.archive_file.code", Kind: model.KindData, Type: "archive_file", Name: "code", Body: model.Body{Blocks: []model.Block{{
			Type: "source", Body: model.Body{Attrs: []model.Attr{{Name: "content", Value: model.Value{Expr: "<<-EOT\nif (path === \"/orders\") {}\nif (path.startsWith(\"/orders/\")) {}\nconst tmp = \"/tmp/x\"\nEOT"}}}},
		}}}},
		{ID: "aws_sqs_queue.jobs", Kind: model.KindResource, Type: "aws_sqs_queue", Name: "jobs", Label: "Jobs queue"},
	}}
	st := &model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{
		res("aws_apigatewayv2_api.http", map[string]any{"id": "abc123", "protocol_type": "HTTP", "api_endpoint": "https://abc123.execute-api.us-east-1.amazonaws.com", "arn": "arn:aws:apigateway:us-east-1::/apis/abc123"}),
		res("aws_apigatewayv2_stage.default", map[string]any{"api_id": "abc123", "name": "$default"}),
		res("aws_apigatewayv2_route.proxy", map[string]any{"api_id": "abc123", "route_key": "ANY /{proxy+}"}),
		res("aws_apigatewayv2_route.get", map[string]any{"api_id": "abc123", "route_key": "GET /items/{id}"}),
		res("aws_apigatewayv2_integration.fn", map[string]any{"api_id": "abc123", "integration_uri": "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/arn:aws:lambda:us-east-1:000000000000:function:shop-api/invocations"}),
		res("aws_api_gateway_rest_api.rest", map[string]any{"id": "rest1", "root_resource_id": "r0", "name": "pets"}),
		res("aws_api_gateway_resource.pets", map[string]any{"id": "r1", "rest_api_id": "rest1", "path": "/pets"}),
		res("aws_api_gateway_method.get", map[string]any{"rest_api_id": "rest1", "resource_id": "r1", "http_method": "GET"}),
		res("aws_api_gateway_stage.prod", map[string]any{"rest_api_id": "rest1", "stage_name": "prod"}),
		res("aws_lambda_function.api", map[string]any{"function_name": "shop-api", "arn": "arn:aws:lambda:us-east-1:000000000000:function:shop-api", "runtime": "nodejs22.x", "handler": "index.handler"}),
		res("aws_lambda_function_url.api", map[string]any{"function_name": "shop-api", "url_id": "u1x2", "function_url": "https://u1x2.lambda-url.us-east-1.on.aws/"}),
		res("aws_sqs_queue.jobs", map[string]any{"name": "jobs", "arn": "arn:aws:sqs:us-east-1:000000000000:jobs", "url": "http://sqs.us-east-1.localhost.floci.io:4566/000000000000/jobs"}),
		res("aws_sqs_queue.events", map[string]any{"name": "events.fifo", "fifo_queue": true, "arn": "arn:aws:sqs:us-east-1:000000000000:events.fifo"}),
		res("aws_s3_bucket.assets", map[string]any{"bucket": "shop-assets", "id": "shop-assets", "arn": "arn:aws:s3:::shop-assets"}),
		res("aws_dynamodb_table.orders", map[string]any{"name": "orders", "arn": "arn:aws:dynamodb:us-east-1:000000000000:table/orders", "hash_key": "id", "range_key": "ts",
			"attribute": []any{map[string]any{"name": "id", "type": "S"}, map[string]any{"name": "ts", "type": "N"}}}),
		res("aws_iam_role.lambda", map[string]any{"name": "shop-lambda", "arn": "arn:aws:iam::000000000000:role/shop-lambda", "id": "shop-lambda"}),
		res("aws_db_instance.db", map[string]any{"identifier": "shop-db", "address": "localhost", "port": float64(7001), "endpoint": "localhost:7001", "engine": "postgres"}),
		{Address: "data.aws_region.current", Mode: "data", Type: "aws_region", Name: "current", Values: map[string]any{"name": "us-east-1"}},
	}}
	sb := Sandbox{Bases: map[model.Cloud]string{model.CloudAWS: base}, Running: map[model.Cloud]bool{model.CloudAWS: true}, Region: "eu-west-1"}
	return wf, st, sb
}

func byAddr(t *testing.T, info model.EndpointsInfo, addr string) model.ResourceAccess {
	t.Helper()
	for _, r := range info.Resources {
		if r.Address == addr {
			return r
		}
	}
	t.Fatalf("no resource %s in %d resources", addr, len(info.Resources))
	return model.ResourceAccess{}
}

func field(r model.ResourceAccess, label string) string {
	for _, f := range r.Fields {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

func snippet(t *testing.T, r model.ResourceAccess, label string) string {
	t.Helper()
	for _, s := range r.Snippets {
		if s.Label == label {
			return s.Code
		}
	}
	t.Fatalf("%s has no %s snippet", r.Address, label)
	return ""
}

func hasAction(r model.ResourceAccess, id string) *model.TryAction {
	for i := range r.Actions {
		if r.Actions[i].ID == id {
			return &r.Actions[i]
		}
	}
	return nil
}

func TestBuildSandboxAccess(t *testing.T) {
	info := Build(fixture())
	if len(info.Sandbox) != 1 || info.Sandbox[0].Cloud != model.CloudAWS || info.Sandbox[0].URL != base {
		t.Fatalf("sandbox access: %+v", info.Sandbox)
	}
	// Resources were created in us-east-1; that wins over the settings region.
	if info.Sandbox[0].Region != "us-east-1" {
		t.Fatalf("region %q", info.Sandbox[0].Region)
	}
	env := info.Sandbox[0].EnvSnippet
	for _, want := range []string{"AWS_ENDPOINT_URL=" + base, "AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_REGION=us-east-1"} {
		if !strings.Contains(env, want) {
			t.Errorf("env snippet %q lacks %q", env, want)
		}
	}

	_, st, sb := fixture()
	sb.Running = map[model.Cloud]bool{}
	if got := Build(nil, st, sb); len(got.Sandbox) != 0 || len(got.Resources) == 0 {
		t.Fatalf("stopped sandbox: %d access entries, %d resources", len(got.Sandbox), len(got.Resources))
	}
	if got := Build(nil, &model.DeployedState{WorkflowID: "wf1"}, sb); got.Resources == nil || len(got.Resources) != 0 {
		t.Fatalf("empty state must give an empty resource list: %+v", got.Resources)
	}
}

func TestBuildHTTPAPIv2(t *testing.T) {
	info := Build(fixture())
	api := byAddr(t, info, "aws_apigatewayv2_api.http")
	if api.Kind != KindHTTPAPI {
		t.Fatalf("kind %q", api.Kind)
	}
	stage := base + "/execute-api/abc123/$default"
	if got := field(api, "Invoke URL"); got != stage {
		t.Fatalf("invoke URL %q", got)
	}
	if len(api.Endpoints) != 2 {
		t.Fatalf("endpoints: %+v", api.Endpoints)
	}
	proxy, get := api.Endpoints[0], api.Endpoints[1]
	// The code compares paths with "/orders", so that is the sample.
	if proxy.Method != "ANY" || proxy.Path != "/{proxy+}" || !proxy.Proxy || proxy.URL != stage+"/orders" {
		t.Fatalf("proxy endpoint: %+v", proxy)
	}
	if get.Method != "GET" || get.Proxy || get.URL != stage+"/items/1" {
		t.Fatalf("param endpoint: %+v", get)
	}
	curl := snippet(t, api, "curl")
	if !strings.Contains(curl, "curl -i '"+stage+"/orders'") || !strings.Contains(curl, "-X POST '"+stage+"/orders'") {
		t.Fatalf("curl snippet must single-quote the $default URL:\n%s", curl)
	}
	if !strings.Contains(snippet(t, api, "JavaScript"), `await fetch("`+stage+`/orders")`) {
		t.Fatal("fetch snippet lacks the route URL")
	}
	if !strings.Contains(snippet(t, api, "Python"), "urllib.request.urlopen") {
		t.Fatal("python snippet lacks urllib")
	}
	if a := hasAction(api, ActRequest); a == nil || a.Input != "http" {
		t.Fatalf("request action: %+v", api.Actions)
	}
	// Stages, routes and integrations are folded into the API.
	for _, r := range info.Resources {
		if strings.HasPrefix(r.Type, "aws_apigatewayv2_") && r.Type != "aws_apigatewayv2_api" {
			t.Errorf("%s listed separately", r.Address)
		}
	}
	fn := byAddr(t, info, "aws_lambda_function.api")
	if !strings.Contains(fn.Summary, "HTTP API") {
		t.Errorf("function summary does not mention its API: %q", fn.Summary)
	}
}

func TestBuildRESTAPI(t *testing.T) {
	api := byAddr(t, Build(fixture()), "aws_api_gateway_rest_api.rest")
	want := base + "/restapis/rest1/prod/_user_request_/pets"
	if len(api.Endpoints) != 1 || api.Endpoints[0].Method != "GET" || api.Endpoints[0].URL != want {
		t.Fatalf("rest endpoints: %+v", api.Endpoints)
	}
	if api.Label != "pets" {
		t.Errorf("label %q", api.Label)
	}
}

func TestBuildFunctionAndURL(t *testing.T) {
	info := Build(fixture())
	fn := byAddr(t, info, "aws_lambda_function.api")
	if field(fn, "Function name") != "shop-api" || field(fn, "ARN") == "" {
		t.Fatalf("function fields: %+v", fn.Fields)
	}
	cli := snippet(t, fn, "AWS CLI")
	for _, want := range []string{"aws --endpoint-url " + base + " lambda invoke --function-name shop-api", "--cli-binary-format raw-in-base64-out", "--payload '{}' response.json"} {
		if !strings.Contains(cli, want) {
			t.Errorf("lambda CLI lacks %q:\n%s", want, cli)
		}
	}
	js := snippet(t, fn, "JavaScript")
	if !strings.Contains(js, `from "@aws-sdk/client-lambda"`) || !strings.Contains(js, `endpoint: "`+base+`"`) {
		t.Errorf("lambda JS snippet:\n%s", js)
	}
	py := snippet(t, fn, "Python")
	if !strings.Contains(py, `boto3.client(`) || !strings.Contains(py, `endpoint_url="`+base+`"`) {
		t.Errorf("lambda Python snippet:\n%s", py)
	}
	if a := hasAction(fn, ActInvoke); a == nil || a.Input != "json" || a.Default != "{}" {
		t.Fatalf("invoke action: %+v", fn.Actions)
	}
	fu := byAddr(t, info, "aws_lambda_function_url.api")
	if fu.Kind != KindFunctionURL || field(fu, "URL") != base+"/lambda-url/u1x2/" {
		t.Fatalf("function URL: %+v", fu)
	}
}

func TestBuildQueue(t *testing.T) {
	info := Build(fixture())
	q := byAddr(t, info, "aws_sqs_queue.jobs")
	qurl := base + "/000000000000/jobs"
	if q.Kind != KindQueue || q.Label != "Jobs queue" || field(q, "Queue URL") != qurl {
		t.Fatalf("queue: label %q fields %+v", q.Label, q.Fields)
	}
	cli := snippet(t, q, "AWS CLI")
	if !strings.Contains(cli, "sqs send-message --queue-url "+qurl) || !strings.Contains(cli, "--visibility-timeout 0") {
		t.Errorf("queue CLI:\n%s", cli)
	}
	if send := hasAction(q, ActSend); send == nil || !send.Writes || send.Input != "text" {
		t.Errorf("send action: %+v", q.Actions)
	}
	if recv := hasAction(q, ActReceive); recv == nil || recv.Writes || recv.Input != "none" {
		t.Errorf("receive action: %+v", q.Actions)
	}
	fifo := byAddr(t, info, "aws_sqs_queue.events")
	if field(fifo, "Queue URL") != base+"/000000000000/events.fifo" {
		t.Errorf("queue URL from ARN: %+v", fifo.Fields)
	}
	if !strings.Contains(snippet(t, fifo, "AWS CLI"), "--message-group-id") {
		t.Error("FIFO send lacks a message group")
	}
}

func TestBuildBucketAndTable(t *testing.T) {
	info := Build(fixture())
	b := byAddr(t, info, "aws_s3_bucket.assets")
	if field(b, "Bucket name") != "shop-assets" || field(b, "URL (path style)") != base+"/shop-assets" {
		t.Fatalf("bucket fields: %+v", b.Fields)
	}
	if !strings.Contains(snippet(t, b, "JavaScript"), "forcePathStyle: true") {
		t.Error("S3 JS snippet must force path style")
	}
	if !strings.Contains(snippet(t, b, "Python"), `addressing_style": "path"`) {
		t.Error("S3 Python snippet must use path addressing")
	}
	if put := hasAction(b, ActPut); put == nil || put.Default != s3PutDefault || !put.Writes {
		t.Errorf("bucket put: %+v", b.Actions)
	}

	tb := byAddr(t, info, "aws_dynamodb_table.orders")
	if got := field(tb, "Key"); got != "id (string, partition key), ts (number, sort key)" {
		t.Fatalf("key field %q", got)
	}
	cli := snippet(t, tb, "AWS CLI")
	if !strings.Contains(cli, `"id":{"S":"example-1"}`) || !strings.Contains(cli, `"ts":{"N":"2"}`) {
		t.Errorf("typed example item missing:\n%s", cli)
	}
	put := hasAction(tb, ActPut)
	var item map[string]any
	if put == nil || json.Unmarshal([]byte(put.Default), &item) != nil || item["id"] != "example-1" || item["ts"] != float64(2) {
		t.Fatalf("plain put default: %+v", put)
	}
	if hasAction(tb, ActScan) == nil {
		t.Error("table has no scan action")
	}
}

func TestBuildDatabaseAndGeneric(t *testing.T) {
	info := Build(fixture())
	db := byAddr(t, info, "aws_db_instance.db")
	if db.Kind != KindDatabase || db.Warning == "" || field(db, "Port") != "7001" || len(db.Actions) != 0 {
		t.Fatalf("database: %+v", db)
	}
	role := byAddr(t, info, "aws_iam_role.lambda")
	if role.Kind != KindGeneric || field(role, "ARN") == "" || len(role.Actions) != 0 || !role.Supporting {
		t.Fatalf("generic: %+v", role)
	}
	for _, r := range info.Resources {
		if r.Kind != KindGeneric && r.Supporting {
			t.Errorf("%s (%s) must be primary", r.Address, r.Kind)
		}
	}
	for _, r := range info.Resources {
		if r.Address == "data.aws_region.current" {
			t.Error("data sources must not be listed")
		}
	}
}

func TestBuildWithoutAddress(t *testing.T) {
	_, st, _ := fixture()
	info := Build(nil, st, Sandbox{})
	q := byAddr(t, info, "aws_sqs_queue.jobs")
	if q.Kind != KindGeneric || q.Warning == "" || len(q.Actions) != 0 || q.Supporting {
		t.Fatalf("resource without a known sandbox address: %+v", q)
	}
	// Without the API builder, the API's wiring is listed but as plumbing.
	if api, stage := byAddr(t, info, "aws_apigatewayv2_api.http"), byAddr(t, info, "aws_apigatewayv2_stage.default"); api.Supporting || !stage.Supporting {
		t.Fatalf("api supporting=%v, stage supporting=%v", api.Supporting, stage.Supporting)
	}
}

func TestSupporting(t *testing.T) {
	cases := []struct {
		typ    string
		values map[string]any
		want   bool
	}{
		{"aws_iam_role", map[string]any{"arn": "arn:aws:iam::0:role/x"}, true},
		{"aws_iam_role_policy_attachment", nil, true},
		{"aws_iam_instance_profile", nil, true},
		{"aws_lambda_permission", nil, true},
		{"aws_lambda_event_source_mapping", map[string]any{"uuid": "u"}, true},
		{"aws_apigatewayv2_integration", nil, true},
		{"aws_apigatewayv2_stage", map[string]any{"invoke_url": "https://x"}, true},
		{"aws_api_gateway_deployment", map[string]any{"invoke_url": "https://x"}, true},
		{"aws_sqs_queue_policy", nil, true},
		{"aws_sqs_queue_redrive_policy", nil, true},
		{"aws_s3_bucket_policy", nil, true},
		{"aws_s3_bucket_notification", nil, true},
		{"aws_s3_bucket_public_access_block", nil, true},
		{"aws_s3_bucket_ownership_controls", nil, true},
		{"aws_sns_topic_subscription", nil, true},
		{"aws_cloudwatch_log_group", map[string]any{"name": "/aws/lambda/x"}, true},
		{"google_pubsub_subscription", nil, true},
		{"google_project_iam_member", nil, true},
		{"azurerm_role_assignment", nil, true},
		{"azurerm_resource_group", map[string]any{"name": "rg"}, true},
		{"aws_vpc", map[string]any{"cidr_block": "10.0.0.0/16"}, true},
		// Primary: things people call, even when listed generically.
		{"aws_apigatewayv2_api", nil, false},
		{"aws_api_gateway_rest_api", nil, false},
		{"aws_lambda_function", nil, false},
		{"aws_sqs_queue", nil, false},
		{"oci_objectstorage_bucket", map[string]any{"name": "b"}, false},
		{"azurerm_storage_container", nil, false},
		{"azurerm_storage_account", map[string]any{"primary_blob_endpoint": "https://a.blob.core.windows.net/"}, false},
		{"aws_ecr_repository", map[string]any{"repository_url": "0.dkr.ecr/x"}, false},
	}
	for _, c := range cases {
		r := &resource{Type: c.typ, Values: c.values}
		if got := supporting(r, KindGeneric); got != c.want {
			t.Errorf("supporting(%s) = %v, want %v", c.typ, got, c.want)
		}
	}
	if supporting(&resource{Type: "aws_sqs_queue_policy"}, KindQueue) {
		t.Error("resources with a dedicated kind are never supporting")
	}
}

func TestSampleSegment(t *testing.T) {
	if got := sampleSegment(nil, nil); got != "hello" {
		t.Fatalf("default sample %q", got)
	}
	if got := sampleSegment(nil, []string{`app.get("/users", h); app.post("/users", h); open("/tmp/x")`}); got != "users" {
		t.Fatalf("sample from sources %q", got)
	}
}

func TestShellQuoting(t *testing.T) {
	cases := map[string]string{
		"plain-name_1.fifo":             "plain-name_1.fifo",
		"http://127.0.0.1:1/$default/x": "'http://127.0.0.1:1/$default/x'",
		`{"a":"it's"}`:                  `'{"a":"it'"'"'s"}'`,
		"":                              "''",
	}
	for in, want := range cases {
		if got := shq(in); got != want {
			t.Errorf("shq(%q) = %s, want %s", in, got, want)
		}
	}
}
