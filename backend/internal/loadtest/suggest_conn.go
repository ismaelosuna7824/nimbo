package loadtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	ddbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const (
	// suggestTimeout bounds SuggestConnection.
	suggestTimeout = 20 * time.Second
	// suggestTables caps tables / collections suggested; suggestAWS caps
	// resources listed per AWS service.
	suggestTables = 5
	suggestAWS    = 10
	// writesData marks suggestions that modify the target service.
	writesData = " (writes data)"
	// benchCollection receives the documents of the MongoDB write suggestion.
	benchCollection = "nimbo_bench"
	mongoBenchDoc   = `{"source":"nimbo-loadtest","seq":{{seq}},"id":"{{uuid}}","sentAt":"{{now}}"}`
)

// SuggestConnection lists ready-to-run targets for a connection. Listing is
// read-only and small; every suggestion carries the connection's ID and
// suggestions that write data end their label with " (writes data)" (Lambda
// invocations with " (invokes function)").
func SuggestConnection(ctx context.Context, c model.Connection) ([]model.LoadTarget, error) {
	ctx, cancel := context.WithTimeout(ctx, suggestTimeout)
	defer cancel()
	out, err := suggestConnection(ctx, &c)
	if err != nil {
		return nil, errors.New(redactSecrets(err.Error(), &c))
	}
	for i := range out {
		out[i].ConnectionID = c.ID
	}
	return out, nil
}

func suggestConnection(ctx context.Context, c *model.Connection) ([]model.LoadTarget, error) {
	switch c.Kind {
	case model.ConnHTTP:
		return []model.LoadTarget{{
			Kind:  model.TargetHTTP,
			Label: "GET / · " + c.Name,
			HTTP:  &model.HTTPTarget{Method: http.MethodGet, URL: "/"},
		}}, nil
	case model.ConnPostgres:
		return suggestPostgres(ctx, c)
	case model.ConnMySQL:
		return suggestMySQL(ctx, c)
	case model.ConnRedis:
		return []model.LoadTarget{
			redisTarget("PING", "PING"),
			redisTarget("GET random keys", "GET nimbo:bench:{{rand}}"),
			redisTarget("SET keys"+writesData, "SET nimbo:bench:{{seq}} {{uuid}}"),
		}, nil
	case model.ConnMongo:
		return suggestMongo(ctx, c)
	case model.ConnAWS:
		return suggestAWSAccount(ctx, c)
	}
	return nil, fmt.Errorf("unknown connection kind %q", c.Kind)
}

func redisTarget(label, cmd string) model.LoadTarget {
	return model.LoadTarget{Kind: model.TargetRedis, Label: label, Redis: &model.RedisTarget{Command: cmd}}
}

func sqlTarget(label, query string) model.LoadTarget {
	return model.LoadTarget{Kind: model.TargetSQL, Label: label, SQL: &model.SQLTarget{Query: query, Mode: "query"}}
}

func suggestPostgres(ctx context.Context, c *model.Connection) ([]model.LoadTarget, error) {
	cfg, err := postgresConfig(*c)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg.ConnConfig)
	if err != nil {
		return nil, err
	}
	defer conn.Close(context.WithoutCancel(ctx))
	rows, err := conn.Query(ctx, `SELECT table_schema, table_name FROM information_schema.tables
		WHERE table_type = 'BASE TABLE' AND table_schema NOT IN ('pg_catalog', 'information_schema')
		ORDER BY table_schema = 'public' DESC, table_schema, table_name LIMIT $1`, suggestTables)
	if err != nil {
		return nil, err
	}
	out := []model.LoadTarget{sqlTarget("SELECT 1", "SELECT 1")}
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			rows.Close()
			return nil, err
		}
		name := table
		if schema != "public" {
			name = schema + "." + table
		}
		out = append(out, sqlTarget("Read "+name, "SELECT * FROM "+pgx.Identifier{schema, table}.Sanitize()+" LIMIT 10"))
	}
	rows.Close()
	return out, rows.Err()
}

func suggestMySQL(ctx context.Context, c *model.Connection) ([]model.LoadTarget, error) {
	cfg, err := mysqlConfig(*c)
	if err != nil {
		return nil, err
	}
	conn, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(conn)
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = DATABASE() AND table_type = 'BASE TABLE' ORDER BY table_name LIMIT ?`, suggestTables)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.LoadTarget{sqlTarget("SELECT 1", "SELECT 1")}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		out = append(out, sqlTarget("Read "+table, "SELECT * FROM "+mysqlIdent(table)+" LIMIT 10"))
	}
	return out, rows.Err()
}

// mysqlIdent quotes a MySQL identifier.
func mysqlIdent(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "``") + "`"
}

