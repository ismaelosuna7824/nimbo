package advisor

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AWS design smells: a function invoking another synchronously, a producer
// sending the same work to several queues itself, long work behind API
// Gateway, uploads streamed through the API, a table write and a publish in
// the same function (dual write), and the shared-database smells of
// smells.go. Permissions are read from the IAM policies of each compute
// unit's role, so only what the design grants explicitly (actions on
// resources of the design) triggers them.

func awsSmells(d *design, out *[]rec) {
	units := awsUnits(d)
	awsLambdaChain(d, units, out)
	awsFanOut(d, units, out)
	awsAsyncRequestReply(d, out)
	awsValetKey(d, units, out)
	awsOutbox(d, units, out)
	sqlSmells(d, out, awsSQL, awsServices(d, units))
}

var pillarsDesign = []model.Pillar{model.PillarReliability, model.PillarPerformance, model.PillarOperations}

// ---------------------------------------------------------------------------
// Compute units and their IAM permissions

// awsUnit is one compute unit: a Lambda function, an ECS service with its
// task definitions, or an App Runner service, and the IAM statements of
// its roles.
type awsUnit struct {
	node   *model.Node
	parts  []*model.Node
	grants []awsGrant
}

// awsGrant is one Allow statement (or a whole policy document written as
// an expression): its source, to match actions, and the design resources
// it names.
type awsGrant struct {
	text string
	refs []*model.Node
}

func awsUnits(d *design) []*awsUnit {
	var out []*awsUnit
	for _, n := range d.ofType("aws_lambda_function", "aws_ecs_service", "aws_apprunner_service") {
		u := &awsUnit{node: n, parts: append([]*model.Node{n}, d.referenced(n, "aws_ecs_task_definition")...)}
		for _, p := range u.parts {
			for _, role := range d.referenced(p, "aws_iam_role") {
				u.grants = append(u.grants, awsRoleGrants(d, role)...)
			}
		}
		out = append(out, u)
	}
	return out
}

// can reports a statement of the unit's roles that allows one of actions
// on target.
func (u *awsUnit) can(target *model.Node, actions ...string) bool {
	for _, g := range u.grants {
		if slices.Contains(g.refs, target) && iamAllows(g.text, actions) {
			return true
		}
	}
	return false
}

// awsRoleGrants are the statements of a role's inline policies, attached
// customer-managed policies and inline_policy blocks.
func awsRoleGrants(d *design, role *model.Node) []awsGrant {
	var out []awsGrant
	for _, p := range d.referrers(role, "aws_iam_role_policy") {
		out = append(out, awsPolicyGrants(d, p, "policy")...)
	}
	for _, att := range d.referrers(role, "aws_iam_role_policy_attachment") {
		for _, p := range d.referenced(att, "aws_iam_policy") {
			out = append(out, awsPolicyGrants(d, p, "policy")...)
		}
	}
	i := 0
	for _, b := range role.Body.Blocks {
		if b.Type != "inline_policy" {
			continue
		}
		at := fmt.Sprintf("inline_policy[%d].policy", i)
		i++
		out = append(out, awsGrant{text: expr(&b.Body, "policy"), refs: resources(d.referencedAt(role, at))})
		for _, doc := range d.referencedAt(role, at, "aws_iam_policy_document") {
			out = append(out, awsDocGrants(d, doc)...)
		}
	}
	return out
}

// awsPolicyGrants reads the policy document of attribute at: an expression
// (jsonencode, heredoc) is one grant; a data aws_iam_policy_document is
// read statement by statement.
func awsPolicyGrants(d *design, p *model.Node, at string) []awsGrant {
	out := []awsGrant{{text: expr(&p.Body, at), refs: resources(d.referencedAt(p, at))}}
	for _, doc := range d.referencedAt(p, at, "aws_iam_policy_document") {
		out = append(out, awsDocGrants(d, doc)...)
	}
	return out
}

