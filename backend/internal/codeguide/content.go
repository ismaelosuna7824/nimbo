package codeguide

import "github.com/ismaelosuna7824/nimbo/backend/internal/model"

// Principles, layouts, practices, compute options and sources. Claims follow
// the sources listed at the end; Floci support (Local) matches Nimbo's
// catalog (tested).

func principles(o Options) []string {
	p := []string{
		"Application code lives in services/<svc>/ asset folders (one folder per deployable), specs in openspec/; never put code inline in Terraform (archive_file source blocks, heredocs). Terraform references the folder (source_dir) or the image built from it.",
		"Building from zero: propose the simplest compute and style that meet the requirement (see compute: functions for bursty short requests, managed containers for steady or long-running HTTP, Kubernetes when the team already runs it, VMs for special OS or licensing needs) and say what you left out. Extending an existing design: do only what was asked; offer improvements as an optional list.",
	}
	if o.Mode == ModeArchitecture {
		return append(p,
			"Architecture mode: no business code. Ship only the smoke handler: GET /health returns 200 and GET /smoke writes and reads back a unique value on every connected store (publish/consume for queues and topics), configured through env vars set by Terraform. Done means: deployed, /health 200 and /smoke 200 with every check ok.",
			"Smoke handlers are JavaScript (or Python when asked) on every platform because they need no build; the application language matters only in full mode.",
			"Grant the compute identity exactly the store actions the smoke test uses (put/get, send/receive, publish); keep the same least-privilege grants when full-mode code replaces the handler.",
		)
	}
	p = append(p,
		"Dependency rule: source dependencies point inward. domain imports nothing from application, adapters, SDKs, HTTP or the database; application imports only domain; adapters import both. (Clean Architecture, Hexagonal Architecture)",
		"Ports are interfaces owned by the application (e.g. ItemRepository). Add one only at a real seam: an external system, a second implementation, or a test double that matters; otherwise call the code directly. (AWS hexagonal guidance)",
		"One composition root per deployable (the handler or main file): the only place that reads env vars, creates SDK clients and wires adapters to use cases.",
		"Value objects validate on construction so invalid states cannot exist (ItemName); entities carry identity; data crossing the boundary is plain views, never entities or DB rows.",
		"Typed domain errors (invalid, not_found, conflict) are raised in domain/application and mapped to HTTP status codes in the driving adapter only; unknown errors become a generic 500 and are logged.",
		"Use cases are small and named by intent (CreateItem, CapturePayment), one public operation each; comments explain why, not what.",
		"Writes are idempotent: client- or deterministic IDs plus a conditional insert (attribute_not_exists, ON CONFLICT DO NOTHING) so retries never duplicate.",
		"Configuration comes from the environment (12-factor); secrets come from the platform's secret store at startup, never from code, images, Terraform defaults or logs.",
	)
	switch o.Style {
	case StyleModularMonolith:
		p = append(p,
			"Modular monolith: one deployable (one service folder), one module per bounded context under src/modules/<context>/ with its own domain/application/adapters; modules talk through their application API or in-process domain events, never through each other's tables.",
			"Keep each context's data in its own tables/collections; when a module is split out later, its events move to a broker unchanged. (Monolith First)",
			"Domain events between modules: publish after commit; when they must survive a crash, write them to an outbox table in the same transaction.",
		)
	case StyleMicroservices:
		p = append(p,
			"Microservices: one service folder per bounded context, each with its own datastore; justify the split by independent deploy/scale needs and the team's operational maturity (rapid provisioning, monitoring, rapid deployment). (Microservice Prerequisites)",
			"Cross-service events use the transactional outbox (event row written in the same DB transaction, relayed to the broker) and consumers are idempotent because delivery is at least once.",
			"A business transaction spanning services is a saga (orchestration by default) with compensating actions; no distributed transactions.",
			"Every service publishes a contract (OpenAPI or event schema) and runs consumer contract tests in CI.",
		)
	default:
		p = append(p,
			"Single service, hexagonal-lite: domain, application and adapters folders, one repository port, no aggregates hierarchy, no event bus and no CQRS. Add a domain event only when a second consumer exists.",
		)
	}
	return append(p,
		"Event sourcing and CQRS only for the aggregate whose requirement needs full history, temporal queries or very different read models; sagas only for cross-service transactions.",
	)
}

