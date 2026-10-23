package endpoints

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// requestAction is the "Try it" action of HTTP resources.
var requestAction = model.TryAction{
	ID: ActRequest, Label: "Send a request",
	Description: "Pick a route, change the path or body if you like, and send it.",
	Input:       "http", Default: sampleJSONBody,
}

// ---------------------------------------------------------------------------
// API Gateway

func (b *builder) httpAPIs() {
	for _, api := range b.byType["aws_apigatewayv2_api"] {
		id := str(api, "id")
		if id == "" || (str(api, "protocol_type") != "" && str(api, "protocol_type") != "HTTP") {
			continue
		}
		var stages, routes []string
		for _, typ := range []string{"aws_apigatewayv2_stage", "aws_apigatewayv2_route", "aws_apigatewayv2_integration", "aws_apigatewayv2_deployment"} {
			for _, r := range b.byType[typ] {
				if str(r, "api_id") != id {
					continue
				}
				b.done[r] = true
				switch typ {
				case "aws_apigatewayv2_stage":
					stages = append(stages, str(r, "name"))
				case "aws_apigatewayv2_route":
					routes = append(routes, str(r, "route_key"))
				}
			}
		}
		// APIs defined by an OpenAPI document (body) declare their routes
		// there.
		routes = append(routes, openAPIRoutes(str(api, "body"))...)
		// Quick-create APIs (target on the API) get an implicit $default
		// stage and route.
		if str(api, "target") != "" {
			if len(stages) == 0 {
				stages = []string{"$default"}
			}
			if len(routes) == 0 {
				routes = []string{first(api, "route_key")}
				if routes[0] == "" {
					routes[0] = "$default"
				}
			}
		}
		stages = uniq(stages)
		slices.SortStableFunc(stages, func(a, c string) int {
			switch {
			case a == c:
				return 0
			case a == "$default":
				return -1
			case c == "$default":
				return 1
			}
			return 0
		})

		e := b.add(api, KindHTTPAPI, "", target{cloud: model.CloudAWS, region: b.regionOf(str(api, "arn"))})
		e.Summary = "Your HTTP API. Send requests to the URLs below from your browser, curl or Postman."
		if len(stages) == 0 {
			e.Warning = "No stage is deployed, so the API can't be called yet. Add an aws_apigatewayv2_stage named \"$default\" with auto_deploy = true."
		}
		for i, stage := range stages {
			base := b.aws + "/execute-api/" + id + "/" + stage
			if i == 0 {
				e.t.httpBase = base
			}
			label := "Invoke URL"
			if len(stages) > 1 {
				label += " (stage " + stage + ")"
			}
			e.field(label, base, "Add a route path after it, e.g. "+base+"/"+b.sample)
			for _, rk := range uniq(routes) {
				e.Endpoints = append(e.Endpoints, b.routeEndpoint(base, rk, stage, len(stages) > 1))
			}
		}
		e.field("API ID", id, "")
		if ep := str(api, "api_endpoint"); ep != "" {
			e.field("Address on real AWS", ep, "What the URL would be in a real AWS account; in the sandbox use the invoke URL above.")
		}
		if len(e.Endpoints) > 0 {
			e.addHTTPSnippets()
			e.action(requestAction)
		}
	}
}

// openAPIRoutes lists the route keys ("GET /orders", "ANY /{proxy+}") of
// an HTTP API's OpenAPI body, sorted; YAML or invalid bodies give none.
func openAPIRoutes(body string) []string {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if body == "" || json.Unmarshal([]byte(body), &doc) != nil {
		return nil
	}
	var out []string
	for path, ops := range doc.Paths {
		for op := range ops {
			switch op = strings.ToLower(op); op {
			case "get", "put", "post", "delete", "options", "head", "patch":
				out = append(out, strings.ToUpper(op)+" "+path)
			case "x-amazon-apigateway-any-method":
				out = append(out, "ANY "+path)
			}
		}
	}
	slices.Sort(out)
	return out
}

// routeEndpoint turns a v2 route key ("GET /items/{id}", "ANY /{proxy+}",
// "$default") into a callable endpoint under the stage base URL.
func (b *builder) routeEndpoint(base, routeKey, stage string, showStage bool) model.HTTPEndpoint {
	method, path, ok := strings.Cut(strings.TrimSpace(routeKey), " ")
	if !ok {
		// $default catches every method and path.
		return model.HTTPEndpoint{
			Method: "ANY", Path: "/{proxy+}", URL: base + "/" + b.sample, Proxy: true,
			Description: "Catch-all route: any method and any path reach your code. Change the end of the URL to the path you want.",
		}
	}
	method = strings.ToUpper(method)
	path = strings.TrimSpace(path)
	concrete, proxy := fillPath(path, b.sample)
	ep := model.HTTPEndpoint{Method: method, Path: path, URL: base + concrete, Proxy: proxy}
	var desc []string
	if method == "ANY" {
		desc = append(desc, "Any method works (GET, POST, PUT, DELETE, …).")
	}
	switch {
	case proxy:
		desc = append(desc, "Catch-all route: change the end of the URL to the path you want.")
	case hasParams(path):
		desc = append(desc, "Path parameters are filled with 1; replace them with real values.")
	}
	if showStage {
		desc = append(desc, "Stage "+stage+".")
	}
	ep.Description = strings.Join(desc, " ")
	return ep
}

