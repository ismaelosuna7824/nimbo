package loadtest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AWS operations supported by the engine.
const (
	OpSQSSend        = "sqs:SendMessage"
	OpSQSReceive     = "sqs:ReceiveMessage"
	OpS3Put          = "s3:PutObject"
	OpS3Get          = "s3:GetObject"
	OpDynamoPut      = "dynamodb:PutItem"
	OpDynamoGet      = "dynamodb:GetItem"
	OpLambdaInvoke   = "lambda:Invoke"
	OpSNSPublish     = "sns:Publish"
	OpKinesisPut     = "kinesis:PutRecord"
	OpEventsPut      = "events:PutEvents"
	OpStatesStartExe = "states:StartExecution"
)

const (
	defaultRegion  = "us-east-1"
	emulatorAcct   = "000000000000"
	defaultPayload = `{"source":"nimbo-loadtest","seq":{{seq}},"id":"{{uuid}}","sentAt":"{{now}}"}`
	defaultPutKey  = "loadtest/{{uuid}}.json"
	defaultGetKey  = "loadtest/sample.json"
)

type callInfoKey struct{}

// callInfo receives the HTTP status and response size of one SDK call.
// Retries are disabled, so each call makes exactly one HTTP attempt on the
// calling goroutine.
type callInfo struct {
	status int
	bytes  int64
}

type countingDoer struct{ c *http.Client }

func (d countingDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := d.c.Do(req)
	if err != nil {
		return resp, err
	}
	if ci, ok := req.Context().Value(callInfoKey{}).(*callInfo); ok {
		ci.status = resp.StatusCode
		resp.Body = &countingBody{ReadCloser: resp.Body, n: &ci.bytes}
	}
	return resp, nil
}

type countingBody struct {
	io.ReadCloser
	n *int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	*b.n += int64(n)
	return n, err
}

// opError is a failure reported in a successful API response (Lambda
// function errors, PutEvents failed entries).
type opError struct{ kind, msg string }

func (e *opError) Error() string { return e.msg }

type awsExec struct {
	httpc *http.Client
	setup func(ctx context.Context) error
	call  func(ctx context.Context, seq int64) error
}

func (a *awsExec) preflight(ctx context.Context) error {
	if a.setup != nil {
		if err := a.setup(ctx); err != nil {
			return err
		}
	}
	return a.call(ctx, 0)
}

func (a *awsExec) do(ctx context.Context, seq int64) outcome {
	ci := &callInfo{}
	err := a.call(context.WithValue(ctx, callInfoKey{}, ci), seq)
	o := outcome{bytes: ci.bytes}
	if ci.status != 0 {
		o.status = strconv.Itoa(ci.status)
	}
	if err != nil {
		o.errKind = classifyAWS(err)
	}
	return o
}

func (a *awsExec) close() { a.httpc.CloseIdleConnections() }

func classifyAWS(err error) string {
	var oe *opError
	if errors.As(err, &oe) {
		return oe.kind
	}
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() != "" {
		return "aws:" + api.ErrorCode()
	}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		switch code := re.HTTPStatusCode(); {
		case code >= 500:
			return errHTTP5xx
		case code >= 400:
			return errHTTP4xx
		}
	}
	return classify(err)
}

type arn struct{ service, region, account, resource string }

func parseARN(s string) (arn, bool) {
	p := strings.SplitN(s, ":", 6)
	if len(p) != 6 || p[0] != "arn" {
		return arn{}, false
	}
	return arn{service: p[2], region: p[3], account: p[4], resource: p[5]}, true
}

// resourceRegion takes the region from an ARN or an AWS-style SQS queue URL;
// "" when the resource does not name one.
func resourceRegion(resource string) string {
	if a, ok := parseARN(resource); ok && a.region != "" {
		return a.region
	}
	if u, err := url.Parse(resource); err == nil && u.Host != "" {
		labels := strings.Split(u.Hostname(), ".")
		if len(labels) >= 3 && labels[0] == "sqs" {
			return labels[1]
		}
		if len(labels) >= 3 && labels[1] == "queue" {
			return labels[0]
		}
	}
	return ""
}

