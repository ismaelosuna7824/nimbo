package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Tool groups as shown in the catalog.
const (
	groupWorkflows   = "Workflows"
	groupCatalog     = "Catalog"
	groupEditing     = "Editing"
	groupTerraform   = "Terraform code"
	groupRuns        = "Sandbox and runs"
	groupLoadTests   = "Load tests"
	groupConnections = "Connections"
	groupNetwork     = "Network"
)

// workflowArg is the input of tools that only need a workflow.
type workflowArg struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or exact name; omit or pass \"current\" for the workflow open in the Nimbo app"`
}

func registerTools(r *registry, t *tools) {
	r.group = groupWorkflows
	addTool(r, "list_workflows", "List workflows", "List workflows (ID, name, node count, clouds), most recently updated first. Archived workflows are hidden unless include_archived=true.", t.listWorkflows)
	addTool(r, "get_open_workflow", "Get open workflow", "Get the workflow currently open in the Nimbo desktop app (compact view with structure, like get_workflow), or a message when none is open. Every workflow argument also accepts \"current\" (or can be omitted) to mean this workflow.", t.getOpenWorkflow)
	addTool(r, "list_templates", "List templates", "List starter templates that can seed create_workflow (e.g. aws-queue-worker, aws-serverless-api), with kind and cloud. Edge patterns (how client traffic reaches services) have category \"edge-patterns\" and pattern \"reverse-proxy\", \"api-gateway\" or \"load-balancer\", one of each per kind and cloud (e.g. docker-reverse-proxy, k8s-api-gateway).", t.listTemplates)
	addTool(r, "create_workflow", "Create workflow", "Create a NEW workflow, optionally from a template ID (see list_templates). Only when the user asks for a new or separate workflow or nothing is open: to change the design the user has open (get_open_workflow), edit that one instead. mode: \"architecture\" (default: infrastructure plus smoke handlers that prove the wiring) or \"full\" (real application code in services/<svc>/ asset files, spec first with spec_feature); ask the user once when unsure. Returns the compact workflow (with a notice when the user has another workflow open); use its id in every other tool.", t.createWorkflow)
	addTool(r, "get_workflow", "Get workflow", "Get a compact view of a workflow: nodes (address, type, attributes as HCL expressions, nested blocks), derived edges (source.attr -> target.attribute), variables, outputs, locals, providers, environments (secret values redacted), diagnostics, the export layout and a structure summary (states, resources per state, modules with nesting, backend). Call this before editing.", t.getWorkflow)
	addTool(r, "update_workflow", "Update workflow", "Change a workflow's name, description, build mode and/or export layout; pass only the fields that change (description \"\" clears it). mode: \"architecture\" (infrastructure plus smoke handlers) or \"full\" (real application code; run spec_feature first). layout: \"environments\" exports modules/<service> plus one root (and state, backend.tf) per environment under environments/<env>; \"single\" one root with a tfvars file per environment; \"auto\" picks environments from two environments up. Its Terraform is unchanged. Returns the compact workflow with its structure.", t.updateWorkflow)
	addTool(r, "archive_workflow", "Archive workflow", "Archive (close) a workflow: its sandbox is stopped keeping state, it is hidden from list_workflows and runs/load tests are refused until unarchive_workflow.", t.archiveWorkflow)
	addTool(r, "unarchive_workflow", "Unarchive workflow", "Restore an archived workflow so it can be edited, deployed and load-tested again.", t.unarchiveWorkflow)

	r.group = groupCatalog
	addTool(r, "search_resources", "Search resources", "Search the Terraform resource catalog (aws, azure, gcp, oci). Returns resource types with category and whether the local Floci emulator supports them. Prefer floci_supported=true types so the workflow can be deployed and load-tested locally.", t.searchResources)
	addTool(r, "describe_resource", "Describe resource", "Describe a Terraform resource or data source type: required and optional attributes with types, nested blocks, exported (computed) attributes usable in references, and Floci emulator support.", t.describeResource)

	r.group = groupEditing
	addTool(r, "add_node", "Add node", "Add a resource, data source or module node. Curated starters prefill required attributes and may add helper nodes (e.g. a Lambda adds its IAM role and archive). Optional attributes are HCL expressions keyed by attribute path, e.g. {\"bucket\": \"\\\"my-bucket\\\"\", \"tags\": \"{ env = var.env }\", \"versioning.enabled\": \"true\"}. Strings must be quoted HCL strings.", t.addNode)
	addTool(r, "update_node", "Update node", "Edit a node: set attributes (attribute path -> HCL expression; nested block paths like \"versioning.enabled\" or \"rule[0].id\" create blocks as needed), unset attributes or blocks, rename it (references elsewhere are rewritten; a deployed resource or module call gets a moved block so the next deploy keeps it instead of destroying and recreating it) and/or move it to another root .tf file (file: \"network.tf\"; \"auto\" returns it to Nimbo's recommended layout). Returns the node and current diagnostics.", t.updateNode)
	addTool(r, "remove_node", "Remove node", "Remove a node. References to it in other nodes become diagnostics that should be fixed.", t.removeNode)
	addTool(r, "list_connection_options", "List connection options", "List the ways two nodes can be connected (recipes). Source is the upstream node being referenced (e.g. a queue), target the node that will reference it (e.g. a Lambda). Curated recipes also add helper resources such as event source mappings, permissions or IAM policies.", t.listConnectionOptions)
	addTool(r, "connect", "Connect nodes", "Connect two nodes with a recipe ID from list_connection_options. Returns nodes that were added or changed and diagnostics.", t.connect)
	addTool(r, "extract_module", "Extract to module", "Move a group of related nodes into a new local module modules/<name> and call it from the root in their place (module.<name>). References from the group to the rest of the root become typed, described input variables passed by the call; root references to the group become described module outputs (module.<name>.<output>); depends_on is rewritten (depends_on = [module.<name>]); non-default provider aliases are passed with providers = {}; and root moved blocks keep deployed resources (count/for_each instances included) without destroy/recreate. The module gets versions.tf, variables.tf, outputs.tf, main.tf/category files and README.md. Use it when one concern has many resources or is repeated: a network (VPC + subnets + route tables + gateways), a service (function + role + policies + log group + permissions), a queue with its DLQ and alarms. dry_run=true only reports what would be created. Rejected with an explanation when the move would form a dependency cycle or a lifecycle.replace_triggered_by crosses the module boundary.", t.extractModule)
	addTool(r, "inline_module", "Inline module", "Reverse of extract_module for a local module (created by Nimbo or imported): its blocks come back to the root in place of the call, inputs are replaced by the call's arguments, module outputs by their expressions, and root moved blocks keep deployed resources. Only for modules called once without count/for_each; the module's .tf files are deleted.", t.inlineModule)
	addTool(r, "set_variable", "Set variable", "Create or update an input variable. type and default are HCL expressions (e.g. type \"list(string)\", default \"[\\\"a\\\"]\").", t.setVariable)
	addTool(r, "remove_variable", "Remove variable", "Remove an input variable.", t.removeVariable)
	addTool(r, "set_output", "Set output", "Create or update an output; value is an HCL expression such as \"aws_sqs_queue.jobs.url\".", t.setOutput)
	addTool(r, "remove_output", "Remove output", "Remove an output.", t.removeOutput)
	addTool(r, "set_environment", "Set environment", "Create or update an environment: variable values (HCL expressions), process environment variables for Terraform runs, which env vars are secret, and optionally make it the active environment used by runs.", t.setEnvironment)

	r.group = groupTerraform
	addTool(r, "get_terraform", "Get Terraform code", "Render the workflow as Terraform files. Imported projects keep their own files; new content follows Nimbo's layout: versions.tf, providers.tf, variables.tf, locals.tf, outputs.tf, data.tf, one file per service category (network.tf, compute.tf, storage.tf, messaging.tf, api.tf, iam.tf, ...) and main.tf for module calls (small projects: one main.tf). Move a node with update_node file.", t.getTerraform)
	addTool(r, "review_terraform", "Review Terraform", "Check the workflow against Terraform best practices and return its structure (export layout, states, resources per state, modules with nesting, backend) and the findings grouped by severity (warnings: security, unpinned versions, providers in modules, oversized states; info: documentation, versions, hardcoded values, tags, naming, structure, modules, environments), each with why it matters and how to fix it. Local module contents are checked too. Call it before finishing any change; mutating tools already return the findings their change introduced as best_practices. Findings never block deploys.", t.reviewTerraform)
	addTool(r, "apply_terraform_code", "Apply Terraform code", "Edit the workflow by writing code. Docker workflows: files maps any file path (compose.yaml, api/Dockerfile, .env, app sources) -> full content, \"\" deletes it; YAML/Dockerfile syntax errors are refused with file:line and the graph is re-derived. Terraform workflows: files maps file name -> full content and is merged over the current files from get_terraform; an empty string deletes that file. The code is re-imported; node IDs, positions and environments are kept by address. Other paths are stored as workflow files (exported, copied into the sandbox): application code goes in services/<svc>/… (e.g. services/orders/src/index.mjs; a Dockerfile whose last stage is `FROM scratch AS package` when it needs a build, which Nimbo runs into services/<svc>/dist), specs in openspec/…; Terraform zips the folder with data \"archive_file\" source_dir = \"${path.module}/services/<svc>/src\" (or /dist). Never put application code inline in Terraform (archive_file source content heredocs). Use for complex blocks that are awkward with update_node.", t.applyTerraformCode)
	addTool(r, "get_workflow_files", "Get workflow files", "List every file of a workflow (any kind: Terraform, application code under services/<svc>/, specs under openspec/, Dockerfiles) with size and kind; prefix (e.g. \"openspec/\" or \"services/orders/\") or path returns the content of the matching text files too. Write them with apply_terraform_code.", t.getWorkflowFiles)
	addTool(r, "scan_terraform_folder", "Scan Terraform folder", "Scan a local folder (e.g. a repository) for Terraform projects without importing: every directory with *.tf files, with resource/data/module counts, providers, clouds, tfvars files, backend, layout and parse diagnostics. Directories used only as local modules are not separate projects, and an environments tree (environments/<env>/ roots that each only call one ../../modules/<service>) is one project with layout \"environments\" and its environments. Pass the chosen paths to import_terraform as projects.", t.scanTerraformFolder)
	addTool(r, "import_terraform", "Import Terraform", "Import existing Terraform as new workflows, from a local directory or from files (relative path -> content). Local modules (source = \"./modules/x\") are kept as assets and can be opened in the app. An environments tree (as export_terraform writes it) becomes one workflow: the service module's content with one environment per environments/<env> root. By default only the root folder is imported; pass projects (paths from scan_terraform_folder, \".\" = root) to import several projects, one workflow each.", t.importTerraform)
	addTool(r, "export_terraform", "Export Terraform", "Write the workflow's Terraform (plus assets such as Lambda sources, local modules and the dependency lock file) to a local directory, in the workflow's layout (structure.layout from get_workflow). Single layout: one root; with environments=true each environment's values go to the variable file it was imported from (e.g. terraform.tfvars), else environments/<name>.tfvars, plus .env.example files. Environments layout (auto from two environments): modules/<service>/ with the workflow's resources, its local modules under modules/, and one root per environment under environments/<env>/ (backend.tf with its own state, main.tf calling the service module, providers.tf, versions.tf, variables.tf, outputs.tf; environments=true adds terraform.tfvars and .env.example), plus README.md, .gitignore and a Makefile. Comments are kept. Docker and Kubernetes workflows write their files as they are. ci=github|gitlab adds a ready-to-use pipeline (format/validate or render checks, a plan per environment on pull requests, gated per-environment applies/deploys on the default branch, smoke=true for an HTTP check after each deploy) whose header and README section list the secrets and variables to set.", t.exportTerraform)

	r.group = groupRuns
	addTool(r, "sandbox_status", "Sandbox status", "Show the workflow's local Floci sandbox: state, emulator URLs and running services.", t.sandboxStatus)
	addTool(r, "sandbox_start", "Start sandbox", "Start the workflow's local Floci sandbox (Docker) for the clouds it uses. Runs start it automatically.", t.sandboxAction("start"))
	addTool(r, "sandbox_stop", "Stop sandbox", "Stop the workflow's sandbox containers (state is kept).", t.sandboxAction("stop"))
	addTool(r, "sandbox_reset", "Reset sandbox", "Wipe the sandbox's emulator state and Terraform state (everything deployed is gone).", t.sandboxAction("reset"))
	addTool(r, "validate", "Validate", "Run `terraform validate` in the sandbox and wait for the result (status, last log lines).", t.runAction(model.ActionValidate))
	addTool(r, "plan", "Plan", "Run `terraform plan` against the sandbox and wait for the result (planned add/change/destroy counts, last log lines).", t.runAction(model.ActionPlan))
	addTool(r, "deploy", "Deploy", "Terraform workflows: plan, then apply exactly that plan against the local sandbox and wait; on success returns the deployed resources (ids, ARNs, URLs), outputs and drift (resources that still change right after the deploy, with a fix hint each). When the plan would delete or replace already-deployed resources nothing is applied: status needs_confirmation with destructive (address, action delete/replace, reasons = attributes forcing the replacement). Show that list to the user and call deploy again with confirm_destroy=true ONLY after the user explicitly agreed; never set it on your own. Docker workflows: `docker compose up` (build, wait for healthy) in the workflow's own project and return the container status; remap_ports publishes on free host ports when the compose ports are taken. Kubernetes workflows: create/start the workflow's local k3s cluster, build and load the local images, apply every source for the environment (server-side apply / helm upgrade --install), wait for rollouts and return the cluster status (workloads, port-forwards, Ingress URLs, problems).", t.runAction(model.ActionApply))
	addTool(r, "destroy", "Destroy", "Terraform workflows: run `terraform destroy` against the sandbox. Docker workflows: `docker compose down` of the workflow's project (remove_volumes also deletes its named volumes). Kubernetes workflows: delete what deploy applied (remove_volumes also deletes PersistentVolumeClaims, delete_cluster the cluster).", t.runAction(model.ActionDestroy))
	addTool(r, "get_run", "Get run", "Get a Terraform run's status and last log lines (use after a run tool returned while still running).", t.getRun)
	addTool(r, "get_deployed_state", "Get deployed state", "Terraform workflows: what is deployed in the sandbox, resources with key values (id, arn, name, url) and outputs; pass address to get every value of matching resources. Docker workflows: the compose project's containers per service (state, health, published host ports, port remaps). Kubernetes workflows: the cluster, each workload's rollout and pods (restarts, OOM kills, crash loops), port-forwards, Ingress URLs, problems and warning events.", t.getDeployedState)
	addTool(r, "get_endpoints", "Get endpoints", "Explain how to use what is deployed: sandbox address and env settings, then per resource a plain summary, host URLs (API routes, function URLs), identifiers (queue URL, ARN, bucket, table…), a ready-to-run snippet and the try_endpoint actions. Deploy first.", t.getEndpoints)
	addTool(r, "try_endpoint", "Try endpoint", "Call a deployed resource in the local sandbox: send an HTTP request to an API route, invoke a function, send/peek queue messages, publish to a topic, list/put/get bucket files, scan/put table items, … (actions from get_endpoints). Sandbox only; returns status, latency and the response body.", t.tryEndpoint)

	r.group = groupNetwork
	addTool(r, "check_network", "Check network", "Review the workflow's networks: VPCs/VNets, subnets classified public/private/isolated with the reason, route tables, gateways, security group summaries, where each workload sits (subnet, AZ, public or not), and the net_* findings (subnet outside its VPC, overlapping CIDRs, public subnet without internet gateway route, private subnet without egress, NAT in a private subnet, unassociated route tables, cross-VPC routes/security groups, NACLs dropping replies, single-AZ load balancers/DB subnet groups) with why and fix. Uses the deployed sandbox's exact values when everything is deployed (basis \"deployed\"), else the design (basis \"design\"); unknown values are skipped, never guessed. Call it after designing or changing a network.", t.checkNetwork)
	addTool(r, "check_reachability", "Check reachability", "AWS only today: Azure and GCP endpoints are rejected. Answer \"can traffic get from A to B?\" like AWS Reachability Analyzer: from internet or a node to a node or internet, protocol and port. Returns reachable, the path hop by hop (internet gateway, route table, network ACL both ways with ephemeral ports, security groups, listeners, targets) each allow/deny/unknown with the reason, blockers and suggested fixes. live=true also runs real connections in the deployed sandbox for EC2 instances and labels each hop verified live or analysis-only (the local emulator doesn't enforce security groups between instances, NACLs or route tables).", t.checkReachability)
	addTool(r, "add_test_instance", "Add test instance", "Add a small, clearly labelled EC2 test instance (Amazon Linux 2023, t3.micro, user_data web server on port 80 with a friendly page) and its security group (port 80 open to 0.0.0.0/0, commented) into a subnet, so traffic can be tested for real after deploying: get_endpoints then shows its host URL and check_reachability live=true connects to it. subnet is an aws_subnet address; for count/for_each subnets name one instance (aws_subnet.public[\"a\"]).", t.addTestInstance)

	r.group = groupLoadTests
	addTool(r, "list_load_targets", "List load targets", "List load-test targets suggested from the deployed workflow (HTTP endpoints, SQS queues, S3 buckets, DynamoDB tables, Lambda functions, ...). Deploy first.", t.listLoadTargets)
	addTool(r, "run_load_test", "Run load test", "Run a load test against a target (index from list_load_targets or a full target object) with a preset (smoke, load, stress, spike, soak, autoscale) or custom stages, and optional pass/fail thresholds. Waits for the result by default and returns throughput, latency percentiles, error rate, stability, breaking point and threshold results; docker and Kubernetes workflows add container usage and the bottleneck. Kubernetes runs also return the environment and autoscaling: per workload in scope (the target and every workload with a HorizontalPodAutoscaler) its replicas and a verdict (scaled, late, maxed, no_hpa, no_metrics, not_needed, too_short) with numbers; use preset autoscale (4.5 min ramp and hold) to judge an HPA, runs under 60 s are too short. Docker and Kubernetes workflows can inject a real, reversible incident during the run with scenario {id, target, atSec, durationSec, params} (list_scenarios shows what applies): kill_replica, dependency_down, dependency_latency, cache_flush or traffic_spike; the result's scenario has a verdict (survived, degraded, failed), a one-line message with numbers and a fix, the checks, and the injection/revert/recovery seconds. Leave at least 10 s of baseline before atSec and 15 s after the fault ends. A target object may use a saved connection (connectionId from list_connections; required for sql/redis/mongo targets) or a non-local URL: such targets are outside the sandbox and need confirm_external=true."+externalWarning, t.runLoadTest)
	addTool(r, "get_load_test", "Get load test", "Get a load test run's status, summary and threshold results, plus container usage, (Kubernetes) the environment and autoscaling verdicts, and the scenario verdict when one was injected.", t.getLoadTest)
	addTool(r, "list_scenarios", "List scenarios", "List the incident scenarios a load test of this docker or Kubernetes workflow can inject in its active environment, each with a plain explanation, its targets (graph node ids: compose services \"service:<name>\", workloads \"Deployment/<ns>/<name>\") and why they qualify, its numeric params (dependency_latency: delayMs, jitterMs, lossPct; traffic_spike: factor) and a default duration; unavailable lists the others with the reason. Faults act only on the workflow's own containers or cluster and are always reverted (at the end of the window, on stop, and at daemon start after a crash).", t.listScenarios)

	r.group = groupConnections
	addTool(r, "list_connections", "List connections", "List saved connections (the global Connections library of already-deployed services: HTTP APIs, PostgreSQL, MySQL, Redis, MongoDB, AWS accounts via profile). Secrets are never returned; secretsSet names the stored ones.", t.listConnections)
	addTool(r, "get_connection", "Get connection", "Get one saved connection by ID or name (secrets never returned; secretsSet names the stored ones).", t.getConnection)
	addTool(r, "create_connection", "Create connection", "Save a connection to an already-deployed service so it can be load-tested without a workflow. kind http needs base_url; postgres/mysql/redis/mongodb need uri or host; aws uses profile (shared config/SSO; empty = default chain) and region. Secrets (password, uri, auth token/password) are stored locally (0600) and never returned. Saving sends no traffic; only ask the user for secrets they choose to provide.", t.createConnection)
	addTool(r, "update_connection", "Update connection", "Update a saved connection: only the given fields change; blank or omitted secrets keep the stored values; clear_secrets removes stored secrets.", t.updateConnection)
	addTool(r, "delete_connection", "Delete connection", "Delete a saved connection and its load test history.", t.deleteConnection)
	addTool(r, "test_connection", "Test connection", "Check that a connection is reachable (one lightweight request: HTTP GET of the base URL, a database ping/version query, or AWS STS GetCallerIdentity). Pass connection for a saved one, fields for an unsaved draft, or both to try edits (blank secrets come from the saved one). This contacts the real service: confirm with the user first if it is not local.", t.testConnection)
	addTool(r, "list_connection_targets", "List connection targets", "List load-test targets suggested for a saved connection (HTTP endpoints, SQL/Redis/Mongo read probes, AWS resources found in the account). Listing AWS resources makes read-only API calls to the real account; confirm with the user first.", t.listConnectionTargets)
	registerDockerTools(r, t)
	registerKubeTools(r, t)
	registerEnvironmentTools(r, t)
	registerMonitoringTools(r, t)
	registerReadinessTools(r, t)
	registerComplianceTools(r, t)
	registerWellArchitectedTools(r, t)
	registerCodeGuideTools(r, t)
	registerCapacityTools(r, t)
	registerLearnTools(r, t)
	registerChallengeTools(r, t)
	registerAccessTools(r, t)
	r.group = groupConnections
	addTool(r, "run_connection_load_test", "Run connection load test", "Run a load test against a saved connection (no workflow needed): target is an index from list_connection_targets or a target object; preset (smoke, load, stress, spike, soak) or custom stages; optional thresholds. confirm_external must be true. Waits for the result by default."+externalWarning, t.runConnectionLoadTest)
}

type tools struct{ c *client }

// ---------------------------------------------------------------------------
// Workflows

type listWorkflowsIn struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"also list archived (closed) workflows"`
}

