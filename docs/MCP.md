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
| Least privilege | "The Lambda only reads the orders table and writes its own logs: replace its admin policy." | "La Lambda solo lee la tabla de pedidos y escribe sus propios logs: reemplaza su política de administrador." |
| Test a deployed service | "Load test our staging API at https://staging.example.com using a connection." | "Haz una prueba de carga a nuestra API de staging en https://staging.example.com usando una conexión." |
| Containers (Docker) | "Create a docker compose stack with an Express API, PostgreSQL and Redis, with healthchecks and credentials in .env." | "Crea un stack de docker compose con una API en Express, PostgreSQL y Redis, con healthchecks y credenciales en .env." |
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
| `create_docker_stack` | `request` | Template or `create_workflow kind "docker"` → data services from the palette → app services (starters or your Dockerfiles) → `connect_services` (service_healthy) → the Docker practices below → `review_docker` → `deploy` → endpoints + smoke load test + `get_docker_metrics` → `production_readiness` (for production) → summary |
| `create_kubernetes_app` | `request` | Template, `convert_to_kubernetes` or `create_workflow kind "kubernetes"` → palette objects (images, charts, starters) → `connect_kubernetes` → the Kubernetes practices → `review_kubernetes` per environment → `deploy` → endpoints + smoke load test → `production_readiness` → summary |
| `design_well_architected` | `goal`, `platform?` | `well_architected_guidance` → plan → build with secure, reliable and scalable defaults per pillar → `review_well_architected` (fix warnings, explain what stays) → `validate`/`deploy` locally → load test, failure scenarios and `production_readiness` where available → per-pillar summary |
| `design_from_requirements` | `requirement`, `platform?`, `environment?` | Restate the requirement and settle the non-functional requirements (ask only what can't be assumed, state assumptions) → `architecture_patterns`: style + patterns with a one-line why each and what was deliberately left out → `well_architected_guidance` (layout too) → `plan_structure` → build → `recommend_improvements`, `review_well_architected`, `compliance_report` → deploy locally, load test and failure scenarios → architecture decision record (context, decision, alternatives, consequences) |
| `build_from_design` | `design`, `platform?` | The user's design is the spec: inventory components, connections and boundaries (actors that aren't resources become attributes or notes) → map each to a concrete type without swapping services, asking where it's ambiguous → list the minimal plumbing added (IAM, network, log groups) → build → fidelity check against the inventory → improvements only as grouped proposals (`recommend_improvements`, `architecture_patterns`, reviews), applied when the user accepts |

## Recommended loop

1. `get_open_workflow`, or `list_templates` → `create_workflow` (or `get_workflow` on an existing one). New or growing designs: plan the structure first (`plan_structure`, see "Structure").
2. `search_resources` (prefer `floci_supported`) → `describe_resource`.
3. `add_node` with attributes as HCL expressions (strings quoted: `"\"my-bucket\""`).
4. `list_connection_options` → `connect`.
5. Fix diagnostics returned by every edit (`update_node`).
   Group a concern with many resources, or one that repeats, into a local
   module: `extract_module` (`dry_run: true` first) — see "Modules" below.
6. `validate` → `deploy`. A deploy whose plan would delete or replace
   resources already in the sandbox applies nothing: it returns status
   `needs_confirmation` with `destructive` (address, `action`
   delete/replace, `reasons` = attributes forcing the replacement, `cause`).
   Show the list to the user and call `deploy` again with
   `confirm_destroy: true` only after they agree: it applies exactly that
   plan (if the workflow changed since, it returns a new list to confirm).
   A succeeded deploy may return `drift`: resources that still change right
   after it (every deploy would change them again), each with a `cause` and
   a `hint` (JSON policy normalization → `jsonencode()` /
   `aws_iam_policy_document`; `timestamp()`/`uuid()` → a variable or
   `ignore_changes`; tags repeating provider `default_tags`; otherwise
   `lifecycle { ignore_changes }`).
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
- **Edit in place.** Agents are told to change the workflow the user has
  open when asked to build, change or redo a design, and to call
  `create_workflow` only for a new or separate project. When a workflow is
  created while another one is open, `create_workflow` returns a `notice`
  and the desktop app shows *"Your AI agent created …"* with **Open** (from
  Home it opens right away). Requests from the MCP server carry
  `X-Nimbo-Client: mcp`; the `workflow.created` event reports `origin`.
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

## Docker workspaces

A workflow with `kind: "docker"` is a docker-compose project: its compose files
(overrides, `include`, `extends`, profiles, anchors, `.env` interpolation),
Dockerfiles, `.env` and app code are the source of truth. `get_workflow`
returns the derived view (services with image/build, stack hints, base images,
ports, depends_on, networks, volumes, env with secrets redacted; edges;
`dk_*` error diagnostics with `file:line`, why and fix; best-practice advice is
summarized in `review` and listed by `review_docker`).

| Tool | What it does |
|---|---|
| `create_workflow` | `kind: "docker"` (or a `docker-*` template) creates a docker workflow |
| `list_docker_images` | Curated images (id, image:tag, category, icon, ports, env, volumes, healthcheck, connection variables) |
| `get_compose` | Compose files (in `-f` order), Dockerfiles and `.env` (secret values redacted) with content |
| `add_service` | From `catalog_id`, `image` or `build_context` (+ `starter` scaffolds a buildable Dockerfile) |
| `update_service` | Partial patch (`null` deletes; lists replace); rename updates every reference; comments/formatting kept. `resources {cpus?, memory?, pids?}`, `logging {driver?, maxSize?, maxFile?}` (rotation options), `labels {name: value}`. Hardening keys: `readOnly`, `privileged`, `init`, `capDrop`, `capAdd`, `securityOpt`, `tmpfs`, `secrets`; `secretFromEnv {key, secret?, fileEnv?}` moves a variable into `./secrets/<secret>.txt` as a granted secret (and sets `<key>_FILE`) |
| `remove_service` | Removes it and other services' references to it |
| `connect_services` | Without `kind`/`recipe_id`: the options; else depends_on (condition), network, volume (target), env recipe |
| `apply_terraform_code` | Kind-aware: writes any file of a docker workflow (`""` deletes); YAML/Dockerfile syntax errors are refused |
| `review_docker` | Best-practice findings grouped by severity, each with `at` (file:line), why, fix and a docs link, plus the opt-in rules the user can turn on |
| `get_docker_metrics` | `{workflow, service?}`: live usage from `docker stats` (CPU % of limit, memory vs limit, net/block I/O, PIDs, last-60 s CPU), start→healthy time, problems (OOM kills, restarts, exit codes, unhealthy), the latest build per service (time, cache hits/misses, size split into base/own layers, biggest layers, slowest uncached steps, delta vs previous, findings with `dk_*` rules) and the last load test's bottleneck verdict |