func layout(o Options) []Folder {
	svc := "services/" + serviceName(o.Style) + "/"
	common := []Folder{
		{Path: "openspec/", Purpose: "Specs: openspec/specs/<capability>/spec.md (current behaviour) and openspec/changes/<change>/ (proposal, design, tasks, delta specs)."},
	}
	if o.Mode == ModeArchitecture {
		return append(common,
			Folder{Path: svc, Purpose: "The smoke handler: one per deployable compute in the diagram (copy the folder per service)."},
			Folder{Path: svc + "src/", Purpose: "Handler source; Terraform zips it (functions) or the Dockerfile copies it (containers, VMs)."},
		)
	}
	src := map[string]string{
		"typescript": "src/", "javascript": "src/", "python": "src/app/", "go": "internal/", "java": "src/main/java/com/example/api/", "csharp": "src/Api/",
	}[o.Language]
	if src == "" {
		src = "src/"
	}
	f := append(common, Folder{Path: svc, Purpose: "One deployable: its code, tests, Dockerfile and dependency manifest."})
	switch o.Style {
	case StyleModularMonolith:
		f = append(f,
			Folder{Path: svc + src + "modules/<context>/domain/", Purpose: "Per bounded context: entities, value objects, domain events and errors (pure)."},
			Folder{Path: svc + src + "modules/<context>/application/", Purpose: "Per context: use cases and the ports they own; the module's public API."},
			Folder{Path: svc + src + "modules/<context>/adapters/", Purpose: "Per context: repositories (its own tables), publishers, inbound HTTP routes."},
			Folder{Path: svc + src + "shared/", Purpose: "Only cross-cutting kernel code (IDs, clock, in-process event bus, outbox relay); no business rules."},
		)
	default:
		f = append(f,
			Folder{Path: svc + src + "domain/", Purpose: "Entities, value objects and typed domain errors (pure)."},
			Folder{Path: svc + src + "application/", Purpose: "Use cases and the ports they own."},
			Folder{Path: svc + src + "adapters/", Purpose: "Driving (HTTP, queue consumer) and driven (repository, publisher) adapters."},
		)
		if o.Style == StyleMicroservices {
			f = append(f,
				Folder{Path: "services/<context>/", Purpose: "Repeat this service per bounded context, each with its own datastore, outbox table and contract tests."},
				Folder{Path: "services/<context>/contracts/", Purpose: "OpenAPI documents and event schemas the service publishes; consumers test against them."},
			)
		}
	}
	return append(f,
		Folder{Path: svc + "dist/", Purpose: "Build output of the Dockerfile's package stage (generated: never edit, never export)."},
	)
}

func testPractices(o Options) []string {
	if o.Mode == ModeArchitecture {
		return []string{
			"Acceptance: after apply, GET /health returns 200 and GET /smoke returns 200 with every configured check \"ok\".",
			"A failing check names the store and logs the SDK error with the request id: fix the env var, the IAM grant or the network path, not the handler.",
		}
	}
	t := []string{
		"Domain unit tests without mocks (value objects, invariants); use-case tests through the in-memory repository adapter.",
		"One integration test per driven adapter against the local emulator or a test container (DynamoDB on Floci, Postgres container).",
		"One smoke test per deployed route after apply (health plus one write/read).",
		"Each spec scenario (GIVEN/WHEN/THEN) maps to at least one test; the build stage runs the tests, so a failing test fails the build.",
		"No tests for trivial getters; no snapshot tests of JSON the client doesn't depend on.",
	}
	if o.Style == StyleMicroservices {
		t = append(t, "Consumer-driven contract tests for every API and event a service consumes; idempotency test: the same message twice changes state once.")
	}
	return t
}

var security = []string{
	"OWASP API1/API5: check that the caller owns each object ID and may call each function, in the use case, server-side.",
	"OWASP API3: accept only whitelisted fields (no mass assignment); return views, not entities.",
	"OWASP API4: bound body size, page size, timeouts and concurrency (the skeleton rejects bodies over 16 KiB).",
	"OWASP API8: generic error bodies (no stack traces), explicit CORS, no default credentials.",
	"OWASP API7/API10: validate URLs you fetch and every third-party response before use.",
	"Secrets come from the secret store (Secrets Manager, Key Vault, Secret Manager, OCI Vault, Kubernetes Secrets) through the compute identity; never in code, images, env defaults or logs.",
	"Least privilege: the function/task/VM identity gets only the actions on the resources it uses.",
	"Containers run as a non-root user from a minimal runtime image.",
}

