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
1. Plan the structure first (the plan_structure recipe): list the resources per logical group (network foundation, data tier, security controls, each app/service) and estimate their count. Around 100 resources or more: one workflow per layer (network, data, app), each with its own state, upper layers finding lower ones with data sources by tags/IDs. Two or more environments (dev, prod, ...): the environments layout, one state per environment. Tell the user the plan in a few lines before building.
2. list_templates; if one is close, create_workflow from it, otherwise create_workflow empty with a short descriptive name (one per layer when you split).
3. For each service needed: search_resources (prefer floci_supported=true so it runs in the local sandbox) and describe_resource for required attributes.
4. add_node for each resource (string attributes are quoted HCL: "\"my-name\""). Keep the conventions the starters add: names "${local.name_prefix}-<role>", configurable values as typed, described variables (set_variable with description) or locals, no hardcoded regions/zones/account IDs, outputs with descriptions (set_output), sensitive = true for secrets. Prefer attachment resources (aws_vpc_security_group_ingress_rule, aws_iam_role_policy_attachment) over inline rule blocks.
5. Wire nodes: list_connection_options, then connect with the best recipe (it adds IAM policies, permissions, event mappings).
6. Fix every error diagnostic (update_node) and every best_practices finding an edit returns. Organize before deploying: each logical group with several resources becomes a local module with extract_module (dry_run=true first; snake_case name after the group: network, data, orders_api), called from the root through its inputs and outputs. Modules: never a single resource, no provider blocks, no nesting below the first level, every resource reachable from an output, environment-specific inputs without defaults, units in names (ram_size_gb), positive booleans (enable_x).
7. Environments (when the user wants more than one): set_environment per environment with its non-secret values (secrets as secret env vars TF_VAR_<name> with secret_keys, never as values); with two or more the layout is environments (update_workflow layout to change it). Never use terraform.workspace.
8. Call review_terraform: read its structure (states, resources per state, modules, nesting, backend) and fix the findings (warnings first), then validate.
9. deploy into the local sandbox; on failure read the log tail, fix and redeploy.
10. list_load_targets and run_load_test with preset smoke to prove it works.
11. Summarize for the user: the structure (workflows/layers, modules, environments and layout), what was built (nodes and how they connect), deployed endpoints/outputs, smoke-test latency and error rate, best-practice findings you deliberately left (and why), and next steps (e.g. a load test or export_terraform).

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
1. get_workflow (workflow %q) to understand the current nodes, edges, diagnostics and structure (layout, resources per state, modules, environments). If it fails because nothing is open, ask the user which workflow to use (list_workflows).
2. Decide where the addition belongs before adding it: inside the module of its logical group (network foundation, data tier, security controls, an app/service) or a new group. If the addition takes the state past ~100 resources, propose a separate workflow for the new layer (it finds the existing one with data sources by tags/IDs) instead of growing this one (see plan_structure).
3. search_resources / describe_resource for each new service (prefer floci_supported=true).
4. add_node for each new resource, then list_connection_options + connect to wire it to the existing nodes the request mentions. Follow the workflow's conventions: names from local.name_prefix when it exists, configurable values as described variables or locals (environment-specific ones without defaults, with a value per environment via set_environment), descriptions on new variables/outputs, sensitive secrets (never as environment values), no hardcoded regions/zones/account IDs, attachment resources rather than inline rule blocks.
5. Fix error diagnostics with update_node and the best_practices findings your edits return. If the addition makes one group large or repeats an existing group, extract it into a module with extract_module (dry_run=true first); an existing local module grows by inline_module + extract_module or by editing its files. Modules: no single-resource wrappers, no provider blocks, no nesting below the first level, every resource reachable from an output. Call review_terraform and fix what your change introduced (findings that were there before are the user's call), then validate.
6. plan and report what Terraform would add, change or destroy. Deploy only if the user asks.
7. Summarize the changes (new nodes, new connections, where they sit in the structure, best-practice findings left open) for the user.

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
		Name:        "plan_structure",
		Title:       "Plan structure",
		Description: "Design the workflow/module/environment breakdown of a request before adding any resource.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to build or grow, e.g. \"an online shop on AWS with dev and prod\"", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Plan the Terraform structure for: %s

Do not add resources yet. Follow HashiCorp's style guide, Google Cloud's root-module guidance and AWS Prescriptive Guidance:

1. Context: which clouds, which environments (dev, staging, prod, ...) and what already exists. If a workflow is open or named, get_workflow and read its layout and structure (resources per state, modules, nesting, backend).
2. Inventory: list the resources the request needs, grouped by logical group: network foundation (VPC, subnets, route tables, gateways, endpoints), data tier (databases, caches, tables, buckets that hold data), security controls (KMS keys, WAF, shared security groups, IAM boundaries), and each application/service (compute, API, queues, its IAM role and log group). Count instances (count/for_each included).
3. States: one workflow is one root module and one state per environment.
   - up to a few dozen resources: one workflow;
   - 50 to 100: one workflow, every logical group as a module;
   - more than ~100 (Google: at most 100 per state): one workflow per layer, created in order network → data → app. Upper layers read lower ones with data sources filtered by tags or IDs (data.aws_vpc with a Name tag, data.aws_subnets, data.aws_db_instance), never through shared state; tag what lower layers create so they can be found.
4. Environments: one state each (environments layout, automatic from two environments; never CLI workspaces or terraform.workspace). For every input decide: environment-specific (instance sizes, counts, CIDRs, domain names: variables without defaults, a value per environment with set_environment) or environment-independent (variables with defaults or locals). Secrets are sensitive variables fed from secret env vars (TF_VAR_<name>), never tfvars values.
5. Modules per workflow: one local module per logical group (modules/network, modules/data, modules/<service>), never one per resource; no provider blocks inside; call them from the workflow's root only (the environments layout already wraps the workflow in a service module, so nesting stays within 2 levels); outputs for every resource callers need (IDs, ARNs, endpoints); input names with units (ram_size_gb, retention_days) and positive booleans (enable_x). Registry modules pinned with version, git modules with ?ref=<tag>.
6. Present the plan to the user and wait for an OK: a tree like
   <workflow>/
     modules/network  modules/data  modules/<service>
     environments/dev  environments/prod   (one backend.tf and state each)
   plus a table: workflow → layer → modules → estimated resources → environments → how it finds the layers below.
7. After the OK: create_workflow per workflow (descriptive names, e.g. "shop-network"), add each group's resources and connect them, extract_module per group (dry_run=true first), set_environment per environment, update_workflow layout if it should differ from auto, then review_terraform: its structure must match the plan and its findings should be fixed (warnings first).`, req), nil
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