func (b *builder) restAPIs() {
	for _, api := range b.byType["aws_api_gateway_rest_api"] {
		id := str(api, "id")
		if id == "" {
			continue
		}
		var stages []string
		paths := map[string]string{str(api, "root_resource_id"): "/"}
		var methods []*resource
		for _, typ := range []string{"aws_api_gateway_stage", "aws_api_gateway_resource", "aws_api_gateway_method", "aws_api_gateway_integration",
			"aws_api_gateway_deployment", "aws_api_gateway_method_response", "aws_api_gateway_integration_response", "aws_api_gateway_method_settings"} {
			for _, r := range b.byType[typ] {
				if str(r, "rest_api_id") != id {
					continue
				}
				b.done[r] = true
				switch typ {
				case "aws_api_gateway_stage":
					stages = append(stages, str(r, "stage_name"))
				case "aws_api_gateway_resource":
					paths[str(r, "id")] = str(r, "path")
				case "aws_api_gateway_method":
					methods = append(methods, r)
				}
			}
		}
		stages = uniq(stages)
		e := b.add(api, KindHTTPAPI, "Your REST API. Send requests to the URLs below from your browser, curl or Postman.",
			target{cloud: model.CloudAWS, region: b.regionOf(str(api, "arn"))})
		if len(stages) == 0 {
			e.Warning = "No stage is deployed, so the API can't be called yet. Add an aws_api_gateway_deployment and an aws_api_gateway_stage."
		}
		for i, stage := range stages {
			base := b.aws + "/restapis/" + id + "/" + stage + "/_user_request_"
			if i == 0 {
				e.t.httpBase = base
			}
			label := "Invoke URL"
			if len(stages) > 1 {
				label += " (stage " + stage + ")"
			}
			e.field(label, base, "Add a resource path after it, e.g. "+base+"/"+b.sample)
			for _, m := range methods {
				p, ok := paths[str(m, "resource_id")]
				if !ok {
					continue
				}
				e.Endpoints = append(e.Endpoints, b.routeEndpoint(base, str(m, "http_method")+" "+p, stage, len(stages) > 1))
			}
		}
		e.field("API ID", id, "")
		if len(e.Endpoints) > 0 {
			e.addHTTPSnippets()
			e.action(requestAction)
		}
	}
}

func (b *builder) functionURLs() {
	for _, fu := range b.byType["aws_lambda_function_url"] {
		urlID := str(fu, "url_id")
		if urlID == "" {
			if u, err := url.Parse(str(fu, "function_url")); err == nil {
				urlID, _, _ = strings.Cut(u.Hostname(), ".")
			}
		}
		if urlID == "" {
			continue
		}
		base := b.aws + "/lambda-url/" + urlID
		e := b.add(fu, KindFunctionURL, "A web address that runs your function directly. Send any method and path to it from your browser, curl or Postman.",
			target{cloud: model.CloudAWS, httpBase: base, region: b.region})
		e.Label = b.label(fu, "function_name") + " URL"
		e.field("URL", base+"/", "Any path after it reaches your function.")
		e.field("Function", str(fu, "function_name"), "")
		if at := str(fu, "authorization_type"); at == "AWS_IAM" {
			e.field("Authorization", at, "Requests must be signed with AWS credentials (test/test).")
		}
		e.Endpoints = []model.HTTPEndpoint{{Method: "ANY", Path: "/", URL: base + "/", Description: "Any method and path work."}}
		e.addHTTPSnippets()
		e.action(requestAction)
	}
}

// ---------------------------------------------------------------------------
// Lambda

func (b *builder) functions() {
	for _, f := range b.byType["aws_lambda_function"] {
		name := first(f, "function_name", "id")
		if name == "" {
			continue
		}
		arn := str(f, "arn")
		region := b.regionOf(arn)
		e := b.add(f, KindFunction, "", target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.Summary = "A function that runs your code. Invoke it directly with a JSON event to test it." + b.triggers(f)
		e.field("Function name", name, "")
		e.field("ARN", arn, "")
		e.field("Runtime", str(f, "runtime"), "")
		e.field("Handler", str(f, "handler"), "")
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"lambda invoke --function-name "+shq(name)+" \\\n  --cli-binary-format raw-in-base64-out \\\n  --payload '{}' response.json && cat response.json"))
		e.snippet("JavaScript", "javascript", jsSDK(sdkLambda, b.aws, region, []string{"InvokeCommand"}, fmt.Sprintf(`
const out = await client.send(new InvokeCommand({
  FunctionName: %s,
  Payload: JSON.stringify({}),
}));
console.log(out.FunctionError ?? "OK", new TextDecoder().decode(out.Payload));`, jsStr(name))))
		e.snippet("Python", "python", pySDK(sdkLambda, b.aws, region, []string{"import json"}, fmt.Sprintf(`
out = client.invoke(FunctionName=%s, Payload=json.dumps({}))
print(out.get("FunctionError", "OK"), out["Payload"].read().decode())`, jsStr(name))))
		e.action(model.TryAction{ID: ActInvoke, Label: "Invoke", Description: "Run the function with this JSON event and show what it returns.", Input: "json", Default: "{}"})
	}
}

// triggers explains what calls function f besides direct invocation.
func (b *builder) triggers(f *resource) string {
	arn, name := str(f, "arn"), str(f, "function_name")
	matches := func(ref string) bool {
		return ref != "" && (ref == arn || ref == name || strings.Contains(ref, ":function:"+name))
	}
	var parts []string
	for _, api := range b.byType["aws_apigatewayv2_api"] {
		for _, in := range b.byType["aws_apigatewayv2_integration"] {
			if str(in, "api_id") == str(api, "id") && (matches(str(in, "integration_uri")) || strings.Contains(str(in, "integration_uri"), ":function:"+name+"/")) {
				parts = append(parts, "requests to the HTTP API "+b.label(api, "name"))
				break
			}
		}
	}
	for _, m := range b.byType["aws_lambda_event_source_mapping"] {
		if !matches(str(m, "function_name")) && !matches(str(m, "function_arn")) {
			continue
		}
		src := str(m, "event_source_arn")
		parts = append(parts, "messages from "+b.labelByARN(src))
	}
	if len(parts) == 0 {
		return ""
	}
	return " It also runs automatically for " + strings.Join(uniq(parts), " and ") + "."
}