// workflowItem is a summary flagged when it is open in the desktop app.
type workflowItem struct {
	model.WorkflowSummary
	Open bool `json:"open,omitempty"`
}

func (t *tools) listWorkflows(ctx context.Context, in listWorkflowsIn) (any, error) {
	var list []model.WorkflowSummary
	if err := t.c.get(ctx, "/workflows", &list); err != nil {
		return nil, err
	}
	openID, err := t.c.openWorkflowID(ctx)
	if err != nil {
		return nil, err
	}
	out := []workflowItem{}
	for _, s := range list {
		if s.Archived && !in.IncludeArchived {
			continue
		}
		out = append(out, workflowItem{WorkflowSummary: s, Open: s.ID == openID})
	}
	return out, nil
}

func (t *tools) getOpenWorkflow(ctx context.Context, _ struct{}) (any, error) {
	wf, err := t.c.workflow(ctx, currentWorkflow)
	if errors.Is(err, errNothingOpen) {
		return "No workflow is open in the Nimbo app. Ask the user to open one, or use list_workflows / create_workflow.", nil
	}
	if err != nil {
		return nil, err
	}
	if wf.IsDocker() {
		return viewDocker(wf), nil
	}
	if wf.IsKubernetes() {
		return viewKube(wf), nil
	}
	return t.viewWithStructure(ctx, wf)
}

