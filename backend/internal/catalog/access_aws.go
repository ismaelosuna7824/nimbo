package catalog

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// AWS grants: dragging an identity (IAM role, Lambda function, ECS task
// definition, IAM user or group) onto a resource writes a least-privilege
// statement scoped to the resource's ARN into the identity's existing
// editable inline policy (the first aws_iam_role_policy / user / group
// policy of its role, user or group; a policy built from an
// aws_iam_policy_document is extended there), or into a new inline policy.
// An Allow statement with the same resources gets the missing actions;
// actions already granted are skipped.

type awsGranter struct{}

func init() { registerGranter(awsGranter{}) }

// awsGrantStmt is one statement of a grant: actions on the resource's ARN
// followed by each suffix ("" = the ARN itself, "/*", ":*").
type awsGrantStmt struct {
	actions  []string
	suffixes []string
}

// awsSARRes is a resource type of the Service Authorization Reference and
// the ARN suffix that addresses it from the Terraform resource's arn.
type awsSARRes struct{ prefix, rtype, suffix string }

// awsGrantSpec is the curated least privilege of a resource type. Manage
// adds every action of the reference that applies to sar's resource types.
type awsGrantSpec struct {
	read, write []awsGrantStmt
	sar         []awsSARRes
}

func awsActs(prefix string, names ...string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + ":" + n
	}
	return out
}

func awsOn(actions []string, suffixes ...string) awsGrantStmt {
	if len(suffixes) == 0 {
		suffixes = []string{""}
	}
	return awsGrantStmt{actions: actions, suffixes: suffixes}
}

