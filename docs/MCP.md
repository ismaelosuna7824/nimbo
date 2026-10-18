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
| Test a deployed service | "Load test our staging API at https://staging.example.com using a connection." | "Haz una prueba de carga a nuestra API de staging en https://staging.example.com usando una conexión." |
| Close finished work | "Archive the old demo workflows." | "Archiva los workflows de demo viejos." |

## Prompts

Step-by-step recipes for the tasks above; most clients show them as slash
commands (e.g. `/mcp__nimbo__extend_workflow` in Claude Code).

| Prompt | Arguments | What it guides |
|---|---|---|
| `create_infrastructure` | `request`, `cloud?` | `list_templates` → `create_workflow` → `search_resources`/`describe_resource` → `add_node` → `connect` → `validate` → `deploy` → smoke test → summary |
| `extend_workflow` | `request`, `workflow?` (default `current`) | Inspect the graph, add and connect services, `validate`, `plan` |
| `import_terraform_folder` | `dir` | `scan_terraform_folder` → `import_terraform` → summary of projects, diagnostics and Floci support |
| `load_test` | `workflow?` (default `current`), `goal?` | Deploy if needed, choose targets, smoke test, then the profile that answers the goal; interpret the results |
| `test_deployed_service` | `connection?` | Connections flow with explicit user confirmation before any traffic |

## Recommended loop

1. `get_open_workflow`, or `list_templates` → `create_workflow` (or `get_workflow` on an existing one).
2. `search_resources` (prefer `floci_supported`) → `describe_resource`.
3. `add_node` with attributes as HCL expressions (strings quoted: `"\"my-bucket\""`).
4. `list_connection_options` → `connect`.
5. Fix diagnostics returned by every edit (`update_node`).
6. `validate` → `deploy`.
7. `get_endpoints` → `try_endpoint` to show the user how to call what was
   deployed (URLs, queue URLs, snippets) and smoke test it.
8. `list_load_targets` → `run_load_test` (start with `smoke`).
9. `export_terraform` to hand the code over.

Existing code: `scan_terraform_folder` → `import_terraform` with `projects`
(one workflow per project; local modules are kept and can be opened in the
app).

Already-deployed services (no workflow): `create_connection` →
`test_connection` → `list_connection_targets` → ask the user →
`run_connection_load_test` with `confirm_external: true`.

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
| `get_open_workflow` | — | The workflow open in the desktop app (or a message when none is) |
| `list_templates` | — | Starter templates |
| `create_workflow` | `name`, `description?`, `template?` | New workflow, optionally from a template |
| `get_workflow` | `workflow` | Compact workflow: nodes, edges, variables, outputs, environments, diagnostics |
| `archive_workflow` / `unarchive_workflow` | `workflow` | Close a workflow (sandbox stopped keeping state, hidden, runs refused) or restore it |
| `search_resources` | `query`, `cloud?`, `kind?`, `supported_only?`, `limit?` | Search the resource catalog |
| `describe_resource` | `type`, `kind?` | Required/optional attributes, nested blocks, exported attributes, Floci support |
| `add_node` | `workflow`, `type`, `name?`, `kind?`, `attributes?`, `x?`, `y?` | Add a node (curated starters add helper nodes) |
| `update_node` | `workflow`, `node`, `set?`, `unset?`, `rename?` | Edit attributes or rename (references are rewritten) |
| `remove_node` | `workflow`, `node` | Remove a node |
| `list_connection_options` | `workflow`, `source`, `target` | Connection recipes between two nodes |
| `connect` | `workflow`, `source`, `target`, `recipe` | Apply a recipe |
| `set_variable` | `workflow`, `name`, `type?`, `default?`, `description?`, `sensitive?` | Create/update an input variable |
| `remove_variable` | `workflow`, `name` | Remove a variable |
| `set_output` | `workflow`, `name`, `value`, `description?`, `sensitive?` | Create/update an output |
| `remove_output` | `workflow`, `name` | Remove an output |
| `set_environment` | `workflow`, `name`, `values?`, `env_vars?`, `secret_keys?`, `activate?` | Create/update an environment |
| `get_terraform` | `workflow` | Rendered Terraform files |
| `apply_terraform_code` | `workflow`, `files` | Edit by code (merged over current files; `""` deletes a file) |
| `scan_terraform_folder` | `dir` | Terraform projects in a folder/repo (counts, providers, clouds, tfvars, backend, diagnostics) without importing |
| `import_terraform` | `dir?` \| `files?`, `projects?`, `name?` | Import Terraform as new workflows (default: the root only; `projects` from the scan, `"."` = root) |
| `export_terraform` | `workflow`, `dir`, `environments?` | Write Terraform + assets to a directory |
| `sandbox_status` / `sandbox_start` / `sandbox_stop` / `sandbox_reset` | `workflow` | Manage the local Floci sandbox |
| `validate` / `plan` / `deploy` / `destroy` | `workflow`, `environment?`, `wait_seconds?` | Terraform runs in the sandbox; status, plan summary, last 60 log lines, deployed resources after `deploy` |
| `get_run` | `run` | Status and log tail of a run |
| `get_deployed_state` | `workflow`, `address?` | Deployed resources (key values; all values for matching `address`) and outputs |
| `get_endpoints` | `workflow` | How to use what is deployed: sandbox address/env, and per resource a plain summary, host URLs, identifiers, the first ready-to-run snippet and its `try_endpoint` actions |
| `try_endpoint` | `workflow`, `node`, `action`, `input?`, `method?`, `path?`, `headers?` | Call a deployed resource in the sandbox (HTTP request to a route, invoke, send/peek messages, publish, list/put/get files, scan/put items, …); returns status, latency and body |
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