// labelByARN names the deployed resource with this ARN, else the ARN's
// resource name.
func (b *builder) labelByARN(arn string) string {
	for _, r := range b.ordered {
		if str(r, "arn") == arn && arn != "" {
			return b.label(r, nameKey(r.Type))
		}
	}
	_, _, _, res, ok := arnParts(arn)
	if !ok {
		return arn
	}
	if i := strings.LastIndexAny(res, ":/"); i >= 0 {
		return res[i+1:]
	}
	return res
}

// consumer returns the label of a function consuming the queue/stream arn.
func (b *builder) consumer(arn string) string {
	for _, m := range b.byType["aws_lambda_event_source_mapping"] {
		if str(m, "event_source_arn") != arn || arn == "" {
			continue
		}
		fn := first(m, "function_arn", "function_name")
		for _, f := range b.byType["aws_lambda_function"] {
			if fn == str(f, "arn") || fn == str(f, "function_name") || strings.HasPrefix(fn, str(f, "arn")+":") {
				return b.label(f, "function_name")
			}
		}
		return b.labelByARN(fn)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Messaging

// queueURL is the host-reachable URL of an SQS queue: the path of the URL
// the emulator reported (/<account>/<name>) on the sandbox address.
func (b *builder) queueURL(q *resource) string {
	if u, err := url.Parse(str(q, "url")); err == nil && strings.Count(strings.Trim(u.Path, "/"), "/") == 1 {
		return b.aws + u.Path
	}
	_, _, account, name, ok := arnParts(str(q, "arn"))
	if !ok {
		account, name = emulatorAccount, str(q, "name")
	}
	if name == "" {
		return ""
	}
	return b.aws + "/" + account + "/" + name
}

func (b *builder) queues() {
	for _, q := range b.byType["aws_sqs_queue"] {
		qurl := b.queueURL(q)
		if qurl == "" {
			continue
		}
		arn := str(q, "arn")
		region := b.regionOf(arn)
		name := first(q, "name")
		t := target{cloud: model.CloudAWS, region: region, name: name, arn: arn, url: qurl,
			fifo: boolean(q, "fifo_queue") || strings.HasSuffix(name, ".fifo"), dedup: boolean(q, "content_based_deduplication")}
		e := b.add(q, KindQueue, "", t)
		e.Summary = "A message queue. Send messages to it and read them back with the queue URL below."
		if c := b.consumer(arn); c != "" {
			e.Summary += " Your function " + c + " picks up its messages automatically, so reading the queue may find nothing: check what the function did instead."
		}
		e.field("Queue URL", qurl, "Use it as QueueUrl in SDKs and --queue-url in the AWS CLI.")
		e.field("ARN", arn, "")
		e.field("Name", name, "")
		if rp := str(q, "redrive_policy"); rp != "" {
			var p struct {
				DeadLetterTargetArn string `json:"deadLetterTargetArn"`
				MaxReceiveCount     any    `json:"maxReceiveCount"`
			}
			if json.Unmarshal([]byte(rp), &p) == nil && p.DeadLetterTargetArn != "" {
				e.field("Dead-letter queue", b.labelByARN(p.DeadLetterTargetArn), fmt.Sprintf("Messages that fail %v times move there.", p.MaxReceiveCount))
			}
		}
		sendExtra, jsExtra, pyExtra := "", "", ""
		if t.fifo {
			sendExtra = " \\\n  --message-group-id demo"
			jsExtra = "\n  MessageGroupId: \"demo\","
			pyExtra = ", MessageGroupId=\"demo\""
			if !t.dedup {
				sendExtra += " --message-deduplication-id \"$(date +%s%N)\""
				jsExtra += "\n  MessageDeduplicationId: String(Date.now()),"
				pyExtra += ", MessageDeduplicationId=str(time.time_ns())"
			}
		}
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"sqs send-message --queue-url "+shq(qurl)+" \\\n  --message-body "+shq(helloMessage)+sendExtra,
			"sqs receive-message --queue-url "+shq(qurl)+" \\\n  --max-number-of-messages 10 --visibility-timeout 0"))
		e.snippet("JavaScript", "javascript", jsSDK(sdkSQS, b.aws, region, []string{"SendMessageCommand", "ReceiveMessageCommand"}, fmt.Sprintf(`
const QueueUrl = %s;

await client.send(new SendMessageCommand({
  QueueUrl,
  MessageBody: JSON.stringify(%s),%s
}));

const { Messages = [] } = await client.send(new ReceiveMessageCommand({
  QueueUrl,
  MaxNumberOfMessages: 10,
  VisibilityTimeout: 0,
}));
console.log(Messages.map((m) => m.Body));`, jsStr(qurl), jsLit(helloMessage), jsExtra)))
		pyImports := []string{"import json"}
		if t.fifo && !t.dedup {
			pyImports = append(pyImports, "import time")
		}
		e.snippet("Python", "python", pySDK(sdkSQS, b.aws, region, pyImports, fmt.Sprintf(`
queue_url = %s

client.send_message(QueueUrl=queue_url, MessageBody=json.dumps(%s)%s)

out = client.receive_message(QueueUrl=queue_url, MaxNumberOfMessages=10, VisibilityTimeout=0)
for m in out.get("Messages", []):
    print(m["Body"])`, jsStr(qurl), pyLit(helloMessage), pyExtra)))
		e.action(model.TryAction{ID: ActSend, Label: "Send a message", Description: "Put this text on the queue.", Input: "text", Default: helloMessage, Writes: true})
		e.action(model.TryAction{ID: ActReceive, Label: "Peek at messages", Description: "Read up to 10 waiting messages without removing them (they stay visible).", Input: "none"})
	}
}

