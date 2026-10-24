# Contributing to Nimbo Learn

Nimbo Learn has a **roadmap** (beginner → intermediate → advanced → expert)
and hands-on **challenges** for every provider: `aws`, `azure`, `gcp`,
`oci`, `docker`, `kubernetes`, plus `global` (problems that apply to every
provider, with notes on how to solve them on each one). Nimbo grades every
challenge for real: it validates the workflow, runs the best-practice
rules, inspects the structure, and can deploy it, call it, load-test it or
inject an incident.

All content lives in **content migrations**: YAML files in
[`backend/internal/learn/migrations/`](../backend/internal/learn/migrations).
They are built into the daemon. When the daemon starts, it applies the
migrations that are missing from `<nimbo home>/learn.db`. The user's
progress lives in the same database, and migrations never change it.

- [Quick start](#quick-start)
- [Migration files](#migration-files)
- [Add, update and remove content](#add-update-and-remove-content)
- [Challenge reference](#challenge-reference)
- [Roadmap reference](#roadmap-reference)
- [Check types](#check-types)
- [Sourcing rule](#sourcing-rule)
- [Verify locally](#verify-locally)

## Quick start

1. Add a file named `YYYYMMDDHHMMSS_<provider>_<slug>.yaml` (UTC time now)
   under `backend/internal/learn/migrations/`.
2. Write `description` and `operations` (see the example below).
3. Run the validation test:

   ```sh
   cd backend && go test ./internal/learn/ -run TestMigrations
   ```

4. Grade it for real in Nimbo. The starter must fail at least one check,
   and the reference solution must pass every check (see
   [Verify locally](#verify-locally)).
5. Open a pull request. Do not edit migrations that are already merged.

## Migration files

- **Name**: `YYYYMMDDHHMMSS_<provider>_<slug>.yaml`, for example
  `20261101093000_aws_static-site.yaml`. The timestamp is the UTC time
  when you write the file. Files are applied in name order. Timestamps keep
  two pull requests from colliding.
- **Provider**: every operation in a file is for the provider in its name.
  Challenge ids start with `<provider>-`. The provider's single roadmap
  has the id `<provider>-roadmap`.
- **Atomic**: a file applies inside one transaction. If any operation
  fails, nothing from that file is applied, and later files wait until it
  is fixed.
- **Immutable once merged**: the database records each applied file with
  its SHA-256 checksum. Editing an applied file is reported as a load error
  (in tests and on the Learn page). To change or remove content, add a new
  migration.
- **YAML**: YAML 1.2, decoded strictly. Keys must match the field names
  below exactly. Unknown keys and duplicate keys are errors. Only
  `true`/`false` are booleans. Quote strings that contain `: ` or start
  with `{ [ * & ! | > ' " % @`. Use `|` block scalars for markdown and file
  contents.

```yaml
description: Adds the AWS static website challenge
operations:
  - op: upsert            # upsert | delete
    kind: challenge       # challenge | roadmap
    id: aws-static-site-s3
    data:
      provider: aws
      level: beginner
      title: Host a static website on Amazon S3
      summary: Serve a website from an S3 bucket with public access blocked by default.
      minutes: 20
      tags: [s3, storage, static-site]
      prerequisites: []
      runtime: emulated
      problem: |
        Your team needs a landing page…
      requirements:
        - An S3 bucket holds the site.
        - "Block Public Access stays on: serve it through the website endpoint only if your policy allows it."
      hints:
        - Look at the `aws_s3_bucket_website_configuration` resource.
      solution: |
        1. …
      solutionFiles:
        main.tf: |
          resource "aws_s3_bucket" "site" { … }
      starter: { templateId: aws-static-site }
      checks:
        - { id: valid, label: The Terraform is valid, type: validate }
        - id: website
          label: The bucket has a website configuration
          type: has
          has: { terraform: { type: aws_s3_bucket_website_configuration } }
      sources:
        - title: Hosting a static website using Amazon S3
          url: https://docs.aws.amazon.com/AmazonS3/latest/userguide/WebsiteHosting.html
```

## Add, update and remove content

| To… | Add a migration with… |
|---|---|
| Add a challenge or the roadmap | `op: upsert` and the full `data` |
| Update one | `op: upsert` for the same `id` with the full new `data` (it replaces the old data; no partial patches) |
| Remove one | `op: delete`, `kind`, `id` (no `data`). Deleting something that doesn't exist is an error |
| Rename an id | Delete the old id and upsert the new one. The user's progress stays with the old id, so avoid renames |

References are checked on the content left after **all** migrations are
applied. A roadmap step may link to a challenge from the same file or an
earlier file. A `global` roadmap may link to challenges of any provider.

## Challenge reference

| Field | Type | Rules |
|---|---|---|
| `provider` | `aws`\|`azure`\|`gcp`\|`oci`\|`docker`\|`kubernetes`\|`global` | Must match the id prefix and the file name |
| `level` | `beginner`\|`intermediate`\|`advanced`\|`expert` | |
| `title` | string | Required |
| `summary` | string | One line |
| `minutes` | int | Estimate, > 0 |
| `tags` | string[] | Lower-case `a-z0-9` words joined by `-`, `_` or `.` |
| `prerequisites` | challenge ids | Must exist; no cycles; not itself |
| `runtime` | `local`\|`emulated`\|`design` | `local` = Docker or the workflow's k3s cluster; `emulated` = Floci (AWS); `design` = validate/rules/structure/readiness only (no deploy) |
| `problem` | markdown | The story and context |
| `requirements` | markdown[] | At least one: what must be true |
| `hints` | markdown[] | At least one, revealed one at a time; the last one is the closest to the answer |
| `solution` | markdown | The steps and why they follow the official best practice |
| `solutionFiles` | `{path: content}` | Optional. Relative paths written over the starter in a copy ("Show me the solution"); also used to verify that the solution passes |
| `starter` | `{templateId}` or `{kind, cloud?, files, kubernetes?}` | `templateId` is an existing template id (`GET /api/v1/catalog/templates`: Terraform, `docker-*`, `k8s-*`). Inline starters use `kind: terraform\|docker\|kubernetes`, `cloud` for Terraform, and `files` (relative paths). For Kubernetes, `kubernetes: {sources, builds}` is optional (sources are detected otherwise) |
| `checks` | Check[] | At least one; ids are unique kebab-case; every check has a `label` (shown as a goal) |
| `adaptations` | `{provider: markdown}` | **Only for `global`, and required there**: which services and how, per provider (`aws`, `azure`, `gcp`, `oci`, `docker`, `kubernetes`) |
| `sources` | `[{title, url}]` | At least one official https page (see [Sourcing rule](#sourcing-rule)) |

The Learn page renders markdown safely. It supports paragraphs, `#`
headings, `-`/`1.` lists, fenced code blocks, `` `code` ``, `**bold**`,
`*italic*` and `[links](https://…)`. Raw HTML is never rendered.

## Roadmap reference

```yaml
  - op: upsert
    kind: roadmap
    id: docker-roadmap
    data:
      provider: docker
      title: Docker
      summary: From your first container to production-ready compose projects.
      levels:                      # in order, no repeats: beginner → expert
        - level: beginner
          title: First containers
          goals: |                 # markdown: what the learner can do afterwards
            Run, inspect and connect containers.
          steps:
            - title: Images and containers
              description: |
                …
              challenges: [docker-first-container]   # optional, must exist
              docs:                                  # at least one official page
                - { title: What is a container?, url: "https://docs.docker.com/get-started/docker-concepts/the-basics/what-is-a-container/" }
      sources:                     # what the roadmap structure follows (exam guides, curricula, …)
        - { title: Docker docs, url: "https://docs.docker.com/" }
```

## Check types

Every check has `id`, `label` and `type`, plus a parameter object named
like its type. `environment` (docker and Kubernetes only) opens the
workflow in that environment for `validate`, `rules_clean` and `has`: the
Kustomize overlay or Helm `values-<env>.yaml`, or `compose.<env>.yaml` +
`.env.<env>`. With `environment: prod`, the production-only rules run too.

| Type | Parameters | Passes when |
|---|---|---|
| `validate` | none | The workflow validates with no error findings: Terraform analysis (HCL, provider schema, references), compose load (`compose config`), or the Kubernetes render |
| `rules_clean` | `rules_clean: {codes?, prefixes?, minSeverity: info\|warning\|error}` | No finding matches. A finding matches when its code is in `codes` or starts with a prefix (`bp_`, `dk_`, `k8s_`; with neither, every finding matches) **and** its severity is at least `minSeverity`. Every rule runs, including opt-in rules, whatever the user's settings are |
| `has` | `has: {terraform \| compose \| kubernetes \| connected}` (exactly one) | The structure exists (see below) |
| `deploy` | none | The deploy succeeds: Docker compose up, the workflow's k3s cluster, or Floci for AWS |
| `http` | `http: {service, port?, ingress?, path, method?, status? (200), contains?}` | After the deploy, the endpoint answers with `status` (and the body contains `contains`). Docker uses the service's **published** port. Kubernetes uses a Service and its port (port-forward), or `ingress: true`. Terraform uses the node address and the endpoint the Load test tab lists |
| `load` | `load: {target: {service, port?, ingress?, path, method?}, spec: {preset? \| stages: [{durationSec, target}], mode?, timeoutMs?, maxWorkers?}, expect: [{metric, op, value, workload?}]}` | Every expectation holds on the run. Metrics: `p50_ms`, `p90_ms`, `p95_ms`, `p99_ms`, `max_ms`, `error_rate` (0–1), `rps_min`, `replicas_max`. Ops: `lt`, `lte`, `gt`, `gte` |
| `scenario` | `scenario: {target, spec, scenario: {id, target, atSec, durationSec, params?}, expect: {verdict: survived\|degraded, metrics?}}` | The incident scenario's verdict is `verdict` or better. `scenario.target` is the compose service or workload **name**; `atSec ≥ 10`. Leave at least 15 s of traffic after the fault ends |
| `readiness` | `readiness: {minScore, environment?}` | The production-readiness score reaches `minScore` |

`has` selectors (`count: {min?, max?}` defaults to at least one match;
`count: {max: 0}` means "must not exist"):

- `terraform: {type, name?, where?, count?}`: resources, or data sources
  as `data.<type>`.
- `compose: {service?, image? (prefix), category? (database|cache|queue|…), where?, count?}`:
  `where` reads the effective compose service with compose-spec keys
  (`healthcheck.test`, `deploy.resources.limits.memory`,
  `depends_on.db.condition`, `environment.REDIS_URL`). With no
  service/image/category, `where` reads the whole project
  (`networks.back.internal`).
- `kubernetes: {kind, name?, namespace?, where?, count?}`: rendered
  objects (`spec.replicas`, `spec.template.spec…`).
- `connected: {from, to}`: a graph edge in either direction. Each end is a
  Terraform type or address, a compose service, or a Kubernetes
  `Kind/name` or name.

`where` maps a path to an expected value:

- **Path syntax**: dots, `[0]` or `.0` for an index, `*` for any element,
  and `"quoted.key"` for keys that contain dots. In lists of objects, a
  plain segment selects the element whose name or key equals it
  (`containers.web.image`). A Terraform nested block matches any instance,
  and map attributes match by key (`tags.Environment`), also when some
  of the map's values are expressions (`app_settings.DB_PASSWORD`).
- **Expected values**:
  - A scalar must be equal. Numbers compare numerically, and `"true"`
    equals `true`.
  - A matcher object uses `exists`, `in`, `notIn`, `min`, `max`,
    `contains` or `ref` (`ref` matches an expression that references that
    type or address prefix).
- **Terraform expressions are not evaluated.** `exists: true` matches
  them. A scalar compares against the expression text, and `contains`
  searches inside it.

Design-only challenges (`runtime: design`) use only `validate`,
`rules_clean`, `has` and `readiness`. Keep thresholds generous so they
don't depend on the speed of the machine.

## Sourcing rule

Every fact, recommendation and roadmap structure must come from
**official** documentation that you read while writing:

- AWS docs and the Well-Architected Framework.
- Microsoft Learn, including the Azure Well-Architected Framework and the
  Cloud Adoption Framework.
- Google Cloud docs and the Architecture Framework.
- OCI docs and the OCI Architecture Center.
- Docker docs.
- kubernetes.io and the CNCF curricula.
- Terraform Registry provider docs.
- Certification exam guides from these vendors.
- The project's own docs for software used in a challenge (Redis,
  PostgreSQL, NGINX, …).

Cite the exact pages in `sources` (challenges) and `docs` (roadmap steps),
and write the text in your own words.
Community sites (blogs, roadmap.sh, Stack Overflow, Medium) are not
sources.

The validator only accepts https URLs on these official hosts (and their
subdomains), listed in `officialHosts` in
[`validate.go`](../backend/internal/learn/validate.go):

- **AWS**: docs.aws.amazon.com, aws.amazon.com, skillbuilder.aws,
  d1.awsstatic.com
- **Azure**: learn.microsoft.com, azure.microsoft.com
- **Google Cloud**: cloud.google.com, docs.cloud.google.com,
  services.google.com, sre.google
- **Oracle**: docs.oracle.com, www.oracle.com, education.oracle.com,
  mylearn.oracle.com, learn.oracle.com
- **Docker**: docs.docker.com, www.docker.com, hub.docker.com
- **Kubernetes and CNCF**: kubernetes.io, cncf.io,
  training.linuxfoundation.org, helm.sh, kustomize.io, opentelemetry.io,
  prometheus.io
- **Terraform**: registry.terraform.io, developer.hashicorp.com
- **Project docs**: redis.io, www.postgresql.org, nginx.org,
  docs.nginx.com, www.rabbitmq.com, dev.mysql.com, docs.haproxy.org,
  nodejs.org
- **GitHub**: repositories under `github.com/` of `cncf`, `kubernetes`,
  `kubernetes-sigs`, `docker`, `hashicorp`, `aws`, `awslabs`, `Azure`,
  `GoogleCloudPlatform` and `oracle`

If you need another official host, add it in the same pull request and
explain why.

Verify Terraform resource and attribute names against the provider docs,
and by validating the workflow in Nimbo.

## Verify locally

1. **Validate the content**. This checks file names, fields, ids, official
   https sources, check shapes and starters (through the grading engine's
   `challenge.Validate`), roadmap and prerequisite references, and applies
   every migration to a fresh database:

   ```sh
   cd backend && go test ./internal/learn/ -run TestMigrations
   ```

   The static self-check grades every challenge's static checks. The
   starter must fail at least one of them (when all checks are static),
   and the starter plus `solutionFiles` must pass them all:

   ```sh
   cd backend && go test ./internal/challenge/ -run TestContentStaticChecks
   ```

2. **Grade it for real** with a throwaway home, so your own
   `~/.nimbo/learn.db` isn't touched:

   ```sh
   cd backend && go build -o /tmp/nimbo-dev/nimbo ./cmd/nimbo
   /tmp/nimbo-dev/nimbo serve --home /tmp/nimbo-dev/home --addr 127.0.0.1:7790
   # another terminal: start the challenge, check the starter (must fail), then the solution (must pass)
   curl -s -XPOST localhost:7790/api/v1/learn/challenges/<id>/start -d '{}'          # → workflow id
   curl -s -XPOST localhost:7790/api/v1/workflows/<wf>/challenge/check
   curl -s localhost:7790/api/v1/workflows/<wf>/challenge                           # poll until finished
   curl -s -XPOST localhost:7790/api/v1/workflows/<wf>/challenge/solution          # → copy with solutionFiles
   ```

   You can also open the desktop app against that daemon:

   ```sh
   cd apps/desktop && VITE_NIMBO_URL=http://127.0.0.1:7790 bunx vite
   ```

   Then use **Learn → challenge → Start challenge**, and **Check my
   solution** in the workflow's Challenge tab.

3. While you iterate on an **unmerged** file, delete the throwaway
   `learn.db` (`rm /tmp/nimbo-dev/home/learn.db*`) before restarting the
   daemon. Otherwise the daemon reports that you edited a migration it
   already applied.

## How it works

- **Schema**: `learn.db` has these tables:
  - `migrations`: filename, checksum, description, applied_at.
  - `challenges`: id, provider, level, title, search text, JSON data, and
    the migration that last wrote it.
  - `challenge_tags`: tags, for filtering.
  - `roadmaps`: one row per provider.
  - `progress`: status, attempts, completed goals, workflow id, and
    started/updated/completed times. It has no foreign key to
    `challenges`: progress survives updates and deletes.
- **Startup**: a migration that fails is logged and listed on the Learn
  page. Everything before it is still served, and the daemon keeps
  running.
- **Progress**: *Start challenge* marks the challenge in progress and
  links its workflow. Every finished *Check my solution* adds an attempt
  and records the goals that passed. A fully passing check completes the
  challenge. *Reset progress* clears it.
- **Agents**: the MCP tools `list_learning_paths`, `list_challenges` and
  `get_challenge` read the same content (see [MCP.md](MCP.md#learn)).