// viewWithStructure is the compact workflow plus its structure summary.
func (t *tools) viewWithStructure(ctx context.Context, wf *model.Workflow) (workflowView, error) {
	v := viewWorkflow(wf)
	s, err := t.structure(ctx, wf.ID)
	if err != nil {
		return v, err
	}
	v.Structure = s
	return v, nil
}

func (t *tools) structure(ctx context.Context, id string) (*structureView, error) {
	var s model.StructureSummary
	if err := t.c.get(ctx, "/workflows/"+pathEscape(id)+"/structure", &s); err != nil {
		return nil, err
	}
	return viewStructure(s), nil
}

func (t *tools) archiveWorkflow(ctx context.Context, in workflowArg) (any, error) {
	return t.setArchived(ctx, in.Workflow, "archive")
}

func (t *tools) unarchiveWorkflow(ctx context.Context, in workflowArg) (any, error) {
	return t.setArchived(ctx, in.Workflow, "unarchive")
}

func (t *tools) setArchived(ctx context.Context, ref, action string) (any, error) {
	wf, err := t.c.workflow(ctx, ref)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/"+action, nil, &out); err != nil {
		return nil, err
	}
	return map[string]any{"id": out.ID, "name": out.Name, "archived": out.Archived}, nil
}

func (t *tools) listTemplates(ctx context.Context, _ struct{}) (any, error) {
	var list []model.Template
	err := t.c.get(ctx, "/catalog/templates", &list)
	return list, err
}

type createWorkflowIn struct {
	Name        string `json:"name" jsonschema:"human readable workflow name"`
	Description string `json:"description,omitempty"`
	Template    string `json:"template,omitempty" jsonschema:"optional template ID from list_templates"`
	Kind        string `json:"kind,omitempty" jsonschema:"\"docker\" for a docker-compose workflow, \"kubernetes\" for manifests/Kustomize/Helm on a local cluster (implied by a docker-*/k8s-* template); omit for Terraform"`
	Mode        string `json:"mode,omitempty" jsonschema:"build mode: \"architecture\" (default: infrastructure plus smoke handlers) or \"full\" (real application code in services/<svc>/ asset files)"`
}

func (t *tools) createWorkflow(ctx context.Context, in createWorkflowIn) (any, error) {
	// What the user is looking at before this one exists.
	var session struct {
		OpenWorkflowID *string `json:"openWorkflowId"`
	}
	_ = t.c.get(ctx, "/session", &session)
	var wf model.Workflow
	body := map[string]string{"name": in.Name, "description": in.Description, "templateId": in.Template, "kind": in.Kind, "mode": strings.TrimSpace(in.Mode)}
	if err := t.c.do(ctx, http.MethodPost, "/workflows", body, &wf); err != nil {
		return nil, err
	}
	var out any
	switch {
	case wf.IsDocker():
		out = dockerResult(nil, &wf)
	case wf.IsKubernetes():
		out = kubeResult(nil, &wf)
	default:
		v := viewWorkflow(&wf)
		v.BestPractices = bpDelta(nil, &wf)
		out = v
	}
	if session.OpenWorkflowID == nil || *session.OpenWorkflowID == wf.ID {
		return out, nil
	}
	openName := *session.OpenWorkflowID
	var open model.Workflow
	if t.c.get(ctx, "/workflows/"+pathEscape(openName), &open) == nil {
		openName = open.Name
	}
	return struct {
		Workflow any    `json:"workflow"`
		Notice   string `json:"notice"`
	}{out, fmt.Sprintf("The user still has %q open in Nimbo; the app shows a notification offering to open %q. If they meant to change %q, edit that workflow instead and archive this one (archive_workflow).", openName, wf.Name, openName)}, nil
}

type updateWorkflowIn struct {
	Workflow    string  `json:"workflow,omitempty" jsonschema:"workflow ID or exact name; omit or pass \"current\" for the workflow open in the Nimbo app"`
	Name        *string `json:"name,omitempty" jsonschema:"new human readable name"`
	Description *string `json:"description,omitempty" jsonschema:"new description; \"\" clears it"`
	Layout      *string `json:"layout,omitempty" jsonschema:"export layout: auto (default: environments from two environments up), single or environments"`
	Environment *string `json:"environment,omitempty" jsonschema:"docker and Kubernetes workflows: the active environment (\"\" = base) the graph, rules and deploy render (list_environments)"`
	Mode        *string `json:"mode,omitempty" jsonschema:"build mode: \"architecture\" (infrastructure plus smoke handlers) or \"full\" (real application code; run spec_feature first)"`
}

