package mcp

import (
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerPrompts adds the guided multi-step recipes agents can offer to
// the user (slash commands in most MCP clients).
func registerPrompts(r *registry) {
	r.addPrompt(&sdk.Prompt{
		Name:        "create_docker_stack",
		Title:       "Create Docker stack",
		Description: "Build a docker-compose stack (services, Dockerfiles, databases) that follows Docker best practices, run it and prove it works.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to run, e.g. \"a Node.js API with PostgreSQL and Redis\"", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Build this Docker stack with the Nimbo tools: %s

Steps:
1. architecture_patterns (platform docker) with the request: pick the simplest style/patterns that fit (e.g. web-queue-worker when work can be async, cache-aside only for a measured read need) and tell the user what you left out. Then list_templates: if a docker-* template is close, create_workflow from it (templates are worked examples of every practice below; category "edge-patterns" has a reverse proxy, an API gateway and a load balancer); otherwise create_workflow kind "docker" with a short descriptive name.
2. Data services first: add_service with catalog_id from list_docker_images (pinned tag, credentials moved to .env, named volume, healthcheck, published on 127.0.0.1). Then the app services: add_service build_context with a starter (node, python, go, static) or your own Dockerfile, written with get_compose + apply_terraform_code.
3. Wire them with connect_services: depends_on with condition service_healthy on every database, cache and broker; env recipes for connection URLs (DATABASE_URL=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@db:5432/app).
4. Follow these practices in every file you write:
   - Images: pinned tags (postgres:16-alpine, node:22-alpine), never latest, in compose and in every FROM.
   - Dockerfiles: multi-stage builds for compiled code and bundles (Go, Rust, Java, .NET, npm run build) with a slim runtime stage (alpine, -slim, gcr.io/distroless, -jre); copy the dependency manifests (package.json + lock, go.mod/go.sum, requirements.txt, pom.xml, *.csproj) and install before COPY . so source edits reuse the dependency layer; BuildKit cache mounts for package managers (RUN --mount=type=cache,target=/root/.npm npm ci; /root/.cache/pip; /go/pkg/mod; /root/.m2; /root/.nuget/packages); apt-get install --no-install-recommends with rm -rf /var/lib/apt/lists/* in the same RUN, apk add --no-cache; COPY (not ADD), WORKDIR (not cd); a non-root USER; exec-form CMD/ENTRYPOINT; a HEALTHCHECK (or a compose healthcheck).
   - A .dockerignore in every build context: node_modules, .git, .venv, target, bin/obj, .env*.
   - Secrets: in .env as ${VAR} (or secrets:), never literal in compose; build secrets with RUN --mount=type=secret, never ARG/ENV.
   - Services: restart: unless-stopped; healthchecks with interval 5s or more and timeout below interval; init: true for node/python processes; deploy.resources.limits with memory and cpus; json-file logging with max-size and max-file (one x-defaults anchor merged into every service with <<: *defaults keeps restart and logging in one place); named volumes for data, never anonymous; data services on an internal network (networks: {data: {internal: true}}) with the app on both networks; publish only what users reach, databases on 127.0.0.1:<port> or not at all; no docker.sock, privileged, network_mode: host or pid: host.
5. Fix every error diagnostic and every best_practices finding your edits return, then call review_docker and fix its warnings and info (or tell the user why one stays). Mention the opt-in rules it lists (hardening, limits, cache mounts) if they matter for the request. Environments (when the user wants more than one): create_environment per environment, each a compose.<env>.yaml and/or .env.<env> on top of the base compose.yaml (see well_architected_guidance platform docker, layout); no code bind mounts in prod. Then recommend_improvements: propose each item with its one-line why (e.g. a rate-limiting reverse proxy in front of a published API) and apply only what the user accepts or what clearly fits.
6. deploy (docker compose up, waits for healthy; remap_ports=true if host ports are taken). On failure read the log tail and get_deployed_state, fix and deploy again.
7. get_endpoints and try_endpoint on the published services, then list_load_targets + run_load_test with preset smoke; get_docker_metrics for image sizes, build times, memory vs limits and startup times (resize limits from it). For anything headed to production, add_monitoring (Prometheus, Grafana, cAdvisor and container alerts) and deploy again.
8. For anything headed to production, production_readiness: resolve its open items (a load test, run_load_test with a scenario from list_scenarios for the app and each data service, plan_capacity for the target traffic) or tell the user which stay open.
9. Summarize for the user: services and how they connect, what is published where, image sizes, health and smoke-test results, the readiness score, findings left open and why, and how to stop it (destroy).`, req), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "create_infrastructure",
		Title:       "Create infrastructure",
		Description: "Design, deploy and smoke-test new infrastructure from a plain-language request.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to build, e.g. \"an HTTP API that stores orders in DynamoDB\"", Required: true},
			{Name: "cloud", Description: "aws, gcp, azure or oci (default: inferred from the request, else aws)"},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		cloud := strings.TrimSpace(a["cloud"])
		if cloud == "" {
			cloud = "infer it from the request; default to aws"
		}
		return fmt.Sprintf(`Build this infrastructure with the Nimbo tools: %s
Cloud: %s.

Steps:
1. architecture_patterns with the request and the cloud: choose the simplest style and patterns that meet it (one-line why each, what you left out). Then plan the structure (the plan_structure recipe): list the resources per logical group (network foundation, data tier, security controls, each app/service) and estimate their count. Around 100 resources or more: one workflow per layer (network, data, app), each with its own state, upper layers finding lower ones with data sources by tags/IDs. Two or more environments (dev, prod, ...): the environments layout, one state per environment. Tell the user the plan in a few lines before building.
2. list_templates; if one is close, create_workflow from it, otherwise create_workflow empty with a short descriptive name (one per layer when you split).
3. For each service needed: search_resources (prefer floci_supported=true so it runs in the local sandbox) and describe_resource for required attributes.
4. add_node for each resource (string attributes are quoted HCL: "\"my-name\""). Keep the conventions the starters add: names "${local.name_prefix}-<role>", configurable values as typed, described variables (set_variable with description) or locals, no hardcoded regions/zones/account IDs, outputs with descriptions (set_output), sensitive = true for secrets. Prefer attachment resources (aws_vpc_security_group_ingress_rule, aws_iam_role_policy_attachment) over inline rule blocks.
5. Wire nodes: list_connection_options, then connect with the best recipe (it adds IAM policies, permissions, event mappings).
6. Fix every error diagnostic (update_node) and every best_practices finding an edit returns. Organize before deploying: each logical group with several resources becomes a local module with extract_module (dry_run=true first; snake_case name after the group: network, data, orders_api), called from the root through its inputs and outputs. Modules: never a single resource, no provider blocks, no nesting below the first level, every resource reachable from an output, environment-specific inputs without defaults, units in names (ram_size_gb), positive booleans (enable_x).
7. Environments (when the user wants more than one): set_environment per environment with its non-secret values (secrets as secret env vars TF_VAR_<name> with secret_keys, never as values); with two or more the layout is environments (update_workflow layout to change it). Never use terraform.workspace.
8. Call review_terraform: read its structure (states, resources per state, modules, nesting, backend) and fix the findings (warnings first), then recommend_improvements (propose each companion with its one-line why, e.g. WAF + rate limiting for a public API; apply what the user accepts or what clearly fits; production-scope and export-only items are proposals in the local sandbox) and review_well_architected for the per-pillar view (security, reliability, performance, cost, operations) and fix its warnings; then validate.
9. deploy into the local sandbox; on failure read the log tail, fix and redeploy.
10. list_load_targets and run_load_test with preset smoke to prove it works.
11. Summarize for the user: the structure (workflows/layers, modules, environments and layout), what was built (nodes and how they connect), deployed endpoints/outputs, smoke-test latency and error rate, best-practice findings you deliberately left (and why), and next steps (e.g. a load test or export_terraform).

Nothing touches a real cloud: everything runs in the local Floci emulator. Tell the user they can watch the graph live in the Nimbo app.`, req, cloud), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "create_kubernetes_app",
		Title:       "Create Kubernetes app",
		Description: "Build a Kubernetes app (manifests, Kustomize or Helm) that follows Kubernetes best practices, deploy it to a local cluster and prove it works.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to run, e.g. \"a Node.js API with PostgreSQL behind an Ingress, dev and prod\"", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Build this Kubernetes app with the Nimbo tools: %s

Steps:
1. architecture_patterns (platform kubernetes) with the request: choose the simplest style/patterns that fit (one-line why each, what you left out). Then list_templates: if a k8s-* template is close (web app + PostgreSQL, Kustomize dev/prod, Helm chart, microservices with Ingress + HPA, CronJob worker, and the "edge-patterns" reverse proxy, API gateway and load balancer), create_workflow from it; with an existing docker workflow, deploy it and run_load_test against its HTTP service, then convert_to_kubernetes with format kustomize or helm (requests/limits sized from that run, HPAs in prod); otherwise create_workflow kind "kubernetes". Several environments: Kustomize overlays/<env> or Helm values-<env>.yaml.
2. Add objects with add_kubernetes_object: catalog_id from list_kubernetes_catalog (image:<id> for databases and services: probes, resources, claims and a Secret already set; chart:<id> for pinned Helm charts; starters for Ingress, HPA, PDB, NetworkPolicy, ConfigMap, Secret) or your own yaml. Code you build goes in a folder with a Dockerfile, its image named <folder>:dev.
3. Wire them with connect_kubernetes (expose, route, envFrom/volume, env, scale) and change fields with update_kubernetes_object.
4. Follow these practices: pinned tags (local images <name>:dev + imagePullPolicy IfNotPresent); requests (cpu, memory) and a memory limit on every container; readiness and liveness probes on named ports for every container that serves a port; credentials in Secrets (secretKeyRef/envFrom), never literal env values; selectors equal to the pod template labels; Ingress backends on existing Service ports; securityContext runAsNonRoot + numeric runAsUser, automountServiceAccountToken: false when the pod never calls the API, no privileged/hostPath/hostNetwork; a Namespace object for the app; app.kubernetes.io/name and instance labels; StatefulSets with volumeClaimTemplates for data and an init container that waits for dependencies (pg_isready) instead of crash-looping; prod-like environments with replicas >= 2 (or an HPA), a PodDisruptionBudget with minAvailable below the replicas, topologySpreadConstraints on kubernetes.io/hostname (ScheduleAnyway) and RollingUpdate maxUnavailable: 0 / maxSurge: 1.
5. Fix every error diagnostic and best_practices finding the edits return, then review_kubernetes for each environment (update_workflow environment switches it) and fix warnings and info (or tell the user why one stays). Layout: base plus overlays/<env> (Kustomize) or values-<env>.yaml (Helm); the prod one carries the HPA (min ≥2, max bound), PDB, NetworkPolicies and PSS namespace labels (well_architected_guidance platform kubernetes, layout). Then recommend_improvements: propose each item with its one-line why (e.g. Ingress TLS and rate-limit annotations) and apply only what the user accepts or what clearly fits.
6. deploy (local k3s cluster; waits for rollouts). On failure read get_deployed_state problems/events and get_kubernetes_logs, fix and deploy again.
7. get_endpoints and try_endpoint on the Services and Ingress URLs, then list_load_targets + run_load_test with preset smoke. With an HPA in the deployed environment, run_load_test with preset autoscale and read its autoscaling verdict (late: lower the CPU target or raise minReplicas; maxed: raise maxReplicas; no_metrics: set cpu requests).
8. production_readiness: resolve its open items per environment (add_monitoring for monitoring, a load test in each production-like environment for scaling, run_load_test with a scenario for resilience, plan_capacity) or tell the user which stay open.
9. Summarize: objects and how they connect, URLs, health and smoke-test results, the readiness status per environment, findings left open and why, and how to stop it (destroy, kubernetes_cluster stop).`, req), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "design_from_requirements",
		Title:       "Design from requirements",
		Description: "Act as a senior architect: settle the non-functional requirements, choose the simplest fitting architecture style and patterns, build it Well-Architected, prove it locally and record the decision.",
		Arguments: []*sdk.PromptArgument{
			{Name: "requirement", Description: "what the system must do, e.g. \"an orders API with spiky traffic that notifies billing and shipping\"", Required: true},
			{Name: "platform", Description: "aws, azure, gcp, oci, docker or kubernetes (default: inferred from the requirement or the open workflow, else aws)"},
			{Name: "environment", Description: "intent, e.g. prototype, dev or production (default: ask or assume dev)"},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "requirement")
		if err != nil {
			return "", err
		}
		platform := strings.TrimSpace(a["platform"])
		if platform == "" {
			platform = "infer it from the requirement or the open workflow (get_open_workflow); default to aws"
		}
		env := strings.TrimSpace(a["environment"])
		if env == "" {
			env = "not stated: assume dev unless the requirement implies production, and say so"
		}
		return fmt.Sprintf(`Design this as a senior architect with the Nimbo tools: %s
Platform: %s.
Environment intent: %s.

Steps:
1. Requirements: restate the requirement in one or two lines, then the non-functional requirements to settle: exposure (public or internal), expected traffic and peaks, latency targets, consistency needs (strong vs eventual), data sensitivity and compliance (CIS, PCI, HIPAA), availability target, team/ops maturity, budget and environments. Ask the user only what you cannot reasonably assume; state every assumption you make.
2. Architecture: architecture_patterns with the requirement and the platform; pattern=<id> for the candidates you keep. Choose one style and the fewest patterns that meet the requirements, each with a one-line justification tied to an NFR, and say what you deliberately did NOT add and why (check each pattern's avoidWhen: a simple CRUD API stays simple; no microservices, CQRS, event sourcing or sagas without the need that justifies them).
3. well_architected_guidance for the platform (every cloud the design uses): the practices that shape it and its layout (folders, environments).
4. Structure: the plan_structure recipe for Terraform (groups, states, modules, environments); for docker/Kubernetes, the services/objects and environments from the layout. Present the plan with the chosen patterns mapped to concrete resources/services, and wait for an OK when the design is large.
5. Build it: the create_infrastructure, create_docker_stack or create_kubernetes_app recipe (templates the patterns name are good starting points), applying the patterns' practices (idempotency, DLQs, retries with backoff and timeouts, outbox, health checks…). Fix every error diagnostic and best_practices finding the edits return.
6. Review: recommend_improvements (propose each item with its one-line why, apply what the user accepts or what clearly fits), review_well_architected (fix warnings first) and compliance_report; explain what you leave open.
7. Prove it locally: validate and deploy; list_load_targets + run_load_test (smoke, then a profile that checks the traffic and latency targets); for docker/Kubernetes, list_scenarios and a failure scenario (kill_replica, dependency_down) and production_readiness; plan_capacity where available. Say what is export-only (not emulated locally).
8. Summarize as an architecture decision record: context (requirement, NFRs, assumptions), decision (style, patterns and how they map to the platform's services), alternatives considered and why not, consequences (trade-offs, risks, what production would add, findings left open), plus the load-test and scenario results.

Nothing touches a real cloud: everything runs locally.`, req, platform, env), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "design_well_architected",
		Title:       "Design Well-Architected",
		Description: "Design infrastructure (Terraform on AWS, Azure, GCP or OCI, Docker Compose or Kubernetes) along the provider's Well-Architected pillars: secure, reliable and scalable defaults, reviewed per pillar and proven locally.",
		Arguments: []*sdk.PromptArgument{
			{Name: "goal", Description: "what to build, e.g. \"a highly available orders API with PostgreSQL\"", Required: true},
			{Name: "platform", Description: "aws, azure, gcp, oci, docker or kubernetes (default: inferred from the goal or the open workflow, else aws)"},
		},
	}, func(a map[string]string) (string, error) {
		goal, err := required(a, "goal")
		if err != nil {
			return "", err
		}
		platform := strings.TrimSpace(a["platform"])
		if platform == "" {
			platform = "infer it from the goal or the open workflow (get_open_workflow); default to aws"
		}
		return fmt.Sprintf(`Design this with the Nimbo tools, following the Well-Architected pillars (security, reliability, performance, cost, operations): %s
Platform: %s.

Steps:
1. architecture_patterns with the goal and platform: choose the simplest architecture style and patterns that meet it (one-line why each, trade-offs, what you deliberately left out). Then well_architected_guidance for the platform (every cloud the design uses): read each pillar's practices and the rule codes that check them. Tell the user in a few lines which patterns and practices shape the design.
2. Plan the structure and environments with the guidance's layout (plan_structure for Terraform; the create_docker_stack / create_kubernetes_app recipes for local workspaces): logical groups, environments and what differs per environment, what is public and what stays private.
3. Build with secure, reliable and scalable defaults:
   - Security: least-privilege IAM scoped to the resources used, encryption at rest and in transit, private data tiers (no 0.0.0.0/0 to admin/database ports, databases unpublished or on 127.0.0.1, internal networks), secrets in secret stores/.env/Secrets, non-root containers.
   - Reliability: health checks, backups with retention, restart policies or readiness/liveness probes, depends_on service_healthy, PodDisruptionBudgets; production-only practices (multi-AZ, deletion protection, long backup retention) are advice in the local sandbox: mention them and apply them when the user wants production parity.
   - Performance and scaling: autoscaling (auto scaling groups, HPAs) or replicas sized from load tests, resource requests/limits, caches and queues to absorb spikes.
   - Cost: right-sized instances and limits, lifecycle/retention policies, no idle capacity.
   - Operations: tags/labels, log retention, monitoring (add_monitoring for docker/Kubernetes), everything as code with pinned versions.
   Fix every error diagnostic and best_practices finding the edits return.
4. recommend_improvements after the first draft: propose each companion with its one-line why (WAF/rate limiting on public entry points, DLQs, backups, alarms…), apply what the user accepts or what clearly fits; production-scope items stay proposals in the local sandbox, and say when one is export-only (not emulated locally). Call it again until nothing you intend to apply is left. Then review_well_architected: fix warnings (and errors) first, then the info that fits the goal; call it again until the risk pillars are clean. Explain every finding you leave and why.
5. validate and deploy locally (Floci sandbox for Terraform, docker compose, or the workflow's k3s cluster); on failure read the logs, fix and redeploy.
6. Prove it: list_load_targets + run_load_test (smoke, then load); for docker/Kubernetes, list_scenarios and run_load_test with a failure scenario (kill_replica, dependency_down) and production_readiness; plan_capacity for the target load where available.
7. Summarize per pillar: what the design does for it, review status, findings left open and why, load-test and scenario results, and what production would add.

Nothing touches a real cloud: everything runs locally.`, goal, platform), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "extend_workflow",
		Title:       "Extend workflow",
		Description: "Add and connect services to an existing workflow (the one open in the app by default).",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to add, e.g. \"an SQS queue feeding the Lambda\"", Required: true},
			{Name: "workflow", Description: "workflow ID or name (default: current, the workflow open in the app)"},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		wf := workflowOrCurrent(a["workflow"])
		return fmt.Sprintf(`Extend the Nimbo workflow %q with: %s

Steps:
1. get_workflow (workflow %q) to understand the current nodes, edges, diagnostics and structure (layout, resources per state, modules, environments). If it fails because nothing is open, ask the user which workflow to use (list_workflows).
2. Decide where the addition belongs before adding it: inside the module of its logical group (network foundation, data tier, security controls, an app/service) or a new group. If the addition takes the state past ~100 resources, propose a separate workflow for the new layer (it finds the existing one with data sources by tags/IDs) instead of growing this one (see plan_structure).
3. search_resources / describe_resource for each new service (prefer floci_supported=true).
4. add_node for each new resource, then list_connection_options + connect to wire it to the existing nodes the request mentions. Follow the workflow's conventions: names from local.name_prefix when it exists, configurable values as described variables or locals (environment-specific ones without defaults, with a value per environment via set_environment), descriptions on new variables/outputs, sensitive secrets (never as environment values), no hardcoded regions/zones/account IDs, attachment resources rather than inline rule blocks.
5. Fix error diagnostics with update_node and the best_practices findings your edits return. If the addition makes one group large or repeats an existing group, extract it into a module with extract_module (dry_run=true first); an existing local module grows by inline_module + extract_module or by editing its files. Modules: no single-resource wrappers, no provider blocks, no nesting below the first level, every resource reachable from an output. Call review_terraform and fix what your change introduced (findings that were there before are the user's call), then validate.
6. plan and report what Terraform would add, change or destroy. Deploy only if the user asks.
7. Summarize the changes (new nodes, new connections, where they sit in the structure, best-practice findings left open) for the user.

Pass workflow %q to every tool so the edits land in the same workflow.`, wf, req, wf, wf), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "import_terraform_folder",
		Title:       "Import Terraform folder",
		Description: "Import an existing Terraform folder or repository and explain what was found.",
		Arguments: []*sdk.PromptArgument{
			{Name: "dir", Description: "absolute path of the folder or repository", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		dir, err := required(a, "dir")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Import the Terraform code in %s into Nimbo.

Steps:
1. scan_terraform_folder with dir %q. Show the user the projects found (path, resources, modules, providers, clouds, diagnostics) and what was skipped.
2. If there are several projects, ask which to import (default: all of them); then import_terraform with dir and projects.
3. For each imported workflow, get_workflow and summarize: main services, how they connect, local modules (they can be opened in the app), and diagnostics that need fixing.
4. Check Floci support: search_resources/describe_resource tell whether each type runs in the local emulator. List unsupported types so the user knows what cannot be deployed locally.
5. Suggest next steps: fix diagnostics, validate, deploy to the sandbox, load test.`, dir, dir), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "load_test",
		Title:       "Load test",
		Description: "Deploy a workflow if needed and load-test it, interpreting the results.",
		Arguments: []*sdk.PromptArgument{
			{Name: "workflow", Description: "workflow ID or name (default: current, the workflow open in the app)"},
			{Name: "goal", Description: "what to learn, e.g. \"p95 under 200 ms at 50 rps\" or \"find the breaking point\""},
		},
	}, func(a map[string]string) (string, error) {
		wf := workflowOrCurrent(a["workflow"])
		goal := strings.TrimSpace(a["goal"])
		if goal == "" {
			goal = "check it handles a realistic load (preset load) and report latency, throughput and errors"
		}
		return fmt.Sprintf(`Load-test the Nimbo workflow %q. Goal: %s

Steps:
1. get_deployed_state (workflow %q). If nothing is deployed, validate and deploy first.
2. list_load_targets and pick the targets that matter for the goal (an HTTP endpoint first, else queues/tables/functions); tell the user which you chose.
3. run_load_test with preset smoke to make sure the target works; stop and diagnose if it errors.
4. Run the profile that answers the goal: load for normal traffic, stress or spike to find limits, soak for stability, autoscale for a Kubernetes workload with a HorizontalPodAutoscaler; add thresholds (p95_ms, error_rate, rps_min) that encode the goal.
5. Interpret: throughput, p50/p95/p99 latency, error rate, stability, breaking point and threshold results, the container bottleneck, and for Kubernetes the autoscaling verdicts (did new pods become Ready before latency degraded?). Say whether the goal is met and what to change if not (e.g. Lambda memory, batch size, DynamoDB capacity, HPA target or replica bounds).`, wf, goal, wf), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "plan_structure",
		Title:       "Plan structure",
		Description: "Design the workflow/module/environment breakdown of a request before adding any resource.",
		Arguments: []*sdk.PromptArgument{
			{Name: "request", Description: "what to build or grow, e.g. \"an online shop on AWS with dev and prod\"", Required: true},
		},
	}, func(a map[string]string) (string, error) {
		req, err := required(a, "request")
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`Plan the Terraform structure for: %s

Do not add resources yet. Follow HashiCorp's style guide, Google Cloud's root-module guidance and AWS Prescriptive Guidance:

1. Context: which clouds, which environments (dev, staging, prod, ...) and what already exists. If a workflow is open or named, get_workflow and read its layout and structure (resources per state, modules, nesting, backend).
2. Inventory: list the resources the request needs, grouped by logical group: network foundation (VPC, subnets, route tables, gateways, endpoints), data tier (databases, caches, tables, buckets that hold data), security controls (KMS keys, WAF, shared security groups, IAM boundaries), and each application/service (compute, API, queues, its IAM role and log group). Count instances (count/for_each included).
3. States: one workflow is one root module and one state per environment.
   - up to a few dozen resources: one workflow;
   - 50 to 100: one workflow, every logical group as a module;
   - more than ~100 (Google: at most 100 per state): one workflow per layer, created in order network → data → app. Upper layers read lower ones with data sources filtered by tags or IDs (data.aws_vpc with a Name tag, data.aws_subnets, data.aws_db_instance), never through shared state; tag what lower layers create so they can be found.
4. Environments: one state each (environments layout, automatic from two environments; never CLI workspaces or terraform.workspace); well_architected_guidance's layout shows the tree and what each environment adds (prod: multi-AZ, deletion protection, WAF and alarms on public entry points, bounded autoscaling). For every input decide: environment-specific (instance sizes, counts, CIDRs, domain names: variables without defaults, a value per environment with set_environment) or environment-independent (variables with defaults or locals). Secrets are sensitive variables fed from secret env vars (TF_VAR_<name>), never tfvars values.
5. Modules per workflow: one local module per logical group (modules/network, modules/data, modules/<service>), never one per resource; no provider blocks inside; call them from the workflow's root only (the environments layout already wraps the workflow in a service module, so nesting stays within 2 levels); outputs for every resource callers need (IDs, ARNs, endpoints); input names with units (ram_size_gb, retention_days) and positive booleans (enable_x). Registry modules pinned with version, git modules with ?ref=<tag>.
6. Present the plan to the user and wait for an OK: a tree like
   <workflow>/
     modules/network  modules/data  modules/<service>
     environments/dev  environments/prod   (one backend.tf and state each)
   plus a table: workflow → layer → modules → estimated resources → environments → how it finds the layers below.
7. After the OK: create_workflow per workflow (descriptive names, e.g. "shop-network"), add each group's resources and connect them, extract_module per group (dry_run=true first), set_environment per environment, update_workflow layout if it should differ from auto, then review_terraform: its structure must match the plan and its findings should be fixed (warnings first); then recommend_improvements and propose its items to the user.`, req), nil
	})

	r.addPrompt(&sdk.Prompt{
		Name:        "test_deployed_service",
		Title:       "Test a deployed service",
		Description: "Load-test an already-deployed service (HTTP API, database, cache, AWS account) through saved connections, with explicit user confirmation.",
		Arguments: []*sdk.PromptArgument{
			{Name: "connection", Description: "saved connection ID or name (default: ask the user or create one)"},
		},
	}, func(a map[string]string) (string, error) {
		conn := strings.TrimSpace(a["connection"])
		first := "1. list_connections and ask the user which service to test; if it is not saved yet, ask for its details and create_connection (only ask for secrets the user chooses to give)."
		if conn != "" {
			first = fmt.Sprintf("1. get_connection %q and show the user what it points to.", conn)
		}
		return first + `
2. Explain that this sends real traffic to a service outside the local sandbox and ask the user to confirm before any request. Do not continue without an explicit yes.
3. test_connection to check it is reachable.
4. list_connection_targets (for AWS this makes read-only calls to the real account; it is covered by the confirmation) and agree on a target with the user.
5. run_connection_load_test with preset smoke and confirm_external=true. Only run heavier presets (load, stress, spike, soak) if the user asks for them and confirms again, since they can affect production.
6. Report latency, throughput, error rate and threshold results, and any risk you noticed.`, nil
	})
}

func required(args map[string]string, name string) (string, error) {
	v := strings.TrimSpace(args[name])
	if v == "" {
		return "", fmt.Errorf("argument %q is required", name)
	}
	return v, nil
}

func workflowOrCurrent(ref string) string {
	if ref = strings.TrimSpace(ref); ref != "" {
		return ref
	}
	return currentWorkflow
}
