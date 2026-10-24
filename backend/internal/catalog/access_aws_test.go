package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func importWF(t *testing.T, src string) *model.Workflow {
	t.Helper()
	wf, _, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	return wf
}

func grantRecipe(t *testing.T, c *Catalog, wf *model.Workflow, src, dst string, level model.AccessLevel) model.ConnectionRecipe {
	t.Helper()
	a, b := nodeByAddr(t, wf, src), nodeByAddr(t, wf, dst)
	for _, r := range c.Connections(wf, a.ID, b.ID) {
		if r.ID == access.RecipeID(level) {
			if r.Group != model.AccessRecipeGroup || r.Level != level || !r.Curated {
				t.Fatalf("recipe = %+v", r)
			}
			return r
		}
	}
	t.Fatalf("no %s grant for %s -> %s", level, src, dst)
	return model.ConnectionRecipe{}
}

func grant(t *testing.T, c *Catalog, wf *model.Workflow, src, dst string, level model.AccessLevel) {
	t.Helper()
	grantRecipe(t, c, wf, src, dst, level)
	if err := c.Connect(wf, nodeByAddr(t, wf, src).ID, nodeByAddr(t, wf, dst).ID, access.RecipeID(level)); err != nil {
		t.Fatal(err)
	}
	for _, d := range hclcodec.Normalize(wf) {
		if d.Severity == model.SeverityError {
			t.Fatalf("after %s grant: %+v", level, d)
		}
	}
}

func policyDoc(t *testing.T, wf *model.Workflow, addr, path string) model.AWSPolicyDocument {
	t.Helper()
	n := nodeByAddr(t, wf, addr)
	v, err := access.View(wf, n)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range v.AWS {
		if d.Path == path {
			return d
		}
	}
	t.Fatalf("%s has no document %q", addr, path)
	return model.AWSPolicyDocument{}
}

const grantsSrc = `resource "aws_iam_role" "app" {
  name               = "app"
  assume_role_policy = jsonencode({ Version = "2012-10-17", Statement = [{ Effect = "Allow", Principal = { Service = "ecs-tasks.amazonaws.com" }, Action = "sts:AssumeRole" }] })
}

resource "aws_iam_role_policy" "app" {
  name = "app"
  role = aws_iam_role.app.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect   = "Allow"
        Action   = ["s3:GetObject"]
        Resource = "${aws_s3_bucket.assets.arn}/*"
      },
    ]
  })
}

resource "aws_ecs_task_definition" "app" {
  family                = "app"
  task_role_arn         = aws_iam_role.app.arn
  container_definitions = "[]"
}

resource "aws_s3_bucket" "assets" {
  bucket = "assets"
}

resource "aws_dynamodb_table" "orders" {
  name     = "orders"
  hash_key = "id"
  attribute {
    name = "id"
    type = "S"
  }
}

resource "aws_athena_workgroup" "reports" {
  name = "reports"
}

resource "aws_iam_user" "ci" {
  name = "ci"
}

resource "aws_sqs_queue" "jobs" {
  name = "jobs"
}
`

func TestAWSGrantMergesIntoInlinePolicy(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, grantsSrc)
	r := grantRecipe(t, c, wf, "aws_ecs_task_definition.app", "aws_s3_bucket.assets", model.AccessWrite)
	// s3:GetObject on /* is already granted: the preview leaves it out.
	if r.Description != `Allows s3:PutObject, s3:DeleteObject on "${aws_s3_bucket.assets.arn}/*"; s3:ListBucket on aws_s3_bucket.assets.arn (in aws_iam_role_policy.app).` {
		t.Fatalf("description = %q", r.Description)
	}
	grant(t, c, wf, "aws_ecs_task_definition.app", "aws_s3_bucket.assets", model.AccessWrite)
	d := policyDoc(t, wf, "aws_iam_role_policy.app", "policy")
	if len(d.Statements) != 2 {
		t.Fatalf("statements = %+v", d.Statements)
	}
	if got := d.Statements[0].Actions; !slices.Equal(got, []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"}) {
		t.Errorf("merged actions = %v", got)
	}
	if s := d.Statements[1]; s.Sid != "WriteAssets" || !slices.Equal(s.Resources, []string{"aws_s3_bucket.assets.arn"}) || !slices.Equal(s.Actions, []string{"s3:ListBucket"}) {
		t.Errorf("new statement = %+v", s)
	}
	if n := len(nodesOfType(wf, model.KindResource, "aws_iam_role_policy")); n != 1 {
		t.Errorf("%d role policies, want the existing one", n)
	}

	// Granting again changes nothing and says so.
	r = grantRecipe(t, c, wf, "aws_iam_role.app", "aws_s3_bucket.assets", model.AccessWrite)
	if !strings.HasPrefix(r.Description, "Already granted:") {
		t.Errorf("repeat description = %q", r.Description)
	}
	before := attrExpr(t, nodeByAddr(t, wf, "aws_iam_role_policy.app"), "policy")
	grant(t, c, wf, "aws_iam_role.app", "aws_s3_bucket.assets", model.AccessWrite)
	if after := attrExpr(t, nodeByAddr(t, wf, "aws_iam_role_policy.app"), "policy"); after != before {
		t.Errorf("repeat grant changed the policy:\n%s", after)
	}

	// The other orientation (resource dragged onto the role) offers the same grants.
	grant(t, c, wf, "aws_dynamodb_table.orders", "aws_iam_role.app", model.AccessRead)
	d = policyDoc(t, wf, "aws_iam_role_policy.app", "policy")
	last := d.Statements[len(d.Statements)-1]
	if last.Sid != "ReadOrders" || !slices.Equal(last.Resources, []string{"aws_dynamodb_table.orders.arn", `"${aws_dynamodb_table.orders.arn}/index/*"`}) ||
		!slices.Contains(last.Actions, "dynamodb:GetItem") || slices.Contains(last.Actions, "dynamodb:PutItem") {
		t.Errorf("read statement = %+v", last)
	}
	if len(access.AWSWarnings(d)) != 0 {
		t.Errorf("grants produce warnings: %+v", access.AWSWarnings(d))
	}
}