var awsCuratedGrants = map[string]awsGrantSpec{
	"aws_dynamodb_table": {
		read:  []awsGrantStmt{awsOn(awsActs("dynamodb", "GetItem", "BatchGetItem", "Query", "Scan", "DescribeTable"), "", "/index/*")},
		write: []awsGrantStmt{awsOn(awsActs("dynamodb", "GetItem", "BatchGetItem", "Query", "Scan", "DescribeTable", "PutItem", "UpdateItem", "DeleteItem", "BatchWriteItem", "ConditionCheckItem"), "", "/index/*")},
		sar:   []awsSARRes{{"dynamodb", "table", ""}, {"dynamodb", "index", "/index/*"}},
	},
	"aws_s3_bucket": {
		read:  []awsGrantStmt{awsOn(awsActs("s3", "GetObject"), "/*"), awsOn(awsActs("s3", "ListBucket"))},
		write: []awsGrantStmt{awsOn(awsActs("s3", "GetObject", "PutObject", "DeleteObject"), "/*"), awsOn(awsActs("s3", "ListBucket"))},
		sar:   []awsSARRes{{"s3", "bucket", ""}, {"s3", "object", "/*"}},
	},
	"aws_sqs_queue": {
		read:  []awsGrantStmt{awsOn(awsActs("sqs", "ReceiveMessage", "DeleteMessage", "ChangeMessageVisibility", "GetQueueAttributes", "GetQueueUrl"))},
		write: []awsGrantStmt{awsOn(awsActs("sqs", "SendMessage", "GetQueueAttributes", "GetQueueUrl"))},
		sar:   []awsSARRes{{"sqs", "queue", ""}},
	},
	"aws_sns_topic": {
		read:  []awsGrantStmt{awsOn(awsActs("sns", "GetTopicAttributes", "ListSubscriptionsByTopic"))},
		write: []awsGrantStmt{awsOn(awsActs("sns", "Publish"))},
		sar:   []awsSARRes{{"sns", "topic", ""}},
	},
	"aws_cloudwatch_log_group": {
		read:  []awsGrantStmt{awsOn(awsActs("logs", "DescribeLogStreams", "FilterLogEvents", "GetLogEvents"), "", ":*")},
		write: []awsGrantStmt{awsOn(awsActs("logs", "CreateLogStream", "PutLogEvents"), ":*")},
		sar:   []awsSARRes{{"logs", "log-group", ""}, {"logs", "log-stream", ":*"}},
	},
	"aws_secretsmanager_secret": {
		read:  []awsGrantStmt{awsOn(awsActs("secretsmanager", "GetSecretValue", "DescribeSecret"))},
		write: []awsGrantStmt{awsOn(awsActs("secretsmanager", "GetSecretValue", "DescribeSecret", "PutSecretValue"))},
		sar:   []awsSARRes{{"secretsmanager", "Secret", ""}},
	},
	"aws_kms_key": {
		read:  []awsGrantStmt{awsOn(awsActs("kms", "Decrypt", "DescribeKey"))},
		write: []awsGrantStmt{awsOn(awsActs("kms", "Encrypt", "Decrypt", "GenerateDataKey", "ReEncryptFrom", "ReEncryptTo", "DescribeKey"))},
		sar:   []awsSARRes{{"kms", "key", ""}},
	},
	"aws_kinesis_stream": {
		read:  []awsGrantStmt{awsOn(awsActs("kinesis", "GetRecords", "GetShardIterator", "DescribeStream", "DescribeStreamSummary", "ListShards"))},
		write: []awsGrantStmt{awsOn(awsActs("kinesis", "PutRecord", "PutRecords", "DescribeStreamSummary"))},
		sar:   []awsSARRes{{"kinesis", "stream", ""}},
	},
	"aws_lambda_function": {
		read:  []awsGrantStmt{awsOn(awsActs("lambda", "GetFunction", "GetFunctionConfiguration"))},
		write: []awsGrantStmt{awsOn(awsActs("lambda", "InvokeFunction"))},
		sar:   []awsSARRes{{"lambda", "function", ""}},
	},
	"aws_sfn_state_machine": {
		read:  []awsGrantStmt{awsOn(awsActs("states", "DescribeStateMachine", "ListExecutions"))},
		write: []awsGrantStmt{awsOn(awsActs("states", "StartExecution", "StartSyncExecution"))},
		sar:   []awsSARRes{{"states", "stateMachine", ""}},
	},
	"aws_ecr_repository": {
		read:  []awsGrantStmt{awsOn(awsActs("ecr", "BatchGetImage", "GetDownloadUrlForLayer", "BatchCheckLayerAvailability", "DescribeImages"))},
		write: []awsGrantStmt{awsOn(awsActs("ecr", "BatchGetImage", "GetDownloadUrlForLayer", "BatchCheckLayerAvailability", "DescribeImages", "PutImage", "InitiateLayerUpload", "UploadLayerPart", "CompleteLayerUpload"))},
		sar:   []awsSARRes{{"ecr", "repository", ""}},
	},
	"aws_ssm_parameter": {
		read:  []awsGrantStmt{awsOn(awsActs("ssm", "GetParameter", "GetParameters"))},
		write: []awsGrantStmt{awsOn(awsActs("ssm", "GetParameter", "GetParameters", "PutParameter"))},
		sar:   []awsSARRes{{"ssm", "parameter", ""}},
	},
	"aws_cloudwatch_event_bus": {
		read:  []awsGrantStmt{awsOn(awsActs("events", "DescribeEventBus"))},
		write: []awsGrantStmt{awsOn(awsActs("events", "PutEvents"))},
		sar:   []awsSARRes{{"events", "event-bus", ""}},
	},
}

// awsSARTypes maps Terraform types whose name doesn't follow
// aws_<service prefix>_<resource type> to the reference.
var awsSARTypes = map[string]awsSARRes{
	"aws_cloudwatch_metric_alarm":          {"cloudwatch", "alarm", ""},
	"aws_db_instance":                      {"rds", "db", ""},
	"aws_rds_cluster":                      {"rds", "cluster", ""},
	"aws_efs_file_system":                  {"elasticfilesystem", "file-system", ""},
	"aws_cognito_user_pool":                {"cognito-idp", "userpool", ""},
	"aws_kinesis_firehose_delivery_stream": {"firehose", "deliverystream", ""},
	"aws_opensearch_domain":                {"es", "domain", ""},
	"aws_elasticsearch_domain":             {"es", "domain", ""},
	"aws_elasticache_replication_group":    {"elasticache", "replicationgroup", ""},
	"aws_sfn_activity":                     {"states", "activity", ""},
}

