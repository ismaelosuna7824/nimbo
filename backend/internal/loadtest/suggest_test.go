package loadtest

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

type fixedResolver map[model.Cloud]string

func (r fixedResolver) EmulatorURL(_ string, c model.Cloud) (string, bool) {
	u, ok := r[c]
	return u, ok
}

func (fixedResolver) HostURL(_, u string) string { return u }

func res(typ, name string, values map[string]any) model.DeployedResource {
	return model.DeployedResource{Address: typ + "." + name, Mode: "managed", Type: typ, Name: name, Values: values}
}

func TestSuggestBuildsTargetsFromDeployedState(t *testing.T) {
	wf := &model.Workflow{ID: "wf1", Nodes: []model.Node{
		{ID: "n-api", Kind: model.KindResource, Type: "aws_apigatewayv2_api", Name: "http", Label: "Orders API"},
		{ID: "n-q", Kind: model.KindResource, Type: "aws_sqs_queue", Name: "q"},
	}}
	queueARN := "arn:aws:sqs:eu-west-1:000000000000:orders"
	st := &model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{
		res("aws_sqs_queue", "q", map[string]any{"arn": queueARN, "url": "http://localhost:4566/000000000000/orders", "name": "orders"}),
		res("aws_apigatewayv2_api", "http", map[string]any{"id": "abc123", "protocol_type": "HTTP", "name": "orders"}),
		res("aws_apigatewayv2_stage", "default", map[string]any{"api_id": "abc123", "name": "$default"}),
		res("aws_apigatewayv2_route", "hello", map[string]any{"api_id": "abc123", "route_key": "GET /hello"}),
		res("aws_apigatewayv2_route", "item", map[string]any{"api_id": "abc123", "route_key": "POST /items/{id}"}),
		res("aws_apigatewayv2_route", "proxy", map[string]any{"api_id": "abc123", "route_key": "ANY /{proxy+}"}),
		res("aws_apigatewayv2_route", "fallback", map[string]any{"api_id": "abc123", "route_key": "$default"}),
		res("aws_apigatewayv2_api", "ws", map[string]any{"id": "ws1", "protocol_type": "WEBSOCKET"}),
		res("aws_apigatewayv2_api", "quick", map[string]any{"id": "qc1", "protocol_type": "HTTP", "name": "quick", "target": "arn:aws:lambda:us-east-1:000000000000:function:f"}),
		res("aws_api_gateway_rest_api", "rest", map[string]any{"id": "r1", "root_resource_id": "root", "name": "legacy"}),
		res("aws_api_gateway_resource", "users", map[string]any{"id": "res-users", "rest_api_id": "r1", "path": "/users/{userId}"}),
		res("aws_api_gateway_method", "root_get", map[string]any{"rest_api_id": "r1", "resource_id": "root", "http_method": "GET"}),
		res("aws_api_gateway_method", "users_any", map[string]any{"rest_api_id": "r1", "resource_id": "res-users", "http_method": "ANY"}),
		res("aws_api_gateway_stage", "dev", map[string]any{"rest_api_id": "r1", "stage_name": "dev"}),
		res("aws_lambda_function_url", "u", map[string]any{"url_id": "xyz789", "function_name": "hello"}),
		res("aws_dynamodb_table", "t", map[string]any{
			"arn": "arn:aws:dynamodb:us-east-1:000000000000:table/users", "name": "users", "hash_key": "pk", "range_key": "n",
			"attribute": []any{map[string]any{"name": "pk", "type": "S"}, map[string]any{"name": "n", "type": "N"}},
		}),
		res("aws_dynamodb_table", "bin", map[string]any{"name": "blobs", "hash_key": "b", "attribute": []any{map[string]any{"name": "b", "type": "B"}}}),
		res("aws_s3_bucket", "b", map[string]any{"bucket": "assets", "id": "assets"}),
		res("google_storage_bucket", "g", map[string]any{"name": "media"}),
		res("google_pubsub_topic", "p", map[string]any{"id": "projects/demo/topics/events", "name": "events", "project": "demo"}),
	}}
	st.Resources[0].Index = float64(0)

	got := Suggest(wf, st, fixedResolver{model.CloudAWS: "http://127.0.0.1:41000", model.CloudGCP: "http://127.0.0.1:42000/"})

	type row struct{ label, nodeID, what string }
	var rows []row
	for _, tg := range got {
		what := ""
		if tg.HTTP != nil {
			what = tg.HTTP.Method + " " + tg.HTTP.URL
		} else {
			what = tg.AWS.Operation + " " + tg.AWS.Resource + " key=" + tg.AWS.Key + " payload=" + tg.AWS.Payload
		}
		rows = append(rows, row{tg.Label, tg.NodeID, what})
	}
	want := []row{
		{"GET /hello · Orders API", "n-api", "GET http://127.0.0.1:41000/execute-api/abc123/$default/hello"},
		{"POST /items/1 · Orders API", "n-api", "POST http://127.0.0.1:41000/execute-api/abc123/$default/items/1"},
		{"GET / · Orders API", "n-api", "GET http://127.0.0.1:41000/execute-api/abc123/$default/"},
		{"GET / · quick", "", "GET http://127.0.0.1:41000/execute-api/qc1/$default/"},
		{"GET / · legacy (stage dev)", "", "GET http://127.0.0.1:41000/restapis/r1/dev/_user_request_/"},
		{"GET /users/1 · legacy (stage dev)", "", "GET http://127.0.0.1:41000/restapis/r1/dev/_user_request_/users/1"},
		{"GET function URL · hello", "", "GET http://127.0.0.1:41000/lambda-url/xyz789/"},
		{"Upload objects · media", "", "POST http://127.0.0.1:42000/upload/storage/v1/b/media/o?uploadType=media&name=loadtest%2F{{uuid}}.json"},
		{"List objects · media", "", "GET http://127.0.0.1:42000/storage/v1/b/media/o"},
		{"Publish messages · events", "", "POST http://127.0.0.1:42000/v1/projects/demo/topics/events:publish"},
		{"Send messages · orders[0]", "n-q", "sqs:SendMessage " + queueARN + " key= payload="},
		{"Upload objects · assets", "", "s3:PutObject assets key=loadtest/{{uuid}}.json payload="},
		{"Download an object · assets", "", "s3:GetObject assets key=loadtest/sample.json payload="},
		{"Write items · users", "", `dynamodb:PutItem arn:aws:dynamodb:us-east-1:000000000000:table/users key= payload={"pk":"{{uuid}}","n":{{rand}},"payload":"load test {{seq}}","createdAt":"{{now}}"}`},
		{"Read an item · users", "", `dynamodb:GetItem arn:aws:dynamodb:us-east-1:000000000000:table/users key={"pk":"loadtest","n":1} payload={"pk":"{{uuid}}","n":{{rand}},"payload":"load test {{seq}}","createdAt":"{{now}}"}`},
	}
	if len(rows) != len(want) {
		var b strings.Builder
		for _, r := range rows {
			b.WriteString("\n  " + r.label + " | " + r.what)
		}
		t.Fatalf("got %d targets, want %d:%s", len(rows), len(want), b.String())
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("target %d:\n got  %+v\n want %+v", i, rows[i], want[i])
		}
	}
	for _, tg := range got {
		if tg.HTTP != nil && tg.HTTP.Method == "POST" && (tg.HTTP.Body == "" || tg.HTTP.Headers["Content-Type"] != "application/json") {
			t.Errorf("%s: POST without JSON body", tg.Label)
		}
	}
}

func TestSuggestFallsBackToSandboxURLs(t *testing.T) {
	st := &model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{
		res("aws_lambda_function_url", "u", map[string]any{"url_id": "xyz789", "function_name": "hello"}),
	}}
	got := Suggest(nil, st, nil)
	if len(got) != 1 || got[0].HTTP.URL != "http://localhost:4566/lambda-url/xyz789/" {
		t.Fatalf("got %+v", got)
	}
}
