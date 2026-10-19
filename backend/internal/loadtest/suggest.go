package loadtest

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

// In-sandbox emulator URLs, used when the sandbox is not running; the engine
// rewrites them to host URLs through Resolver.HostURL at start.
const (
	sandboxAWSURL = "http://localhost:4566"
	sandboxGCPURL = "http://localhost:4588"
)

const (
	httpJSONBody   = `{"id":"{{uuid}}","seq":{{seq}},"sentAt":"{{now}}"}`
	pubsubAttrBody = `{"messages":[{"data":%q,"attributes":{"seq":"{{seq}}","id":"{{uuid}}"}}]}`
)

// Suggest builds ready-to-run load targets for what is deployed: HTTP
// endpoints (API Gateway v1/v2 routes, Lambda function URLs, EC2 instance
// ports published on the host, GCS and Pub/Sub REST APIs) first, then AWS
// SDK operations. instances are the sandbox's EC2 containers by instance ID
// (sandbox.Manager.Instances; nil when there are none).
func Suggest(wf *model.Workflow, st *model.DeployedState, r Resolver, instances map[string]sandbox.InstanceAccess) []model.LoadTarget {
	out := []model.LoadTarget{}
	if st == nil {
		return out
	}
	s := suggester{wf: wf, byType: map[string][]*model.DeployedResource{}, awsBase: sandboxAWSURL, gcpBase: sandboxGCPURL}
	wfID := st.WorkflowID
	if wf != nil && wf.ID != "" {
		wfID = wf.ID
	}
	if r != nil {
		if u, ok := r.EmulatorURL(wfID, model.CloudAWS); ok {
			s.awsBase = strings.TrimSuffix(u, "/")
		}
		if u, ok := r.EmulatorURL(wfID, model.CloudGCP); ok {
			s.gcpBase = strings.TrimSuffix(u, "/")
		}
	}
	for i := range st.Resources {
		res := &st.Resources[i]
		if res.Mode == "" || res.Mode == "managed" {
			s.byType[res.Type] = append(s.byType[res.Type], res)
		}
	}
	s.apiGatewayV2()
	s.apiGatewayREST()
	s.functionURLs()
	s.instances(instances)
	s.gcp()
	s.awsSDK()
	return s.out
}

// instances suggests a GET on every HTTP port an EC2 instance has published
// on the host (the Endpoints card's Host URL).
func (s *suggester) instances(insts map[string]sandbox.InstanceAccess) {
	for _, r := range s.byType["aws_instance"] {
		acc, ok := insts[str(r, "id")]
		if !ok {
			continue
		}
		for _, p := range acc.AppPorts() {
			if slices.Contains(sandbox.HTTPAppPorts, p) {
				s.http(r, fmt.Sprintf("GET / · %s:%d", s.name(r, ""), p), http.MethodGet, acc.URL(p))
			}
		}
	}
}

type suggester struct {
	wf      *model.Workflow
	byType  map[string][]*model.DeployedResource
	awsBase string
	gcpBase string
	out     []model.LoadTarget
}

func (s *suggester) apiGatewayV2() {
	for _, api := range s.byType["aws_apigatewayv2_api"] {
		if p := str(api, "protocol_type"); p != "" && p != "HTTP" {
			continue
		}
		id := str(api, "id")
		if id == "" {
			continue
		}
		var stages, routes []string
		for _, st := range s.byType["aws_apigatewayv2_stage"] {
			if str(st, "api_id") == id {
				stages = append(stages, str(st, "name"))
			}
		}
		for _, rt := range s.byType["aws_apigatewayv2_route"] {
			if str(rt, "api_id") == id {
				routes = append(routes, str(rt, "route_key"))
			}
		}
		// Quick-create APIs (target set on the API) get an implicit
		// $default stage and route.
		if str(api, "target") != "" {
			if len(stages) == 0 {
				stages = []string{"$default"}
			}
			if len(routes) == 0 {
				routes = []string{orDefault(str(api, "route_key"), "$default")}
			}
		}
		name := s.name(api, "name")
		for _, stage := range stages {
			seen := map[string]bool{}
			for _, rk := range routes {
				method, path := routeRequest(rk)
				if seen[method+path] {
					continue
				}
				seen[method+path] = true
				label := method + " " + path + " · " + name
				if stage != "$default" {
					label += " (stage " + stage + ")"
				}
				s.http(api, label, method, s.awsBase+"/execute-api/"+id+"/"+stage+path)
			}
		}
	}
}

