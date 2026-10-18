package endpoints

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func TestPostmanCollection(t *testing.T) {
	data, err := json.Marshal(Postman(fixture()))
	if err != nil {
		t.Fatal(err)
	}
	// Decode generically, as Postman would, to check the v2.1 essentials.
	var col map[string]any
	if err := json.Unmarshal(data, &col); err != nil {
		t.Fatal(err)
	}
	info := col["info"].(map[string]any)
	if info["schema"] != PostmanSchema || info["name"] != "Shop (Nimbo sandbox)" {
		t.Fatalf("info: %v", info)
	}
	vars := col["variable"].([]any)
	if v := vars[0].(map[string]any); v["key"] != "baseUrl" || v["value"] != base {
		t.Fatalf("variable: %v", vars)
	}

	requests := map[string]map[string]any{}
	folders := map[string]bool{}
	var walk func(items []any)
	walk = func(items []any) {
		for _, x := range items {
			it := x.(map[string]any)
			name, _ := it["name"].(string)
			if name == "" {
				t.Fatalf("item without name: %v", it)
			}
			if sub, ok := it["item"].([]any); ok {
				folders[name] = true
				walk(sub)
				continue
			}
			req, ok := it["request"].(map[string]any)
			if !ok {
				t.Fatalf("item %q is neither folder nor request", name)
			}
			u := req["url"].(map[string]any)
			if !strings.HasPrefix(u["raw"].(string), "{{baseUrl}}") || u["host"].([]any)[0] != "{{baseUrl}}" {
				t.Errorf("%s: url %v does not use baseUrl", name, u)
			}
			if _, ok := req["header"].([]any); !ok {
				t.Errorf("%s: header must be a list", name)
			}
			requests[name] = req
		}
	}
	walk(col["item"].([]any))

	for _, f := range []string{"http", "pets", "shop-api URL", "AWS services (sandbox)"} {
		if !folders[f] {
			t.Errorf("missing folder %q (have %v)", f, folders)
		}
	}
	// ANY /{proxy+} gives a GET and a POST with the editable sample path.
	get, post := requests["GET /{proxy+}"], requests["POST /{proxy+}"]
	if get == nil || post == nil {
		t.Fatalf("proxy requests missing: %v", keys(requests))
	}
	if raw := get["url"].(map[string]any)["raw"]; raw != "{{baseUrl}}/execute-api/abc123/$default/orders" {
		t.Errorf("proxy URL %v", raw)
	}
	if post["body"].(map[string]any)["mode"] != "raw" {
		t.Errorf("POST body: %v", post["body"])
	}

	send := requests["SQS SendMessage · Jobs queue"]
	if send == nil {
		t.Fatalf("SQS request missing: %v", keys(requests))
	}
	assertAWSv4(t, send, "sqs", "us-east-1")
	if !hasHeader(send, "X-Amz-Target", "AmazonSQS.SendMessage") || !hasHeader(send, "Content-Type", "application/x-amz-json-1.0") {
		t.Errorf("SQS headers: %v", send["header"])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(send["body"].(map[string]any)["raw"].(string)), &body); err != nil || body["QueueUrl"] != base+"/000000000000/jobs" {
		t.Errorf("SQS body %v (%v)", body, err)
	}
	for name, svc := range map[string]string{
		"SQS ReceiveMessage · Jobs queue": "sqs",
		"DynamoDB Scan · orders":          "dynamodb",
		"DynamoDB PutItem · orders":       "dynamodb",
		"S3 ListObjectsV2 · shop-assets":  "s3",
		"Lambda Invoke · shop-api":        "lambda",
	} {
		req := requests[name]
		if req == nil {
			t.Errorf("missing %q", name)
			continue
		}
		assertAWSv4(t, req, svc, "us-east-1")
	}
	var put map[string]any
	_ = json.Unmarshal([]byte(requests["DynamoDB PutItem · orders"]["body"].(map[string]any)["raw"].(string)), &put)
	if item, _ := put["Item"].(map[string]any); item["ts"] == nil {
		t.Errorf("PutItem item %v", put)
	}
}

