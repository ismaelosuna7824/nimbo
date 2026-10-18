//go:build docker

package loadtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/kinesis"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// startFloci runs a throwaway floci/floci container and returns its host URL.
func startFloci(t *testing.T) string {
	t.Helper()
	var b [3]byte
	_, _ = rand.Read(b[:])
	name := "nimbo-test-floci-" + hex.EncodeToString(b[:])
	if out, err := exec.Command("docker", "run", "-d", "--name", name, "-p", "127.0.0.1::4566", "floci/floci:latest").CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("docker rm: %v: %s", err, out)
		}
	})
	out, err := exec.Command("docker", "port", name, "4566/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	base := "http://" + strings.TrimSpace(strings.Split(string(out), "\n")[0])
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		var init struct {
			Completed struct{ Ready bool } `json:"completed"`
		}
		if resp, err := http.Get(base + "/_floci/init"); err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&init)
			resp.Body.Close()
			if init.Completed.Ready {
				return base
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("floci did not become ready")
	return ""
}

func awsConfig(base, region string) aws.Config {
	return aws.Config{
		Region:       region,
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		BaseEndpoint: aws.String(base),
	}
}

func TestFlociAWSTargets(t *testing.T) {
	base := startFloci(t)
	ctx := context.Background()
	east, west := awsConfig(base, "us-east-1"), awsConfig(base, "eu-west-1")

	// Queue and table live in eu-west-1 and are addressed by ARN, which
	// exercises region selection and queue URL resolution.
	q, err := sqs.NewFromConfig(west).CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("lt-queue")})
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := sqs.NewFromConfig(west).GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn}})
	if err != nil {
		t.Fatal(err)
	}
	queueARN := attrs.Attributes["QueueArn"]
	table, err := dynamodb.NewFromConfig(west).CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("lt-table"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	tableARN := aws.ToString(table.TableDescription.TableArn)
	if _, err := s3.NewFromConfig(east, func(o *s3.Options) { o.UsePathStyle = true }).CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String("lt-bucket")}); err != nil {
		t.Fatal(err)
	}
	topic, err := sns.NewFromConfig(east).CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("lt-topic")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kinesis.NewFromConfig(east).CreateStream(ctx, &kinesis.CreateStreamInput{StreamName: aws.String("lt-stream"), ShardCount: aws.Int32(1)}); err != nil {
		t.Fatal(err)
	}
	bus, err := eventbridge.NewFromConfig(east).CreateEventBus(ctx, &eventbridge.CreateEventBusInput{Name: aws.String("lt-bus")})
	if err != nil {
		t.Fatal(err)
	}
	machine, err := sfn.NewFromConfig(east).CreateStateMachine(ctx, &sfn.CreateStateMachineInput{
		Name:       aws.String("lt-machine"),
		Definition: aws.String(`{"StartAt":"Done","States":{"Done":{"Type":"Pass","End":true}}}`),
		RoleArn:    aws.String("arn:aws:iam::000000000000:role/lt"),
	})
	if err != nil {
		t.Fatal(err)
	}

	item, key, _ := dynamoTemplates(&model.DeployedResource{Values: map[string]any{
		"hash_key": "pk", "attribute": []any{map[string]any{"name": "pk", "type": "S"}},
	}})
	type tc struct {
		target   model.AWSTarget
		sec, rps int
	}
	batches := [][]tc{
		{ // the acceptance smoke runs, concurrently
			{model.AWSTarget{Operation: OpSQSSend, Resource: queueARN}, 5, 20},
			{model.AWSTarget{Operation: OpS3Put, Resource: "lt-bucket", Key: defaultPutKey}, 5, 20},
			{model.AWSTarget{Operation: OpDynamoPut, Resource: tableARN, Payload: item}, 5, 20},
		},
		{
			{model.AWSTarget{Operation: OpSQSReceive, Resource: queueARN}, 2, 10},
			{model.AWSTarget{Operation: OpS3Get, Resource: "lt-bucket", Key: defaultGetKey}, 2, 10},
			{model.AWSTarget{Operation: OpDynamoGet, Resource: tableARN, Key: key, Payload: item}, 2, 10},
		},
		{
			{model.AWSTarget{Operation: OpSNSPublish, Resource: aws.ToString(topic.TopicArn)}, 2, 10},
			{model.AWSTarget{Operation: OpKinesisPut, Resource: "lt-stream"}, 2, 10},
			{model.AWSTarget{Operation: OpEventsPut, Resource: aws.ToString(bus.EventBusArn)}, 2, 10},
		},
		{
			{model.AWSTarget{Operation: OpStatesStartExe, Resource: aws.ToString(machine.StateMachineArn)}, 2, 10},
		},
	}

	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Resolver: fixedResolver{model.CloudAWS: base}})
	for _, batch := range batches {
		ids := make([]string, len(batch))
		for i, c := range batch {
			target := c.target
			run, err := e.Start(ctx, testWF, model.LoadTestSpec{
				Target:  model.LoadTarget{Kind: model.TargetAWS, Label: target.Operation, AWS: &target},
				Profile: model.LoadProfile{Mode: model.ModeRPS, Stages: hold(c.sec, c.rps)},
			})
			if err != nil {
				t.Fatalf("%s: %v", target.Operation, err)
			}
			ids[i] = run.ID
		}
		for i, c := range batch {
			done := w.wait(t, ids[i], time.Duration(c.sec)*time.Second+30*time.Second)
			if done.Status != model.LoadPassed {
				t.Errorf("%s: status %s: %s", c.target.Operation, done.Status, done.Error)
				continue
			}
			s := done.Summary
			t.Logf("%-22s target=%d rps achieved=%.1f rps requests=%d errors=%d p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms bytesIn=%d codes=%v kinds=%v",
				c.target.Operation, c.rps, s.RPS, s.Requests, s.Errors, s.Latency.P50, s.Latency.P95, s.Latency.P99, s.Latency.Max, s.BytesIn, s.StatusCodes, s.ErrorKinds)
			if s.Errors != 0 || s.RPS < 0.9*float64(c.rps) || s.RPS > 1.1*float64(c.rps) {
				t.Errorf("%s: %d errors %v, %.1f rps", c.target.Operation, s.Errors, s.ErrorKinds, s.RPS)
			}
		}
	}

	// The writes actually landed.
	got, err := s3.NewFromConfig(east, func(o *s3.Options) { o.UsePathStyle = true }).ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String("lt-bucket"), Prefix: aws.String("loadtest/")})
	if err != nil {
		t.Fatal(err)
	}
	if n := aws.ToInt32(got.KeyCount); n < 100 {
		t.Errorf("bucket holds %d load test objects, want >= 100", n)
	}
	scan, err := dynamodb.NewFromConfig(west).Scan(ctx, &dynamodb.ScanInput{TableName: aws.String("lt-table"), Select: ddbtypes.SelectCount})
	if err != nil {
		t.Fatal(err)
	}
	if scan.Count < 100 {
		t.Errorf("table holds %d items, want >= 100", scan.Count)
	}
}