// awsSpecOf returns the grant spec of a resource type: curated, or derived
// from the reference for an AWS type with an ARN whose service and
// resource type the reference knows. IAM types are identities, not
// resources.
func (c *Catalog) awsSpecOf(n *model.Node) (awsGrantSpec, bool) {
	if n.Kind != model.KindResource || !strings.HasPrefix(n.Type, "aws_") || strings.HasPrefix(n.Type, "aws_iam_") {
		return awsGrantSpec{}, false
	}
	if s, ok := awsCuratedGrants[n.Type]; ok {
		return s, true
	}
	if !c.hasArn(n.Type) {
		return awsGrantSpec{}, false
	}
	res, ok := awsSARTypes[n.Type]
	if ok {
		svc, found := access.AWSServiceByPrefix(res.prefix)
		if !found {
			return awsGrantSpec{}, false
		}
		if res.rtype, ok = svc.ResourceType(res.rtype); !ok {
			return awsGrantSpec{}, false
		}
	} else if res, ok = awsGuessSAR(n.Type); !ok {
		return awsGrantSpec{}, false
	}
	svc, _ := access.AWSServiceByPrefix(res.prefix)
	spec := awsGrantSpec{sar: []awsSARRes{res}}
	if acts := svc.ActionsOn(res.rtype, "List", "Read"); len(acts) > 0 {
		spec.read = []awsGrantStmt{awsOn(acts)}
	}
	if acts := svc.ActionsOn(res.rtype, "Write"); len(acts) > 0 {
		spec.write = []awsGrantStmt{awsOn(acts)}
	}
	return spec, true
}

// awsGuessSAR splits aws_<service>_<resource> at every underscore and
// looks the halves up in the reference ("aws_glue_job" -> glue/job).
func awsGuessSAR(typ string) (awsSARRes, bool) {
	parts := strings.Split(strings.TrimPrefix(typ, "aws_"), "_")
	for i := 1; i < len(parts); i++ {
		rest := strings.Join(parts[i:], "_")
		for _, prefix := range []string{strings.Join(parts[:i], "-"), strings.Join(parts[:i], "")} {
			svc, ok := access.AWSServiceByPrefix(prefix)
			if !ok {
				continue
			}
			if rt, ok := svc.ResourceType(rest); ok {
				return awsSARRes{prefix: prefix, rtype: rt}, true
			}
		}
	}
	return awsSARRes{}, false
}

func (c *Catalog) hasArn(typ string) bool {
	s, ok := c.Schema(model.KindResource, typ)
	if !ok {
		return false
	}
	return slices.ContainsFunc(s.Block.Attributes, func(a model.SchemaAttribute) bool { return a.Name == "arn" })
}

// statements returns the grant of level as statements on res.
func (s awsGrantSpec) statements(level model.AccessLevel, res *model.Node) []model.AWSStatement {
	var stmts []awsGrantStmt
	switch level {
	case model.AccessRead:
		stmts = s.read
	case model.AccessWrite:
		stmts = s.write
	case model.AccessManage:
		bySuffix := map[string][]string{}
		var order []string
		addTo := func(suffix string, acts []string) {
			if _, ok := bySuffix[suffix]; !ok {
				order = append(order, suffix)
			}
			for _, a := range acts {
				if !slices.Contains(bySuffix[suffix], a) {
					bySuffix[suffix] = append(bySuffix[suffix], a)
				}
			}
		}
		for _, r := range s.sar {
			if svc, ok := access.AWSServiceByPrefix(r.prefix); ok {
				addTo(r.suffix, svc.ActionsOn(r.rtype))
			}
		}
		for _, st := range slices.Concat(s.read, s.write) {
			for _, suf := range st.suffixes {
				addTo(suf, st.actions)
			}
		}
		for _, suf := range order {
			acts := slices.Clone(bySuffix[suf])
			slices.Sort(acts)
			stmts = append(stmts, awsOn(acts, suf))
		}
	}
	arn := ref(res, "arn")
	out := make([]model.AWSStatement, 0, len(stmts))
	for _, st := range stmts {
		if len(st.actions) == 0 {
			continue
		}
		s := model.AWSStatement{Effect: "Allow", Actions: slices.Clone(st.actions), Editable: true}
		for _, suf := range st.suffixes {
			if suf == "" {
				s.Resources = append(s.Resources, arn)
			} else {
				s.Resources = append(s.Resources, `"${`+arn+`}`+suf+`"`)
			}
		}
		out = append(out, s)
	}
	return out
}

// awsIdentity is the IAM principal behind an identity node and where its
// inline policies live.
type awsIdentity struct {
	principal  *model.Node // aws_iam_role, aws_iam_user or aws_iam_group
	policyType string      // aws_iam_role_policy, ...
	attr       string      // attribute of the policy naming the principal
	refAttr    string      // attribute of the principal it references
}

