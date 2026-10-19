//go:build corpus

package tfqa

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Module refactoring on deployed infrastructure: apply a template on Floci,
// extract a group of its nodes into a local module (POST
// /workflows/{id}/modules/extract) and require the plan of the export to
// only move resources (0 to add, 0 to change, 0 to destroy), apply it, then
// inline the module back (POST .../modules/{node}/inline) and require the
// same again; a final plan after each apply has no changes.
//
//	go test -tags corpus -timeout 30m ./internal/tfqa/ -run TestModuleRefactorDeploy -v
//
// Needs docker (floci/floci, alpine) and host terraform.

// refactorCases name, per AWS template, the group extracted into a module.
var refactorCases = []struct {
	template, module string
	types            []string // node types (resource types; data.<type> for data sources) moved
}{
	{"aws-queue-worker", "worker", []string{"data.archive_file", "aws_iam_role", "aws_iam_role_policy_attachment", "aws_iam_role_policy", "aws_lambda_function", "aws_cloudwatch_log_group"}},
	{"aws-vpc-basics", "network", []string{"aws_subnet", "aws_internet_gateway", "aws_route_table", "aws_route", "aws_route_table_association"}},
	{"aws-serverless-api", "http_api", []string{"aws_apigatewayv2_api", "aws_apigatewayv2_integration", "aws_apigatewayv2_route", "aws_apigatewayv2_stage", "aws_lambda_permission"}},
}

func TestModuleRefactorDeploy(t *testing.T) {
	if terraformBin() == "" {
		t.Skip("terraform not on PATH")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker not available")
	}
	endpoint := startFloci(t)
	env := []string{"AWS_ENDPOINT_URL=" + endpoint, "AWS_ACCESS_KEY_ID=test", "AWS_SECRET_ACCESS_KEY=test", "AWS_REGION=us-east-1", "AWS_DEFAULT_REGION=us-east-1"}
	base := nimbo(t)
	var table strings.Builder
	table.WriteString("| template | module | moved | plan after extract | plan after inline |\n|---|---|---|---|---|\n")
	for _, tc := range refactorCases {
		t.Run(tc.template, func(t *testing.T) {
			extract, inline, moved := refactorRoundTrip(t, base, env, tc.template, tc.module, tc.types)
			fmt.Fprintf(&table, "| %s | %s | %d | %s | %s |\n", tc.template, tc.module, moved, extract, inline)
		})
	}
	t.Log("\n" + table.String())
}

func refactorRoundTrip(t *testing.T, base string, env []string, template, module string, types []string) (extract, inline string, moved int) {
	var wf model.Workflow
	if err := call(base, "POST", "/workflows", map[string]string{"name": template + "-modules", "templateId": template}, &wf); err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	run := func(args ...string) (string, error) {
		args = append(args, "-no-color")
		if args[0] != "show" && args[0] != "validate" {
			args = append(args, "-input=false")
		}
		return terraform(work, env, args...)
	}
	// swap replaces the configuration in work with the workflow's export;
	// state, .terraform and the emulator wiring stay.
	swap := func() {
		t.Helper()
		exp := t.TempDir()
		if err := exportTo(base, wf.ID, exp); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(work)
		for _, e := range entries {
			n := e.Name()
			if n == "modules" || (!e.IsDir() && n != overrideName && (strings.HasSuffix(n, ".tf") || strings.HasSuffix(n, ".tfvars"))) {
				if err := os.RemoveAll(filepath.Join(work, n)); err != nil {
					t.Fatal(err)
				}
			}
		}
		copyTree(t, exp, work)
		if out, err := initRetry(run); err != nil {
			t.Fatalf("init: %v\n%s", err, lastLines(out, 30))
		}
		if out, err := run("validate"); err != nil {
			t.Fatalf("validate: %v\n%s", err, lastLines(out, 30))
		}
	}
	if err := os.WriteFile(filepath.Join(work, overrideName), []byte(flociProviderOverride), 0o644); err != nil {
		t.Fatal(err)
	}
	swap()
	if out, err := run("apply", "-auto-approve"); err != nil {
		t.Fatalf("apply (template): %v\n%s", err, lastLines(out, 30))
	}
	defer func() {
		if out, err := run("destroy", "-auto-approve"); err != nil {
			t.Logf("destroy: %v\n%s", err, lastLines(out, 15))
		}
	}()
	if code, changes, _ := planMoves(t, run); code != 0 {
		t.Fatalf("template plans changes right after apply (emulator drift): %v", changes)
	}

	var nodes []string
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		typ := n.Type
		if n.Kind == model.KindData {
			typ = "data." + typ
		}
		if slices.Contains(types, typ) {
			nodes = append(nodes, hclcodec.Address(n))
		}
	}
	if len(nodes) < 3 {
		t.Fatalf("group %v matched only %v", types, nodes)
	}
	if err := call(base, "POST", "/workflows/"+wf.ID+"/modules/extract", model.ModuleExtractRequest{NodeIDs: nodes, Name: module}, &wf); err != nil {
		t.Fatal(err)
	}
	extract, moved = applyMoves(t, run, swap, "module."+module+".")

	var callNode *model.Node
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == "module."+module {
			callNode = &wf.Nodes[i]
		}
	}
	if callNode == nil {
		t.Fatalf("module.%s not in the workflow", module)
	}
	if err := call(base, "POST", "/workflows/"+wf.ID+"/modules/"+callNode.ID+"/inline", nil, &wf); err != nil {
		t.Fatal(err)
	}
	inline, back := applyMoves(t, run, swap, "")
	if back != moved {
		t.Errorf("inline moved %d instances back, extract moved %d", back, moved)
	}
	return extract, inline, moved
}