func observability(platform string) []string {
	logs := map[string]string{
		"aws": "CloudWatch Logs (set retention on the log group)", "azure": "Application Insights / Log Analytics",
		"gcp": "Cloud Logging", "oci": "OCI Logging", "docker": "docker compose logs", "kubernetes": "kubectl logs / the cluster's log stack",
	}[platform]
	return []string{
		"Structured JSON logs, one line per request with the correlation id (platform request id or X-Request-Id, echoed back) and the status; shipped to " + logs + ".",
		"GET /health answers 200 without touching dependencies (liveness); /smoke or a readiness check exercises them.",
		"Metrics: request count, errors and latency per route; alert on the error rate.",
		"Propagate trace context (W3C traceparent) through HTTP calls and message attributes; OpenTelemetry is the vendor-neutral SDK.",
	}
}

var specGuide = SpecGuide{
	Layout: []Folder{
		{Path: "openspec/specs/<capability>/spec.md", Purpose: "Source of truth: current behaviour as requirements with scenarios."},
		{Path: "openspec/changes/<change-id>/proposal.md", Purpose: "Why and what: problem, scope, out of scope, the questions asked and their answers."},
		{Path: "openspec/changes/<change-id>/specs/<capability>/spec.md", Purpose: "Delta spec: ADDED / MODIFIED / REMOVED requirements of the change."},
		{Path: "openspec/changes/<change-id>/design.md", Purpose: "How: mode, compute, style, language and why; data ownership; packaging; what was left out."},
		{Path: "openspec/changes/<change-id>/tasks.md", Purpose: "Ordered checklist of implementation tasks, each tied to requirements."},
	},
	Questions: []string{
		"Actors and flows: who calls the system, and what are the main flows step by step (happy path and failures)?",
		"Data: which entities, who owns each, retention, and is any of it personal or sensitive?",
		"Consistency: which operations must be atomic or idempotent; what happens on retries and duplicates?",
		"Integrations: which external systems (payment, email, identity) and what if they are down?",
		"Traffic: expected requests per second, peaks, latency target, and long-running work?",
		"Security: authentication, who may read/change which objects, and compliance needs?",
		"Availability: acceptable downtime and data loss (RTO/RPO); single region?",
		"Team and operations: language the team knows, existing platform (functions, containers, Kubernetes, VMs), who operates it?",
		"Mode: architecture only (smoke handlers) or full application code?",
	},
	Templates: []File{
		{Path: "openspec/changes/<change-id>/proposal.md", Purpose: "Proposal template.", Content: `# Proposal: <change title>

## Why
<problem and who has it>

## What changes
- <capability>: <one line>

## Out of scope
- <deliberately excluded>

## Questions and answers
- Q: <question asked> — A: <user's answer>
- [NEEDS CLARIFICATION: <open question>]
`},
		{Path: "openspec/changes/<change-id>/specs/<capability>/spec.md", Purpose: "Delta spec template (OpenSpec requirement + scenario format).", Content: `# <Capability>

## ADDED Requirements

### Requirement: <name>
The system SHALL <behaviour>.

#### Scenario: <name>
- GIVEN <state>
- WHEN <action>
- THEN <observable result>
`},
		{Path: "openspec/changes/<change-id>/design.md", Purpose: "Design template.", Content: `# Design: <change title>

## Decisions
- Mode: architecture | full
- Compute: <function | container | Kubernetes | VM> on <platform> — why: <traffic, latency, ops>
- Style: service | modular monolith | microservices — why
- Language: <language> — why (or "chosen by the user")

## Components and data ownership
- <service/module>: owns <data>; exposes <API/events>

## Packaging
- services/<svc>/: <zip of src | package stage into dist | container image>

## Left out on purpose
- <pattern or service> — <why not needed now>
`},
		{Path: "openspec/changes/<change-id>/tasks.md", Purpose: "Tasks template.", Content: `# Tasks

## 1. Infrastructure
- [ ] 1.1 <resource or wiring> (Requirement: <name>)

## 2. Code
- [ ] 2.1 <use case / adapter> with its tests (Scenario: <name>)

## 3. Verify
- [ ] 3.1 Deploy, /health 200, smoke or scenario tests pass
`},
	},
	Gate: []string{
		"No design, Terraform or code while any [NEEDS CLARIFICATION] marker remains in the change's files.",
		"Every question asked has its answer recorded in proposal.md.",
		"Every requirement has at least one GIVEN/WHEN/THEN scenario; specs describe behaviour, not technology.",
		"design.md records mode, compute, style and language with a reason each, plus what was left out.",
		"Ask open questions in one batch, not one at a time; when the user says to proceed with assumptions, record each assumption as an answer.",
	},
}