func awsDocGrants(d *design, doc *model.Node) []awsGrant {
	var out []awsGrant
	i := 0
	for _, b := range doc.Body.Blocks {
		if b.Type != "statement" {
			continue
		}
		at := fmt.Sprintf("statement[%d]", i)
		i++
		if effect, _ := str(&b.Body, "effect"); strings.EqualFold(effect, "Deny") {
			continue
		}
		out = append(out, awsGrant{text: expr(&b.Body, "actions"), refs: resources(d.referencedAt(doc, at))})
	}
	return out
}

// resources keeps the managed resources of nodes.
func resources(nodes []*model.Node) []*model.Node {
	return slices.DeleteFunc(nodes, func(n *model.Node) bool { return n.Kind != model.KindResource })
}

// iamAction matches a quoted IAM action ("sqs:SendMessage", "s3:Put*").
var iamAction = regexp.MustCompile(`"([A-Za-z0-9-]+:[A-Za-z0-9*]+)"`)

// iamAllows reports a policy source granting one of actions, directly or
// through a trailing wildcard ("sqs:*", "dynamodb:Put*"). A bare "*" is not
// read as a grant: it is too broad to say what the unit does.
func iamAllows(text string, actions []string) bool {
	for _, m := range iamAction.FindAllStringSubmatch(text, -1) {
		granted := strings.ToLower(m[1])
		for _, a := range actions {
			a = strings.ToLower(a)
			if granted == a || strings.HasSuffix(granted, "*") && strings.HasPrefix(a, strings.TrimSuffix(granted, "*")) {
				return true
			}
		}
	}
	return false
}

// awsExecRole is the role a unit's code runs with: a function's role, a
// task definition's task role, an App Runner instance role.
func awsExecRole(d *design, n *model.Node) *model.Node {
	for _, e := range d.into[n.ID] {
		if r := d.byID[e.Source]; r != nil && r.Type == "aws_iam_role" && (e.Attribute == "role" || e.Attribute == "task_role_arn" || strings.HasSuffix(e.Attribute, "instance_role_arn")) {
			return r
		}
	}
	return nil
}

// awsRolePolicy is an inline policy on the role of n allowing actions on
// resource; a comment when the role is not part of the design.
func awsRolePolicy(d *design, n *model.Node, suffix string, actions []string, resource string) string {
	quoted := make([]string, len(actions))
	for i, a := range actions {
		quoted[i] = fmt.Sprintf("%q", a)
	}
	role := awsExecRole(d, n)
	if role == nil {
		return fmt.Sprintf("# Allow %s's execution role %s on %s.", addr(n), strings.Join(actions, ", "), resource)
	}
	name := d.unique("aws_iam_role_policy", label(n, suffix))
	return fmt.Sprintf(`resource "aws_iam_role_policy" %q {
  name = %q
  role = %s.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Action = [%s]
      Resource = %s
    }]
  })
}`, name, dashed(name), addr(role), strings.Join(quoted, ", "), resource)
}

// awsName is the constant name attribute of n, or its Terraform name
// dashed.
func awsName(n *model.Node, attrName string) string {
	if s, ok := str(&n.Body, attrName); ok && s != "" {
		return s
	}
	return dashed(n.Name)
}

// awsWorkQueue is a queue with a dead-letter queue consumed by the function
// worker, and the worker's permission to read it. It returns the queue's
// address and the HCL.
func awsWorkQueue(d *design, worker *model.Node, suffix string) (string, string) {
	base := label(worker, suffix)
	dlq := d.unique("aws_sqs_queue", base+"_dlq")
	q := d.unique("aws_sqs_queue", base)
	name := awsName(worker, "function_name") + "-" + dashed(suffix)
	timeout, ok := number(&worker.Body, "timeout")
	if !ok {
		timeout = 3
	}
	visibility := max(30, int(6*timeout))
	queueAddr := "aws_sqs_queue." + q
	hcl := fmt.Sprintf(`resource "aws_sqs_queue" %q {
  name = %q
  message_retention_seconds = 1209600
}

resource "aws_sqs_queue" %q {
  name = %q
  # At least six times the consumer's timeout, so a message is not delivered again while it is processed.
  visibility_timeout_seconds = %d
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.%s.arn
    maxReceiveCount = 5
  })
}

resource "aws_lambda_event_source_mapping" %q {
  event_source_arn = %s.arn
  function_name = %s.arn
  batch_size = 10
  function_response_types = ["ReportBatchItemFailures"]

  # Caps the consumer's concurrency: the queue absorbs the peaks.
  scaling_config {
    maximum_concurrency = 10
  }
}`, dlq, name+"-dlq", q, name, visibility, dlq, d.unique("aws_lambda_event_source_mapping", base), queueAddr, addr(worker))
	return queueAddr, joinLines(hcl, awsRolePolicy(d, worker, suffix+"_consume", []string{"sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"}, queueAddr+".arn"))
}