func awsIdentityOf(b *builder, n *model.Node) (awsIdentity, bool) {
	if n.Kind != model.KindResource {
		return awsIdentity{}, false
	}
	role := func(attr string) (awsIdentity, bool) {
		a := getAttr(&n.Body, attr)
		if a == nil {
			return awsIdentity{}, false
		}
		r, _ := b.resolve(a.Value)
		if r == nil || r.Kind != model.KindResource || r.Type != "aws_iam_role" {
			return awsIdentity{}, false
		}
		return awsIdentity{r, "aws_iam_role_policy", "role", "id"}, true
	}
	switch n.Type {
	case "aws_iam_role":
		return awsIdentity{n, "aws_iam_role_policy", "role", "id"}, true
	case "aws_iam_user":
		return awsIdentity{n, "aws_iam_user_policy", "user", "name"}, true
	case "aws_iam_group":
		return awsIdentity{n, "aws_iam_group_policy", "group", "name"}, true
	case "aws_lambda_function":
		return role("role")
	case "aws_ecs_task_definition":
		return role("task_role_arn")
	}
	return awsIdentity{}, false
}

// awsTarget is where a grant writes: an existing policy's document, or a
// new inline policy (node nil; name is its future address).
type awsTarget struct {
	node *model.Node
	path string
	addr string
}

// policies are the identity's inline policies of its policy type.
func (id awsIdentity) policies(b *builder) []*model.Node {
	var out []*model.Node
	for _, n := range b.all() {
		if n.Kind == model.KindResource && n.Type == id.policyType && access.AWSRefersTo(n, id.attr, id.principal) {
			out = append(out, n)
		}
	}
	return out
}

func (id awsIdentity) target(b *builder) awsTarget {
	all := b.all()
	for _, n := range id.policies(b) {
		doc, ok := access.AWSDocument(all, n, "policy")
		if !ok {
			continue
		}
		if doc.Editable {
			return awsTarget{node: n, path: "policy", addr: address(n)}
		}
		if doc.Encoding == "reference" {
			for _, d := range all {
				if d.ID == doc.Linked {
					return awsTarget{node: d, path: "", addr: address(d)}
				}
			}
		}
	}
	name := b.uniqueName(model.KindResource, id.policyType, id.principal.Name+"_access")
	return awsTarget{addr: id.policyType + "." + name}
}

// granted reports whether some Allow statement of the identity's policies
// (and target) allows action on resource (exact names and expressions).
func (id awsIdentity) granted(b *builder, t awsTarget) func(action, resource string) bool {
	all := b.all()
	var stmts []model.AWSStatement
	for _, n := range id.policies(b) {
		if doc, ok := access.AWSDocument(all, n, "policy"); ok {
			stmts = append(stmts, doc.Statements...)
		}
	}
	if t.node != nil && t.path == "" {
		if doc, ok := access.AWSDocument(all, t.node, ""); ok {
			stmts = append(stmts, doc.Statements...)
		}
	}
	return func(action, resource string) bool {
		for _, s := range stmts {
			if s.Effect != "Allow" || len(s.Principals) > 0 || len(s.Extra) > 0 {
				continue
			}
			if slices.ContainsFunc(s.Actions, func(a string) bool { return strings.EqualFold(a, action) }) &&
				slices.ContainsFunc(s.Resources, func(r string) bool { return strings.TrimSpace(r) == resource }) {
				return true
			}
		}
		return false
	}
}

// awsMissing drops the actions already granted on every resource of a
// statement, and statements left empty.
func awsMissing(stmts []model.AWSStatement, granted func(action, resource string) bool) []model.AWSStatement {
	var out []model.AWSStatement
	for _, s := range stmts {
		var acts []string
		for _, a := range s.Actions {
			if !slices.ContainsFunc(s.Resources, func(r string) bool { return !granted(a, r) }) {
				continue
			}
			acts = append(acts, a)
		}
		if len(acts) > 0 {
			s.Actions = acts
			out = append(out, s)
		}
	}
	return out
}

func (awsGranter) plans(c *Catalog, wf *model.Workflow, a, b *model.Node) []accessPlan {
	bld := newBuilder(c, wf)
	if ps := c.awsPlans(bld, a, b); ps != nil {
		return ps
	}
	return c.awsPlans(bld, b, a)
}

