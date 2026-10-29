import type { Connection, ModuleTree, Workflow, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import { archCategoryOf, baseType, isPlumbing, providerOfType, type ArchCategory, type ArchProvider } from "./icons/classify"
import { PROVIDER_NAME, type ArchActor, type ArchCard, type ArchEdge, type ArchGroup, type ArchModel, type FlowKind, type GroupKind, type SubnetTier } from "./model"
import { expandModules, moduleServiceType, type ModuleInstance } from "./modules"
import { azapiType } from "./azapi"
import { firewallTargets, ROUTE, ROUTE_TABLE, routeLines, SECURITY_GROUP, SECURITY_RULE, securityName, securitySummary, type RefAt } from "./network"
import { findAttr, localEdges, makeResolver, valueAt, type Resolver } from "./resolve"

/** The parts of a workflow the diagram is derived from. */
export type DeriveWorkflow = Pick<
  Workflow,
  "nodes" | "edges" | "variables" | "locals" | "providers" | "environments" | "activeEnvironment"
>

export interface DeriveOptions {
  /** Saved connections the workflow uses (its load tests), drawn as third-party services. */
  connections?: Pick<Connection, "id" | "name" | "kind">[]
  /** Friendly label of a resource type (catalog label); defaults to a humanized type. */
  labelOf?: (type: string) => string | undefined
  /** Content of the module calls: readable modules are drawn as the services they create. */
  modules?: ModuleTree
  /**
   * Keep what any environment or the variables' defaults create, not only the active environment (the Build
   * canvas' request walkthrough follows the whole design: a front door a sandbox environment skips with
   * `count = 0` is still its entry point).
   */
  everyEnvironment?: boolean
}

const PROVIDERS: ArchProvider[] = ["aws", "azure", "gcp", "oci"]
const PROVIDER_BLOCK: Record<ArchProvider, string> = { aws: "aws", azure: "azurerm", gcp: "google", oci: "oci" }

// ------------------------------------------------------------ type families

const VPC_TYPE = /^(aws_vpc|aws_default_vpc|google_compute_network|azurerm_virtual_network|oci_core_vcn|oci_core_virtual_network)$/
const SUBNET_TYPE = /^(aws_subnet|aws_default_subnet|google_compute_subnetwork|azurerm_subnet|oci_core_subnet)$/
const ACCOUNT_TYPE = /^(azurerm_resource_group|oci_identity_compartment)$/
const INTERNET_GATEWAY = /^(aws_internet_gateway|aws_egress_only_internet_gateway|oci_core_internet_gateway)$/
const NAT_GATEWAY = /^(aws_nat_gateway|oci_core_nat_gateway|azurerm_nat_gateway|google_compute_router)$/
/** Never drawn nor listed: tags, enabled APIs, locks, telemetry and lookups of provider metadata. */
const QUIET =
  /^(oci_identity_tag|oci_identity_tag_namespace|oci_identity_tag_default|google_project_service|google_project_service_identity|google_compute_shared_vpc_host_project|google_compute_shared_vpc_service_project|azurerm_management_lock|azurerm_virtual_network_peering|google_compute_network_peering|modtm_telemetry|azapi_client_config|azapi_deployment|azapi_update_resource|azapi_resource_action|azurerm_ssh_public_key|aws_default_tags)$/
/**
 * Data sources that only look a value up (an account ID for a bucket policy, CloudFront's address
 * list for a security group, an AMI): helpers of what uses them, never a card of their own.
 */
const LOOKUP =
  /^(aws_elb_service_account|aws_ec2_managed_prefix_list|aws_prefix_list|aws_ip_ranges|aws_canonical_user_id|aws_billing_service_account|aws_cloudfront_log_delivery_canonical_user_id|aws_service_principal|aws_arn|aws_ami_ids|aws_ssm_parameter|aws_availability_zone|aws_ec2_instance_type_offerings?|aws_iam_policy|aws_cloudfront_(cache|origin_request|response_headers)_policy|google_netblock_ip_ranges|google_compute_lb_ip_ranges|google_compute_default_service_account|google_storage_project_service_account|azurerm_role_definition|azurerm_client_config|oci_core_services|oci_identity_availability_domains?)$/

/** Edge & DNS: CDNs, DNS, API front doors and global load balancing. */
const EDGE_TYPE =
  /(cloudfront|route53|apigateway|api_gateway|globalaccelerator|_cdn_|_cdn$|frontdoor|_dns_|api_management|global_forwarding_rule|traffic_manager)/
/** Edge security: WAF, DDoS protection and certificates. */
const EDGE_SECURITY_TYPE = /(wafv2|_waf_|_waf$|shield|_acm_|web_application_firewall|firewall_policy|security_policy|ssl_certificate|certificate_manager)/

/** AWS services without a region. */
const AWS_GLOBAL = /^aws_(cloudfront|route53|iam|organizations|budgets|ce|shield|globalaccelerator|waf)_/
/** GCP services without a region: global load balancing, DNS, Cloud Armor, certificates, IAM. */
const GCP_GLOBAL =
  /^google_(compute_global_|compute_url_map|compute_target_https?_proxy|compute_backend_(service|bucket)|compute_security_policy|compute_(managed_)?ssl_|compute_health_check|certificate_manager_|dns_|service_account|project_iam_|iam_|identity_platform_|organization_|folder_|billing_|compute_network$)/
/** Azure services without a location of their own (sub-resources sit where their parent does). */
const AZURE_GLOBAL = /^azurerm_(cdn_frontdoor_|frontdoor|dns_|private_dns_|traffic_manager_)/

/** References through these attributes carry traffic from the referenced resource into the referrer. */
const INBOUND_ATTR = /(^|\.)(event_source_arn|topic|pubsub_topic|target_group_arns?|source_arn|subscription|load_balancer_id)$/
/** Sub-resource references (a queue in its storage account, a service in its cluster): shown by grouping, not arrows. */
const PARENT_ATTR =
  /^(storage_account_(name|id)|namespace_(name|id)|server_(name|id)|cluster|cluster_(name|id)|account_name|key_vault_id|service_plan_id|app_service_plan_id|resource_group_name|location|virtual_network_name|api_management_(name|id)|eventhub_namespace_name|cosmosdb_account_name|compartment_id|project|dataset_id|parent_id|container_app_environment_id|application_id|kubernetes_cluster_id|stream_pool_id|vault_id|management_endpoint|availability_domain|fault_domain)$/
/** Attributes of a serverless service reaching into a subnet (VNet integration, Direct VPC egress): an arrow, not a placement. */
const INTEGRATION_ATTR = /^(virtual_network_subnet_id|(template\.)?vpc_access\.network_interfaces\.(subnetwork|network))$/

/** IAM resources that grant a role permissions; their references are what the role's users may call. */
const IAM_ATTACHMENT = /^aws_iam_(role_policy|role_policy_attachment|policy_attachment)$/
const IAM_FOLLOW = /^(aws_iam_|data\.aws_iam_policy_document$)/

/**
 * Helper resources linking two resources: traffic flows from the one referenced through `from` to
 * the one referenced through `to` (both resolved to their cards).
 */
const LINKS: Record<string, readonly [RegExp, RegExp]> = {
  aws_lambda_event_source_mapping: [/^event_source_arn$/, /^function_name$/],
  aws_lambda_permission: [/^source_arn$/, /^function_name$/],
  aws_apigatewayv2_integration: [/^api_id$/, /^integration_uri$/],
  aws_api_gateway_integration: [/^rest_api_id$/, /^uri$/],
  aws_sns_topic_subscription: [/^topic_arn$/, /^(endpoint|for_each)$/],
  aws_s3_bucket_notification: [/^bucket$/, /^(lambda_function|queue|topic)\./],
  aws_cloudwatch_event_target: [/^rule$/, /^arn$/],
  aws_lb_listener: [/^load_balancer_arn$/, /target_group_arn$/],
  aws_alb_listener: [/^load_balancer_arn$/, /target_group_arn$/],
  aws_lb_listener_rule: [/^listener_arn$/, /target_group_arn$/],
  aws_alb_listener_rule: [/^listener_arn$/, /target_group_arn$/],
  aws_lb_target_group_attachment: [/^target_group_arn$/, /^target_id$/],
  aws_alb_target_group_attachment: [/^target_group_arn$/, /^target_id$/],
  aws_route53_record: [/^zone_id$/, /^(alias\.name|alias\.zone_id|records)$/],
  azurerm_eventgrid_event_subscription: [/^scope$/, /(endpoint|function_id|queue_name|webhook)/],
  azurerm_dns_a_record: [/^zone_name$/, /^(records|target_resource_id)$/],
  azurerm_dns_cname_record: [/^zone_name$/, /^(record|target_resource_id)$/],
  azurerm_cdn_frontdoor_origin: [/^cdn_frontdoor_origin_group_id$/, /^(host_name|origin_host_header)$/],
  azurerm_cdn_frontdoor_security_policy: [/^cdn_frontdoor_profile_id$/, /cdn_frontdoor_firewall_policy_id$/],
  azurerm_virtual_network_gateway_connection: [/^virtual_network_gateway_id$/, /^(peer_virtual_network_gateway_id|local_network_gateway_id|express_route_circuit_id)$/],
  azurerm_network_interface_application_gateway_backend_address_pool_association: [/^backend_address_pool_id$/, /^network_interface_id$/],
  azurerm_network_interface_backend_address_pool_association: [/^backend_address_pool_id$/, /^network_interface_id$/],
  azurerm_api_management_backend: [/^api_management_name$/, /^(url|resource_id)$/],
  azurerm_monitor_diagnostic_setting: [/^target_resource_id$/, /^(log_analytics_workspace_id|storage_account_id|eventhub_authorization_rule_id)$/],
  google_storage_notification: [/^bucket$/, /^topic$/],
  google_dns_record_set: [/^managed_zone$/, /^rrdatas$/],
  oci_load_balancer_backend: [/^load_balancer_id$/, /^ip_address$/],
  oci_network_load_balancer_backend: [/^network_load_balancer_id$/, /^(target_id|ip_address)$/],
  oci_apigateway_deployment: [/^gateway_id$/, /^specification\.routes\.backend\.(url|function_id)$/],
  oci_ons_subscription: [/^topic_id$/, /^endpoint$/],
}

/** Secrets whose value can carry a connection string (see the secret flows of deriveArchitecture), and that value. */
const SECRET_VALUE = /^(azurerm_key_vault_secret|aws_secretsmanager_secret_version|google_secret_manager_secret_version|aws_ssm_parameter|oci_vault_secret)$/
const SECRET_VALUE_ATTR = /^(value|value_wo|secret_string|secret_string_wo|secret_data|secret_data_wo|insecure_value|secret_content\.content)$/

/** DNS records: a name pointing at a resource (through LINKS, see dnsTargets). */
const DNS_RECORD = /^(aws_route53_record|azurerm_dns_(a|aaaa|cname)_record|google_dns_record_set)$/
/** Certificates whose DNS validation records (and validations) belong to them, not to the zone. */
const CERTIFICATE = /^(aws_acm_certificate|google_certificate_manager_dns_authorization)$/
/** References of a helper to a certificate are its owner's (an HTTPS listener's certificate is its load balancer's). */
const CERTIFICATE_ATTR = /(^|\.)(certificate_arn|acm_certificate_arn)$/
/**
 * Helpers that are part of another resource, through the attribute naming it: drawn inside it, and
 * a reference to one is a reference to it (CloudFront using a certificate's validation uses the
 * certificate; a web app reading a vault's secret reads the vault).
 */
const PART_OF: Record<string, RegExp> = {
  aws_acm_certificate_validation: /^certificate_arn$/,
  aws_secretsmanager_secret_version: /^secret_id$/,
  aws_kms_alias: /^target_key_id$/,
  google_secret_manager_secret_version: /^secret$/,
  azurerm_key_vault_secret: /^key_vault_id$/,
  azurerm_key_vault_key: /^key_vault_id$/,
  azurerm_key_vault_certificate: /^key_vault_id$/,
}
/**
 * Access grants: a principal (through `principal`) may use a resource (through `scope`). Drawn like
 * AWS IAM: the workloads using the principal reach the resource; a managed identity itself only
 * gets the dashed lines of the workloads using it.
 */
const GRANTS: Record<string, readonly [principal: RegExp, scope: RegExp]> = {
  azurerm_role_assignment: [/^principal_id$/, /^scope$/],
  azurerm_key_vault_access_policy: [/^object_id$/, /^key_vault_id$/],
  azurerm_redis_cache_access_policy_assignment: [/^object_id$/, /^redis_cache_id$/],
  azurerm_cosmosdb_sql_role_assignment: [/^principal_id$/, /^(scope|account_name)$/],
}

/** Attributes naming the resource a helper belongs to, most specific first. */
const OWNER_ATTRS = [
  "function_name",
  "role",
  "roles",
  "bucket",
  "queue_url",
  "topic_arn",
  "topic",
  "subscription",
  "api_id",
  "rest_api_id",
  "load_balancer_arn",
  "loadbalancer_id",
  "load_balancer_id",
  "network_load_balancer_id",
  "listener_arn",
  "target_group_arn",
  "user_pool_id",
  "secret_id",
  "key_id",
  "target_key_id",
  "table_name",
  "repository",
  "storage_account_id",
  "storage_account_name",
  "route_table_id",
  "route_table_name",
  "security_group_id",
  "network_security_group_id",
  "network_security_group_name",
  "rule",
  "dimensions",
  "alarm_actions",
  "log_group_name",
  "zone_id",
  "managed_zone",
  "zone_name",
  "instance_id",
  "instance",
  "virtual_machine_id",
  "virtual_network_gateway_id",
  "parent_id",
  "network_interface_id",
  "server_id",
  "namespace_id",
  "eventhub_id",
  "cosmosdb_account_name",
  "key_vault_id",
  "cdn_frontdoor_profile_id",
  "cdn_frontdoor_origin_group_id",
  "cdn_frontdoor_endpoint_id",
  "api_management_name",
  "api_management_id",
  "app_service_id",
  "target_resource_id",
  "gateway_id",
  "topic_id",
  "drg_id",
  "bastion_id",
  "export_set_id",
  "file_system_id",
  "autonomous_database_id",
  "router",
  "service",
  "name",
]

/** Facts a folded helper adds to its card. */
const FOLDED_FACT: Record<string, string> = {
  aws_s3_bucket_website_configuration: "static website",
  aws_acm_certificate_validation: "DNS validated",
  azurerm_storage_account_static_website: "static website",
  aws_s3_bucket_versioning: "versioned",
  aws_lambda_function_url: "function URL",
  aws_sqs_queue_redrive_policy: "DLQ",
  aws_s3_bucket_notification: "event notifications",
  aws_eip: "Elastic IP",
  aws_appautoscaling_target: "autoscaling",
  aws_appautoscaling_policy: "autoscaling",
  azurerm_public_ip: "public IP",
  azurerm_private_endpoint: "private endpoint",
  azurerm_private_dns_zone: "private DNS",
  azurerm_monitor_autoscale_setting: "autoscaling",
  google_compute_router_nat: "Cloud NAT",
  google_compute_address: "static IP",
  google_compute_target_https_proxy: "HTTPS",
  google_compute_region_target_https_proxy: "HTTPS",
  google_compute_managed_ssl_certificate: "managed TLS",
  google_compute_autoscaler: "autoscaling",
  google_compute_region_autoscaler: "autoscaling",
  oci_core_public_ip: "public IP",
  oci_autoscaling_auto_scaling_configuration: "autoscaling",
}

/** Container registries: platform services rather than compute. */
const REGISTRY_TYPE = /^(aws_ecr_|aws_ecrpublic_|google_artifact_registry_|azurerm_container_registry|oci_artifacts_container_)/
/** Network fabric a module may create next to its services; drawn in its VPC, never folded into a service. */
const NETWORK_FABRIC =
  /(internet_gateway|nat_gateway|service_gateway|vpc_endpoint|vpc_peering|vpn_|transit_gateway|network_interface|customer_gateway|_router(_nat)?$|^aws_dx_|^oci_core_drg|local_peering_gateway|remote_peering_connection|vpc_access_connector|service_networking_connection|bastion)/
/** Load balancers: the front of the services behind them. */
const LOAD_BALANCER =
  /^(aws_lb|aws_alb|aws_elb|azurerm_lb|azurerm_application_gateway|oci_load_balancer|oci_load_balancer_load_balancer|oci_network_load_balancer_network_load_balancer|google_compute_(global_)?forwarding_rule|google_compute_url_map)$/
/** The parts of a GCP load balancer behind its forwarding rule, folded into it. */
const GCP_LB_PART =
  /^google_compute_(target_(http|https|ssl|tcp|grpc)_proxy|region_target_(http|https|tcp)_proxy|(region_)?url_map|(region_)?backend_service|backend_bucket|(region_)?health_check|https?_health_check|(global_|region_)?network_endpoint_group|managed_ssl_certificate|(region_)?ssl_certificate|ssl_policy|global_address|address|(global_)?forwarding_rule)$/
/** Folded without a word in the card's "+ …" line. */
const QUIET_FOLD = /(_association$|^google_(?!project_).+_iam_(member|binding|policy)$|^azurerm_network_interface$|^oci_core_vnic_attachment$|^azurerm_cdn_frontdoor_(endpoint|origin_group|origin|route|rule_set|rule|secret|security_policy)$)/
/** Absorbed resources sizing their card ("512 CPU · 1 GB", "P1v2"). */
const SIZING =
  /^(aws_ecs_task_definition|azurerm_service_plan|azurerm_app_service_plan|google_compute_instance_template|oci_core_instance_configuration|google_container_node_pool|azurerm_kubernetes_cluster_node_pool|oci_containerengine_node_pool)$/

/** A way to find the card a resource is drawn inside: a user through `attr` (of `type`), or a reference. */
interface Via {
  user?: RegExp
  userType?: RegExp
  ref?: RegExp
  refType?: RegExp
  /** Only when exactly one card is found (a plan hosting one app folds into it; one hosting several is a card). */
  single?: boolean
}
/**
 * Resources drawn inside the card using them, first matching way first: an Elastic IP in its NAT
 * gateway, a target group in its load balancer, a task definition in its service, a private endpoint
 * in the service it exposes, a queue in its namespace, a node pool in its cluster.
 */
const ABSORB: readonly (readonly [RegExp, readonly Via[]])[] = [
  [/^aws_eip$/, [{ user: /^allocation_id$/ }, { ref: /^(instance|network_interface)$/ }]],
  [/^aws_(lb|alb)_target_group$/, [{ userType: /^aws_(lb|alb)_listener(_rule)?$/ }]],
  [/^aws_ecs_task_definition$/, [{ user: /^task_definition$/, userType: /^aws_ecs_service$/ }]],
  [/^aws_cloudfront_vpc_origin$/, [{ userType: /^aws_cloudfront_distribution$/ }]],
  // Azure
  [/^azurerm_public_ip$/, [{ user: /public_ip_address_ids?$/ }, { userType: /^azurerm_nat_gateway_public_ip_association$/ }]],
  [/^azurerm_private_endpoint$/, [{ ref: /^private_service_connection\.private_connection_resource_id$/ }]],
  [/^azurerm_private_dns_zone$/, [{ user: /private_dns_zone_ids$/, userType: /^azurerm_private_endpoint$/ }]],
  [
    /^(azurerm_service_plan|azurerm_app_service_plan)$/,
    [
      { user: /^(service_plan_id|app_service_plan_id)$/, single: true },
      // AzAPI sites name their plan inside `body`.
      { userType: /^azurerm_(linux|windows)_(web|function)_app$/, single: true },
    ],
  ],
  [/^azurerm_container_app_environment$/, [{ user: /^container_app_environment_id$/, single: true }]],
  [/^azurerm_storage_(queue|table|share|container)$/, [{ ref: /^storage_account_(name|id)$/ }]],
  [/^azurerm_servicebus_(queue|topic)$/, [{ ref: /^namespace_(name|id)$/ }]],
  [/^azurerm_eventhub$/, [{ ref: /^namespace_(name|id)$/ }]],
  [/^azurerm_cosmosdb_(sql|mongo|cassandra|gremlin|table)_/, [{ ref: /^(account_name|cosmosdb_account_name)$/ }]],
  [/^azurerm_(mssql|mysql|postgresql|mariadb)_database$/, [{ ref: /^server_(id|name)$/ }]],
  [/^azurerm_mysql_flexible_database$/, [{ ref: /^server_(id|name)$/ }]],
  [/^azurerm_kubernetes_cluster_node_pool$/, [{ ref: /^kubernetes_cluster_id$/ }]],
  [/^azurerm_linux_web_app_slot$|^azurerm_windows_web_app_slot$/, [{ ref: /^app_service_id$/ }]],
  // GCP
  [/^google_container_node_pool$/, [{ ref: /^cluster$/ }]],
  [/^google_pubsub_subscription$/, [{ ref: /^topic$/ }]],
  [/^google_compute_instance_template$/, [{ user: /instance_template$/, userType: /^google_compute_(region_)?instance_group_manager$/ }]],
  [/^google_compute_(region_)?autoscaler$/, [{ ref: /^target$/ }]],
  [/^google_compute_address$/, [{ user: /^nat_ips$/ }, { user: /access_config\.nat_ip$/ }]],
  [/^google_compute_global_address$/, [{ user: /^reserved_peering_ranges$/ }]],
  // OCI
  [/^oci_kms_key$/, [{ ref: /^management_endpoint$/ }]],
  [/^oci_vault_secret$/, [{ ref: /^vault_id$/ }]],
  [/^oci_core_instance_configuration$/, [{ user: /^instance_configuration_id$/ }]],
  [/^oci_containerengine_node_pool$/, [{ ref: /^cluster_id$/ }]],
  [/^oci_functions_application$/, [{ user: /^application_id$/, single: true }]],
  [/^oci_streaming_stream_pool$/, [{ user: /^stream_pool_id$/, single: true }]],
]
/** Node pools with a subnet of their own stay cards (worker nodes apart from the control plane). */
const NODE_POOL = /^(azurerm_kubernetes_cluster_node_pool|oci_containerengine_node_pool)$/
/** Resources at least this heavy are a main service of a module (see serviceWeight). */
const MAIN_SERVICE = 50
/** More dashed (control) lines than this into one card become a "used by N" fact on it. */
const SHARED_FAN_IN = 3

/**
 * How much a resource is "the service" of a module call: running services first, then their data,
 * messaging and front doors; identity, keys and telemetry only support them.
 */
function serviceWeight(type: string, category: ArchCategory): number {
  const t = baseType(type)
  if (
    /^(aws_ecs_service|aws_apprunner_service|google_cloud_run_v2_service|google_cloud_run_service|azurerm_container_app|azurerm_(linux|windows)_(web|function)_app|azurerm_function_app_flex_consumption|oci_functions_function|google_compute_(region_)?instance_group_manager)$/.test(
      t,
    )
  )
    return 110
  if (/acm_|certificate/.test(t)) return 40
  // A node pool serves its cluster.
  if (/node_pool$/.test(t)) return 90
  if (EDGE_SECURITY_TYPE.test(t)) return 60
  if (EDGE_TYPE.test(t) || LOAD_BALANCER.test(t)) return 85
  if (REGISTRY_TYPE.test(t)) return 50
  switch (category) {
    case "compute":
    case "containers":
    case "serverless":
      return 100
    case "database":
      return 90
    case "storage":
    case "integration":
    case "analytics":
    case "ai":
      return 80
    case "networking":
    case "devops":
      return 50
    case "security":
      return /secret|kms|key_vault|vault/.test(t) ? 40 : 30
    case "identity":
      return 20
    case "monitoring":
    case "management":
      return 10
    default:
      return 5
  }
}

/** Resources of one service share a family: "aws_cloudfront_function" and "aws_cloudfront_distribution". */
function serviceFamily(n: WorkflowNode) {
  return /^[a-z0-9]+_[a-z0-9]+/.exec(baseType(n.type))?.[0] ?? n.type
}

/** Words of module names that only say where a module comes from ("terraform-oci-arch-", "avm-res-"). */
const MODULE_NOISE = /^(avm|res|ptn|utl|terraform|module|aws|azure|azurerm|google|gcp|oci|oracle|arch)$/i

/**
 * Title of a module call's cards: the call's name without the words it shares with the module's
 * ("ecs_service_billing" calling ecs_service → "Billing"); the whole name when it is the module's
 * ("oci-arch-tomcat" calling terraform-oci-arch-tomcat → "Tomcat").
 */
function moduleTitle(inst: ModuleInstance) {
  const split = (name: string) => name.split(/[-_]+/).filter(Boolean)
  const words = split(inst.name)
  while (words.length > 1 && MODULE_NOISE.test(words[0])) words.shift()
  const own = split(inst.moduleName.toLowerCase()).filter((w) => !MODULE_NOISE.test(w))
  let i = 0
  if (words.join("_").toLowerCase() !== own.join("_")) while (i < words.length - 1 && own.includes(words[i].toLowerCase())) i++
  return humanize(words.slice(i).join("_"))
}

/** Types that take traffic from the internet. */
const PUBLIC_ENTRY: Record<string, true> = Object.fromEntries(
  [
  "aws_apigatewayv2_api",
  "aws_api_gateway_rest_api",
  "aws_cloudfront_distribution",
  "aws_appsync_graphql_api",
  "aws_globalaccelerator_accelerator",
  "azurerm_application_gateway",
  "azurerm_cdn_frontdoor_profile",
  "azurerm_cdn_frontdoor_endpoint",
  "azurerm_frontdoor",
  "azurerm_cdn_profile",
  "azurerm_api_management",
  "azurerm_static_web_app",
  "azurerm_linux_web_app",
  "azurerm_windows_web_app",
  "azurerm_linux_function_app",
  "azurerm_windows_function_app",
  "azurerm_function_app_flex_consumption",
  "azurerm_container_app",
  "google_compute_global_forwarding_rule",
  "google_api_gateway_gateway",
  "oci_apigateway_gateway",
  ].map((t) => [t, true]),
)
/** Helpers that make their card public. */
const PUBLIC_HELPER: Record<string, true> = { aws_lambda_function_url: true, aws_s3_bucket_website_configuration: true, azurerm_storage_account_static_website: true }
const DNS_ZONE = /^(aws_route53_zone|google_dns_managed_zone|azurerm_dns_zone|oci_dns_zone)$/

type LogicalKey =
  | "edge"
  | "edgesec"
  | "compute"
  | "messaging"
  | "data"
  | "security"
  | "observability"
  | "ai"
  | "network"
  | "platform"
  | "modules"

const LOGICAL: Record<LogicalKey, string> = {
  edge: "Edge & DNS",
  edgesec: "Edge security",
  compute: "Compute",
  messaging: "Messaging & integration",
  data: "Data",
  security: "Identity & security",
  observability: "Observability",
  ai: "AI & ML",
  network: "Networking",
  platform: "Platform services",
  modules: "Modules",
}
const LOGICAL_ORDER = Object.keys(LOGICAL) as LogicalKey[]

export function logicalKeyOf(type: string, category: ArchCategory): LogicalKey {
  const t = baseType(type)
  if (EDGE_SECURITY_TYPE.test(t)) return "edgesec"
  if (EDGE_TYPE.test(t)) return "edge"
  if (REGISTRY_TYPE.test(t)) return "platform"
  switch (category) {
    case "compute":
    case "containers":
    case "serverless":
      return "compute"
    case "integration":
      return "messaging"
    case "storage":
    case "database":
    case "analytics":
      return "data"
    case "identity":
    case "security":
      return "security"
    case "monitoring":
      return "observability"
    case "ai":
      return "ai"
    case "networking":
      return "network"
    default:
      return "platform"
  }
}

// ------------------------------------------------------------ text helpers

const ACRONYMS: Record<string, true> = Object.fromEntries(
  "api apis acm alb arn aws az cdn cidr db dlq dns ec2 ecr ecs efs eip eks elb fifo fss gce gke aks oke adb mds mig drg nsg lpg http https iam id ip kms lb nat nlb rds s3 ses sg sns sqs sql ssl ssm sso tls url vm vpc vcn vnet vpn waf gcp oci ttl ui"
    .split(" ")
    .map((w) => [w, true]),
)

/** Words with their own spelling. */
const WORDS: Record<string, string> = {
  apigatewayv2: "API Gateway",
  wafv2: "WAF",
  cloudwatch: "CloudWatch",
  cloudfront: "CloudFront",
  dynamodb: "DynamoDB",
  elasticache: "ElastiCache",
  route53: "Route 53",
  secretsmanager: "Secrets Manager",
  opensearch: "OpenSearch",
  pubsub: "Pub/Sub",
  cosmosdb: "Cosmos DB",
  servicebus: "Service Bus",
  eventhub: "Event Hub",
  objectstorage: "Object Storage",
  mssql: "SQL Server",
  postgresql: "PostgreSQL",
  mysql: "MySQL",
  containerengine: "Container Engine",
}

/** "test_web" → "Test web", "api" → "API". */
export function humanize(name: string) {
  const words = name.split(/[_\-\s]+/).filter(Boolean)
  return words
    .map((w, i) => {
      const lower = w.toLowerCase()
      if (WORDS[lower]) return WORDS[lower]
      if (ACRONYMS[lower]) return lower.toUpperCase()
      return i === 0 ? lower.charAt(0).toUpperCase() + lower.slice(1) : lower
    })
    .join(" ")
}

/** Humanized type without its provider prefix: "aws_iam_role_policy" → "IAM role policy". */
export function typeLabel(type: string) {
  return humanize(baseType(type).replace(/^(aws|azurerm|azuread|google|oci)_/, ""))
}

function plural(label: string) {
  if (/(s|x|ch|sh)$/i.test(label)) return `${label}es`
  if (/[^aeiou]y$/i.test(label)) return `${label.slice(0, -1)}ies`
  return `${label}s`
}

// ------------------------------------------------------------ derivation

interface Ref {
  /** The other node. */
  id: string
  /** Attribute path of the referrer, indices removed. */
  attr: string
  dependsOn: boolean
}

/**
 * sg: a security group, folded into the cards using it; routes: a route table, folded into its
 * subnets; quiet: never drawn (tags, enabled APIs, lookups and helpers of nothing drawn).
 */
type Role = "vpc" | "subnet" | "account" | "plumbing" | "card" | "sg" | "routes" | "quiet"

const isStr = (v: unknown): v is string => typeof v === "string" && v !== ""
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.filter(isStr) : isStr(v) ? [v] : [])
/** Group sublabel lines, without the empty ones. */
const sublabel = (...lines: unknown[]) => {
  const out = lines.filter(isStr)
  return out.length ? out : undefined
}
const unique = <T,>(xs: T[]) => [...new Set(xs)]