// ---------------------------------------------------------------------------
// Smells

// awsLambdaChain recommends a queue between a function and the function it
// invokes synchronously (lambda:InvokeFunction granted on it).
func awsLambdaChain(d *design, units []*awsUnit, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	queues := map[*model.Node]string{}
	for _, a := range units {
		if a.node.Type != "aws_lambda_function" {
			continue
		}
		for _, b := range d.ofType("aws_lambda_function") {
			if b == a.node || !a.can(b, "lambda:InvokeFunction") {
				continue
			}
			for _, n := range []*model.Node{a.node, b} {
				if !slices.Contains(nodes, n) {
					nodes = append(nodes, n)
				}
			}
			queue, ok := queues[b]
			if !ok {
				var hcl string
				queue, hcl = awsWorkQueue(d, b, "requests")
				queues[b] = queue
				parts = append(parts, hcl)
			}
			parts = append(parts, fmt.Sprintf("# %s sends its requests for %s to %s instead of invoking it (remove lambda:InvokeFunction on %s from its role and pass the queue URL in its environment).", addr(a.node), addr(b), queue, addr(b)),
				awsRolePolicy(d, a.node, label(b, "send"), []string{"sqs:SendMessage"}, queue+".arn"))
		}
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      "aws_lambda_chain_queue",
		Pattern: "queue-based-load-leveling",
		Title:   "Put a queue between functions that invoke each other synchronously",
		Why: "A function that invokes another and waits pays for both while the second runs, inherits its errors, throttles and timeouts, and multiplies its concurrency during a spike; AWS lists Lambda functions calling Lambda functions as an anti-pattern. " +
			"With a queue in between the caller sends a message and returns, the consumer drains it at a bounded concurrency, and failed messages are retried and then kept in a dead-letter queue.",
		Pillars: pillarsDesign,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_sqs_queue", Purpose: "request queue per invoked function, with a dead-letter queue after 5 receives"},
			{Type: "aws_lambda_event_source_mapping", Purpose: "the invoked function consumes the queue with partial batch failures and a maximum concurrency"},
			{Type: "aws_iam_role_policy", Purpose: "sqs:SendMessage for the caller, receive and delete for the consumer"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "The caller needs the answer to reply to its own client: move the shared logic into a library both functions use, or orchestrate them with Step Functions. Also when the call is already asynchronous (InvocationType Event with an on-failure destination).",
		Rules:   []string{"bp_sqs_dlq"},
		Docs:    "https://docs.aws.amazon.com/lambda/latest/dg/concepts-event-driven-architectures.html",
	})
}

