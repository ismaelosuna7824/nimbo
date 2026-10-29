# Code guidelines

`backend/internal/codeguide` is Nimbo's guidance for the application code a
workflow deploys. MCP tool `code_guidelines` serves it (`backend/internal/mcp/codeguide.go`).

## Inputs

| Field | Values | Default |
|---|---|---|
| `mode` | `architecture`, `full` | the workflow's mode, else `architecture` |
| `language` | `typescript`, `javascript`, `python`, `go`, `java`, `csharp` | full mode: none, so the guide returns `languages` to propose |
| `style` | `service`, `modular-monolith`, `microservices` | `service` |
| `platform` | `aws`, `azure`, `gcp`, `oci`, `docker`, `kubernetes` | the workflow's platform, else `aws` |
| `target` | `function`, `container`, `vm` | `function` on clouds, `container` on docker/kubernetes |

## Modes

- **architecture**: no business code. The files are one smoke handler per
  service, in JavaScript (or Python). It answers `GET /health` and `GET /smoke`.
  `/smoke` writes a unique value and reads it back on every store whose env
  var is set: DynamoDB, S3, SQS, SNS, Azure Blob, Service Bus, GCS, Pub/Sub,
  Postgres and Redis. The handler is done when both routes return 200.
- **full**: a hexagonal skeleton in the chosen language. It has a domain (an
  entity, a value object and typed errors), use cases, a repository port, an
  in-memory adapter and a real adapter (DynamoDB on AWS, Postgres elsewhere).
  It also has the HTTP driving adapter, the composition root, unit tests and
  a Dockerfile. The principles scale with the style: hexagonal-lite for a
  service, one module per bounded context for a modular monolith, and outbox,
  idempotent consumers and contract tests for microservices.

## Layout and packaging

Code lives in `services/<svc>/` workflow files. Specs live in `openspec/`.
Code is never inline in Terraform.

| Kind | When | How |
|---|---|---|
| `zip` | JavaScript or Python functions | `archive_file` `source_dir = "${path.module}/services/<svc>/src"` |
| `build-zip` | TypeScript, Go, Java and C# functions | The Dockerfile's last stage is `FROM scratch AS package`. Nimbo builds it into `services/<svc>/dist` before Terraform runs, and `source_dir` points there. |
| `image` | managed containers, OCI Functions | The Dockerfile's `runtime` stage. The variable `<svc>_image` defaults to `nimbo.local/<svc>:latest`. Before Terraform runs, Nimbo builds the image and sets `TF_VAR_<svc>_image` to that build's own tag `nimbo.local/<svc>:<hash>` (workflow + sources), so a source change rolls the service on the next deploy. For the real cloud, set it to a registry reference. |
| `compose-build` / `k8s-image` | docker / kubernetes workflows | Nimbo builds from the service's Dockerfile. |
| `vm-image` | VMs | `deploy/cloud-init.yaml.tftpl` runs the image under systemd behind an instance group. |

Functions on Azure, GCP and OCI have skeletons for JavaScript, TypeScript and
Python. For other languages, use target `container`.

## Compute and local support

`compute` lists the platform's options (functions, managed containers,
Kubernetes, VMs) with `when` and `avoidWhen`. `local` is one of:

- `runs locally`: workloads were verified running on Floci.
- `deploys locally, workloads do not run`: the resources deploy, but only as
  a control plane.
- `deploys locally, workloads not verified`
- `export only`

A test checks `export only` against the catalog's Floci support. Export-only
today: Azure Functions, Cloud Functions, OCI Container Instances, and the VM
groups on Azure, GCP and OCI.

Verified running on Floci:

- AWS Lambda runtimes `nodejs22.x`, `python3.12`, `provided.al2023` (Go),
  `java21` and `dotnet8`, behind API Gateway with DynamoDB, S3, SQS and SNS.
- Cloud Run and ECS Fargate running the `nimbo.local/<svc>` build.

Azure Container Apps deploys but runs no container.

## Sandbox access

`sandbox` tells how code running in the local sandbox reaches the emulated
services (verified 2026-10-28 on floci 2.3.0 and floci-gcp 0.11.0):

- AWS Lambda and ECS tasks get `AWS_ENDPOINT_URL=http://localhost.floci.io:4566`,
  region and test credentials from Floci: SDK defaults work, no endpoint in
  code or Terraform.
- Cloud Run containers get only `PORT` and `K_*`; `localhost:4588` and
  `localhost.floci.io:4588` there are the container itself. GCP sandbox runs
  set `TF_VAR_gcp_emulator_host=nimbo-<wf>-pod:4588`; the Cloud Run snippet
  declares `gcp_emulator_host` (default `""`) and passes
  `PUBSUB_EMULATOR_HOST`/`STORAGE_EMULATOR_HOST` only while it is set.
- Cloud SQL is a real PostgreSQL/MySQL container at the instance's
  `first_ip_address`. The sandbox's Cloud Run rejects env `value_source`, so
  the full-mode snippet passes `DATABASE_URL` as a plain value only in the
  sandbox.

## Spec

`spec` gives the OpenSpec layout, the clarification questions, and templates
for proposal, delta spec, design and tasks. Its gate allows no design or code
while `[NEEDS CLARIFICATION]` remains.

## Maintenance

Starter files are real source trees under `backend/internal/codeguide/_files/`.
They are stored as layers (`common`, store, entry per platform/target), and
later layers override earlier ones. `go test ./internal/codeguide/` checks:

- every combination
- the C2 paths
- that every `build-zip` Dockerfile ends with `FROM scratch AS package` and
  that Nimbo detects its build
- that the JavaScript and Python files compile
- the Terraform snippets against the provider schemas
- the `local` flags against the catalog
