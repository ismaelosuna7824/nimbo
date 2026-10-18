//go:build docker

package loadtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// evilArg must reach the database as a plain value.
const evilArg = `x'); DROP TABLE "Items"; DROP TABLE Items; -- `

// startContainer runs a throwaway nimbo-test-* container publishing port on
// a random 127.0.0.1 port and returns that port.
func startContainer(t *testing.T, kind, image string, port int, env []string, cmd ...string) int {
	t.Helper()
	var b [3]byte
	_, _ = rand.Read(b[:])
	name := "nimbo-test-" + kind + "-" + hex.EncodeToString(b[:])
	args := []string{"run", "-d", "--name", name, "-p", fmt.Sprintf("127.0.0.1::%d", port)}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(append(args, image), cmd...)
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v: %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("docker", "rm", "-f", "-v", name).CombinedOutput(); err != nil {
			t.Errorf("docker rm: %v: %s", err, out)
		}
	})
	out, err := exec.Command("docker", "port", name, fmt.Sprintf("%d/tcp", port)).Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	_, p, _ := strings.Cut(strings.TrimSpace(strings.Split(string(out), "\n")[0]), ":")
	hp, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("docker port output %q", out)
	}
	return hp
}

// waitReady retries CheckConnection until it succeeds.
func waitReady(t *testing.T, c model.Connection, limit time.Duration) model.ConnectionCheck {
	t.Helper()
	deadline := time.Now().Add(limit)
	for {
		check := CheckConnection(context.Background(), c)
		if check.OK {
			t.Logf("%s check: %+v", c.Kind, check)
			return check
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never became ready: %+v", c.Kind, check)
		}
		time.Sleep(time.Second)
	}
}

// expectAuthFailure checks that a wrong password is reported without
// leaking it.
func expectAuthFailure(t *testing.T, c model.Connection, secret string) {
	t.Helper()
	check := CheckConnection(context.Background(), c)
	t.Logf("%s wrong password: %+v", c.Kind, check)
	if check.OK || check.Message != "Authentication failed" {
		t.Errorf("wrong password: %+v, want not ok with Authentication failed", check)
	}
	if strings.Contains(check.Detail+check.Message, secret) {
		t.Errorf("check leaked the password: %+v", check)
	}
}

// runTargets runs every target against its connection for sec seconds at
// rps, three at a time (the engine's global cap), and requires zero errors.
func runTargets(t *testing.T, conns connections, targets []model.LoadTarget, sec, rps int) {
	t.Helper()
	w := newWatcher()
	e := NewEngine(EngineConfig{Home: t.TempDir(), Events: w, Connections: conns})
	for start := 0; start < len(targets); start += maxConcurrentRuns {
		batch := targets[start:min(start+maxConcurrentRuns, len(targets))]
		ids := make([]string, len(batch))
		for i, target := range batch {
			run, err := e.StartConnection(context.Background(), target.ConnectionID, model.LoadTestSpec{
				Target:          target,
				Profile:         model.LoadProfile{Mode: model.ModeRPS, Stages: hold(sec, rps)},
				ConfirmExternal: true,
			})
			if err != nil {
				t.Fatalf("%s: %v", target.Label, err)
			}
			ids[i] = run.ID
		}
		for i, target := range batch {
			done := w.wait(t, ids[i], time.Duration(sec)*time.Second+45*time.Second)
			if done.Status != model.LoadPassed || done.Summary == nil {
				t.Errorf("%s: status %s: %s", target.Label, done.Status, done.Error)
				continue
			}
			s := done.Summary
			t.Logf("%-40s rps=%.1f requests=%d errors=%d p50=%.2fms p95=%.2fms p99=%.2fms bytesIn=%d kinds=%v",
				target.Label, s.RPS, s.Requests, s.Errors, s.Latency.P50, s.Latency.P95, s.Latency.P99, s.BytesIn, s.ErrorKinds)
			if s.Errors != 0 || s.RPS < 0.9*float64(rps) {
				t.Errorf("%s: %d errors %v, %.1f rps", target.Label, s.Errors, s.ErrorKinds, s.RPS)
			}
			if !done.External || done.ConnectionID != target.ConnectionID {
				t.Errorf("%s: external=%v connection=%q", target.Label, done.External, done.ConnectionID)
			}
		}
	}
}

func suggest(t *testing.T, c model.Connection) []model.LoadTarget {
	t.Helper()
	targets, err := SuggestConnection(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		t.Logf("suggested %-45q %s", tg.Label, targetDesc(tg))
		if tg.ConnectionID != c.ID {
			t.Errorf("%s: connectionId %q", tg.Label, tg.ConnectionID)
		}
	}
	return targets
}