func suggestMongo(ctx context.Context, c *model.Connection) ([]model.LoadTarget, error) {
	opts, err := mongoOptions(*c)
	if err != nil {
		return nil, err
	}
	client, err := mongo.Connect(opts.SetMaxPoolSize(2))
	if err != nil {
		return nil, err
	}
	defer client.Disconnect(context.WithoutCancel(ctx))
	dbName := orDefault(c.Database, "admin")
	if err := client.Database(dbName).RunCommand(ctx, bson.D{{Key: "ping", Value: 1}}).Err(); err != nil {
		return nil, err
	}
	out := []model.LoadTarget{{
		Kind:  model.TargetMongo,
		Label: "ping · " + dbName,
		Mongo: &model.MongoTarget{Database: dbName, Operation: MongoRunCommand, Document: `{"ping":1}`},
	}}
	if c.Database == "" {
		return out, nil
	}
	names, err := client.Database(c.Database).ListCollectionNames(ctx, bson.D{{Key: "name", Value: bson.D{{Key: "$not", Value: bson.Regex{Pattern: "^system\\."}}}}})
	if err != nil {
		return nil, err
	}
	n := 0
	for _, name := range names {
		if name == benchCollection {
			continue
		}
		if n++; n > suggestTables {
			break
		}
		out = append(out, model.LoadTarget{
			Kind:  model.TargetMongo,
			Label: "findOne · " + name,
			Mongo: &model.MongoTarget{Database: c.Database, Collection: name, Operation: MongoFindOne, Document: "{}"},
		})
	}
	out = append(out, model.LoadTarget{
		Kind:  model.TargetMongo,
		Label: "insertOne · " + benchCollection + writesData,
		Mongo: &model.MongoTarget{Database: c.Database, Collection: benchCollection, Operation: MongoInsertOne, Document: mongoBenchDoc},
	})
	return out, nil
}

// suggestAWSAccount lists up to suggestAWS queues, tables, buckets,
// functions and topics of the account. Services that fail (e.g. denied by
// IAM) are skipped; the call fails only when every service fails.
func suggestAWSAccount(ctx context.Context, c *model.Connection) ([]model.LoadTarget, error) {
	cfg, err := loadAWSConfig(ctx, *c, nil)
	if err != nil {
		return nil, err
	}
	listers := []func(context.Context, aws.Config) ([]model.LoadTarget, error){
		suggestSQS, suggestDynamo, suggestS3, suggestLambda, suggestSNS,
	}
	results := make([][]model.LoadTarget, len(listers))
	errs := make([]error, len(listers))
	var wg sync.WaitGroup
	for i, list := range listers {
		wg.Go(func() { results[i], errs[i] = list(ctx, cfg) })
	}
	wg.Wait()
	out := []model.LoadTarget{}
	failed := 0
	for i := range listers {
		if errs[i] != nil {
			failed++
			continue
		}
		out = append(out, results[i]...)
	}
	if failed == len(listers) {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func awsTarget(label, op, resource, payload, key string) model.LoadTarget {
	return model.LoadTarget{Kind: model.TargetAWS, Label: label, AWS: &model.AWSTarget{Operation: op, Resource: resource, Payload: payload, Key: key}}
}

func suggestSQS(ctx context.Context, cfg aws.Config) ([]model.LoadTarget, error) {
	out, err := sqs.NewFromConfig(cfg).ListQueues(ctx, &sqs.ListQueuesInput{MaxResults: aws.Int32(suggestAWS)})
	if err != nil {
		return nil, err
	}
	var targets []model.LoadTarget
	for _, u := range out.QueueUrls {
		name := u[strings.LastIndex(u, "/")+1:]
		targets = append(targets, awsTarget("Send messages · "+name+writesData, OpSQSSend, u, "", ""))
	}
	return targets, nil
}

func suggestDynamo(ctx context.Context, cfg aws.Config) ([]model.LoadTarget, error) {
	c := dynamodb.NewFromConfig(cfg)
	out, err := c.ListTables(ctx, &dynamodb.ListTablesInput{Limit: aws.Int32(suggestAWS)})
	if err != nil {
		return nil, err
	}
	var targets []model.LoadTarget
	for _, name := range out.TableNames {
		desc, err := c.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(name)})
		if err != nil || desc.Table == nil {
			continue
		}
		item, key, ok := dynamoKeyTemplates(desc.Table)
		if !ok {
			continue
		}
		table := orDefault(aws.ToString(desc.Table.TableArn), name)
		targets = append(targets,
			awsTarget("Read random items · "+name, OpDynamoGet, table, "", key),
			awsTarget("Write items · "+name+writesData, OpDynamoPut, table, item, ""),
		)
	}
	return targets, nil
}