func (c *Catalog) awsPlans(b *builder, idn, res *model.Node) []accessPlan {
	id, ok := awsIdentityOf(b, idn)
	if !ok || res == id.principal || res == idn {
		return nil
	}
	// A function is not granted access to itself through its own role.
	if rid, ok := awsIdentityOf(b, res); ok && rid.principal == id.principal {
		return nil
	}
	spec, ok := c.awsSpecOf(res)
	if !ok {
		return nil
	}
	t := id.target(b)
	granted := id.granted(b, t)
	var out []accessPlan
	for _, level := range model.AccessLevels {
		stmts := spec.statements(level, res)
		if len(stmts) == 0 {
			continue
		}
		out = append(out, accessPlan{level: level, desc: awsDesc(id, t, stmts, awsMissing(stmts, granted)), identity: idn, resource: res})
	}
	return out
}

func awsDesc(id awsIdentity, t awsTarget, all, missing []model.AWSStatement) string {
	parts := func(stmts []model.AWSStatement) string {
		ps := make([]string, len(stmts))
		for i, s := range stmts {
			ps[i] = strings.Join(s.Actions, ", ") + " on " + strings.Join(s.Resources, ", ")
		}
		return strings.Join(ps, "; ")
	}
	if len(missing) == 0 {
		return fmt.Sprintf("Already granted: %s; nothing to change.", parts(all))
	}
	where := "in " + t.addr
	if t.node == nil {
		where = fmt.Sprintf("in a new inline policy %s of %s", t.addr, address(id.principal))
	}
	return fmt.Sprintf("Allows %s (%s).", parts(missing), where)
}

func (awsGranter) apply(x *conn, p accessPlan) error {
	id, ok := awsIdentityOf(x.b, x.src)
	if !ok {
		return access.Editf("catalog: %s is not an AWS identity", address(x.src))
	}
	spec, ok := x.c.awsSpecOf(x.dst)
	if !ok {
		return access.Editf("catalog: Nimbo can't grant access to %s", address(x.dst))
	}
	t := id.target(x.b)
	stmts := awsMissing(spec.statements(p.level, x.dst), id.granted(x.b, t))
	if len(stmts) == 0 {
		return nil
	}
	node, path := t.node, t.path
	if node == nil {
		node = x.helper(model.KindResource, id.policyType, id.principal.Name+"_access")
		setAttr(&node.Body, "name", lit(strings.ReplaceAll(node.Name, "_", "-")))
		setAttr(&node.Body, id.attr, expr(ref(id.principal, id.refAttr)))
		path = "policy"
	}
	all := x.b.all()
	doc, ok := access.AWSDocument(all, node, path)
	if !ok {
		return access.Editf("catalog: %s has no policy document", address(node))
	}
	level := access.LevelLabel(p.level)
	for _, s := range stmts {
		if i := awsMergeable(doc.Statements, s.Resources); i >= 0 {
			for _, a := range s.Actions {
				if !slices.Contains(doc.Statements[i].Actions, a) {
					doc.Statements[i].Actions = append(doc.Statements[i].Actions, a)
				}
			}
			continue
		}
		s.Sid = awsSid(doc.Statements, level+camel(x.dst.Name)+awsSidSuffix(s.Resources))
		doc.Statements = append(doc.Statements, s)
	}
	return access.AWSWrite(all, node, doc)
}

// awsMergeable is the editable plain Allow statement whose resources are
// exactly resources, or -1.
func awsMergeable(stmts []model.AWSStatement, resources []string) int {
	for i, s := range stmts {
		if !s.Editable || s.Effect != "Allow" || len(s.Principals) > 0 || len(s.Extra) > 0 || len(s.Resources) != len(resources) {
			continue
		}
		same := true
		for _, r := range resources {
			if !slices.ContainsFunc(s.Resources, func(x string) bool { return strings.TrimSpace(x) == r }) {
				same = false
			}
		}
		if same {
			return i
		}
	}
	return -1
}

// awsSidSuffix tells object statements of a bucket apart ("ReadAssetsObjects").
func awsSidSuffix(resources []string) string {
	if strings.HasSuffix(resources[0], `/*"`) {
		return "Objects"
	}
	return ""
}

// awsSid makes sid unique among the statements (Sids are alphanumeric).
func awsSid(stmts []model.AWSStatement, sid string) string {
	sid = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, sid)
	taken := func(s string) bool {
		return slices.ContainsFunc(stmts, func(st model.AWSStatement) bool { return st.Sid == s })
	}
	if !taken(sid) {
		return sid
	}
	for i := 2; ; i++ {
		if s := fmt.Sprintf("%s%d", sid, i); !taken(s) {
			return s
		}
	}
}