func (t *tools) updateWorkflow(ctx context.Context, in updateWorkflowIn) (any, error) {
	if in.Name == nil && in.Description == nil && in.Layout == nil && in.Environment == nil && in.Mode == nil {
		return nil, errors.New("pass name, description, mode, layout and/or environment")
	}
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if in.Name != nil {
		name := strings.TrimSpace(*in.Name)
		if name == "" {
			return nil, errors.New("name must not be empty")
		}
		wf.Name = name
	}
	if in.Description != nil {
		wf.Description = strings.TrimSpace(*in.Description)
	}
	if in.Mode != nil {
		mode := strings.TrimSpace(*in.Mode)
		if err := model.ValidMode(mode); err != nil {
			return nil, err
		}
		if mode == "" {
			mode = model.ModeArchitecture
		}
		wf.Mode = mode
	}
	if in.Layout != nil && !wf.IsTerraform() {
		return nil, fmt.Errorf("%s workflows have no export layout", model.KindLabel(wf.Kind))
	}
	if in.Environment != nil {
		if wf.IsTerraform() {
			return nil, errors.New("environment switches the rendered environment of docker and Kubernetes workflows; Terraform runs take environment per run")
		}
		if *in.Environment != "" && !slices.ContainsFunc(wf.Environments, func(e model.Environment) bool { return e.Name == *in.Environment }) {
			return nil, fmt.Errorf("environment %q is not one of %v", *in.Environment, envNames(wf))
		}
		wf.ActiveEnvironment = *in.Environment
	}
	if in.Layout != nil {
		layout := strings.TrimSpace(*in.Layout)
		if layout == "auto" {
			layout = model.LayoutAuto
		}
		if err := model.ValidLayout(layout); err != nil {
			return nil, err
		}
		wf.Layout = layout
	}
	// The workflow's layout field is omitted when empty, which PUT reads as
	// "keep the current layout": send it explicitly so "auto" sticks.
	body := struct {
		*model.Workflow
		Layout string `json:"layout"`
	}{wf, wf.Layout}
	var out model.Workflow
	if wf.IsDocker() {
		if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID), wf, &out); err != nil {
			return nil, err
		}
		return viewDocker(&out), nil
	}
	if wf.IsKubernetes() {
		if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID), wf, &out); err != nil {
			return nil, err
		}
		return kubeResult(wf, &out), nil
	}
	if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID), body, &out); err != nil {
		return nil, err
	}
	return t.viewWithStructure(ctx, &out)
}

func (t *tools) getWorkflow(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.IsDocker() {
		return viewDocker(wf), nil
	}
	if wf.IsKubernetes() {
		return viewKube(wf), nil
	}
	return t.viewWithStructure(ctx, wf)
}

// ---------------------------------------------------------------------------
// Catalog

type searchIn struct {
	Query         string `json:"query" jsonschema:"free text, e.g. 'queue', 'object storage', 'aws_lambda'"`
	Cloud         string `json:"cloud,omitempty" jsonschema:"aws, azure, gcp or oci"`
	Kind          string `json:"kind,omitempty" jsonschema:"resource (default) or data"`
	SupportedOnly bool   `json:"supported_only,omitempty" jsonschema:"only types the local Floci emulator supports"`
	Limit         int    `json:"limit,omitempty" jsonschema:"max results (default 25)"`
}

func (t *tools) searchResources(ctx context.Context, in searchIn) (any, error) {
	q := url.Values{"q": {in.Query}}
	if in.Cloud != "" {
		q.Set("cloud", in.Cloud)
	}
	if in.Kind != "" {
		q.Set("kind", in.Kind)
	}
	if in.SupportedOnly {
		q.Set("supported", "true")
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 25
	}
	q.Set("limit", strconv.Itoa(limit))
	var list []model.ResourceSummary
	if err := t.c.get(ctx, "/catalog/resources?"+q.Encode(), &list); err != nil {
		return nil, err
	}
	type item struct {
		Type           string         `json:"type"`
		Kind           model.NodeKind `json:"kind"`
		Label          string         `json:"label"`
		Category       string         `json:"category"`
		FlociSupported bool           `json:"floci_supported"`
	}
	out := []item{}
	for _, r := range list {
		out = append(out, item{r.Type, r.Kind, r.Label, r.Category, r.FlociSupported})
	}
	return out, nil
}

type describeIn struct {
	Type string `json:"type" jsonschema:"Terraform type, e.g. aws_sqs_queue"`
	Kind string `json:"kind,omitempty" jsonschema:"resource (default) or data"`
}

func (t *tools) describeResource(ctx context.Context, in describeIn) (any, error) {
	kind := in.Kind
	if kind == "" {
		kind = string(model.KindResource)
	}
	var s model.ResourceSchema
	if err := t.c.get(ctx, "/catalog/resources/"+pathEscape(kind)+"/"+pathEscape(in.Type), &s); err != nil {
		return nil, err
	}
	return viewSchema(&s), nil
}

// ---------------------------------------------------------------------------
// Nodes and connections

// findNode resolves ref as a node ID, an address (aws_s3_bucket.b,
// data.x.y, module.m) or a unique local name.
func findNode(wf *model.Workflow, ref string) (*model.Node, error) {
	var byName []*model.Node
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.ID == ref || address(n) == ref {
			return n, nil
		}
		if n.Name == ref {
			byName = append(byName, n)
		}
	}
	if len(byName) == 1 {
		return byName[0], nil
	}
	var addrs []string
	for i := range wf.Nodes {
		addrs = append(addrs, address(&wf.Nodes[i]))
	}
	return nil, fmt.Errorf("node %q not found in workflow %s; nodes: %s", ref, wf.ID, strings.Join(addrs, ", "))
}

type nodeResult struct {
	Node          nodeView   `json:"node"`
	Added         []nodeView `json:"added_nodes,omitempty"`
	Diagnostics   []diagView `json:"diagnostics"`
	BestPractices []bpView   `json:"best_practices,omitempty"`
}

func nodeDiagnostics(wf *model.Workflow) []diagView { return viewDiagnostics(wf, wf.Diagnostics) }

// mutationResult is the result map of a mutating tool: fields, the
// diagnostics of after and the best-practice findings the change introduced.
func mutationResult(before, after *model.Workflow, fields map[string]any) map[string]any {
	fields["diagnostics"] = nodeDiagnostics(after)
	if bp := bpDelta(before, after); len(bp) > 0 {
		fields["best_practices"] = bp
	}
	return fields
}

type addNodeIn struct {
	Workflow   string            `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Type       string            `json:"type" jsonschema:"Terraform type, e.g. aws_sqs_queue (use search_resources); 'module' for a module call"`
	Name       string            `json:"name,omitempty" jsonschema:"Terraform local name (snake_case); derived from the type when empty"`
	Kind       string            `json:"kind,omitempty" jsonschema:"resource (default), data or module"`
	Attributes map[string]string `json:"attributes,omitempty" jsonschema:"attribute path -> HCL expression"`
	X          *float64          `json:"x,omitempty" jsonschema:"proposed canvas x (default 0); the daemon moves new nodes to the nearest free spot"`
	Y          *float64          `json:"y,omitempty" jsonschema:"proposed canvas y (default 0)"`
}

func (t *tools) addNode(ctx context.Context, in addNodeIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	before := map[string]bool{}
	for _, n := range wf.Nodes {
		before[n.ID] = true
	}
	var pos model.Position
	if in.X != nil {
		pos.X = *in.X
	}
	if in.Y != nil {
		pos.Y = *in.Y
	}
	kind := in.Kind
	if kind == "" {
		kind = string(model.KindResource)
	}
	var res struct {
		Workflow model.Workflow `json:"workflow"`
		NodeID   string         `json:"nodeId"`
	}
	body := map[string]any{"kind": kind, "type": in.Type, "name": in.Name, "position": pos}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/nodes", body, &res); err != nil {
		return nil, err
	}
	out := &res.Workflow
	if len(in.Attributes) > 0 {
		node, err := findNode(out, res.NodeID)
		if err != nil {
			return nil, err
		}
		if out, err = t.patchBody(ctx, out, node, in.Attributes, nil, "", ""); err != nil {
			return nil, fmt.Errorf("node %s was added but setting attributes failed: %w", address(node), err)
		}
	}
	node, err := findNode(out, res.NodeID)
	if err != nil {
		return nil, err
	}
	result := nodeResult{Node: viewNode(out, node), Diagnostics: nodeDiagnostics(out), BestPractices: bpDelta(wf, out)}
	for i := range out.Nodes {
		if n := &out.Nodes[i]; !before[n.ID] && n.ID != res.NodeID {
			result.Added = append(result.Added, viewNode(out, n))
		}
	}
	return result, nil
}

// patchBody applies set/unset/rename to node and saves it through
// PATCH /workflows/{id}/nodes/{nodeId}.
func (t *tools) patchBody(ctx context.Context, wf *model.Workflow, node *model.Node, set map[string]string, unset []string, rename, file string) (*model.Workflow, error) {
	patch := map[string]any{}
	if len(set) > 0 || len(unset) > 0 {
		body := node.Body
		paths := make([]string, 0, len(set))
		for p := range set {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		for _, p := range paths {
			if err := setAttr(&body, p, set[p]); err != nil {
				return nil, err
			}
		}
		for _, p := range unset {
			if err := unsetAttr(&body, p); err != nil {
				return nil, err
			}
		}
		patch["body"] = body
	}
	if rename != "" {
		patch["name"] = rename
	}
	switch file {
	case "":
	case "auto":
		patch["file"] = ""
	default:
		patch["file"] = file
	}
	if len(patch) == 0 {
		return wf, nil
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPatch, "/workflows/"+pathEscape(wf.ID)+"/nodes/"+pathEscape(node.ID), patch, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type updateNodeIn struct {
	Workflow string            `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string            `json:"node" jsonschema:"node address (aws_s3_bucket.b) or ID"`
	Set      map[string]string `json:"set,omitempty" jsonschema:"attribute path -> HCL expression"`
	Unset    []string          `json:"unset,omitempty" jsonschema:"attribute or block paths to remove"`
	Rename   string            `json:"rename,omitempty" jsonschema:"new Terraform local name"`
	File     string            `json:"file,omitempty" jsonschema:"root .tf file to write the node to, e.g. network.tf; \"auto\" = Nimbo's recommended layout (by category; one main.tf for small projects)"`
}