func languageOptions(o Options) []LanguageOption {
	opts := []LanguageOption{
		{ID: "typescript", Name: "TypeScript (Node.js)", Why: "Types over the JavaScript ecosystem, fast cold starts, one language with a web front end.", When: "The team writes JavaScript/TypeScript; I/O-bound APIs and event handlers. Needs a build (esbuild bundle)."},
		{ID: "javascript", Name: "JavaScript (Node.js, ESM)", Why: "No build step: the source folder deploys as-is; the AWS SDK v3 is in the Lambda runtime.", When: "Small services and glue code where a build is not worth it."},
		{ID: "python", Name: "Python", Why: "Simple, readable, no build for pure-Python code; boto3 is in the Lambda runtime; strongest data/ML ecosystem.", When: "Data processing, ML-adjacent services, scripting-heavy teams. Native dependencies need a container or a build."},
		{ID: "go", Name: "Go", Why: "Single static binary, tiny cold starts and memory, strong concurrency.", When: "Performance-sensitive or high-throughput services, small images, teams that value simplicity."},
		{ID: "java", Name: "Java", Why: "Mature enterprise and DDD ecosystem, strong typing and tooling.", When: "Enterprise teams with Java skills; long-running containers. Heavier cold starts on functions: prefer containers or Lambda SnapStart."},
		{ID: "csharp", Name: "C# (.NET)", Why: "Rich DDD/CQRS guidance and libraries, strong typing, good performance.", When: ".NET teams; containers or the managed dotnet runtime. Heavier cold starts than Node/Go/Python on functions."},
	}
	if o.Target == TargetFunction && o.Platform != "aws" {
		// Function skeletons on Azure/GCP/OCI exist for these three.
		var keep []LanguageOption
		for _, l := range opts {
			for _, id := range nonAWSFunctionLanguages {
				if l.ID == id {
					keep = append(keep, l)
				}
			}
		}
		return keep
	}
	return opts
}

const (
	runsLocally  = "runs locally"
	controlPlane = "deploys locally, workloads do not run (export for runtime behaviour)"
	unverified   = "deploys locally, workloads not verified"
	exportOnly   = "export only"
)