`run_load_test` / `get_load_test` against a docker workflow's service also
return `bottleneck` (e.g. "api reached 99% of its 0.25 CPU limit at 877 req/s —
CPU-bound; raise limits or add replicas") and `containers` (peak CPU/memory,
startup and verdict per service). Against a Kubernetes workflow they also
return `environment` (the active environment when the run started) and
`autoscaling`: one line per workload in scope (the target and every workload
with a HorizontalPodAutoscaler) with its verdict `scaled`, `late`, `maxed`,
`no_hpa`, `no_metrics`, `not_needed` or `too_short`, peak Ready pods, HPA
bounds and CPU, e.g. "web [late]: peak 4 ready pods, HPA 2–5 at 70% CPU —
p95 above 4× its baseline for 25 s before 2 new pods of web were Ready at
52 s — lower the HPA's CPU target (70%) or raise minReplicas (2)…". Use preset
`autoscale` (2 min ramp to 200 req/s, 2 min hold): runs under a minute are
too short for an HPA to react.

**Incident scenarios.** `list_scenarios` shows what applies to the workflow;
`run_load_test` with `scenario {id, target, atSec, durationSec, params}`
injects it for real at `atSec` and reverts it at `atSec + durationSec` (or as
soon as the run stops): `kill_replica` (crash one container — SIGKILL of its
main process so the restart policy applies — or delete one pod),
`dependency_down` (docker pause / scale the workload to 0),
`dependency_latency` (tc netem `delayMs`, `jitterMs`, `lossPct` in the
dependency's network namespace), `cache_flush` (Redis/Valkey `FLUSHALL`,
Memcached `flush_all`) and `traffic_spike` (the load × `factor`). The result's
`scenario` has `verdict` (`survived`, `degraded`, `failed`), a one-line
`message` with numbers and a fix ("Degraded: killing one api replica caused 13%
errors, p95 peak 48 ms, back to normal in 4.0 s. Fix: put a load balancer…"),
`checks` (name, passed, detail), and `injectedAtSec` / `revertedAtSec` /
`recoveredAtSec` on the timeline's axis. Leave ~10 s of traffic before
`atSec` (the baseline) and ~15 s after the fault (the recovery).

Every mutating docker tool (`create_workflow`, `add_service`,
`update_service`, `remove_service`, `connect_services`, `apply_terraform_code`)
returns `best_practices`: only the findings its change introduced
(`{severity, code, node, at, message, fix}`), so the agent fixes them right
away; findings that were already there are left to `review_docker`.

Terraform-only tools (`add_node`, `connect`, `get_terraform`, modules, network
checks, `validate`, `plan`) answer an error for docker workflows; `deploy`,
`destroy` and `get_deployed_state` run and read the compose project (step 7).

### Flow

1. **Pick or start the project.** Use `list_templates` (the `docker-*` ones)
   or `create_workflow` with `kind: "docker"`. To bring in an existing repo,
   import it in the app: compose projects are detected next to Terraform roots.
2. **Read what is there.** `get_workflow` gives the services, edges and `dk_*`
   findings. `get_compose` gives the files themselves.
3. **Add services.** Call `list_docker_images` and pick a `catalog_id`, then
   `add_service`. A catalog service comes with its ports, a named data
   volume, a healthcheck, and its credentials in `.env` as `${VAR}`. For your
   own code, call `add_service` with `build_context` (and `starter` when the
   folder has no Dockerfile yet).
4. **Wire them.** Call `connect_services` with `from`/`to` and no kind: it
   lists the options. Then apply one:
   - `depends_on` with `service_healthy` (it needs a healthcheck on the target);
   - the env recipe, which writes connection URLs such as `DATABASE_URL`;
   - a shared network;
   - a volume mount.
5. **Fine-tune.** Use `update_service` for partial patches (a rename updates
   every reference). Use `apply_terraform_code` for anything else: any file,
   including Dockerfiles and app sources. Comments and formatting of the
   compose files are kept either way.
6. **Check.** Fix every `dk_*` error and the `best_practices` each edit
   returned, then call `review_docker` and fix its warnings and info (see
   Docker best practices below).
7. **Run it.** `deploy` runs `docker compose up` (build, wait until healthy)
   in the workflow's own project and returns each service's state, health and
   published host ports; pass `remap_ports: true` when a port is taken (the
   compose files stay unchanged). `get_deployed_state` shows the same status
   later, `get_endpoints` / `try_endpoint` and the load-test tools use the
   published ports, and `destroy` runs `docker compose down`
   (`remove_volumes: true` also deletes the named volumes). `validate` and
   `plan` are Terraform-only: docker workflows are checked on every save.

### Docker best practices

The templates (`docker-*`), the palette and the build starters are worked
examples of these practices; the MCP instructions and the
`create_docker_stack` prompt give agents the same list, and the `dk_*`
advice rules check them on every save (Problems → Best practices in the app,
`review_docker` for agents). Rules are toggled in Settings → Best-practice
checks; opt-in rules are off until turned on there.

- **Images**: pinned tags (`postgres:16-alpine`, never `latest`/no tag) in
  compose and every `FROM` (`dk_latest_tag`).
- **Small, fast builds**: multi-stage builds for compiled code and bundles
  (`dk_single_stage`) on slim/alpine/distroless/JRE runtimes
  (`dk_heavy_base`); dependency manifests copied and installed before
  `COPY .` (`dk_layer_order`); a `.dockerignore` for contexts with
  `node_modules`/`.git`/`.venv`/`target` — the message gives their measured
  size (`dk_dockerignore`); BuildKit cache mounts for package managers
  (`dk_cache_mount`, opt-in); `apt-get --no-install-recommends` with the lists
  removed in the same `RUN`, `pip --no-cache-dir`, `apk --no-cache`
  (`dk_apt_install`, `dk_pip_cache`, `dk_apk_cache`); `COPY` not `ADD`
  (`dk_add`); `WORKDIR` not `cd` (`dk_workdir`).
- **Runtime**: exec-form `CMD`/`ENTRYPOINT` (`dk_shell_form`), `init: true`
  for node/python (`dk_no_init`), healthchecks on servers and on the images
  others depend on (`dk_missing_healthcheck`) every 5s or more with a timeout
  below the interval (`dk_healthcheck_timing`), `depends_on` with
  `service_healthy` on databases/caches/brokers (`dk_depends_healthy`,
  `dk_no_healthcheck`), `restart: unless-stopped` (`dk_no_restart`), memory
  and CPU limits (`dk_no_limits`, opt-in), rotated json-file logs
  (`dk_log_rotation`), named volumes for data (`dk_anon_volume`), dependency
  folders in volumes rather than bind mounts on macOS/Windows
  (`dk_bind_deps`), published ports that match `EXPOSE` / the database's port
  (`dk_expose_mismatch`).
- **Security**: credentials in `.env` as `${VAR}` (`dk_secret_env`), build
  secrets through `RUN --mount=type=secret` instead of `ARG`/`ENV`
  (`dk_secret_build`), a non-root `USER` (`dk_root_user`), databases published
  on `127.0.0.1` or not at all and data tiers on an `internal: true` network
  (`dk_db_published`), no `docker.sock` mounts (`dk_docker_sock`), no
  `privileged`/`network_mode: host`/`pid: host` (`dk_privileged`),
  `read_only` + `cap_drop: [ALL]` + `no-new-privileges` (`dk_hardening`,
  opt-in). CIS Docker rules: no dangerous `cap_add` (`dk_cap_add`), security
  profiles left on (`dk_apparmor_unconfined`, `dk_selinux_disabled`,
  `dk_seccomp_unconfined`), no sensitive host directories (`dk_host_dirs`),
  no `ipc`/`uts`/`userns_mode: host` (`dk_host_ipc`, `dk_host_uts`,
  `dk_host_userns`), no host devices (`dk_devices`, info), no shared mount
  propagation (`dk_mount_propagation`), no `network_mode: bridge`
  (`dk_default_bridge`, info), updates with their install
  (`dk_update_alone`, info); opt-in: `dk_net_raw`, `dk_read_only`,
  `dk_no_new_privileges`, `dk_pids_limit`, `dk_port_interface`,
  `dk_privileged_port`, `dk_add_any`.

Shared settings stay short with an extension field merged into every
service, as the templates do:

```yaml
x-defaults: &defaults
  restart: unless-stopped
  logging:
    driver: json-file
    options: {max-size: "10m", max-file: "3"}
services:
  api:
    <<: *defaults
    init: true
    deploy: {resources: {limits: {cpus: "1", memory: 256M}}}
```

The full rule table (severity, trigger, defaults) is in
[ARCHITECTURE.md](ARCHITECTURE.md#docker-best-practice-rules).

### Examples

| Goal | English | Español |
|---|---|---|
| Create a stack | "Create a docker workflow with a Node API, PostgreSQL and Redis; the API must wait until the database is healthy." | "Crea un workflow de docker con una API en Node, PostgreSQL y Redis; la API debe esperar a que la base de datos esté sana." |
| Start from a template | "Create a docker workflow from the FastAPI + PostgreSQL template and tell me which ports it publishes." | "Crea un workflow de docker desde la plantilla FastAPI + PostgreSQL y dime qué puertos publica." |
| Add a service | "Add RabbitMQ to the open docker workflow and give the worker the connection settings." | "Agrega RabbitMQ al workflow de docker abierto y dale al worker los datos de conexión." |
| Build your own code | "Add a Go service built from ./billing with a starter Dockerfile and publish it on port 8090." | "Agrega un servicio en Go construido desde ./billing con un Dockerfile inicial y publícalo en el puerto 8090." |
| Change a service | "Give the api service 2 replicas, 512 MB of memory and a healthcheck on /health." | "Dale al servicio api 2 réplicas, 512 MB de memoria y un healthcheck en /health." |
| Review | "Review the compose file and fix every warning: unpinned tags, passwords in environment, missing healthchecks." | "Revisa el compose y corrige todas las advertencias: tags sin fijar, contraseñas en environment, healthchecks faltantes." |
| Run and try it | "Deploy the docker workflow (use free ports if one is taken), call the API's /health and smoke load test it." | "Despliega el workflow de docker (usa puertos libres si alguno está ocupado), llama al /health de la API y hazle una prueba de carga smoke." |

## Kubernetes workspaces

A workflow with kind `kubernetes` deploys plain manifests, Kustomize trees
(`base` + `overlays/<env>`) and Helm charts (`values-<env>.yaml`) to the
workflow's own local k3s cluster (Kubernetes 1.36, `nimbo-k8s-<id>`; the
user's `~/.kube/config` is never touched). The files are the source of
truth: `get_workflow` shows the rendered objects of the active environment
(node ids `Deployment/shop/api`), their edges and the `k8s_*` errors; the
`k8s_*` advice is in `review` and listed by `review_kubernetes`.
`deploy`, `destroy` (`remove_volumes`, `delete_cluster`),
`get_deployed_state` (rollouts, pods, problems, port-forwards, Ingress
URLs), `get_endpoints`, `try_endpoint`, `list_load_targets` and
`apply_terraform_code` are kind-aware; `update_workflow environment`
switches the rendered overlay/values file.

| Tool | Inputs | What it does |
|---|---|---|
| `list_kubernetes_catalog` | `query?`, `group?` (starter, image, chart), `category?` | The palette: 13 starters, 159 `image:<id>` workloads from the Docker image catalog (StatefulSet + headless Service + claims for data images, Deployment + Service otherwise; probes, resources, a Secret for credentials) and 14 pinned `chart:<id>` Helm charts |
| `get_kubernetes` | `workflow`, `rendered?`, `environment?` | Sources, builds and the content of manifests, kustomizations, values files and chart templates; `rendered` adds the YAML `deploy` applies |
| `add_kubernetes_object` | `workflow`, `catalog_id?` \| `yaml?`, `name?`, `namespace?`, `source?`, `file?` | Add palette entries (charts become a helm source with the chart vendored) or your own documents; returns the node ids and the findings the change introduced |
| `update_kubernetes_object` | `workflow`, `node`, `set?`, `unset?` | Write fields where they come from (manifest, Kustomize patch, Helm values key, template literal), comments kept |
| `remove_kubernetes_object` | `workflow`, `node` | Remove an object's document |
| `get_kubernetes_fields` | `workflow`, `node` | Every field with its value, file:line, origin and whether it is editable |
| `connect_kubernetes` | `workflow`, `from`, `to?`, `recipe_id?` \| `kind?`, `port?`, `path?`, `host?`, `key?` | List or apply wiring recipes: expose, selects, route, envFrom, volume, env, serviceaccount, scale |
| `review_kubernetes` | `workflow` | `k8s_*` findings of the active environment grouped by severity, each with file:line, field, why, fix and docs; disabled and opt-in rules |
| `get_kubernetes_logs` | `workflow`, `workload?` \| `pod?`, `container?`, `tail?` | Recent log lines of a workload's pods |
| `restart_workload` | `workflow`, `workload?` | Rollout restart (one workload or all) and wait |
| `kubernetes_cluster` | `workflow`, `action` (stop, start, delete) | Manage the workflow's local cluster |
| `convert_to_kubernetes` | `workflow`, `format?` (manifests, kustomize, helm), `name?`, `namespace?`, `dry_run?`, `size_from_load_test?`, `load_test_run_id?`, `autoscaling?`, `max_replicas?`, `target_cpu?` | Convert a docker workflow into a new Kubernetes workflow; returns it with a report (sources, builds, environments, objects, mappings, unmapped features, `loadTest`, `sizing` with each service's requests/limits and their provenance, `autoscaling` HPAs); requests/limits not set in compose are sized from the newest load test (p90/peak usage), and kustomize/helm prod autoscales stateless services with HPAs; `dry_run` previews the files |

Templates (`list_templates`, kind `kubernetes`): `k8s-webapp-postgres`
(Node.js app built from `web/` + PostgreSQL StatefulSet, manifests),
`k8s-kustomize-app` (base + dev/prod overlays), `k8s-helm-app` (podinfo
chart with values-dev/prod), `k8s-microservices` (frontend + backend behind
one Ingress, HPAs, Redis), `k8s-cronjob-worker` (CronJob producer + queue
worker) and the edge patterns `k8s-reverse-proxy` (Traefik Ingress by path),
`k8s-api-gateway` (Kong DB-less: key-auth, rate limit) and
`k8s-load-balancer` (3 replicas behind a LoadBalancer Service). Each renders
without warnings in every environment and deploys healthy on the local
cluster. Prompt `create_kubernetes_app` walks the whole loop.

### Kubernetes best practices

Errors (`kubernetes`, never toggleable): `k8s_yaml`, `k8s_render`,
`k8s_load`, `k8s_dependency`, `k8s_object`, `k8s_schema` (Kubernetes 1.36
OpenAPI: known kinds and fields, types, required fields, quantities,
base64 Secret data; custom resources against a CRD of the workflow),
`k8s_removed_api`, `k8s_duplicate`, `k8s_workload_selector`. Advice
(toggle in Settings → Best-practice checks): reliability —
`k8s_deprecated_api`, `k8s_no_requests`, `k8s_no_readiness_probe`,
`k8s_probe_port`, `k8s_latest_tag`, `k8s_local_image`,
`k8s_single_replica`, `k8s_topology_spread` (2+ replicas spread over nodes
or zones), `k8s_rollout_strategy` (no Recreate or maxUnavailable ≥
replicas; error when maxSurge and maxUnavailable are both 0) — those three
in production-like environments only — `k8s_pdb` (a PodDisruptionBudget
must allow one eviction; one budget per pod), `k8s_hpa_no_requests`
(warnings); `k8s_no_memory_limit`, `k8s_no_liveness_probe`,
`k8s_pvc_storage_class` (info); networking —
`k8s_selector_mismatch`, `k8s_service_port`, `k8s_ingress_backend`,
`k8s_unknown_ref` (warnings), `k8s_namespace_missing` (info),
`k8s_default_namespace`, `k8s_network_policy` (opt-in); security —
`k8s_secret_env`, `k8s_secret_in_configmap`, `k8s_privileged`,
`k8s_host_namespaces`, `k8s_host_path`, `k8s_host_port` (warnings; info for
DaemonSets), `k8s_windows_host_process`, `k8s_rbac_cluster_admin`,
`k8s_rbac_system_masters`, `k8s_rbac_default_sa` (warnings),
`k8s_run_as_root`, `k8s_rbac_wildcard` (info), `k8s_hardening`,
`k8s_net_raw`, `k8s_capabilities`, `k8s_seccomp`, `k8s_secret_env_ref`,
`k8s_automount_token`, `k8s_rbac_secrets`, `k8s_rbac_create_pods`,
`k8s_rbac_escalate`, `k8s_rbac_create_pv`, `k8s_rbac_node_proxy`,
`k8s_rbac_csr_approval`, `k8s_rbac_webhook_config`, `k8s_rbac_sa_token`
(opt-in; the CIS Kubernetes rules); hygiene —
`k8s_recommended_labels` (info: app.kubernetes.io/name and instance),
`k8s_resource_quota`, `k8s_image_digest` (opt-in, production-like
environments). `production_readiness` evaluates the opt-in rules too.

### Examples

| Goal | English | Español |
|---|---|---|
| Start from a template | "Create a Kubernetes workflow from the Kustomize template, switch to prod and review it." | "Crea un workflow de Kubernetes desde la plantilla de Kustomize, cambia a prod y revísalo." |
| Add a database | "Add PostgreSQL from the palette to the shop namespace and give the api its DATABASE_URL." | "Agrega PostgreSQL desde la paleta al namespace shop y dale al api su DATABASE_URL." |
| Run and try it | "Deploy it to the local cluster, open the Ingress URL and smoke load test it." | "Despliégalo en el clúster local, abre la URL del Ingress y hazle una prueba de carga smoke." |

## Environments (docker and Kubernetes)

Docker and Kubernetes workflows keep their environments (dev, staging,
prod, …) as files on top of the base: Kustomize overlays
(`overlays/<env>/kustomization.yaml`) and Helm values files
(`values-<env>.yaml`) for Kubernetes; for docker the official compose
pattern on top of the base (the compose files and `.env`): an override file
next to the first compose file (`compose.<env>.yaml`) and/or an env file
(`.env.<env>` or `<env>.env`, used as named) read after `.env`; either
alone is an environment (runs use `docker compose -f compose.yaml [-f
compose.<env>.yaml] --env-file .env --env-file <env file>`). Sample files
(`.env.example`, `.env.sample`…), local-only files (`.env.local`,
`.env.<env>.local`) and files a service reads with `env_file` are never
environments; invalid names and a second env file for one environment get
a `dk_environment_file` info finding. The active one
(`update_workflow environment`, `""` = base) is what the graph, rules,
deploy, load tests (`environment` on the run) and `production_readiness`
use. Terraform workflows keep `set_environment`.

Docker edits with an active environment: `update_service` writes a key to
the override when the override sets it (lists tagged `!override`, so
items are never duplicated) and to the base files otherwise; `null`
removes a key from the override only, and `resetOverrides` drops keys
from it so the base values apply. With `scope: "environment"` every key
of the patch goes to the override only (what the inspector's "Only in
<env>" switch does): lists are tagged `!override`, `null` resets a key the
base sets (`!reset null`), and the base files stay as they are (not for
`name`, `environment`, `dependsOn`, `networks`, `labels`, `secrets`).
`get_workflow` lists per service the `overrides` the active environment
sets.

| Tool | Inputs | What it does |
|---|---|---|
| `list_environments` | `workflow` | Environments (`name`, `active`, `prodLike`, the `files` each renders) and the sources that take environments (plain manifests don't); docker adds `baseFiles` (compose files and `.env`) |
| `create_environment` | `workflow`, `name`, `from?` | New environment for every Kustomize/Helm source: `from` copies that environment's overlay directory / values file with its name rewritten in namespaces, hosts and labels (`hello-prod` → `hello-staging`; image tags kept); without `from` a new overlay referencing the base (following the overlays' namespace convention) or an empty values file. Docker: a commented empty `compose.<name>.yaml` (`from` copies that environment's override and `.env.<env>`). Names: lowercase DNS labels, not `base` |
| `rename_environment` | `workflow`, `name`, `new_name` | Moves the overlay directory / values file (contents follow), updates explicit `environments` entries; docker moves the override and the env file keeping its style (`dev.env` → `qa.env`); the active environment follows |
| `delete_environment` | `workflow`, `name` | Deletes its overlay directory / values file (never the base, a source's own path or shared values files); the base becomes active when it was |
| `compare_environments` | `workflow` | Base and every environment rendered on their own, per workload: namespace, replicas, autoscaling (`2–5 @ 70% CPU`), images, CPU/memory requests/limits, PodDisruptionBudget, topology spread, Ingress hosts, and the env vars / ConfigMap keys that differ (`differs` per row; `errors` per environment). Docker, per service: image/build, replicas, CPU and memory limits, ports, healthcheck and every environment variable (secret values masked) |
| `get_environment_settings` | `workflow`, `environment`, `target?` | The knobs of one workload (Kubernetes: `replicas`, `hpa.min`, `hpa.max`, `hpa.cpu`, and per container `image:<c>` (tag), `cpu.request:<c>`, `cpu.limit:<c>`, `memory.request:<c>`, `memory.limit:<c>`; docker, per service: `image`, `replicas`, `cpus`, `memory`) with the value there, the base's, `overridden`, `editable`/`reason` and the `file` edits go to |
| `set_environment_values` | `workflow`, `environment`, `target?`, `values` | Writes knobs into that environment's files only (`null` drops its override): Kustomize — where the overlay already sets the field (its `replicas`/`images` entries, patches, resources), else a new `replicas`/`images` entry or a strategic-merge patch `<kind>-<name>-patch.yaml` listed in its `patches`; Helm — the values key the chart renders the field from, in `values-<env>.yaml` (fields the chart doesn't expose as values are not editable; a value equal to the base's removes the override); docker — minimal keys in `compose.<env>.yaml` (replicas above 1 on a fixed host port also give each replica a port: `ports: !override ["3100-3101:3000"]`) |
| `set_environment_variable` | `workflow`, `environment`, `key`, `value?`, `secret?`, `unset?` | Docker: a variable of the environment's env file as named (`.env.<env>` or `<env>.env`; the first variable creates `.env.<env>`; removing the last deletes the file, and an environment that was only that file with it); it wins over `.env` in that environment |

Example: "Duplicate prod as staging, give it 3 replicas and nginx 1.29, compare, switch to staging and deploy" → `create_environment {name: staging, from: prod}`, `set_environment_values {environment: staging, target: Deployment/web, values: {replicas: "3", "image:nginx": "1.29-alpine"}}`, `compare_environments`, `update_workflow {environment: staging}`, `deploy`.

Docker example: "Add dev and prod; prod runs 2 api replicas with 0.5 CPU and LOG_LEVEL=warn; deploy prod" → `create_environment {name: dev}`, `create_environment {name: prod}`, `set_environment_values {environment: prod, target: api, values: {replicas: "2", cpus: "0.5"}}`, `set_environment_variable {environment: prod, key: LOG_LEVEL, value: warn}` (the compose file reads `${LOG_LEVEL:-debug}`), `update_workflow {environment: prod}`, `deploy`. `convert_to_kubernetes` then writes `overlays/dev` and `overlays/prod` (prod with an HPA).

## Production readiness (docker and Kubernetes)

`production_readiness` checks every environment of a docker or Kubernetes
workflow on its own (docker: the base and each environment; Kubernetes:
each environment, or the base as production without any) and returns a
status (ready / needs_work / blocked), a 0-100 **score**, a 0-100
**confidence** and one score per category:

| Category | Weight | What counts |
|---|---|---|
| reliability | 30 | Kubernetes: manifests apply, replicas + PDBs, probes, requests/limits, rollout, topology spread, wiring. Docker: project loads, configuration, healthchecks, restart and shutdown, resource limits, persistent data |
| performance | 20 | Kubernetes HPAs; the latest load test of that environment (autoscaling verdict, or container verdicts on docker); the latest capacity plan (`plan_capacity`) |
| resilience | 15 | The latest run of each applicable incident scenario (`list_scenarios`, `run_load_test scenario`): survived / degraded / failed |
| monitoring | 10 | `add_monitoring` stack (or the user's own Prometheus / ServiceMonitors) |
| security | 15 | Secrets, privileges, NetworkPolicies / published database ports |
| hygiene | 10 | Labels, image pinning, namespaces/quotas, Dockerfile practices, log rotation |

Scoring is deterministic: pass and info items earn 1, warnings ½,
failures 0. Items with no evidence yet (no load test, scenario run or
capacity plan of that environment) are **todo**: left out of the score,
they lower the confidence instead, and keep production-like environments
at needs_work. A rule error blocks, whatever the score; a failed scenario
scores 0 in resilience and needs work (it does not block). Each
environment is offered the scenarios of its own services or workloads.
`components` has one row per compose service
or workload with its open cells (rules, health, resources, monitoring,
load headroom, resilience, capacity) and its own score. Every open item
names the tool that resolves or verifies it.

| Tool | Inputs | What it does |
|---|---|---|
| `production_readiness` | `workflow`, `environment?` | Per environment: status, score, confidence, category scores, open items (findings with file:line and fix, the resolving tool, run ids), passed and skipped items, component rows and the load test the evidence comes from. Call it before declaring a workflow production-ready |

Example: "Is the docker stack ready for production?" → `production_readiness`
→ `add_monitoring` + `deploy`, `run_load_test {preset: load}`,
`run_load_test {scenario: {id: kill_replica, target: service:api, atSec: 15}}`,
`plan_capacity {target_rps: 300}` → `production_readiness` again.

## Compliance (pre-audit of the design)

`compliance_report` grades a workflow's **design** against the security
benchmarks of its platforms: CIS AWS, Azure, Google Cloud and OCI
Foundations for the clouds a Terraform workflow uses, CIS Docker for docker
workflows, CIS Kubernetes for Kubernetes ones. It is a pre-audit, never a
certification. Automatic controls are decided by the best-practice rules
(opt-in ones included) on the resources each control is about:

| Status | Meaning |
|---|---|
| `met` | No findings of its rules |
| `violated` | A finding on any resource it applies to (reason: "N of M resources have findings"; each finding with address, message, fix) |
| `not_applicable` | Nothing in the design is what it is about |
| `not_evaluated` | One of its rules is turned off in Settings |
| `manual` / `attested` | A design can't show it (account settings, MFA, processes); only the user marks it done, with a note, in the app's Compliance tab |

| Tool | Inputs | What it does |
|---|---|---|
| `compliance_report` | `workflow`, `framework?`, `markdown?` | Read-only. Per framework: "N / M automatic controls met", counts per status, open controls (violated, manual not attested, not evaluated) with rules, findings, reason and guidance; met, attested and not applicable ids. `markdown: true` returns the exportable report of one framework (header, disclaimer, summary, a table per section, attestations) |

Example: "Does this design meet CIS AWS?" → `compliance_report` → fix the
violated controls' findings with the edit tools → `compliance_report` again;
tell the user which manual controls they need to check and attest.

## Well-Architected (pillar guidance and review)

Agents design along the pillars AWS Well-Architected, Azure Well-Architected,
the Google Cloud Architecture Framework and OCI Well-Architected share:
security, reliability, performance (including scaling), cost and operations.
Settle the requirements and call `architecture_patterns` first (style and
patterns, simplest that fits), then `well_architected_guidance` at the start
of the design (its `layout` gives the folder and environment layout),
`recommend_improvements` after the first draft and `review_well_architected`
+ `compliance_report` before finishing; prompts `design_well_architected`
and `design_from_requirements` walk the loop. See
[WELL_ARCHITECTED.md](WELL_ARCHITECTED.md).

Loop: guidance → draft → `recommend_improvements` (propose each item with
its why; apply what the user accepts) → again until nothing applies →
`review_well_architected` (fix warnings) → deploy locally.

| Tool | Inputs | What it does |
|---|---|---|
| `architecture_patterns` | `requirement?`, `platform?` (aliases accepted), `pattern?` | Architecture styles and cloud design patterns (`GET /patterns`, `GET /patterns/{id}`). `requirement` (English or Spanish): ranked matches, styles first, each with id, name, kind, problem, `useWhen`, `avoidWhen` and the block for `platform` (Terraform types / compose services / Kubernetes kinds, local support); `pattern`: one pattern in full (trade-offs, practices, blocks, related, templates, sources); neither: the catalog. Pick the simplest that fits and tell the user the trade-offs and what was left out |
| `well_architected_guidance` | `platform` (`aws`, `azure`, `gcp`, `oci`, `docker`, `kubernetes`; `azurerm`, `google`, `k8s`… accepted), `pillar?` | The platform's framework, sources, `layout` (summary, tree, what each environment adds, sources) and, per pillar, a summary and practices (title, why, how with resource types/attributes or compose/Kubernetes fields, the Nimbo rule codes that check it) |
| `recommend_improvements` | `workflow` | Companions the design lacks, highest value first (`GET /workflows/{id}/recommendations`): id, title, why, pillars, triggering nodes, what to add, a snippet with the design's addresses, scope (`recommended` / `production`), local support (runs locally / export only / local equivalent), when to skip it, checking rules, docs. Applied items disappear; an empty list means nothing is left |
| `review_well_architected` | `workflow` | Runs every rule (opt-in ones and rules turned off in Settings included; Terraform also the `net_*` network checks) and groups findings by their rule's main pillar: status `ok` / `advice` (info only) / `risk` (warnings or errors), counts, findings (severity, code, rule title, node, file:line, why, fix, docs), the frameworks that apply, open recommendations (id, title, why, scope, local), a summary and next steps |

Production-only practices (multi-AZ, deletion protection, long backup
retention) are info in a local sandbox: agents mention them and apply them
when the user wants production parity.

## Access (permissions)

Read and edit permissions structurally instead of writing policy JSON, role
assignments or RBAC YAML. The tools use the same editors as the desktop's
inspector "Access" section and the same grant as dragging an identity onto
a resource on the canvas ("What can it do? Read / Write / Manage"), so the
result is least privilege in each provider's own model:

| Provider | `get_access` / `set_access` edit | `grant_access` writes |
|---|---|---|
| AWS | Policy documents (statements: `sid`, `effect`, `actions`, `resources` as HCL expressions, `principals`) of IAM policies, role trust/inline policies, `aws_iam_policy_document` and S3/SQS/SNS resource policies | A statement with the level's least-privilege actions on that resource's ARN, in the identity's inline policy (role, Lambda function, ECS task definition, user, group) |
| Azure | `azurerm_role_assignment` (scope, role, principal) | A role assignment with the level's built-in role scoped to the resource (and a system-assigned identity when missing) |
| GCP | `google_*_iam_member` / `_binding` (resource, role, members) | A `google_<resource>_iam_member` with the level's predefined role for the service account |
| OCI | `oci_identity_policy` statements (subject, verb, resource type, location, condition) | A statement in the compartment's policy (read / use / manage) |
| Kubernetes | Role/ClusterRole `rules`, binding `role_ref`/`subjects`, workload `service_account`, `automount_token` | ServiceAccount + Role (scoped with `resourceNames`) + RoleBinding + `serviceAccountName` |

Parts an editor can't represent (conditions it doesn't model, dynamic
expressions, Helm templates, …) come back with `editable: false` and their
source; they are kept verbatim and edits to them are refused. Every view
carries least-privilege `warnings` (wildcards, admin/basic/privileged
roles, tenancy/subscription/cluster-wide grants, public principals).
Docker has no IAM: harden services with `update_service` (`readOnly`,
`capDrop`, `securityOpt`, `tmpfs`, `user`, `secretFromEnv`, …).

| Tool | Inputs | What it does |
|---|---|---|
| `get_access` | `workflow`, `node` | The node's permissions as structure, with warnings (Terraform: `AccessView`; Kubernetes: `KubeAccessView`) |
| `set_access` | `workflow`, `node`, `aws?` \| `azure?` \| `gcp?` \| `oci?` (Terraform), `rules?`, `role_ref?`, `subjects?`, `service_account?`, `automount_token?` (Kubernetes) | Write what `get_access` returned after changing it: AWS documents are matched by `path`; send every statement (omitted ones are deleted; unchanged ones keep their `source`) |
| `grant_access` | `workflow`, `identity`, `resource`, `level?` | Without `level`: the Read/Write/Manage options with exactly what each writes. With `level`: applies it (Terraform `access:<level>` connect recipe; Kubernetes `access:<level>` kube recipe) |
| `list_access_catalog` | `provider`, `service?`, `resource_type?`, `query?` | Pinned official reference data with source and date: AWS services and actions (access level, resource types), Azure built-in roles, GCP predefined roles (suggested ones for a Terraform type first), OCI verbs and resource types, Kubernetes resources and verbs |

Example: "The Lambda function only reads the orders table" →
`grant_access {identity: aws_iam_role.reader, resource:
aws_dynamodb_table.orders, level: read}` → `get_access {node:
aws_iam_role_policy.reader}` → drop the `"*"` statement with `set_access`.

## Learn

Nimbo Learn has roadmaps (beginner → expert) and hands-on challenges for
every provider (`aws`, `azure`, `gcp`, `oci`, `docker`, `kubernetes`, and
`global` for provider-agnostic problems with per-provider adaptations).
They are written from official documentation, and Nimbo grades them for
real. Agents can read them to coach a learner. Hints come first; the
solution is only returned on request.

| Tool | Inputs | What it does |
|---|---|---|
| `list_learning_paths` | `provider?` | Without `provider`: every provider with challenge counts per level, the user's completions and content load errors. With `provider`: its roadmap. Each level has goals and steps, and each step has its linked challenges (id, title, level, the user's status) and official docs |
| `list_challenges` | `provider?`, `level?`, `tag?`, `query?` | Challenges with summary, minutes, runtime (`local`, `emulated`, `design`), tags, the user's status (`not_started`, `in_progress`, `completed`) and `solvableWithoutCode` (false: some step needs code or file edits; the hints of the others are UI steps) |
| `get_challenge` | `id`, `include_solution?` | Problem, requirements, hints, graded checks, starter, adaptations (global), official sources, prerequisites (with completion) and progress (attempts, passed check ids, workflow id). The solution and its files are only included with `include_solution: true` |
| `start_challenge` | `challenge`, `name?` | Creates a workflow from the challenge's starter (broken on purpose), linked to it, and marks the challenge in progress |
| `check_challenge` | `workflow`, `wait?` | Grades the challenge workflow for real: validate / rules_clean / has on the files (every rule on), one deploy before the first runtime check, http calls, load tests, incident scenarios, readiness. Waits (up to 25 min) by default; returns per check status, measured vs expected values, detail and load test run id, plus passed and score. Progress is recorded in Learn |

Example: "I want to learn Kubernetes" → `list_learning_paths {provider:
kubernetes}` → pick the first step that isn't completed → `get_challenge
{id}` → explain the problem and requirements, then give hints one at a
time. The learner starts it from the Learn page (or `start_challenge`); its
workflow shows the Challenge tab, and `check_challenge` grades it.

## Tools

All `workflow` arguments are optional and default to `current` (see
Conventions).

| Tool | Arguments | Purpose |
|---|---|---|
| `list_workflows` | `include_archived?` | Workflows, most recent first; the open one is flagged |
| `get_open_workflow` | — | The workflow open in the desktop app, like `get_workflow` (or a message when none is) |
| `list_templates` | — | Starter templates (`id`, `name`, `description`, `kind`, `cloud`, `difficulty`, `tags`); edge patterns add `category: "edge-patterns"` and `pattern` (`reverse-proxy`, `api-gateway`, `load-balancer`), one of each per kind and cloud |
| `create_workflow` | `name`, `description?`, `template?`, `kind?` | New workflow, optionally from a template; `kind: "docker"` (or a `docker-*` template) makes a docker-compose workflow |
| `get_workflow` | `workflow` | Compact workflow: nodes, edges, variables, outputs, environments, diagnostics, and `structure` (layout, states, resources per state, modules, nesting, backend) |
| `update_workflow` | `workflow`, `name?`, `description?`, `layout?` (`auto`/`single`/`environments`), `environment?` | Rename a workflow, change its description or its export layout (only the given fields change; `description: ""` clears it); the Terraform is unchanged; returns the workflow with its `structure`. `environment` (docker and Kubernetes) switches the active environment (`""` = base; see Environments) |
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
| `review_terraform` | `workflow` | `structure` summary plus best-practice findings grouped by severity, with why and fix (see Code quality and Structure); on a docker workflow it returns the `review_docker` result, on a Kubernetes one `review_kubernetes` |
| `review_docker` | `workflow` | Docker best-practice findings grouped by severity, each with file:line, why, fix and docs; disabled and opt-in rules (see Docker best practices) |
| `review_kubernetes` | `workflow` | Kubernetes best-practice findings of the active environment grouped by severity, each with file:line, field, why, fix and docs; disabled and opt-in rules (see Kubernetes best practices) |
| `apply_terraform_code` | `workflow`, `files` | Edit by code (merged over current files; `""` deletes a file, and its blocks are gone unless the same call writes them to another file). Every block stays in the file it is written in; applying the same files again changes nothing |
| `scan_terraform_folder` | `dir` | Terraform projects in a folder/repo (counts, providers, clouds, tfvars, backend, layout, environments, diagnostics) without importing; an `environments/<env>/` + `modules/<service>` tree is one project |
| `import_terraform` | `dir?` \| `files?`, `projects?`, `name?` | Import Terraform as new workflows (default: the root only; `projects` from the scan, `"."` = root); an environments tree becomes one workflow with its environments |
| `export_terraform` | `workflow`, `dir`, `environments?`, `ci?`, `smoke?` | Write Terraform + assets to a directory in the workflow's layout (single root, or `modules/<service>` + `environments/<env>/` roots; see Structure); `environments` adds each environment's values and `.env.example`. Docker and Kubernetes workflows write their files as they are. `ci`: `none` (default), `github` (`.github/workflows/<name>.yml`) or `gitlab` (`.gitlab-ci.yml`), a ready-to-use pipeline (checks, a plan/render per environment on pull requests, gated per-environment deploys from the default branch) whose header and a README section list the secrets and variables to set; `smoke` adds an HTTP check of each environment's `SMOKE_URL` after every deploy (see ARCHITECTURE.md, "CI/CD pipeline export") |
| `sandbox_status` / `sandbox_start` / `sandbox_stop` / `sandbox_reset` | `workflow` | Manage the local Floci sandbox |
| `validate` / `plan` / `deploy` / `destroy` | `workflow`, `environment?`, `wait_seconds?`, `confirm_destroy?` (deploy) | Terraform runs in the sandbox; status, plan summary, last 60 log lines, deployed resources after `deploy`. `deploy` plans and applies that exact plan; a plan deleting/replacing deployed resources returns `needs_confirmation` + `destructive` instead, applied by `deploy` with `confirm_destroy: true` once the user agreed; `drift` lists resources that still change after a deploy, with fix hints |
| `get_run` | `run` | Status and log tail of a run |
| `get_deployed_state` | `workflow`, `address?` | Deployed resources (key values; all values for matching `address`) and outputs |
| `get_endpoints` | `workflow` | How to use what is deployed: sandbox address/env, and per resource a plain summary, host URLs, identifiers, the first ready-to-run snippet and its `try_endpoint` actions |
| `try_endpoint` | `workflow`, `node`, `action`, `input?`, `method?`, `path?`, `headers?` | Call a deployed resource in the sandbox (HTTP request to a route, invoke, send/peek messages, publish, list/put/get files, scan/put items, …); returns status, latency and body |
| `check_network` | `workflow` | Network review: basis (`deployed` sandbox values or `design`), VPCs, subnets classified public/private/isolated with why, route tables, gateways, security group summaries, placements (subnet, AZ, public or not), `net_*` findings with why and fix, notes on what was skipped (unknown values) |
| `check_reachability` | `workflow`, `from`, `to` (`"internet"` or a node), `protocol?` (tcp/udp/icmp), `port`, `live?` | AWS only today. Can traffic get from A to B? `reachable`, `verdict`, the path hop by hop (allow/deny/unknown + reason), blockers, suggestions; `live` adds real probes for EC2 instances with per-hop `enforcement` |
| `add_test_instance` | `workflow`, `subnet` | Add a labelled EC2 test web server (port 80 page) and its security group into a subnet, to test traffic for real after deploying |
| `list_load_targets` | `workflow` | Suggested load-test targets (indexed) |
| `run_load_test` | `workflow`, `target`, `preset?`, `stages?`, `mode?`, `thresholds?`, `confirm_external?`, `wait?`, `scenario?` | Run a load test; returns summary and threshold results. Target objects may use `connectionId` (needs `confirm_external`). Docker/Kubernetes: `scenario {id, target, atSec, durationSec, params?}` injects a real, reversible incident and returns its verdict |
| `get_load_test` | `run` | Status and results of a load test (with the scenario verdict when one was injected) |
| `list_scenarios` | `workflow` | Docker/Kubernetes: the incident scenarios that apply in the active environment, with targets (graph node ids), params, default duration and plain explanation; `unavailable` says why the others don't apply |
| `plan_capacity` | `workflow`, `target_rps`, `p95_ms?`, `environment?`, `run_id?` | Docker/Kubernetes: size an environment for a target request rate from a finished load test (default: the environment's newest run with container usage). Per service: role (app scales out, dependency projected, idle), replicas with N+1 headroom at a 70% CPU ceiling (or the HPA target), requests/limits, HPA min/max (Kubernetes), projected CPU, caveats ("not measured beyond X req/s" for databases/caches); totals; cheapest node shape, count and monthly on-demand compute cost per cloud (AWS, Azure, GCP, OCI) from a pinned, dated price table; confidence (high/medium/low) with reasons; assumptions. The plan is kept as the environment's latest; the app applies it to the environment after a confirmation |
| `list_learning_paths` | `provider?` | Learn providers with progress, or one provider's roadmap (levels, steps, linked challenges with status, official docs) |
| `list_challenges` | `provider?`, `level?`, `tag?`, `query?` | Learn challenges with the user's status |
| `get_challenge` | `id`, `include_solution?` | One challenge: problem, requirements, hints, checks, starter, adaptations, sources, prerequisites, progress (solution only on request) |
| `start_challenge` | `challenge`, `name?` | New workflow from a challenge's starter, linked to it |
| `check_challenge` | `workflow`, `wait?` | Grade a challenge workflow for real (static checks, deploy, http, load, scenarios, readiness); per-check measurements, passed, score |
| `compliance_report` | `workflow`, `framework?`, `markdown?` | Read-only pre-audit of the design against its platforms' CIS benchmarks: summary, counts, open controls with findings and guidance; `markdown` returns the exportable report |
| `architecture_patterns` | `requirement?`, `platform?`, `pattern?` | Architecture styles and design patterns ranked for a requirement, or one in full, mapped to the platform's services; call at the start of a design, prefer the simplest |
| `well_architected_guidance` | `platform`, `pillar?` | Well-Architected practices of a platform per pillar (why, how, rule codes); call before designing |
| `review_well_architected` | `workflow` | Every rule's findings grouped by pillar with status ok/advice/risk plus open recommendations; call before finishing, fix warnings first |
| `recommend_improvements` | `workflow` | Companions the design lacks (WAF/rate limits on public entry points, reverse proxy, DLQs…) with why, snippet, scope and local support; call after the first draft and before finishing |
| `get_access` | `workflow`, `node` | Structured permissions of a node (AWS policy statements, Azure role assignment, GCP IAM member/binding, OCI policy statements, Kubernetes RBAC) with least-privilege warnings |
| `set_access` | `workflow`, `node`, provider structure | Write a node's permissions back from that structure; read-only parts are kept verbatim |
| `grant_access` | `workflow`, `identity`, `resource`, `level?` | Give an identity Read/Write/Manage on a resource in the provider's model (lists the options without `level`) |
| `list_access_catalog` | `provider`, `service?`, `resource_type?`, `query?` | Pinned official actions, roles, verbs and resource types |
| `list_connections` | — | Saved connections (secrets never returned) |
| `get_connection` | `connection` | One saved connection |
| `create_connection` | `name`, `kind` (`http`/`postgres`/`mysql`/`redis`/`mongodb`/`aws`), `description?`, http: `base_url`, `headers?`, `auth?`, `insecure_skip_verify?`; databases: `uri?` \| `host`, `port?`, `username?`, `password?`, `database?`, `tls?`; aws: `profile?`, `region?` | Save a connection |
| `update_connection` | `connection`, any create field, `clear_secrets?` | Partial update |
| `delete_connection` | `connection` | Delete it and its load test history |
| `test_connection` | `connection?` and/or create fields (draft) | Reachability check (`ok`, latency, message) |
| `list_connection_targets` | `connection` | Suggested targets for a connection (indexed) |
| `run_connection_load_test` | `connection`, `target`, `preset?`, `stages?`, `mode?`, `thresholds?`, `confirm_external` (must be true), `wait?` | Load test a saved connection without a workflow |