func (t *tools) updateNode(ctx context.Context, in updateNodeIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	node, err := findNode(wf, in.Node)
	if err != nil {
		return nil, err
	}
	id := node.ID
	if len(in.Set) == 0 && len(in.Unset) == 0 && in.Rename == "" && in.File == "" {
		return nil, errors.New("nothing to do: pass set, unset, rename or file")
	}
	out, err := t.patchBody(ctx, wf, node, in.Set, in.Unset, in.Rename, in.File)
	if err != nil {
		return nil, err
	}
	if node, err = findNode(out, id); err != nil {
		return nil, err
	}
	return nodeResult{Node: viewNode(out, node), Diagnostics: nodeDiagnostics(out), BestPractices: bpDelta(wf, out)}, nil
}

type nodeRefIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string `json:"node" jsonschema:"node address or ID"`
}

func (t *tools) removeNode(ctx context.Context, in nodeRefIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	node, err := findNode(wf, in.Node)
	if err != nil {
		return nil, err
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodDelete, "/workflows/"+pathEscape(wf.ID)+"/nodes/"+pathEscape(node.ID), nil, &out); err != nil {
		return nil, err
	}
	return mutationResult(wf, &out, map[string]any{"removed": address(node)}), nil
}

type connectionIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Source   string `json:"source" jsonschema:"upstream node being referenced (address or ID)"`
	Target   string `json:"target" jsonschema:"node that will hold the reference (address or ID)"`
}

func (t *tools) listConnectionOptions(ctx context.Context, in connectionIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	src, err := findNode(wf, in.Source)
	if err != nil {
		return nil, err
	}
	dst, err := findNode(wf, in.Target)
	if err != nil {
		return nil, err
	}
	q := url.Values{"source": {src.ID}, "target": {dst.ID}}
	var recipes []model.ConnectionRecipe
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/connections?"+q.Encode(), &recipes); err != nil {
		return nil, err
	}
	if len(recipes) == 0 {
		return fmt.Sprintf("No connection recipes from %s to %s. Try the reverse direction, or set a reference attribute with update_node.", address(src), address(dst)), nil
	}
	return recipes, nil
}

type connectIn struct {
	connectionIn
	Recipe string `json:"recipe" jsonschema:"recipe ID from list_connection_options"`
}

func (t *tools) connect(ctx context.Context, in connectIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	src, err := findNode(wf, in.Source)
	if err != nil {
		return nil, err
	}
	dst, err := findNode(wf, in.Target)
	if err != nil {
		return nil, err
	}
	before := map[string]string{}
	for _, n := range wf.Nodes {
		data, _ := json.Marshal(n.Body)
		before[n.ID] = string(data)
	}
	var out model.Workflow
	body := map[string]string{"source": src.ID, "target": dst.ID, "recipeId": in.Recipe}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/connections", body, &out); err != nil {
		return nil, err
	}
	var added, changed []nodeView
	for i := range out.Nodes {
		n := &out.Nodes[i]
		prev, ok := before[n.ID]
		data, _ := json.Marshal(n.Body)
		switch {
		case !ok:
			added = append(added, viewNode(&out, n))
		case prev != string(data):
			changed = append(changed, viewNode(&out, n))
		}
	}
	return mutationResult(wf, &out, map[string]any{"added_nodes": added, "changed_nodes": changed, "edges": viewEdges(&out)}), nil
}

// ---------------------------------------------------------------------------
// Variables, outputs, environments

type setVariableIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name        string `json:"name"`
	Type        string `json:"type,omitempty" jsonschema:"HCL type expression, e.g. string, number, list(string)"`
	Default     string `json:"default,omitempty" jsonschema:"HCL expression, e.g. \"\\\"dev\\\"\" or \"3\""`
	Description string `json:"description,omitempty"`
	Sensitive   *bool  `json:"sensitive,omitempty"`
}

func literal(v any) model.Value {
	data, _ := json.Marshal(v)
	return model.Value{Literal: data}
}

func (t *tools) setVariable(ctx context.Context, in setVariableIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	i := namedIndex(wf.Variables, in.Name)
	if i < 0 {
		wf.Variables = append(wf.Variables, model.NamedBlock{Name: in.Name, Body: model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}})
		i = len(wf.Variables) - 1
	}
	body := &wf.Variables[i].Body
	if in.Type != "" {
		setNamedAttr(body, "type", model.Value{Expr: in.Type})
	}
	if in.Default != "" {
		setNamedAttr(body, "default", model.Value{Expr: in.Default})
	}
	if in.Description != "" {
		setNamedAttr(body, "description", literal(in.Description))
	}
	if in.Sensitive != nil {
		setNamedAttr(body, "sensitive", literal(*in.Sensitive))
	}
	return t.saveAndReport(ctx, wf, func(out *model.Workflow) any {
		return namedViews(out.Variables)
	})
}

type namedRefIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name     string `json:"name"`
}

func (t *tools) removeVariable(ctx context.Context, in namedRefIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	i := namedIndex(wf.Variables, in.Name)
	if i < 0 {
		return nil, fmt.Errorf("variable %q not found", in.Name)
	}
	wf.Variables = append(wf.Variables[:i], wf.Variables[i+1:]...)
	return t.saveAndReport(ctx, wf, func(out *model.Workflow) any { return namedViews(out.Variables) })
}

type setOutputIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name        string `json:"name"`
	Value       string `json:"value" jsonschema:"HCL expression, e.g. aws_sqs_queue.jobs.url"`
	Description string `json:"description,omitempty"`
	Sensitive   *bool  `json:"sensitive,omitempty"`
}

func (t *tools) setOutput(ctx context.Context, in setOutputIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	i := namedIndex(wf.Outputs, in.Name)
	if i < 0 {
		wf.Outputs = append(wf.Outputs, model.NamedBlock{Name: in.Name, Body: model.Body{Attrs: []model.Attr{}, Blocks: []model.Block{}}})
		i = len(wf.Outputs) - 1
	}
	body := &wf.Outputs[i].Body
	setNamedAttr(body, "value", model.Value{Expr: in.Value})
	if in.Description != "" {
		setNamedAttr(body, "description", literal(in.Description))
	}
	if in.Sensitive != nil {
		setNamedAttr(body, "sensitive", literal(*in.Sensitive))
	}
	return t.saveAndReport(ctx, wf, func(out *model.Workflow) any { return namedViews(out.Outputs) })
}

func (t *tools) removeOutput(ctx context.Context, in namedRefIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	i := namedIndex(wf.Outputs, in.Name)
	if i < 0 {
		return nil, fmt.Errorf("output %q not found", in.Name)
	}
	wf.Outputs = append(wf.Outputs[:i], wf.Outputs[i+1:]...)
	return t.saveAndReport(ctx, wf, func(out *model.Workflow) any { return namedViews(out.Outputs) })
}

type setEnvironmentIn struct {
	Workflow   string            `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Name       string            `json:"name" jsonschema:"environment name, e.g. dev"`
	Values     map[string]string `json:"values,omitempty" jsonschema:"variable name -> HCL expression"`
	EnvVars    map[string]string `json:"env_vars,omitempty" jsonschema:"process environment variables for Terraform runs"`
	SecretKeys []string          `json:"secret_keys,omitempty" jsonschema:"env var keys whose values are secret (blank in exports, redacted in get_workflow)"`
	Activate   bool              `json:"activate,omitempty" jsonschema:"make this the active environment used by runs"`
}

func (t *tools) setEnvironment(ctx context.Context, in setEnvironmentIn) (any, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, errors.New("name is required")
	}
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var env *model.Environment
	for i := range wf.Environments {
		if wf.Environments[i].Name == in.Name {
			env = &wf.Environments[i]
		}
	}
	if env == nil {
		wf.Environments = append(wf.Environments, model.Environment{Name: in.Name, Values: map[string]model.Value{}, EnvVars: []model.EnvVar{}})
		env = &wf.Environments[len(wf.Environments)-1]
	}
	if env.Values == nil {
		env.Values = map[string]model.Value{}
	}
	for k, v := range in.Values {
		env.Values[k] = model.Value{Expr: v}
	}
	keys := make([]string, 0, len(in.EnvVars))
	for k := range in.EnvVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		found := false
		for i := range env.EnvVars {
			if env.EnvVars[i].Key == k {
				env.EnvVars[i].Value, found = in.EnvVars[k], true
			}
		}
		if !found {
			env.EnvVars = append(env.EnvVars, model.EnvVar{Key: k, Value: in.EnvVars[k]})
		}
	}
	for _, k := range in.SecretKeys {
		found := false
		for i := range env.EnvVars {
			if env.EnvVars[i].Key == k {
				env.EnvVars[i].Secret, found = true, true
			}
		}
		if !found {
			return nil, fmt.Errorf("secret key %q is not an env var of environment %q", k, in.Name)
		}
	}
	if in.Activate {
		wf.ActiveEnvironment = in.Name
	}
	return t.saveAndReport(ctx, wf, func(out *model.Workflow) any { return viewEnvironments(out) })
}

func namedIndex(blocks []model.NamedBlock, name string) int {
	for i, b := range blocks {
		if b.Name == name {
			return i
		}
	}
	return -1
}

func (t *tools) saveAndReport(ctx context.Context, wf *model.Workflow, view func(*model.Workflow) any) (any, error) {
	out, err := t.c.saveWorkflow(ctx, wf)
	if err != nil {
		return nil, err
	}
	// wf still carries the diagnostics from before the edit.
	return mutationResult(wf, out, map[string]any{"result": view(out)}), nil
}

// ---------------------------------------------------------------------------
// Terraform code

func formatFiles(files map[string]string) string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "# ===== %s =====\n%s\n", n, strings.TrimRight(files[n], "\n"))
	}
	return b.String()
}

func (t *tools) terraformFiles(ctx context.Context, id string) (map[string]string, error) {
	var res struct {
		Files map[string]string `json:"files"`
	}
	err := t.c.get(ctx, "/workflows/"+pathEscape(id)+"/terraform", &res)
	return res.Files, err
}

func (t *tools) getTerraform(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	files, err := t.terraformFiles(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return "The workflow is empty: no Terraform files.", nil
	}
	return formatFiles(files), nil
}

type applyCodeIn struct {
	Workflow string            `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Files    map[string]string `json:"files" jsonschema:"file name (e.g. main.tf) -> full new content; empty string deletes the file"`
}