var compute = map[string][]ComputeOption{
	"aws": {
		{ID: "lambda", Name: "AWS Lambda + API Gateway", Target: TargetFunction, TerraformTypes: []string{"aws_lambda_function", "aws_apigatewayv2_api"},
			When: "Bursty or low traffic, event-driven work, requests under 15 minutes, pay per use, minimal operations.", AvoidWhen: "Steady high throughput (cost), long-running or connection-heavy work, strict latency with heavy runtimes.", Local: runsLocally},
		{ID: "ecs-fargate", Name: "Amazon ECS on Fargate", Target: TargetContainer, TerraformTypes: []string{"aws_ecs_cluster", "aws_ecs_task_definition", "aws_ecs_service"},
			When: "Steady HTTP traffic, long-running processes, any language/runtime, no servers to manage.", AvoidWhen: "Tiny or very spiky workloads where functions cost less; need for host-level control.", Local: runsLocally},
		{ID: "eks", Name: "Amazon EKS", Target: TargetContainer, TerraformTypes: []string{"aws_eks_cluster", "aws_eks_node_group"},
			When: "The team already runs Kubernetes, many services, portability across clouds.", AvoidWhen: "One or two services or a team without Kubernetes operations experience.", Local: unverified},
		{ID: "ec2-asg", Name: "EC2 Auto Scaling group behind a load balancer", Target: TargetVM, TerraformTypes: []string{"aws_launch_template", "aws_autoscaling_group", "aws_lb"},
			When: "Special OS, agents or licensing needs, lift-and-shift, steady load with reserved capacity.", AvoidWhen: "Teams that don't want to patch and operate servers.", Local: unverified},
	},
	"azure": {
		{ID: "functions", Name: "Azure Functions (Linux)", Target: TargetFunction, TerraformTypes: []string{"azurerm_service_plan", "azurerm_linux_function_app"},
			When: "Bursty or event-driven work with Azure triggers, pay per use.", AvoidWhen: "Steady high throughput, long-running work, a need to run it locally in Nimbo.", Local: exportOnly},
		{ID: "container-apps", Name: "Azure Container Apps", Target: TargetContainer, TerraformTypes: []string{"azurerm_container_app_environment", "azurerm_container_app"},
			When: "HTTP APIs and workers in containers with scale-to-zero, no cluster to run.", AvoidWhen: "Need for full Kubernetes APIs or host control.", Local: controlPlane},
		{ID: "aks", Name: "Azure Kubernetes Service", Target: TargetContainer, TerraformTypes: []string{"azurerm_kubernetes_cluster"},
			When: "The team already runs Kubernetes, many services.", AvoidWhen: "Few services or no Kubernetes operations experience.", Local: unverified},
		{ID: "vmss", Name: "VM scale set behind a load balancer", Target: TargetVM, TerraformTypes: []string{"azurerm_linux_virtual_machine_scale_set", "azurerm_lb"},
			When: "Special OS, agents or licensing needs, lift-and-shift.", AvoidWhen: "Teams that don't want to patch and operate servers.", Local: exportOnly},
	},
	"gcp": {
		{ID: "cloud-functions", Name: "Cloud Functions (2nd gen)", Target: TargetFunction, TerraformTypes: []string{"google_cloudfunctions2_function", "google_storage_bucket_object"},
			When: "Bursty or event-driven work with Eventarc/Pub/Sub triggers, pay per use.", AvoidWhen: "Long-running work, a need to run it locally in Nimbo (use Cloud Run instead).", Local: exportOnly},
		{ID: "cloud-run", Name: "Cloud Run", Target: TargetContainer, TerraformTypes: []string{"google_cloud_run_v2_service"},
			When: "HTTP APIs and workers in containers with scale-to-zero; the default compute on GCP and it runs locally.", AvoidWhen: "Need for host control or non-HTTP long-lived protocols.", Local: runsLocally},
		{ID: "gke", Name: "Google Kubernetes Engine", Target: TargetContainer, TerraformTypes: []string{"google_container_cluster", "google_container_node_pool"},
			When: "The team already runs Kubernetes, many services.", AvoidWhen: "Few services or no Kubernetes operations experience.", Local: unverified},
		{ID: "mig", Name: "Managed instance group behind a load balancer", Target: TargetVM, TerraformTypes: []string{"google_compute_instance_template", "google_compute_region_instance_group_manager"},
			When: "Special OS, agents or licensing needs, lift-and-shift.", AvoidWhen: "Teams that don't want to patch and operate servers.", Local: exportOnly},
	},
	"oci": {
		{ID: "functions", Name: "OCI Functions", Target: TargetFunction, TerraformTypes: []string{"oci_functions_application", "oci_functions_function"},
			When: "Event-driven or bursty work behind OCI API Gateway; functions are container images built with the Fn FDK.", AvoidWhen: "Long-running work or steady high throughput, or a need to run it locally in Nimbo (floci-oci doesn't serve the Functions API oracle/oci 9.3+ calls).", Local: exportOnly},
		{ID: "container-instances", Name: "OCI Container Instances", Target: TargetContainer, TerraformTypes: []string{"oci_container_instances_container_instance"},
			When: "A few containers without a cluster.", AvoidWhen: "Autoscaling fleets (use OKE) or a need to run it locally in Nimbo.", Local: exportOnly},
		{ID: "oke", Name: "Oracle Container Engine for Kubernetes", Target: TargetContainer, TerraformTypes: []string{"oci_containerengine_cluster", "oci_containerengine_node_pool"},
			When: "The team already runs Kubernetes, many services; the local container default on OCI.", AvoidWhen: "Few services or no Kubernetes operations experience.", Local: unverified},
		{ID: "instance-pool", Name: "Instance pool behind a load balancer", Target: TargetVM, TerraformTypes: []string{"oci_core_instance_configuration", "oci_core_instance_pool", "oci_load_balancer_load_balancer"},
			When: "Special OS, agents or licensing needs, lift-and-shift.", AvoidWhen: "Teams that don't want to patch and operate servers.", Local: exportOnly},
	},
	"docker": {
		{ID: "compose", Name: "Docker Compose service", Target: TargetContainer,
			When: "Local development and single-host deployments; Nimbo builds services from their Dockerfile.", AvoidWhen: "Multi-host scaling or self-healing across machines.", Local: runsLocally},
	},
	"kubernetes": {
		{ID: "deployment", Name: "Kubernetes Deployment + Service + HorizontalPodAutoscaler", Target: TargetContainer,
			When: "Many services, autoscaling, rolling updates; Nimbo builds images whose repository names a services/<svc> folder and loads them into local k3s.", AvoidWhen: "One small service where Compose or a managed container service is simpler.", Local: runsLocally},
	},
}

