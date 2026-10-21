# Nimbo MCP server

`nimbo mcp` is a [Model Context Protocol](https://modelcontextprotocol.io)
server over stdio. It lets AI agents (Claude Code, Cursor, Codex, …) design
infrastructure, deploy it into the local Floci sandbox and load-test it.

Every tool is a client of the `nimbo` REST API, so the desktop app shows the
agent's edits live. On start, `nimbo mcp` reads `<home>/daemon.json` and
attaches to the running daemon (normally the one the desktop app started).
If none answers, it starts the daemon in-process on `127.0.0.1:<free port>`
and writes `daemon.json`, so the desktop app can attach to it instead.

Requirements: Docker running (for sandboxes and Terraform runs). The home
directory defaults to `$NIMBO_HOME` or `~/.nimbo`; pass
`--home DIR` to use another one. Use the same home as the desktop app to
share workflows.

## Register the server

Easiest: in the desktop app open **Settings → AI agents (MCP)** (⌘, or ⌘K
"Connect an AI agent") and click **Install** on Claude Code, Claude Desktop,
Cursor, Codex, VS Code, Windsurf or Gemini CLI. Nimbo adds a `nimbo` entry
to that app's user config (keeping every other server, comments included,
and saving `<file>.<timestamp>.bak` first); **Remove** deletes only that
entry. Each card also shows the snippet for manual setup.

Manually: build the binary first (`cd backend && go build -o bin/nimbo ./cmd/nimbo`) or
use the one shipped with the desktop app.

### Claude Code

```sh
claude mcp add --scope user nimbo -- /path/to/nimbo mcp
```

### Cursor (`~/.cursor/mcp.json`) and other JSON-configured clients

```json
{
  "mcpServers": {
    "nimbo": {
      "command": "/path/to/nimbo",
      "args": ["mcp"]
    }
  }
}
```

### Codex (`~/.codex/config.toml`)

```toml
[mcp_servers.nimbo]
command = "/path/to/nimbo"
args = ["mcp"]
```

## What you can do

Just ask your agent in plain language (any language). The workflow open in
the desktop app is the **current** one, so "this workflow" works: the app
tells the daemon what is open and the tools use it when no workflow is named.

| Goal | English | Español |
|---|---|---|
| Create from scratch (AWS) | "Create an HTTP API on AWS that stores orders in DynamoDB, deploy it and smoke test it." | "Crea una API HTTP en AWS que guarde pedidos en DynamoDB, despliégala y haz un smoke test." |
| Create from scratch (GCP) | "Build a Cloud Storage bucket that triggers a Cloud Function on GCP." | "Arma en GCP un bucket de Cloud Storage que dispare una Cloud Function." |
| Create from scratch (Azure) | "Set up an Azure storage account with a queue and a function app that processes it." | "Monta en Azure una storage account con una cola y una function app que la procese." |
| Create from scratch (OCI) | "Create an OCI VCN with a subnet and an Object Storage bucket." | "Crea en OCI una VCN con una subred y un bucket de Object Storage." |
| Extend the open workflow | "Add an SQS queue to the current workflow and connect it to the Lambda." | "Agrega una cola SQS al workflow abierto y conéctala a la Lambda." |
| Import a repo folder | "Import the Terraform in ~/code/platform and tell me what's in it." | "Importa el Terraform de ~/code/platform y explícame qué contiene." |
| Use what you deployed | "What's the URL of my API and how do I call it? Send a test order to it." | "¿Cuál es la URL de mi API y cómo la llamo? Envíale un pedido de prueba." |
| Deploy + load test | "Deploy this workflow and load test the API: I need p95 under 200 ms at 50 rps." | "Despliega este workflow y haz una prueba de carga a la API: necesito p95 menor a 200 ms con 50 rps." |
| Test a network | "Check my VPC design and tell me if the database is reachable from the internet." | "Revisa el diseño de mi VPC y dime si la base de datos es accesible desde internet." |
| See traffic really flow | "Add a test instance to the public subnet, deploy, and prove port 80 is reachable." | "Agrega una instancia de prueba a la subred pública, despliega y demuestra que el puerto 80 es accesible." |
| Test a deployed service | "Load test our staging API at https://staging.example.com using a connection." | "Haz una prueba de carga a nuestra API de staging en https://staging.example.com usando una conexión." |
| Close finished work | "Archive the old demo workflows." | "Archiva los workflows de demo viejos." |