func (t *tools) applyTerraformCode(ctx context.Context, in applyCodeIn) (any, error) {
	if len(in.Files) == 0 {
		return nil, errors.New("files is required")
	}
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if !wf.IsTerraform() {
		return t.applyFileChanges(ctx, wf, in.Files)
	}
	// Application code and specs (services/<svc>/…, openspec/…) are asset
	// files: send every file as a change so the daemon re-imports the
	// Terraform and stores the rest next to it, in one request.
	if hasAppFiles(in.Files) {
		var out model.Workflow
		if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID)+"/terraform", map[string]any{"changes": fileChanges(in.Files)}, &out); err != nil {
			return nil, err
		}
		v := viewWorkflow(&out)
		v.BestPractices = bpDelta(wf, &out)
		return v, nil
	}
	files, err := t.terraformFiles(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if files == nil {
		files = map[string]string{}
	}
	for name, content := range in.Files {
		if content == "" {
			delete(files, name)
		} else {
			files[name] = content
		}
	}
	var out model.Workflow
	if err := t.c.do(ctx, http.MethodPut, "/workflows/"+pathEscape(wf.ID)+"/terraform", map[string]any{"files": files}, &out); err != nil {
		return nil, err
	}
	v := viewWorkflow(&out)
	v.BestPractices = bpDelta(wf, &out)
	return v, nil
}

// isTerraformPath reports a Terraform code file (.tf, .tf.json) or variable
// file (.tfvars, .tfvars.json): what GET /terraform returns.
func isTerraformPath(p string) bool {
	for _, ext := range []string{".tf", ".tf.json", ".tfvars", ".tfvars.json"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

// hasAppFiles reports a file that is not Terraform code.
func hasAppFiles(files map[string]string) bool {
	for p := range files {
		if !isTerraformPath(p) {
			return true
		}
	}
	return false
}

// fileChanges turns apply_terraform_code files into a code request's
// changes: "" deletes the file.
func fileChanges(files map[string]string) map[string]*string {
	changes := make(map[string]*string, len(files))
	for p, c := range files {
		if c == "" {
			changes[p] = nil
			continue
		}
		changes[p] = &c
	}
	return changes
}

type workflowFilesIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Prefix   string `json:"prefix,omitempty" jsonschema:"return the content of text files whose path starts with it, e.g. openspec/"`
	Path     string `json:"path,omitempty" jsonschema:"return the content of this one file"`
}

// fileView is one workflow file; Content is set for the requested ones.
type fileView struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Kind    string `json:"kind"`
	Content string `json:"content,omitempty"`
}

func (t *tools) getWorkflowFiles(ctx context.Context, in workflowFilesIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	base := "/workflows/" + pathEscape(wf.ID) + "/files"
	if in.Path != "" {
		var f model.WorkflowFileContent
		if err := t.c.get(ctx, base+"/content?path="+url.QueryEscape(in.Path), &f); err != nil {
			return nil, err
		}
		return fileView{Path: f.Path, Size: f.Size, Kind: string(f.Kind), Content: f.Content}, nil
	}
	var list struct {
		Files []model.WorkflowFile `json:"files"`
	}
	if err := t.c.get(ctx, base, &list); err != nil {
		return nil, err
	}
	out := make([]fileView, 0, len(list.Files))
	for _, f := range list.Files {
		v := fileView{Path: f.Path, Size: f.Size, Kind: string(f.Kind)}
		if in.Prefix != "" && strings.HasPrefix(f.Path, in.Prefix) && !f.Binary {
			var c model.WorkflowFileContent
			if err := t.c.get(ctx, base+"/content?path="+url.QueryEscape(f.Path), &c); err != nil {
				return nil, err
			}
			v.Content = c.Content
		}
		out = append(out, v)
	}
	return out, nil
}

type scanIn struct {
	Dir string `json:"dir" jsonschema:"absolute path of a local folder, e.g. a repository checkout"`
}

func (t *tools) scanTerraformFolder(ctx context.Context, in scanIn) (any, error) {
	if strings.TrimSpace(in.Dir) == "" {
		return nil, errors.New("dir is required")
	}
	var scan json.RawMessage
	if err := t.c.do(ctx, http.MethodPost, "/import/scan", map[string]any{"dir": in.Dir}, &scan); err != nil {
		return nil, err
	}
	return scan, nil
}

type importIn struct {
	Dir      string            `json:"dir,omitempty" jsonschema:"absolute path of a local folder with Terraform code"`
	Files    map[string]string `json:"files,omitempty" jsonschema:"relative path -> content (alternative to dir)"`
	Projects []string          `json:"projects,omitempty" jsonschema:"project paths from scan_terraform_folder to import, one workflow each (\".\" = the root; default: the root only)"`
	Name     string            `json:"name,omitempty" jsonschema:"workflow name when a single project is imported (defaults to the directory name)"`
}

// importedView summarizes one imported workflow.
type importedView struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Nodes       int           `json:"nodes"`
	Clouds      []model.Cloud `json:"clouds,omitempty"`
	Diagnostics []diagView    `json:"diagnostics,omitempty"`
	Review      string        `json:"review,omitempty"`
}

func (t *tools) importTerraform(ctx context.Context, in importIn) (any, error) {
	if (in.Dir == "") == (len(in.Files) == 0) {
		return nil, errors.New("pass exactly one of dir or files")
	}
	body := map[string]any{"name": in.Name}
	if in.Dir != "" {
		body["dir"] = in.Dir
	} else {
		body["files"] = in.Files
	}
	if len(in.Projects) > 0 {
		body["projects"] = in.Projects
	}
	var res struct {
		Workflows []model.Workflow `json:"workflows"`
	}
	if err := t.c.do(ctx, http.MethodPost, "/import", body, &res); err != nil {
		return nil, err
	}
	out := make([]importedView, len(res.Workflows))
	for i := range res.Workflows {
		wf := &res.Workflows[i]
		out[i] = importedView{ID: wf.ID, Name: wf.Name, Nodes: len(wf.Nodes), Clouds: wf.Clouds, Diagnostics: nodeDiagnostics(wf), Review: reviewSummary(wf)}
	}
	return map[string]any{"workflows": out, "hint": "call get_workflow with an id for the full graph"}, nil
}

type exportIn struct {
	Workflow     string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Dir          string `json:"dir" jsonschema:"absolute destination directory (created if missing; existing files with the same names are overwritten)"`
	Environments bool   `json:"environments,omitempty" jsonschema:"also write each environment's values: terraform.tfvars and .env.example in environments/<env>/ (environments layout), or the imported variable file / environments/<name>.tfvars and .env.example (single layout)"`
	CI           string `json:"ci,omitempty" jsonschema:"also write a CI/CD pipeline: none (default), github (.github/workflows/<name>.yml) or gitlab (.gitlab-ci.yml); its header and a README section list the secrets and variables to set"`
	Smoke        bool   `json:"smoke,omitempty" jsonschema:"with ci: an HTTP smoke check of each environment's SMOKE_URL after every deploy"`
}