// awsFanOut recommends an SNS topic for a producer that sends to two or
// more standard queues itself.
func awsFanOut(d *design, units []*awsUnit, out *[]rec) {
	dlqs := awsDeadLetterQueues(d)
	var nodes []*model.Node
	var parts []string
	for _, u := range units {
		var queues []*model.Node
		for _, q := range d.ofType("aws_sqs_queue") {
			if dlqs[q.ID] || isTrue(&q.Body, "fifo_queue") || d.referredBy(q, "aws_sns_topic_subscription") || awsConsumes(d, u, q) || !u.can(q, "sqs:SendMessage") {
				continue
			}
			queues = append(queues, q)
		}
		if len(queues) < 2 {
			continue
		}
		nodes = appendNew(nodes, append([]*model.Node{u.node}, queues...)...)
		topic := d.unique("aws_sns_topic", label(u.node, "events"))
		topicAddr := "aws_sns_topic." + topic
		parts = append(parts, fmt.Sprintf("# %s publishes each event once to the topic; every queue subscribes to it (remove its sqs:SendMessage on them).\nresource \"aws_sns_topic\" %q {\n  name = %q\n}",
			addr(u.node), topic, dashed(topic)))
		for _, q := range queues {
			parts = append(parts, fmt.Sprintf(`resource "aws_sns_topic_subscription" %q {
  topic_arn = %s.arn
  protocol = "sqs"
  endpoint = %s.arn
  raw_message_delivery = true
}`, d.unique("aws_sns_topic_subscription", q.Name), topicAddr, addr(q)))
			if existing := d.referrers(q, "aws_sqs_queue_policy"); len(existing) > 0 {
				parts = append(parts, fmt.Sprintf("# Add to %s a statement allowing sns.amazonaws.com sqs:SendMessage on %s.arn when aws:SourceArn is %s.arn.", addr(existing[0]), addr(q), topicAddr))
				continue
			}
			parts = append(parts, fmt.Sprintf(`resource "aws_sqs_queue_policy" %q {
  queue_url = %s.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "sns.amazonaws.com" }
      Action = "sqs:SendMessage"
      Resource = %s.arn
      Condition = { ArnEquals = { "aws:SourceArn" = %s.arn } }
    }]
  })
}`, d.unique("aws_sqs_queue_policy", q.Name), addr(q), addr(q), topicAddr))
		}
		parts = append(parts, awsRolePolicy(d, u.node, "publish", []string{"sns:Publish"}, topicAddr+".arn"))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      "aws_fan_out_topic",
		Pattern: "fan-out",
		Title:   "Publish once to an SNS topic instead of sending to each queue",
		Why: "A producer that sends the same work to several queues itself must change for every new consumer, pays one call per queue, and leaves the consumers inconsistent when it fails between two sends. " +
			"Publishing once to a topic that each queue subscribes to fans the message out: consumers subscribe (with filter policies) without touching the producer. Queues encrypted with a customer-managed key must let sns.amazonaws.com use it.",
		Pillars: pillarsDesign,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_sns_topic", Purpose: "one topic per producer"},
			{Type: "aws_sns_topic_subscription", Purpose: "each queue subscribes with raw message delivery"},
			{Type: "aws_sqs_queue_policy", Purpose: "lets the topic, and only it, send to the queue"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "The producer sends different commands to different queues (each queue is its own channel, not a copy of the same event).",
		Docs:    "https://docs.aws.amazon.com/sns/latest/dg/sns-sqs-as-subscriber.html",
	})
}

// awsConsumes reports a unit that consumes queue q (event source mapping).
func awsConsumes(d *design, u *awsUnit, q *model.Node) bool {
	for _, m := range d.referrers(q, "aws_lambda_event_source_mapping") {
		if slices.Contains(d.referenced(m, "aws_lambda_function"), u.node) {
			return true
		}
	}
	return false
}

// awsAPIIntegrations maps each function behind an API Gateway integration
// to its integrations.
func awsAPIIntegrations(d *design) map[*model.Node][]*model.Node {
	out := map[*model.Node][]*model.Node{}
	for _, in := range d.ofType("aws_api_gateway_integration", "aws_apigatewayv2_integration") {
		for _, fn := range d.referenced(in, "aws_lambda_function") {
			out[fn] = append(out[fn], in)
		}
	}
	return out
}

