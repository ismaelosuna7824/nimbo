package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func mustLoad(t testing.TB) *Catalog {
	t.Helper()
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func BenchmarkLoad(b *testing.B) {
	for b.Loop() {
		if _, err := Load(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoadAndFirstSchema(b *testing.B) {
	for b.Loop() {
		c, _ := Load()
		if _, ok := c.Schema(model.KindResource, "aws_s3_bucket"); !ok {
			b.Fatal("no schema")
		}
	}
}

func types(rs []model.ResourceSummary) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Type
	}
	return out
}

func TestResourceFile(t *testing.T) {
	c := mustLoad(t)
	for typ, want := range map[string]string{
		"aws_vpc":                     "network.tf",
		"aws_subnet":                  "network.tf",
		"aws_lambda_function":         "compute.tf",
		"aws_ecs_service":             "containers.tf",
		"aws_s3_bucket":               "storage.tf",
		"aws_dynamodb_table":          "database.tf",
		"aws_sqs_queue":               "messaging.tf",
		"aws_apigatewayv2_api":        "api.tf",
		"aws_api_gateway_rest_api":    "api.tf",
		"aws_iam_role":                "iam.tf",
		"aws_kms_key":                 "security.tf",
		"aws_cloudwatch_log_group":    "monitoring.tf",
		"google_pubsub_topic":         "messaging.tf",
		"google_storage_bucket":       "storage.tf",
		"azurerm_storage_account":     "storage.tf",
		"azurerm_api_management":      "api.tf",
		"oci_objectstorage_bucket":    "storage.tf",
		"random_id":                   "",
		"terraform_data":              "",
		"aws_not_a_real_thing_at_all": "",
	} {
		if got := c.ResourceFile(typ); got != want {
			t.Errorf("ResourceFile(%s) = %q, want %q", typ, got, want)
		}
	}
}

func TestSearchRanking(t *testing.T) {
	c := mustLoad(t)
	cases := []struct {
		name  string
		q     Query
		first string
	}{
		{"popular label word", Query{Text: "queue", Cloud: model.CloudAWS}, "aws_sqs_queue"},
		{"exact type", Query{Text: "aws_dynamodb_table_item"}, "aws_dynamodb_table_item"},
		{"type without prefix", Query{Text: "lambda_function_url", Cloud: model.CloudAWS}, "aws_lambda_function_url"},
		{"cross-cloud word", Query{Text: "queue"}, "aws_sqs_queue"},
		{"popular over type prefix", Query{Text: "database"}, "aws_db_instance"},
		{"provider prefix partial", Query{Text: "aws_dynamo"}, "aws_dynamodb_table"},
		{"label not in type", Query{Text: "step functions"}, "aws_sfn_state_machine"},
		{"multi word", Query{Text: "pubsub sub"}, "google_pubsub_subscription"},
		{"data kind", Query{Text: "caller identity", Kind: model.KindData}, "aws_caller_identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := c.Search(tc.q)
			if len(got) == 0 || got[0].Type != tc.first {
				t.Fatalf("first result = %v, want %s", types(got[:min(5, len(got))]), tc.first)
			}
		})
	}

	t.Run("popular before niche", func(t *testing.T) {
		got := types(c.Search(Query{Text: "bucket"}))
		policy := slices.Index(got, "aws_s3_bucket_policy")
		for _, popular := range []string{"aws_s3_bucket", "google_storage_bucket", "oci_objectstorage_bucket"} {
			i := slices.Index(got, popular)
			if i < 0 || policy >= 0 && i > policy {
				t.Errorf("%s at %d, aws_s3_bucket_policy at %d", popular, i, policy)
			}
		}
	})
	t.Run("filters", func(t *testing.T) {
		for _, r := range c.Search(Query{Cloud: model.CloudAzure, PopularOnly: true}) {
			if r.Cloud != model.CloudAzure || !r.Popular {
				t.Errorf("unexpected %+v", r)
			}
		}
		for _, r := range c.Search(Query{Text: "instance", SupportedOnly: true, Limit: 30}) {
			if !r.FlociSupported {
				t.Errorf("unsupported %s returned", r.Type)
			}
		}
		if got := c.Search(Query{Text: "qqqzzz"}); len(got) != 0 {
			t.Errorf("nonsense query matched %v", types(got))
		}
		if got := c.Search(Query{Category: "Databases", Cloud: model.CloudAWS, Limit: 500}); !slices.Contains(types(got), "aws_dynamodb_table") {
			t.Error("aws_dynamodb_table not in Databases")
		}
	})
}

func TestFlociSupport(t *testing.T) {
	c := mustLoad(t)
	cases := []struct {
		kind      model.NodeKind
		typ       string
		service   string
		supported bool
	}{
		{model.KindResource, "aws_s3_bucket", "s3", true},
		{model.KindResource, "aws_apigatewayv2_route", "apigatewayv2", true},
		{model.KindResource, "aws_api_gateway_rest_api", "apigateway", true},
		{model.KindResource, "aws_route_table", "ec2", true},
		{model.KindResource, "aws_route53_zone", "route53", true},
		{model.KindResource, "aws_cloudwatch_log_group", "logs", true},
		{model.KindResource, "aws_cloudwatch_metric_alarm", "monitoring", true},
		{model.KindResource, "aws_cloudwatch_event_rule", "events", true},
		{model.KindResource, "aws_sfn_state_machine", "states", true},
		{model.KindResource, "aws_cognito_identity_pool", "cognito-identity", true},
		{model.KindResource, "aws_cognito_user_pool_client", "cognito-idp", true},
		{model.KindResource, "aws_lb_listener", "elasticloadbalancing", true},
		{model.KindResource, "aws_elb", "elb", true},
		{model.KindResource, "aws_db_instance", "rds", true},
		{model.KindResource, "aws_kinesis_firehose_delivery_stream", "firehose", true},
		{model.KindResource, "aws_kinesis_video_stream", "kinesisvideo", false},
		{model.KindResource, "aws_appstream_fleet", "appstream", false},
		{model.KindData, "aws_iam_policy_document", "iam", true},
		{model.KindData, "aws_caller_identity", "caller", true},
		{model.KindResource, "azurerm_resource_group", "resource-manager", true},
		{model.KindResource, "azurerm_storage_account", "blob", true},
		{model.KindResource, "azurerm_storage_queue", "queue", true},
		{model.KindResource, "azurerm_storage_share", "files", false},
		{model.KindResource, "azurerm_cosmosdb_account", "cosmosdb", true},
		// floci-az answers 404 to App Service Plan creation (2026-10-18), so
		// Function Apps, which need a plan, are export only.
		{model.KindResource, "azurerm_service_plan", "functions", false},
		{model.KindResource, "azurerm_linux_function_app", "functions", false},
		{model.KindResource, "azurerm_spring_cloud_service", "spring", false},
		{model.KindResource, "google_pubsub_topic", "pubsub", true},
		{model.KindResource, "google_compute_instance", "compute", true},
		{model.KindResource, "google_compute_router", "compute-other", false},
		{model.KindResource, "google_firestore_database", "firestore", false},
		{model.KindResource, "google_cloud_run_v2_service", "run", true},
		{model.KindResource, "oci_objectstorage_bucket", "objectstorage", true},
		{model.KindResource, "oci_identity_compartment", "identity", true},
		{model.KindResource, "oci_core_vcn", "core", false},
	}
	for _, tc := range cases {
		s, ok := c.Schema(tc.kind, tc.typ)
		if !ok {
			t.Errorf("%s: no schema", tc.typ)
			continue
		}
		if s.Service != tc.service || s.FlociSupported != tc.supported {
			t.Errorf("%s: service %q supported %v, want %q %v", tc.typ, s.Service, s.FlociSupported, tc.service, tc.supported)
		}
	}

	c.SetLiveServices(model.CloudAWS, map[string]string{"s3": "running"})
	if s, _ := c.Schema(model.KindResource, "aws_sqs_queue"); s.FlociSupported {
		t.Error("sqs still supported after live services without sqs")
	}
	if s, _ := c.Schema(model.KindResource, "aws_s3_bucket"); !s.FlociSupported {
		t.Error("s3 unsupported despite live service")
	}
}

func newWorkflow() *model.Workflow { return &model.Workflow{ID: "wf", Name: "wf"} }

func errorsOf(diags []model.Diagnostic) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

func TestAddNodeDefaultsValidate(t *testing.T) {
	c := mustLoad(t)
	popular := c.Search(Query{PopularOnly: true, Kind: model.KindResource})
	if len(popular) < 50 {
		t.Fatalf("only %d popular resources", len(popular))
	}
	for _, r := range popular {
		t.Run(r.Type, func(t *testing.T) {
			wf := newWorkflow()
			if _, err := c.AddNode(wf, model.KindResource, r.Type, "", model.Position{}); err != nil {
				t.Fatal(err)
			}
			if errs := errorsOf(c.Validate(wf)); len(errs) > 0 {
				t.Fatalf("validation errors: %+v", errs)
			}
			p, _ := c.providerOf(r.Type)
			if wf.Terraform == nil || getAttr(&getBlock(wf.Terraform, "required_providers").Body, p.info.Name) == nil {
				t.Errorf("required_providers missing %s", p.info.Name)
			}
			if !slices.ContainsFunc(wf.Providers, func(nb model.NamedBlock) bool { return nb.Name == p.info.Name }) {
				t.Errorf("provider block %s missing", p.info.Name)
			}
		})
	}
}

func nodeByAddr(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("node %s not found", addr)
	return nil
}

func attrExpr(t *testing.T, n *model.Node, path ...string) string {
	t.Helper()
	b := &n.Body
	for _, blk := range path[:len(path)-1] {
		bl := getBlock(b, blk)
		if bl == nil {
			t.Fatalf("%s: block %s missing", address(n), blk)
		}
		b = &bl.Body
	}
	a := getAttr(b, path[len(path)-1])
	if a == nil {
		t.Fatalf("%s: attribute %s missing", address(n), strings.Join(path, "."))
	}
	return valueSource(a.Value)
}

func TestAddNodeLambdaStarter(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	id, err := c.AddNode(wf, model.KindResource, "aws_lambda_function", "", model.Position{X: 400, Y: 100})
	if err != nil {
		t.Fatal(err)
	}
	fn := nodeByAddr(t, wf, "aws_lambda_function.function")
	if fn.ID != id {
		t.Errorf("returned id %q, node id %q", id, fn.ID)
	}
	if got := attrExpr(t, fn, "role"); got != "aws_iam_role.function_role.arn" {
		t.Errorf("role = %s", got)
	}
	if got := attrExpr(t, fn, "filename"); got != "data.archive_file.function_code.output_path" {
		t.Errorf("filename = %s", got)
	}
	if got := attrExpr(t, fn, "source_code_hash"); got != "data.archive_file.function_code.output_base64sha256" {
		t.Errorf("source_code_hash = %s", got)
	}
	role := nodeByAddr(t, wf, "aws_iam_role.function_role")
	if !strings.Contains(attrExpr(t, role, "assume_role_policy"), "lambda.amazonaws.com") {
		t.Error("role does not trust lambda")
	}
	if role.Position.X >= fn.Position.X {
		t.Errorf("helper at %v not left of function at %v", role.Position, fn.Position)
	}
	rp := getBlock(wf.Terraform, "required_providers")
	for _, p := range []string{"aws", "archive"} {
		if getAttr(&rp.Body, p) == nil {
			t.Errorf("required_providers missing %s", p)
		}
	}
	if len(wf.Providers) != 1 || attrExpr(t, &model.Node{Body: wf.Providers[0].Body}, "region") != "var.aws_region" {
		t.Errorf("providers = %+v", wf.Providers)
	}
	if got := variableDefault(t, wf, "aws_region"); got != `"us-east-1"` {
		t.Errorf("aws_region default = %s", got)
	}
	logs := nodeByAddr(t, wf, "aws_cloudwatch_log_group.function_logs")
	if got := attrExpr(t, logs, "name"); got != `"/aws/lambda/${aws_lambda_function.function.function_name}"` {
		t.Errorf("log group name = %s", got)
	}
	if got := attrExpr(t, logs, "retention_in_days"); got != "14" {
		t.Errorf("log retention = %s", got)
	}
	logPolicy := nodeByAddr(t, wf, "aws_iam_role_policy.function_logs")
	if got := attrExpr(t, logPolicy, "role"); got != "aws_iam_role.function_role.id" ||
		!strings.Contains(attrExpr(t, logPolicy, "policy"), `"${aws_cloudwatch_log_group.function_logs.arn}:*"`) {
		t.Errorf("log policy role = %s policy = %s", got, attrExpr(t, logPolicy, "policy"))
	}

	// A second function gets its own role and code; names stay unique.
	if _, err := c.AddNode(wf, model.KindResource, "aws_lambda_function", "", model.Position{}); err != nil {
		t.Fatal(err)
	}
	fn2 := nodeByAddr(t, wf, "aws_lambda_function.function_2")
	if got := attrExpr(t, fn2, "role"); got != "aws_iam_role.function_2_role.arn" {
		t.Errorf("second role = %s", got)
	}
	if _, err := c.AddNode(wf, model.KindResource, "aws_sqs_queue", "function", model.Position{}); err != nil {
		t.Errorf("same name on another type rejected: %v", err)
	}
	if _, err := c.AddNode(wf, model.KindResource, "aws_lambda_function", "function", model.Position{}); err == nil {
		t.Error("duplicate explicit name accepted")
	}
}

func addNode(t *testing.T, c *Catalog, wf *model.Workflow, typ string, x float64) *model.Node {
	t.Helper()
	id, err := c.AddNode(wf, model.KindResource, typ, "", model.Position{X: x})
	if err != nil {
		t.Fatal(err)
	}
	return findNode(wf, id)
}

func connect(t *testing.T, c *Catalog, wf *model.Workflow, src, dst *model.Node, recipeID string) {
	t.Helper()
	srcID, dstID := src.ID, dst.ID
	recipes := c.Connections(wf, srcID, dstID)
	ids := make([]string, len(recipes))
	for i, r := range recipes {
		ids[i] = r.ID
	}
	if !slices.Contains(ids, recipeID) {
		t.Fatalf("recipe %s not offered for %s -> %s: %v", recipeID, src.Type, dst.Type, ids)
	}
	if err := c.Connect(wf, srcID, dstID, recipeID); err != nil {
		t.Fatal(err)
	}
	if errs := errorsOf(c.Validate(wf)); len(errs) > 0 {
		t.Fatalf("validation errors after %s: %+v", recipeID, errs)
	}
}

func nodesOfType(wf *model.Workflow, kind model.NodeKind, typ string) []*model.Node {
	var out []*model.Node
	for i := range wf.Nodes {
		if wf.Nodes[i].Kind == kind && wf.Nodes[i].Type == typ {
			out = append(out, &wf.Nodes[i])
		}
	}
	return out
}

func TestAddNodeUsesDefaultAWSRegion(t *testing.T) {
	c := mustLoad(t)
	c.SetDefaultAWSRegion("eu-west-1")
	wf := newWorkflow()
	addNode(t, c, wf, "aws_sqs_queue", 0)
	if len(wf.Providers) != 1 || attrExpr(t, &model.Node{Body: wf.Providers[0].Body}, "region") != "var.aws_region" {
		t.Errorf("providers = %+v", wf.Providers)
	}
	if got := variableDefault(t, wf, "aws_region"); got != `"eu-west-1"` {
		t.Errorf("aws_region default = %s", got)
	}
}

func variableDefault(t *testing.T, wf *model.Workflow, name string) string {
	t.Helper()
	for _, v := range wf.Variables {
		if v.Name == name {
			return attrExpr(t, &model.Node{Body: v.Body}, "default")
		}
	}
	t.Fatalf("variable %s missing", name)
	return ""
}

// recipePolicies are the aws_iam_role_policy nodes recipes added (not the
// Lambda starter's log policies).
func recipePolicies(wf *model.Workflow) []*model.Node {
	var out []*model.Node
	for _, n := range nodesOfType(wf, model.KindResource, "aws_iam_role_policy") {
		if !strings.HasSuffix(n.Name, "_logs") {
			out = append(out, n)
		}
	}
	return out
}

func TestConnectSQSToLambda(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	q := addNode(t, c, wf, "aws_sqs_queue", 0)
	fn := addNode(t, c, wf, "aws_lambda_function", 600)
	connect(t, c, wf, q, fn, "sqs-lambda")

	esm := nodesOfType(wf, model.KindResource, "aws_lambda_event_source_mapping")
	if len(esm) != 1 {
		t.Fatalf("%d event source mappings", len(esm))
	}
	if got := attrExpr(t, esm[0], "event_source_arn"); got != "aws_sqs_queue.queue.arn" {
		t.Errorf("event_source_arn = %s", got)
	}
	if got := attrExpr(t, esm[0], "function_name"); got != "aws_lambda_function.function.arn" {
		t.Errorf("function_name = %s", got)
	}
	if esm[0].Position.X <= 0 || esm[0].Position.X >= 600 {
		t.Errorf("helper position %v not between source and target", esm[0].Position)
	}
	pol := recipePolicies(wf)
	if len(pol) != 1 || attrExpr(t, pol[0], "role") != "aws_iam_role.function_role.id" ||
		!strings.Contains(attrExpr(t, pol[0], "policy"), "sqs:ReceiveMessage") {
		t.Fatalf("role policy = %+v", pol)
	}
	if !strings.Contains(attrExpr(t, esm[0], "depends_on"), address(pol[0])) {
		t.Error("mapping does not depend on the role policy")
	}

	// Dragging the other way still offers the trigger.
	wf2 := newWorkflow()
	q2 := addNode(t, c, wf2, "aws_sqs_queue", 0)
	fn2 := addNode(t, c, wf2, "aws_lambda_function", 600)
	connect(t, c, wf2, fn2, q2, "sqs-lambda")
	if len(nodesOfType(wf2, model.KindResource, "aws_lambda_event_source_mapping")) != 1 {
		t.Error("reversed drag did not add a mapping")
	}
}

func TestConnectHTTPAPIToLambda(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	api := addNode(t, c, wf, "aws_apigatewayv2_api", 0)
	fn := addNode(t, c, wf, "aws_lambda_function", 600)
	connect(t, c, wf, api, fn, "apigwv2-lambda")

	integ := nodesOfType(wf, model.KindResource, "aws_apigatewayv2_integration")[0]
	for attr, want := range map[string]string{
		"api_id": "aws_apigatewayv2_api.http_api.id", "integration_type": `"AWS_PROXY"`,
		"integration_uri": "aws_lambda_function.function.invoke_arn", "payload_format_version": `"2.0"`,
	} {
		if got := attrExpr(t, integ, attr); got != want {
			t.Errorf("integration %s = %s, want %s", attr, got, want)
		}
	}
	route := nodesOfType(wf, model.KindResource, "aws_apigatewayv2_route")[0]
	if got := attrExpr(t, route, "route_key"); got != `"ANY /{proxy+}"` {
		t.Errorf("route_key = %s", got)
	}
	if got := attrExpr(t, route, "target"); got != `"integrations/${aws_apigatewayv2_integration.function_integration.id}"` {
		t.Errorf("target = %s", got)
	}
	stages := nodesOfType(wf, model.KindResource, "aws_apigatewayv2_stage")
	if len(stages) != 1 || attrExpr(t, stages[0], "name") != `"$default"` {
		t.Fatalf("stages = %+v", stages)
	}
	perm := nodesOfType(wf, model.KindResource, "aws_lambda_permission")[0]
	if attrExpr(t, perm, "principal") != `"apigateway.amazonaws.com"` ||
		attrExpr(t, perm, "source_arn") != `"${aws_apigatewayv2_api.http_api.execution_arn}/*/*"` {
		t.Errorf("permission = %+v", perm.Body.Attrs)
	}

	// A second function on the same API reuses the stage and gets its own route.
	fn2 := addNode(t, c, wf, "aws_lambda_function", 600)
	connect(t, c, wf, api, fn2, "apigwv2-lambda")
	if n := len(nodesOfType(wf, model.KindResource, "aws_apigatewayv2_stage")); n != 1 {
		t.Errorf("%d stages after second function", n)
	}
	routes := nodesOfType(wf, model.KindResource, "aws_apigatewayv2_route")
	if got := attrExpr(t, routes[1], "route_key"); got != `"ANY /function-2/{proxy+}"` {
		t.Errorf("second route_key = %s", got)
	}
}

func TestConnectSNSToSQS(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	topic := addNode(t, c, wf, "aws_sns_topic", 0)
	q := addNode(t, c, wf, "aws_sqs_queue", 600)
	connect(t, c, wf, topic, q, "sns-sqs")

	sub := nodesOfType(wf, model.KindResource, "aws_sns_topic_subscription")[0]
	if attrExpr(t, sub, "topic_arn") != "aws_sns_topic.topic.arn" || attrExpr(t, sub, "protocol") != `"sqs"` ||
		attrExpr(t, sub, "endpoint") != "aws_sqs_queue.queue.arn" {
		t.Errorf("subscription = %+v", sub.Body.Attrs)
	}
	qp := nodesOfType(wf, model.KindResource, "aws_sqs_queue_policy")
	if len(qp) != 1 || attrExpr(t, qp[0], "queue_url") != "aws_sqs_queue.queue.url" {
		t.Fatalf("queue policies = %+v", qp)
	}
	doc := nodeByAddr(t, wf, strings.TrimSuffix(attrExpr(t, qp[0], "policy"), ".json"))
	st := getBlock(&doc.Body, "statement")
	if st == nil || attrExpr(t, &model.Node{Body: st.Body}, "condition", "values") != "[aws_sns_topic.topic.arn]" ||
		attrExpr(t, &model.Node{Body: st.Body}, "principals", "identifiers") != `["sns.amazonaws.com"]` {
		t.Fatalf("statement = %+v", st)
	}

	// A second topic adds a statement to the same policy instead of a
	// competing aws_sqs_queue_policy.
	topic2 := addNode(t, c, wf, "aws_sns_topic", 0)
	connect(t, c, wf, topic2, q, "sns-sqs")
	if n := len(nodesOfType(wf, model.KindResource, "aws_sqs_queue_policy")); n != 1 {
		t.Errorf("%d queue policies", n)
	}
	doc = nodeByAddr(t, wf, address(doc))
	if n := len(doc.Body.Blocks); n != 2 {
		t.Errorf("%d statements, want 2", n)
	}
}

func TestConnectLambdaToDynamoDB(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	fn := addNode(t, c, wf, "aws_lambda_function", 0)
	orders := addNode(t, c, wf, "aws_dynamodb_table", 600)
	users := addNode(t, c, wf, "aws_dynamodb_table", 600)
	connect(t, c, wf, fn, orders, "lambda-uses-dynamodb_table")
	fn = nodeByAddr(t, wf, "aws_lambda_function.function")
	connect(t, c, wf, fn, users, "lambda-uses-dynamodb_table")

	fn = nodeByAddr(t, wf, "aws_lambda_function.function")
	vars := attrExpr(t, fn, "environment", "variables")
	for _, want := range []string{"TABLE_NAME = aws_dynamodb_table.table.name", "TABLE_2_TABLE_NAME = aws_dynamodb_table.table_2.name"} {
		if !strings.Contains(vars, want) {
			t.Errorf("environment.variables = %s, missing %s", vars, want)
		}
	}
	pols := recipePolicies(wf)
	if len(pols) != 2 {
		t.Fatalf("%d role policies", len(pols))
	}
	p := attrExpr(t, pols[0], "policy")
	for _, want := range []string{"dynamodb:PutItem", "aws_dynamodb_table.table.arn", `"${aws_dynamodb_table.table.arn}/index/*"`} {
		if !strings.Contains(p, want) {
			t.Errorf("policy missing %s:\n%s", want, p)
		}
	}
}

func TestConnectAzureResourceGroup(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	sa := addNode(t, c, wf, "azurerm_storage_account", 0) // creates rg via needs
	rg2 := addNode(t, c, wf, "azurerm_resource_group", 600)
	connect(t, c, wf, rg2, sa, "azure-resource-group")
	sa = nodeByAddr(t, wf, address(sa))
	if got := attrExpr(t, sa, "resource_group_name"); got != "azurerm_resource_group.rg_2.name" {
		t.Errorf("resource_group_name = %s", got)
	}
	if got := attrExpr(t, sa, "location"); got != "azurerm_resource_group.rg_2.location" {
		t.Errorf("location = %s", got)
	}
	for _, r := range c.Connections(wf, rg2.ID, sa.ID) {
		if strings.HasPrefix(r.ID, genericRefPrefix) && strings.Contains(r.ID, "resource_group_name") {
			t.Errorf("generic duplicate of curated recipe offered: %s", r.ID)
		}
	}
}

func TestGenericRecipeRanking(t *testing.T) {
	c := mustLoad(t)
	wf := newWorkflow()
	vpc := &model.Node{ID: "v", Kind: model.KindResource, Type: "aws_vpc", Name: "main"}
	subnet := &model.Node{ID: "s", Kind: model.KindResource, Type: "aws_subnet", Name: "a"}
	wf.Nodes = []model.Node{*vpc, *subnet}

	cands := c.genericCandidates(subnet, vpc, true)
	slices.SortStableFunc(cands, func(a, b genericCandidate) int { return b.score - a.score })
	if len(cands) == 0 || cands[0].attr != "vpc_id" || cands[0].refAttr != "id" {
		t.Fatalf("top candidate = %+v", cands)
	}

	// Through Connections (vpc dragged onto subnet): curated placement first,
	// and applying the generic recipe writes the reference.
	rt := &model.Node{ID: "rt", Kind: model.KindResource, Type: "aws_route_table_association", Name: "a"}
	sub := &model.Node{ID: "sub", Kind: model.KindResource, Type: "aws_subnet", Name: "b"}
	wf.Nodes = append(wf.Nodes, *rt, *sub)
	recipes := c.Connections(wf, "sub", "rt")
	if len(recipes) == 0 || recipes[0].ID != "ref:target:subnet_id:id" {
		t.Fatalf("recipes = %+v", recipes)
	}
	if err := c.Connect(wf, "sub", "rt", recipes[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := attrExpr(t, findNode(wf, "rt"), "subnet_id"); got != "aws_subnet.b.id" {
		t.Errorf("subnet_id = %s", got)
	}
	if last := c.Connections(wf, "sub", "rt"); last[len(last)-1].ID != dependsOnID {
		t.Errorf("depends_on not offered last: %+v", last)
	}
}

func TestValidate(t *testing.T) {
	c := mustLoad(t)
	attr := func(name, src string) model.Attr { return model.Attr{Name: name, Value: expr(src)} }
	wf := &model.Workflow{Nodes: []model.Node{
		{ID: "unknown", Kind: model.KindResource, Type: "aws_does_not_exist", Name: "x"},
		{ID: "missing", Kind: model.KindResource, Type: "aws_sqs_queue_policy", Name: "p",
			Body: model.Body{Attrs: []model.Attr{attr("queue_url", `"u"`), attr("bogus", "1")}}},
		{ID: "blocks", Kind: model.KindResource, Type: "aws_scheduler_schedule", Name: "s",
			Body: model.Body{Attrs: []model.Attr{attr("schedule_expression", `"rate(1 hour)"`), attr("arn", `"x"`)},
				Blocks: []model.Block{{Type: "flexible_time_window", Body: model.Body{Attrs: []model.Attr{attr("mode", `"OFF"`)}}}, {Type: "nope"}}}},
		{ID: "dynamic", Kind: model.KindResource, Type: "aws_scheduler_schedule", Name: "d",
			Body: model.Body{Attrs: []model.Attr{attr("schedule_expression", `"rate(1 hour)"`), attr("count", "2")},
				Blocks: []model.Block{
					{Type: "dynamic", Labels: []string{"flexible_time_window"}, Body: model.Body{Blocks: []model.Block{{Type: "content"}}}},
					{Type: "dynamic", Labels: []string{"target"}},
					{Type: "lifecycle"},
				}}},
		{ID: "deprecated", Kind: model.KindResource, Type: "aws_s3_bucket", Name: "b",
			Body: model.Body{Attrs: []model.Attr{attr("acl", `"private"`)}}},
		{ID: "unsupported", Kind: model.KindResource, Type: "aws_appstream_fleet", Name: "f",
			Body: model.Body{Attrs: []model.Attr{attr("name", `"f"`), attr("instance_type", `"x"`)},
				Blocks: []model.Block{{Type: "compute_capacity", Body: model.Body{Attrs: []model.Attr{attr("desired_instances", "1")}}}}}},
		{ID: "module", Kind: model.KindModule, Type: "module", Name: "m", Body: model.Body{Attrs: []model.Attr{attr("whatever", "1")}}},
	}}
	type key struct{ node, code, attr string }
	got := map[key]model.Severity{}
	for _, d := range c.Validate(wf) {
		got[key{d.NodeID, d.Code, d.Attribute}] = d.Severity
	}
	want := map[key]model.Severity{
		{"unknown", CodeUnknownType, ""}:            model.SeverityError,
		{"missing", CodeMissingAttribute, "policy"}: model.SeverityError,
		{"missing", CodeUnknownAttribute, "bogus"}:  model.SeverityError,
		{"blocks", CodeMissingBlock, "target"}:      model.SeverityError,
		{"blocks", CodeUnknownBlock, "nope"}:        model.SeverityError,
		{"blocks", CodeReadOnlyAttribute, "arn"}:    model.SeverityError,
		{"deprecated", CodeDeprecated, "acl"}:       model.SeverityWarning,
		{"unsupported", CodeFlociUnsupported, ""}:   model.SeverityWarning,
	}
	for k, sev := range want {
		if got[k] != sev {
			t.Errorf("missing %s diagnostic %+v", sev, k)
		}
	}
	for k := range got {
		if k.node == "dynamic" || k.node == "module" {
			t.Errorf("unexpected diagnostic %+v", k)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d diagnostics, want %d: %+v", len(got), len(want), got)
	}
}

func TestTemplatesHaveFiles(t *testing.T) {
	c := mustLoad(t)
	ts := c.Templates()
	if len(ts) != 22 {
		t.Fatalf("%d templates", len(ts))
	}
	for _, tpl := range ts {
		files, ok := c.TemplateFiles(tpl.ID)
		if !ok || files["main.tf"] == nil || files["variables.tf"] == nil || files["outputs.tf"] == nil {
			t.Errorf("%s: files %v", tpl.ID, ok)
		}
	}
	if _, ok := c.TemplateFiles("../curated"); ok {
		t.Error("path traversal accepted")
	}
}

// One edge-pattern template per cloud and pattern, whose description is
// honest about the sandbox: "Runs locally" only when Floci supports every
// resource, "Design and export only" when it doesn't.
func TestEdgePatternTemplates(t *testing.T) {
	c := mustLoad(t)
	seen := map[string]bool{}
	for _, tpl := range c.Templates() {
		if tpl.Category != model.TemplateCategoryEdgePatterns {
			if tpl.Pattern != "" {
				t.Errorf("%s: pattern %q outside the edge-patterns category", tpl.ID, tpl.Pattern)
			}
			continue
		}
		if want := string(tpl.Cloud) + "-" + tpl.Pattern; tpl.ID != want || seen[want] {
			t.Errorf("%s: want one template %s", tpl.ID, want)
		}
		seen[tpl.ID] = true
		files, _ := c.TemplateFiles(tpl.ID)
		wf, _, err := hclcodec.Import(tpl.ID, files)
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		hclcodec.Normalize(wf)
		unsupported := 0
		for _, d := range c.Validate(wf) {
			if d.Code != "floci_unsupported" {
				t.Errorf("%s: %s %s %s", tpl.ID, d.NodeID, d.Code, d.Message)
				continue
			}
			unsupported++
		}
		local := strings.Contains(tpl.Description, "Runs locally")
		designOnly := strings.Contains(tpl.Description, "Design and export only")
		if local == designOnly || local != (unsupported == 0) {
			t.Errorf("%s: description %q, %d resources Floci doesn't support", tpl.ID, tpl.Description, unsupported)
		}
	}
	for _, cloud := range []model.Cloud{model.CloudAWS, model.CloudAzure, model.CloudGCP, model.CloudOCI} {
		for _, p := range []string{model.PatternReverseProxy, model.PatternAPIGateway, model.PatternLoadBalancer} {
			if id := string(cloud) + "-" + p; !seen[id] {
				t.Errorf("no %s template", id)
			}
		}
	}
}