func keys(m map[string]map[string]any) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasHeader(req map[string]any, key, value string) bool {
	for _, h := range req["header"].([]any) {
		if m := h.(map[string]any); m["key"] == key && m["value"] == value {
			return true
		}
	}
	return false
}

func assertAWSv4(t *testing.T, req map[string]any, service, region string) {
	t.Helper()
	auth, ok := req["auth"].(map[string]any)
	if !ok || auth["type"] != "awsv4" {
		t.Fatalf("auth: %v", req["auth"])
	}
	got := map[string]any{}
	for _, kv := range auth["awsv4"].([]any) {
		m := kv.(map[string]any)
		got[m["key"].(string)] = m["value"]
	}
	want := map[string]any{"accessKey": "test", "secretKey": "test", "region": region, "service": service}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("awsv4 %s = %v, want %v", k, got[k], v)
		}
	}
}

// tryFixture is a sandbox whose AWS address is srv, with one function URL.
func tryFixture(srv string) (*model.Workflow, *model.DeployedState, Sandbox) {
	st := &model.DeployedState{WorkflowID: "wf1", Resources: []model.DeployedResource{
		res("aws_lambda_function_url.api", map[string]any{"function_name": "fn", "url_id": "u1"}),
		res("aws_iam_role.r", map[string]any{"name": "r"}),
	}}
	return nil, st, Sandbox{Bases: map[model.Cloud]string{model.CloudAWS: srv}, Running: map[model.Cloud]bool{model.CloudAWS: true}}
}

func TestTryHTTP(t *testing.T) {
	var gotPath, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://example.com/elsewhere", http.StatusFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		gotPath, gotBody, gotCT = r.URL.Path, string(body), r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"n":[1,2]}`))
	}))
	defer srv.Close()
	wf, st, sb := tryFixture(srv.URL)
	ctx := context.Background()

	res, err := Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_lambda_function_url.api", Action: ActRequest, Method: "post", Path: "/orders", Input: `{"a":1}`})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Status != "201 Created" || gotPath != "/lambda-url/u1/orders" || gotBody != `{"a":1}` || gotCT != "application/json" {
		t.Fatalf("result %+v; server saw %s %q %q", res, gotPath, gotBody, gotCT)
	}
	if !strings.Contains(res.Body, "\n  \"ok\": true") {
		t.Errorf("JSON body not pretty-printed: %q", res.Body)
	}

	// Redirects off this machine are not followed.
	res, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_lambda_function_url.api", Action: ActRequest, Path: srv.URL + "/redirect"})
	if err != nil || res.Status != "302 Found" || res.Headers["Location"] != "https://example.com/elsewhere" {
		t.Fatalf("redirect: %+v %v", res, err)
	}

	var re *RequestError
	for _, path := range []string{"https://example.com/x", "http://127.0.0.1:1/x", "http://10.0.0.1" + strings.TrimPrefix(srv.URL, "http://127.0.0.1")} {
		_, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_lambda_function_url.api", Action: ActRequest, Path: path})
		if !errors.As(err, &re) {
			t.Errorf("%s: want a RequestError, got %v", path, err)
		}
	}
	if _, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_iam_role.r", Action: ActRequest}); !errors.As(err, &re) {
		t.Errorf("action on a resource without actions: %v", err)
	}
	if _, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "nope", Action: ActRequest}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown node: %v", err)
	}

	sb.Running = map[model.Cloud]bool{}
	res, err = Try(ctx, wf, st, sb, model.TryRequest{NodeID: "aws_lambda_function_url.api", Action: ActRequest})
	if err != nil || res.OK || res.Error == "" {
		t.Fatalf("stopped sandbox: %+v %v", res, err)
	}
}

func TestReadBodyTruncates(t *testing.T) {
	big := strings.Repeat("a", maxBodySize+10)
	out := readBody(strings.NewReader(big), "text/plain")
	if !strings.HasSuffix(out, "(truncated: the response is larger than 256 KB)") || len(out) > maxBodySize+100 {
		t.Fatalf("truncation: len %d, tail %q", len(out), out[len(out)-60:])
	}
}
