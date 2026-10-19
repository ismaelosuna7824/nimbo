package mcp

import (
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPrompts adds the guided multi-step recipes agents can offer to
// the user (slash commands in most MCP clients).
func registerPrompts(r *registry) {
	r.addPrompt(&sdk.Prompt{
		Name:        "create_infrastructure",
		Title:       "Create infrastructure",
		Description: "Design, deploy and smoke-test new infrastructure from a plain-language request.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to build, e.g. \"an HTTP API that stores orders in DynamoDB\"", Required: true},
			{Name: "cloud", Description: "aws, gcp, azure or oci (default: inferred from the request, else aws)"},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		cloud := strings.TrimSpace(a["cloud"])
		if cloud == "" {
			cloud = "infer it from the request; default to aws"
		}
		return fmt.Sprintf(`Build this infrastructure with the Nimbo tools: %s
Cloud: %s.

Steps:
1. list_templates; if one is close, create_workflow from it, otherwise create_workflow empty with a short descriptive name.
2. For each service needed: search_resources (prefer floci_supported=true so it runs in the local sandbox) and describe_resource for required attributes.
3. add_node for each resource (string attributes are quoted HCL: "\"my-name\""). Keep the conventions the starters add: names "${local.name_prefix}-<role>", configurable values as typed, described variables (set_variable with description) or locals, no hardcoded regions/zones/account IDs, outputs with descriptions (set_output), sensitive = true for secrets.
4. Wire nodes: list_connection_options, then connect with the best recipe (it adds IAM policies, permissions, event mappings).
5. Fix every error diagnostic (update_node) and every best_practices finding an edit returns. Organize before deploying: a concern with many resources or one that repeats (a network, a service = function + role + policies + log group + permissions) goes into a local module with extract_module (dry_run=true first; snake_case name after the concern), referenced from the root through its inputs and outputs. Then call review_terraform, fix what it reports (warnings first), and validate.
6. deploy into the local sandbox; on failure read the log tail, fix and redeploy.
7. list_load_targets and run_load_test with preset smoke to prove it works.
8. Summarize for the user: what was built (nodes and how they connect), deployed endpoints/outputs, smoke-test latency and error rate, best-practice findings you deliberately left (and why), and next steps (e.g. a load test or export_terraform).

Nothing touches a real cloud: everything runs in the local Floci emulator. Tell the user they can watch the graph live in the Nimbo app.`, req, cloud), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "extend_workflow",
		Title:       "Extend workflow",
		Description: "Add and connect services to an existing workflow (the one open in the app by default).",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to add, e.g. \"an SQS queue feeding the Lambda\"", Required: true},
			{Name: "workflow", Description: "workflow ID or name (default: current, the workflow open in the app)"},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		wf := workflowOrCurrent(a["workflow"])
		return fmt.Sprintf(`Extend the Nimbo workflow %q with: %s

Steps:
1. get_workflow (workflow %q) to understand the current nodes, edges and diagnostics. If it fails because nothing is open, ask the user which workflow to use (list_workflows).
2. search_resources / describe_resource for each new service (prefer floci_supported=true).
3. add_node for each new resource, then list_connection_options + connect to wire it to the existing nodes the request mentions. Follow the workflow's conventions: names from local.name_prefix when it exists, configurable values as described variables or locals, descriptions on new variables/outputs, sensitive secrets, no hardcoded regions/zones/account IDs.
4. Fix error diagnostics with update_node and the best_practices findings your edits return. If the addition makes one concern large (many resources for a network or service) or repeats an existing group, extract it into a module with extract_module (dry_run=true first); an existing local module grows by inline_module + extract_module or by editing its files. Call review_terraform and fix what your change introduced (findings that were there before are the user's call), then validate.
5. plan and report what Terraform would add, change or destroy. Deploy only if the user asks.
6. Summarize the changes (new nodes, new connections, best-practice findings left open) for the user.

Pass workflow %q to every tool so the edits land in the same workflow.`, wf, req, wf, wf), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "import_terraform_folder",
		Title:       "Import Terraform folder",
		Description: "Import an existing Terraform folder or repository and explain what was found.",
		Arguments: []*sdk.PromptArgument{
			{Name: "dir", Description: "absolute path of the folder or repository", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		dir, err := required(a, "dir")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Import the Terraform code in %s into Nimbo.

Steps:
1. scan_terraform_folder with dir %q. Show the user the projects found (path, resources, modules, providers, clouds, diagnostics) and what was skipped.
2. If there are several projects, ask which to import (default: all of them); then import_terraform with dir and projects.
3. For each imported workflow, get_workflow and summarize: main services, how they connect, local modules (they can be opened in the app), and diagnostics that need fixing.
4. Check Floci support: search_resources/describe_resource tell whether each type runs in the local emulator. List unsupported types so the user knows what cannot be deployed locally.
5. Suggest next steps: fix diagnostics, validate, deploy to the sandbox, load test.`, dir, dir), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "load_test",
		Title:       "Load test",
		Description: "Deploy a workflow if needed and load-test it, interpreting the results.",
		Arguments: []*sdk.PromptArgument{
			{Name: "workflow", Description: "workflow ID or name (default: current, the workflow open in the app)"},
			{Name: "goal", Description: "what to learn, e.g. \"p95 under 200 ms at 50 rps\" or \"find the breaking point\""},
		},
	}, func(a map[string]string) (string, error) {
		wf := workflowOrCurrent(a["workflow"])
		goal := strings.TrimSpace(a["goal"])
		if goal == "" {
			goal = "check it handles a realistic load (preset load) and report latency, throughput and errors"
		}
		return fmt.Sprintf(`Load-test the Nimbo workflow %q. Goal: %s

Steps:
1. get_deployed_state (workflow %q). If nothing is deployed, validate and deploy first.
2. list_load_targets and pick the targets that matter for the goal (an HTTP endpoint first, else queues/tables/functions); tell the user which you chose.
3. run_load_test with preset smoke to make sure the target works; stop and diagnose if it errors.
4. Run the profile that answers the goal: load for normal traffic, stress or spike to find limits, soak for stability; add thresholds (p95_ms, error_rate, rps_min) that encode the goal.
5. Interpret: throughput, p50/p95/p99 latency, error rate, stability, breaking point and threshold results. Say whether the goal is met and what to change if not (e.g. Lambda memory, batch size, DynamoDB capacity).`, wf, goal, wf), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "test_deployed_service",
		Title:       "Test a deployed service",
		Description: "Load-test an already-deployed service (HTTP API, database, cache, AWS account) through saved connections, with explicit user confirmation.",
		Arguments: []*sdk.PromptArgument{
			{Name: "connection", Description: "saved connection ID or name (default: ask the user or create one)"},
		},
	}, func(a map[string]string) (string, error) {
		conn := strings.TrimSpace(a["connection"])
		first := "1. list_connections and ask the user which service to test; if it is not saved yet, ask for its details and create_connection (only ask for secrets the user chooses to give)."
		if conn != "" {
			first = fmt.Sprintf("1. get_connection %q and show the user what it points to.", conn)
		}
		return first + `
2. Explain that this sends real traffic to a service outside the local sandbox and ask the user to confirm before any request. Do not continue without an explicit yes.
3. test_connection to check it is reachable.
4. list_connection_targets (for AWS this makes read-only calls to the real account; it is covered by the confirmation) and agree on a target with the user.
5. run_connection_load_test with preset smoke and confirm_external=true. Only run heavier presets (load, stress, spike, soak) if the user asks for them and confirms again, since they can affect production.
6. Report latency, throughput, error rate and threshold results, and any risk you noticed.`, nil
	})
}

func required(args map[string]string, name string) (string, error) {
	v := strings.TrimSpace(args[name])
	if v == "" {
		return "", fmt.Errorf("argument %q is required", name)
	}
	return v, nil
}

func workflowOrCurrent(ref string) string {
	if ref = strings.TrimSpace(ref); ref != "" {
		return ref
	}
	return currentWorkflow
}