func targetDesc(t model.LoadTarget) string {
	switch {
	case t.SQL != nil:
		return t.SQL.Query
	case t.Redis != nil:
		return t.Redis.Command
	case t.Mongo != nil:
		return t.Mongo.Operation + " " + t.Mongo.Database + "." + t.Mongo.Collection + " " + t.Mongo.Document
	case t.AWS != nil:
		return t.AWS.Operation + " " + t.AWS.Resource + " " + t.AWS.Key
	}
	return ""
}

func findTarget(t *testing.T, targets []model.LoadTarget, match func(model.LoadTarget) bool, what string) model.LoadTarget {
	t.Helper()
	for _, tg := range targets {
		if match(tg) {
			return tg
		}
	}
	t.Fatalf("no suggestion %s", what)
	return model.LoadTarget{}
}

func TestPostgresConnection(t *testing.T) {
	port := startContainer(t, "postgres", "postgres:16-alpine", 5432, []string{"POSTGRES_USER=bench", "POSTGRES_PASSWORD=benchpw", "POSTGRES_DB=bench"})
	c := model.Connection{ID: "pg", Name: "pg", Kind: model.ConnPostgres, Host: "127.0.0.1", Port: port, Username: "bench", Password: "benchpw", Database: "bench"}
	check := waitReady(t, c, 90*time.Second)
	if !strings.HasPrefix(check.Message, "PostgreSQL 16") {
		t.Errorf("message %q", check.Message)
	}

	ctx := context.Background()
	cfg, _ := postgresConfig(c)
	db, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	if _, err := db.Exec(ctx, `CREATE TABLE "Items" (id serial PRIMARY KEY, name text NOT NULL);
		INSERT INTO "Items"(name) SELECT 'item ' || g FROM generate_series(1, 20) g`); err != nil {
		t.Fatal(err)
	}

	targets := suggest(t, c)
	if targets[0].SQL.Query != "SELECT 1" {
		t.Errorf("first suggestion %q", targets[0].SQL.Query)
	}
	read := findTarget(t, targets, func(tg model.LoadTarget) bool { return tg.SQL.Query == `SELECT * FROM "public"."Items" LIMIT 10` }, "reading Items")
	insert := model.LoadTarget{Kind: model.TargetSQL, ConnectionID: "pg", Label: "insert evil arg",
		SQL: &model.SQLTarget{Query: `INSERT INTO "Items"(name) VALUES ($1 || $2)`, Args: []string{evilArg, "{{seq}}"}, Mode: "exec"}}
	runTargets(t, connections{"pg": c}, []model.LoadTarget{targets[0], read, insert}, 5, 50)

	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM "Items" WHERE name LIKE $1`, evilArg+"%").Scan(&n); err != nil {
		t.Fatalf("table gone or unreadable after the run: %v", err)
	}
	if n < 225 {
		t.Errorf("%d rows hold the literal argument, want >= 225", n)
	}

	bad := model.Connection{ID: "pg-bad", Kind: model.ConnPostgres, URI: fmt.Sprintf("postgres://bench:wrongpass-123@127.0.0.1:%d/bench?sslmode=disable", port)}
	expectAuthFailure(t, bad, "wrongpass-123")
}

func TestMySQLConnection(t *testing.T) {
	port := startContainer(t, "mysql", "mysql:8", 3306, []string{"MYSQL_ROOT_PASSWORD=rootpw", "MYSQL_DATABASE=bench", "MYSQL_USER=bench", "MYSQL_PASSWORD=benchpw"})
	c := model.Connection{ID: "my", Name: "my", Kind: model.ConnMySQL, Host: "127.0.0.1", Port: port, Username: "bench", Password: "benchpw", Database: "bench"}
	check := waitReady(t, c, 180*time.Second)
	if !strings.HasPrefix(check.Message, "MySQL 8") {
		t.Errorf("message %q", check.Message)
	}

	cfg, _ := mysqlConfig(c)
	conn, _ := mysql.NewConnector(cfg)
	db := sql.OpenDB(conn)
	defer db.Close()
	for _, q := range []string{
		"CREATE TABLE `Items` (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(200) NOT NULL)",
		"INSERT INTO `Items`(name) VALUES ('a'),('b'),('c'),('d'),('e')",
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	targets := suggest(t, c)
	read := findTarget(t, targets, func(tg model.LoadTarget) bool { return tg.SQL.Query == "SELECT * FROM `Items` LIMIT 10" }, "reading Items")
	insert := model.LoadTarget{Kind: model.TargetSQL, ConnectionID: "my", Label: "insert evil arg",
		SQL: &model.SQLTarget{Query: "INSERT INTO `Items`(name) VALUES (CONCAT(?, ?))", Args: []string{evilArg, "{{seq}}"}, Mode: "exec"}}
	runTargets(t, connections{"my": c}, []model.LoadTarget{targets[0], read, insert}, 5, 50)

	var n int
	if err := db.QueryRow("SELECT count(*) FROM `Items` WHERE name LIKE ?", strings.ReplaceAll(evilArg, "_", `\_`)+"%").Scan(&n); err != nil {
		t.Fatalf("table gone or unreadable after the run: %v", err)
	}
	if n < 225 {
		t.Errorf("%d rows hold the literal argument, want >= 225", n)
	}

	bad := model.Connection{ID: "my-bad", Kind: model.ConnMySQL, URI: fmt.Sprintf("bench:wrongpass-123@tcp(127.0.0.1:%d)/bench", port)}
	expectAuthFailure(t, bad, "wrongpass-123")
}

func TestRedisConnection(t *testing.T) {
	port := startContainer(t, "redis", "redis:7-alpine", 6379, nil, "redis-server", "--requirepass", "benchpw")
	c := model.Connection{ID: "rd", Name: "rd", Kind: model.ConnRedis, Host: "127.0.0.1", Port: port, Password: "benchpw", Database: "2"}
	check := waitReady(t, c, 60*time.Second)
	if !strings.HasPrefix(check.Message, "Redis 7") {
		t.Errorf("message %q", check.Message)
	}

	targets := suggest(t, c)
	if len(targets) != 3 || !strings.HasSuffix(targets[2].Label, writesData) {
		t.Fatalf("suggestions %+v", targets)
	}
	runTargets(t, connections{"rd": c}, targets, 5, 50)

	client := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("127.0.0.1:%d", port), Password: "benchpw", DB: 2})
	defer client.Close()
	ctx := context.Background()
	if keys, err := client.Keys(ctx, "nimbo:bench:*").Result(); err != nil || len(keys) < 225 {
		t.Errorf("SET wrote %d keys into db 2 (%v), want >= 225", len(keys), err)
	}
	if err := client.RPush(ctx, "a list", "x").Err(); err != nil {
		t.Fatal(err)
	}
	wrong, err := newRedisExec(&model.RedisTarget{Command: `GET "a list"`}, c, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer wrong.close()
	if o := wrong.do(ctx, 1); o.errKind != "redis:WRONGTYPE" {
		t.Errorf("GET on a list: kind %q, want redis:WRONGTYPE", o.errKind)
	}

	bad := model.Connection{ID: "rd-bad", Kind: model.ConnRedis, URI: fmt.Sprintf("redis://:wrongpass-123@127.0.0.1:%d/0", port)}
	expectAuthFailure(t, bad, "wrongpass-123")
}

func TestMongoConnection(t *testing.T) {
	port := startContainer(t, "mongo", "mongo:7", 27017, []string{"MONGO_INITDB_ROOT_USERNAME=bench", "MONGO_INITDB_ROOT_PASSWORD=benchpw"})
	c := model.Connection{ID: "mg", Name: "mg", Kind: model.ConnMongo, Host: "127.0.0.1", Port: port, Username: "bench", Password: "benchpw", Database: "bench"}
	check := waitReady(t, c, 120*time.Second)
	if !strings.HasPrefix(check.Message, "MongoDB 7") {
		t.Errorf("message %q", check.Message)
	}

	ctx := context.Background()
	opts, _ := mongoOptions(c)
	client, err := mongo.Connect(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	items := client.Database("bench").Collection("items")
	if _, err := items.InsertMany(ctx, []any{bson.D{{Key: "n", Value: 1}}, bson.D{{Key: "n", Value: 2}}}); err != nil {
		t.Fatal(err)
	}

	targets := suggest(t, c)
	if len(targets) != 3 || targets[0].Mongo.Operation != MongoRunCommand || targets[1].Mongo.Collection != "items" ||
		targets[2].Mongo.Collection != benchCollection || !strings.HasSuffix(targets[2].Label, writesData) {
		t.Fatalf("suggestions %+v", targets)
	}
	runTargets(t, connections{"mg": c}, targets, 5, 50)
	if n, err := client.Database("bench").Collection(benchCollection).CountDocuments(ctx, bson.D{}); err != nil || n < 225 {
		t.Errorf("insertOne wrote %d documents (%v), want >= 225", n, err)
	}

	bad := model.Connection{ID: "mg-bad", Kind: model.ConnMongo, URI: fmt.Sprintf("mongodb://bench:wrongpass-123@127.0.0.1:%d/?authSource=admin", port)}
	expectAuthFailure(t, bad, "wrongpass-123")
}

// TestAWSProfileConnection drives the AWS-profile code path against Floci:
// a temporary shared config/credentials profile plus AWS_ENDPOINT_URL, so no
// request can reach a real account.
func TestAWSProfileConnection(t *testing.T) {
	base := startFloci(t)
	dir := t.TempDir()
	cfgFile, credFile := filepath.Join(dir, "config"), filepath.Join(dir, "credentials")
	if err := os.WriteFile(cfgFile, []byte("[profile nimbo-test]\nregion = eu-west-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credFile, []byte("[nimbo-test]\naws_access_key_id = test\naws_secret_access_key = test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"AWS_CONFIG_FILE": cfgFile, "AWS_SHARED_CREDENTIALS_FILE": credFile, "AWS_ENDPOINT_URL": base,
		"AWS_PROFILE": "", "AWS_DEFAULT_PROFILE": "", "AWS_REGION": "", "AWS_DEFAULT_REGION": "",
		"AWS_ACCESS_KEY_ID": "", "AWS_SECRET_ACCESS_KEY": "", "AWS_SESSION_TOKEN": "",
	} {
		t.Setenv(k, v)
	}

	ctx := context.Background()
	west := awsConfig(base, "eu-west-1")
	q, err := sqs.NewFromConfig(west).CreateQueue(ctx, &sqs.CreateQueueInput{QueueName: aws.String("conn-queue")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dynamodb.NewFromConfig(west).CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName:            aws.String("conn-table"),
		AttributeDefinitions: []ddbtypes.AttributeDefinition{{AttributeName: aws.String("pk"), AttributeType: ddbtypes.ScalarAttributeTypeS}},
		KeySchema:            []ddbtypes.KeySchemaElement{{AttributeName: aws.String("pk"), KeyType: ddbtypes.KeyTypeHash}},
		BillingMode:          ddbtypes.BillingModePayPerRequest,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.NewFromConfig(west, func(o *s3.Options) { o.UsePathStyle = true }).CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket:                    aws.String("conn-bucket"),
		CreateBucketConfiguration: &s3types.CreateBucketConfiguration{LocationConstraint: s3types.BucketLocationConstraintEuWest1},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := sns.NewFromConfig(west).CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String("conn-topic")}); err != nil {
		t.Fatal(err)
	}

	c := model.Connection{ID: "acct", Name: "acct", Kind: model.ConnAWS, Profile: "nimbo-test"}
	check := CheckConnection(ctx, c)
	t.Logf("aws check: %+v", check)
	if !check.OK || !strings.HasPrefix(check.Message, "AWS account ") {
		t.Fatalf("check %+v", check)
	}

	targets := suggest(t, c)
	send := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpSQSSend && tg.AWS.Resource == aws.ToString(q.QueueUrl) && strings.HasSuffix(tg.Label, writesData)
	}, "sending to conn-queue")
	get := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpDynamoGet && strings.HasSuffix(tg.AWS.Resource, "conn-table")
	}, "reading conn-table")
	put := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpDynamoPut && strings.HasSuffix(tg.AWS.Resource, "conn-table") && strings.HasSuffix(tg.Label, writesData)
	}, "writing conn-table")
	s3put := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpS3Put && tg.AWS.Resource == "conn-bucket" && strings.HasSuffix(tg.Label, writesData)
	}, "uploading to conn-bucket")
	s3get := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpS3Get && tg.AWS.Resource == "conn-bucket"
	}, "downloading from conn-bucket")
	publish := findTarget(t, targets, func(tg model.LoadTarget) bool {
		return tg.AWS.Operation == OpSNSPublish && strings.HasSuffix(tg.AWS.Resource, ":conn-topic") && strings.HasSuffix(tg.Label, writesData)
	}, "publishing to conn-topic")
	runTargets(t, connections{"acct": c}, []model.LoadTarget{send, get, put, s3put, s3get, publish}, 3, 20)

	attrs, err := sqs.NewFromConfig(west).GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{QueueUrl: q.QueueUrl, AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameApproximateNumberOfMessages}})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := strconv.Atoi(attrs.Attributes["ApproximateNumberOfMessages"]); n < 54 {
		t.Errorf("queue holds %d messages, want >= 54", n)
	}
}