// awsAsyncRequestReply recommends 202 Accepted plus a worker for functions
// behind API Gateway whose timeout exceeds the integration timeout.
func awsAsyncRequestReply(d *design, out *[]rec) {
	integrations := awsAPIIntegrations(d)
	var nodes []*model.Node
	var parts []string
	for _, fn := range d.ofType("aws_lambda_function") {
		timeout, ok := number(&fn.Body, "timeout")
		// A function that already consumes a queue is the worker of an
		// asynchronous design.
		if !ok || len(integrations[fn]) == 0 || d.referredBy(fn, "aws_lambda_event_source_mapping") {
			continue
		}
		over := false
		for _, in := range integrations[fn] {
			limit := 29.0 // REST APIs
			if in.Type == "aws_apigatewayv2_integration" {
				limit = 30
			}
			if ms, ok := number(&in.Body, "timeout_milliseconds"); ok && ms/1000 > limit {
				limit = ms / 1000
			}
			over = over || timeout > limit
		}
		if !over {
			continue
		}
		nodes = append(nodes, fn)
		table := d.unique("aws_dynamodb_table", label(fn, "jobs"))
		tableAddr := "aws_dynamodb_table." + table
		_, queue := awsWorkQueue(d, fn, "jobs")
		parts = append(parts, fmt.Sprintf(`# The route's integration moves to a small function that stores {id, status = "PENDING"} in %s, sends the job to the queue and answers 202 Accepted with Location: /jobs/{id};
# GET /jobs/{id} reads the status. %s becomes the worker: it consumes the queue and writes the result with status = "DONE".
resource "aws_dynamodb_table" %q {
  name = %q
  billing_mode = "PAY_PER_REQUEST"
  hash_key = "id"

  attribute {
    name = "id"
    type = "S"
  }

  # Finished jobs expire on their own.
  ttl {
    attribute_name = "expires_at"
    enabled = true
  }
}`, tableAddr, addr(fn), table, dashed(table)), queue, awsRolePolicy(d, fn, "job_status", []string{"dynamodb:GetItem", "dynamodb:UpdateItem"}, tableAddr+".arn"))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      "aws_api_async_request_reply",
		Pattern: "async-request-reply",
		Title:   "Answer long API requests with 202 Accepted and finish the work asynchronously",
		Why: "These functions may run longer than API Gateway waits: 29 seconds for REST APIs (raising it for Regional APIs needs a quota increase) and 30 seconds for HTTP APIs. " +
			"The client gets a 504 while the function keeps running, retries, and the work runs twice. With asynchronous request-reply the API stores the job, queues it and answers 202 with a status URL; a worker does the work and the client polls (or is notified).",
		Pillars: pillarsDesign,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_sqs_queue", Purpose: "job queue with a dead-letter queue"},
			{Type: "aws_lambda_event_source_mapping", Purpose: "the long-running function consumes the jobs"},
			{Type: "aws_dynamodb_table", Purpose: "job status (PENDING, DONE, FAILED) read by GET /jobs/{id}, expiring with a TTL"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Functions that always finish well under the API timeout (lower their timeout to match instead).",
		Rules:   []string{"bp_sqs_dlq"},
		Docs:    "https://learn.microsoft.com/en-us/azure/architecture/patterns/async-request-reply",
	})
}

// uploadHint matches names of functions, buckets and variables that carry
// user files.
var uploadHint = regexp.MustCompile(`(?i)upload|file|image|media|attachment|document|photo|video|avatar|asset`)