// applyMoves swaps in the current export, requires a plan that only moves
// resource instances (their new address starts with prefix), applies it and
// requires a clean plan afterwards. It returns the report cell and the
// number of moved instances.
func applyMoves(t *testing.T, run func(args ...string) (string, error), swap func(), prefix string) (string, int) {
	t.Helper()
	swap()
	code, changes, moves := planMoves(t, run)
	if len(changes) > 0 {
		t.Fatalf("plan changes besides moves:\n%s", strings.Join(changes, "\n"))
	}
	if len(moves) == 0 {
		t.Fatalf("plan moves nothing (exit %d)", code)
	}
	for _, m := range moves {
		if !strings.HasPrefix(m, prefix) || (prefix == "" && strings.HasPrefix(m, "module.")) {
			t.Errorf("unexpected move target %s", m)
		}
	}
	out, err := run("plan", "-out=tfqa.plan")
	if err != nil || !strings.Contains(out, "0 to add, 0 to change, 0 to destroy") {
		t.Fatalf("plan summary: %v\n%s", err, lastLines(out, 20))
	}
	if out, err := run("apply", "-auto-approve"); err != nil {
		t.Fatalf("apply moves: %v\n%s", err, lastLines(out, 30))
	}
	if code, changes, moves := planMoves(t, run); code != 0 || len(moves) > 0 {
		t.Fatalf("plan after applying the moves: exit %d, changes %v, moves %v", code, changes, moves)
	}
	return fmt.Sprintf("only moves (%d), 0 to add/change/destroy; clean after apply", len(moves)), len(moves)
}

// planMoves runs terraform plan -detailed-exitcode and returns its exit
// code, the planned changes other than moves (address, actions) and the
// new addresses of moved instances.
func planMoves(t *testing.T, run func(args ...string) (string, error)) (int, []string, []string) {
	t.Helper()
	out, err := run("plan", "-detailed-exitcode", "-out=tfqa.plan")
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	if code == 1 {
		t.Fatalf("plan failed:\n%s", lastLines(out, 30))
	}
	raw, err := run("show", "-json", "tfqa.plan")
	if err != nil {
		t.Fatalf("show: %v\n%s", err, raw)
	}
	var p struct {
		ResourceChanges []struct {
			Address         string `json:"address"`
			PreviousAddress string `json:"previous_address"`
			Change          struct {
				Actions []string `json:"actions"`
			} `json:"change"`
		} `json:"resource_changes"`
		OutputChanges map[string]struct {
			Actions []string `json:"actions"`
		} `json:"output_changes"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatalf("plan JSON: %v", err)
	}
	var changes, moves []string
	for _, rc := range p.ResourceChanges {
		if rc.PreviousAddress != "" && rc.PreviousAddress != rc.Address {
			moves = append(moves, rc.Address)
		}
		if !slices.Equal(rc.Change.Actions, []string{"no-op"}) && !slices.Equal(rc.Change.Actions, []string{"read"}) {
			changes = append(changes, fmt.Sprintf("%s %v", rc.Address, rc.Change.Actions))
		}
	}
	for name, oc := range p.OutputChanges {
		if !slices.Equal(oc.Actions, []string{"no-op"}) {
			changes = append(changes, fmt.Sprintf("output.%s %v", name, oc.Actions))
		}
	}
	slices.Sort(changes)
	slices.Sort(moves)
	return code, changes, moves
}