## Prompts

Step-by-step recipes for the tasks above; most clients show them as slash
commands (e.g. `/mcp__nimbo__extend_workflow` in Claude Code).

| Prompt | Arguments | What it guides |
|---|---|---|
| `create_infrastructure` | `request`, `cloud?` | Plan the structure (layers, modules, environments) → `list_templates` → `create_workflow` → `search_resources`/`describe_resource` → `add_node` → `connect` → one module per logical group with `extract_module` → environments → `review_terraform` (structure + findings) → `validate` → `deploy` → smoke test → summary |
| `plan_structure` | `request` | Design before building: inventory per logical group, states by size (one workflow; modules; one workflow per layer past ~100 resources), environments and their inputs, modules per workflow; present a tree + table and wait for the user's OK, then build it |
| `extend_workflow` | `request`, `workflow?` (default `current`) | Inspect the graph and its structure, place the addition in its logical group (or a new layer workflow past ~100 resources), add and connect services, extract a group that grew large into a module, `review_terraform`, `validate`, `plan` |
| `import_terraform_folder` | `dir` | `scan_terraform_folder` → `import_terraform` → summary of projects, diagnostics and Floci support |
| `load_test` | `workflow?` (default `current`), `goal?` | Deploy if needed, choose targets, smoke test, then the profile that answers the goal; interpret the results |
| `test_deployed_service` | `connection?` | Connections flow with explicit user confirmation before any traffic |

## Recommended loop

1. `get_open_workflow`, or `list_templates` → `create_workflow` (or `get_workflow` on an existing one). New or growing designs: plan the structure first (`plan_structure`, see "Structure").
2. `search_resources` (prefer `floci_supported`) → `describe_resource`.
3. `add_node` with attributes as HCL expressions (strings quoted: `"\"my-bucket\""`).
4. `list_connection_options` → `connect`.
5. Fix diagnostics returned by every edit (`update_node`).
   Group a concern with many resources, or one that repeats, into a local
   module: `extract_module` (`dry_run: true` first) — see "Modules" below.
6. `validate` → `deploy`.
7. `get_endpoints` → `try_endpoint` to show the user how to call what was
   deployed (URLs, queue URLs, snippets) and smoke test it.
8. `list_load_targets` → `run_load_test` (start with `smoke`).
9. `export_terraform` to hand the code over.

Networks: after designing or changing VPCs, subnets, route tables, gateways
or security groups, call `check_network` and fix its `net_*` findings
(errors first), then `check_reachability` (AWS only today: Azure and GCP
endpoints are rejected) for the paths that matter
(internet → load balancer:443 ✓, app → database:5432 ✓, database →
internet ✗). To show real traffic, `add_test_instance` into a public
subnet, `deploy`, then `check_reachability` with `live: true`: each hop says
whether the local emulator verified it (`enforcement: live`) or only the
analysis did (`analysis`: Floci doesn't enforce security groups between
instances, network ACLs or route tables). Network findings never block
deploys.

Existing code: `scan_terraform_folder` → `import_terraform` with `projects`
(one workflow per project; local modules are kept and can be opened in the
app).

## Modules

Agents are told to keep the root small and readable: a concern with many
resources or one that repeats goes into a local module, called from the
root with clear inputs and outputs. Typical groups: a network (VPC +
subnets + route tables + gateways), a service (function + IAM role +
policies + log group + permissions), a queue with its dead-letter queue and
alarms. Providers, variables and locals shared by everything stay in the
root.

`extract_module` moves the chosen nodes into `modules/<name>/`
(`versions.tf` with `required_providers`, `variables.tf`, `outputs.tf`,
`main.tf` or category files, `README.md`) and calls it in their place:

- what the group reads from the root becomes typed, described input
  variables (types from the provider schema of the referenced attribute,
  else of the attribute they feed, else the root variable's type, else
  `string`/`number`/`bool` for a root local holding a string template or
  literal such as `local.name_prefix`, else `any`);
- what the root reads from the group becomes described outputs, and the
  root references become `module.<name>.<output>`; `depends_on` on a moved
  block becomes `depends_on = [module.<name>]`;