func (b *builder) topics() {
	for _, tp := range b.byType["aws_sns_topic"] {
		arn := first(tp, "arn", "id")
		if arn == "" {
			continue
		}
		region := b.regionOf(arn)
		name := str(tp, "name")
		t := target{cloud: model.CloudAWS, region: region, name: name, arn: arn,
			fifo: boolean(tp, "fifo_topic") || strings.HasSuffix(name, ".fifo"), dedup: boolean(tp, "content_based_deduplication")}
		e := b.add(tp, KindTopic, "A notification topic. Every message you publish is copied to all of its subscribers.", t)
		e.field("Topic ARN", arn, "Use it as TopicArn in SDKs and --topic-arn in the AWS CLI.")
		e.field("Name", name, "")
		var subs []string
		for _, s := range b.byType["aws_sns_topic_subscription"] {
			if str(s, "topic_arn") == arn {
				subs = append(subs, str(s, "protocol")+": "+b.labelByARN(str(s, "endpoint")))
			}
		}
		if len(subs) > 0 {
			e.field("Subscribers", strings.Join(subs, ", "), "")
		}
		extra, jsExtra, pyExtra := "", "", ""
		if t.fifo {
			extra = " --message-group-id demo"
			jsExtra = "\n  MessageGroupId: \"demo\","
			pyExtra = ", MessageGroupId=\"demo\""
			if !t.dedup {
				extra += " --message-deduplication-id \"$(date +%s%N)\""
				jsExtra += "\n  MessageDeduplicationId: String(Date.now()),"
				pyExtra += ", MessageDeduplicationId=str(time.time_ns())"
			}
		}
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region, "sns publish --topic-arn "+shq(arn)+" \\\n  --message "+shq(helloMessage)+extra))
		e.snippet("JavaScript", "javascript", jsSDK(sdkSNS, b.aws, region, []string{"PublishCommand"}, fmt.Sprintf(`
const out = await client.send(new PublishCommand({
  TopicArn: %s,
  Message: JSON.stringify(%s),%s
}));
console.log(out.MessageId);`, jsStr(arn), jsLit(helloMessage), jsExtra)))
		pyImports := []string{"import json"}
		if t.fifo && !t.dedup {
			pyImports = append(pyImports, "import time")
		}
		e.snippet("Python", "python", pySDK(sdkSNS, b.aws, region, pyImports, fmt.Sprintf(`
out = client.publish(TopicArn=%s, Message=json.dumps(%s)%s)
print(out["MessageId"])`, jsStr(arn), pyLit(helloMessage), pyExtra)))
		e.action(model.TryAction{ID: ActPublish, Label: "Publish a message", Description: "Send this text to every subscriber.", Input: "text", Default: helloMessage, Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Storage

const s3PutDefault = `{"key":"hello.txt","body":"Hello from Nimbo"}`

func (b *builder) buckets() {
	for _, bk := range b.byType["aws_s3_bucket"] {
		name := first(bk, "bucket", "id")
		if name == "" {
			continue
		}
		arn := str(bk, "arn")
		region := b.regionOf(arn)
		e := b.add(bk, KindBucket, "A storage bucket for files (objects). Upload, list and download them with the bucket name below.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.field("Bucket name", name, "")
		e.field("URL (path style)", b.aws+"/"+name, "Objects live at this URL + /<key>. SDKs need path-style addressing.")
		e.field("ARN", arn, "")
		s3uri := "s3://" + name
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"s3 ls "+shq(s3uri+"/"),
			"s3 cp ./hello.txt "+shq(s3uri+"/hello.txt"),
			"s3 cp "+shq(s3uri+"/hello.txt")+" -"))
		e.snippet("JavaScript", "javascript", jsSDK(sdkS3, b.aws, region, []string{"PutObjectCommand", "ListObjectsV2Command", "GetObjectCommand"}, fmt.Sprintf(`
const Bucket = %s;

await client.send(new PutObjectCommand({ Bucket, Key: "hello.txt", Body: "Hello from Nimbo" }));

const list = await client.send(new ListObjectsV2Command({ Bucket }));
console.log((list.Contents ?? []).map((o) => o.Key));

const obj = await client.send(new GetObjectCommand({ Bucket, Key: "hello.txt" }));
console.log(await obj.Body.transformToString());`, jsStr(name))))
		e.snippet("Python", "python", pySDK(sdkS3, b.aws, region, nil, fmt.Sprintf(`
bucket = %s

client.put_object(Bucket=bucket, Key="hello.txt", Body=b"Hello from Nimbo")

for obj in client.list_objects_v2(Bucket=bucket).get("Contents", []):
    print(obj["Key"])

print(client.get_object(Bucket=bucket, Key="hello.txt")["Body"].read().decode())`, jsStr(name))))
		e.action(model.TryAction{ID: ActList, Label: "List files", Description: "Show the first 50 objects.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Upload a file", Description: "Store body as an object named key.", Input: "json", Default: s3PutDefault, Writes: true})
		e.action(model.TryAction{ID: ActGet, Label: "Download a file", Description: "Show the content of the object with this key.", Input: "text", Default: "hello.txt"})
	}
}

// keyAttr is one key attribute of a DynamoDB table.
type keyAttr struct{ name, typ string }

func tableKeys(t *resource) []keyAttr {
	types := map[string]string{}
	for _, a := range list(t, "attribute") {
		types[mstr(a, "name")] = mstr(a, "type")
	}
	var out []keyAttr
	for _, k := range []string{str(t, "hash_key"), str(t, "range_key")} {
		if k != "" {
			typ := types[k]
			if typ == "" {
				typ = "S"
			}
			out = append(out, keyAttr{k, typ})
		}
	}
	return out
}

// exampleItem is a plain JSON item with sample key values plus a message.
func exampleItem(keys []keyAttr) map[string]any {
	item := map[string]any{}
	for i, k := range keys {
		switch k.typ {
		case "N":
			item[k.name] = float64(i + 1)
		case "B":
			item[k.name] = "aGVsbG8="
		default:
			item[k.name] = []string{"example-1", "2026-01-01"}[i]
		}
	}
	if _, ok := item["message"]; !ok {
		item["message"] = "Hello from Nimbo"
	}
	return item
}

// typedItem renders a plain item in DynamoDB's typed JSON (the AWS CLI and
// low-level SDK clients use it).
func typedItem(item map[string]any, keys []keyAttr) map[string]any {
	out := map[string]any{}
	for k, v := range item {
		typ := "S"
		for _, ka := range keys {
			if ka.name == k {
				typ = ka.typ
			}
		}
		switch x := v.(type) {
		case float64:
			out[k] = map[string]any{"N": fmt.Sprint(x)}
		default:
			out[k] = map[string]any{typ: fmt.Sprint(x)}
		}
	}
	return out
}

func mustJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (b *builder) tables() {
	for _, tb := range b.byType["aws_dynamodb_table"] {
		name := first(tb, "name", "id")
		if name == "" {
			continue
		}
		arn := str(tb, "arn")
		region := b.regionOf(arn)
		keys := tableKeys(tb)
		e := b.add(tb, KindTable, "A NoSQL table. Every item needs the key below; other attributes are free-form.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.field("Table name", name, "")
		var ks []string
		typeNames := map[string]string{"S": "string", "N": "number", "B": "binary"}
		for i, k := range keys {
			role := "partition key"
			if i == 1 {
				role = "sort key"
			}
			ks = append(ks, fmt.Sprintf("%s (%s, %s)", k.name, typeNames[k.typ], role))
		}
		e.field("Key", strings.Join(ks, ", "), "")
		e.field("ARN", arn, "")
		item := exampleItem(keys)
		typed := mustJSON(typedItem(item, keys))
		e.t.item = typed
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"dynamodb put-item --table-name "+shq(name)+" \\\n  --item "+shq(typed),
			"dynamodb scan --table-name "+shq(name)+" --max-items 25"))
		e.snippet("JavaScript", "javascript", jsSDK(sdkDynamo, b.aws, region, []string{"PutItemCommand", "ScanCommand"}, fmt.Sprintf(`
const TableName = %s;

await client.send(new PutItemCommand({
  TableName,
  Item: %s,
}));

const { Items = [] } = await client.send(new ScanCommand({ TableName, Limit: 25 }));
console.log(JSON.stringify(Items, null, 2));`, jsStr(name), jsLit(typed))))
		e.snippet("Python", "python", pySDK(sdkDynamo, b.aws, region, nil, fmt.Sprintf(`
table = %s

client.put_item(TableName=table, Item=%s)

for item in client.scan(TableName=table, Limit=25).get("Items", []):
    print(item)`, jsStr(name), pyLit(typed))))
		e.action(model.TryAction{ID: ActScan, Label: "Show items", Description: "Read the first 25 items as plain JSON.", Input: "none"})
		e.action(model.TryAction{ID: ActPut, Label: "Save an item", Description: "Write this item (plain JSON; it must include the key).", Input: "json", Default: mustJSON(item), Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Streams, events, workflows

func (b *builder) streams() {
	for _, s := range b.byType["aws_kinesis_stream"] {
		name := first(s, "name")
		if name == "" {
			continue
		}
		arn := first(s, "arn", "id")
		region := b.regionOf(arn)
		e := b.add(s, KindStream, "A data stream. Put records on it; consumers read them in order per partition key.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		if c := b.consumer(arn); c != "" {
			e.Summary += " Your function " + c + " reads it automatically."
		}
		e.field("Stream name", name, "")
		e.field("ARN", arn, "")
		e.field("Shards", str(s, "shard_count"), "")
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"kinesis put-record --stream-name "+shq(name)+" --partition-key demo \\\n  --cli-binary-format raw-in-base64-out --data "+shq(helloMessage)))
		e.snippet("JavaScript", "javascript", jsSDK(sdkKinesis, b.aws, region, []string{"PutRecordCommand"}, fmt.Sprintf(`
const out = await client.send(new PutRecordCommand({
  StreamName: %s,
  PartitionKey: "demo",
  Data: new TextEncoder().encode(JSON.stringify(%s)),
}));
console.log(out.SequenceNumber);`, jsStr(name), jsLit(helloMessage))))
		e.snippet("Python", "python", pySDK(sdkKinesis, b.aws, region, []string{"import json"}, fmt.Sprintf(`
out = client.put_record(StreamName=%s, PartitionKey="demo", Data=json.dumps(%s).encode())
print(out["SequenceNumber"])`, jsStr(name), pyLit(helloMessage))))
		e.action(model.TryAction{ID: ActPut, Label: "Put a record", Description: "Add this text to the stream (partition key \"nimbo\").", Input: "text", Default: helloMessage, Writes: true})
	}
}

// eventSample builds a sample event matching the first simple rule on bus.
func (b *builder) eventSample(bus string) (source, detailType string) {
	source, detailType = "nimbo.try", "Example"
	for _, r := range b.byType["aws_cloudwatch_event_rule"] {
		rb := str(r, "event_bus_name")
		if rb == "" {
			rb = "default"
		}
		if rb != bus && !strings.HasSuffix(rb, "/"+bus) {
			continue
		}
		var p map[string]any
		if json.Unmarshal([]byte(str(r, "event_pattern")), &p) != nil {
			continue
		}
		pick := func(key string) string {
			if l, ok := p[key].([]any); ok && len(l) > 0 {
				s, _ := l[0].(string)
				return s
			}
			return ""
		}
		if s := pick("source"); s != "" {
			source = s
			if d := pick("detail-type"); d != "" {
				detailType = d
			}
			return source, detailType
		}
	}
	return source, detailType
}

func (b *builder) eventBuses() {
	add := func(r *resource, name, arn string) {
		region := b.regionOf(arn)
		e := b.add(r, KindEventBus, "An event bus. Put events on it; rules that match an event forward it to their targets.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		if r.Type != "aws_cloudwatch_event_bus" {
			e.Address = "default-event-bus"
			e.NodeID = ""
			e.Type = "aws_cloudwatch_event_bus"
			e.Label = "default event bus"
			e.Summary = "Your account's default event bus, where your rules listen. Put events on it; matching rules forward them to their targets."
		}
		e.field("Event bus name", name, "")
		e.field("ARN", arn, "")
		var rules []string
		for _, rl := range b.byType["aws_cloudwatch_event_rule"] {
			rb := str(rl, "event_bus_name")
			if rb == "" {
				rb = "default"
			}
			if rb == name || rb == arn {
				rules = append(rules, b.label(rl, "name"))
			}
		}
		if len(rules) > 0 {
			e.field("Rules", strings.Join(rules, ", "), "")
		}
		source, dt := b.eventSample(name)
		sample := map[string]any{"source": source, "detail-type": dt, "detail": map[string]any{"message": "Hello from Nimbo"}}
		entries := mustJSON([]map[string]any{{"EventBusName": name, "Source": source, "DetailType": dt, "Detail": `{"message":"Hello from Nimbo"}`}})
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region, "events put-events --entries "+shq(entries)))
		e.snippet("JavaScript", "javascript", jsSDK(sdkEvents, b.aws, region, []string{"PutEventsCommand"}, fmt.Sprintf(`
const out = await client.send(new PutEventsCommand({
  Entries: [{
    EventBusName: %s,
    Source: %s,
    DetailType: %s,
    Detail: JSON.stringify({ message: "Hello from Nimbo" }),
  }],
}));
console.log(out.FailedEntryCount, out.Entries);`, jsStr(name), jsStr(source), jsStr(dt))))
		e.snippet("Python", "python", pySDK(sdkEvents, b.aws, region, []string{"import json"}, fmt.Sprintf(`
out = client.put_events(Entries=[{
    "EventBusName": %s,
    "Source": %s,
    "DetailType": %s,
    "Detail": json.dumps({"message": "Hello from Nimbo"}),
}])
print(out["FailedEntryCount"], out["Entries"])`, jsStr(name), jsStr(source), jsStr(dt))))
		e.action(model.TryAction{ID: ActPut, Label: "Put an event", Description: "Send this event (source, detail-type and detail) to the bus.", Input: "json", Default: mustJSON(sample), Writes: true})
	}
	hasDefault := false
	for _, bus := range b.byType["aws_cloudwatch_event_bus"] {
		name := first(bus, "name", "id")
		if name == "" {
			continue
		}
		hasDefault = hasDefault || name == "default"
		add(bus, name, str(bus, "arn"))
	}
	if hasDefault {
		return
	}
	for _, rl := range b.byType["aws_cloudwatch_event_rule"] {
		if bus := str(rl, "event_bus_name"); bus == "" || bus == "default" {
			region := b.regionOf(str(rl, "arn"))
			// The rule itself stays listed on its own.
			add(&resource{Address: "default-event-bus", Type: "aws_cloudwatch_event_rule", Name: "default", Values: map[string]any{}},
				"default", "arn:aws:events:"+region+":"+emulatorAccount+":event-bus/default")
			return
		}
	}
}

