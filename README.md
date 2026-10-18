# Nimbo

A local ADE for infrastructure. Design cloud infrastructure by connecting
nodes on a canvas, get clean Terraform out of it (or import existing
Terraform into a diagram), deploy it into a private local
[Floci](https://floci.io) sandbox and load-test it — speed, stability and
capacity under different traffic volumes — without touching a real cloud
account. AI agents can do the same through the built-in MCP server.

- **Design**: React Flow canvas, every resource of the `aws`, `azurerm`,
  `google` and `oci` Terraform providers (≈5,200 resource types) with a
  schema-driven inspector. *Guided* mode for beginners (required fields,
  curated defaults, one-click connection recipes such as "SQS → Lambda
  trigger"); *Pro* mode for power users (every attribute, raw HCL
  expressions, meta-arguments, code editing, ⌘K command palette).
- **Terraform both ways**: export a workflow as idiomatic, `terraform fmt`
  clean files (plus per-environment `.tfvars`); import a Terraform folder and
  get the diagram back, edges derived from references.
- **Environments**: per-workflow variable values and process env vars
  (secrets masked), applied to runs and exported as tfvars.
- **Test locally**: each workflow gets its own Docker sandbox running the
  Floci emulators it needs (AWS `:4566`, Azure `:4577`, GCP `:4588`,
  OCI `:4599`); Terraform runs inside it. Nodes show what is deployed.
- **Use what you deployed**: the Endpoints tab explains every deployed
  API, function, queue, topic, bucket and table in plain language, with
  copyable URLs and identifiers, ready snippets (curl, AWS CLI, JavaScript,
  Python), a built-in *Try it* (send requests and messages, read them back)
  and a one-click Postman collection export. The sandbox address and test
  credentials stay the same across restarts, so saved URLs keep working.
- **Load test**: smoke / load / stress / spike / soak presets or custom
  stages, open (req/s) or closed (virtual users) model, against HTTP
  endpoints (API Gateway, Lambda URLs, any URL) or AWS operations (SQS,
  S3, DynamoDB, Lambda, SNS, Kinesis, EventBridge, Step Functions). Live
  latency percentiles, error kinds, stability score, breaking point,
  thresholds with pass/fail, history and run comparison.
- **Connections**: a global library of services that already exist
  elsewhere — any HTTP/HTTPS API (headers, bearer/basic/API-key auth),
  PostgreSQL, MySQL, Redis, MongoDB, or a real AWS account via an AWS
  profile. Save once (secrets stay local, never shown again), test the
  connection, and load-test it from the home screen or as a target in any
  workflow. Traffic that leaves the local sandbox always needs explicit
  confirmation.
- **MCP**: 47 tools for agents (search → add → connect → validate → deploy →
  use what you deployed → load test → export, plus the Connections library). See
  [docs/MCP.md](docs/MCP.md).

Architecture, contracts and verified Floci facts:
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Quick start

One command checks your computer, installs what's missing (asking first),
and builds the app:

```sh
# macOS / Linux
git clone https://github.com/ismaelosuna7824/nimbo.git && cd nimbo && ./setup.sh
```

```powershell
# Windows (PowerShell)
git clone https://github.com/ismaelosuna7824/nimbo.git; cd nimbo
powershell -ExecutionPolicy Bypass -File .\setup.ps1
```

It installs, only with your OK and from the official sources:

- **macOS**: Xcode Command Line Tools, Homebrew, Go 1.26+, Bun 1.4+, Rust.
- **Linux** (apt, dnf, pacman, zypper): the Tauri system libraries
  (WebKitGTK, …), Go 1.26+, Bun 1.4+, Rust.
- **Windows** (winget): Visual Studio C++ Build Tools, WebView2, Go 1.26+,
  Bun 1.4+, Rust.

Then it builds the backend (`backend/bin/nimbo`) and the desktop app
(`.app`/`.dmg`, `.deb`/`.rpm`/`.AppImage`, or `.msi`/`-setup.exe`), offers to
install the app and the `nimbo` CLI for AI agents, and runs `nimbo doctor`.

**Docker is not needed to build.** Open the app: its first-run guide checks
Docker and the sandbox images, explains anything missing in plain language and
fixes it with one click where it can.

| Command | What it does |
|---|---|
| `./setup.sh` | Install prerequisites, build and install (default) |
| `./setup.sh dev` | Same checks, then start the app in development mode (hot reload) |
| `./setup.sh build` | Rebuild everything (prerequisites already installed) |
| `./setup.sh doctor` | Check Docker, images, disk and network (`nimbo doctor`) |

Options: `--yes` (no questions) and `--no-install` (only report, change
nothing outside the repo). Windows: the same commands with `.\setup.ps1`,
`-Yes`, `-NoInstall`. Details and troubleshooting: [docs/SETUP.md](docs/SETUP.md).

### What Nimbo uses at runtime

- A Docker engine (Docker Desktop, OrbStack, Colima, Rancher Desktop, Docker
  Engine on Linux, or Podman with the `docker` CLI). Images pulled on first
  use: `floci/floci*`, `hashicorp/terraform`, `alpine:3`.
- No host Terraform needed; runs use the `hashicorp/terraform` image.

### Without the desktop shell

```sh
# backend + web UI in a browser
cd backend && go run ./cmd/nimbo serve --addr 127.0.0.1:7788
cd apps/desktop && bun run dev   # http://localhost:1420
```

Data lives in `~/.nimbo` (override with `NIMBO_HOME` or `--home`).
Sandbox containers and networks are named `nimbo-<workflow>-*`.

## Develop

```sh
cd backend
go test ./...                                  # unit tests
go test -tags docker ./internal/sandbox/...    # real Floci integration tests
go generate ./internal/catalog/...             # regenerate provider schemas

cd apps/desktop
bun run build                                  # tsc + vite
bun run tauri:build                            # desktop bundle
```

## Layout

| Path | What |
|---|---|
| `backend/cmd/nimbo` | `nimbo serve` (HTTP + SSE daemon), `nimbo mcp` (MCP stdio) |
| `backend/internal/hclcodec` | Workflow ⇄ Terraform, edge derivation |
| `backend/internal/catalog` | Embedded provider schemas, Floci support map, recipes, templates, validation |
| `backend/internal/sandbox` | Per-workflow Docker sandbox and Terraform runs |
| `backend/internal/loadtest` | Load engine |
| `backend/internal/{api,store,events,mcp}` | REST API, persistence, SSE, MCP tools |
| `apps/desktop` | Tauri 2 + React + shadcn/ui + React Flow app |
| `apps/landing` | Public website (Astro, Firebase Hosting) |

## Website

The landing page (English at `/`, Spanish at `/es/`) lives in
[`apps/landing`](apps/landing): Astro + Bun, static output for Firebase
Hosting, Google Analytics for Firebase only after cookie consent.

```sh
cd apps/landing && bun install && bun run dev   # http://localhost:4321
bun run deploy                                  # build + firebase deploy (see apps/landing/README.md)
```
