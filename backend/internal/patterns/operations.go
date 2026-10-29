package patterns

func operations() []Pattern {
	return []Pattern{
		{
			ID:      "scheduler-agent-supervisor",
			Name:    "Scheduler agent supervisor",
			Kind:    KindPattern,
			Problem: "Multi-step or scheduled distributed work must complete reliably even when individual steps or agents fail.",
			UseWhen: []string{
				"scheduled jobs and workflows touching several remote systems",
				"long-running processes that must resume after failures",
			},
			AvoidWhen: []string{
				"a single idempotent cron job with retries suffices",
			},
			Tradeoffs: []string{
				"reliable completion and visibility into workflow state",
				"state store and supervisor logic add complexity (prefer a managed workflow engine)",
			},
			Practices: []string{
				"durable workflow state; idempotent steps; compensation on failure",
				"supervisor detects stuck steps via timeouts/heartbeats and retries or compensates",
				"use managed orchestrators (Step Functions, Workflows, Temporal) instead of hand-rolled schedulers",
				"alert on failed and timed-out executions",
			},
			Blocks: []Block{
				aws("EventBridge Scheduler triggers Step Functions, which runs and supervises Lambda/ECS steps.", "aws_scheduler_schedule", "aws_sfn_state_machine", "aws_lambda_function"),
				azure("Container Apps jobs on a schedule; Logic Apps or Durable Functions for workflows.", "azurerm_container_app_job", "azurerm_logic_app_workflow"),
				gcp("Cloud Scheduler triggers Workflows or Cloud Run jobs.", "google_cloud_scheduler_job", "google_workflows_workflow", "google_cloud_run_v2_job"),
				oci("Resource Scheduler schedules with Functions.", "oci_resource_scheduler_schedule", "oci_functions_function"),
				docker("Temporal or Airflow/Prefect as scheduler and supervisor.", "temporal", "airflow", "prefect"),
				kube("CronJob/Job with backoffLimit and activeDeadlineSeconds.", "CronJob", "Job"),
			},
			Related:   []string{"saga", "retry", "leader-election"},
			Templates: []string{"aws-scheduled-job", "aws-step-functions", "k8s-cronjob-worker"},
			Keywords:  []string{"cron", "scheduled", "programado", "programada", "schedule", "tareas programadas", "batch", "lotes", "nightly", "nocturno", "workflow", "orquestar"},
			Sources: []Source{
				azP("Scheduler Agent Supervisor pattern", "scheduler-agent-supervisor"),
			},
		},
		{
			ID:      "leader-election",
			Name:    "Leader election",
			Kind:    KindPattern,
			Problem: "Several instances of a service run for availability, but some work (scheduling, relays, singleton consumers) must be done by exactly one at a time.",
			UseWhen: []string{
				"singleton tasks in a horizontally scaled service",
				"coordinating replicas of a stateful component",
			},
			AvoidWhen: []string{
				"the work can be partitioned or made idempotent across instances",
				"a managed scheduler can run it once",
			},
			Tradeoffs: []string{
				"high availability for singleton work",
				"split-brain risk; a leader is a throughput bottleneck",
			},
			Practices: []string{
				"use leases with TTL from a consistent store (Kubernetes Lease, etcd, DynamoDB conditional writes)",
				"fence work with lease epochs/tokens to avoid split-brain",
				"never build consensus yourself",
			},
			Blocks: []Block{
				aws("DynamoDB conditional writes as lease lock for ECS tasks.", "aws_dynamodb_table", "aws_ecs_service"),
				azure("Blob lease on a storage container as the lock.", "azurerm_storage_account", "azurerm_storage_container"),
				gcp("Cloud Storage or Firestore preconditions as the lock.", "google_storage_bucket", "google_firestore_database"),
				oci("NoSQL conditional writes as the lock.", "oci_nosql_table"),
				docker("etcd or Consul sessions for locks.", "etcd", "consul"),
				kube("coordination.k8s.io Lease objects via client-go leader election; RBAC Role for leases.", "Deployment", "Role", "RoleBinding", "ServiceAccount"),
			},
			Related:  []string{"scheduler-agent-supervisor", "transactional-outbox"},
			Keywords: []string{"leader election", "eleccion de lider", "lider", "leader", "singleton", "single instance", "una sola instancia", "lock", "bloqueo distribuido", "distributed lock"},
			Sources: []Source{
				azP("Leader Election pattern", "leader-election"),
			},
		},
		{
			ID:      "deployment-stamps",
			Name:    "Deployment stamps",
			Kind:    KindPattern,
			Problem: "A multi-tenant SaaS outgrows the limits of a single deployment, or tenants need regional data residency and isolation.",
			UseWhen: []string{
				"multi-tenant SaaS beyond single-deployment scale limits",
				"data residency per region; blast-radius reduction",
			},
			AvoidWhen: []string{
				"a single deployment comfortably handles all tenants",
				"small teams without infrastructure-as-code automation",
			},
			Tradeoffs: []string{
				"near-linear scale and fault isolation",
				"fleet management, routing tenants to stamps and cross-stamp operations",
			},
			Practices: []string{
				"stamps are identical, created entirely from IaC modules",
				"a global routing layer maps tenants to stamps",
				"roll out changes stamp by stamp (rings)",
			},
			Blocks: []Block{
				aws("One stack per stamp (VPC, ECS, database) behind Route 53 records or Global Accelerator.", "aws_route53_record", "aws_globalaccelerator_accelerator", "aws_ecs_service", "aws_rds_cluster"),
				azure("Stamp per region behind Front Door or Traffic Manager.", "azurerm_cdn_frontdoor_profile", "azurerm_traffic_manager_profile", "azurerm_container_app_environment"),
				gcp("Stamp per region behind a global load balancer.", "google_compute_global_forwarding_rule", "google_cloud_run_v2_service"),
				oci("Stamp per region with DNS steering.", "oci_dns_record", "oci_containerengine_cluster"),
				docker("One compose project per stamp behind a shared Traefik.", "traefik").equivalent("one compose project per stamp"),
				kube("One namespace (or cluster) per stamp from the same Kustomize overlays.", "Deployment", "Service", "Ingress"),
			},
			Related:   []string{"sharding", "bulkhead"},
			Templates: []string{"k8s-kustomize-app"},
			Keywords:  []string{"deployment stamps", "stamps", "multi tenant", "multitenant", "multi inquilino", "saas", "tenants", "inquilinos", "multi region", "multiregion", "data residency", "residencia de datos"},
			Sources: []Source{
				azP("Deployment Stamps pattern", "deployment-stamp"),
			},
		},
		{
			ID:      "external-configuration-store",
			Name:    "External configuration store",
			Kind:    KindPattern,
			Problem: "Configuration and secrets baked into packages require redeploys to change and leak across environments.",
			UseWhen: []string{
				"several environments or instances sharing configuration",
				"secrets and feature flags that change without redeploying",
			},
			AvoidWhen: []string{
				"a single service with a handful of static environment variables",
			},
			Tradeoffs: []string{
				"centralized, audited configuration and secret rotation",
				"a runtime dependency; caching and fallbacks needed",
			},
			Practices: []string{
				"secrets in a secrets manager with rotation; never in images, git or plain env files",
				"least-privilege read access per service identity",
				"cache configuration with refresh; validate before rollout",
			},
			Blocks: []Block{
				aws("SSM Parameter Store and Secrets Manager; AppConfig for feature flags.", "aws_ssm_parameter", "aws_secretsmanager_secret", "aws_appconfig_application"),
				azure("App Configuration plus Key Vault references.", "azurerm_app_configuration", "azurerm_key_vault", "azurerm_key_vault_secret"),
				gcp("Secret Manager; Parameter Manager for configuration.", "google_secret_manager_secret", "google_parameter_manager_parameter"),
				oci("Vault secrets.", "oci_kms_vault", "oci_vault_secret"),
				docker("HashiCorp Vault or Consul KV; compose secrets.", "vault", "consul"),
				kube("ConfigMaps and Secrets mounted into Deployments.", "ConfigMap", "Secret", "Deployment"),
			},
			Related:  []string{"sidecar", "federated-identity"},
			Keywords: []string{"configuration", "configuracion", "config", "secrets", "secretos", "feature flags", "credenciales", "credentials", "vault"},
			Sources: []Source{
				azP("External Configuration Store pattern", "external-configuration-store"),
			},
		},
	}
}
