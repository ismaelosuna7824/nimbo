// AzAPI resources (`azapi_resource` with `type = "Microsoft.Web/sites@2024-04-01"`, as Azure
// Verified Modules write them) drawn as the azurerm resource of the same service, so they get its
// icon, group, placement and folding. Child resources (`Microsoft.Web/sites/config`) fold into their
// parent like azurerm helpers do.

/** ARM resource types (lowercase, no API version) → the azurerm type standing for them. */
const ARM_TYPES: Record<string, string> = {
  // Azure Verified Modules' usage telemetry.
  "microsoft.resources/deployments": "azapi_deployment",
  "microsoft.resources/resourcegroups": "azurerm_resource_group",
  "microsoft.network/virtualnetworks": "azurerm_virtual_network",
  "microsoft.network/virtualnetworks/subnets": "azurerm_subnet",
  "microsoft.network/networksecuritygroups": "azurerm_network_security_group",
  "microsoft.network/networksecuritygroups/securityrules": "azurerm_network_security_rule",
  "microsoft.network/routetables": "azurerm_route_table",
  "microsoft.network/routetables/routes": "azurerm_route",
  "microsoft.network/networkinterfaces": "azurerm_network_interface",
  "microsoft.network/publicipaddresses": "azurerm_public_ip",
  "microsoft.network/natgateways": "azurerm_nat_gateway",
  "microsoft.network/loadbalancers": "azurerm_lb",
  "microsoft.network/applicationgateways": "azurerm_application_gateway",
  "microsoft.network/applicationgatewaywebapplicationfirewallpolicies": "azurerm_web_application_firewall_policy",
  "microsoft.network/privateendpoints": "azurerm_private_endpoint",
  "microsoft.network/privateendpoints/privatednszonegroups": "azurerm_private_endpoint_dns_zone_group",
  "microsoft.network/privatednszones": "azurerm_private_dns_zone",
  "microsoft.network/privatednszones/virtualnetworklinks": "azurerm_private_dns_zone_virtual_network_link",
  "microsoft.network/dnszones": "azurerm_dns_zone",
  "microsoft.network/bastionhosts": "azurerm_bastion_host",
  "microsoft.network/azurefirewalls": "azurerm_firewall",
  "microsoft.network/firewallpolicies": "azurerm_firewall_policy",
  "microsoft.network/virtualnetworkgateways": "azurerm_virtual_network_gateway",
  "microsoft.network/frontdoorwebapplicationfirewallpolicies": "azurerm_cdn_frontdoor_firewall_policy",
  "microsoft.cdn/profiles": "azurerm_cdn_frontdoor_profile",
  "microsoft.web/serverfarms": "azurerm_service_plan",
  "microsoft.web/sites": "azurerm_linux_web_app",
  "microsoft.web/sites/slots": "azurerm_linux_web_app_slot",
  "microsoft.web/staticsites": "azurerm_static_web_app",
  "microsoft.app/managedenvironments": "azurerm_container_app_environment",
  "microsoft.app/containerapps": "azurerm_container_app",
  "microsoft.app/jobs": "azurerm_container_app_job",
  "microsoft.containerservice/managedclusters": "azurerm_kubernetes_cluster",
  "microsoft.containerservice/managedclusters/agentpools": "azurerm_kubernetes_cluster_node_pool",
  "microsoft.containerregistry/registries": "azurerm_container_registry",
  "microsoft.containerinstance/containergroups": "azurerm_container_group",
  "microsoft.compute/virtualmachines": "azurerm_linux_virtual_machine",
  "microsoft.compute/virtualmachinescalesets": "azurerm_linux_virtual_machine_scale_set",
  "microsoft.compute/disks": "azurerm_managed_disk",
  "microsoft.compute/sshpublickeys": "azurerm_ssh_public_key",
  "microsoft.storage/storageaccounts": "azurerm_storage_account",
  "microsoft.sql/servers": "azurerm_mssql_server",
  "microsoft.sql/servers/databases": "azurerm_mssql_database",
  "microsoft.dbforpostgresql/flexibleservers": "azurerm_postgresql_flexible_server",
  "microsoft.dbformysql/flexibleservers": "azurerm_mysql_flexible_server",
  "microsoft.documentdb/databaseaccounts": "azurerm_cosmosdb_account",
  "microsoft.cache/redis": "azurerm_redis_cache",
  "microsoft.cache/redisenterprise": "azurerm_managed_redis",
  "microsoft.servicebus/namespaces": "azurerm_servicebus_namespace",
  "microsoft.servicebus/namespaces/queues": "azurerm_servicebus_queue",
  "microsoft.servicebus/namespaces/topics": "azurerm_servicebus_topic",
  "microsoft.eventhub/namespaces": "azurerm_eventhub_namespace",
  "microsoft.eventhub/namespaces/eventhubs": "azurerm_eventhub",
  "microsoft.eventgrid/topics": "azurerm_eventgrid_topic",
  "microsoft.eventgrid/systemtopics": "azurerm_eventgrid_system_topic",
  "microsoft.apimanagement/service": "azurerm_api_management",
  "microsoft.keyvault/vaults": "azurerm_key_vault",
  "microsoft.keyvault/vaults/secrets": "azurerm_key_vault_secret",
  "microsoft.keyvault/vaults/keys": "azurerm_key_vault_key",
  "microsoft.managedidentity/userassignedidentities": "azurerm_user_assigned_identity",
  "microsoft.authorization/roleassignments": "azurerm_role_assignment",
  "microsoft.authorization/locks": "azurerm_management_lock",
  "microsoft.insights/components": "azurerm_application_insights",
  "microsoft.insights/diagnosticsettings": "azurerm_monitor_diagnostic_setting",
  "microsoft.insights/actiongroups": "azurerm_monitor_action_group",
  "microsoft.insights/metricalerts": "azurerm_monitor_metric_alert",
  "microsoft.operationalinsights/workspaces": "azurerm_log_analytics_workspace",
  "microsoft.cognitiveservices/accounts": "azurerm_cognitive_account",
  "microsoft.search/searchservices": "azurerm_search_service",
  "microsoft.logic/workflows": "azurerm_logic_app_workflow",
  "microsoft.datafactory/factories": "azurerm_data_factory",
}

/**
 * The azurerm type an AzAPI resource stands for, from its ARM type: a known service, else
 * "azapi_resource" (its own card), or "azapi_child" for a child resource with no service of its
 * own (folded into its parent). Web sites of kind "functionapp" are function apps.
 */
export function azapiType(armType: string, kind?: unknown): string {
  const t = armType.replace(/@.*$/, "").toLowerCase()
  const mapped = ARM_TYPES[t]
  if (mapped === "azurerm_linux_web_app" && typeof kind === "string" && /functionapp/i.test(kind)) return "azurerm_linux_function_app"
  if (mapped) return mapped
  // "Microsoft.Web/sites/config": provider namespace, then type segments (more than one = a child).
  return t.split("/").length > 2 ? "azapi_child" : "azapi_resource"
}