func computeOptions(platform string) []ComputeOption { return compute[platform] }

// sandboxAccess tells how code running in the local sandbox reaches the
// emulated services, per platform. Verified on floci 2.3.0 (AWS) and
// floci-gcp 0.11.0 by running code in Lambda, ECS and Cloud Run
// (2026-10-28): the env each one gets and which hosts answer from inside.
var sandboxAccess = map[string][]string{
	"aws": {
		"Lambda functions and ECS tasks get AWS_ENDPOINT_URL=http://localhost.floci.io:4566 (inside them localhost.floci.io is the sandbox), AWS_REGION and test credentials from the emulator: SDK clients built with the default configuration reach the emulated services, so set no endpoint in code or Terraform (verified with boto3 in Lambda and the AWS SDK for Go v2 in ECS: SQS, S3, DynamoDB).",
		"S3: use path-style addressing when AWS_ENDPOINT_URL is set. SQS: queue URLs from Terraform read http://localhost:4566/...; send to the SDK's endpoint, not the URL's host (JavaScript SDK v3: useQueueUrlAsEndpoint: false; boto3 and the Go SDK v2 already do).",
	},
	"gcp": {
		"Cloud Run containers get only PORT and K_SERVICE/K_REVISION/K_CONFIGURATION from the emulator, and localhost:4588 or localhost.floci.io:4588 inside them is the container itself. Declare variable \"" + model.GCPEmulatorHostVar + "\" (default \"\") and, while it is set, pass PUBSUB_EMULATOR_HOST = var." + model.GCPEmulatorHostVar + " and STORAGE_EMULATOR_HOST = \"http://${var." + model.GCPEmulatorHostVar + "}\" (packaging shows it): Nimbo's sandbox sets it to the emulator on the sandbox network (nimbo-<workflow id>-pod:4588); on Google Cloud it stays empty. The Pub/Sub and Cloud Storage client libraries then switch to the emulator by themselves (verified with the Go clients: publish and pull, write and read).",
		"Pass GOOGLE_CLOUD_PROJECT (the resources' project, data.google_client_config.current.project): there is no metadata server locally.",
		"Cloud SQL runs a real PostgreSQL or MySQL container per instance: connect with a normal driver to google_sql_database_instance.<name>.first_ip_address:5432 with the google_sql_user and google_sql_database Terraform creates (verified with pgx from Cloud Run). On Google Cloud that is the instance's IP: give it a private IP and the service Direct VPC egress. Creating google_sql_database locally needs hashicorp/google >= 7.26.",
		"The sandbox's Cloud Run rejects env value_source (Secret Manager references): pass a secret as a plain value only while " + model.GCPEmulatorHostVar + " is set, and value_source on Google Cloud (packaging shows it for DATABASE_URL).",
	},
	"azure": {
		"No Azure code runs in the sandbox: Function Apps are export only and Container Apps deploy without running their containers. Read every endpoint and connection string from env vars and prove the code on the docker platform.",
	},
	"oci": {
		"OCI Functions and OKE workloads are not verified to reach the emulator from inside the sandbox, and Container Instances are export only: read every endpoint from env vars and prove the code on the docker platform.",
	},
	"docker": {
		"Compose services reach each other by service name on a shared network (postgres://app@db:5432/app); publish only the ports you call from your computer.",
	},
	"kubernetes": {
		"Pods reach each other through Services (http://<service>.<namespace>.svc:<port>); kubectl port-forward (or the workflow's forwarded ports) reaches them from your computer.",
	},
}