func (b *builder) stateMachines() {
	for _, m := range b.byType["aws_sfn_state_machine"] {
		arn := first(m, "arn", "id")
		if arn == "" {
			continue
		}
		region := b.regionOf(arn)
		name := str(m, "name")
		e := b.add(m, KindStateMachine, "A workflow (Step Functions state machine). Start an execution with a JSON input and watch its status.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.field("State machine ARN", arn, "")
		e.field("Name", name, "")
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region,
			"stepfunctions start-execution --state-machine-arn "+shq(arn)+" --input '{}'",
			"stepfunctions list-executions --state-machine-arn "+shq(arn)))
		e.snippet("JavaScript", "javascript", jsSDK(sdkSFN, b.aws, region, []string{"StartExecutionCommand", "DescribeExecutionCommand"}, fmt.Sprintf(`
const { executionArn } = await client.send(new StartExecutionCommand({
  stateMachineArn: %s,
  input: JSON.stringify({}),
}));
await new Promise((r) => setTimeout(r, 1000));
const out = await client.send(new DescribeExecutionCommand({ executionArn }));
console.log(out.status, out.output);`, jsStr(arn))))
		e.snippet("Python", "python", pySDK(sdkSFN, b.aws, region, []string{"import json", "import time"}, fmt.Sprintf(`
arn = client.start_execution(stateMachineArn=%s, input=json.dumps({}))["executionArn"]
time.sleep(1)
out = client.describe_execution(executionArn=arn)
print(out["status"], out.get("output"))`, jsStr(arn))))
		e.action(model.TryAction{ID: ActStart, Label: "Start an execution", Description: "Run the state machine with this JSON input and show its status after a second.", Input: "json", Default: "{}", Writes: true})
	}
}

