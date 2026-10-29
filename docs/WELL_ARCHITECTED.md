# Well-Architected in Nimbo

Nimbo helps AI agents (and you) design infrastructure along the pillars the
cloud frameworks share — [AWS Well-Architected](https://aws.amazon.com/architecture/well-architected/),
[Azure Well-Architected / Architecture Center](https://learn.microsoft.com/en-us/azure/architecture/),
[Google Cloud Architecture Center / Framework](https://docs.cloud.google.com/architecture/)
and [OCI best practices](https://docs.oracle.com/en/solutions/oci-best-practices/) —
for Terraform workflows and for local Docker Compose and Kubernetes workspaces.

## Pillars

| Pillar | What it covers |
|---|---|
| `security` | Least privilege, encryption, private networks, secrets, hardened containers |
| `reliability` | Health checks, backups, multiple zones/replicas, self-healing, deletion protection |
| `performance` | Scaling (autoscaling, replicas), right-sized resources, caching, load balancing |
| `cost` | Tags/labels, retention and lifecycle, scale to demand, slim images |
| `operations` | Logs and alarms, pinned versions, parameters instead of hard-coding, remote state |

Every best-practice rule (`bp_*` Terraform, `dk_*` Docker, `k8s_*`
Kubernetes) lists the pillars it serves, most relevant first
(`GET /api/v1/best-practices/rules`, field `pillars`).

## What Nimbo checks per platform

`GET /api/v1/well-architected/{platform}` (MCP `well_architected_guidance`)
returns the practices per pillar, each with why, how (resource types and
attributes, compose or Kubernetes fields) and the rule codes that check it.

| Platform | Framework | Examples |
|---|---|---|
| `aws` | AWS Well-Architected Framework | Least-privilege IAM, restricted security groups, KMS encryption, IMDSv2; multi-AZ, RDS backups and deletion protection, Auto Scaling health checks, queues with DLQs; tags, log retention, pinned versions; network checks (`net_*`: overlaps, routing, single AZ) |
| `azure` | Azure Well-Architected Framework | Managed identities and RBAC, private endpoints, NSGs, storage and Key Vault hardening, WAF; zones, soft delete, backups, health probes; Log Analytics diagnostics |
| `gcp` | Google Cloud Well-Architected Framework | Dedicated service accounts, private instances and Cloud SQL, firewall rules, CMEK and audit logs; regional MIGs, Cloud SQL HA and backups; labels, flow logs |
| `oci` | OCI Well-Architected Framework | Least-privilege policies, security lists/NSGs, private buckets with your keys; fault/availability domains, backups; tags, flow logs |
| `docker` | Docker build and Compose production best practices | Non-root users, no docker.sock/privileged, secrets in `.env`, databases unpublished; healthchecks, `service_healthy`, restart policies, named volumes; limits, multi-stage builds; log rotation; pinned tags |
| `kubernetes` | Kubernetes configuration and security best practices | Restricted securityContext, NetworkPolicies, RBAC, Secrets; probes, replicas + PDBs and spread (production environments); requests/limits, HPAs; recommended labels, current APIs |

## Review a workflow

`GET /api/v1/workflows/{id}/well-architected` (MCP `review_well_architected`)
runs every rule for the workflow's kind — opt-in rules and rules turned off
in Settings included; Terraform also the `net_*` network checks — and lists
each finding once, under its rule's main pillar. A pillar is `ok` (no
findings), `advice` (info only) or `risk` (warnings or errors). The
workflow's own diagnostics keep following your Settings.

Severity policy: warnings are security or data-loss risks; reliability,
scaling, cost and operations advice is info. Production-only practices
(multi-AZ, deletion protection, long backup retention) are info and often
opt-in, so a local sandbox stays quiet; apply them when you want production
parity.

## Recommendations (advisor)

`GET /api/v1/workflows/{id}/recommendations` (MCP `recommend_improvements`)
lists the companions a senior engineer would add to *this* design, highest
value first: a public API → WAF with a rate-based rule and stage
throttling (or the provider's equivalent: Front Door/App Gateway WAF or
APIM rate limits, Cloud Armor, OCI WAF/API Gateway rate limiting); a
published local service → a rate-limiting reverse proxy; an Ingress → TLS
and rate-limit annotations; a queue consumer → a DLQ. Each item has why,
pillars, the nodes that triggered it, what to add, a snippet with the
design's real addresses, `scope` (`recommended` everywhere or `production`
only), `local` (runs locally, export only, or the local equivalent), when
to skip it, the rules that check it and an official docs link. An item
disappears once the design has the companion, so agents loop until the list
is empty or the rest is declined. The review
(`GET /workflows/{id}/well-architected`) carries the same list as
`recommendations`. Dev and prototypes get fewer additions; production-only
items are labelled as such.

## Layouts

Each guide has a `layout` (summary, tree, what each environment adds,
sources) matching what Nimbo exports:

| Platform | Layout |
|---|---|
| Terraform (`aws`, `azure`, `gcp`, `oci`) | One root, or `modules/<service>` + `environments/<env>/` roots with one remote state each; modules ≤2 levels, no provider blocks, outputs for every resource; no CLI workspaces; pinned versions, validated variables, tags from one locals map |
| `docker` | Base `compose.yaml` + `compose.<env>.yaml` and/or `.env.<env>`; `secrets:`; no code bind mounts in prod |
| `kubernetes` | Kustomize `base/` + `overlays/<env>/` (prod: HPA with bounds, PDB, NetworkPolicy, Pod Security labels) or Helm `values-<env>.yaml` |

## Architecture patterns

Before the pillars come the shape of the system. `GET /api/v1/patterns`
(MCP `architecture_patterns`) is a catalog of architecture styles
(`n-tier`, `web-queue-worker`, `microservices`, `event-driven`,
`serverless`, `modular-monolith`, `big-data-streaming`) and cloud design
patterns (API gateway, BFF, gateway offloading, cache-aside, CQRS, event
sourcing, saga, transactional outbox, pub/sub, fan-out, competing
consumers, queue-based load leveling, async request-reply, claim check,
circuit breaker, retry, bulkhead, rate limiting, strangler fig,
anti-corruption layer, sidecar, database per service, sharding, valet key,
deployment stamps…). Each pattern has the problem it solves, `useWhen`
(requirement signals), `avoidWhen` (the over-engineering guard: a CRUD API
stays simple), trade-offs, concrete practices (idempotency keys, outbox,
DLQs, retries with backoff and jitter, timeouts, versioned events) and one
block per platform: the Terraform resource types on AWS, Azure, GCP and
OCI, the compose services or the Kubernetes kinds, with whether it runs
locally, is export only, or has a local equivalent. Related patterns and
the Nimbo templates that start it are linked.

- `GET /api/v1/patterns?requirement=&platform=&limit=` ranks patterns for a
  free-text requirement (English or Spanish), styles first (default 6, at
  most 20); without `requirement` it lists the catalog (id, name, kind,
  problem, platforms), optionally only those with a block for `platform`.
- `GET /api/v1/patterns/{id}` returns one pattern in full (404 if unknown).

Agents settle the non-functional requirements first (exposure, traffic and
peaks, latency, consistency, data sensitivity/compliance, availability,
team maturity, budget), pick the simplest style and the fewest patterns
that meet them, and tell the user the trade-offs and what they deliberately
left out. Prompt `design_from_requirements` ends with an architecture
decision record (context, decision, alternatives, consequences).

Sources: [Azure Architecture Center cloud design patterns](https://learn.microsoft.com/en-us/azure/architecture/patterns/)
and [architecture styles](https://learn.microsoft.com/en-us/azure/architecture/guide/architecture-styles/),
[AWS Prescriptive Guidance cloud design patterns](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/introduction.html),
[Google Cloud Architecture Center](https://docs.cloud.google.com/architecture/)
and [microservices.io patterns](https://microservices.io/patterns/).

## How agents use it

1. Settle the requirements, then `architecture_patterns {requirement,
   platform}` to choose the style and patterns (with why and what is left
   out).
2. `well_architected_guidance {platform}` before designing (practices and
   layout); clarify public vs internal and dev vs production.
3. Build with secure, reliable and scalable defaults.
4. `recommend_improvements {workflow}` after the first draft: propose each
   item with its why, apply what the user accepts, call again.
5. `review_well_architected {workflow}` and `compliance_report` before
   finishing: fix warnings first, then the advice that fits the goal;
   explain what stays.
6. Validate and deploy locally, then load-test and run failure scenarios
   (docker/Kubernetes) and `production_readiness`.

Prompt `design_well_architected {goal, platform?}` walks this loop;
`design_from_requirements {requirement, platform?, environment?}` adds the
requirements and architecture decision around it; see
[MCP.md](MCP.md#well-architected-pillar-guidance-and-review).

## Sources

Besides each framework, the guides cite AWS Prescriptive Guidance
(Terraform structure), the HashiCorp style guide, Google Cloud's Terraform
root-module and style guidance, Docker's Compose-in-production and `deploy`
references, Kubernetes Pod Security Standards/Admission and Kustomize docs,
the OWASP API Security Top 10 2023 (API4 unrestricted resource consumption)
and the Terraform registry pages of the WAF resources
(`aws_wafv2_web_acl`, `azurerm_cdn_frontdoor_firewall_policy`,
`google_compute_security_policy`, `oci_waas_waas_policy`).
