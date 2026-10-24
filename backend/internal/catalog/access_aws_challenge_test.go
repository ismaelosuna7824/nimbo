package catalog_test

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func lambdaChallenge(t *testing.T) model.Challenge {
	t.Helper()
	const file = "20261023120000_aws_beginner_challenges.yaml"
	data, err := fs.ReadFile(learn.Migrations(), file)
	if err != nil {
		t.Fatal(err)
	}
	m, err := learn.ParseMigration(file, data, challenge.Validate)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range m.Ops {
		if op.Kind == "challenge" && op.ID == "aws-lambda-least-privilege" && op.Challenge != nil {
			c := *op.Challenge
			c.ID = op.ID
			return c
		}
	}
	t.Fatal("challenge aws-lambda-least-privilege not found")
	return model.Challenge{}
}

func nodeAt(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("no node %s", addr)
	return nil
}

func offered(t *testing.T, cat *catalog.Catalog, wf *model.Workflow, src, dst, recipe string) model.ConnectionRecipe {
	t.Helper()
	rs := cat.Connections(wf, nodeAt(t, wf, src).ID, nodeAt(t, wf, dst).ID)
	i := slices.IndexFunc(rs, func(r model.ConnectionRecipe) bool { return r.ID == recipe })
	if i < 0 {
		t.Fatalf("%s not offered for %s -> %s", recipe, src, dst)
	}
	return rs[i]
}

// TestAWSLambdaChallengeSolvedByGrants solves aws-lambda-least-privilege the
// way the canvas does (add a log group, connect it to the function, drag the
// role onto the table and the log group, delete the admin statement in the
// Access section) and grades the challenge's static checks.
func TestAWSLambdaChallengeSolvedByGrants(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	c := lambdaChallenge(t)
	starter, err := challenge.ResolveStarter(cat, c.Starter)
	if err != nil {
		t.Fatal(err)
	}
	wf, err := challenge.ImportTerraform(cat, c.Title, starter.Files)
	if err != nil {
		t.Fatal(err)
	}

	groupID, err := cat.AddNode(wf, model.KindResource, "aws_cloudwatch_log_group", "", model.Position{X: 600, Y: 300})
	if err != nil {
		t.Fatal(err)
	}
	var group string
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == groupID {
			group = hclcodec.Address(&wf.Nodes[i])
		}
	}
	connect := func(src, dst, recipe string) model.ConnectionRecipe {
		t.Helper()
		r := offered(t, cat, wf, src, dst, recipe)
		if err := cat.Connect(wf, nodeAt(t, wf, src).ID, nodeAt(t, wf, dst).ID, recipe); err != nil {
			t.Fatalf("%s: %v", recipe, err)
		}
		hclcodec.Normalize(wf)
		return r
	}
	connect("aws_lambda_function.reader", group, "lambda-log-group")

	read := offered(t, cat, wf, "aws_iam_role.reader", "aws_dynamodb_table.orders", access.RecipeID(model.AccessRead))
	if !strings.Contains(read.Description, "dynamodb:GetItem") || !strings.Contains(read.Description, "aws_dynamodb_table.orders.arn") ||
		!strings.Contains(read.Description, "in aws_iam_role_policy.reader") {
		t.Errorf("read preview = %q", read.Description)
	}
	connect("aws_iam_role.reader", "aws_dynamodb_table.orders", access.RecipeID(model.AccessRead))
	write := connect("aws_iam_role.reader", group, access.RecipeID(model.AccessWrite))
	if want := `Allows logs:CreateLogStream, logs:PutLogEvents on "${` + group + `.arn}:*" (in aws_iam_role_policy.reader).`; write.Description != want {
		t.Errorf("write preview = %q, want %q", write.Description, want)
	}

	// Delete the admin statement in the role policy's Access section.
	pol := nodeAt(t, wf, "aws_iam_role_policy.reader")
	v, err := access.View(wf, pol)
	if err != nil {
		t.Fatal(err)
	}
	doc := v.AWS[0]
	if len(doc.Statements) != 3 || !slices.ContainsFunc(v.Warnings, func(w model.AccessWarning) bool { return w.Code == "aws_admin" && w.Path == "policy/statements/0" }) {
		t.Fatalf("policy before cleanup = %+v, warnings %+v", doc.Statements, v.Warnings)
	}
	doc.Statements = doc.Statements[1:]
	if err := access.Apply(wf, pol, model.AccessEdit{NodeID: pol.ID, AWS: &doc}); err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	if v, _ = access.View(wf, pol); len(v.Warnings) != 0 {
		t.Errorf("warnings after cleanup: %+v", v.Warnings)
	}

	// Grade the static checks on the exported files, as a save would.
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{ResourceFile: cat.ResourceFile})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := challenge.ImportTerraform(cat, c.Title, files)
	if err != nil {
		t.Fatal(err)
	}
	a, err := challenge.Analyze(context.Background(), cat, saved, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	static := 0
	for _, k := range c.Checks {
		if k.Type != model.CheckValidate && k.Type != model.CheckRulesClean && k.Type != model.CheckHas {
			continue
		}
		static++
		if r := challenge.GradeStatic(k, a); !r.Passed {
			t.Errorf("%s (%s) fails: %s", k.ID, k.Type, r.Detail)
		}
	}
	if static < 6 {
		t.Fatalf("only %d static checks graded", static)
	}
	if t.Failed() {
		for name, src := range files {
			if strings.HasSuffix(name, ".tf") {
				t.Logf("%s:\n%s", name, src)
			}
		}
	}
}
