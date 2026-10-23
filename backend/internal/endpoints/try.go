package endpoints

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"

	"github.com/ismaelosuna7824/nimbo/backend/internal/loadtest"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// ErrNotFound reports a TryRequest naming no deployed resource.
var ErrNotFound = errors.New("no deployed resource with that node ID or address")

// RequestError is a TryRequest the executor refuses (a bad request).
type RequestError struct{ Msg string }

func (e *RequestError) Error() string { return e.Msg }

func badRequest(format string, args ...any) error {
	return &RequestError{Msg: fmt.Sprintf(format, args...)}
}

const (
	tryTimeout  = 15 * time.Second
	maxBodySize = 256 << 10
	maxRedirect = 5
)

// Try runs req on a deployed resource of the workflow. Failures of the
// operation itself (an HTTP 500, an AWS error, a stopped sandbox) are
// reported in the result; the error is ErrNotFound or a *RequestError.
func Try(ctx context.Context, wf *model.Workflow, st *model.DeployedState, sb Sandbox, req model.TryRequest) (model.TryResult, error) {
	b := newBuilder(wf, st, sb)
	e := b.find(req.NodeID)
	if e == nil {
		return model.TryResult{}, ErrNotFound
	}
	var act *model.TryAction
	for i := range e.Actions {
		if e.Actions[i].ID == req.Action {
			act = &e.Actions[i]
		}
	}
	if act == nil {
		ids := make([]string, len(e.Actions))
		for i, a := range e.Actions {
			ids[i] = a.ID
		}
		if len(ids) == 0 {
			return model.TryResult{}, badRequest("%s has no actions to try", e.Label)
		}
		return model.TryResult{}, badRequest("unknown action %q for %s (available: %s)", req.Action, e.Label, strings.Join(ids, ", "))
	}
	if act.Input == "json" && !json.Valid([]byte(orDefault(req.Input, act.Default))) {
		return model.TryResult{}, badRequest("input must be valid JSON")
	}
	if act.Input == "http" {
		if err := b.checkHTTP(e, &req); err != nil {
			return model.TryResult{}, err
		}
	}
	switch {
	case e.Kind == KindInstance && !sb.Instances[e.t.name].Running:
		return model.TryResult{Error: "The instance is not running. Start the sandbox, then try again."}, nil
	case e.Kind != KindInstance && !sb.Running[e.t.cloud]:
		return model.TryResult{Error: "The sandbox is not running. Start it, then try again."}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, tryTimeout)
	defer cancel()
	input := orDefault(req.Input, act.Default)
	if act.Input == "none" {
		input = ""
	}
	start := time.Now()
	var res model.TryResult
	var err error
	switch {
	case act.Input == "http" && e.t.cloud == model.CloudAzure:
		res = b.tryAzureHTTP(ctx, e, req)
	case act.Input == "http":
		res = doHTTP(ctx, req.Method, req.Path, req.Headers, req.Input)
	case e.t.cloud == model.CloudAzure:
		res, err = b.tryAzure(ctx, e, act.ID, input)
	case e.t.cloud == model.CloudGCP:
		res, err = b.tryGCP(ctx, e, act.ID, input)
	case e.t.cloud == model.CloudOCI:
		res, err = b.tryOCI(ctx, e, act.ID, input)
	default:
		res, err = b.tryAWS(ctx, e, act.ID, input)
	}
	if err != nil {
		res = model.TryResult{Error: describeError(err)}
	}
	res.LatencyMs = float64(time.Since(start).Microseconds()) / 1000
	return res, nil
}

// find looks a resource up by address, then by node ID.
func (b *builder) find(ref string) *entry {
	if ref == "" {
		return nil
	}
	for i := range b.entries {
		if b.entries[i].Address == ref {
			return &b.entries[i]
		}
	}
	for i := range b.entries {
		if b.entries[i].NodeID == ref {
			return &b.entries[i]
		}
	}
	return nil
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func describeError(err error) string {
	var ae smithy.APIError
	if errors.As(err, &ae) {
		return ae.ErrorCode() + ": " + ae.ErrorMessage()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "No answer within 15 seconds."
	}
	return err.Error()
}

// ---------------------------------------------------------------------------
// HTTP

var validMethod = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true}