func (t *tools) exportTerraform(ctx context.Context, in exportIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var res struct {
		Dir   string   `json:"dir"`
		Files []string `json:"files"`
	}
	body := map[string]any{"dir": in.Dir, "environments": in.Environments, "ci": in.CI, "smoke": in.Smoke}
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/export", body, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// ---------------------------------------------------------------------------
// Sandbox and runs

func (t *tools) sandboxStatus(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var st model.SandboxStatus
	err = t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/sandbox", &st)
	return st, err
}

func (t *tools) sandboxAction(action string) func(context.Context, workflowArg) (any, error) {
	return func(ctx context.Context, in workflowArg) (any, error) {
		wf, err := t.c.workflow(ctx, in.Workflow)
		if err != nil {
			return nil, err
		}
		var st model.SandboxStatus
		err = t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/sandbox/"+action, nil, &st)
		return st, err
	}
}

type runIn struct {
	Workflow    string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Environment string `json:"environment,omitempty" jsonschema:"environment name; defaults to the active environment (Terraform and Kubernetes workflows)"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"max seconds to wait for completion (default 600); the run keeps going after that, poll with get_run"`
	// Terraform deploy only.
	ConfirmDestroy bool `json:"confirm_destroy,omitempty" jsonschema:"Terraform deploy: apply the plan of the last deploy that returned needs_confirmation, deleting/replacing the resources it listed. Set it ONLY after the user explicitly agreed to that list"`
	// Docker workflows only.
	RemapPorts    bool `json:"remap_ports,omitempty" jsonschema:"docker deploy: publish on free host ports when the compose ports are taken (the compose files stay unchanged)"`
	RemoveVolumes bool `json:"remove_volumes,omitempty" jsonschema:"docker/Kubernetes destroy: also delete the named volumes / PersistentVolumeClaims (their data is lost)"`
	// Kubernetes workflows only.
	DeleteCluster bool `json:"delete_cluster,omitempty" jsonschema:"Kubernetes destroy: also delete the local cluster"`
}

func terminalRun(s model.RunStatus) bool {
	return s == model.RunSucceeded || s == model.RunFailed || s == model.RunCanceled || s == model.RunNeedsConfirmation
}

// dockerAction is the compose action a Terraform run tool stands for in a
// docker workflow: deploy → up, destroy → down. Validate and plan have none.
func dockerAction(action model.RunAction) (model.RunAction, bool) {
	switch action {
	case model.ActionApply:
		return model.ActionUp, true
	case model.ActionDestroy:
		return model.ActionDown, true
	}
	return "", false
}

func (t *tools) runAction(action model.RunAction) func(context.Context, runIn) (any, error) {
	return func(ctx context.Context, in runIn) (any, error) {
		wf, err := t.c.workflow(ctx, in.Workflow)
		if err != nil {
			return nil, err
		}
		body := map[string]any{"action": action, "environment": in.Environment}
		if action == model.ActionApply && in.ConfirmDestroy {
			body["confirmDestroy"] = true
		}
		if wf.IsKubernetes() {
			a, ok := dockerAction(action)
			if !ok {
				return nil, fmt.Errorf("%s is a Terraform action; Kubernetes workflows are rendered and checked against the Kubernetes %s API on every save (see get_workflow diagnostics, review_kubernetes) and run with deploy (apply to the local cluster) and destroy", action, "1.36")
			}
			return t.kubeRun(ctx, wf, map[string]any{"action": a, "environment": in.Environment, "removeVolumes": in.RemoveVolumes, "deleteCluster": in.DeleteCluster}, in.WaitSeconds)
		}
		if wf.IsDocker() {
			a, ok := dockerAction(action)
			if !ok {
				return nil, fmt.Errorf("%s is a Terraform action; docker workflows are checked on every save (see get_workflow diagnostics) and run with deploy (docker compose up) and destroy (down)", action)
			}
			body = map[string]any{"action": a, "remapPorts": in.RemapPorts, "removeVolumes": in.RemoveVolumes}
		}
		var run model.Run
		if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/runs", body, &run); err != nil {
			return nil, err
		}
		wait := time.Duration(in.WaitSeconds) * time.Second
		if wait <= 0 {
			wait = 600 * time.Second
		}
		v, err := t.waitRun(ctx, wf.ID, run.ID, wait)
		if err != nil || !wf.IsDocker() {
			return v, err
		}
		var st model.ComposeStatus
		if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/compose/status", &st); err != nil {
			return v, nil
		}
		return dockerRunView{runView: v, Compose: &st}, nil
	}
}

// dockerRunView is a docker run with the project's container status after it.
type dockerRunView struct {
	runView
	Compose *model.ComposeStatus `json:"compose,omitempty"`
}

func (t *tools) waitRun(ctx context.Context, wfID, runID string, wait time.Duration) (runView, error) {
	deadline := time.Now().Add(wait)
	var run model.Run
	for {
		if err := t.c.get(ctx, "/runs/"+pathEscape(runID), &run); err != nil {
			return runView{}, err
		}
		if terminalRun(run.Status) || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return runView{}, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	v := viewRun(&run)
	if !terminalRun(run.Status) {
		v.Hint = "still running; call get_run with this id"
	}
	if run.Status == model.RunSucceeded && run.Action == model.ActionApply {
		var st model.DeployedState
		if err := t.c.get(ctx, "/workflows/"+pathEscape(wfID)+"/state", &st); err == nil {
			sv := viewState(&st, "", false)
			v.Deployed = &sv
		}
	}
	return v, nil
}

type runRefIn struct {
	Run string `json:"run" jsonschema:"run ID"`
}

func (t *tools) getRun(ctx context.Context, in runRefIn) (any, error) {
	var run model.Run
	if err := t.c.get(ctx, "/runs/"+pathEscape(in.Run), &run); err != nil {
		return nil, err
	}
	return viewRun(&run), nil
}

type stateIn struct {
	Workflow string `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Address  string `json:"address,omitempty" jsonschema:"resource address prefix; matching resources include every value"`
}

func (t *tools) getDeployedState(ctx context.Context, in stateIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	if wf.IsDocker() {
		var st model.ComposeStatus
		if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/compose/status", &st); err != nil {
			return nil, err
		}
		return st, nil
	}
	if wf.IsKubernetes() {
		return t.kubeStatus(ctx, wf)
	}
	var st model.DeployedState
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/state", &st); err != nil {
		return nil, err
	}
	return viewState(&st, in.Address, in.Address != ""), nil
}

// endpointsView is the compact form of model.EndpointsInfo for agents.
type endpointsView struct {
	Sandbox   []sandboxAccessView  `json:"sandbox"`
	Resources []resourceAccessView `json:"resources"`
	Hint      string               `json:"hint,omitempty"`
}

type sandboxAccessView struct {
	Cloud model.Cloud `json:"cloud"`
	URL   string      `json:"url"`
	Env   string      `json:"env"`
}

type resourceAccessView struct {
	Node    string            `json:"node"` // pass to try_endpoint
	Label   string            `json:"label"`
	Kind    string            `json:"kind"`
	Summary string            `json:"summary"`
	URLs    []string          `json:"urls,omitempty"` // "METHOD URL"
	Fields  map[string]string `json:"fields,omitempty"`
	Snippet *model.Snippet    `json:"snippet,omitempty"`
	Actions []string          `json:"actions,omitempty"` // "id (input)"
	Warning string            `json:"warning,omitempty"`
}

func (t *tools) endpoints(ctx context.Context, wfID string) (*model.EndpointsInfo, error) {
	var info model.EndpointsInfo
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wfID)+"/endpoints", &info); err != nil {
		return nil, err
	}
	return &info, nil
}

func accessRef(r *model.ResourceAccess) string {
	if r.NodeID != "" {
		return r.NodeID
	}
	return r.Address
}

func (t *tools) getEndpoints(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	info, err := t.endpoints(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	out := endpointsView{Sandbox: []sandboxAccessView{}, Resources: []resourceAccessView{}}
	for _, s := range info.Sandbox {
		out.Sandbox = append(out.Sandbox, sandboxAccessView{Cloud: s.Cloud, URL: s.URL, Env: s.EnvSnippet})
	}
	refs := map[string]int{}
	for i := range info.Resources {
		refs[accessRef(&info.Resources[i])]++
	}
	for i := range info.Resources {
		r := &info.Resources[i]
		v := resourceAccessView{Node: accessRef(r), Label: r.Label, Kind: r.Kind, Summary: r.Summary, Warning: r.Warning}
		if refs[v.Node] > 1 {
			v.Node = r.Address // count/for_each instances share their node
		}
		for _, ep := range r.Endpoints {
			v.URLs = append(v.URLs, ep.Method+" "+ep.URL)
		}
		if len(r.Fields) > 0 {
			v.Fields = map[string]string{}
			for _, f := range r.Fields {
				v.Fields[f.Label] = f.Value
			}
		}
		if len(r.Snippets) > 0 {
			v.Snippet = &r.Snippets[0]
		}
		for _, a := range r.Actions {
			v.Actions = append(v.Actions, a.ID+" ("+a.Input+")")
		}
		out.Resources = append(out.Resources, v)
	}
	switch {
	case len(out.Resources) == 0:
		out.Hint = "Nothing is deployed. Deploy the workflow first (deploy)."
	case len(out.Sandbox) == 0:
		out.Hint = "The sandbox is not running; URLs stay valid but answer only after sandbox_start."
	default:
		out.Hint = "Use try_endpoint with node and an action to call a resource; the URLs work from this machine with curl, Postman or any AWS SDK."
	}
	return out, nil
}

type tryEndpointIn struct {
	Workflow string            `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Node     string            `json:"node" jsonschema:"resource from get_endpoints: its node value, node ID, address or label"`
	Action   string            `json:"action" jsonschema:"action ID from get_endpoints, e.g. request, invoke, send, receive, publish, list, put, get, scan, start"`
	Input    string            `json:"input,omitempty" jsonschema:"action input: JSON for json actions, text for text actions, the request body for request; the action's default when omitted"`
	Method   string            `json:"method,omitempty" jsonschema:"request action: HTTP method (default GET)"`
	Path     string            `json:"path,omitempty" jsonschema:"request action: a path relative to the API/function URL (e.g. /items) or a full sandbox URL from get_endpoints"`
	Headers  map[string]string `json:"headers,omitempty" jsonschema:"request action: extra HTTP headers"`
}

func (t *tools) tryEndpoint(ctx context.Context, in tryEndpointIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	info, err := t.endpoints(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	ref := ""
	for _, match := range []func(r *model.ResourceAccess) bool{
		func(r *model.ResourceAccess) bool { return r.Address == in.Node },
		func(r *model.ResourceAccess) bool { return r.NodeID == in.Node },
		func(r *model.ResourceAccess) bool { return strings.EqualFold(r.Label, in.Node) },
	} {
		for i := range info.Resources {
			if r := &info.Resources[i]; ref == "" && match(r) {
				ref = r.Address
			}
		}
	}
	if ref == "" {
		return nil, fmt.Errorf("no deployed resource %q; call get_endpoints for the node values", in.Node)
	}
	req := model.TryRequest{NodeID: ref, Action: in.Action, Input: in.Input, Method: in.Method, Path: in.Path, Headers: in.Headers}
	var res model.TryResult
	if err := t.c.do(ctx, http.MethodPost, "/workflows/"+pathEscape(wf.ID)+"/endpoints/try", req, &res); err != nil {
		return nil, err
	}
	if len(res.Body) > 8000 {
		res.Body = res.Body[:8000] + "\n… (truncated)"
	}
	res.Headers = nil
	return res, nil
}

// ---------------------------------------------------------------------------
// Load tests

func (t *tools) loadTargets(ctx context.Context, wfID string) ([]model.LoadTarget, error) {
	var targets []model.LoadTarget
	err := t.c.get(ctx, "/workflows/"+pathEscape(wfID)+"/loadtests/targets", &targets)
	return targets, err
}

func (t *tools) listLoadTargets(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	targets, err := t.loadTargets(ctx, wf.ID)
	if err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return "No load targets. Deploy the workflow first (deploy), or pass a custom HTTP/AWS target object to run_load_test.", nil
	}
	type item struct {
		Index int `json:"index"`
		model.LoadTarget
	}
	out := make([]item, len(targets))
	for i, tg := range targets {
		out[i] = item{i, tg}
	}
	return out, nil
}

type loadTestIn struct {
	Workflow        string              `json:"workflow,omitempty" jsonschema:"workflow ID or name; omit or \"current\" for the workflow open in the Nimbo app"`
	Target          any                 `json:"target" jsonschema:"index from list_load_targets, or a target object {kind: http|aws|sql|redis|mongo, label, connectionId?, http: {method, url, headers?, body?} | aws: {operation, resource, payload?, key?} | sql: {query, args?, mode} | redis: {command} | mongo: {database, collection?, operation, document?, update?}}; connectionId (see list_connections) targets a saved external service and sql/redis/mongo require it"`
	Preset          string              `json:"preset,omitempty" jsonschema:"smoke (default), load, stress, spike, soak or autoscale (Kubernetes: ramp to 200 req/s over 2 min and hold 2 min so an HPA can add pods)"`
	Stages          []model.LoadStage   `json:"stages,omitempty" jsonschema:"custom ramp stages overriding the preset; each ramps linearly to target over durationSec"`
	Mode            string              `json:"mode,omitempty" jsonschema:"rps (target = requests/second) or concurrency (target = virtual users)"`
	Thresholds      []model.Threshold   `json:"thresholds,omitempty" jsonschema:"pass/fail criteria, e.g. {metric: p95_ms, op: <, value: 200}; metrics p50_ms p90_ms p95_ms p99_ms max_ms error_rate rps_min"`
	ConfirmExternal bool                `json:"confirm_external,omitempty" jsonschema:"set true ONLY after the user explicitly approved sending this traffic to a service outside the local sandbox (a connection target or a non-local URL)"`
	Wait            *bool               `json:"wait,omitempty" jsonschema:"wait for the result (default true)"`
	Scenario        *model.ScenarioSpec `json:"scenario,omitempty" jsonschema:"docker/Kubernetes workflows: incident to inject during the run, {id: kill_replica|dependency_down|dependency_latency|cache_flush|traffic_spike, target: node id from list_scenarios (omit for traffic_spike), atSec: seconds after load starts (>= 5; leave ~10 s of baseline), durationSec: how long the fault lasts (0 = scenario default), params: string numbers e.g. {delayMs: \"300\"} or {factor: \"3\"}}; atSec + durationSec must fit in the profile"`
}

func (t *tools) listScenarios(ctx context.Context, in workflowArg) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	var cat model.ScenarioCatalog
	if err := t.c.get(ctx, "/workflows/"+pathEscape(wf.ID)+"/scenarios", &cat); err != nil {
		return nil, err
	}
	if cat.Kind == "" {
		return "Scenarios need a docker or Kubernetes workflow.", nil
	}
	return cat, nil
}