// ---------------------------------------------------------------------------
// Configuration and identity

func (b *builder) secrets() {
	for _, s := range b.byType["aws_secretsmanager_secret"] {
		arn := first(s, "arn", "id")
		if arn == "" {
			continue
		}
		region := b.regionOf(arn)
		name := str(s, "name")
		e := b.add(s, KindSecret, "A secret (password, API key, …). Your code reads it by name or ARN; in the sandbox you can look at its value.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.field("Name", name, "")
		e.field("ARN", arn, "")
		id := name
		if id == "" {
			id = arn
		}
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region, "secretsmanager get-secret-value --secret-id "+shq(id)))
		e.snippet("JavaScript", "javascript", jsSDK(sdkSecrets, b.aws, region, []string{"GetSecretValueCommand"}, fmt.Sprintf(`
const out = await client.send(new GetSecretValueCommand({ SecretId: %s }));
console.log(out.SecretString);`, jsStr(id))))
		e.snippet("Python", "python", pySDK(sdkSecrets, b.aws, region, nil, fmt.Sprintf(`
print(client.get_secret_value(SecretId=%s)["SecretString"])`, jsStr(id))))
		e.action(model.TryAction{ID: ActGet, Label: "Show the value", Description: "Read the current secret value (sandbox data only).", Input: "none"})
	}
}