/** Provider of a module call from its source ("terraform-aws-modules/vpc/aws" → aws). */
function moduleProvider(n: WorkflowNode, r: Resolver, fallback?: ArchProvider): ArchProvider | undefined {
  const source = r.attr(n, "source")
  if (isStr(source)) {
    const m = /(?:^|[/-])(aws|azurerm|azure|google|gcp|oci|oracle)(?:$|[/-])/.exec(source)
    if (m) return m[1] === "azurerm" || m[1] === "azure" ? "azure" : m[1] === "google" || m[1] === "gcp" ? "gcp" : m[1] === "oracle" ? "oci" : (m[1] as ArchProvider)
  }
  return fallback
}

/**
 * Derives the architecture diagram of a workflow: containers (cloud, account/project/resource
 * group, region, VPC, AZ, subnet, logical service groups), resource cards with folded helpers,
 * external actors and de-duplicated data-flow edges. Pure and deterministic.
 */
export function deriveArchitecture(input: DeriveWorkflow, opts: DeriveOptions = {}): ArchModel {
  const modular = expandModules(input, opts.modules)
  // References through locals count like direct ones: the workflow's edges do not have them (an
  // expanded workflow's edges, recomputed through module scopes, do).
  const throughLocals = modular.instanceOf.size ? [] : localEdges(modular.wf)
  const expanded = throughLocals.length ? { ...modular, wf: { ...modular.wf, edges: [...(modular.wf.edges ?? []), ...throughLocals] } } : modular
  const { instanceOf, calls } = expanded
  // The resolver follows a for_each over another resource to that resource's instances.
  const declared = new Map(expanded.wf.nodes.map((n) => [n.id, n]))
  const forEachOf = new Map<string, string>()
  for (const e of expanded.wf.edges ?? []) if (e.kind !== "depends_on" && e.attribute === "for_each" && !forEachOf.has(e.target)) forEachOf.set(e.target, e.source)
  const forEachSource = (n: WorkflowNode) => declared.get(forEachOf.get(n.id) ?? "")
  const r = makeResolver(expanded.wf, forEachSource)
  /** How many copies the module calls around a resource make (1 outside modules; undefined when unknown). */
  const scaleOf = (n: WorkflowNode, by: Resolver = r) => {
    let k: number | undefined = 1
    for (const c of calls.get(n.id) ?? []) {
      if (!findAttr(c.body, "count") && !findAttr(c.body, "for_each")) continue
      const v = by.count(c)
      k = k !== undefined && v !== undefined ? k * v : undefined
    }
    return k
  }
  // What exists: the active environment's design; with environments but none active (or with every
  // environment asked for), what any of them, or the defaults, creates (a frontend only dev and qa enable is
  // still part of the design).
  const envNames = opts.everyEnvironment ? [undefined, ...(input.environments ?? []).map((e) => e.name)] : input.activeEnvironment || !input.environments?.length ? [] : input.environments.map((e) => e.name)
  const scenarios = envNames.length ? envNames.map((name) => makeResolver({ ...expanded.wf, activeEnvironment: name }, forEachSource)) : [r]
  // Blocks without instances (count = 0, an empty for_each, inside a module call with none) are not
  // drawn; AzAPI resources take the azurerm type of their ARM type.
  const kept = expanded.wf.nodes.flatMap((n): WorkflowNode[] => {
    const counted = !!(findAttr(n.body, "count") || findAttr(n.body, "for_each"))
    if (scenarios.every((s) => (counted && s.count(n) === 0) || scaleOf(n, s) === 0)) return []
    const arm = baseType(n.type) === "azapi_resource" ? r.attr(n, "type") : undefined
    return [isStr(arm) ? { ...n, type: azapiType(arm, r.attr(n, "body.kind")) } : n]
  })
  const wf = { ...expanded.wf, nodes: kept }
  const nodes = new Map(wf.nodes.map((n) => [n.id, n]))
  const ordered = [...wf.nodes].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))
  const refs = new Map<string, Ref[]>() // node → nodes it references
  const users = new Map<string, Ref[]>() // node → nodes referencing it
  const edgesTo = new Map<string, WorkflowEdge[]>() // node → its references, with indexed attribute paths
  // A reference only in the branch of a conditional Terraform does not take is not one
  // ("route_table_id = var.private ? oci_core_route_table.nat.id : oci_core_route_table.igw.id").
  const taken = (e: WorkflowEdge) => {
    const v = e.kind === "depends_on" ? undefined : valueAt(nodes.get(e.target)!.body, e.attribute)
    if (!v?.expr?.includes("?")) return true
    const prefix = /^(module\.[^.]+\.)*/.exec(e.target)?.[0] ?? ""
    const rel = e.source.startsWith(prefix) ? e.source.slice(prefix.length) : e.source
    const address = (rel.startsWith("module.") ? rel.split(".").slice(0, 2).join(".") : rel).replace(/[.[\]]/g, "\\$&")
    const at = new RegExp(`(^|[^\\w.])${address}([^\\w-]|$)`)
    if (!at.test(v.expr)) return true
    return scenarios.some((s) => {
      const branch = s.live(v)
      return branch === undefined || at.test(branch)
    })
  }
  const edges: WorkflowEdge[] = (wf.edges ?? []).filter((e) => nodes.has(e.source) && nodes.has(e.target) && e.source !== e.target && taken(e))
  for (const e of edges) {
    const attr = e.attribute.replace(/\[[^\]]*\]/g, "")
    const dependsOn = e.kind === "depends_on"
    refs.set(e.target, [...(refs.get(e.target) ?? []), { id: e.source, attr, dependsOn }])
    users.set(e.source, [...(users.get(e.source) ?? []), { id: e.target, attr, dependsOn }])
    edgesTo.set(e.target, [...(edgesTo.get(e.target) ?? []), e])
  }
  const refAt: RefAt = (n, path) => nodes.get((edgesTo.get(n.id) ?? []).find((e) => e.kind !== "depends_on" && e.attribute === path)?.source ?? "")
  const firstCloud = PROVIDERS.find((p) => wf.nodes.some((n) => providerOfType(n.type) === p))
  const providerOf = (n: WorkflowNode) =>
    n.kind === "module" ? moduleProvider(n, r, firstCloud) : providerOfType(n.type)
  const typeOf = (n: WorkflowNode) => baseType(n.type)
  /** The resource type an unreadable module call stands for (its icon and group), from its source. */
  const moduleType = (n: WorkflowNode) => {
    const source = r.attr(n, "source")
    return isStr(source) ? moduleServiceType(source, providerOf(n)) : undefined
  }
  const categoryOf = (n: WorkflowNode): ArchCategory => {
    const p = providerOf(n)
    const t = n.kind === "module" ? moduleType(n) : n.type
    return p && t ? archCategoryOf(p, t) : "other"
  }
  const logicalOf = (n: WorkflowNode): LogicalKey => {
    const t = n.kind === "module" ? moduleType(n) : n.type
    return t ? logicalKeyOf(t, categoryOf(n)) : "modules"
  }
  const refsOf = (id: string) => (refs.get(id) ?? []).filter((x) => !x.dependsOn)
  const usersOf = (id: string) => (users.get(id) ?? []).filter((x) => !x.dependsOn)

  // Roles.
  const role = new Map<string, Role>()
  for (const n of ordered) {
    const t = n.kind === "module" ? (moduleType(n) ?? "") : typeOf(n)
    if (VPC_TYPE.test(t) && n.kind !== "module") role.set(n.id, "vpc")
    else if (SUBNET_TYPE.test(t) && n.kind !== "module") role.set(n.id, "subnet")
    else if (ACCOUNT_TYPE.test(t) && n.kind !== "module") role.set(n.id, "account")
    else if (QUIET.test(t)) role.set(n.id, "quiet")
    else if (SECURITY_GROUP.test(t) && n.kind !== "module") role.set(n.id, "sg")
    else if ((ROUTE_TABLE.test(t) || t === "google_compute_route") && n.kind !== "module") role.set(n.id, "routes")
    else if (n.kind !== "module" && (isPlumbing(providerOf(n) ?? firstCloud ?? "aws", n.type) || (n.kind === "data" && LOOKUP.test(t)))) role.set(n.id, "plumbing")
    else role.set(n.id, "card")
  }
  const isCard = (id: string) => role.get(id) === "card"

  // Owners of helpers: the card (or security group, route table) named by the most specific
  // attribute, else any referenced one, else one using it; chains of helpers resolve to the first.
  const owner = new Map<string, string>()
  // A network interface belongs to the machine using it, not to its subnet or public IP.
  for (const n of ordered) {
    if (typeOf(n) !== "azurerm_network_interface") continue
    const vm = usersOf(n.id).find((u) => /^network_interface_ids?$/.test(u.attr) && isCard(u.id))
    if (vm) owner.set(n.id, vm.id)
  }
  // Parts of another resource (see PART_OF); a certificate's DNS validation records are its own.
  const partOf = new Map<string, string>() // part → the resource it stands for in data flows
  for (const n of ordered) {
    const t = typeOf(n)
    const via = PART_OF[t]
    const parent = via && refsOf(n.id).find((x) => via.test(x.attr) && isCard(x.id))?.id
    if (parent) {
      role.set(n.id, "plumbing")
      owner.set(n.id, parent)
      partOf.set(n.id, parent)
    } else if (DNS_RECORD.test(t)) {
      const cert = refsOf(n.id).find((x) => isCard(x.id) && CERTIFICATE.test(typeOf(nodes.get(x.id)!)))?.id
      if (cert) owner.set(n.id, cert)
    }
  }
  /** Helpers made once for each of several cards (one lifecycle policy per repository): in each of them. */
  const spread = new Map<string, string[]>()
  const ownerOf = (id: string, seen = new Set<string>()): string | undefined => {
    if (isCard(id) || role.get(id) === "sg" || role.get(id) === "routes") return id
    if (role.get(id) !== "plumbing" || seen.has(id)) return undefined
    if (owner.has(id)) return owner.get(id)
    seen.add(id)
    const out = refs.get(id) ?? []
    let found: string | undefined
    for (const a of OWNER_ATTRS) {
      for (const ref of out) {
        if (ref.attr !== a && !ref.attr.startsWith(`${a}.`)) continue
        found = ownerOf(ref.id, seen)
        if (found) break
      }
      if (found) break
    }
    // A for_each over several cards ("{ api = aws_ecr_repository.api.name, … }"): one per card.
    if (!found) {
      const each = unique(out.filter((x) => x.attr === "for_each" && !x.dependsOn).map((x) => ownerOf(x.id, seen)).filter(isStr))
      if (each.length > 1) {
        spread.set(id, each)
        found = each[0]
      }
    }
    for (const a of OWNER_ATTRS) {
      for (const ref of out) {
        if (ref.attr !== a && !ref.attr.startsWith(`${a}.`)) continue
        found = ownerOf(ref.id, seen)
        if (found) break
      }
      if (found) break
    }
    // Otherwise the most significant resource it references (an ECS service over its cluster).
    const weightOf = (x: Ref) => serviceWeight(nodes.get(x.id)!.type, categoryOf(nodes.get(x.id)!))
    for (const ref of [...out].sort((a, b) => weightOf(b) - weightOf(a))) {
      if (found) break
      if (!ref.dependsOn) found = ownerOf(ref.id, seen)
    }
    for (const ref of users.get(id) ?? []) {
      if (found) break
      if (!ref.dependsOn) found = ownerOf(ref.id, seen)
    }
    if (found) owner.set(id, found)
    return found
  }
  /** The card a node is drawn as or in (through plumbing it belongs to). */
  const cardOf = (id: string) => (role.get(id) === "plumbing" ? ownerOf(id) : isCard(id) ? id : undefined)
  /** Subnets a node references, directly or through a helper (a NIC, a DB subnet group). */
  const subnetRefs = (id: string): string[] => {
    const out: string[] = []
    for (const ref of refsOf(id)) {
      if (INTEGRATION_ATTR.test(ref.attr)) continue
      if (role.get(ref.id) === "subnet") out.push(ref.id)
      else if (role.get(ref.id) === "plumbing" && !/swift_connection$/.test(typeOf(nodes.get(ref.id)!))) {
        for (const r2 of refsOf(ref.id)) if (role.get(r2.id) === "subnet" && !INTEGRATION_ATTR.test(r2.attr)) out.push(r2.id)
      }
    }
    return unique(out).sort()
  }
  // Resources drawn inside the card using them (see ABSORB).
  const absorbed = new Set<string>()
  const absorb = (id: string, into: string) => {
    role.set(id, "plumbing")
    owner.set(id, into)
    absorbed.add(id)
  }
  for (const n of ordered) {
    if (!isCard(n.id) || n.kind === "data") continue
    const t = typeOf(n)
    const ways = ABSORB.find(([re]) => re.test(t))?.[1]
    if (!ways) continue
    let to: string | undefined
    for (const way of ways) {
      const found = [
        ...(way.user || way.userType
          ? usersOf(n.id).filter((u) => (!way.user || way.user.test(u.attr)) && (!way.userType || way.userType.test(typeOf(nodes.get(u.id)!))))
          : []),
        ...(way.ref || way.refType
          ? refsOf(n.id).filter((x) => (!way.ref || way.ref.test(x.attr)) && (!way.refType || way.refType.test(typeOf(nodes.get(x.id)!))))
          : []),
      ]
      const cards = unique(found.map((x) => cardOf(x.id)).filter((c): c is string => !!c && c !== n.id && isCard(c)))
      if (way.single ? cards.length === 1 : cards.length > 0) to = cards[0]
      if (to) break
    }
    if (!to) continue
    // A node pool in a subnet of its own is drawn there, apart from its cluster.
    if (NODE_POOL.test(t)) {
      const own = subnetRefs(n.id)
      if (own.length && own.join() !== subnetRefs(to).join()) continue
    }
    absorb(n.id, to)
  }
  // Azure sub-resources without a location of their own, naming their parent through
  // "<parent type>_id" (a cache's access policy assignment, a storage account's static website), are
  // parts of it; ones placed in a subnet of their own stay cards.
  for (const n of ordered) {
    const t = typeOf(n)
    if (!isCard(n.id) || n.kind !== "resource" || !t.startsWith("azurerm_") || findAttr(n.body, "location") || subnetRefs(n.id).length) continue
    const parent = refsOf(n.id).find((x) => {
      const pt = typeOf(nodes.get(x.id)!)
      return isCard(x.id) && t.startsWith(`${pt}_`) && x.attr === `${pt.slice("azurerm_".length)}_id`
    })?.id
    if (!parent) continue
    role.set(n.id, "plumbing")
    owner.set(n.id, parent)
    partOf.set(n.id, parent)
  }
  // A GCP load balancer is one card: its forwarding rule with the proxies, URL maps, backend
  // services, NEGs, health checks, certificates and addresses behind it. Forwarding rules sharing
  // any of them (HTTP and HTTPS on one IP) are one load balancer, named after the rule serving
  // traffic (HTTPS), not the one redirecting HTTP to it.
  const serves = (n: WorkflowNode) =>
    refsOf(n.id).some((x) => /target_(https|ssl)_proxy$/.test(typeOf(nodes.get(x.id)!))) && !/^"?80"?$/.test(String(r.attr(n, "port_range") ?? "")) ? 0 : 1
  const rules = ordered.filter((n) => /^google_compute_(global_)?forwarding_rule$/.test(typeOf(n))).sort((a, b) => serves(a) - serves(b) || (a.id < b.id ? -1 : 1))
  const ruleRank = new Map(rules.map((n, i) => [n.id, i]))
  for (const n of rules) {
    if (!isCard(n.id)) continue
    const chain = new Set<string>()
    const queue = [n.id]
    let into: string | undefined
    while (queue.length) {
      const cur = queue.shift()!
      for (const x of [...refsOf(cur), ...(cur === n.id ? [] : usersOf(cur).filter((u) => /forwarding_rule$/.test(typeOf(nodes.get(u.id)!))))]) {
        const xt = typeOf(nodes.get(x.id)!)
        if (x.id === n.id || chain.has(x.id) || !GCP_LB_PART.test(xt)) continue
        if (absorbed.has(x.id) && /forwarding_rule$/.test(typeOf(nodes.get(owner.get(x.id)!)!))) into ??= owner.get(x.id)
        if (absorbed.has(x.id) || (role.get(x.id) !== "card" && role.get(x.id) !== "plumbing")) continue
        if (/forwarding_rule$/.test(xt) && (ruleRank.get(x.id) ?? -1) < ruleRank.get(n.id)!) continue
        chain.add(x.id)
        queue.push(x.id)
      }
    }
    for (const id of chain) absorb(id, into ?? n.id)
    if (into) absorb(n.id, into)
  }
  // Module calls: an instance is drawn as the services it creates. Its heaviest resource is its card;
  // the heaviest resource of each other main service keeps a card too; everything else folds into
  // the card of its service (or the instance's card), like plumbing.
  const members = new Map<string, WorkflowNode[]>()
  /** The cards a module instance is drawn as (titled after its call). */
  const moduleCards = new Set<string>()
  for (const n of ordered) {
    const inst = instanceOf.get(n.id)
    if (inst) members.set(inst.callId, [...(members.get(inst.callId) ?? []), n])
  }
  for (const list of members.values()) {
    const inside = new Set(list.map((n) => n.id))
    const candidates = list.filter((n) => isCard(n.id) && !NETWORK_FABRIC.test(typeOf(n)))
    if (candidates.length === 0) continue
    // Bastions and jump hosts only reach the service; data sources only describe it.
    const weight = (n: WorkflowNode) =>
      serviceWeight(n.kind === "module" ? (moduleType(n) ?? "") : n.type, categoryOf(n)) - (n.kind === "data" ? 5 : 0) - (/bastion|jump/i.test(n.name) ? 10 : 0)
    // Equal weights: the most connected resource is the service (the queue, not its DLQ; the distribution, not its functions).
    const degree = (n: WorkflowNode) => [...(refs.get(n.id) ?? []), ...(users.get(n.id) ?? [])].filter((x) => !x.dependsOn).length
    const ranked = [...candidates].sort((a, b) => weight(b) - weight(a) || degree(b) - degree(a) || (a.id < b.id ? -1 : 1))
    const primary = ranked[0]
    const mains = new Map<string, WorkflowNode>([[serviceFamily(primary), primary]])
    for (const n of ranked) if (n.kind !== "data" && weight(n) >= MAIN_SERVICE && !mains.has(serviceFamily(n))) mains.set(serviceFamily(n), n)
    for (const n of mains.values()) moduleCards.add(n.id)
    for (const n of candidates) {
      const main = mains.get(serviceFamily(n)) ?? primary
      if (moduleCards.has(n.id)) continue
      // Machines and node pools in a subnet of their own (a bastion, worker nodes) keep their card there.
      const category = categoryOf(n)
      const own = subnetRefs(n.id).join()
      if ((category === "compute" || category === "containers") && own && own !== subnetRefs(main.id).join()) continue
      role.set(n.id, "plumbing")
      owner.set(n.id, main.id)
      // Flows of a part of the same service (a DLQ, a CloudFront function) are the service's.
      if (serviceFamily(n) === serviceFamily(main)) absorbed.add(n.id)
    }
    // Helpers inside the module belong to its services, not to whatever they reference outside it.
    for (const n of list) {
      if (role.get(n.id) !== "plumbing") continue
      const o = ownerOf(n.id)
      if (!o || !inside.has(o)) owner.set(n.id, primary.id)
    }
  }
  // Helpers with nothing to belong to: utility providers and lookups are not drawn, others are cards.
  for (const n of ordered) {
    if (role.get(n.id) !== "plumbing" || ownerOf(n.id)) continue
    role.set(n.id, !providerOf(n) || n.kind === "data" ? "quiet" : "card")
  }
  // Owners folded later (into a module's card) hand their helpers on to their own owner.
  const handOn = (o: string) => {
    let cur = o
    for (let i = 0; i < 8 && role.get(cur) === "plumbing" && owner.has(cur); i++) cur = owner.get(cur)!
    return cur
  }
  for (const [id, o] of owner) owner.set(id, handOn(o))
  for (const [id, list] of spread) spread.set(id, unique(list.map(handOn)))
  const folded = new Map<string, WorkflowNode[]>()
  for (const n of ordered) {
    if (role.get(n.id) !== "plumbing") continue
    for (const o of spread.get(n.id) ?? (owner.has(n.id) ? [owner.get(n.id)!] : [])) folded.set(o, [...(folded.get(o) ?? []), n])
  }
  /** The card (or security group, route table) standing for a node, if any. */
  const rep = (id: string) => (isCard(id) ? id : role.get(id) === "plumbing" ? owner.get(id) : undefined)
  /** The card standing for a node in data flows: itself, or the card that absorbed it. */
  const flowCard = (id: string) => (isCard(id) ? id : absorbed.has(id) ? owner.get(id) : undefined)
  /** The card a part (see PART_OF) stands for when something references it. */
  const partCard = (id: string) => (partOf.has(id) ? rep(id) : undefined)
  const vpcOf = (id: string) => refsOf(id).find((x) => role.get(x.id) === "vpc")?.id

  // Security groups, NSGs, firewall rules and security lists: a line on every card (or subnet) they
  // apply to; on their VPC when nothing uses them.
  const security = new Map<string, string[]>() // card → lines
  const subnetSecurity = new Map<string, string[]>() // subnet → lines
  const vpcLines = new Map<string, string[]>() // VPC node → lines
  const unusedSgs = new Map<string, string[]>() // VPC node → names
  const push = (m: Map<string, string[]>, k: string, line: string) => m.set(k, unique([...(m.get(k) ?? []), line]))
  /** Network tags and service accounts of a card (and its folded instance template) for GCP firewall rules. */
  const tagsOf = (c: string) =>
    [nodes.get(c)!, ...(folded.get(c) ?? [])].flatMap((n) => [...r.strings(n, "tags"), ...r.strings(n, "node_config.tags")])
  const accountsOf = (c: string) =>
    [c, ...(folded.get(c) ?? []).map((n) => n.id)].flatMap((id) => refsOf(id).filter((x) => /service_account(\.email)?$/.test(x.attr)).map((x) => x.id))
  for (const n of ordered) {
    if (role.get(n.id) !== "sg") continue
    const t = typeOf(n)
    const line = (() => {
      const summary = securitySummary(n, (folded.get(n.id) ?? []).filter((h) => SECURITY_RULE.test(typeOf(h))), r)
      return summary ? `${securityName(n, r)}: ${summary}` : securityName(n, r)
    })()
    const cards = new Set<string>()
    const subnets = new Set<string>()
    const hit = (id: string) => {
      if (role.get(id) === "subnet") subnets.add(id)
      else {
        const c = rep(id)
        if (c && isCard(c)) cards.add(c)
      }
    }
    if (t === "google_compute_firewall") {
      const target = firewallTargets(n, r)
      const accounts = target.serviceAccounts ? refsOf(n.id).filter((x) => x.attr === "target_service_accounts").map((x) => x.id) : []
      for (const c of ordered.filter((x) => isCard(x.id))) {
        if (target.tags.some((tag) => tagsOf(c.id).includes(tag)) || accountsOf(c.id).some((a) => accounts.includes(a))) cards.add(c.id)
      }
      if (cards.size === 0) {
        const vpc = vpcOf(n.id)
        const scope = target.tags.length ? ` → tag ${target.tags.join(", ")}` : ""
        if (vpc) push(vpcLines, vpc, `${line.replace(/^FW /, "firewall ")}${scope}`)
        else role.set(n.id, "card")
        continue
      }
    } else if (/default_security_list$/.test(t)) {
      // The VCN's default security list: on the subnets using it, else on the VCN.
      const vcn = refsOf(n.id).find((x) => role.get(x.id) === "vpc")?.id
      if (vcn) for (const u of usersOf(vcn)) if (role.get(u.id) === "subnet" && u.attr === "security_list_ids") subnets.add(u.id)
      if (!subnets.size) {
        if (vcn) push(vpcLines, vcn, line)
        continue
      }
    } else if (t === "aws_default_security_group") {
      // The VPC's default security group (nothing uses it explicitly): what it allows, on the VPC.
      const vpc = vpcOf(n.id)
      const summary = securitySummary(n, [], r)
      if (vpc) {
        push(vpcLines, vpc, `default SG: ${summary ?? (n.body.blocks.some((b) => b.type === "egress") ? "no inbound" : "deny all")}`)
        continue
      }
    }
    for (const u of usersOf(n.id)) {
      const un = nodes.get(u.id)!
      const ut = typeOf(un)
      if (SECURITY_RULE.test(ut)) continue
      if (/association$/.test(ut)) {
        for (const x of refsOf(u.id)) if (x.id !== n.id) hit(x.id)
      } else hit(u.id)
    }
    for (const c of cards) push(security, c, line)
    for (const s of subnets) push(subnetSecurity, s, line)
    if (cards.size || subnets.size || t === "google_compute_firewall") continue
    const vpc = vpcOf(n.id)
    const nameAttr = r.attr(n, t.startsWith("oci_") ? "display_name" : "name")
    if (vpc) unusedSgs.set(vpc, [...(unusedSgs.get(vpc) ?? []), isStr(nameAttr) ? nameAttr : n.name])
    else role.set(n.id, "card")
  }

  // Route tables: a "routes: 0.0.0.0/0 → IGW" line on the subnets associated with them (on the VPC
  // when none is). GCP routes and Azure NAT gateway associations add theirs too.
  const routeLinesOf = new Map<string, string[]>() // subnet or VPC node → routes
  const tierHint = new Map<string, SubnetTier>()
  const gatewayEdges: [string, string][] = [] // [internet gateway node, public subnet node]
  /** Subnets associated with a route table without any route: nothing leaves them (isolated). */
  const noRoutes = new Set<string>()
  for (const n of ordered) {
    if (role.get(n.id) !== "routes") continue
    const t = typeOf(n)
    const own = usersOf(n.id)
    const routes = own.filter((u) => ROUTE.test(typeOf(nodes.get(u.id)!))).map((u) => nodes.get(u.id)!)
    const lines = routeLines(n, routes, r, refAt)
    if (t === "google_compute_route") {
      const vpc = refsOf(n.id).find((x) => role.get(x.id) === "vpc")?.id
      if (vpc) for (const l of lines) push(routeLinesOf, vpc, l)
      else role.set(n.id, "card")
      continue
    }
    const subnets = unique([
      ...own.filter((u) => /route_table_(association|attachment)$/.test(typeOf(nodes.get(u.id)!))).flatMap((u) => refsOf(u.id).filter((x) => role.get(x.id) === "subnet").map((x) => x.id)),
      ...own.filter((u) => role.get(u.id) === "subnet").map((u) => u.id),
    ])
    const vpc = vpcOf(n.id)
    // The VCN's default route table: the subnets naming it through their VCN.
    if (/default_route_table$/.test(t) && vpc) for (const u of usersOf(vpc)) if (role.get(u.id) === "subnet" && u.attr === "route_table_id") subnets.push(u.id)
    const at = subnets.length ? unique(subnets) : vpc ? [vpc] : []
    if (at.length === 0) role.set(n.id, "card")
    for (const s of at) for (const l of lines) push(routeLinesOf, s, l)
    const empty = routes.length === 0 && !n.body.blocks.some((b) => /^(route|route_rules|dynamic)$/.test(b.type)) && !findAttr(n.body, "route") && !findAttr(n.body, "route_rules")
    if (empty) for (const s of subnets) noRoutes.add(s)
    // Tiers: a subnet routing to an internet gateway is public; to a NAT gateway, private.
    const gateways = unique([...refsOf(n.id), ...routes.flatMap((x) => refsOf(x.id))].map((x) => x.id))
    for (const g of gateways) {
      const gt = typeOf(nodes.get(g)!)
      for (const s of subnets) {
        if (INTERNET_GATEWAY.test(gt)) {
          tierHint.set(s, "public")
          gatewayEdges.push([g, s])
        } else if (NAT_GATEWAY.test(gt) && tierHint.get(s) !== "public") tierHint.set(s, "private")
      }
    }
  }
  // Azure: a subnet associated with a NAT gateway goes out through it.
  const natSubnets = new Map<string, string[]>() // NAT gateway → subnets
  for (const n of ordered) {
    if (typeOf(n) !== "azurerm_subnet_nat_gateway_association") continue
    const nat = refsOf(n.id).find((x) => typeOf(nodes.get(x.id)!) === "azurerm_nat_gateway")?.id
    const subnet = refsOf(n.id).find((x) => role.get(x.id) === "subnet")?.id
    if (!nat || !subnet) continue
    natSubnets.set(nat, [...(natSubnets.get(nat) ?? []), subnet])
    push(routeLinesOf, subnet, "0.0.0.0/0 → NAT")
    if (tierHint.get(subnet) !== "public") tierHint.set(subnet, "private")
  }
  const routesLine = (ids: string[]) => {
    const lines = unique(ids.flatMap((id) => routeLinesOf.get(id) ?? []))
    return lines.length ? `routes: ${lines.join(", ")}` : undefined
  }

  // ---------------------------------------------------------- groups
  const groups = new Map<string, ArchGroup>()
  const addGroup = (g: ArchGroup) => {
    if (!groups.has(g.id)) groups.set(g.id, g)
    return g.id
  }
  const providerRegion = new Map<string, string>() // "aws" / "aws.west" → region
  for (const p of PROVIDERS) {
    for (const b of (wf.providers ?? []).filter((x) => x.name === PROVIDER_BLOCK[p])) {
      const alias = r.value(b.body.attrs.find((a) => a.name === "alias")?.value)
      const region = r.value(b.body.attrs.find((a) => a.name === "region")?.value)
      if (isStr(region)) providerRegion.set(isStr(alias) ? `${p}.${alias}` : p, region)
    }
  }
  const gcpProject = (() => {
    const b = (wf.providers ?? []).find((x) => x.name === "google")
    const v = b ? r.value(b.body.attrs.find((a) => a.name === "project")?.value) : undefined
    return isStr(v) ? v : undefined
  })()

  const cloudGroup = (p: ArchProvider) => addGroup({ id: `group:cloud:${p}`, kind: "cloud", label: PROVIDER_NAME[p], provider: p })
  const regionsUsed: string[] = []
  /** The parent a sub-resource sits with (a queue's storage account, a database's server). */
  const parentOf = (n: WorkflowNode) =>
    refsOf(n.id).find((x) => PARENT_ATTR.test(x.attr) && x.attr !== "location" && role.get(x.id) !== "account" && role.get(x.id) !== "quiet")?.id
  /** A resource's region; "" for regional with an unknown region; undefined for global services. */
  const regionOf = (n: WorkflowNode, p: ArchProvider, depth = 0): string | undefined => {
    const t = typeOf(n)
    const own = r.attr(n, "region")
    if (isStr(own)) return own
    if (p === "aws") {
      if (AWS_GLOBAL.test(`${t}_`)) return undefined
      const via = findAttr(n.body, "provider")?.expr?.trim()
      return (via && providerRegion.get(via)) ?? providerRegion.get("aws") ?? ""
    }
    if (p === "gcp") {
      const loc = r.attr(n, "location")
      if (isStr(loc) && /^[a-z]+-[a-z]+\d+$/.test(loc)) return loc
      const zone = r.attr(n, "zone")
      if (isStr(zone) && /^[a-z]+-[a-z]+\d+-[a-z]$/.test(zone)) return zone.replace(/-[a-z]$/, "")
      return GCP_GLOBAL.test(t) ? undefined : (providerRegion.get("gcp") ?? "")
    }
    if (p === "azure") {
      const loc = r.attr(n, "location")
      if (isStr(loc)) return loc.toLowerCase().replace(/\s+/g, "")
      // "location = azurerm_resource_group.main.location": where that one is.
      const via = refsOf(n.id).find((x) => x.attr === "location")
      if (via && depth < 4) return regionOf(nodes.get(via.id)!, p, depth + 1) ?? ""
      if (findAttr(n.body, "location")) return ""
      const parent = parentOf(n)
      if (parent && depth < 4) return regionOf(nodes.get(parent)!, p, depth + 1)
      if (AZURE_GLOBAL.test(t)) return undefined
      // Otherwise where its resource group is.
      const rg = refsOf(n.id).find((x) => role.get(x.id) === "account")
      return rg && depth < 4 ? (regionOf(nodes.get(rg.id)!, p, depth + 1) ?? "") : ""
    }
    return providerRegion.get("oci") ?? ""
  }
  const accountOf = (n: WorkflowNode, p: ArchProvider, depth = 0): string | undefined => {
    if (p === "azure") {
      const rg = refsOf(n.id).find((x) => (x.attr === "resource_group_name" || x.attr === "parent_id") && role.get(x.id) === "account")
      if (rg) return accountGroup(nodes.get(rg.id)!)
      const name = r.attr(n, "resource_group_name")
      const byName = isStr(name)
        ? ordered.find((x) => role.get(x.id) === "account" && typeOf(x) === "azurerm_resource_group" && r.attr(x, "name") === name)
        : undefined
      if (byName) return accountGroup(byName)
      // A sub-resource (a queue in its storage account) sits where its parent does.
      const parent = parentOf(n)
      return parent && depth < 4 ? accountOf(nodes.get(parent)!, p, depth + 1) : undefined
    }
    if (p === "oci") {
      const c = refsOf(n.id).find((x) => x.attr === "compartment_id" && role.get(x.id) === "account")
      if (c) return accountGroup(nodes.get(c.id)!)
      const expr = findAttr(n.body, "compartment_id")?.expr
      if (expr) {
        // The compartment (or tenancy) the design is given: one group per known value.
        const value = r.attr(n, "compartment_id")
        const tenancy = /tenancy/i.test(expr)
        return addGroup({
          id: `group:account:oci:${isStr(value) ? value : tenancy ? "tenancy" : "compartment"}`,
          parent: cloudGroup("oci"),
          kind: "account",
          label: tenancy ? "Tenancy" : "Compartment",
          provider: "oci",
        })
      }
      const parent = parentOf(n)
      return parent && depth < 4 ? accountOf(nodes.get(parent)!, p, depth + 1) : undefined
    }
    if (p === "gcp") {
      const project = r.attr(n, "project")
      const id = isStr(project) ? project : gcpProject
      return addGroup({ id: `group:account:gcp:${id ?? ""}`, parent: cloudGroup("gcp"), kind: "account", label: id ? `Project · ${id}` : "Project", provider: "gcp" })
    }
    return undefined
  }
  const accountGroup = (n: WorkflowNode): string => {
    const p = providerOf(n)!
    const name = r.attr(n, "name")
    const azure = typeOf(n) === "azurerm_resource_group"
    return addGroup({
      id: `group:account:${n.id}`,
      parent: cloudGroup(p),
      kind: "account",
      label: `${azure ? "Resource group" : "Compartment"}${isStr(name) ? ` · ${name}` : ` · ${n.name}`}`,
      provider: p,
      nodeId: n.id,
    })
  }
  const regionGroup = (p: ArchProvider, region: string, account: string | undefined) => {
    if (region && !regionsUsed.includes(region)) regionsUsed.push(region)
    return addGroup({
      id: account && p !== "aws" ? `group:region:${p}:${region}:${account}` : `group:region:${p}:${region}`,
      parent: account ?? cloudGroup(p),
      kind: "region",
      label: region ? `Region · ${region}` : "Region",
      provider: p,
    })
  }
  /** The container a regional or global service sits in (region, account or cloud; GCP's Global group). */
  const scopeOf = (n: WorkflowNode, p: ArchProvider): string => {
    const account = accountOf(n, p)
    const region = regionOf(n, p)
    if (region !== undefined) return regionGroup(p, region, account)
    if (p === "gcp") return addGroup({ id: `group:region:gcp:global:${account}`, parent: account, kind: "region", label: "Global", provider: p })
    return account ?? cloudGroup(p)
  }

  // VPCs and subnets.
  const vpcGroup = new Map<string, string>() // vpc node → group
  for (const n of ordered) {
    if (role.get(n.id) !== "vpc") continue
    const p = providerOf(n)!
    const cidr = strs(r.attr(n, "cidr_block") ?? r.attr(n, "address_space") ?? r.attr(n, "cidr_blocks"))
    // A module's VPC ("module.vcn_hub.oci_core_vcn.vcn") is named after its call.
    const inst = instanceOf.get(n.id)
    const name = n.label || (inst && /^(this|main|vpc|vcn|vnet|network|default)$/.test(n.name) ? moduleTitle(inst) : humanize(n.name))
    const label = p === "azure" ? "Virtual network" : p === "gcp" ? "VPC network" : p === "oci" ? "VCN" : "VPC"
    let parent = scopeOf(n, p)
    if (p === "gcp") {
      // A VPC network is global: drawn in the region of its subnets when they share one.
      const subnets = usersOf(n.id).filter((u) => role.get(u.id) === "subnet")
      // Without subnets: the region of what it holds (a VPC connector, a router).
      const inside = subnets.length ? subnets : usersOf(n.id).filter((u) => isCard(u.id))
      const regions = unique(inside.map((u) => regionOf(nodes.get(u.id)!, p)).filter(isStr))
      const account = accountOf(n, p)
      parent = regions.length > 1 ? account! : regionGroup(p, regions[0] ?? providerRegion.get("gcp") ?? "", account)
    }
    vpcGroup.set(
      n.id,
      addGroup({
        id: `group:vpc:${n.id}`,
        parent,
        kind: "vpc",
        label: `${label} · ${name}${(scaleOf(n) ?? 1) > 1 ? ` ×${scaleOf(n)}` : ""}`,
        sublabel: sublabel(
          cidr.join(", "),
          routesLine([n.id]),
          ...(vpcLines.get(n.id) ?? []),
          unusedSgs.has(n.id) ? `unused security group${unusedSgs.get(n.id)!.length > 1 ? "s" : ""}: ${unusedSgs.get(n.id)!.join(", ")}` : undefined,
        ),
        provider: p,
        nodeId: n.id,
      }),
    )
  }
  // Subnets referenced together by one resource (a load balancer, a database subnet group) are drawn
  // as one tier group: union-find over subnets.
  const subnetSet = new Map<string, string>()
  const find = (s: string): string => {
    const p = subnetSet.get(s) ?? s
    if (p === s) return s
    const root = find(p)
    subnetSet.set(s, root)
    return root
  }
  const union = (a: string, b: string) => {
    const ra = find(a)
    const rb = find(b)
    if (ra !== rb) subnetSet.set(ra < rb ? rb : ra, ra < rb ? ra : rb)
  }
  const cardSubnets = new Map<string, string[]>()
  /** Subnets (or VPCs) a serverless service reaches into: VNet integration, Direct VPC egress. */
  const integrations = new Map<string, string[]>()
  for (const n of ordered) {
    if (!isCard(n.id)) continue
    // A service reached through private endpoints (and in no subnet of its own) is drawn in their
    // subnet, the address it has in the network.
    const own = subnetRefs(n.id)
    const subs = own.length ? own : unique((folded.get(n.id) ?? []).filter((h) => typeOf(h) === "azurerm_private_endpoint").flatMap((h) => subnetRefs(h.id))).sort()
    const reach = [n.id, ...(folded.get(n.id) ?? []).map((h) => h.id)].flatMap((id) =>
      refsOf(id)
        .filter((x) => (INTEGRATION_ATTR.test(x.attr) || /swift_connection$/.test(typeOf(nodes.get(id)!))) && (role.get(x.id) === "subnet" || role.get(x.id) === "vpc"))
        .map((x) => x.id),
    )
    if (reach.length) integrations.set(n.id, unique(reach))
    if (subs.length === 0) continue
    cardSubnets.set(n.id, subs)
    for (const s of subs.slice(1)) if (vpcOf(s) === vpcOf(subs[0])) union(subs[0], s)
  }
  const tierOf = (s: WorkflowNode): SubnetTier => {
    const hint = tierHint.get(s.id)
    if (hint) return hint
    if (r.attr(s, "map_public_ip_on_launch") === true) return "public"
    if (r.attr(s, "prohibit_public_ip_on_vnic") === true) return "private"
    const name = `${s.name} ${s.label ?? ""} ${String(r.attr(s, "name") ?? r.attr(s, "display_name") ?? "")}`.toLowerCase()
    if (/public|dmz|web|ingress|frontend/.test(name)) return "public"
    if (/private|app|db|data|internal|backend|isolated/.test(name)) return "private"
    return "unknown"
  }
  const subnetGroup = new Map<string, string>() // subnet node → group
  const classes = new Map<string, string[]>()
  for (const n of ordered) if (role.get(n.id) === "subnet") classes.set(find(n.id), [...(classes.get(find(n.id)) ?? []), n.id])
  for (const [root, members] of classes) {
    const subnets = members.map((m) => nodes.get(m)!)
    const first = nodes.get(root)!
    const p = providerOf(first)!
    const tiers = unique(subnets.map(tierOf))
    let tier: SubnetTier = tiers.length === 1 ? tiers[0] : tiers.includes("public") ? "public" : "private"
    let instances = 0
    const cidrs: string[] = []
    const azs: string[] = []
    for (const s of subnets) {
      instances += r.count(s) ?? 1
      cidrs.push(...strs(r.eachAttr(s, "cidr_block") ?? r.attr(s, "cidr_block") ?? r.attr(s, "ip_cidr_range") ?? r.attr(s, "address_prefixes")))
      azs.push(...strs(r.eachAttr(s, "availability_zone") ?? r.attr(s, "availability_zone") ?? r.attr(s, "availability_domain") ?? r.attr(s, "region")))
    }
    // A subnet with no route out at all is isolated (a data tier).
    const isolated = tier !== "public" && members.every((m) => noRoutes.has(m) && !tierHint.has(m) && !routeLinesOf.get(m)?.length)
    if (isolated) tier = "private"
    const tierName = tier === "public" ? "Public subnet" : isolated ? "Isolated subnet" : tier === "private" ? "Private subnet" : "Subnet"
    const title = members.length === 1 ? first.label || (tier === "unknown" ? `Subnet · ${humanize(first.name)}` : tierName) : `${tierName}s`
    const count = instances > 1 ? ` ×${instances}` : ""
    const vpc = vpcOf(root)
    const parentVpc = vpc ? vpcGroup.get(vpc) : undefined
    const azList = unique(azs)
    let parent = parentVpc ?? scopeOf(first, p)
    if (parentVpc && (p === "aws" || p === "oci") && azList.length === 1 && instances === 1 && (p === "aws" || findAttr(first.body, "availability_domain"))) {
      const zone = p === "aws" ? "Availability Zone" : "Availability domain"
      parent = addGroup({ id: `group:az:${vpc}:${azList[0]}`, parent: parentVpc, kind: "az", label: `${zone} · ${azList[0]}`, provider: p })
    }
    const where = [unique(cidrs).join(", "), parent.startsWith("group:az:") || p === "gcp" ? "" : azList.join(", ")].filter(Boolean).join(" · ")
    const gid = addGroup({
      id: `group:subnet:${root}`,
      parent,
      kind: "subnet",
      label: `${title}${count}`,
      sublabel: sublabel(where, routesLine(members), ...unique(members.flatMap((m) => subnetSecurity.get(m) ?? []))),
      provider: p,
      tier,
      nodeId: members.length === 1 ? root : undefined,
    })
    for (const m of members) subnetGroup.set(m, gid)
  }
  // Single subnets with the same title side by side in a VNet ("Private subnet" for app, integration
  // and data) are told apart by their names, without the words all their names share
  // ("tomcat_vcn_subnet_app", "tomcat_vcn_subnet_db" → "Private subnet · App", "· DB").
  const subnetGroups = [...groups.values()].filter((g) => g.kind === "subnet" && g.nodeId)
  const titles = new Map(subnetGroups.map((g) => [g, g.label.split(" ×")[0]]))
  const words = (g: ArchGroup) => nodes.get(g.nodeId!)!.name.split(/[_-]+/).filter(Boolean)
  for (const g of subnetGroups) {
    const node = nodes.get(g.nodeId!)!
    const title = titles.get(g)!
    const siblings = subnetGroups.filter((o) => o.parent === g.parent)
    if (node.label || !siblings.some((o) => o !== g && titles.get(o) === title)) continue
    const own = words(g)
    let shared = 0
    while (shared < own.length - 1 && siblings.every((o) => words(o)[shared]?.toLowerCase() === own[shared].toLowerCase())) shared++
    const rest = own.slice(shared)
    const name = humanize((rest.length > 1 ? rest.filter((w) => !/^(subnet|sn)$/i.test(w)) : rest).join("_"))
    if (!title.toLowerCase().includes(name.toLowerCase())) g.label = g.label.replace(title, `${title} · ${name}`)
  }

  // ---------------------------------------------------------- cards
  const cards: ArchCard[] = []
  const cardIds = new Set<string>()
  const placeCard = (n: WorkflowNode): string | undefined => {
    const p = providerOf(n)
    const category = categoryOf(n)
    const subs = cardSubnets.get(n.id)
    if (subs?.length) return subnetGroup.get(subs[0])
    if (p && (category === "networking" || category === "security")) {
      const vpc = refsOf(n.id).find((x) => role.get(x.id) === "vpc")
      if (vpc) return vpcGroup.get(vpc.id)
      // Through its helpers: a private DNS zone's VNet link, a NAT gateway's subnet association.
      const nat = natSubnets.get(n.id)?.[0]
      const viaHelper = nat ? vpcOf(nat) : (folded.get(n.id) ?? []).map((h) => vpcOf(h.id)).find(isStr)
      if (viaHelper && vpcGroup.has(viaHelper)) return vpcGroup.get(viaHelper)
    }
    if (!p) {
      const prefix = n.kind === "module" ? "modules" : (/^([a-z0-9]+)_/.exec(typeOf(n))?.[1] ?? "other")
      return addGroup({ id: `group:other:${prefix}`, kind: "other", label: prefix === "modules" ? "Modules" : humanize(prefix) })
    }
    const scope = scopeOf(n, p)
    const key = logicalOf(n)
    return addGroup({ id: `group:logical:${scope}:${key}`, parent: scope, kind: "logical", label: LOGICAL[key], provider: p })
  }
  const sortedCards = ordered
    .filter((n) => isCard(n.id))
    .sort((a, b) => {
      const ka = LOGICAL_ORDER.indexOf(logicalOf(a))
      const kb = LOGICAL_ORDER.indexOf(logicalOf(b))
      return ka - kb || (a.type < b.type ? -1 : a.type > b.type ? 1 : 0) || (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
    })
  for (const n of sortedCards) {
    const helpers = folded.get(n.id) ?? []
    const facts = helpers.map((h) => foldedFact(h, n, r)).filter(isStr)
    const counts = new Map<string, number>()
    for (const h of helpers) {
      // Associations, NICs and a Front Door's own routes say nothing a reader needs, nor do the
      // proxies, URL maps and backend services of a GCP load balancer or a certificate's validation
      // records (its "DNS validated" fact says it).
      if (h.kind === "data" || !providerOf(h) || foldedFact(h, n, r) || QUIET_FOLD.test(typeOf(h)) || (GCP_LB_PART.test(typeOf(h)) && LOAD_BALANCER.test(typeOf(n))) || (DNS_RECORD.test(typeOf(h)) && CERTIFICATE.test(typeOf(n)))) continue
      const label = opts.labelOf?.(h.type) || typeLabel(h.type)
      // A helper spread over several cards counts its instances among them.
      counts.set(label, (counts.get(label) ?? 0) + Math.max(1, Math.round((r.count(h) ?? 1) / (spread.get(h.id)?.length ?? 1))))
    }
    const foldedText = [...counts].map(([label, c]) => (c > 1 ? `${c} ${plural(label)}` : label)).join(", ")
    // A service's task definition, plan or instance template sizes it.
    const sizing = helpers.filter((h) => SIZING.test(typeOf(h))).flatMap((h) => detailsOf(h, r, scaleOf(h)))
    const parent = placeCard(n)
    const inst = instanceOf.get(n.id)
    const source = n.kind === "module" ? r.attr(n, "source") : undefined
    cards.push({
      id: n.id,
      parent,
      nodeId: inst?.callId ?? n.id,
      provider: providerOf(n),
      type: n.kind === "module" ? (moduleType(n) ?? n.type) : n.type,
      kind: n.kind,
      category: categoryOf(n),
      label: moduleCards.has(n.id) && inst ? moduleTitle(inst) : n.kind === "module" ? humanize(n.name) : n.label || undefined,
      name: moduleCards.has(n.id) || n.kind === "module" ? "" : n.name,
      details: unique([...(isStr(source) ? [`module · ${source}`] : []), ...detailsOf(n, r, scaleOf(n)), ...sizing, ...facts]),
      folded: foldedText ? `+ ${foldedText}` : undefined,
      security: security.get(n.id),
      onBorder: (INTERNET_GATEWAY.test(typeOf(n)) && parent?.startsWith("group:vpc:")) || undefined,
      existing: n.kind === "data" || undefined,
    })
    cardIds.add(n.id)
  }

  // Three or more cards of one kind in a container form a labelled sub-group ("ECS services (6)").
  const sameKind = new Map<string, ArchCard[]>()
  const kindGroup = new Map<string, string>() // card → its sub-group
  for (const c of cards) if (c.parent && !c.onBorder) sameKind.set(`${c.parent}|${c.type}`, [...(sameKind.get(`${c.parent}|${c.type}`) ?? []), c])
  for (const list of sameKind.values()) {
    if (list.length < 3) continue
    const { parent, type, provider } = list[0]
    const gid = addGroup({ id: `group:kind:${parent}:${type}`, parent, kind: "logical", label: `${plural(typeLabel(type))} (${list.length})`, provider, packed: true })
    for (const c of list) {
      c.parent = gid
      kindGroup.set(c.id, gid)
    }
  }
  // A provider's global services and its regional ones of one kind must not read as two groups of
  // the same name ("Identity & security" for IAM roles and for KMS): the global one says what it
  // holds ("IAM") or that it is global.
  const logicalGroups = [...groups.values()].filter((g) => g.id.startsWith("group:logical:"))
  const logicalLabel = new Map(logicalGroups.map((g) => [g.id, g.label]))
  const inside = (id: string, scope: string): boolean => {
    for (let cur = groups.get(id)?.parent; cur; cur = groups.get(cur)?.parent) if (cur === scope) return true
    return false
  }
  for (const g of logicalGroups) {
    if (!g.parent || groups.get(g.parent)?.kind === "region") continue
    if (!logicalGroups.some((o) => o !== g && logicalLabel.get(o.id) === logicalLabel.get(g.id) && groups.get(o.parent ?? "")?.kind === "region" && inside(o.id, g.parent!))) continue
    const held = cards.filter((c) => c.parent === g.id || groups.get(c.parent ?? "")?.parent === g.id)
    g.label = held.length && held.every((c) => /^aws_iam_/.test(c.type)) ? "IAM" : `${g.label} · global`
  }

  // ---------------------------------------------------------- edges
  const flows = new Map<string, { s: string; t: string; kind: FlowKind; prio: number }>()
  const add = (s: string | undefined, t: string | undefined, kind: FlowKind, prio: number) => {
    if (!s || !t || s === t) return
    const reverse = flows.get(`${t}->${s}`)
    if (reverse) {
      if (reverse.prio >= prio) return
      flows.delete(`${t}->${s}`)
    }
    const key = `${s}->${t}`
    const cur = flows.get(key)
    if (!cur) flows.set(key, { s, t, kind, prio })
    else if (cur.kind !== kind && kind === "data") cur.kind = "data"
  }
  const isMonitoring = (id: string) => categoryOf(nodes.get(id)!) === "monitoring"
  const isControl = (id: string) => {
    const c = categoryOf(nodes.get(id)!)
    return c === "identity" || c === "security"
  }
  /** The cards a DNS record points at (its alias, records or target resource). */
  const dnsTargets = (id: string) => {
    const to = LINKS[typeOf(nodes.get(id)!)]?.[1]
    return unique(refsOf(id).flatMap((x) => (to?.test(x.attr) ? [rep(x.id)] : [])).filter((c): c is string => !!c && isCard(c)))
  }
  // 1. Helpers that link two resources. A DNS record another card reaches its target by (a CDN's
  // origin at "origin.example.com") is that card's way there, not the zone's; a certificate's
  // validation record links nothing.
  for (const n of ordered) {
    const rule = LINKS[typeOf(n)]
    if (!rule || role.get(n.id) !== "plumbing") continue
    if (DNS_RECORD.test(typeOf(n)) && (CERTIFICATE.test(typeOf(nodes.get(owner.get(n.id) ?? "") ?? n)) || usersOf(n.id).some((u) => flowCard(u.id)))) continue
    const out = refsOf(n.id)
    const from = unique(out.filter((x) => rule[0].test(x.attr)).map((x) => rep(x.id)))
    const to = unique(out.filter((x) => rule[1].test(x.attr)).map((x) => rep(x.id)))
    // (AWS links carry requests; on the other clouds a link to a key vault or WAF is control.)
    for (const s of from) for (const t of to) add(s, t, t && (isMonitoring(t) || (isControl(t) && !typeOf(n).startsWith("aws_"))) ? "control" : "data", 3)
  }
  // 2. Direct references between cards (a resource drawn inside a card counts as that card; a
  // reference to a part of a card, such as a secret's version, is one to the card).
  for (const e of edges) {
    if (e.kind === "depends_on") continue
    const attr = e.attribute.replace(/\[[^\]]*\]/g, "")
    // A certificate a helper uses is its owner's (an HTTPS listener's is its load balancer's).
    const t = flowCard(e.target) ?? (CERTIFICATE_ATTR.test(attr) && isCard(rep(e.target) ?? "") ? rep(e.target) : undefined)
    const s = flowCard(e.source) ?? partCard(e.source)
    if (!s && t && DNS_RECORD.test(typeOf(nodes.get(e.source)!))) {
      // Through a DNS name: to the card the name points at (CloudFront → origin record → ALB).
      for (const d of dnsTargets(e.source)) add(t, d, isControl(d) ? "control" : "data", 2)
      continue
    }
    if (!s || !t || s === t) continue
    // Tags (often another module's tag map) say nothing about traffic.
    if (PARENT_ATTR.test(attr) || attr === "for_each" || attr === "count" || /^(tags|tags_all|labels|defined_tags|freeform_tags)(\.|$)/.test(attr)) continue
    if (isMonitoring(t) && !isMonitoring(s)) add(s, t, "control", 2)
    // Above a reverse reference (a service's environment naming its load balancer).
    else if (INBOUND_ATTR.test(attr)) add(s, t, "data", 3)
    // (A part keeps its own kind inside another card: a certificate in its zone's module is still a certificate.)
    else if (isMonitoring(s) || isControl(s) || isControl(partOf.get(e.source) ?? s) || REGISTRY_TYPE.test(typeOf(nodes.get(s)!))) add(t, s, "control", 2)
    else add(t, s, isControl(t) ? "control" : "data", 2)
  }
  // 3. What a role's users may call, through the policies attached to the role.
  for (const n of ordered) {
    if (typeOf(n) !== "aws_iam_role") continue
    const principals = new Set<string>()
    for (const u of users.get(n.id) ?? []) {
      if (u.dependsOn) continue
      const ut = typeOf(nodes.get(u.id)!)
      if (ut === "aws_iam_instance_profile") {
        for (const u2 of users.get(u.id) ?? []) if (flowCard(u2.id) && !IAM_FOLLOW.test(typeOf(nodes.get(u2.id)!))) principals.add(flowCard(u2.id)!)
      } else if (flowCard(u.id) && !IAM_FOLLOW.test(ut)) principals.add(flowCard(u.id)!)
    }
    // A role many services share says nothing about what each of them calls.
    if (principals.size === 0 || principals.size > 2) continue
    const resources = new Set<string>()
    const seen = new Set<string>([n.id])
    const queue = (users.get(n.id) ?? []).filter((u) => IAM_ATTACHMENT.test(typeOf(nodes.get(u.id)!))).map((u) => u.id)
    while (queue.length) {
      const cur = queue.shift()!
      if (seen.has(cur)) continue
      seen.add(cur)
      for (const ref of refs.get(cur) ?? []) {
        if (ref.dependsOn || seen.has(ref.id)) continue
        const rt = nodes.get(ref.id)!
        if (IAM_FOLLOW.test(rt.kind === "data" ? `data.${rt.type}` : rt.type)) queue.push(ref.id)
        else if (rep(ref.id)) resources.add(rep(ref.id)!)
      }
    }
    for (const p of principals) for (const res of resources) if (categoryOf(nodes.get(res)!) !== "identity") add(p, res, isMonitoring(res) || isControl(res) ? "control" : "data", 1)
  }
  // 3b. What a principal may use through access grants (Azure role assignments, access policies):
  // like an IAM role, a managed identity stands for the workloads using it, so its grants are their
  // arrows and the identity only has their dashed lines.
  for (const n of ordered) {
    const rule = GRANTS[typeOf(n)]
    if (!rule || role.get(n.id) !== "plumbing") continue
    const out = refsOf(n.id)
    const principals = unique(
      out
        .filter((x) => rule[0].test(x.attr))
        .flatMap((x) => {
          const c = flowCard(x.id) ?? rep(x.id)
          if (!c || !isCard(c)) return []
          if (categoryOf(nodes.get(c)!) !== "identity") return [c]
          return usersOf(c).flatMap((u) => {
            const w = flowCard(u.id)
            return w && w !== c ? [w] : []
          })
        }),
    )
    if (principals.length === 0 || principals.length > 2) continue
    const scopes = unique(out.filter((x) => rule[1].test(x.attr)).map((x) => rep(x.id))).filter((c): c is string => !!c && isCard(c))
    for (const p of principals) for (const res of scopes) add(p, res, isMonitoring(res) || isControl(res) || REGISTRY_TYPE.test(typeOf(nodes.get(res)!)) ? "control" : "data", 1)
  }
  // 3c. Connection strings kept as secrets: a service reading a secret whose value names another service (a
  // database URL in Key Vault, Secrets Manager, Secret Manager, SSM) reaches that service.
  for (const n of ordered) {
    if (!SECRET_VALUE.test(typeOf(n))) continue
    const targets = unique(refsOf(n.id).filter((x) => SECRET_VALUE_ATTR.test(x.attr)).map((x) => rep(x.id))).filter(
      (c): c is string => !!c && isCard(c) && !isControl(c) && !isMonitoring(c),
    )
    for (const u of usersOf(n.id)) {
      const s = flowCard(u.id)
      if (s && !isControl(s) && !isMonitoring(s)) for (const t of targets) add(s, t, "data", 1)
    }
  }
  // 4. Internet gateways reach the public subnets routing to them. (Private subnets going out through
  // a NAT gateway show it as their "routes: 0.0.0.0/0 → NAT" line, not as an arrow.)
  for (const [g, s] of gatewayEdges) {
    const sg = subnetGroup.get(s)
    if (cardIds.has(g) && sg) add(g, sg, "data", 2)
  }
  // 5. Network links between containers: VNet peerings, DRG attachments, serverless services reaching
  // into a subnet, private service access to the databases of its VPC.
  for (const n of ordered) {
    const t = typeOf(n)
    if (/^(azurerm_virtual_network_peering|google_compute_network_peering)$/.test(t)) {
      const nets = unique(refsOf(n.id).filter((x) => role.get(x.id) === "vpc").map((x) => vpcGroup.get(x.id)))
      if (nets.length === 2) add(nets[0], nets[1], "data", 2)
    } else if (t === "oci_core_drg_attachment") {
      const drg = rep(n.id)
      for (const x of refsOf(n.id)) if (role.get(x.id) === "vpc" && drg && cardIds.has(drg)) add(drg, vpcGroup.get(x.id), "data", 2)
    } else if (t === "google_service_networking_connection" && cardIds.has(n.id)) {
      const vpc = vpcOf(n.id)
      for (const c of ordered) {
        if (!cardIds.has(c.id) || categoryOf(c) !== "database") continue
        const onVpc = (id: string) => refsOf(id).some((x) => x.id === vpc) || (refs.get(id) ?? []).some((x) => x.dependsOn && x.id === n.id)
        if (vpc && [c.id, ...(folded.get(c.id) ?? []).map((h) => h.id)].some(onVpc)) add(n.id, c.id, "data", 2)
      }
    }
  }
  for (const [c, targets] of integrations) for (const x of targets) if (cardIds.has(c)) add(c, subnetGroup.get(x) ?? vpcGroup.get(x), "data", 2)
  // A role, secret or registry many cards use: "used by N" on it instead of N dashed lines.
  const controlIn = new Map<string, string[]>()
  for (const [key, f] of flows) if (f.kind === "control" && cardIds.has(f.s)) controlIn.set(f.t, [...(controlIn.get(f.t) ?? []), key])
  for (const [t, keys] of controlIn) {
    const card = cards.find((c) => c.id === t)
    if (keys.length <= SHARED_FAN_IN || !card) continue
    for (const k of keys) flows.delete(k)
    card.details.push(`used by ${keys.length}`)
  }

  // ---------------------------------------------------------- external actors
  const actors: ArchActor[] = []
  const addActor = (a: ArchActor) => {
    actors.push(a)
    return a.id
  }
  const entries = new Set<string>()
  for (const n of ordered) {
    const c = rep(n.id)
    if (c && n.kind !== "data" && isPublicEntry(n, r)) entries.add(c)
  }
  const visibleEntries = [...entries].filter((id) => cardIds.has(id)).sort()
  if (visibleEntries.length) {
    const usersActor = addActor({ id: "actor:users", kind: "users", label: "Users", sublabel: "Web & mobile clients" })
    // Users reach DNS zones pointing at entry points, and entry points no other one feeds: with
    // Route 53 → CloudFront → load balancer, only Users → Route 53.
    const heads = new Set<string>()
    const viaDns = new Set<string>()
    for (const f of flows.values()) {
      const zone = nodes.get(f.s)
      if (zone && DNS_ZONE.test(typeOf(zone)) && entries.has(f.t)) {
        viaDns.add(f.t)
        heads.add(f.s)
      }
    }
    for (const id of visibleEntries) if (!viaDns.has(id)) heads.add(id)
    const reached = new Set([...heads, ...viaDns])
    const fed = new Set([...flows.values()].filter((f) => f.kind === "data" && reached.has(f.s) && entries.has(f.t)).map((f) => f.t))
    const unfed = [...heads].sort().filter((id) => !fed.has(id))
    for (const id of unfed.length ? unfed : [...heads].sort().slice(0, 1)) add(usersActor, id, "data", 4)
  }
  // The Internet: above the internet gateways it reaches, and where Azure/GCP NAT goes out to.
  const gateways = ordered.filter((n) => INTERNET_GATEWAY.test(typeOf(n)) && cardIds.has(n.id))
  const egress = ordered.filter(
    (n) => cardIds.has(n.id) && (typeOf(n) === "azurerm_nat_gateway" || (typeOf(n) === "google_compute_router" && (folded.get(n.id) ?? []).some((h) => typeOf(h) === "google_compute_router_nat"))),
  )
  if (gateways.length || egress.length) {
    const onBorder = cards.find((c) => c.onBorder && gateways.some((g) => g.id === c.id))
    const internet = addActor({ id: "actor:internet", kind: "internet", label: "Internet", ...(onBorder ? { above: onBorder.id } : {}) })
    for (const g of gateways) add(internet, g.id, "data", 4)
    for (const g of egress) add(g.id, internet, "data", 4)
  }
  if (opts.connections?.length) {
    const external = addGroup({ id: "group:external", kind: "external", label: "Third-party services" })
    for (const c of [...opts.connections].sort((a, b) => a.name.localeCompare(b.name))) {
      addActor({ id: `conn:${c.id}`, parent: external, kind: "service", label: c.name, sublabel: CONNECTION_KIND[c.kind] })
    }
  }

  // ---------------------------------------------------------- prune and order
  const present = new Set<string>([...cardIds, ...actors.map((a) => a.id)])
  const needed = new Set<string>()
  const keep = (gid: string | undefined) => {
    for (let cur = gid; cur && !needed.has(cur); cur = groups.get(cur)?.parent) needed.add(cur)
  }
  for (const c of cards) keep(c.parent)
  for (const a of actors) keep(a.parent)
  // Network containers stay even when empty: they are part of the design.
  // (Not existing ones a data source only looks up.)
  const existing = (g: ArchGroup) => !!g.nodeId && nodes.get(g.nodeId)?.kind === "data"
  for (const g of groups.values()) if ((g.kind === "vpc" || g.kind === "subnet" || g.kind === "az" || (g.kind === "account" && g.nodeId)) && !existing(g)) keep(g.id)
  const groupList = [...groups.values()].filter((g) => needed.has(g.id))
  for (const g of groupList) present.add(g.id)
  // Two or more cards of a sub-group with the same flow to (or from) one item share one arrow from
  // (or to) the sub-group: "ECS task definitions → SQS queues", not a line per pair.
  let bundled = [...flows.values()].filter((f) => present.has(f.s) && present.has(f.t))
  for (const side of ["s", "t"] as const) {
    const other = side === "s" ? "t" : "s"
    const buckets = new Map<string, typeof bundled>()
    for (const f of bundled) {
      const g = kindGroup.get(f[side])
      if (!g || kindGroup.get(f[other]) === g) continue
      const key = `${g}|${f[other]}|${f.kind}`
      buckets.set(key, [...(buckets.get(key) ?? []), f])
    }
    for (const fs of buckets.values()) {
      if (fs.length < 2) continue
      const g = kindGroup.get(fs[0][side])!
      bundled = bundled.filter((f) => !fs.includes(f))
      bundled.push({ ...fs[0], [side]: g })
    }
  }
  const edgesOut: ArchEdge[] = unique(bundled.map((f) => `${f.s}|${f.t}|${f.kind}`))
    .map((key) => {
      const [source, target, kind] = key.split("|")
      return { id: `flow:${source}->${target}`, source, target, kind: kind as FlowKind }
    })
    .filter((e, i, all) => all.findIndex((x) => x.id === e.id) === i)
    .sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0))

  const providers = PROVIDERS.filter((p) => groupList.some((g) => g.provider === p) || cards.some((c) => c.provider === p))
  return { groups: sortGroups(groupList), cards, actors, edges: edgesOut, providers, regions: regionsUsed }
}