func (s *suggester) apiGatewayREST() {
	for _, api := range s.byType["aws_api_gateway_rest_api"] {
		id := str(api, "id")
		if id == "" {
			continue
		}
		var stages []string
		for _, st := range s.byType["aws_api_gateway_stage"] {
			if str(st, "rest_api_id") == id {
				stages = append(stages, str(st, "stage_name"))
			}
		}
		paths := map[string]string{str(api, "root_resource_id"): "/"}
		for _, res := range s.byType["aws_api_gateway_resource"] {
			if str(res, "rest_api_id") == id {
				paths[str(res, "id")] = str(res, "path")
			}
		}
		name := s.name(api, "name")
		for _, stage := range stages {
			for _, m := range s.byType["aws_api_gateway_method"] {
				p, ok := paths[str(m, "resource_id")]
				if str(m, "rest_api_id") != id || !ok {
					continue
				}
				method := httpMethod(str(m, "http_method"))
				path := samplePath(p)
				label := fmt.Sprintf("%s %s · %s (stage %s)", method, path, name, stage)
				s.http(api, label, method, s.awsBase+"/restapis/"+id+"/"+stage+"/_user_request_"+path)
			}
		}
	}
}

func (s *suggester) functionURLs() {
	for _, fu := range s.byType["aws_lambda_function_url"] {
		urlID := str(fu, "url_id")
		if urlID == "" {
			continue
		}
		s.http(fu, "GET function URL · "+s.name(fu, "function_name"), http.MethodGet, s.awsBase+"/lambda-url/"+urlID+"/")
	}
}

func (s *suggester) gcp() {
	for _, b := range s.byType["google_storage_bucket"] {
		bucket := str(b, "name")
		if bucket == "" {
			continue
		}
		name := s.name(b, "name")
		base := s.gcpBase + "/storage/v1/b/" + url.PathEscape(bucket) + "/o"
		upload := s.gcpBase + "/upload/storage/v1/b/" + url.PathEscape(bucket) + "/o?uploadType=media&name=loadtest%2F{{uuid}}.json"
		s.http(b, "Upload objects · "+name, http.MethodPost, upload)
		s.http(b, "List objects · "+name, http.MethodGet, base)
	}
	data := base64.StdEncoding.EncodeToString([]byte(`{"source":"nimbo-loadtest"}`))
	for _, t := range s.byType["google_pubsub_topic"] {
		topic := str(t, "id")
		if !strings.HasPrefix(topic, "projects/") {
			if str(t, "project") == "" || str(t, "name") == "" {
				continue
			}
			topic = "projects/" + str(t, "project") + "/topics/" + str(t, "name")
		}
		target := s.target(t, "Publish messages · "+s.name(t, "name"))
		target.Kind = model.TargetHTTP
		target.HTTP = &model.HTTPTarget{
			Method:  http.MethodPost,
			URL:     s.gcpBase + "/v1/" + topic + ":publish",
			Headers: map[string]string{"Content-Type": "application/json"},
			Body:    fmt.Sprintf(pubsubAttrBody, data),
		}
		s.out = append(s.out, target)
	}
}

func (s *suggester) awsSDK() {
	for _, f := range s.byType["aws_lambda_function"] {
		s.aws(f, "Invoke · "+s.name(f, "function_name"), OpLambdaInvoke, first(f, "arn", "function_name"), "", "")
	}
	for _, q := range s.byType["aws_sqs_queue"] {
		s.aws(q, "Send messages · "+s.name(q, "name"), OpSQSSend, first(q, "arn", "url"), "", "")
	}
	for _, t := range s.byType["aws_sns_topic"] {
		s.aws(t, "Publish messages · "+s.name(t, "name"), OpSNSPublish, first(t, "arn"), "", "")
	}
	for _, b := range s.byType["aws_s3_bucket"] {
		name := s.name(b, "bucket")
		bucket := first(b, "bucket", "id")
		s.aws(b, "Upload objects · "+name, OpS3Put, bucket, "", defaultPutKey)
		s.aws(b, "Download an object · "+name, OpS3Get, bucket, "", defaultGetKey)
	}
	for _, t := range s.byType["aws_dynamodb_table"] {
		item, key, ok := dynamoTemplates(t)
		if !ok {
			continue
		}
		name := s.name(t, "name")
		table := first(t, "arn", "name")
		s.aws(t, "Write items · "+name, OpDynamoPut, table, item, "")
		s.aws(t, "Read an item · "+name, OpDynamoGet, table, item, key)
	}
	for _, k := range s.byType["aws_kinesis_stream"] {
		s.aws(k, "Put records · "+s.name(k, "name"), OpKinesisPut, first(k, "arn", "name"), "", "")
	}
	for _, b := range s.byType["aws_cloudwatch_event_bus"] {
		s.aws(b, "Put events · "+s.name(b, "name"), OpEventsPut, first(b, "arn", "name"), "", "")
	}
	for _, m := range s.byType["aws_sfn_state_machine"] {
		s.aws(m, "Start executions · "+s.name(m, "name"), OpStatesStartExe, first(m, "arn", "id"), "", "")
	}
}

func (s *suggester) target(r *model.DeployedResource, label string) model.LoadTarget {
	return model.LoadTarget{NodeID: s.nodeID(r), Label: label}
}

