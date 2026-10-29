package patterns

func resilience() []Pattern {
	return []Pattern{
		{
			ID:      "circuit-breaker",
			Name:    "Circuit breaker",
			Kind:    KindPattern,
			Problem: "Calls to a failing dependency keep timing out, exhausting threads and connections and cascading the failure upstream.",
			UseWhen: []string{
				"synchronous calls to remote services or third-party APIs that can fail for extended periods",
				"microservice call chains",
			},
			AvoidWhen: []string{
				"in-process calls or local resources",
				"as a substitute for handling business exceptions",
			},
			Tradeoffs: []string{
				"fails fast and gives dependencies room to recover",
				"tuning thresholds is tricky; callers need a fallback path",
			},
			Practices: []string{
				"closed → open on error rate/consecutive failures, half-open probes before closing",
				"combine with timeouts and bounded retries; never retry inside an open circuit",
				"meaningful fallback (cached data, degraded response, queued request)",
				"emit metrics and alerts on state changes",
				"implement in a library (resilience4j, Polly) or a service mesh/sidecar (Envoy outlier detection)",
			},
			Blocks: []Block{
				aws("Implemented in the client SDK/library; Step Functions Retry/Catch with DynamoDB-held circuit state for serverless flows.", "aws_sfn_state_machine", "aws_dynamodb_table", "aws_lambda_function"),
				azure("Polly in the app, or API Management circuit-breaker settings on backends.", "azurerm_api_management", "azurerm_container_app"),
				gcp("Library in Cloud Run services; outlier detection on backend services.", "google_cloud_run_v2_service", "google_compute_backend_service"),
				oci("Library in the service; load balancer health checks remove failing backends.", "oci_load_balancer_load_balancer"),
				docker("Envoy (outlier detection) or a gateway such as Kong in front of upstream services.", "envoy", "kong"),
				kube("Library in Deployments, or a service mesh sidecar with outlier detection.", "Deployment", "Service"),
			},
			Related:  []string{"retry", "bulkhead", "microservices", "health-endpoint-monitoring"},
			Keywords: []string{"circuit breaker", "cortocircuito", "fail fast", "cascading failure", "fallo en cascada", "third party api", "api externa", "terceros", "resilience", "resiliencia", "fault tolerance", "tolerancia a fallos"},
			Sources: []Source{
				azP("Circuit Breaker pattern", "circuit-breaker"),
				awsP("Circuit breaker pattern", "circuit-breaker.html"),
				msioP("Circuit Breaker", "reliability/circuit-breaker.html"),
			},
		},
		{
			ID:      "retry",
			Name:    "Retry with backoff",
			Kind:    KindPattern,
			Problem: "Transient faults (throttling, brief network failures, failovers) make otherwise-healthy calls fail.",
			UseWhen: []string{
				"any call to a remote service, database or cloud API",
				"throttled APIs that return 429/503",
			},
			AvoidWhen: []string{
				"non-transient errors (validation, authorization) — retrying only adds load",
				"non-idempotent operations without an idempotency key",
			},
			Tradeoffs: []string{
				"absorbs transient faults transparently",
				"retry storms amplify outages; retries add latency",
			},
			Practices: []string{
				"exponential backoff with full jitter and a capped number of attempts",
				"retry only idempotent operations or send an idempotency key",
				"retry at one layer only to avoid multiplicative retries",
				"respect Retry-After; overall deadline/timeout per request",
				"log and meter retries; failing messages go to a DLQ",
			},
			Blocks: []Block{
				aws("AWS SDK retry modes; Lambda async retries + on-failure destinations; Step Functions Retry with BackoffRate and jitter; SQS redrive.", "aws_lambda_function", "aws_sfn_state_machine", "aws_sqs_queue", "aws_sqs_queue_redrive_policy"),
				azure("Azure SDK retry policies; Service Bus max delivery count and DLQ.", "azurerm_servicebus_queue", "azurerm_container_app"),
				gcp("Client library retries; Pub/Sub retry policy with exponential backoff and dead-letter topics; Cloud Tasks retry config.", "google_pubsub_subscription", "google_cloud_tasks_queue"),
				oci("SDK retry configuration; Queue max delivery attempts.", "oci_queue_queue"),
				docker("Retries in the client; RabbitMQ dead-letter exchanges for failed messages.", "rabbitmq"),
				kube("Job backoffLimit; liveness/readiness probes; retries in clients or mesh.", "Job", "Deployment"),
			},
			Related:  []string{"circuit-breaker", "competing-consumers", "saga"},
			Keywords: []string{"retry", "retries", "reintentos", "reintentar", "backoff", "transient", "transitorio", "throttling", "429", "flaky", "intermitente"},
			Sources: []Source{
				azP("Retry pattern", "retry"),
				awsP("Retry with backoff pattern", "retry-backoff.html"),
			},
		},
		{
			ID:      "bulkhead",
			Name:    "Bulkhead",
			Kind:    KindPattern,
			Problem: "One overloaded dependency or noisy tenant exhausts shared resources and takes the whole service down.",
			UseWhen: []string{
				"a service calls several dependencies with different reliability",
				"noisy-neighbor isolation between tenants or workloads",
				"critical and non-critical traffic share infrastructure",
			},
			AvoidWhen: []string{
				"small services with a single dependency",
			},
			Tradeoffs: []string{
				"failures stay contained",
				"less efficient resource use; more pools to size",
			},
			Practices: []string{
				"separate connection/thread pools per dependency",
				"separate queues, consumers or deployments for critical versus batch work",
				"resource requests/limits and quotas per workload",
				"Lambda reserved concurrency per function",
			},
			Blocks: []Block{
				aws("Separate Lambda reserved concurrency, ECS services or SQS queues per workload.", "aws_lambda_function", "aws_ecs_service", "aws_sqs_queue"),
				azure("Separate Container Apps or node pools per workload.", "azurerm_container_app", "azurerm_kubernetes_cluster"),
				gcp("Separate Cloud Run services with max instances and concurrency limits.", "google_cloud_run_v2_service"),
				oci("Separate function applications or instance pools.", "oci_functions_application", "oci_core_instance_pool"),
				docker("Separate services with cpu/memory limits in compose.", "nginx"),
				kube("Separate Deployments with resource limits, ResourceQuota per namespace, PodDisruptionBudgets.", "Deployment", "PodDisruptionBudget", "HorizontalPodAutoscaler"),
			},
			Related:  []string{"circuit-breaker", "deployment-stamps", "rate-limiting"},
			Keywords: []string{"bulkhead", "isolation", "aislamiento", "aislar", "noisy neighbor", "vecino ruidoso", "contain failures", "contener fallos"},
			Sources: []Source{
				azP("Bulkhead pattern", "bulkhead"),
			},
		},
		{
			ID:      "rate-limiting",
			Name:    "Rate limiting and throttling",
			Kind:    KindPattern,
			Problem: "Unbounded clients, abuse or traffic spikes exceed a system's capacity or a downstream quota.",
			UseWhen: []string{
				"public APIs, multi-tenant SaaS, per-plan quotas",
				"protecting a backend or third-party quota",
				"defending against scraping and credential stuffing",
			},
			AvoidWhen: []string{
				"internal batch flows better served by a queue (load leveling)",
			},
			Tradeoffs: []string{
				"protects availability and enforces fair use",
				"legitimate clients get 429s; limits need tuning per tier",
			},
			Practices: []string{
				"throttle at the edge (API gateway usage plans, WAF rate-based rules)",
				"return 429 with Retry-After; clients back off with jitter",
				"per-client keys (API key, tenant, IP) with token-bucket limits",
				"queue work that can wait instead of rejecting it",
			},
			Blocks: []Block{
				aws("API Gateway stage throttling and usage plans; WAF rate-based rules on CloudFront/ALB/API Gateway.", "aws_api_gateway_rest_api", "aws_api_gateway_usage_plan", "aws_wafv2_web_acl"),
				azure("API Management rate-limit-by-key policies and products; Front Door WAF rate-limit rules.", "azurerm_api_management", "azurerm_api_management_product", "azurerm_cdn_frontdoor_firewall_policy"),
				gcp("Cloud Armor rate-based rules on backend services.", "google_compute_security_policy", "google_compute_backend_service"),
				oci("API Gateway rate limiting policy on deployments; WAF rate limiting.", "oci_apigateway_deployment", "oci_waf_web_app_firewall_policy"),
				docker("Rate-limiting plugin in Kong/Traefik/NGINX in front of services.", "kong", "traefik", "nginx"),
				kube("Ingress controller or Gateway rate-limit annotations/policies.", "Ingress", "Gateway", "HTTPRoute"),
			},
			Related:  []string{"api-gateway", "gateway-offloading", "queue-based-load-leveling", "bulkhead"},
			Keywords: []string{"rate limit", "rate limiting", "limite de peticiones", "limitar peticiones", "throttling", "quota", "cuota", "abuse", "abuso", "public api", "api publica", "ddos", "high traffic", "alto trafico"},
			Sources: []Source{
				azP("Rate Limiting pattern", "rate-limiting-pattern"),
				azP("Throttling pattern", "throttling"),
			},
		},
		{
			ID:      "health-endpoint-monitoring",
			Name:    "Health endpoint monitoring",
			Kind:    KindPattern,
			Problem: "Load balancers, orchestrators and operators need to know whether an instance can serve traffic.",
			UseWhen: []string{
				"every service behind a load balancer or orchestrator",
				"external uptime monitoring of public endpoints",
			},
			AvoidWhen: []string{
				"never skip it for services that take traffic; keep it simple for tiny apps",
			},
			Tradeoffs: []string{
				"automatic removal of unhealthy instances and faster detection",
				"deep checks can cascade failures if they call every dependency",
			},
			Practices: []string{
				"separate liveness (process alive) from readiness (can serve; dependencies reachable)",
				"cheap, unauthenticated-but-uninformative endpoint; no secrets in responses",
				"external synthetic checks from several locations with alerting",
			},
			Blocks: []Block{
				aws("ALB target group health checks, Route 53 health checks, CloudWatch alarms.", "aws_lb_target_group", "aws_route53_health_check", "aws_cloudwatch_metric_alarm"),
				azure("Container Apps probes, metric alerts, Application Insights availability tests.", "azurerm_container_app", "azurerm_monitor_metric_alert", "azurerm_application_insights_standard_web_test"),
				gcp("Cloud Run probes, uptime checks and alert policies.", "google_cloud_run_v2_service", "google_monitoring_uptime_check_config", "google_monitoring_alert_policy"),
				oci("Load balancer health checks, Health Checks monitors and alarms.", "oci_load_balancer_load_balancer", "oci_health_checks_http_monitor", "oci_monitoring_alarm"),
				docker("Compose healthchecks with depends_on: service_healthy; Uptime Kuma or Prometheus blackbox for monitoring.", "uptime-kuma", "prometheus"),
				kube("livenessProbe/readinessProbe/startupProbe on Deployments behind Services.", "Deployment", "Service"),
			},
			Related:   []string{"circuit-breaker", "n-tier"},
			Templates: []string{"aws-load-balancer", "docker-load-balancer", "k8s-load-balancer"},
			Keywords:  []string{"health check", "healthcheck", "health checks", "salud", "monitoring", "monitoreo", "uptime", "disponibilidad", "availability", "probes", "sondas"},
			Sources: []Source{
				azP("Health Endpoint Monitoring pattern", "health-endpoint-monitoring"),
				msioP("Health Check API", "observability/health-check-api.html"),
			},
		},
	}
}