// Docker and Kubernetes kinds never occur in Terraform models (see docker.ts, kubernetes.ts); ranked last for completeness.
const GROUP_RANK: Record<GroupKind, number> = {
  cloud: 0,
  account: 1,
  region: 2,
  vpc: 3,
  az: 4,
  subnet: 5,
  logical: 6,
  other: 7,
  external: 8,
  "docker-host": 9,
  "compose-project": 10,
  "docker-network": 11,
  "k8s-cluster": 12,
  "k8s-namespace": 13,
  "k8s-data": 14,
}

/** Parents before children; siblings by kind (GCP's Global group before its regions), then logical order, then ID. */
function sortGroups(groups: ArchGroup[]) {
  const logicalRank = (g: ArchGroup) => {
    if (g.kind === "region") return g.label === "Global" ? -1 : 0
    const key = g.id.slice(g.id.lastIndexOf(":") + 1) as LogicalKey
    return g.kind === "logical" ? LOGICAL_ORDER.indexOf(key) : 0
  }
  const providerRank = (g: ArchGroup) => (g.provider ? PROVIDERS.indexOf(g.provider) : 9)
  const byParent = new Map<string, ArchGroup[]>()
  for (const g of groups) byParent.set(g.parent ?? "", [...(byParent.get(g.parent ?? "") ?? []), g])
  const out: ArchGroup[] = []
  const walk = (parent: string) => {
    const kids = (byParent.get(parent) ?? []).sort(
      (a, b) =>
        providerRank(a) - providerRank(b) ||
        GROUP_RANK[a.kind] - GROUP_RANK[b.kind] ||
        logicalRank(a) - logicalRank(b) ||
        (a.id < b.id ? -1 : a.id > b.id ? 1 : 0),
    )
    for (const k of kids) {
      out.push(k)
      walk(k.id)
    }
  }
  walk("")
  return out
}