func (b *builder) parameters() {
	for _, p := range b.byType["aws_ssm_parameter"] {
		name := first(p, "name", "id")
		if name == "" {
			continue
		}
		arn := str(p, "arn")
		region := b.regionOf(arn)
		e := b.add(p, KindParameter, "A configuration value in Parameter Store. Your code reads it by name.",
			target{cloud: model.CloudAWS, region: region, name: name, arn: arn})
		e.field("Name", name, "")
		e.field("Type", str(p, "type"), "")
		e.field("ARN", arn, "")
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region, "ssm get-parameter --name "+shq(name)+" --with-decryption"))
		e.snippet("JavaScript", "javascript", jsSDK(sdkSSM, b.aws, region, []string{"GetParameterCommand"}, fmt.Sprintf(`
const out = await client.send(new GetParameterCommand({ Name: %s, WithDecryption: true }));
console.log(out.Parameter.Value);`, jsStr(name))))
		e.snippet("Python", "python", pySDK(sdkSSM, b.aws, region, nil, fmt.Sprintf(`
print(client.get_parameter(Name=%s, WithDecryption=True)["Parameter"]["Value"])`, jsStr(name))))
		e.action(model.TryAction{ID: ActGet, Label: "Show the value", Description: "Read the parameter's current value.", Input: "none"})
	}
}

func (b *builder) userPools() {
	for _, up := range b.byType["aws_cognito_user_pool"] {
		id := str(up, "id")
		if id == "" {
			continue
		}
		arn := str(up, "arn")
		region := b.regionOf(arn)
		e := b.add(up, KindUserPool, "A user directory (Cognito user pool) for sign-up and sign-in. Your app talks to it with the pool and app client IDs.",
			target{cloud: model.CloudAWS, region: region, name: id, arn: arn})
		e.field("User pool ID", id, "")
		e.field("Cognito endpoint", b.aws, "Point the Cognito SDK (or Amplify's endpoint option) here instead of cognito-idp."+region+".amazonaws.com.")
		for _, c := range b.byType["aws_cognito_user_pool_client"] {
			if str(c, "user_pool_id") == id {
				b.done[c] = true
				e.field("App client ID ("+b.label(c, "name")+")", str(c, "id"), "")
			}
		}
		e.field("ARN", arn, "")
		e.snippet("AWS CLI", "bash", awsCLI(b.aws, region, "cognito-idp list-users --user-pool-id "+shq(id)))
		e.snippet("JavaScript", "javascript", jsSDK(sdkCognito, b.aws, region, []string{"ListUsersCommand"}, fmt.Sprintf(`
const out = await client.send(new ListUsersCommand({ UserPoolId: %s }));
console.log(out.Users);`, jsStr(id))))
		e.snippet("Python", "python", pySDK(sdkCognito, b.aws, region, nil, fmt.Sprintf(`
print(client.list_users(UserPoolId=%s)["Users"])`, jsStr(id))))
	}
}

// ---------------------------------------------------------------------------
// Databases and caches

const noHostAccess = "Not reachable from your computer yet: the sandbox publishes only the emulator API ports on 127.0.0.1, not the port this %s listens on. The address below is how code running inside the sandbox reaches it."

func (b *builder) databases() {
	for _, typ := range []string{"aws_db_instance", "aws_rds_cluster"} {
		for _, d := range b.byType[typ] {
			e := b.add(d, KindDatabase, "A managed SQL database ("+first(d, "engine")+").", target{cloud: model.CloudAWS, region: b.regionOf(str(d, "arn"))})
			e.Warning = fmt.Sprintf(noHostAccess, "database")
			if typ == "aws_db_instance" {
				e.field("Endpoint", first(d, "endpoint", "address"), "")
				e.field("Host", str(d, "address"), "")
			} else {
				e.field("Endpoint", str(d, "endpoint"), "")
				e.field("Reader endpoint", str(d, "reader_endpoint"), "")
			}
			e.field("Port", str(d, "port"), "")
			e.field("Engine", strings.TrimSpace(first(d, "engine")+" "+str(d, "engine_version_actual")), "")
			e.field("Database", first(d, "db_name", "database_name"), "")
			e.field("Username", first(d, "username", "master_username"), "")
			e.field("ARN", str(d, "arn"), "")
		}
	}
}