// arnName returns the part of an ARN resource after prefix ("table/",
// "stream/", "event-bus/", "function:"), or s itself when s is not an ARN.
func arnName(s, prefix string) string {
	a, ok := parseARN(s)
	if !ok {
		return s
	}
	return strings.TrimPrefix(a.resource, prefix)
}

// nameARN returns a setup step pointing *dst at the ARN of a resource given
// by name, once the account is known.
func (env awsEnv) nameARN(dst *string, service, region, resource string) func(context.Context) error {
	return func(ctx context.Context) error {
		partition, account, err := env.identity(ctx)
		if err != nil {
			return err
		}
		*dst = fmt.Sprintf("arn:%s:%s:%s:%s:%s", partition, service, region, account, resource)
		return nil
	}
}

// bucketRegion asks S3 where a bucket lives; "" when it cannot tell.
func bucketRegion(ctx context.Context, c *s3.Client, bucket string) string {
	out, err := c.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &bucket})
	if err == nil {
		return aws.ToString(out.BucketRegion)
	}
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) && re.Response != nil {
		return re.Response.Header.Get("X-Amz-Bucket-Region")
	}
	return ""
}

// awsEnv is what differs between the sandbox emulator and a real account.
type awsEnv struct {
	// identity returns the partition and account used to build ARNs of
	// resources given by name.
	identity func(ctx context.Context) (partition, account string, err error)
	// realS3 addresses buckets virtual-hosted style in their own region
	// (real accounts); the sandbox uses path style in a single region.
	realS3 bool
}

var sandboxEnv = awsEnv{identity: func(context.Context) (string, string, error) { return "aws", emulatorAcct, nil }}

// SandboxAWSConfig is the SDK configuration for a sandbox AWS emulator at
// endpoint: static test credentials, no retries (each call is exactly one
// HTTP attempt) and checksums only where required. An empty region means
// us-east-1. httpc may be nil for the SDK's default client.
func SandboxAWSConfig(endpoint, region string, httpc aws.HTTPClient) aws.Config {
	cfg := aws.Config{
		Region:                     orDefault(region, defaultRegion),
		Credentials:                credentials.NewStaticCredentialsProvider("test", "test", ""),
		Retryer:                    func() aws.Retryer { return aws.NopRetryer{} },
		BaseEndpoint:               aws.String(endpoint),
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired,
	}
	if httpc != nil {
		cfg.HTTPClient = httpc
	}
	return cfg
}

// newAWSExec builds an executor against the sandbox's AWS emulator.
func newAWSExec(t *model.AWSTarget, endpoint string, workers int) (*awsExec, error) {
	resource := strings.TrimSpace(t.Resource)
	if resource == "" {
		return nil, fmt.Errorf("%s needs a resource", t.Operation)
	}
	httpc := &http.Client{Transport: newTransport(workers)}
	cfg := SandboxAWSConfig(endpoint, resourceRegion(resource), countingDoer{c: httpc})
	return buildAWSExec(t, resource, cfg, httpc, sandboxEnv)
}

// newAWSConnExec builds an executor against the real account of an AWS
// connection: credentials and region come from its profile (the default
// chain when Profile is ""), an ARN's region wins over the connection's.
// Endpoints are the SDK's own, so AWS_ENDPOINT_URL is honored.
func newAWSConnExec(ctx context.Context, t *model.AWSTarget, c model.Connection, workers int) (*awsExec, error) {
	resource := strings.TrimSpace(t.Resource)
	if resource == "" {
		return nil, fmt.Errorf("%s needs a resource", t.Operation)
	}
	httpc := &http.Client{Transport: newTransport(workers)}
	cfg, err := loadAWSConfig(ctx, c, countingDoer{c: httpc})
	if err != nil {
		return nil, err
	}
	if r := resourceRegion(resource); r != "" {
		cfg.Region = r
	}
	cfg.Retryer = func() aws.Retryer { return aws.NopRetryer{} }
	cfg.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	cfg.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	env := awsEnv{realS3: true, identity: func(ctx context.Context) (string, string, error) {
		out, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return "", "", fmt.Errorf("resolve the account: %w", err)
		}
		partition := "aws"
		if p := strings.SplitN(aws.ToString(out.Arn), ":", 3); len(p) == 3 && p[0] == "arn" && p[1] != "" {
			partition = p[1]
		}
		return partition, aws.ToString(out.Account), nil
	}}
	return buildAWSExec(t, resource, cfg, httpc, env)
}