const CONNECTION_KIND: Record<Connection["kind"], string> = {
  http: "HTTP API",
  postgres: "PostgreSQL",
  mysql: "MySQL",
  redis: "Redis",
  mongodb: "MongoDB",
  aws: "AWS account",
}

// ------------------------------------------------------------ smart descriptions

const mb = (v: unknown) => (typeof v === "number" ? (v >= 1024 && v % 1024 === 0 ? `${v / 1024} GB` : `${v} MB`) : undefined)
const days = (v: unknown) => (typeof v === "number" && v > 0 ? `${v}-day retention` : undefined)
const s = (v: unknown) => (isStr(v) ? v : typeof v === "number" ? String(v) : undefined)
const engine = (e: unknown, v: unknown) => (isStr(e) ? (isStr(v) || typeof v === "number" ? `${e} ${v}` : e) : undefined)
/** A Knative annotation's value ("run.googleapis.com/ingress"). */
const annotation = (map: unknown, key: string) => (map && typeof map === "object" ? s(Object.entries(map).find(([k]) => k === key)?.[1]) : undefined)

/** The fact a folded helper adds to its card ("Elastic IP", "push subscription", "10.20.0.0/16 reserved"). */
function foldedFact(h: WorkflowNode, card: WorkflowNode, r: Resolver): string | undefined {
  const t = baseType(h.type)
  if (t === "google_pubsub_subscription") return findAttr(h.body, "push_config.push_endpoint") ? "push subscription" : "pull subscription"
  if (t === "google_compute_global_address" && r.attr(h, "purpose") === "VPC_PEERING") {
    const address = r.attr(h, "address")
    const prefix = r.attr(h, "prefix_length")
    return typeof prefix === "number" ? `${isStr(address) ? address : ""}/${prefix} reserved range` : "reserved range"
  }
  if (t === "google_compute_global_address") return "static IP"
  // Invoker grants on a service: only "everyone" says something.
  if (/^google_(?!project_).+_iam_(member|binding)$/.test(t)) return [...strs(r.attr(h, "member")), ...strs(r.attr(h, "members"))].includes("allUsers") ? "public" : undefined
  if (t === "google_compute_router_nat") return r.attr(h, "nat_ip_allocate_option") === "MANUAL_ONLY" ? "Cloud NAT · static egress IPs" : "Cloud NAT"
  return FOLDED_FACT[t] ?? (h.type === card.type && /dlq|dead/i.test(h.name) ? "DLQ" : undefined)
}