func TestAWSGrantHelperPolicyAndFallback(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, grantsSrc)

	// A user without an inline policy gets one.
	r := grantRecipe(t, c, wf, "aws_iam_user.ci", "aws_sqs_queue.jobs", model.AccessWrite)
	if !strings.Contains(r.Description, "in a new inline policy aws_iam_user_policy.ci_access of aws_iam_user.ci") {
		t.Fatalf("description = %q", r.Description)
	}
	grant(t, c, wf, "aws_iam_user.ci", "aws_sqs_queue.jobs", model.AccessWrite)
	p := nodeByAddr(t, wf, "aws_iam_user_policy.ci_access")
	if attrExpr(t, p, "user") != "aws_iam_user.ci.name" || !strings.Contains(attrExpr(t, p, "policy"), `"sqs:SendMessage"`) ||
		!strings.Contains(attrExpr(t, p, "policy"), "Resource = aws_sqs_queue.jobs.arn") {
		t.Fatalf("user policy = %+v", p.Body.Attrs)
	}

	// An AWS type without curated grants uses the Service Authorization Reference.
	wfRead := importWF(t, grantsSrc)
	grant(t, c, wfRead, "aws_iam_role.app", "aws_athena_workgroup.reports", model.AccessRead)
	d := policyDoc(t, wfRead, "aws_iam_role_policy.app", "policy")
	st := d.Statements[len(d.Statements)-1]
	if !slices.Equal(st.Resources, []string{"aws_athena_workgroup.reports.arn"}) || !slices.Contains(st.Actions, "athena:GetWorkGroup") {
		t.Fatalf("fallback read = %+v", st)
	}
	for _, a := range st.Actions {
		if strings.HasSuffix(a, ":*") {
			t.Errorf("wildcard action %s", a)
		}
		if def, ok := access.AWSAction(a); !ok || (def.Level != "Read" && def.Level != "List") {
			t.Errorf("read grant has %s (%+v)", a, def)
		}
	}
	manage := grantRecipe(t, c, wfRead, "aws_iam_role.app", "aws_athena_workgroup.reports", model.AccessManage)
	if !strings.Contains(manage.Description, "athena:DeleteWorkGroup") || strings.Contains(manage.Description, "athena:*") {
		t.Errorf("manage = %q", manage.Description)
	}

	// Identities and non-AWS nodes get no grant between them.
	if rs := c.Connections(wf, nodeByAddr(t, wf, "aws_iam_role.app").ID, nodeByAddr(t, wf, "aws_iam_user.ci").ID); slices.ContainsFunc(rs, func(r model.ConnectionRecipe) bool { return r.Group == model.AccessRecipeGroup }) {
		t.Errorf("role -> user offers grants: %+v", rs)
	}
}

func TestAWSCuratedGrantActionsExist(t *testing.T) {
	for typ, spec := range awsCuratedGrants {
		for _, st := range slices.Concat(spec.read, spec.write) {
			for _, a := range st.actions {
				if _, ok := access.AWSAction(a); !ok {
					t.Errorf("%s: %s is not in the Service Authorization Reference", typ, a)
				}
			}
		}
		for _, r := range spec.sar {
			svc, ok := access.AWSServiceByPrefix(r.prefix)
			if !ok {
				t.Errorf("%s: unknown service %s", typ, r.prefix)
				continue
			}
			if _, ok := svc.ResourceType(r.rtype); !ok {
				t.Errorf("%s: %s has no resource type %s", typ, r.prefix, r.rtype)
			}
		}
	}
}

func TestLambdaLogGroupRecipe(t *testing.T) {
	c := mustLoad(t)
	wf := importWF(t, `resource "aws_lambda_function" "reader" {
  function_name = "reader"
  role          = "arn:aws:iam::123456789012:role/r"
  runtime       = "nodejs22.x"
  handler       = "index.handler"
  filename      = "x.zip"
}

resource "aws_cloudwatch_log_group" "logs" {
  name = "something"
}
`)
	connect(t, c, wf, nodeByAddr(t, wf, "aws_cloudwatch_log_group.logs"), nodeByAddr(t, wf, "aws_lambda_function.reader"), "lambda-log-group")
	g := nodeByAddr(t, wf, "aws_cloudwatch_log_group.logs")
	if got := attrExpr(t, g, "name"); got != `"/aws/lambda/${aws_lambda_function.reader.function_name}"` {
		t.Errorf("name = %s", got)
	}
	if got := attrExpr(t, g, "retention_in_days"); got != "14" {
		t.Errorf("retention = %s", got)
	}
	// A second group for the same function is refused.
	addNode(t, c, wf, "aws_cloudwatch_log_group", 0)
	other := nodesOfType(wf, model.KindResource, "aws_cloudwatch_log_group")[1]
	if err := c.Connect(wf, nodeByAddr(t, wf, "aws_lambda_function.reader").ID, other.ID, "lambda-log-group"); err == nil {
		t.Error("second log group for the same function accepted")
	}
}
