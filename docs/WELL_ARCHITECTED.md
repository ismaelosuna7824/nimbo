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

## How agents use it

1. `well_architected_guidance {platform}` before designing.
2. Build with secure, reliable and scalable defaults.
3. `review_well_architected {workflow}` before finishing: fix warnings
   first, then the advice that fits the goal; explain what stays.
4. Validate and deploy locally, then load-test and run failure scenarios
   (docker/Kubernetes) and `production_readiness`.

Prompt `design_well_architected {goal, platform?}` walks this loop; see
[MCP.md](MCP.md#well-architected-pillar-guidance-and-review).