// awsValetKey recommends pre-signed upload URLs for functions behind API
// Gateway that write user files to a bucket.
func awsValetKey(d *design, units []*awsUnit, out *[]rec) {
	integrations := awsAPIIntegrations(d)
	var nodes []*model.Node
	var parts []string
	for _, u := range units {
		if len(integrations[u.node]) == 0 {
			continue
		}
		vars, _ := literal(path(&u.node.Body, "environment"), "variables").(map[string]any)
		names := append([]string{u.node.Name, awsName(u.node, "function_name")}, sortedKeys(vars)...)
		for _, b := range d.ofType("aws_s3_bucket") {
			if !u.can(b, "s3:PutObject") || !uploadHint.MatchString(strings.Join(append(names, b.Name, awsName(b, "bucket")), " ")) || awsDirectUploads(d, b) {
				continue
			}
			nodes = appendNew(nodes, u.node, b)
			cors := fmt.Sprintf("resource \"aws_s3_bucket_cors_configuration\" %q {\n  bucket = %s.id\n", d.unique("aws_s3_bucket_cors_configuration", b.Name), addr(b))
			if existing := d.referrers(b, "aws_s3_bucket_cors_configuration"); len(existing) > 0 {
				cors = fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n", addr(existing[0]), existing[0].Type, existing[0].Name)
			}
			parts = append(parts, fmt.Sprintf(`# %s returns a pre-signed PUT URL for one object key (5-minute expiry, fixed content type) instead of receiving the file; the browser uploads straight to %s.
# Its role keeps s3:PutObject on the bucket: the URL lends that permission for that one key only.
%s
  cors_rule {
    allowed_methods = ["PUT"]
    allowed_origins = ["https://app.example.com"]
    allowed_headers = ["content-type"]
    max_age_seconds = 3000
  }
}`, addr(u.node), addr(b), cors))
			break
		}
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      "aws_valet_key_uploads",
		Pattern: "valet-key",
		Title:   "Upload files straight to S3 with pre-signed URLs, not through the API",
		Why: "Files sent through API Gateway and a function pay for every byte twice, hold the function for the whole transfer and hit the payload limits (10 MB for API Gateway, 6 MB for a synchronous Lambda invocation). " +
			"A pre-signed URL is a valet key: the function authorizes the upload and returns a short-lived URL for one object, and the client uploads to S3 directly; an S3 event can then process the object.",
		Pillars: []model.Pillar{model.PillarPerformance, model.PillarCost, model.PillarSecurity},
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "aws_s3_bucket_cors_configuration", Purpose: "lets browsers PUT to the bucket from your web origin with the pre-signed URL"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Small payloads the function validates or builds itself (JSON documents, generated files).",
		Docs:    "https://docs.aws.amazon.com/AmazonS3/latest/userguide/PresignedUrlUploadObject.html",
	})
}

// awsDirectUploads reports a bucket whose CORS rules let browsers PUT or
// POST objects: clients already upload to it directly.
func awsDirectUploads(d *design, b *model.Node) bool {
	for _, c := range d.referrers(b, "aws_s3_bucket_cors_configuration") {
		for _, r := range c.Body.Blocks {
			if methods, _ := literal(&r.Body, "allowed_methods").([]any); r.Type == "cors_rule" && (slices.Contains(methods, any("PUT")) || slices.Contains(methods, any("POST"))) {
				return true
			}
		}
	}
	return false
}