/** Whether a resource (or the helper making it public) takes traffic from the internet. */
function isPublicEntry(n: WorkflowNode, r: Resolver): boolean {
  const t = baseType(n.type)
  if (PUBLIC_HELPER[t]) return true
  if (PUBLIC_ENTRY[t]) {
    // Any entry type can turn its public endpoint off (Azure apps, Front Door-only services).
    if (r.attr(n, "public_network_access_enabled") === false || r.attr(n, "public_network_access") === "Disabled") return false
    if (t === "oci_apigateway_gateway") return r.attr(n, "endpoint_type") !== "PRIVATE"
    if (t === "azurerm_container_app") return r.attr(n, "ingress.external_enabled") === true
    return true
  }
  switch (t) {
    case "aws_lb":
    case "aws_alb":
    case "aws_elb":
      return r.attr(n, "internal") !== true
    case "oci_load_balancer":
    case "oci_load_balancer_load_balancer":
    case "oci_network_load_balancer_network_load_balancer":
      return r.attr(n, "is_private") !== true
    case "azurerm_lb":
      return !!findAttr(n.body, "frontend_ip_configuration.public_ip_address_id")
    case "google_cloud_run_v2_service":
      return !/INTERNAL/.test(String(r.attr(n, "ingress") ?? ""))
    case "google_cloud_run_service":
      return /^(all|)$/.test(annotation(r.attr(n, "metadata.annotations"), "run.googleapis.com/ingress") ?? "")
    case "google_storage_bucket":
      return n.body.blocks.some((b) => b.type === "website")
    case "azurerm_storage_account":
      return n.body.blocks.some((b) => b.type === "static_website")
    default:
      return false
  }
}