func (t *tools) runLoadTest(ctx context.Context, in loadTestIn) (any, error) {
	wf, err := t.c.workflow(ctx, in.Workflow)
	if err != nil {
		return nil, err
	}
	target, err := t.resolveTarget(ctx, in.Target, func(ctx context.Context) ([]model.LoadTarget, error) {
		return t.loadTargets(ctx, wf.ID)
	})
	if err != nil {
		return nil, err
	}
	spec, err := t.loadSpec(ctx, target, in.Preset, in.Stages, in.Mode, in.Thresholds)
	if err != nil {
		return nil, err
	}
	spec.ConfirmExternal = in.ConfirmExternal
	spec.Scenario = in.Scenario
	return t.startLoadRun(ctx, "/workflows/"+pathEscape(wf.ID)+"/loadtests", spec, in.Wait)
}

// loadSpec builds a spec from a preset (default smoke), optionally replaced
// by custom stages and mode.
func (t *tools) loadSpec(ctx context.Context, target model.LoadTarget, preset string, stages []model.LoadStage, mode string, thresholds []model.Threshold) (model.LoadTestSpec, error) {
	if preset == "" {
		preset = "smoke"
	}
	var presets []model.LoadProfile
	if err := t.c.get(ctx, "/catalog/load-presets", &presets); err != nil {
		return model.LoadTestSpec{}, err
	}
	var profile *model.LoadProfile
	var names []string
	for i := range presets {
		names = append(names, presets[i].Preset)
		if presets[i].Preset == preset {
			profile = &presets[i]
		}
	}
	if profile == nil {
		return model.LoadTestSpec{}, fmt.Errorf("unknown preset %q; available: %s", preset, strings.Join(names, ", "))
	}
	if len(stages) > 0 {
		profile.Stages = stages
		profile.Preset = "custom"
		profile.Label = ""
	}
	if mode != "" {
		profile.Mode = model.LoadMode(mode)
	}
	return model.LoadTestSpec{
		Name:       fmt.Sprintf("%s · %s", target.Label, profile.Preset),
		Target:     target,
		Profile:    *profile,
		Thresholds: thresholds,
	}, nil
}

// startLoadRun POSTs spec to path and, unless wait is false, polls until
// the run ends (or its planned duration plus two minutes elapses).
func (t *tools) startLoadRun(ctx context.Context, path string, spec model.LoadTestSpec, wait *bool) (any, error) {
	var run model.LoadTestRun
	if err := t.c.do(ctx, http.MethodPost, path, spec, &run); err != nil {
		var ae *apiError
		if errors.As(err, &ae) && ae.Code == "confirmation_required" {
			return nil, fmt.Errorf("%s. This target is outside the local sandbox: ask the user for explicit approval to send this traffic (write operations create real data), then retry with confirm_external=true", ae.Message)
		}
		return nil, err
	}
	if wait != nil && !*wait {
		v := viewLoadRun(&run)
		v.Hint = "started; call get_load_test with this id"
		return v, nil
	}
	total := 0
	for _, s := range spec.Profile.Stages {
		total += s.DurationSec
	}
	deadline := time.Now().Add(time.Duration(total)*time.Second + 2*time.Minute)
	for run.Status == model.LoadRunning && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
		if err := t.c.get(ctx, "/loadtests/"+pathEscape(run.ID), &run); err != nil {
			return nil, err
		}
	}
	v := viewLoadRun(&run)
	if run.Status == model.LoadRunning {
		v.Hint = "still running; call get_load_test with this id"
	}
	return v, nil
}

// resolveTarget accepts an index into the suggested targets (fetched with
// list only when needed) or a full target object.
func (t *tools) resolveTarget(ctx context.Context, raw any, list func(context.Context) ([]model.LoadTarget, error)) (model.LoadTarget, error) {
	var idx int
	switch v := raw.(type) {
	case float64:
		idx = int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			return model.LoadTarget{}, fmt.Errorf("target %q is neither an index nor a target object", v)
		}
		idx = n
	case map[string]any:
		data, _ := json.Marshal(v)
		var tg model.LoadTarget
		if err := json.Unmarshal(data, &tg); err != nil {
			return model.LoadTarget{}, fmt.Errorf("invalid target object: %w", err)
		}
		ok := false
		switch tg.Kind {
		case model.TargetHTTP:
			ok = tg.HTTP != nil
		case model.TargetAWS:
			ok = tg.AWS != nil
		case model.TargetSQL:
			ok = tg.SQL != nil
		case model.TargetRedis:
			ok = tg.Redis != nil
		case model.TargetMongo:
			ok = tg.Mongo != nil
		}
		if !ok {
			return model.LoadTarget{}, errors.New("target object needs a kind (http, aws, sql, redis or mongo) and the object of the same name, e.g. {kind: http, http: {...}}")
		}
		if tg.Label == "" {
			tg.Label = string(tg.Kind) + " target"
		}
		return tg, nil
	default:
		return model.LoadTarget{}, errors.New("target is required: an index from the list targets tool or a target object")
	}
	targets, err := list(ctx)
	if err != nil {
		return model.LoadTarget{}, err
	}
	if idx < 0 || idx >= len(targets) {
		return model.LoadTarget{}, fmt.Errorf("target index %d out of range (%d targets; list them first)", idx, len(targets))
	}
	return targets[idx], nil
}

type loadRefIn struct {
	Run string `json:"run" jsonschema:"load test run ID"`
}

func (t *tools) getLoadTest(ctx context.Context, in loadRefIn) (any, error) {
	var run model.LoadTestRun
	if err := t.c.get(ctx, "/loadtests/"+pathEscape(in.Run), &run); err != nil {
		return nil, err
	}
	return viewLoadRun(&run), nil
}