var sources = []Source{
	{"Hexagonal architecture (Alistair Cockburn)", "https://alistair.cockburn.us/hexagonal-architecture"},
	{"The Clean Architecture (Robert C. Martin)", "https://blog.cleancoder.com/uncle-bob/2012/08/13/the-clean-architecture.html"},
	{"Explicit architecture: DDD, hexagonal, onion, clean, CQRS (Herberto Graça)", "https://herbertograca.com/2017/11/16/explicit-architecture-01-ddd-hexagonal-onion-clean-cqrs-how-i-put-it-all-together/"},
	{"Hexagonal architecture pattern (AWS Prescriptive Guidance)", "https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/hexagonal-architecture.html"},
	{"Bounded Context (Martin Fowler)", "https://martinfowler.com/bliki/BoundedContext.html"},
	{"Monolith First (Martin Fowler)", "https://martinfowler.com/bliki/MonolithFirst.html"},
	{"Microservice Prerequisites (Martin Fowler)", "https://martinfowler.com/bliki/MicroservicePrerequisites.html"},
	{"Event Sourcing (Martin Fowler)", "https://martinfowler.com/eaaDev/EventSourcing.html"},
	{"DDD and CQRS patterns for microservices (Microsoft)", "https://learn.microsoft.com/en-us/dotnet/architecture/microservices/microservice-ddd-cqrs-patterns/"},
	{"Transactional outbox (microservices.io)", "https://microservices.io/patterns/data/transactional-outbox.html"},
	{"Saga (microservices.io)", "https://microservices.io/patterns/data/saga.html"},
	{"OWASP API Security Top 10 (2023)", "https://owasp.org/API-Security/editions/2023/en/0x11-t10/"},
	{"The Twelve-Factor App: Config", "https://12factor.net/config"},
	{"OpenTelemetry documentation", "https://opentelemetry.io/docs/"},
	{"OpenSpec concepts", "https://github.com/Fission-AI/OpenSpec/blob/main/docs/concepts.md"},
	{"Spec Kit: spec-driven development", "https://github.com/github/spec-kit/blob/main/spec-driven.md"},
	{"Lambda runtimes", "https://docs.aws.amazon.com/lambda/latest/dg/lambda-runtimes.html"},
	{"Building Lambda functions with Node.js (SDK v3 in the runtime)", "https://docs.aws.amazon.com/lambda/latest/dg/lambda-nodejs.html"},
	{"Node.js Lambda .zip packages (bundling)", "https://docs.aws.amazon.com/lambda/latest/dg/nodejs-package.html"},
	{"Python Lambda .zip packages", "https://docs.aws.amazon.com/lambda/latest/dg/python-package.html"},
	{"Go Lambda .zip packages (bootstrap, lambda.norpc)", "https://docs.aws.amazon.com/lambda/latest/dg/golang-package.html"},
	{"Java Lambda packages (lib/ layout, Shade)", "https://docs.aws.amazon.com/lambda/latest/dg/java-package.html"},
	{"C# Lambda .zip packages", "https://docs.aws.amazon.com/lambda/latest/dg/csharp-package.html"},
	{"Azure Functions Node.js developer guide (model v4)", "https://learn.microsoft.com/en-us/azure/azure-functions/functions-reference-node"},
	{"Azure Functions Python developer guide (model v2)", "https://learn.microsoft.com/en-us/azure/azure-functions/functions-reference-python"},
	{"Azure Functions zip deployment (remote build)", "https://learn.microsoft.com/en-us/azure/azure-functions/deployment-zip-push"},
	{"Cloud Functions Functions Framework", "https://cloud.google.com/functions/docs/functions-framework"},
	{"What is Cloud Run", "https://cloud.google.com/run/docs/overview/what-is-cloud-run"},
	{"OCI Functions overview", "https://docs.oracle.com/en-us/iaas/Content/Functions/Concepts/functionsoverview.htm"},
	{"Fn FDK for Node.js", "https://github.com/fnproject/fdk-node"},
	{"Fn FDK for Python", "https://github.com/fnproject/fdk-python"},
	{"Docker multi-stage builds", "https://docs.docker.com/build/building/multi-stage/"},
	{"Docker local and tar exporters (--output type=local)", "https://docs.docker.com/build/exporters/local-tar/"},
	{"cloud-init examples", "https://cloudinit.readthedocs.io/en/latest/reference/examples.html"},
}