/** Short facts about a resource from its literal (or resolvable) attributes. */
function detailsOf(n: WorkflowNode, r: Resolver, scale: number | undefined = 1): string[] {
  const t = baseType(n.type)
  const a = (path: string) => r.attr(n, path)
  const has = (block: string) => n.body.blocks.some((b) => b.type === block) || n.body.attrs.some((x) => x.name === block)
  const out: (string | undefined)[] = []
  if (n.kind === "module") out.push(s(a("version")))
  else
    switch (t) {
      case "aws_instance":
      case "aws_launch_template":
        out.push(s(a("instance_type")), a("associate_public_ip_address") === true ? "public IP" : undefined)
        break
      case "aws_lambda_function":
        out.push(s(a("runtime")) ?? (a("package_type") === "Image" ? "container image" : undefined), mb(a("memory_size")))
        break
      case "aws_dynamodb_table":
        out.push(a("billing_mode") === "PROVISIONED" ? "provisioned" : "on-demand", a("stream_enabled") === true ? "streams" : undefined)
        break
      case "aws_db_instance":
        out.push(engine(a("engine"), a("engine_version")), s(a("instance_class")), a("multi_az") === true ? "Multi-AZ" : undefined)
        break
      case "aws_rds_cluster":
        out.push(engine(a("engine"), a("engine_version")), s(a("engine_mode")))
        break
      case "aws_elasticache_cluster":
      case "aws_elasticache_replication_group":
        out.push(engine(a("engine"), a("engine_version")), s(a("node_type")))
        break
      case "aws_sqs_queue":
        out.push(a("fifo_queue") === true ? "FIFO" : "standard", has("redrive_policy") ? "DLQ" : undefined)
        break
      case "aws_sns_topic":
        out.push(a("fifo_topic") === true ? "FIFO topic" : undefined, has("kms_master_key_id") ? "encrypted" : undefined)
        break
      case "aws_lb":
      case "aws_alb":
        out.push(
          a("load_balancer_type") === "network" ? "Network LB" : a("load_balancer_type") === "gateway" ? "Gateway LB" : "Application LB",
          a("internal") === true ? "internal" : "internet-facing",
        )
        break
      case "aws_apigatewayv2_api":
        out.push(a("protocol_type") === "WEBSOCKET" ? "WebSocket API" : "HTTP API")
        break
      case "aws_api_gateway_rest_api":
        out.push("REST API")
        break
      case "aws_ecs_service":
        out.push(s(a("launch_type")), typeof a("desired_count") === "number" ? `${a("desired_count")} task${a("desired_count") === 1 ? "" : "s"}` : undefined)
        break
      case "aws_ecs_task_definition":
        out.push(s(a("cpu")) && `${a("cpu")} CPU`, mb(Number(a("memory")) || undefined))
        break
      case "aws_eks_cluster":
        out.push(s(a("version")) && `Kubernetes ${a("version")}`)
        break
      case "aws_autoscaling_group":
        out.push(typeof a("min_size") === "number" && typeof a("max_size") === "number" ? `${a("min_size")}–${a("max_size")} instances` : undefined)
        break
      case "aws_cloudwatch_log_group":
        out.push(days(a("retention_in_days")))
        break
      case "aws_cloudfront_distribution":
        out.push(s(a("price_class"))?.replace("PriceClass_", "price class "))
        break
      case "aws_nat_gateway":
        out.push(a("connectivity_type") === "private" ? "private" : "public")
        break
      case "aws_kms_key":
        out.push(a("enable_key_rotation") === true ? "rotation on" : undefined)
        break
      case "aws_sfn_state_machine":
        out.push(a("type") === "EXPRESS" ? "Express" : "Standard")
        break
      case "aws_kinesis_stream":
        out.push(typeof a("shard_count") === "number" ? `${a("shard_count")} shards` : "on-demand")
        break
      case "aws_security_group":
        out.push(securitySummary(n, [], r))
        break
      case "aws_route_table":
        out.push(n.body.blocks.some((b) => b.type === "route") ? "routes" : undefined)
        break
      case "google_sql_database_instance":
        out.push(s(a("database_version")), s(a("settings.tier")), s(a("settings.availability_type")) === "REGIONAL" ? "REGIONAL primary+standby" : undefined)
        break
      case "google_storage_bucket":
        out.push(s(a("location")), s(a("storage_class")))
        break
      case "google_pubsub_topic":
        out.push(isStr(a("message_retention_duration")) ? `retention ${a("message_retention_duration")}` : undefined)
        break
      case "google_pubsub_subscription":
        out.push(typeof a("ack_deadline_seconds") === "number" ? `ack ${a("ack_deadline_seconds")}s` : "pull", has("dead_letter_policy") ? "dead-letter" : undefined)
        break
      case "google_compute_instance":
        out.push(s(a("machine_type")), s(a("zone")))
        break
      case "google_cloudfunctions2_function":
        out.push(s(a("build_config.runtime")))
        break
      case "google_container_cluster": {
        // A zonal cluster says its zone (the region shows a regional one's).
        const location = s(a("location"))
        out.push(location && /-[a-z]$/.test(location) ? location : undefined, a("enable_autopilot") === true ? "Autopilot" : undefined)
        break
      }
      case "google_redis_instance":
        out.push(s(a("tier")), typeof a("memory_size_gb") === "number" ? `${a("memory_size_gb")} GB` : undefined)
        break
      case "azurerm_storage_account":
        out.push([s(a("account_tier")), s(a("account_replication_type"))].filter(Boolean).join(" ") || undefined)
        break
      case "azurerm_linux_virtual_machine":
      case "azurerm_windows_virtual_machine":
        out.push(s(a("size")))
        break
      case "azurerm_kubernetes_cluster":
        out.push(s(a("default_node_pool.vm_size")), typeof a("default_node_pool.node_count") === "number" ? `${a("default_node_pool.node_count")} nodes` : undefined)
        break
      case "azurerm_service_plan":
        out.push(s(a("os_type")), s(a("sku_name")))
        break
      case "azurerm_mssql_server":
      case "azurerm_postgresql_flexible_server":
      case "azurerm_mysql_flexible_server":
        out.push(s(a("version")) && `v${a("version")}`, s(a("sku_name")))
        break
      case "azurerm_storage_queue":
      case "azurerm_servicebus_queue":
        out.push("queue")
        break
      case "azurerm_linux_web_app":
      case "azurerm_windows_web_app":
      case "azurerm_linux_function_app":
      case "azurerm_windows_function_app": {
        const stack = a("site_config.application_stack")
        const runtime = stack && typeof stack === "object" ? Object.entries(stack).find(([, v]) => isStr(v) && v !== "false") : undefined
        out.push(runtime ? `${runtime[0].replace(/_version$/, "")} ${runtime[1]}` : undefined, a("public_network_access_enabled") === false ? "private" : undefined)
        break
      }
      case "azurerm_function_app_flex_consumption":
        out.push(engine(a("runtime_name"), a("runtime_version")), mb(a("instance_memory_in_mb")), a("public_network_access_enabled") === false ? "private" : undefined)
        break
      case "azurerm_container_app": {
        const min = a("template.min_replicas")
        const max = a("template.max_replicas")
        out.push(
          a("ingress.external_enabled") === true ? "external ingress" : has("ingress") ? "internal ingress" : undefined,
          typeof min === "number" && typeof max === "number" ? `${min}–${max} replicas` : undefined,
          s(a("template.container.cpu")) && `${a("template.container.cpu")} CPU`,
          s(a("template.container.memory")),
        )
        break
      }
      case "azurerm_cosmosdb_account":
        out.push(a("kind") === "MongoDB" ? "MongoDB API" : "NoSQL API", s(a("consistency_policy.consistency_level")))
        break
      case "azurerm_redis_cache":
        out.push([s(a("sku_name")), isStr(a("family")) && a("capacity") !== undefined ? `${a("family")}${a("capacity")}` : undefined].filter(Boolean).join(" ") || undefined)
        break
      case "azurerm_servicebus_namespace":
      case "azurerm_eventhub_namespace":
      case "azurerm_container_registry":
        out.push(s(a("sku")))
        break
      case "azurerm_application_gateway":
        out.push(s(a("sku.name")), /waf/i.test(String(a("sku.tier") ?? "")) ? "WAF" : undefined)
        break
      case "azurerm_cdn_frontdoor_profile":
        out.push(s(a("sku_name"))?.replace(/_AzureFrontDoor$/, ""))
        break
      case "azurerm_lb":
        out.push(s(a("sku")), findAttr(n.body, "frontend_ip_configuration.public_ip_address_id") ? "public" : "internal")
        break
      case "azurerm_virtual_network_gateway":
        out.push(s(a("type")), s(a("sku")))
        break
      case "azurerm_virtual_machine":
        out.push(s(a("vm_size")))
        break
      case "azurerm_log_analytics_workspace":
        out.push(s(a("sku")), days(a("retention_in_days")))
        break
      case "google_cloud_run_v2_service": {
        const ingress = String(a("ingress") ?? "")
        const max = a("template.scaling.max_instance_count")
        out.push(/INTERNAL_LOAD_BALANCER/.test(ingress) ? "internal + LB ingress" : /INTERNAL_ONLY/.test(ingress) ? "internal ingress" : undefined, typeof max === "number" ? `max ${max} instances` : undefined)
        break
      }
      case "google_cloud_run_service": {
        const ingress = annotation(a("metadata.annotations"), "run.googleapis.com/ingress")
        const max = annotation(a("template.metadata.annotations"), "autoscaling.knative.dev/maxScale")
        out.push(ingress && ingress !== "all" ? `${ingress} ingress` : undefined, max ? `max ${max} instances` : undefined)
        break
      }
      case "google_compute_instance_group_manager":
      case "google_compute_region_instance_group_manager":
        out.push(typeof a("target_size") === "number" ? `${a("target_size")} instance${a("target_size") === 1 ? "" : "s"}` : undefined)
        break
      case "google_compute_global_forwarding_rule":
      case "google_compute_forwarding_rule":
        out.push(
          /INTERNAL/.test(String(a("load_balancing_scheme") ?? "")) ? "internal" : "external",
          t === "google_compute_global_forwarding_rule" ? "global" : "regional",
        )
        break
      case "google_vpc_access_connector":
        out.push(s(a("ip_cidr_range")), s(a("machine_type")))
        break
      case "google_firestore_database":
        out.push(a("type") === "DATASTORE_MODE" ? "Datastore mode" : "Native mode", s(a("location_id")))
        break
      case "google_cloud_scheduler_job":
        out.push(s(a("schedule")))
        break
      case "google_container_node_pool":
        out.push(s(a("node_config.machine_type")), typeof a("node_count") === "number" ? `${a("node_count")} nodes` : undefined)
        break
      case "oci_core_instance": {
        const ocpus = a("shape_config.ocpus")
        const memory = a("shape_config.memory_in_gbs")
        out.push(s(a("shape")), typeof ocpus === "number" ? `${ocpus} OCPU` : undefined, typeof memory === "number" ? `${memory} GB` : undefined)
        break
      }
      case "oci_database_autonomous_database": {
        const workload: Record<string, string> = { OLTP: "Transaction Processing", DW: "Data Warehouse", AJD: "JSON", APEX: "APEX" }
        const storage = a("data_storage_size_in_tbs")
        out.push(
          workload[String(a("db_workload") ?? "")],
          a("is_free_tier") === true ? "Always Free" : undefined,
          typeof a("compute_count") === "number" ? `${a("compute_count")} ECPU` : typeof a("cpu_core_count") === "number" ? `${a("cpu_core_count")} OCPU` : undefined,
          typeof storage === "number" ? `${storage} TB` : undefined,
        )
        break
      }
      case "oci_mysql_mysql_db_system":
        out.push(engine("MySQL", a("mysql_version")), s(a("shape_name")), a("is_highly_available") === true ? "HA" : undefined)
        break
      case "oci_load_balancer":
      case "oci_load_balancer_load_balancer": {
        const min = a("shape_details.minimum_bandwidth_in_mbps")
        const max = a("shape_details.maximum_bandwidth_in_mbps")
        out.push(s(a("shape")), typeof min === "number" && typeof max === "number" ? `${min}–${max} Mbps` : undefined, a("is_private") === true ? "private" : "public")
        break
      }
      case "oci_network_load_balancer_network_load_balancer":
        out.push(a("is_private") === true ? "private" : "public")
        break
      case "oci_containerengine_cluster":
        out.push(s(a("kubernetes_version")) && `Kubernetes ${a("kubernetes_version")}`)
        break
      case "oci_containerengine_node_pool":
        out.push(s(a("node_shape")), typeof a("node_config_details.size") === "number" ? `${a("node_config_details.size")} nodes` : undefined)
        break
      case "oci_functions_function":
        out.push(mb(a("memory_in_mbs")))
        break
      case "oci_streaming_stream":
        out.push(typeof a("partitions") === "number" ? `${a("partitions")} partitions` : undefined)
        break
      case "oci_kms_vault":
        out.push(s(a("vault_type")))
        break
      case "oci_apigateway_gateway":
        out.push(s(a("endpoint_type"))?.toLowerCase())
        break
      case "oci_objectstorage_bucket":
        out.push(s(a("storage_tier")), a("access_type") && a("access_type") !== "NoPublicAccess" ? "public" : undefined)
        break
      default: {
        for (const k of ["instance_type", "instance_class", "machine_type", "size", "shape", "sku_name", "sku", "tier", "node_type", "runtime", "engine", "database_version", "protocol_type", "storage_class"]) {
          const v = s(a(k))
          if (v) {
            out.push(v)
            break
          }
        }
      }
    }
  // Instances: the resource's own count/for_each times its module calls'; unknown counts say nothing.
  const own = findAttr(n.body, "count") || findAttr(n.body, "for_each") ? r.count(n) : 1
  const count = own !== undefined && scale !== undefined ? own * scale : undefined
  if (count !== undefined && count > 1) out.unshift(`×${count}`)
  if (n.kind === "data") out.unshift("existing")
  return unique(out.filter(isStr))
}
