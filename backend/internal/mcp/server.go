// Package mcp exposes Nimbo to AI agents as an MCP server. Every tool
// is a client of the nimbo REST API, so edits made by an agent show up live
// in the desktop app through the daemon's event stream.
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `Nimbo lets you design cloud infrastructure as a graph of Terraform resources, deploy it into a local Floci emulator sandbox (AWS, Azure, GCP, OCI in Docker; nothing touches a real cloud) and load-test it. The user watches your edits live in the Nimbo desktop app.

The workflow open in the desktop app is "current": every workflow argument accepts "current" (or "open"), and may be omitted, to act on it. get_open_workflow shows it; when the user says "this workflow" or "the open one", use current. If nothing is open the tools say so: ask the user or call list_workflows.

Recommended loop:
1. get_open_workflow or list_workflows / list_templates, then create_workflow (a template is the fastest start) or get_workflow on an existing one.
2. search_resources (prefer floci_supported=true) and describe_resource to learn required attributes.
3. add_node for each resource; set attributes as HCL expressions (strings must be quoted: "\"my-bucket\"").
4. list_connection_options + connect to wire nodes (curated recipes add IAM policies, event source mappings, permissions). References between nodes are what create edges.
5. Check diagnostics returned by every edit (get_workflow shows all); fix errors with update_node. Fix the best_practices findings an edit returns right away.
6. validate, then deploy (terraform apply into the local sandbox). Read the log tail on failure, fix, and retry.
7. list_load_targets, then run_load_test (start with preset smoke, then load/stress) and report latency, throughput, error rate and threshold results.
8. export_terraform to a directory when the user wants the code. get_terraform shows it inline; apply_terraform_code edits by code.

Networks: use check_network after designing or changing networks (VPCs, subnets, route tables, gateways, security groups) and fix its net_* findings; then check_reachability for the paths that matter (internet → load balancer:443, app → database:5432, database → internet should be blocked). To show traffic really flowing, add_test_instance into a public subnet, deploy, then check_reachability with live=true.

Code quality: the Terraform you write is handed to the user as production code, so follow the practices Nimbo's starters already use.
- Configurable values (sizes, counts, CIDRs, retention, region) are variables with type and description, or locals for values used in several places; no hardcoded regions, zones or account IDs (use var.aws_region, data.aws_availability_zones, data.aws_caller_identity).
- Names come from local.name_prefix (project + environment): "${local.name_prefix}-orders". The first resource of a cloud adds variables project/environment, locals name_prefix/common_tags, region variables and AWS default_tags; keep using them. Azure resources take tags = local.common_tags, GCP resources labels = local.common_labels.
- Every variable and output has a description; secrets are sensitive = true; providers stay pinned in required_providers.
- Secure defaults: encryption on, no public buckets unless the user wants a public website, no 0.0.0.0/0 to SSH/RDP/database ports, IAM scoped to the ARNs used (never "*" on "*"), Lambda log groups with retention.
- Group related resources into modules (below).
- Before you finish, call review_terraform and fix its findings (warnings first) or tell the user which ones you leave and why. Findings never block deploys.

Modules: group related resources into a local module with extract_module when one concern has many resources or is repeated — a network (VPC + subnets + route tables + gateways), a service (function + IAM role + policies + log group + permissions), a queue with its dead-letter queue and alarms. Pick a snake_case name for the concern (network, orders_api). The root then calls module.<name> with clear inputs (typed, described variables) and reads its outputs; moved blocks keep anything already deployed (the next plan only moves, 0 to add/change/destroy). Use dry_run=true to review the inputs/outputs first; inline_module undoes it. Keep shared plumbing (providers, variables, locals) in the root.

Existing code: scan_terraform_folder lists the Terraform projects in a folder or repository, import_terraform imports the chosen ones (one workflow each; local modules are kept).
Already-deployed services outside the sandbox (connections tools) receive real traffic: always get the user's explicit confirmation first.
Finished work can be closed with archive_workflow (hidden from list_workflows unless include_archived=true; runs are refused until unarchive_workflow).

Prompts: create_infrastructure, extend_workflow, import_terraform_folder, load_test and test_deployed_service are step-by-step recipes for the common tasks.

Nodes are referenced by address (aws_sqs_queue.jobs) or ID. Workflows by ID, exact name or "current". Runs and load tests wait for completion by default.`

// NewServer returns an MCP server whose tools call the daemon at baseURL
// (e.g. "http://127.0.0.1:7788").
func NewServer(baseURL, version string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "nimbo", Title: "Nimbo", Version: version}, &sdk.ServerOptions{
		Instructions: instructions,
		Capabilities: &sdk.ServerCapabilities{},
	})
	newRegistry(newClient(baseURL)).installOn(s)
	return s
}

// Run serves MCP over stdin/stdout until the client disconnects or ctx ends.
func Run(ctx context.Context, baseURL, version string) error {
	return NewServer(baseURL, version).Run(ctx, &sdk.StdioTransport{})
}