func (s *suggester) http(r *model.DeployedResource, label, method, u string) {
	t := s.target(r, label)
	t.Kind = model.TargetHTTP
	t.HTTP = &model.HTTPTarget{Method: method, URL: u}
	if method != http.MethodGet && method != http.MethodHead && method != http.MethodDelete {
		t.HTTP.Headers = map[string]string{"Content-Type": "application/json"}
		t.HTTP.Body = httpJSONBody
	}
	s.out = append(s.out, t)
}

func (s *suggester) aws(r *model.DeployedResource, label, op, resource, payload, key string) {
	if resource == "" {
		return
	}
	t := s.target(r, label)
	t.Kind = model.TargetAWS
	t.AWS = &model.AWSTarget{Operation: op, Resource: resource, Payload: payload, Key: key}
	s.out = append(s.out, t)
}

func (s *suggester) node(r *model.DeployedResource) *model.Node {
	if s.wf == nil {
		return nil
	}
	for i := range s.wf.Nodes {
		n := &s.wf.Nodes[i]
		if r.NodeID != "" && n.ID == r.NodeID {
			return n
		}
		if r.NodeID == "" && !strings.HasPrefix(r.Address, "module.") && n.Kind == model.KindResource && n.Type == r.Type && n.Name == r.Name {
			return n
		}
	}
	return nil
}

func (s *suggester) nodeID(r *model.DeployedResource) string {
	if r.NodeID != "" {
		return r.NodeID
	}
	if n := s.node(r); n != nil {
		return n.ID
	}
	return ""
}

// name is the friendliest name for r: its canvas label, else the cloud name
// from nameKey, else the Terraform name; instances get their index appended.
func (s *suggester) name(r *model.DeployedResource, nameKey string) string {
	name := ""
	if n := s.node(r); n != nil {
		name = n.Label
	}
	if name == "" {
		name = str(r, nameKey)
	}
	if name == "" {
		name = r.Name
	}
	switch idx := r.Index.(type) {
	case float64:
		name += "[" + strconv.FormatFloat(idx, 'f', -1, 64) + "]"
	case int:
		name += "[" + strconv.Itoa(idx) + "]"
	case string:
		name += "[" + strconv.Quote(idx) + "]"
	}
	return name
}

// routeRequest turns an API Gateway v2 route key into a concrete request.
func routeRequest(routeKey string) (method, path string) {
	m, p, ok := strings.Cut(strings.TrimSpace(routeKey), " ")
	if !ok {
		return http.MethodGet, "/"
	}
	return httpMethod(m), samplePath(strings.TrimSpace(p))
}

func httpMethod(m string) string {
	m = strings.ToUpper(m)
	if m == "ANY" || m == "" {
		return http.MethodGet
	}
	return m
}

// samplePath fills path parameters: {name} becomes 1, a greedy {name+}
// matches the empty remainder.
func samplePath(p string) string {
	segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "+}") {
			out = append(out, "")
			break
		}
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			seg = "1"
		}
		out = append(out, seg)
	}
	return "/" + strings.Join(out, "/")
}

// dynamoTemplates builds a PutItem payload with unique keys and a fixed
// GetItem key from the table's key schema. Binary keys are not supported.
func dynamoTemplates(t *model.DeployedResource) (item, key string, ok bool) {
	attrTypes := map[string]string{}
	if attrs, isList := t.Values["attribute"].([]any); isList {
		for _, a := range attrs {
			if m, isMap := a.(map[string]any); isMap {
				name, _ := m["name"].(string)
				typ, _ := m["type"].(string)
				attrTypes[name] = typ
			}
		}
	}
	hash, rng := str(t, "hash_key"), str(t, "range_key")
	if hash == "" {
		return "", "", false
	}
	var itemParts, keyParts []string
	for i, k := range []string{hash, rng} {
		if k == "" {
			continue
		}
		q, _ := json.Marshal(k)
		switch orDefault(attrTypes[k], "S") {
		case "S":
			itemParts = append(itemParts, string(q)+`:"`+[]string{"{{uuid}}", "{{now}}"}[i]+`"`)
			keyParts = append(keyParts, string(q)+`:"loadtest"`)
		case "N":
			itemParts = append(itemParts, string(q)+":"+[]string{"{{seq}}", "{{rand}}"}[i])
			keyParts = append(keyParts, string(q)+":1")
		default:
			return "", "", false
		}
	}
	if hash != "payload" && rng != "payload" {
		itemParts = append(itemParts, `"payload":"load test {{seq}}"`)
	}
	if hash != "createdAt" && rng != "createdAt" {
		itemParts = append(itemParts, `"createdAt":"{{now}}"`)
	}
	return "{" + strings.Join(itemParts, ",") + "}", "{" + strings.Join(keyParts, ",") + "}", true
}

func str(r *model.DeployedResource, key string) string {
	v, _ := r.Values[key].(string)
	return v
}

func first(r *model.DeployedResource, keys ...string) string {
	for _, k := range keys {
		if v := str(r, k); v != "" {
			return v
		}
	}
	return ""
}