- non-default provider aliases are passed with `providers = {}`;
- root `moved` blocks keep deployed resources (count/for_each instances
  too): the next plan only moves them — 0 to add, 0 to change, 0 to destroy
  (verified on Floci, see TERRAFORM_FIDELITY.md).

Selections that would form a dependency cycle through `depends_on`, or
where `lifecycle.replace_triggered_by` crosses the module boundary, are
rejected with the reason. `inline_module` reverses it for a local module
called once without count/for_each (reverse `moved` blocks; the module's
`.tf` files are deleted).

Already-deployed services (no workflow): `create_connection` →
`test_connection` → `list_connection_targets` → ask the user →
`run_connection_load_test` with `confirm_external: true`.

## Structure

Agents decide the structure before adding resources (prompt
`plan_structure`) and follow the same rules in `create_infrastructure` and
`extend_workflow`. Sources: the [HashiCorp style
guide](https://developer.hashicorp.com/terraform/language/style), Google
Cloud's [root module best
practices](https://cloud.google.com/docs/terraform/best-practices/root-modules)
and [AWS Prescriptive Guidance: Terraform AWS provider best
practices — code
structure](https://docs.aws.amazon.com/prescriptive-guidance/latest/terraform-aws-provider-best-practices/structure.html).

One workflow is one root module with one state per environment. Its export
layout (`update_workflow` `layout`; `structure.layout` and
`structure.layout_setting` in `get_workflow`) is `auto` by default:
`environments` from two environments up, else `single`. Auto falls back to
`single` when the workflow can't be split into environments (an environment
name that can't name a directory, a provider configuration that reads a
resource); `structure.layout_note` says why.

```
<workflow>/                    # layout "environments"
├── README.md  .gitignore      # .gitignore keeps .terraform.lock.hcl
├── modules/
│   ├── <service>/             # the workflow's resources: one service module
│   │   ├── main.tf | <category>.tf  variables.tf  outputs.tf
│   │   ├── versions.tf        # required_providers with >= minimums, no provider blocks
│   │   └── README.md
│   └── network/  data/  …     # the workflow's local modules (logical groups)
└── environments/
    ├── dev/                   # one root = one state
    │   ├── backend.tf         # remote backend, partial config (+ backend.hcl.example)
    │   ├── main.tf            # module "<service>" { source = "../../modules/<service>" … }
    │   ├── providers.tf  versions.tf (~> pins)  variables.tf  outputs.tf
    │   ├── terraform.tfvars   # this environment's non-secret values
    │   └── .env.example       # env vars (secrets left blank)
    └── prod/
```

Rules the agents follow (and the `bp_*` rule that checks each):

- **Size.** A small design (a few dozen resources, one environment) stays a
  single root. At most ~100 resources per state, ideally a few dozen
  (Google): past that, one workflow per layer (network, data, app), created
  bottom-up; upper layers find lower ones with data sources by tags or IDs
  (`data.aws_vpc`, `data.aws_subnets`), never by sharing state
  (`bp_state_too_large`: info from 50 instances, warning from 100).
- **Environments.** One state per environment, never CLI workspaces
  (`bp_workspace_branching`); with two or more environments, the
  environments layout, or a backend with a key per environment in the
  single layout (`bp_no_remote_backend`). Values per environment with
  `set_environment` (each environment's `terraform.tfvars`); secrets are
  sensitive variables fed from secret env vars (`TF_VAR_<name>`), never
  tfvars values.
- **Modules.** One per logical group — network foundation, data tier,
  security controls, an application (`bp_module_candidate`) — never around a
  single resource (`bp_module_thin_wrapper`); no provider blocks inside
  (`bp_provider_in_module`); at most 2 levels of nesting below a root, the
  environments layout's service module being the first
  (`bp_module_nesting_depth`); every resource reachable from an output
  (`bp_module_output_missing`); environment-specific inputs without
  defaults, environment-independent ones with defaults; units in names
  (`ram_size_gb`) and positive booleans (`enable_x`).
- **Versions.** `~>` pins in roots (`required_version`, providers;
  `bp_required_version`, `bp_provider_version`), `>=` minimums in modules;
  registry modules with `version`, git modules with `?ref=<tag>`
  (`bp_module_version_unpinned`).
- **Attachments.** Prefer standalone rule/attachment resources
  (`aws_vpc_security_group_ingress_rule`, `aws_iam_role_policy_attachment`)
  over inline blocks; mixing both styles is flagged (`bp_inline_sg_rules`,
  a warning when both target the same group).

`get_workflow`, `get_open_workflow`, `update_workflow` and
`review_terraform` return a `structure` summary: `layout`, `states`,
`resources_per_state` (root + local modules, constant count/for_each
expanded, feature-flagged blocks counted as absent), `root_resources`,
`modules` (call → directory or source, kind, resources, depth),
`max_nesting`, `backend` and where `state` lives
(`GET /workflows/{id}/structure`).

## Safety: real services

Connections point at services outside the local sandbox — production or
staging APIs, real databases, a real AWS account (which may cost money).
The daemon refuses any load test against them (a target with
`connectionId`, or an HTTP URL that is not local) unless
`confirm_external` is true, before sending a single request. Agents must
ask the user for explicit approval — naming the service, target and load
profile — before setting it. Write targets (`POST`, `INSERT`, `SET`,
`insertOne`, `SendMessage`, `PutItem`, …) create real data. `test_connection`
and `list_connection_targets` also contact the real service (read-only).
`try_endpoint` only ever calls the workflow's local sandbox; its write
actions (`send`, `publish`, `put`, `start`, POST requests) change sandbox data
only.

## Code quality

Terraform written through Nimbo is meant to be handed over as production
code. The server instructions and the `create_infrastructure` /
`extend_workflow` / `plan_structure` prompts tell agents to use typed,
described variables and locals for configurable values, derive names from
`local.name_prefix`, describe outputs, mark secrets `sensitive`, follow the
structure rules above (modules per logical group with `extract_module`, a
state per environment, a workflow per layer for large designs) and call
`review_terraform` before finishing.

- **Generated defaults.** The first resource of a cloud adds variables
  `project` (default: workflow name slug) and `environment` (default `dev`),
  both validated; locals `name_prefix = "${var.project}-${var.environment}"`
  and `common_tags` (`common_labels` on GCP); a region variable used by the
  provider (`aws_region`, `location`, `gcp_region`, `oci_region`); AWS
  `default_tags { tags = local.common_tags }` and `required_version`.
  Starters name resources `"${local.name_prefix}-<role>"`, tag Azure/GCP
  resources, and come with secure defaults (S3 SSE + public access block,
  DynamoDB SSE, SQS managed SSE, RDS `storage_encrypted`, Lambda log group
  with 14-day retention and a log policy scoped to it). Projects that already
  have a provider block for the cloud keep their own scheme.
- **Findings.** Best-practice checks run on every save: diagnostics with
  codes `bp_*`, severity `info` or `warning` (never `error`, so they never
  block a deploy), a why (`detail`) and a `fix`. They are kept out of tool
  `diagnostics`; `get_workflow` shows a one-line `review` summary.
- **`review_terraform`** returns the `structure` summary and the findings
  grouped into `warnings` and `info`, each with `why` and `fix`, plus
  `disabled_rules` and what to do next. Module rules read the local modules
  the workflow calls (never registry or remote module code).
- **Mutating tools** (`create_workflow`, `add_node`, `update_node`,
  `remove_node`, `connect`, `set_*`, `remove_*`, `apply_terraform_code`)
  return `best_practices`: only the findings their change introduced, in a
  compact form (`code`, `node`, `message`, `fix`), so agents fix them right
  away.
- Rules are toggled in the app (**Settings → Best-practice checks**: on by
  default, off as a whole or per rule); `GET /best-practices/rules` lists them.

## Conventions

- **Workflows** are referenced by ID or exact name. `"current"` (or
  `"open"`) means the workflow open in the desktop app, and every
  `workflow` argument may be omitted to mean the same. If nothing is open the
  tool fails with a clear message; `get_open_workflow` reports it without
  failing. `list_workflows` flags the open one (`open: true`) and hides
  archived workflows unless `include_archived` is true.
- **Nodes** are referenced by address (`aws_sqs_queue.jobs`,
  `data.aws_iam_policy_document.p`, `module.vpc`), ID, or a unique local name.
- **Values** are HCL expressions: `"\"text\""`, `3`, `true`,
  `aws_sqs_queue.jobs.arn`, `{ env = var.env }`.
- **Attribute paths** in `add_node.attributes` and `update_node.set/unset`
  address nested blocks: `versioning.enabled`, `rule[1].id`. Missing blocks
  are created. Use `apply_terraform_code` for complex blocks.
- Runs (`validate`, `plan`, `deploy`, `destroy`) and `run_load_test` wait
  for the result by default (runs: `wait_seconds`, default 600). If they
  return while still running, poll with `get_run` / `get_load_test`.
- Secret environment values and values of `sensitive` variables are
  redacted in `get_workflow`.
- **Connections** are referenced by ID or exact name. Their secrets
  (`password`, `uri`, `auth.token`, `auth.password`) are stored locally
  (`connections.json`, 0600) and never returned: `secretsSet` names the
  stored ones, and credential-looking header values are shown as
  `[redacted]`. `update_connection` changes only the given fields; omitted
  or blank secrets keep their stored value, `clear_secrets` removes them.

## Tools

All `workflow` arguments are optional and default to `current` (see
Conventions).

| Tool | Arguments | Purpose |
|---|---|---|
| `list_workflows` | `include_archived?` | Workflows, most recent first; the open one is flagged |
| `get_open_workflow` | — | The workflow open in the desktop app, like `get_workflow` (or a message when none is) |
| `list_templates` | — | Starter templates |
| `create_workflow` | `name`, `description?`, `template?` | New workflow, optionally from a template |
| `get_workflow` | `workflow` | Compact workflow: nodes, edges, variables, outputs, environments, diagnostics, and `structure` (layout, states, resources per state, modules, nesting, backend) |
| `update_workflow` | `workflow`, `name?`, `description?`, `layout?` (`auto`/`single`/`environments`) | Rename a workflow, change its description or its export layout (only the given fields change; `description: ""` clears it); the Terraform is unchanged; returns the workflow with its `structure` |
| `archive_workflow` / `unarchive_workflow` | `workflow` | Close a workflow (sandbox stopped keeping state, hidden, runs refused) or restore it |
| `search_resources` | `query`, `cloud?`, `kind?`, `supported_only?`, `limit?` | Search the resource catalog |
| `describe_resource` | `type`, `kind?` | Required/optional attributes, nested blocks, exported attributes, Floci support |
| `add_node` | `workflow`, `type`, `name?`, `kind?`, `attributes?`, `x?`, `y?` | Add a node (curated starters add helper nodes) |
| `update_node` | `workflow`, `node`, `set?`, `unset?`, `rename?`, `file?` | Edit attributes, rename (references are rewritten) or move the node to another root `.tf` file (`"auto"` = recommended layout); nodes report their `file` |
| `remove_node` | `workflow`, `node` | Remove a node |
| `list_connection_options` | `workflow`, `source`, `target` | Connection recipes between two nodes |
| `connect` | `workflow`, `source`, `target`, `recipe` | Apply a recipe |
| `extract_module` | `workflow`, `nodes`, `name` (snake_case), `dry_run?` | Move related nodes into a new local module `modules/<name>` called as `module.<name>`: typed inputs, described outputs, rewritten references/`depends_on`, provider alias pass-through and root `moved` blocks. `dry_run` only reports inputs, outputs, moved blocks and files |
| `inline_module` | `workflow`, `node` | Bring a local module's blocks back to the root (called once, no count/for_each), with reverse `moved` blocks |
| `set_variable` | `workflow`, `name`, `type?`, `default?`, `description?`, `sensitive?` | Create/update an input variable |
| `remove_variable` | `workflow`, `name` | Remove a variable |
| `set_output` | `workflow`, `name`, `value`, `description?`, `sensitive?` | Create/update an output |
| `remove_output` | `workflow`, `name` | Remove an output |
| `set_environment` | `workflow`, `name`, `values?`, `env_vars?`, `secret_keys?`, `activate?` | Create/update an environment |
| `get_terraform` | `workflow` | Rendered Terraform files |
| `review_terraform` | `workflow` | `structure` summary plus best-practice findings grouped by severity, with why and fix (see Code quality and Structure) |
| `apply_terraform_code` | `workflow`, `files` | Edit by code (merged over current files; `""` deletes a file, and its blocks are gone unless the same call writes them to another file). Every block stays in the file it is written in; applying the same files again changes nothing |
| `scan_terraform_folder` | `dir` | Terraform projects in a folder/repo (counts, providers, clouds, tfvars, backend, layout, environments, diagnostics) without importing; an `environments/<env>/` + `modules/<service>` tree is one project |
| `import_terraform` | `dir?` \| `files?`, `projects?`, `name?` | Import Terraform as new workflows (default: the root only; `projects` from the scan, `"."` = root); an environments tree becomes one workflow with its environments |
| `export_terraform` | `workflow`, `dir`, `environments?` | Write Terraform + assets to a directory in the workflow's layout (single root, or `modules/<service>` + `environments/<env>/` roots; see Structure); `environments` adds each environment's values and `.env.example` |
| `sandbox_status` / `sandbox_start` / `sandbox_stop` / `sandbox_reset` | `workflow` | Manage the local Floci sandbox |
| `validate` / `plan` / `deploy` / `destroy` | `workflow`, `environment?`, `wait_seconds?` | Terraform runs in the sandbox; status, plan summary, last 60 log lines, deployed resources after `deploy` |
| `get_run` | `run` | Status and log tail of a run |
| `get_deployed_state` | `workflow`, `address?` | Deployed resources (key values; all values for matching `address`) and outputs |
| `get_endpoints` | `workflow` | How to use what is deployed: sandbox address/env, and per resource a plain summary, host URLs, identifiers, the first ready-to-run snippet and its `try_endpoint` actions |
| `try_endpoint` | `workflow`, `node`, `action`, `input?`, `method?`, `path?`, `headers?` | Call a deployed resource in the sandbox (HTTP request to a route, invoke, send/peek messages, publish, list/put/get files, scan/put items, …); returns status, latency and body |
| `check_network` | `workflow` | Network review: basis (`deployed` sandbox values or `design`), VPCs, subnets classified public/private/isolated with why, route tables, gateways, security group summaries, placements (subnet, AZ, public or not), `net_*` findings with why and fix, notes on what was skipped (unknown values) |
| `check_reachability` | `workflow`, `from`, `to` (`"internet"` or a node), `protocol?` (tcp/udp/icmp), `port`, `live?` | AWS only today. Can traffic get from A to B? `reachable`, `verdict`, the path hop by hop (allow/deny/unknown + reason), blockers, suggestions; `live` adds real probes for EC2 instances with per-hop `enforcement` |
| `add_test_instance` | `workflow`, `subnet` | Add a labelled EC2 test web server (port 80 page) and its security group into a subnet, to test traffic for real after deploying |
| `list_load_targets` | `workflow` | Suggested load-test targets (indexed) |
| `run_load_test` | `workflow`, `target`, `preset?`, `stages?`, `mode?`, `thresholds?`, `confirm_external?`, `wait?` | Run a load test; returns summary and threshold results. Target objects may use `connectionId` (needs `confirm_external`) |
| `get_load_test` | `run` | Status and results of a load test |
| `list_connections` | — | Saved connections (secrets never returned) |
| `get_connection` | `connection` | One saved connection |
| `create_connection` | `name`, `kind` (`http`/`postgres`/`mysql`/`redis`/`mongodb`/`aws`), `description?`, http: `base_url`, `headers?`, `auth?`, `insecure_skip_verify?`; databases: `uri?` \| `host`, `port?`, `username?`, `password?`, `database?`, `tls?`; aws: `profile?`, `region?` | Save a connection |
| `update_connection` | `connection`, any create field, `clear_secrets?` | Partial update |
| `delete_connection` | `connection` | Delete it and its load test history |
| `test_connection` | `connection?` and/or create fields (draft) | Reachability check (`ok`, latency, message) |
| `list_connection_targets` | `connection` | Suggested targets for a connection (indexed) |
| `run_connection_load_test` | `connection`, `target`, `preset?`, `stages?`, `mode?`, `thresholds?`, `confirm_external` (must be true), `wait?` | Load test a saved connection without a workflow |