// checkHTTP normalizes an HTTP try request: method (ANY means GET), URL
// (a path relative to the resource, or a full URL that must point at this
// sandbox).
func (b *builder) checkHTTP(e *entry, req *model.TryRequest) error {
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" || req.Method == "ANY" {
		req.Method = http.MethodGet
	}
	if !validMethod[req.Method] {
		return badRequest("unsupported method %q", req.Method)
	}
	target := strings.TrimSpace(req.Path)
	switch {
	case target == "" && len(e.Endpoints) > 0:
		target = e.Endpoints[0].URL
	case target == "":
		target = e.t.httpBase + "/"
	case !strings.Contains(target, "://"):
		target = e.t.httpBase + "/" + strings.TrimPrefix(target, "/")
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return badRequest("invalid URL %q", target)
	}
	if !b.sandboxURL(u) {
		return badRequest("Try only calls this workflow's sandbox (%s); use Connections for other services", b.aws)
	}
	req.Path = u.String()
	return nil
}

// sandboxURL reports whether u is served by one of the sandbox emulators or
// a published app port of one of the workflow's EC2 instances.
func (b *builder) sandboxURL(u *url.URL) bool {
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if !loopback(u.Hostname()) || u.Port() == "" {
		return false
	}
	for _, base := range b.sb.Bases {
		bu, err := url.Parse(base)
		if err == nil && bu.Port() != "" && bu.Port() == u.Port() {
			return true
		}
	}
	return b.instancePort(u.Port())
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tryClient never follows redirects away from this machine. Each request
// opens its own connection, like curl: a reused keep-alive connection would
// pin every Try to the pod or replica a connection-level load balancer (a
// Kubernetes Service, Nimbo's port forward) picked first.
var tryClient = &http.Client{
	Timeout:   tryTimeout,
	Transport: freshTransport(),
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirect || !loopback(req.URL.Hostname()) {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

func freshTransport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	return tr
}

func doHTTP(ctx context.Context, method, u string, headers map[string]string, body string) model.TryResult {
	return doHTTPWith(ctx, tryClient, method, u, headers, body)
}

// doHTTPWith sends one request with client; a Host header sets the
// request's host name (host-routed services such as Azure Container Apps).
func doHTTPWith(ctx context.Context, client *http.Client, method, u string, headers map[string]string, body string) model.TryResult {
	var rd io.Reader
	if body != "" && method != http.MethodGet && method != http.MethodHead {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return model.TryResult{Error: err.Error()}
	}
	for k, v := range headers {
		switch {
		case strings.EqualFold(strings.TrimSpace(k), "Host"):
			req.Host = v
		case strings.TrimSpace(k) != "":
			req.Header.Set(k, v)
		}
	}
	if rd != nil && req.Header.Get("Content-Type") == "" {
		if json.Valid([]byte(body)) {
			req.Header.Set("Content-Type", "application/json")
		} else {
			req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Timeout() {
			return model.TryResult{Error: "No answer within 15 seconds."}
		}
		return model.TryResult{Error: err.Error()}
	}
	defer resp.Body.Close()
	res := model.TryResult{
		OK:          resp.StatusCode < 400,
		Status:      resp.Status,
		ContentType: resp.Header.Get("Content-Type"),
		Headers:     map[string]string{},
	}
	for k, v := range resp.Header {
		res.Headers[k] = strings.Join(v, ", ")
	}
	res.Body = readBody(resp.Body, res.ContentType)
	if !res.OK {
		res.Error = "The request failed with HTTP " + resp.Status + "."
	}
	return res
}

// readBody reads at most maxBodySize bytes, pretty-printing JSON.
func readBody(r io.Reader, contentType string) string {
	data, err := io.ReadAll(io.LimitReader(r, maxBodySize+1))
	truncated := len(data) > maxBodySize
	if truncated {
		data = data[:maxBodySize]
	}
	out := string(data)
	if !truncated && (strings.Contains(contentType, "json") || json.Valid(data)) {
		out = prettyJSON(data)
	}
	if truncated {
		out += "\n… (truncated: the response is larger than 256 KB)"
	}
	if err != nil {
		out += "\n… (reading the response failed: " + err.Error() + ")"
	}
	return out
}

func prettyJSON(data []byte) string {
	var buf bytes.Buffer
	if json.Indent(&buf, bytes.TrimSpace(data), "", "  ") != nil {
		return string(data)
	}
	return buf.String()
}

// jsonResult is a successful result with v as indented JSON.
func jsonResult(status string, v any) model.TryResult {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
	return model.TryResult{OK: true, Status: status, ContentType: "application/json", Body: strings.TrimSpace(buf.String())}
}

// textOrJSON keeps text as is, pretty-printing it when it is JSON.
func textOrJSON(s string) (body, contentType string) {
	if json.Valid([]byte(s)) {
		return prettyJSON([]byte(s)), "application/json"
	}
	return s, "text/plain"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// ---------------------------------------------------------------------------
// AWS

func (b *builder) tryAWS(ctx context.Context, e *entry, action, input string) (model.TryResult, error) {
	cfg := loadtest.SandboxAWSConfig(b.aws, e.t.region, tryClient)
	t := e.t
	switch e.Kind + "/" + action {
	case KindFunction + "/" + ActInvoke:
		out, err := lambda.NewFromConfig(cfg).Invoke(ctx, &lambda.InvokeInput{FunctionName: &t.name, Payload: []byte(input)})
		if err != nil {
			return model.TryResult{}, err
		}
		body, ct := textOrJSON(string(out.Payload))
		res := model.TryResult{OK: out.FunctionError == nil, Status: "Status " + strconv.Itoa(int(out.StatusCode)), Body: body, ContentType: ct}
		if out.FunctionError != nil {
			res.Error = "The function failed (" + *out.FunctionError + "); its error is below."
		}
		return res, nil

	case KindQueue + "/" + ActSend:
		in := &sqs.SendMessageInput{QueueUrl: &t.url, MessageBody: &input}
		if t.fifo {
			in.MessageGroupId = aws.String("nimbo")
			if !t.dedup {
				in.MessageDeduplicationId = aws.String(strconv.FormatInt(time.Now().UnixNano(), 10))
			}
		}
		out, err := sqs.NewFromConfig(cfg).SendMessage(ctx, in)
		if err != nil {
			return model.TryResult{}, err
		}
		return jsonResult("Message sent", map[string]any{"messageId": aws.ToString(out.MessageId)}), nil

	case KindQueue + "/" + ActReceive:
		c := sqs.NewFromConfig(cfg)
		// The Go SDK omits a zero VisibilityTimeout, so received messages
		// are made visible again explicitly: the receive only peeks.
		out, err := c.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl: &t.url, MaxNumberOfMessages: 10, WaitTimeSeconds: 0,
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{sqstypes.MessageSystemAttributeNameSentTimestamp},
		})
		if err != nil {
			return model.TryResult{}, err
		}
		for _, m := range out.Messages {
			if _, err := c.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{QueueUrl: &t.url, ReceiptHandle: m.ReceiptHandle, VisibilityTimeout: 0}); err != nil {
				return model.TryResult{}, err
			}
		}
		msgs := make([]map[string]any, 0, len(out.Messages))
		for _, m := range out.Messages {
			item := map[string]any{"messageId": aws.ToString(m.MessageId), "body": aws.ToString(m.Body)}
			if ts, err := strconv.ParseInt(m.Attributes["SentTimestamp"], 10, 64); err == nil {
				item["sentAt"] = time.UnixMilli(ts).UTC().Format(time.RFC3339)
			}
			msgs = append(msgs, item)
		}
		return jsonResult(plural(len(msgs), "message", "messages")+" waiting", msgs), nil

	case KindTopic + "/" + ActPublish:
		in := &sns.PublishInput{TopicArn: &t.arn, Message: &input}
		if t.fifo {
			in.MessageGroupId = aws.String("nimbo")
			if !t.dedup {
				in.MessageDeduplicationId = aws.String(strconv.FormatInt(time.Now().UnixNano(), 10))
			}
		}
		out, err := sns.NewFromConfig(cfg).Publish(ctx, in)
		if err != nil {
			return model.TryResult{}, err
		}
		return jsonResult("Message published", map[string]any{"messageId": aws.ToString(out.MessageId)}), nil

	case KindBucket + "/" + ActList:
		out, err := s3Client(cfg).ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &t.name, MaxKeys: aws.Int32(50)})
		if err != nil {
			return model.TryResult{}, err
		}
		objs := make([]map[string]any, 0, len(out.Contents))
		for _, o := range out.Contents {
			item := map[string]any{"key": aws.ToString(o.Key), "size": aws.ToInt64(o.Size)}
			if o.LastModified != nil {
				item["lastModified"] = o.LastModified.UTC().Format(time.RFC3339)
			}
			objs = append(objs, item)
		}
		status := plural(len(objs), "object", "objects")
		if aws.ToBool(out.IsTruncated) {
			status += " (first 50)"
		}
		return jsonResult(status, objs), nil

	case KindBucket + "/" + ActPut:
		var in struct {
			Key  string `json:"key"`
			Body string `json:"body"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Key == "" {
			return model.TryResult{Error: `Input must be {"key": "<object name>", "body": "<text>"}.`}, nil
		}
		ct := "text/plain; charset=utf-8"
		if json.Valid([]byte(in.Body)) {
			ct = "application/json"
		}
		if _, err := s3Client(cfg).PutObject(ctx, &s3.PutObjectInput{Bucket: &t.name, Key: &in.Key, Body: strings.NewReader(in.Body), ContentType: &ct}); err != nil {
			return model.TryResult{}, err
		}
		return jsonResult("Uploaded "+in.Key, map[string]any{"bucket": t.name, "key": in.Key, "size": len(in.Body)}), nil

	case KindBucket + "/" + ActGet:
		key := strings.TrimSpace(input)
		out, err := s3Client(cfg).GetObject(ctx, &s3.GetObjectInput{Bucket: &t.name, Key: &key})
		if err != nil {
			return model.TryResult{}, err
		}
		defer out.Body.Close()
		ct := aws.ToString(out.ContentType)
		return model.TryResult{OK: true, Status: "Downloaded " + key, ContentType: ct, Body: readBody(out.Body, ct)}, nil

	case KindTable + "/" + ActScan:
		out, err := dynamodb.NewFromConfig(cfg).Scan(ctx, &dynamodb.ScanInput{TableName: &t.name, Limit: aws.Int32(25)})
		if err != nil {
			return model.TryResult{}, err
		}
		items := []map[string]any{}
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &items); err != nil {
			return model.TryResult{}, err
		}
		status := plural(len(items), "item", "items")
		if len(out.LastEvaluatedKey) > 0 {
			status += " (first 25)"
		}
		return jsonResult(status, items), nil

	case KindTable + "/" + ActPut:
		item, err := loadtest.DynamoItem(input)
		if err != nil {
			return model.TryResult{Error: "Input must be a JSON object item: " + err.Error()}, nil
		}
		if _, err := dynamodb.NewFromConfig(cfg).PutItem(ctx, &dynamodb.PutItemInput{TableName: &t.name, Item: item}); err != nil {
			return model.TryResult{}, err
		}
		body, ct := textOrJSON(input)
		return model.TryResult{OK: true, Status: "Item saved", Body: body, ContentType: ct}, nil

	case KindStream + "/" + ActPut:
		out, err := kinesis.NewFromConfig(cfg).PutRecord(ctx, &kinesis.PutRecordInput{StreamName: &t.name, PartitionKey: aws.String("nimbo"), Data: []byte(input)})
		if err != nil {
			return model.TryResult{}, err
		}
		return jsonResult("Record added", map[string]any{"shardId": aws.ToString(out.ShardId), "sequenceNumber": aws.ToString(out.SequenceNumber)}), nil

	case KindEventBus + "/" + ActPut:
		var in struct {
			Source     string          `json:"source"`
			DetailType string          `json:"detail-type"`
			Detail     json.RawMessage `json:"detail"`
		}
		if err := json.Unmarshal([]byte(input), &in); err != nil || in.Source == "" {
			return model.TryResult{Error: `Input must be {"source": "...", "detail-type": "...", "detail": {...}}.`}, nil
		}
		detail := string(in.Detail)
		if detail == "" || detail == "null" {
			detail = "{}"
		}
		out, err := eventbridge.NewFromConfig(cfg).PutEvents(ctx, &eventbridge.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{{
			EventBusName: &t.name, Source: &in.Source, DetailType: aws.String(orDefault(in.DetailType, "Example")), Detail: &detail,
		}}})
		if err != nil {
			return model.TryResult{}, err
		}
		if out.FailedEntryCount > 0 && len(out.Entries) > 0 {
			return model.TryResult{Error: aws.ToString(out.Entries[0].ErrorCode) + ": " + aws.ToString(out.Entries[0].ErrorMessage)}, nil
		}
		id := ""
		if len(out.Entries) > 0 {
			id = aws.ToString(out.Entries[0].EventId)
		}
		return jsonResult("Event sent", map[string]any{"eventId": id}), nil

	case KindStateMachine + "/" + ActStart:
		c := sfn.NewFromConfig(cfg)
		out, err := c.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: &t.arn, Input: &input})
		if err != nil {
			return model.TryResult{}, err
		}
		select {
		case <-ctx.Done():
			return model.TryResult{}, ctx.Err()
		case <-time.After(time.Second):
		}
		d, err := c.DescribeExecution(ctx, &sfn.DescribeExecutionInput{ExecutionArn: out.ExecutionArn})
		if err != nil {
			return model.TryResult{}, err
		}
		v := map[string]any{"executionArn": aws.ToString(out.ExecutionArn), "status": string(d.Status)}
		if o := aws.ToString(d.Output); o != "" {
			var parsed any
			if json.Unmarshal([]byte(o), &parsed) == nil {
				v["output"] = parsed
			} else {
				v["output"] = o
			}
		}
		if d.Error != nil {
			v["error"], v["cause"] = aws.ToString(d.Error), aws.ToString(d.Cause)
		}
		res := jsonResult("Execution "+strings.ToLower(string(d.Status)), v)
		if d.Status == "FAILED" || d.Status == "TIMED_OUT" || d.Status == "ABORTED" {
			res.OK = false
			res.Error = "The execution ended with status " + string(d.Status) + "."
		}
		return res, nil

	case KindSecret + "/" + ActGet:
		out, err := secretsmanager.NewFromConfig(cfg).GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: &t.arn})
		if err != nil {
			return model.TryResult{}, err
		}
		if out.SecretString == nil {
			return model.TryResult{OK: true, Status: "Binary secret", Body: base64.StdEncoding.EncodeToString(out.SecretBinary), ContentType: "text/plain"}, nil
		}
		body, ct := textOrJSON(*out.SecretString)
		return model.TryResult{OK: true, Status: "Secret value", Body: body, ContentType: ct}, nil

	case KindParameter + "/" + ActGet:
		out, err := ssm.NewFromConfig(cfg).GetParameter(ctx, &ssm.GetParameterInput{Name: &t.name, WithDecryption: aws.Bool(true)})
		if err != nil {
			return model.TryResult{}, err
		}
		p := out.Parameter
		return jsonResult("Parameter value", map[string]any{"name": aws.ToString(p.Name), "type": string(p.Type), "value": aws.ToString(p.Value), "version": p.Version}), nil
	}
	return model.TryResult{}, badRequest("action %q is not available for %s", action, e.Kind)
}

func s3Client(cfg aws.Config) *s3.Client {
	return s3.NewFromConfig(cfg, func(o *s3.Options) { o.UsePathStyle = true })
}