// awsOutbox recommends publishing a table's changes through its stream
// and an EventBridge pipe for units that both write a DynamoDB table and
// publish to an SNS topic or event bus (a dual write).
func awsOutbox(d *design, units []*awsUnit, out *[]rec) {
	writes := []string{"dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:BatchWriteItem", "dynamodb:TransactWriteItems"}
	var nodes []*model.Node
	var parts []string
	done := map[string]bool{}
	for _, u := range units {
		for _, t := range d.ofType("aws_dynamodb_table") {
			if isTrue(&t.Body, "stream_enabled") || !u.can(t, writes...) {
				continue
			}
			var target *model.Node
			action := ""
			for _, n := range d.ofType("aws_sns_topic", "aws_cloudwatch_event_bus") {
				a := "sns:Publish"
				if n.Type == "aws_cloudwatch_event_bus" {
					a = "events:PutEvents"
				}
				if !isTrue(&n.Body, "fifo_topic") && u.can(n, a) {
					target, action = n, a
					break
				}
			}
			if target == nil {
				continue
			}
			nodes = appendNew(nodes, u.node, t, target)
			if done[t.ID+target.ID] {
				continue
			}
			done[t.ID+target.ID] = true
			pipe := d.unique("aws_pipes_pipe", label(t, "outbox"))
			role := d.unique("aws_iam_role", label(t, "outbox_pipe"))
			parts = append(parts, fmt.Sprintf(`# %s only writes %s (the business item, or an outbox item in the same TransactWriteItems) and stops publishing; the pipe publishes the stream to %s.
resource %q %q {
  stream_enabled = true
  stream_view_type = "NEW_IMAGE"
}

resource "aws_iam_role" %q {
  name = %q
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = { Service = "pipes.amazonaws.com" }
      Action = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" %q {
  name = %q
  role = aws_iam_role.%s.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect = "Allow"
        Action = ["dynamodb:DescribeStream", "dynamodb:GetRecords", "dynamodb:GetShardIterator", "dynamodb:ListStreams"]
        Resource = %s.stream_arn
      },
      {
        Effect = "Allow"
        Action = [%q]
        Resource = %s.arn
      }
    ]
  })
}

resource "aws_pipes_pipe" %q {
  name = %q
  role_arn = aws_iam_role.%s.arn
  source = %s.stream_arn
  target = %s.arn

  source_parameters {
    dynamodb_stream_parameters {
      starting_position = "TRIM_HORIZON"
      maximum_retry_attempts = 10
    }
  }
}`, addr(u.node), addr(t), addr(target), t.Type, t.Name, role, dashed(role), d.unique("aws_iam_role_policy", role), dashed(role), role, addr(t), action, addr(target),
				pipe, dashed(pipe), role, addr(t), addr(target)))
		}
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioDesign, Recommendation{
		ID:      "aws_transactional_outbox",
		Pattern: "transactional-outbox",
		Title:   "Publish the table's changes from its stream instead of writing and publishing in the same function",
		Why: "A function that writes a table and then publishes an event makes two separate calls (a dual write): if it fails between them the event is lost, or announces a write that never happened, and retries publish duplicates. " +
			"With a transactional outbox the write is the only step: the change lands in the table's stream and an EventBridge pipe publishes it at least once, in order per item. Consumers must be idempotent (deduplicate on the item key and version).",
		Pillars: []model.Pillar{model.PillarReliability, model.PillarOperations},
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "aws_dynamodb_table", Purpose: "stream_enabled with NEW_IMAGE: every committed change becomes a stream record"},
			{Type: "aws_pipes_pipe", Purpose: "reads the stream and publishes each record to the topic or bus, retrying on failure"},
			{Type: "aws_iam_role", Purpose: "the pipe's role: read the stream, publish to the target"},
			{Type: "aws_iam_role_policy", Purpose: "least-privilege permissions of the pipe"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Notifications that may be lost or duplicated without harm (cache invalidations, best-effort emails).",
		Docs:    "https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html",
	})
}

// awsServices groups the compute units into services: functions with the
// same role or behind the same API are one service deployed together (a
// function per route), ECS and App Runner services are one each.
func awsServices(d *design, units []*awsUnit) []tfService {
	integrations := awsAPIIntegrations(d)
	key := func(u *awsUnit) []string {
		if u.node.Type != "aws_lambda_function" {
			return []string{u.node.ID}
		}
		var keys []string
		if r := awsExecRole(d, u.node); r != nil {
			keys = append(keys, r.ID)
		}
		for _, in := range integrations[u.node] {
			for _, api := range d.referenced(in, "aws_api_gateway_rest_api", "aws_apigatewayv2_api") {
				keys = append(keys, api.ID)
			}
		}
		if len(keys) == 0 {
			keys = append(keys, u.node.ID)
		}
		return keys
	}
	parent := make([]int, len(units))
	var find func(int) int
	find = func(i int) int {
		if parent[i] != i {
			parent[i] = find(parent[i])
		}
		return parent[i]
	}
	first := map[string]int{}
	for i, u := range units {
		parent[i] = i
		for _, k := range key(u) {
			if j, ok := first[k]; ok {
				parent[find(i)] = find(j)
			} else {
				first[k] = i
			}
		}
	}
	var out []tfService
	index := map[int]int{}
	for i, u := range units {
		root := find(i)
		idx, ok := index[root]
		if !ok {
			idx = len(out)
			index[root] = idx
			out = append(out, tfService{})
		}
		out[idx].nodes = append(out[idx].nodes, u.node)
		out[idx].parts = append(out[idx].parts, u.parts...)
	}
	return out
}