// loadAWSConfig loads the shared config of an AWS connection. httpc may be
// nil for the SDK's default client.
func loadAWSConfig(ctx context.Context, c model.Connection, httpc aws.HTTPClient) (aws.Config, error) {
	var opts []func(*config.LoadOptions) error
	if c.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(c.Profile))
	}
	if c.Region != "" {
		opts = append(opts, config.WithRegion(c.Region))
	}
	if httpc != nil {
		opts = append(opts, config.WithHTTPClient(httpc))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return cfg, fmt.Errorf("load AWS profile %q: %w", c.Profile, err)
	}
	if cfg.Region == "" {
		cfg.Region = defaultRegion
	}
	return cfg, nil
}

func buildAWSExec(t *model.AWSTarget, resource string, cfg aws.Config, httpc *http.Client, env awsEnv) (*awsExec, error) {
	region := cfg.Region
	payload := compileTemplate(orDefault(t.Payload, defaultPayload))
	a := &awsExec{httpc: httpc}

	switch t.Operation {
	case OpSQSSend, OpSQSReceive:
		c := sqs.NewFromConfig(cfg)
		queueURL := resource
		if q, ok := parseARN(resource); ok {
			a.setup = func(ctx context.Context) error {
				out, err := c.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(q.resource), QueueOwnerAWSAccountId: nonEmpty(q.account)})
				if err != nil {
					return fmt.Errorf("resolve queue %s: %w", resource, err)
				}
				queueURL = aws.ToString(out.QueueUrl)
				return nil
			}
		}
		if t.Operation == OpSQSSend {
			a.call = func(ctx context.Context, seq int64) error {
				_, err := c.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: &queueURL, MessageBody: aws.String(payload.render(seq))})
				return err
			}
		} else {
			a.call = func(ctx context.Context, _ int64) error {
				_, err := c.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: &queueURL, MaxNumberOfMessages: 10})
				return err
			}
		}

	case OpS3Put, OpS3Get:
		pathStyle := func(o *s3.Options) { o.UsePathStyle = !env.realS3 }
		c := s3.NewFromConfig(cfg, pathStyle)
		bucket := arnName(resource, "")
		// Real buckets answer only in their own region.
		locate := func(ctx context.Context) error {
			if r := bucketRegion(ctx, c, bucket); env.realS3 && r != "" && r != cfg.Region {
				c = s3.NewFromConfig(cfg, pathStyle, func(o *s3.Options) { o.Region = r })
			}
			return nil
		}
		if t.Operation == OpS3Put {
			key := compileTemplate(orDefault(t.Key, defaultPutKey))
			if env.realS3 {
				a.setup = locate
			}
			a.call = func(ctx context.Context, seq int64) error {
				_, err := c.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: aws.String(key.render(seq)), Body: strings.NewReader(payload.render(seq))})
				return err
			}
			break
		}
		key := compileTemplate(orDefault(t.Key, defaultGetKey))
		a.setup = func(ctx context.Context) error {
			if env.realS3 {
				_ = locate(ctx)
			}
			k := key.render(0)
			_, err := c.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &bucket, Key: &k})
			var re *smithyhttp.ResponseError
			if err == nil || !errors.As(err, &re) || re.HTTPStatusCode() != http.StatusNotFound {
				return err
			}
			_, err = c.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &k, Body: strings.NewReader(payload.render(0))})
			if err != nil {
				return fmt.Errorf("upload s3://%s/%s for reading: %w", bucket, k, err)
			}
			return nil
		}
		a.call = func(ctx context.Context, seq int64) error {
			out, err := c.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: aws.String(key.render(seq))})
			if err != nil {
				return err
			}
			defer out.Body.Close()
			_, err = io.Copy(io.Discard, out.Body)
			return err
		}

	case OpDynamoPut, OpDynamoGet:
		c := dynamodb.NewFromConfig(cfg)
		table := arnName(resource, "table/")
		if t.Operation == OpDynamoPut {
			if t.Payload == "" {
				return nil, errors.New("dynamodb:PutItem needs a payload: a JSON object item that includes the table key")
			}
			item, err := newItemTemplate(t.Payload, "payload")
			if err != nil {
				return nil, err
			}
			a.call = func(ctx context.Context, seq int64) error {
				it, err := item.render(seq)
				if err != nil {
					return &opError{kind: errInvalidPayload, msg: err.Error()}
				}
				_, err = c.PutItem(ctx, &dynamodb.PutItemInput{TableName: &table, Item: it})
				return err
			}
			break
		}
		if t.Key == "" {
			return nil, errors.New("dynamodb:GetItem needs a key: a JSON object with the table key attributes")
		}
		key, err := newItemTemplate(t.Key, "key")
		if err != nil {
			return nil, err
		}
		if key.fixed != nil {
			a.setup = func(ctx context.Context) error {
				out, err := c.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: key.fixed})
				if err != nil || len(out.Item) > 0 {
					return err
				}
				item := map[string]ddbtypes.AttributeValue{}
				if t.Payload != "" {
					extra, err := newItemTemplate(t.Payload, "payload")
					if err != nil {
						return err
					}
					if item, err = extra.render(0); err != nil {
						return err
					}
				}
				for k, v := range key.fixed {
					item[k] = v
				}
				if _, err := c.PutItem(ctx, &dynamodb.PutItemInput{TableName: &table, Item: item}); err != nil {
					return fmt.Errorf("seed item for reading: %w", err)
				}
				return nil
			}
		}
		a.call = func(ctx context.Context, seq int64) error {
			k, err := key.render(seq)
			if err != nil {
				return &opError{kind: errInvalidPayload, msg: err.Error()}
			}
			_, err = c.GetItem(ctx, &dynamodb.GetItemInput{TableName: &table, Key: k})
			return err
		}

	case OpLambdaInvoke:
		if err := validJSON(payload, "payload"); err != nil {
			return nil, err
		}
		c := lambda.NewFromConfig(cfg)
		name, qualifier, _ := strings.Cut(arnName(resource, "function:"), ":")
		a.call = func(ctx context.Context, seq int64) error {
			out, err := c.Invoke(ctx, &lambda.InvokeInput{FunctionName: &name, Qualifier: nonEmpty(qualifier), Payload: []byte(payload.render(seq))})
			if err != nil {
				return err
			}
			if out.FunctionError != nil {
				return &opError{kind: errFunction, msg: fmt.Sprintf("function error %s: %s", *out.FunctionError, truncate(out.Payload, 300))}
			}
			return nil
		}

	case OpSNSPublish:
		c := sns.NewFromConfig(cfg)
		topic := resource
		if _, ok := parseARN(resource); !ok {
			a.setup = env.nameARN(&topic, "sns", region, resource)
		}
		a.call = func(ctx context.Context, seq int64) error {
			_, err := c.Publish(ctx, &sns.PublishInput{TopicArn: &topic, Message: aws.String(payload.render(seq))})
			return err
		}

	case OpKinesisPut:
		c := kinesis.NewFromConfig(cfg)
		stream := arnName(resource, "stream/")
		key := compileTemplate(orDefault(t.Key, "{{uuid}}"))
		a.call = func(ctx context.Context, seq int64) error {
			_, err := c.PutRecord(ctx, &kinesis.PutRecordInput{StreamName: &stream, PartitionKey: aws.String(key.render(seq)), Data: []byte(payload.render(seq))})
			return err
		}

	case OpEventsPut:
		if err := validJSON(payload, "payload"); err != nil {
			return nil, err
		}
		c := eventbridge.NewFromConfig(cfg)
		bus := arnName(resource, "event-bus/")
		a.call = func(ctx context.Context, seq int64) error {
			out, err := c.PutEvents(ctx, &eventbridge.PutEventsInput{Entries: []ebtypes.PutEventsRequestEntry{{
				EventBusName: &bus,
				Source:       aws.String("nimbo.loadtest"),
				DetailType:   aws.String("LoadTest"),
				Detail:       aws.String(payload.render(seq)),
			}}})
			if err != nil {
				return err
			}
			if out.FailedEntryCount > 0 && len(out.Entries) > 0 {
				e := out.Entries[0]
				return &opError{kind: "aws:" + aws.ToString(e.ErrorCode), msg: aws.ToString(e.ErrorMessage)}
			}
			return nil
		}

	case OpStatesStartExe:
		if err := validJSON(payload, "payload"); err != nil {
			return nil, err
		}
		c := sfn.NewFromConfig(cfg)
		machine := resource
		if _, ok := parseARN(resource); !ok {
			a.setup = env.nameARN(&machine, "states", region, "stateMachine:"+resource)
		}
		a.call = func(ctx context.Context, seq int64) error {
			_, err := c.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: &machine, Input: aws.String(payload.render(seq))})
			return err
		}

	default:
		return nil, fmt.Errorf("unsupported AWS operation %q", t.Operation)
	}
	return a, nil
}