// dynamoKeyTemplates builds, from a table's key schema, a PutItem payload
// with unique keys and a GetItem key of random values (reads of random keys
// mostly miss, which is still a full read path). Binary keys are not
// supported.
func dynamoKeyTemplates(t *ddbtypes.TableDescription) (item, key string, ok bool) {
	types := map[string]ddbtypes.ScalarAttributeType{}
	for _, a := range t.AttributeDefinitions {
		types[aws.ToString(a.AttributeName)] = a.AttributeType
	}
	var itemParts, keyParts []string
	names := map[string]bool{}
	for _, k := range t.KeySchema {
		name := aws.ToString(k.AttributeName)
		q, _ := json.Marshal(name)
		names[name] = true
		unique := `"{{uuid}}"`
		if k.KeyType == ddbtypes.KeyTypeRange {
			unique = `"{{now}}"`
		}
		switch types[name] {
		case ddbtypes.ScalarAttributeTypeS:
			itemParts = append(itemParts, string(q)+":"+unique)
			keyParts = append(keyParts, string(q)+`:"{{rand}}"`)
		case ddbtypes.ScalarAttributeTypeN:
			n := "{{seq}}"
			if k.KeyType == ddbtypes.KeyTypeRange {
				n = "{{rand}}"
			}
			itemParts = append(itemParts, string(q)+":"+n)
			keyParts = append(keyParts, string(q)+":{{rand}}")
		default:
			return "", "", false
		}
	}
	if len(keyParts) == 0 {
		return "", "", false
	}
	for _, extra := range [][2]string{{"source", `"nimbo-loadtest"`}, {"createdAt", `"{{now}}"`}} {
		if !names[extra[0]] {
			itemParts = append(itemParts, `"`+extra[0]+`":`+extra[1])
		}
	}
	return "{" + strings.Join(itemParts, ",") + "}", "{" + strings.Join(keyParts, ",") + "}", true
}

func suggestS3(ctx context.Context, cfg aws.Config) ([]model.LoadTarget, error) {
	c := s3.NewFromConfig(cfg)
	out, err := c.ListBuckets(ctx, &s3.ListBucketsInput{MaxBuckets: aws.Int32(suggestAWS)})
	if err != nil {
		return nil, err
	}
	var targets []model.LoadTarget
	for _, b := range out.Buckets {
		bucket := aws.ToString(b.Name)
		region := aws.ToString(b.BucketRegion)
		if region == "" {
			region = bucketRegion(ctx, c, bucket)
		}
		bc := c
		label := bucket
		if region != "" {
			label += " (" + region + ")"
			if region != cfg.Region {
				bc = s3.NewFromConfig(cfg, func(o *s3.Options) { o.Region = region })
			}
		}
		// Read an existing object when there is one; otherwise the run
		// uploads a probe object before reading it.
		get := awsTarget("Download an object · "+label+writesData, OpS3Get, bucket, "", defaultGetKey)
		if objs, err := bc.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &bucket, MaxKeys: aws.Int32(1)}); err == nil && len(objs.Contents) > 0 {
			get = awsTarget("Download an object · "+label, OpS3Get, bucket, "", aws.ToString(objs.Contents[0].Key))
		}
		targets = append(targets, get, awsTarget("Upload objects · "+label+writesData, OpS3Put, bucket, "", defaultPutKey))
	}
	return targets, nil
}

func suggestLambda(ctx context.Context, cfg aws.Config) ([]model.LoadTarget, error) {
	out, err := lambda.NewFromConfig(cfg).ListFunctions(ctx, &lambda.ListFunctionsInput{MaxItems: aws.Int32(suggestAWS)})
	if err != nil {
		return nil, err
	}
	var targets []model.LoadTarget
	for _, f := range out.Functions {
		targets = append(targets, awsTarget("Invoke · "+aws.ToString(f.FunctionName)+" (invokes function)", OpLambdaInvoke, orDefault(aws.ToString(f.FunctionArn), aws.ToString(f.FunctionName)), "", ""))
	}
	return targets, nil
}

func suggestSNS(ctx context.Context, cfg aws.Config) ([]model.LoadTarget, error) {
	out, err := sns.NewFromConfig(cfg).ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return nil, err
	}
	var targets []model.LoadTarget
	for _, t := range out.Topics[:min(len(out.Topics), suggestAWS)] {
		topic := aws.ToString(t.TopicArn)
		targets = append(targets, awsTarget("Publish messages · "+arnName(topic, "")+writesData, OpSNSPublish, topic, "", ""))
	}
	return targets, nil
}