func (b *builder) caches() {
	for _, typ := range []string{"aws_elasticache_cluster", "aws_elasticache_replication_group", "aws_elasticache_serverless_cache"} {
		for _, c := range b.byType[typ] {
			e := b.add(c, KindCache, "An in-memory cache ("+first(c, "engine")+").", target{cloud: model.CloudAWS, region: b.regionOf(str(c, "arn"))})
			if e.Summary == "An in-memory cache ()." {
				e.Summary = "An in-memory cache (Redis/Valkey/Memcached)."
			}
			e.Warning = fmt.Sprintf(noHostAccess, "cache")
			switch typ {
			case "aws_elasticache_cluster":
				e.field("Configuration endpoint", str(c, "configuration_endpoint"), "")
				for i, n := range list(c, "cache_nodes") {
					e.field(fmt.Sprintf("Node %d", i+1), mstr(n, "address")+":"+mstr(n, "port"), "")
				}
			case "aws_elasticache_replication_group":
				e.field("Primary endpoint", str(c, "primary_endpoint_address"), "")
				e.field("Reader endpoint", str(c, "reader_endpoint_address"), "")
				e.field("Configuration endpoint", str(c, "configuration_endpoint_address"), "")
				e.field("Port", str(c, "port"), "")
			default:
				for _, ep := range list(c, "endpoint") {
					e.field("Endpoint", mstr(ep, "address")+":"+mstr(ep, "port"), "")
				}
			}
			e.field("ARN", str(c, "arn"), "")
		}
	}
}

// ---------------------------------------------------------------------------
// Everything else

// supportSummaries explain common resources that have nothing to call.
var supportSummaries = map[string]string{
	"aws_iam_role":                        "A permission set (IAM role) your services assume. Nothing to call; it decides what they may do.",
	"aws_iam_policy":                      "A permission policy. Nothing to call; it lists what an identity may do.",
	"aws_iam_role_policy":                 "Permissions attached to a role. Nothing to call.",
	"aws_iam_role_policy_attachment":      "Links a policy to a role. Nothing to call.",
	"aws_iam_user":                        "An IAM user. In the sandbox every request uses the test credentials anyway.",
	"aws_lambda_permission":               "Allows another service to invoke your function. Nothing to call.",
	"aws_lambda_event_source_mapping":     "Connects a queue or stream to a function: new messages are delivered to the function automatically.",
	"aws_sns_topic_subscription":          "Delivers a topic's messages to a subscriber. Publish to the topic to use it.",
	"aws_sqs_queue_policy":                "Controls who may send messages to a queue. Nothing to call.",
	"aws_sqs_queue_redrive_policy":        "Moves failing messages to a dead-letter queue. Nothing to call.",
	"aws_cloudwatch_log_group":            "Where logs are stored. Read them with: aws logs tail <name> (against the sandbox endpoint).",
	"aws_cloudwatch_event_rule":           "A rule that matches events (or a schedule) and forwards them to its targets.",
	"aws_cloudwatch_event_target":         "Where a rule sends matching events. Nothing to call; put events on the bus to trigger it.",
	"aws_scheduler_schedule":              "Runs its target on a schedule. Nothing to call.",
	"aws_s3_bucket_policy":                "Controls who may access a bucket. Nothing to call.",
	"aws_s3_bucket_versioning":            "Keeps old versions of a bucket's objects. Nothing to call.",
	"aws_s3_bucket_public_access_block":   "Controls public access to a bucket. Nothing to call.",
	"aws_s3_object":                       "A file stored in a bucket.",
	"aws_s3_bucket_website_configuration": "Static website settings of a bucket.",
	"oci_identity_compartment":            "A compartment: a folder that groups resources and scopes permissions. Nothing to call; pass its OCID as compartment ID.",
	"oci_identity_policy":                 "Permission statements for groups. Nothing to call; the sandbox stores them but doesn't enforce them.",
	"oci_identity_user":                   "An IAM user. In the sandbox every request works with any key.",
	"oci_identity_group":                  "A group of users that policies grant permissions to. Nothing to call.",
	"oci_identity_user_group_membership":  "Adds a user to a group. Nothing to call.",
	"oci_objectstorage_preauthrequest":    "A pre-authenticated link to a bucket or object; its URL is on the bucket.",
	"oci_functions_application":           "Groups functions and their shared settings. Invoke the functions themselves.",
	"oci_streaming_stream_pool":           "Groups streams and their shared settings. Use the streams themselves.",
	"oci_containerengine_node_pool":       "Worker nodes of a Kubernetes cluster. Use the cluster's kubeconfig.",
}

var identifierKeys = []string{"name", "id", "arn", "url", "endpoint", "dns_name", "address", "domain_name"}

func (b *builder) generic(r *resource) {
	cloud := cloudOf(r.Type)
	e := b.add(r, KindGeneric, "", target{cloud: cloud})
	e.Summary = supportSummaries[r.Type]
	if e.Summary == "" {
		e.Summary = "Deployed in your sandbox. Nimbo has no ready-made examples for this type yet; use the identifiers below with the sandbox settings."
	}
	if b.sb.Bases[cloud] == "" {
		e.Warning = "Start the sandbox to get the addresses of this resource."
	}
	seen := map[string]bool{}
	for _, k := range identifierKeys {
		if v := str(r, k); v != "" && !seen[v] {
			seen[v] = true
			e.field(fieldLabel(k), v, "")
		}
	}
	var extra []string
	for k := range r.Values {
		if strings.HasSuffix(k, "_url") || strings.HasSuffix(k, "_endpoint") {
			extra = append(extra, k)
		}
	}
	slices.Sort(extra)
	for _, k := range extra {
		if v := str(r, k); v != "" && !seen[v] && len(e.Fields) < 8 {
			seen[v] = true
			e.field(fieldLabel(k), v, "")
		}
	}
}

func fieldLabel(key string) string {
	switch key {
	case "id":
		return "ID"
	case "arn":
		return "ARN"
	case "url":
		return "URL"
	case "dns_name":
		return "DNS name"
	}
	s := strings.ReplaceAll(key, "_", " ")
	s = strings.ReplaceAll(s, " url", " URL")
	s = strings.ReplaceAll(s, " arn", " ARN")
	return strings.ToUpper(s[:1]) + s[1:]
}