// itemTemplate renders a DynamoDB item or key from a plain JSON object:
// strings become S, numbers N, booleans BOOL, null NULL, arrays L and
// objects M.
type itemTemplate struct {
	t     template
	fixed map[string]ddbtypes.AttributeValue
}

func newItemTemplate(s, what string) (itemTemplate, error) {
	it := itemTemplate{t: compileTemplate(s)}
	m, err := DynamoItem(it.t.render(1))
	if err != nil {
		return it, fmt.Errorf("%s must be a JSON object: %w", what, err)
	}
	if it.t.static() {
		it.fixed = m
	}
	return it, nil
}

func (it itemTemplate) render(seq int64) (map[string]ddbtypes.AttributeValue, error) {
	if it.fixed != nil {
		return it.fixed, nil
	}
	return DynamoItem(it.t.render(seq))
}

// DynamoItem converts a plain JSON object into a DynamoDB item: strings
// become S, numbers N, booleans BOOL, null NULL, arrays L and objects M.
func DynamoItem(s string) (map[string]ddbtypes.AttributeValue, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("got null")
	}
	out := make(map[string]ddbtypes.AttributeValue, len(m))
	for k, v := range m {
		out[k] = dynamoValue(v)
	}
	return out, nil
}

func dynamoValue(v any) ddbtypes.AttributeValue {
	switch x := v.(type) {
	case string:
		return &ddbtypes.AttributeValueMemberS{Value: x}
	case json.Number:
		return &ddbtypes.AttributeValueMemberN{Value: x.String()}
	case bool:
		return &ddbtypes.AttributeValueMemberBOOL{Value: x}
	case []any:
		l := make([]ddbtypes.AttributeValue, len(x))
		for i, e := range x {
			l[i] = dynamoValue(e)
		}
		return &ddbtypes.AttributeValueMemberL{Value: l}
	case map[string]any:
		m := make(map[string]ddbtypes.AttributeValue, len(x))
		for k, e := range x {
			m[k] = dynamoValue(e)
		}
		return &ddbtypes.AttributeValueMemberM{Value: m}
	default:
		return &ddbtypes.AttributeValueMemberNULL{Value: true}
	}
}

func validJSON(t template, what string) error {
	if !json.Valid([]byte(t.render(1))) {
		return fmt.Errorf("%s must be valid JSON", what)
	}
	return nil
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func truncate(b []byte, n int) string {
	b = bytes.TrimSpace(b)
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
