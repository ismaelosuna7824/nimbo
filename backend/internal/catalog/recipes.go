package catalog

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func isType(types ...string) func(n *model.Node) bool {
	return func(n *model.Node) bool {
		if n.Kind != model.KindResource {
			return false
		}
		for _, t := range types {
			if n.Type == t {
				return true
			}
		}
		return false
	}
}

func pair(src, dst func(*model.Node) bool) func(*Catalog, *model.Node, *model.Node) bool {
	return func(_ *Catalog, s, d *model.Node) bool { return src(s) && dst(d) }
}

var (
	isLambda = isType("aws_lambda_function")
	isQueue  = isType("aws_sqs_queue")
	isTopic  = isType("aws_sns_topic")
)

// grantSpec describes a "Lambda uses <resource>" recipe: IAM permissions on
// the function's role plus an environment variable carrying the resource
// identifier.
type grantSpec struct {
	typ, word, envSuffix, envAttr string
	statements                    func(target *model.Node) []stmt
}

var grants = []grantSpec{
	{"aws_dynamodb_table", "TABLE", "NAME", "name", func(t *model.Node) []stmt {
		return []stmt{{
			actions:   []string{"dynamodb:GetItem", "dynamodb:PutItem", "dynamodb:UpdateItem", "dynamodb:DeleteItem", "dynamodb:Query", "dynamodb:Scan", "dynamodb:BatchGetItem", "dynamodb:BatchWriteItem", "dynamodb:ConditionCheckItem"},
			resources: []string{ref(t, "arn"), fmt.Sprintf(`"${%s}/index/*"`, ref(t, "arn"))},
		}}
	}},
	{"aws_s3_bucket", "BUCKET", "NAME", "bucket", func(t *model.Node) []stmt {
		return []stmt{
			{actions: []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"}, resources: []string{fmt.Sprintf(`"${%s}/*"`, ref(t, "arn"))}},
			{actions: []string{"s3:ListBucket"}, resources: []string{ref(t, "arn")}},
		}
	}},
	{"aws_sqs_queue", "QUEUE", "URL", "url", func(t *model.Node) []stmt {
		return []stmt{{actions: []string{"sqs:SendMessage", "sqs:GetQueueAttributes", "sqs:GetQueueUrl"}, resources: []string{ref(t, "arn")}}}
	}},
	{"aws_sns_topic", "TOPIC", "ARN", "arn", func(t *model.Node) []stmt {
		return []stmt{{actions: []string{"sns:Publish"}, resources: []string{ref(t, "arn")}}}
	}},
	{"aws_secretsmanager_secret", "SECRET", "ARN", "arn", func(t *model.Node) []stmt {
		return []stmt{{actions: []string{"secretsmanager:GetSecretValue", "secretsmanager:DescribeSecret"}, resources: []string{ref(t, "arn")}}}
	}},
	{"aws_kinesis_stream", "STREAM", "NAME", "name", func(t *model.Node) []stmt {
		return []stmt{{actions: []string{"kinesis:PutRecord", "kinesis:PutRecords", "kinesis:DescribeStreamSummary"}, resources: []string{ref(t, "arn")}}}
	}},
	{"aws_sfn_state_machine", "STATE_MACHINE", "ARN", "arn", func(t *model.Node) []stmt {
		return []stmt{{actions: []string{"states:StartExecution", "states:StartSyncExecution"}, resources: []string{ref(t, "arn")}}}
	}},
}

var recipes = buildRecipes()

func buildRecipes() []recipe {
	rs := []recipe{
		{
			id: "sqs-lambda", label: "Trigger the function from the queue",
			desc:  "Adds an event source mapping so Lambda polls the queue, and lets the function's role consume messages.",
			match: pair(isQueue, isLambda), bidi: true,
			apply: func(x *conn) error {
				q, fn := x.src, x.dst
				p := x.rolePolicy(fn, q, []stmt{{
					actions:   []string{"sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes", "sqs:ChangeMessageVisibility"},
					resources: []string{ref(q, "arn")},
				}})
				esm := x.eventSourceMapping(q, fn, ref(q, "arn"))
				setAttr(&esm.Body, "batch_size", lit(10))
				dependOn(esm, p)
				return nil
			},
		},
		{
			id: "dynamodb-stream-lambda", label: "Trigger the function from table changes",
			desc:  "Enables the DynamoDB stream and adds an event source mapping so Lambda receives item changes.",
			match: pair(isType("aws_dynamodb_table"), isLambda), bidi: true,
			apply: func(x *conn) error {
				t, fn := x.src, x.dst
				setAttr(&t.Body, "stream_enabled", lit(true))
				setDefault(&t.Body, "stream_view_type", lit("NEW_AND_OLD_IMAGES"))
				p := x.rolePolicy(fn, t, []stmt{{
					actions:   []string{"dynamodb:GetRecords", "dynamodb:GetShardIterator", "dynamodb:DescribeStream", "dynamodb:ListStreams"},
					resources: []string{ref(t, "stream_arn")},
				}})
				esm := x.eventSourceMapping(t, fn, ref(t, "stream_arn"))
				setAttr(&esm.Body, "starting_position", lit("LATEST"))
				dependOn(esm, p)
				return nil
			},
		},
		{
			id: "kinesis-lambda", label: "Trigger the function from the stream",
			desc:  "Adds an event source mapping so Lambda reads records from the Kinesis stream.",
			match: pair(isType("aws_kinesis_stream"), isLambda), bidi: true,
			apply: func(x *conn) error {
				s, fn := x.src, x.dst
				p := x.rolePolicy(fn, s, []stmt{{
					actions:   []string{"kinesis:GetRecords", "kinesis:GetShardIterator", "kinesis:DescribeStream", "kinesis:DescribeStreamSummary", "kinesis:ListShards", "kinesis:ListStreams"},
					resources: []string{ref(s, "arn")},
				}})
				esm := x.eventSourceMapping(s, fn, ref(s, "arn"))
				setAttr(&esm.Body, "starting_position", lit("LATEST"))
				dependOn(esm, p)
				return nil
			},
		},
		{
			id: "sns-sqs", label: "Subscribe the queue to the topic",
			desc:  "Adds an SNS subscription and a queue policy that allows the topic to send messages.",
			match: pair(isTopic, isQueue), bidi: true,
			apply: func(x *conn) error {
				t, q := x.src, x.dst
				sub := x.helper(model.KindResource, "aws_sns_topic_subscription", q.Name+"_subscription")
				setAttr(&sub.Body, "topic_arn", expr(ref(t, "arn")))
				setAttr(&sub.Body, "protocol", lit("sqs"))
				setAttr(&sub.Body, "endpoint", expr(ref(q, "arn")))
				return x.allowService(q, "sns.amazonaws.com", t, []string{"sqs:SendMessage"})
			},
		},
		{
			id: "sns-lambda", label: "Subscribe the function to the topic",
			desc:  "Adds an SNS subscription and a Lambda permission for SNS to invoke the function.",
			match: pair(isTopic, isLambda), bidi: true,
			apply: func(x *conn) error {
				t, fn := x.src, x.dst
				perm := x.permission(fn, t, "sns.amazonaws.com", ref(t, "arn"))
				sub := x.helper(model.KindResource, "aws_sns_topic_subscription", fn.Name+"_subscription")
				setAttr(&sub.Body, "topic_arn", expr(ref(t, "arn")))
				setAttr(&sub.Body, "protocol", lit("lambda"))
				setAttr(&sub.Body, "endpoint", expr(ref(fn, "arn")))
				dependOn(sub, perm)
				return nil
			},
		},
		{
			id: "s3-lambda", label: "Run the function when objects are created",
			desc:  "Adds a bucket notification for s3:ObjectCreated:* and a Lambda permission for S3.",
			match: pair(isType("aws_s3_bucket"), isLambda), bidi: true,
			apply: func(x *conn) error {
				bkt, fn := x.src, x.dst
				perm := x.permission(fn, bkt, "s3.amazonaws.com", ref(bkt, "arn"))
				var notif *model.Node
				for _, n := range x.b.all() {
					if n.Kind == model.KindResource && n.Type == "aws_s3_bucket_notification" {
						if a := getAttr(&n.Body, "bucket"); a != nil && referencesNode(valueSource(a.Value), address(bkt)) {
							notif = n
						}
					}
				}
				if notif == nil {
					notif = x.helper(model.KindResource, "aws_s3_bucket_notification", bkt.Name+"_events")
					setAttr(&notif.Body, "bucket", expr(ref(bkt, "id")))
				}
				blk := model.Block{Type: "lambda_function", Body: emptyBody()}
				setAttr(&blk.Body, "lambda_function_arn", expr(ref(fn, "arn")))
				setAttr(&blk.Body, "events", lit([]string{"s3:ObjectCreated:*"}))
				notif.Body.Blocks = append(notif.Body.Blocks, blk)
				dependOn(notif, perm)
				return nil
			},
		},
		{
			id: "apigwv2-lambda", label: "Route HTTP requests to the function",
			desc:  "Adds a Lambda proxy integration (payload 2.0), an ANY /{proxy+} route, a $default auto-deploy stage when missing, and an invoke permission.",
			match: pair(isType("aws_apigatewayv2_api"), isLambda), bidi: true,
			apply: func(x *conn) error {
				api, fn := x.src, x.dst
				integ := x.helper(model.KindResource, "aws_apigatewayv2_integration", fn.Name+"_integration")
				setAttr(&integ.Body, "api_id", expr(ref(api, "id")))
				setAttr(&integ.Body, "integration_type", lit("AWS_PROXY"))
				setAttr(&integ.Body, "integration_uri", expr(ref(fn, "invoke_arn")))
				setAttr(&integ.Body, "integration_method", lit("POST"))
				setAttr(&integ.Body, "payload_format_version", lit("2.0"))

				routeKey := "ANY /{proxy+}"
				stage := false
				for _, n := range x.b.all() {
					if n.Kind != model.KindResource {
						continue
					}
					a := getAttr(&n.Body, "api_id")
					if a == nil || !referencesNode(valueSource(a.Value), address(api)) {
						continue
					}
					switch n.Type {
					case "aws_apigatewayv2_route":
						if rk := getAttr(&n.Body, "route_key"); rk != nil && strings.Trim(valueSource(rk.Value), `"`) == routeKey {
							routeKey = "ANY /" + strings.ReplaceAll(fn.Name, "_", "-") + "/{proxy+}"
						}
					case "aws_apigatewayv2_stage":
						stage = true
					}
				}
				route := x.helper(model.KindResource, "aws_apigatewayv2_route", fn.Name+"_route")
				setAttr(&route.Body, "api_id", expr(ref(api, "id")))
				setAttr(&route.Body, "route_key", lit(routeKey))
				setAttr(&route.Body, "target", expr(fmt.Sprintf(`"integrations/${%s}"`, ref(integ, "id"))))
				if !stage {
					st := x.helper(model.KindResource, "aws_apigatewayv2_stage", api.Name+"_default")
					setAttr(&st.Body, "api_id", expr(ref(api, "id")))
					setAttr(&st.Body, "name", lit("$default"))
					setAttr(&st.Body, "auto_deploy", lit(true))
				}
				x.permission(fn, api, "apigateway.amazonaws.com", fmt.Sprintf(`"${%s}/*/*"`, ref(api, "execution_arn")))
				return nil
			},
		},
		{
			id: "restapi-lambda", label: "Proxy REST API requests to the function",
			desc:  "Adds a {proxy+} resource, ANY method, Lambda proxy integration, deployment, \"dev\" stage and an invoke permission.",
			match: pair(isType("aws_api_gateway_rest_api"), isLambda), bidi: true,
			apply: func(x *conn) error {
				api, fn := x.src, x.dst
				pathPart := "{proxy+}"
				for _, n := range x.b.all() {
					if n.Kind == model.KindResource && n.Type == "aws_api_gateway_resource" {
						if a := getAttr(&n.Body, "rest_api_id"); a != nil && referencesNode(valueSource(a.Value), address(api)) {
							if pp := getAttr(&n.Body, "path_part"); pp != nil && strings.Trim(valueSource(pp.Value), `"`) == pathPart {
								pathPart = strings.ReplaceAll(fn.Name, "_", "-")
							}
						}
					}
				}
				res := x.helper(model.KindResource, "aws_api_gateway_resource", fn.Name+"_resource")
				setAttr(&res.Body, "rest_api_id", expr(ref(api, "id")))
				setAttr(&res.Body, "parent_id", expr(ref(api, "root_resource_id")))
				setAttr(&res.Body, "path_part", lit(pathPart))

				method := x.helper(model.KindResource, "aws_api_gateway_method", fn.Name+"_method")
				setAttr(&method.Body, "rest_api_id", expr(ref(api, "id")))
				setAttr(&method.Body, "resource_id", expr(ref(res, "id")))
				setAttr(&method.Body, "http_method", lit("ANY"))
				setAttr(&method.Body, "authorization", lit("NONE"))

				integ := x.helper(model.KindResource, "aws_api_gateway_integration", fn.Name+"_integration")
				setAttr(&integ.Body, "rest_api_id", expr(ref(api, "id")))
				setAttr(&integ.Body, "resource_id", expr(ref(res, "id")))
				setAttr(&integ.Body, "http_method", expr(ref(method, "http_method")))
				setAttr(&integ.Body, "integration_http_method", lit("POST"))
				setAttr(&integ.Body, "type", lit("AWS_PROXY"))
				setAttr(&integ.Body, "uri", expr(ref(fn, "invoke_arn")))

				dep := x.helper(model.KindResource, "aws_api_gateway_deployment", api.Name+"_deployment")
				setAttr(&dep.Body, "rest_api_id", expr(ref(api, "id")))
				setAttr(&dep.Body, "triggers", expr(fmt.Sprintf("{\n  redeployment = sha1(jsonencode([%s, %s, %s]))\n}", ref(res, "id"), ref(method, "id"), ref(integ, "id"))))
				lc := model.Block{Type: "lifecycle", Body: emptyBody()}
				setAttr(&lc.Body, "create_before_destroy", lit(true))
				dep.Body.Blocks = append(dep.Body.Blocks, lc)

				st := x.helper(model.KindResource, "aws_api_gateway_stage", api.Name+"_dev")
				setAttr(&st.Body, "rest_api_id", expr(ref(api, "id")))
				setAttr(&st.Body, "deployment_id", expr(ref(dep, "id")))
				setAttr(&st.Body, "stage_name", lit("dev"))

				x.permission(fn, api, "apigateway.amazonaws.com", fmt.Sprintf(`"${%s}/*/*"`, ref(api, "execution_arn")))
				return nil
			},
		},
		{
			id: "eventrule-lambda", label: "Invoke the function when the rule fires",
			desc:  "Adds an EventBridge target and a Lambda permission for EventBridge.",
			match: pair(isType("aws_cloudwatch_event_rule"), isLambda), bidi: true,
			apply: func(x *conn) error {
				rule, fn := x.src, x.dst
				x.permission(fn, rule, "events.amazonaws.com", ref(rule, "arn"))
				x.eventTarget(rule, fn)
				return nil
			},
		},
		{
			id: "eventrule-sqs", label: "Send matching events to the queue",
			desc:  "Adds an EventBridge target and a queue policy that allows EventBridge to send messages.",
			match: pair(isType("aws_cloudwatch_event_rule"), isQueue), bidi: true,
			apply: func(x *conn) error {
				rule, q := x.src, x.dst
				x.eventTarget(rule, q)
				return x.allowService(q, "events.amazonaws.com", rule, []string{"sqs:SendMessage"})
			},
		},
		{
			id: "eventrule-sns", label: "Publish matching events to the topic",
			desc:  "Adds an EventBridge target and a topic policy that allows EventBridge to publish.",
			match: pair(isType("aws_cloudwatch_event_rule"), isTopic), bidi: true,
			apply: func(x *conn) error {
				rule, t := x.src, x.dst
				x.eventTarget(rule, t)
				return x.allowService(t, "events.amazonaws.com", rule, []string{"sns:Publish"})
			},
		},
		{
			id: "scheduler-lambda", label: "Invoke the function on the schedule",
			desc:  "Points the schedule's target at the function and lets the schedule's role invoke it.",
			match: pair(isType("aws_scheduler_schedule"), isLambda), bidi: true,
			apply: func(x *conn) error {
				sched, fn := x.src, x.dst
				target := ensureBlock(&sched.Body, "target")
				setAttr(&target.Body, "arn", expr(ref(fn, "arn")))
				var role *model.Node
				if a := getAttr(&target.Body, "role_arn"); a != nil {
					if n, _ := x.b.resolve(a.Value); n != nil && n.Type == "aws_iam_role" {
						role = n
					}
				}
				if role == nil {
					role = x.helper(model.KindResource, "aws_iam_role", sched.Name+"_role")
					setAttr(&role.Body, "name", x.b.nameValue("aws_iam_role", strings.ReplaceAll(role.Name, "_", "-")))
					setAttr(&role.Body, "assume_role_policy", expr(assumeRolePolicy("scheduler.amazonaws.com")))
					setAttr(&target.Body, "role_arn", expr(ref(role, "arn")))
				}
				x.policyOnRole(role, sched.Name+"_invoke_"+fn.Name, []stmt{{actions: []string{"lambda:InvokeFunction"}, resources: []string{ref(fn, "arn")}}})
				return nil
			},
		},
		{
			id: "lb-target-group-instance", label: "Register the instance in the target group",
			desc:  "Adds a target group attachment for the instance.",
			match: pair(isType("aws_lb_target_group"), isType("aws_instance")), bidi: true,
			apply: func(x *conn) error {
				tg, inst := x.src, x.dst
				att := x.helper(model.KindResource, "aws_lb_target_group_attachment", inst.Name+"_attachment")
				setAttr(&att.Body, "target_group_arn", expr(ref(tg, "arn")))
				setAttr(&att.Body, "target_id", expr(ref(inst, "id")))
				return nil
			},
		},
		{
			id: "lb-target-group-lambda", label: "Register the function in the target group",
			desc:  "Sets the target group type to lambda, adds an attachment and a Lambda permission for Elastic Load Balancing.",
			match: pair(isType("aws_lb_target_group"), isLambda), bidi: true,
			apply: func(x *conn) error {
				tg, fn := x.src, x.dst
				setAttr(&tg.Body, "target_type", lit("lambda"))
				perm := x.permission(fn, tg, "elasticloadbalancing.amazonaws.com", ref(tg, "arn"))
				att := x.helper(model.KindResource, "aws_lb_target_group_attachment", fn.Name+"_attachment")
				setAttr(&att.Body, "target_group_arn", expr(ref(tg, "arn")))
				setAttr(&att.Body, "target_id", expr(ref(fn, "arn")))
				dependOn(att, perm)
				return nil
			},
		},
	}

	for _, g := range grants {
		rs = append(rs, recipe{
			id:    "lambda-uses-" + strings.TrimPrefix(g.typ, "aws_"),
			label: "Let the function use this " + strings.ToLower(strings.ReplaceAll(g.word, "_", " ")),
			desc:  fmt.Sprintf("Grants the function's role access and passes the %s to the code as an environment variable.", g.envAttr),
			match: pair(isLambda, isType(g.typ)), bidi: true,
			apply: func(x *conn) error {
				fn, t := x.src, x.dst
				x.rolePolicy(fn, t, g.statements(t))
				setEnv(fn, envName(t.Name, g.word, g.envSuffix), ref(t, g.envAttr))
				return nil
			},
		})
	}

	// Plain placement recipes: the dragged resource goes into the network,
	// resource group, topic or compartment it was dropped on.
	place := func(id, label, srcType, dstType, attr, refAttr string) recipe {
		return recipe{
			id: id, label: label,
			desc:  fmt.Sprintf("Sets %s to the %s's %s.", attr, strings.TrimPrefix(dstType, "aws_"), refAttr),
			match: pair(isType(srcType), isType(dstType)), bidi: true,
			sets: []string{"src." + attr},
			apply: func(x *conn) error {
				setAttr(&x.src.Body, attr, expr(ref(x.dst, refAttr)))
				return nil
			},
		}
	}
	rs = append(rs,
		place("subnet-in-vpc", "Place the subnet in the VPC", "aws_subnet", "aws_vpc", "vpc_id", "id"),
		place("security-group-in-vpc", "Place the security group in the VPC", "aws_security_group", "aws_vpc", "vpc_id", "id"),
		place("igw-in-vpc", "Attach the internet gateway to the VPC", "aws_internet_gateway", "aws_vpc", "vpc_id", "id"),
		place("route-table-in-vpc", "Place the route table in the VPC", "aws_route_table", "aws_vpc", "vpc_id", "id"),
		place("instance-in-subnet", "Launch the instance in the subnet", "aws_instance", "aws_subnet", "subnet_id", "id"),
		place("pubsub-topic-subscription", "Subscribe to the topic", "google_pubsub_subscription", "google_pubsub_topic", "topic", "id"),
		recipe{
			id: "instance-security-group", label: "Protect the instance with the security group",
			desc:  "Adds the security group to the instance's vpc_security_group_ids.",
			match: pair(isType("aws_instance"), isType("aws_security_group")), bidi: true,
			sets: []string{"src.vpc_security_group_ids"},
			apply: func(x *conn) error {
				var cur *model.Value
				if a := getAttr(&x.src.Body, "vpc_security_group_ids"); a != nil {
					cur = &a.Value
				}
				setAttr(&x.src.Body, "vpc_security_group_ids", appendList(cur, ref(x.dst, "id")))
				return nil
			},
		},
		recipe{
			id: "azure-resource-group", label: "Place in the resource group",
			desc: "Sets resource_group_name and location from the resource group.",
			match: func(c *Catalog, src, dst *model.Node) bool {
				return isType("azurerm_resource_group")(src) && dst.Kind == model.KindResource &&
					strings.HasPrefix(dst.Type, "azurerm_") && c.hasSettableAttr(dst, "resource_group_name")
			},
			bidi: true,
			sets: []string{"dst.resource_group_name", "dst.location"},
			apply: func(x *conn) error {
				rg, res := x.src, x.dst
				setAttr(&res.Body, "resource_group_name", expr(ref(rg, "name")))
				if x.c.hasSettableAttr(res, "location") {
					setAttr(&res.Body, "location", expr(ref(rg, "location")))
				}
				return nil
			},
		},
		recipe{
			id: "oci-compartment", label: "Place in the compartment",
			desc: "Sets compartment_id to the compartment's OCID.",
			match: func(c *Catalog, src, dst *model.Node) bool {
				return isType("oci_identity_compartment")(src) && dst.Kind == model.KindResource &&
					strings.HasPrefix(dst.Type, "oci_") && c.hasSettableAttr(dst, "compartment_id")
			},
			bidi: true,
			sets: []string{"dst.compartment_id"},
			apply: func(x *conn) error {
				setAttr(&x.dst.Body, "compartment_id", expr(ref(x.src, "id")))
				return nil
			},
		},
	)
	return rs
}

func (c *Catalog) hasSettableAttr(n *model.Node, name string) bool {
	s, ok := c.Schema(n.Kind, n.Type)
	if !ok {
		return false
	}
	for _, a := range s.Block.Attributes {
		if a.Name == name {
			return a.Required || a.Optional
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Recipe building blocks

type stmt struct {
	actions   []string
	resources []string // HCL expressions
}

func policyJSON(stmts []stmt) string {
	var sb strings.Builder
	sb.WriteString("jsonencode({\n  Version = \"2012-10-17\"\n  Statement = [\n")
	for _, s := range stmts {
		actions := make([]string, len(s.actions))
		for i, a := range s.actions {
			actions[i] = hclString(a)
		}
		fmt.Fprintf(&sb, "    {\n      Effect   = \"Allow\"\n      Action   = [%s]\n      Resource = [%s]\n    },\n",
			strings.Join(actions, ", "), strings.Join(s.resources, ", "))
	}
	sb.WriteString("  ]\n})")
	return sb.String()
}

func assumeRolePolicy(service string) string {
	return fmt.Sprintf("jsonencode({\n  Version = \"2012-10-17\"\n  Statement = [{\n    Effect    = \"Allow\"\n    Principal = { Service = %q }\n    Action    = \"sts:AssumeRole\"\n  }]\n})", service)
}

// lambdaRole resolves the aws_iam_role referenced by a function's role.
func (x *conn) lambdaRole(fn *model.Node) *model.Node {
	a := getAttr(&fn.Body, "role")
	if a == nil {
		return nil
	}
	n, _ := x.b.resolve(a.Value)
	if n == nil || n.Type != "aws_iam_role" {
		return nil
	}
	return n
}

// rolePolicy grants the function's role the statements on target. It returns
// nil when the function's role is not an aws_iam_role node of the workflow.
func (x *conn) rolePolicy(fn, target *model.Node, stmts []stmt) *model.Node {
	role := x.lambdaRole(fn)
	if role == nil {
		return nil
	}
	return x.policyOnRole(role, fn.Name+"_"+target.Name, stmts)
}

func (x *conn) policyOnRole(role *model.Node, hint string, stmts []stmt) *model.Node {
	p := x.helper(model.KindResource, "aws_iam_role_policy", hint)
	setAttr(&p.Body, "name", lit(strings.ReplaceAll(p.Name, "_", "-")))
	setAttr(&p.Body, "role", expr(ref(role, "id")))
	setAttr(&p.Body, "policy", expr(policyJSON(stmts)))
	return p
}

func dependOn(n, on *model.Node) {
	if on == nil {
		return
	}
	var cur *model.Value
	if a := getAttr(&n.Body, "depends_on"); a != nil {
		cur = &a.Value
	}
	setAttr(&n.Body, "depends_on", appendList(cur, address(on)))
}

func (x *conn) eventSourceMapping(source, fn *model.Node, sourceArn string) *model.Node {
	esm := x.helper(model.KindResource, "aws_lambda_event_source_mapping", source.Name+"_to_"+fn.Name)
	setAttr(&esm.Body, "event_source_arn", expr(sourceArn))
	setAttr(&esm.Body, "function_name", expr(ref(fn, "arn")))
	return esm
}

func (x *conn) permission(fn, source *model.Node, principal, sourceArn string) *model.Node {
	p := x.helper(model.KindResource, "aws_lambda_permission", "allow_"+source.Name+"_"+fn.Name)
	setAttr(&p.Body, "statement_id", lit(strings.ReplaceAll(p.Name, "_", "-")))
	setAttr(&p.Body, "action", lit("lambda:InvokeFunction"))
	setAttr(&p.Body, "function_name", expr(ref(fn, "function_name")))
	setAttr(&p.Body, "principal", lit(principal))
	setAttr(&p.Body, "source_arn", expr(sourceArn))
	return p
}

func (x *conn) eventTarget(rule, target *model.Node) *model.Node {
	t := x.helper(model.KindResource, "aws_cloudwatch_event_target", rule.Name+"_to_"+target.Name)
	setAttr(&t.Body, "rule", expr(ref(rule, "name")))
	setAttr(&t.Body, "arn", expr(ref(target, "arn")))
	return t
}

// resourcePolicies maps a policy-holding type to its policy attachment
// resource and the attribute identifying the holder.
var resourcePolicies = map[string]struct{ attachType, attachAttr, holderAttr string }{
	"aws_sqs_queue": {"aws_sqs_queue_policy", "queue_url", "url"},
	"aws_sns_topic": {"aws_sns_topic_policy", "arn", "arn"},
}

// allowService adds a statement allowing principal service (scoped to
// source's ARN) to perform actions on holder. Statements accumulate in one
// aws_iam_policy_document per holder because a queue or topic has a single
// resource policy.
func (x *conn) allowService(holder *model.Node, service string, source *model.Node, actions []string) error {
	rp := resourcePolicies[holder.Type]
	var doc *model.Node
	for _, n := range x.b.all() {
		if n.Kind != model.KindResource || n.Type != rp.attachType {
			continue
		}
		a := getAttr(&n.Body, rp.attachAttr)
		if a == nil || !referencesNode(valueSource(a.Value), address(holder)) {
			continue
		}
		pol := getAttr(&n.Body, "policy")
		if pol != nil {
			if d, _ := x.b.resolve(pol.Value); d != nil && d.Kind == model.KindData && d.Type == "aws_iam_policy_document" {
				doc = d
			}
		}
		if doc == nil {
			return fmt.Errorf("catalog: %s already has a policy (%s) that is not built from an aws_iam_policy_document; add the permission there", address(holder), address(n))
		}
	}
	if doc == nil {
		doc = x.helper(model.KindData, "aws_iam_policy_document", holder.Name+"_policy")
		att := x.helper(model.KindResource, rp.attachType, holder.Name+"_policy")
		setAttr(&att.Body, rp.attachAttr, expr(ref(holder, rp.holderAttr)))
		setAttr(&att.Body, "policy", expr(ref(doc, "json")))
	}
	sid := "Allow" + camel(source.Name) + "To" + camel(strings.SplitN(actions[0], ":", 2)[1])
	for _, blk := range doc.Body.Blocks {
		if a := getAttr(&blk.Body, "sid"); blk.Type == "statement" && a != nil && strings.Trim(valueSource(a.Value), `"`) == sid {
			return nil
		}
	}
	st := model.Block{Type: "statement", Body: emptyBody()}
	setAttr(&st.Body, "sid", lit(sid))
	setAttr(&st.Body, "effect", lit("Allow"))
	setAttr(&st.Body, "actions", lit(actions))
	setAttr(&st.Body, "resources", expr("["+ref(holder, "arn")+"]"))
	pr := model.Block{Type: "principals", Body: emptyBody()}
	setAttr(&pr.Body, "type", lit("Service"))
	setAttr(&pr.Body, "identifiers", lit([]string{service}))
	cond := model.Block{Type: "condition", Body: emptyBody()}
	setAttr(&cond.Body, "test", lit("ArnEquals"))
	setAttr(&cond.Body, "variable", lit("aws:SourceArn"))
	setAttr(&cond.Body, "values", expr("["+ref(source, "arn")+"]"))
	st.Body.Blocks = append(st.Body.Blocks, pr, cond)
	doc.Body.Blocks = append(doc.Body.Blocks, st)
	return nil
}

func camel(s string) string {
	var sb strings.Builder
	for _, w := range strings.FieldsFunc(s, func(r rune) bool { return r == '_' || r == '-' || r == ' ' }) {
		sb.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return sb.String()
}

// setEnv sets one entry of a function's environment.variables map.
func setEnv(fn *model.Node, key, valueSrc string) {
	env := ensureBlock(&fn.Body, "environment")
	var cur *model.Value
	if a := getAttr(&env.Body, "variables"); a != nil {
		cur = &a.Value
	}
	setAttr(&env.Body, "variables", mergeObject(cur, key, valueSrc))
}

// envName builds ORDERS_TABLE_NAME from ("orders", "TABLE", "NAME") and
// TABLE_NAME from ("table", "TABLE", "NAME").
func envName(nodeName, word, suffix string) string {
	base := strings.ToUpper(nonIdent.ReplaceAllString(nodeName, "_"))
	if base != word && !strings.HasSuffix(base, "_"+word) {
		base += "_" + word
	}
	return base + "_" + suffix
}
