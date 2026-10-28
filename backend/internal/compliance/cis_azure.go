package compliance

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// CIS Microsoft Azure Foundations Benchmark v6.0.0 (2026-04-19). Recommendation numbers and titles follow the
// benchmark. Since v5 the Foundations benchmark leaves database, App Service and VM hardening to the CIS Azure Database
// Services and Compute Services benchmarks, so Azure SQL, App Service and disk encryption controls are not part of it.
// Controls whose evidence lives in the tenant or subscription (Entra ID settings, role assignments, Defender plans,
// activity log alerts) are manual: a design declaring some of them doesn't prove the subscription has them.

const (
	azDatabricks = "Analytics - Azure Databricks"
	azCompute    = "Compute - Virtual Machines"
	azIdentity   = "Identity"
	azLogging    = "Logging and Monitoring"
	azNetwork    = "Networking"
	azDefender   = "Security - Microsoft Defender"
	azKeyVault   = "Security - Key Vault"
	azSecurity   = "Security - Bastion and DDoS"
	azFiles      = "Storage - Azure Files"
	azBlob       = "Storage - Blob Storage"
	azStorage    = "Storage - Storage Accounts"

	azTenant   = "This is a tenant or subscription setting, not part of the design: check it in the Azure portal (or with the Azure Policy CIS initiative), then attest it. "
	azDefendOn = "Turn the plan on in Microsoft Defender for Cloud > Environment settings > <subscription> > Defender plans (azurerm_security_center_subscription_pricing in a subscription-level workflow), then attest it. "
	azAlert    = "Create an activity log alert (azurerm_monitor_activity_log_alert, scope = the subscription) for this operation with an action group that notifies the security team, then attest it. "
	azSeenOnly = " Resources created outside this design aren't seen."
)

var (
	azStorageAccount = []string{"azurerm_storage_account"}
	azVaults         = []string{"azurerm_key_vault"}
	azWorkspaces     = []string{"azurerm_databricks_workspace"}
	azNSGs           = []string{"azurerm_network_security_group", "azurerm_network_security_rule"}
	azAppGateways    = []string{"azurerm_application_gateway"}
	azWAFPolicies    = []string{"azurerm_web_application_firewall_policy"}
	azFlowLogs       = []string{"azurerm_network_watcher_flow_log"}
	azVMs            = []string{"azurerm_linux_virtual_machine", "azurerm_windows_virtual_machine", "azurerm_virtual_machine", "azurerm_linux_virtual_machine_scale_set", "azurerm_windows_virtual_machine_scale_set"}
)

// azAttr returns the source of n's attribute name and its constant string value (ok false when unset or not a
// constant string).
func azAttr(n *model.Node, name string) (expr string, value string, set, ok bool) {
	for _, a := range n.Body.Attrs {
		if a.Name != name {
			continue
		}
		raw := a.Value.Literal
		if len(raw) == 0 && a.Value.Expr != "" {
			raw, _ = hclcodec.LiteralFromExpr(a.Value.Expr)
		}
		ok = json.Unmarshal(raw, &value) == nil
		return a.Value.Expr, value, true, ok
	}
	return "", "", false, false
}

// azFlowLogTarget classifies a flow log by what it watches: "nsg" (a network security group: legacy NSG flow log),
// "vnet" (a virtual network, subnet or NIC: virtual network flow log) or "" when the target can't be read.
func azFlowLogTarget(n *model.Node) string {
	if _, _, set, _ := azAttr(n, "network_security_group_id"); set {
		return "nsg"
	}
	expr, v, set, ok := azAttr(n, "target_resource_id")
	switch {
	case !set:
		return ""
	case ok:
		if strings.Contains(strings.ToLower(v), "/networksecuritygroups/") {
			return "nsg"
		}
		return "vnet"
	case strings.Contains(expr, "azurerm_network_security_group."):
		return "nsg"
	case strings.Contains(expr, "azurerm_virtual_network.") || strings.Contains(expr, "azurerm_subnet.") || strings.Contains(expr, "azurerm_network_interface."):
		return "vnet"
	}
	return ""
}

// azNSGFlowLog and azVNetFlowLog split the flow logs between 7.5 and 7.8; an unreadable target applies to both.
func azNSGFlowLog(n *model.Node) bool  { return azFlowLogTarget(n) != "vnet" }
func azVNetFlowLog(n *model.Node) bool { return azFlowLogTarget(n) != "nsg" }

// azNSGCapableSubnet narrows 7.11 to the subnets Azure supports network security groups on: azurerm_subnet resources
// other than the gateway, firewall and route server subnets, and virtual networks declaring inline subnets.
func azNSGCapableSubnet(n *model.Node) bool {
	if n.Type != "azurerm_subnet" {
		return slices.ContainsFunc(n.Body.Blocks, func(b model.Block) bool {
			return b.Type == "subnet" || b.Type == "dynamic" && slices.Equal(b.Labels, []string{"subnet"})
		})
	}
	_, name, _, _ := azAttr(n, "name")
	return !slices.Contains([]string{"GatewaySubnet", "AzureFirewallSubnet", "AzureFirewallManagementSubnet", "RouteServerSubnet"}, name)
}

func init() {
	Register(Framework{
		ID:       "cis-azure-6.0",
		Name:     "CIS Microsoft Azure Foundations Benchmark",
		Version:  "6.0.0",
		Platform: "azurerm",
		Source:   "https://www.cisecurity.org/benchmark/azure",
		Controls: []Control{
			// 2 Analytics Services: 2.1 Azure Databricks
			{ID: "2.1.1", Title: "Ensure that Azure Databricks is deployed in a customer-managed virtual network (VNet)", Section: azDatabricks,
				Rules: []string{"bp_azure_databricks_vnet"}, AppliesTo: azWorkspaces,
				Guidance: "Nimbo checks that every azurerm_databricks_workspace sets custom_parameters.virtual_network_id (VNet injection)."},
			{ID: "2.1.2", Title: "Ensure that Network Security Groups are Configured for Databricks Subnets", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Associate a network security group with the workspace's public and private subnets (custom_parameters.*_network_security_group_association_id) with explicit deny rules for unwanted traffic; control 7.11 checks the subnet associations in the design."},
			{ID: "2.1.3", Title: "Ensure that Traffic is Encrypted Between Cluster Worker Nodes", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Enable inter-node encryption with a cluster init script (spark.authenticate, spark.network.crypto.enabled, spark.io.encryption.enabled) applied by a cluster policy, then attest it."},
			{ID: "2.1.4", Title: "Ensure that Users and Groups are Synced from Microsoft Entra ID to Azure Databricks", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Provision users and groups with SCIM (automatic identity management) from Microsoft Entra ID instead of local Databricks accounts, then attest it."},
			{ID: "2.1.5", Title: "Ensure that Unity Catalog is Configured for Azure Databricks", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Assign a Unity Catalog metastore to the workspace (databricks_metastore_assignment) so data access is governed centrally, then attest it."},
			{ID: "2.1.6", Title: "Ensure that Usage is Restricted and Expiry is Enforced for Databricks Personal Access Tokens", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "In the workspace admin settings, limit who can create personal access tokens and set a maximum token lifetime; prefer Entra ID service principals, then attest it."},
			{ID: "2.1.7", Title: "Ensure that Diagnostic Log Delivery is Configured for Azure Databricks", Section: azDatabricks,
				Rules: []string{"bp_azure_diagnostic_logs"}, AppliesTo: azWorkspaces,
				Guidance: "Nimbo checks that every Databricks workspace has an azurerm_monitor_diagnostic_setting in the design sending at least one log category." + azSeenOnly},
			{ID: "2.1.8", Title: "Ensure Critical Data in Azure Databricks is Encrypted with Customer-managed Keys (CMK)", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Which data is critical is your call: for those workspaces use the premium SKU with managed_services_cmk_key_vault_key_id and managed_disk_cmk_key_vault_key_id (and customer_managed_key_enabled for DBFS), then attest it."},
			{ID: "2.1.9", Title: "Ensure 'No Public IP' is Set to 'Enabled'", Section: azDatabricks,
				Rules: []string{"bp_azure_databricks_public_ip"}, AppliesTo: azWorkspaces,
				Guidance: "Nimbo flags workspaces that set custom_parameters.no_public_ip = false; the provider default (true) is secure cluster connectivity."},
			{ID: "2.1.10", Title: "Ensure 'Allow Public Network Access' is set to 'Disabled'", Section: azDatabricks,
				Rules: []string{"bp_azure_public_network_access"}, AppliesTo: azWorkspaces,
				Guidance: "Nimbo checks that every Databricks workspace sets public_network_access_enabled = false (it defaults to true)."},
			{ID: "2.1.11", Title: "Ensure Private Endpoints are used to access Azure Databricks workspaces", Section: azDatabricks,
				Rules: []string{"bp_azure_private_endpoint"}, AppliesTo: azWorkspaces,
				Guidance: "Nimbo checks that an azurerm_private_endpoint in the design connects to every Databricks workspace (private_service_connection.private_connection_resource_id)."},
			{ID: "2.1.12", Title: "Ensure Azure Databricks groups are reviewed periodically", Section: azDatabricks, Manual: true, AppliesTo: azWorkspaces,
				Guidance: "Review workspace and account groups and their entitlements on a schedule (at least quarterly), remove stale members, then attest it."},

			// 3 Compute Services: 3.1 Virtual Machines
			{ID: "3.1.1", Title: "Ensure only MFA Enabled Identities can Access Privileged Virtual Machine", Section: azCompute, Manual: true, AppliesTo: azVMs,
				Guidance: azTenant + "Only identities with MFA (Conditional Access) should hold Virtual Machine Administrator/User Login roles; keep VM managed identities least-privileged."},

			// 5 Identity Services
			{ID: "5.1.1", Title: "Ensure that 'security defaults' is Enabled in Microsoft Entra ID", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Microsoft Entra ID > Overview > Properties > Manage security defaults (only when you don't use Conditional Access)."},
			{ID: "5.1.2", Title: "Ensure that 'Require Multifactor Authentication to register or join devices with Microsoft Entra' is set to 'Yes'", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Microsoft Entra ID > Devices > Device settings (or a Conditional Access policy for the 'Register or join devices' user action)."},
			{ID: "5.1.3", Title: "Ensure that 'multifactor authentication' is 'enabled' For All Users", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Require MFA for every user with security defaults, per-user MFA or a Conditional Access policy."},
			{ID: "5.1.4", Title: "Ensure that 'Allow users to remember multifactor authentication on devices they trust' is Disabled", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Per-user MFA service settings: clear 'Allow users to remember multifactor authentication on devices they trust'."},
			{ID: "5.3.1", Title: "Ensure that Azure Admin Accounts Are Not Used for Daily Operations", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Give administrators separate, cloud-only privileged accounts (ideally activated with PIM) used only for admin tasks."},
			{ID: "5.3.2", Title: "Ensure that Guest Users are Reviewed on a Regular Basis", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Review guest accounts at least monthly (Entra access reviews) and remove the ones no longer needed."},
			{ID: "5.3.3", Title: "Ensure That Use of the 'User Access Administrator' Role is Restricted", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Remove User Access Administrator assignments at root scope ('/') right after the elevated task is done."},
			{ID: "5.3.4", Title: "Ensure that All 'Privileged' Role Assignments are Periodically Reviewed", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Review Owner, Contributor, User Access Administrator and other privileged assignments on a schedule (PIM access reviews)."},
			{ID: "5.3.5", Title: "Ensure Disabled User Accounts do not Have Read, Write, or Owner Permissions", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Remove the role assignments of disabled user accounts."},
			{ID: "5.3.6", Title: "Ensure 'Tenant Creator' Role Assignments are Periodically Reviewed", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Review who holds the Tenant Creator role and remove the assignments that aren't needed."},
			{ID: "5.3.7", Title: "Ensure All Non-privileged Role Assignments are Periodically Reviewed", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Review the remaining role assignments on a schedule and remove stale ones."},
			{ID: "5.4", Title: "Ensure that No Custom Subscription Administrator Roles Exist", Section: azIdentity,
				Rules: []string{"bp_azure_custom_admin_role"}, AppliesTo: []string{"azurerm_role_definition"},
				Guidance: "Nimbo flags azurerm_role_definition resources in the design whose permissions allow \"*\" with a subscription, management group or root assignable scope (a constant path or a reference to a subscription or management group)." + azSeenOnly},
			{ID: "5.5", Title: "Ensure that a Custom Role is Assigned Permissions for Administering Resource Locks", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Create a custom role with only Microsoft.Authorization/locks/* (plus read) and assign it to the people who manage locks."},
			{ID: "5.6", Title: "Ensure that 'Subscription leaving Microsoft Entra tenant' and 'Subscription entering Microsoft Entra tenant' is set to 'Permit no one'", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Subscriptions > Advanced options > Manage policies."},
			{ID: "5.7", Title: "Ensure there are between 2 and 3 Subscription Owners", Section: azIdentity, Manual: true,
				Guidance: azTenant + "Keep two or three Owner assignments on each subscription; grant narrower roles to everyone else."},

			// 6 Management and Governance Services: 6.1 Logging and Monitoring
			{ID: "6.1.1.1", Title: "Ensure that a 'Diagnostic Setting' Exists for Subscription Activity Logs", Section: azLogging, Manual: true,
				Guidance: azTenant + "Export the subscription activity log with a diagnostic setting (azurerm_monitor_diagnostic_setting with target_resource_id = the subscription id) to Log Analytics or a storage account."},
			{ID: "6.1.1.2", Title: "Ensure Diagnostic Setting Captures Appropriate Categories", Section: azLogging, Manual: true,
				Guidance: azTenant + "The subscription diagnostic setting includes the Administrative, Alert, Policy and Security categories."},
			{ID: "6.1.1.3", Title: "Ensure the Storage Account Containing the Container with Activity Logs is Encrypted with Customer-managed Key (CMK)", Section: azLogging, Manual: true,
				Guidance: azTenant + "If activity logs go to a storage account, encrypt it with a Key Vault key (customer_managed_key or azurerm_storage_account_customer_managed_key)."},
			{ID: "6.1.1.4", Title: "Ensure that Logging for Azure Key Vault is 'Enabled'", Section: azLogging,
				Rules: []string{"bp_azure_diagnostic_logs"}, AppliesTo: azVaults,
				Guidance: "Nimbo checks that every Key Vault has an azurerm_monitor_diagnostic_setting in the design with the AuditEvent category (or the audit / allLogs category group)." + azSeenOnly},
			{ID: "6.1.1.5", Title: "Ensure that Network Security Group Flow Logs are Captured and Sent to Log Analytics", Section: azLogging, Manual: true, AppliesTo: []string{"azurerm_network_security_group"},
				Guidance: "NSG flow logs are retired (no new ones since 30 June 2025, removed 30 September 2027): migrate to virtual network flow logs (6.1.1.6). For NSG flow logs that remain, check traffic analytics sends them to a Log Analytics workspace, then attest it."},
			{ID: "6.1.1.6", Title: "Ensure that Virtual Network Flow Logs are Captured and Sent to Log Analytics", Section: azLogging, Manual: true, AppliesTo: []string{"azurerm_virtual_network"},
				Guidance: "Add an azurerm_network_watcher_flow_log with target_resource_id = the virtual network and a traffic_analytics block pointing at a Log Analytics workspace (Network Watcher may live in another workflow), then attest it."},
			{ID: "6.1.1.7", Title: "Ensure that a Microsoft Entra Diagnostic Setting Exists to Send Microsoft Graph Activity Logs to an Appropriate Destination", Section: azLogging, Manual: true,
				Guidance: azTenant + "Microsoft Entra ID > Monitoring > Diagnostic settings: send MicrosoftGraphActivityLogs to Log Analytics or a SIEM."},
			{ID: "6.1.1.8", Title: "Ensure that a Microsoft Entra Diagnostic Setting Exists to Send Microsoft Entra Activity Logs to an Appropriate Destination", Section: azLogging, Manual: true,
				Guidance: azTenant + "Microsoft Entra ID > Monitoring > Diagnostic settings: send AuditLogs, SignInLogs and the other sign-in categories to Log Analytics or a SIEM."},
			{ID: "6.1.1.9", Title: "Ensure that Intune Logs are Captured and Sent to Log Analytics", Section: azLogging, Manual: true,
				Guidance: azTenant + "Intune admin center > Tenant administration > Diagnostic settings: send AuditLogs, OperationalLogs and the device compliance logs to Log Analytics."},
			{ID: "6.1.2.1", Title: "Ensure that Activity Log Alert Exists for Create Policy Assignment", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Authorization/policyAssignments/write."},
			{ID: "6.1.2.2", Title: "Ensure that Activity Log Alert exists for Delete Policy Assignment", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Authorization/policyAssignments/delete."},
			{ID: "6.1.2.3", Title: "Ensure that Activity Log Alert Exists for Create or Update Network Security Group", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Network/networkSecurityGroups/write."},
			{ID: "6.1.2.4", Title: "Ensure that Activity Log Alert Exists for Delete Network Security Group", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Network/networkSecurityGroups/delete."},
			{ID: "6.1.2.5", Title: "Ensure that Activity Log Alert Exists for Create or Update Security Solution", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Security/securitySolutions/write."},
			{ID: "6.1.2.6", Title: "Ensure that Activity Log Alert Exists for Delete Security Solution", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Security/securitySolutions/delete."},
			{ID: "6.1.2.7", Title: "Ensure that Activity Log Alert Exists for Create or Update SQL Server Firewall Rule", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Sql/servers/firewallRules/write."},
			{ID: "6.1.2.8", Title: "Ensure that Activity Log Alert Exists for Delete SQL Server Firewall Rule", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Sql/servers/firewallRules/delete."},
			{ID: "6.1.2.9", Title: "Ensure that Activity Log Alert Exists for Create or Update Public IP Address rule", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Network/publicIPAddresses/write."},
			{ID: "6.1.2.10", Title: "Ensure that Activity Log Alert Exists for Delete Public IP Address rule", Section: azLogging, Manual: true,
				Guidance: azAlert + "Operation: Microsoft.Network/publicIPAddresses/delete."},
			{ID: "6.1.2.11", Title: "Ensure that an Activity Log Alert Exists for Service Health", Section: azLogging, Manual: true,
				Guidance: azAlert + "Category: ServiceHealth (service issues, planned maintenance, health and security advisories)."},
			{ID: "6.1.3.1", Title: "Ensure Application Insights are Configured", Section: azLogging, Manual: true,
				Guidance: "Instrument your applications with an azurerm_application_insights resource (workspace-based) so their telemetry is collected, then attest it."},
			{ID: "6.1.4", Title: "Ensure that Azure Monitor Resource Logging is Enabled for All Services that Support it", Section: azLogging, Manual: true,
				Guidance: "Give every resource that supports resource logs an azurerm_monitor_diagnostic_setting (allLogs or audit category group) to a Log Analytics workspace, or enforce it with Azure Policy, then attest it."},
			{ID: "6.1.5", Title: "Ensure Basic, Free, and Consumption SKUs are not used on Production artifacts requiring monitoring and SLA", Section: azLogging, Manual: true,
				Guidance: "Only you know which resources are production: check that they don't use Basic, Free or Consumption SKUs without an SLA, then attest it."},
			{ID: "6.2", Title: "Ensure that Resource Locks are set for Mission-Critical Azure Resources", Section: azLogging, Manual: true,
				Guidance: "Add azurerm_management_lock (CanNotDelete or ReadOnly) to the resources or resource groups you consider mission-critical, then attest it."},

			// 7 Networking Services
			{ID: "7.1", Title: "Ensure that RDP Access from the Internet is Evaluated and Restricted", Section: azNetwork,
				Rules: []string{"bp_open_ingress"}, Counts: exposesPort(3389), AppliesTo: azNSGs,
				Guidance: "Nimbo flags inbound Allow NSG rules from *, 0.0.0.0/0, Internet or Any whose destination port range includes 3389 (or *). Only the open-ingress findings about RDP count here; open SSH or database ports don't."},
			{ID: "7.2", Title: "Ensure that SSH Access from the Internet is Evaluated and Restricted", Section: azNetwork,
				Rules: []string{"bp_open_ingress"}, Counts: exposesPort(22), AppliesTo: azNSGs,
				Guidance: "Nimbo flags inbound Allow NSG rules from *, 0.0.0.0/0, Internet or Any whose destination port range includes 22 (or *). Only the open-ingress findings about SSH count here; open RDP or database ports don't."},
			{ID: "7.3", Title: "Ensure that UDP Port Access from the Internet is Evaluated and Restricted", Section: azNetwork,
				Rules: []string{"bp_azure_nsg_udp_internet"}, AppliesTo: azNSGs,
				Guidance: "Nimbo flags inbound Allow NSG rules with protocol Udp or * from the internet to ports 53, 123, 161, 389 or 1900 (or ranges and * covering them), as the benchmark's audit query does."},
			{ID: "7.4", Title: "Ensure that HTTP(S) Access from the Internet is Evaluated and Restricted", Section: azNetwork, Manual: true, AppliesTo: azNSGs,
				Guidance: "Public web front ends legitimately allow 80/443 from the internet, so only you can decide: check that only the resources meant to serve the public (ideally behind Application Gateway/WAF or Front Door) accept HTTP(S) from the internet, then attest it."},
			{ID: "7.5", Title: "Ensure that Network Security Group Flow Log Retention Days is Set to Greater than or equal to 90", Section: azNetwork,
				Rules: []string{"bp_azure_flow_log_retention"}, AppliesTo: azFlowLogs, AppliesToNode: azNSGFlowLog,
				Guidance: "Nimbo flags NSG flow logs (azurerm_network_watcher_flow_log targeting a network security group) whose enabled retention policy keeps records fewer than 90 days (0 = forever). NSG flow logs are retired (no new ones since 30 June 2025): migrate to virtual network flow logs (7.8)."},
			{ID: "7.6", Title: "Ensure that Network Watcher is 'Enabled' for Azure Regions That are in Use", Section: azNetwork, Manual: true,
				Guidance: azTenant + "Azure creates a Network Watcher per region in NetworkWatcherRG when a virtual network is deployed; check none was deleted."},
			{ID: "7.7", Title: "Ensure that Public IP Addresses are Evaluated on a Periodic Basis", Section: azNetwork, Manual: true, AppliesTo: []string{"azurerm_public_ip"},
				Guidance: "Review the public IPs of the subscription periodically and remove the ones that aren't needed, then attest it."},
			{ID: "7.8", Title: "Ensure that Virtual Network Flow Log Retention Days is Set to Greater than or Equal to 90", Section: azNetwork,
				Rules: []string{"bp_azure_flow_log_retention"}, AppliesTo: azFlowLogs, AppliesToNode: azVNetFlowLog,
				Guidance: "Nimbo flags virtual network flow logs (azurerm_network_watcher_flow_log targeting a virtual network, subnet or NIC) whose enabled retention policy keeps records fewer than 90 days (0 = forever, as is a disabled retention policy). A flow log whose target can't be read counts for both 7.5 and 7.8."},
			{ID: "7.9", Title: "Ensure 'Authentication type' is Set to 'Azure Active Directory' only for Azure VPN Gateway Point-to-Site Configuration", Section: azNetwork,
				Rules: []string{"bp_azure_vpn_entra_auth"}, AppliesTo: []string{"azurerm_virtual_network_gateway"},
				Guidance: "Nimbo flags VPN gateways whose vpn_client_configuration allows certificate or RADIUS authentication (vpn_auth_types, or root_certificate / radius settings when unset). Gateways without point-to-site configuration have nothing to flag."},
			{ID: "7.10", Title: "Ensure Azure Web Application Firewall (WAF) is Enabled on Azure Application Gateway", Section: azNetwork,
				Rules: []string{"bp_azure_appgw_waf"}, AppliesTo: azAppGateways,
				Guidance: "Nimbo checks that every Application Gateway uses the WAF or WAF_v2 tier with a firewall_policy_id (whose policy isn't disabled) or an enabled waf_configuration."},
			{ID: "7.11", Title: "Ensure Subnets Are Associated with Network Security Groups", Section: azNetwork,
				Rules: []string{"bp_azure_subnet_nsg"}, AppliesTo: []string{"azurerm_subnet", "azurerm_virtual_network"}, AppliesToNode: azNSGCapableSubnet,
				Guidance: "Nimbo checks that every azurerm_subnet has an azurerm_subnet_network_security_group_association and every inline subnet of a virtual network a security_group. GatewaySubnet, AzureFirewallSubnet, AzureFirewallManagementSubnet and RouteServerSubnet don't count: Azure doesn't support NSGs on them."},
			{ID: "7.12", Title: "Ensure the SSL Policy's 'Min protocol version' is Set to 'TLSv1_2' or Higher on Azure Application Gateway", Section: azNetwork,
				Rules: []string{"bp_azure_appgw_tls"}, AppliesTo: azAppGateways,
				Guidance: "Nimbo flags Application Gateway SSL policies (gateway and SSL profiles) with min_protocol_version TLSv1_0/TLSv1_1 or the AppGwSslPolicy20150501/20170401 predefined policies. Without an ssl_policy, Azure's default minimum is TLS 1.2."},
			{ID: "7.13", Title: "Ensure 'HTTP2' is Set to 'Enabled' on Azure Application Gateway", Section: azNetwork,
				Rules: []string{"bp_azure_appgw_http2"}, AppliesTo: azAppGateways,
				Guidance: "Nimbo checks that every Application Gateway sets http2_enabled = true (it defaults to false)."},
			{ID: "7.14", Title: "Ensure Request Body Inspection is Enabled in Azure Web Application Firewall policy on Azure Application Gateway", Section: azNetwork,
				Rules: []string{"bp_azure_waf_request_body"}, AppliesTo: append(append([]string{}, azWAFPolicies...), azAppGateways...),
				Guidance: "Nimbo flags WAF policies (policy_settings) and Application Gateway waf_configuration blocks that set request_body_check = false; it defaults to true."},
			{ID: "7.15", Title: "Ensure Bot Protection is Enabled in Azure Web Application Firewall Policy on Azure Application Gateway", Section: azNetwork,
				Rules: []string{"bp_azure_waf_bot_protection"}, AppliesTo: azWAFPolicies,
				Guidance: "Nimbo checks that every azurerm_web_application_firewall_policy has a managed_rule_set of type Microsoft_BotManagerRuleSet."},
			{ID: "7.16", Title: "Ensure Azure Network Security Perimeter is Used to Secure Azure Platform-as-a-service Resources", Section: azNetwork, Manual: true,
				Guidance: "Associate your PaaS resources (storage, Key Vault, ...) with a network security perimeter profile in enforced mode, then attest it."},

			// 8 Security Services: 8.1 Microsoft Defender for Cloud
			{ID: "8.1.1.1", Title: "Ensure Microsoft Defender CSPM is Set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Defender CSPM (resource_type CloudPosture)."},
			{ID: "8.1.2.1", Title: "Ensure Microsoft Defender for APIs is Set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: APIs (resource_type Api)."},
			{ID: "8.1.3.1", Title: "Ensure that Defender for Servers is Set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Servers (resource_type VirtualMachines)."},
			{ID: "8.1.3.2", Title: "Ensure that 'Vulnerability assessment for machines' Component Status is set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Defender for Servers settings: vulnerability assessment (Microsoft Defender Vulnerability Management)."},
			{ID: "8.1.3.3", Title: "Ensure that 'Endpoint protection' Component Status is set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Defender for Servers settings: Endpoint protection (Defender for Endpoint integration)."},
			{ID: "8.1.3.4", Title: "Ensure that 'Agentless scanning for machines' Component Status is Set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Defender for Servers settings: agentless scanning for machines."},
			{ID: "8.1.3.5", Title: "Ensure that 'File Integrity Monitoring' Component Status is Set to 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Defender for Servers Plan 2 settings: file integrity monitoring."},
			{ID: "8.1.4.1", Title: "Ensure That Microsoft Defender for Containers Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Containers (resource_type Containers)."},
			{ID: "8.1.5.1", Title: "Ensure That Microsoft Defender for Storage Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Storage (resource_type StorageAccounts)."},
			{ID: "8.1.5.2", Title: "Ensure Advanced Threat Protection Alerts for Storage Accounts Are Monitored", Section: azDefender, Manual: true,
				Guidance: "Route Defender for Storage alerts to the people who act on them (security contacts, workflow automation or a SIEM) and review them, then attest it."},
			{ID: "8.1.6.1", Title: "Ensure That Microsoft Defender for App Services Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: App Service (resource_type AppServices)."},
			{ID: "8.1.7.1", Title: "Ensure That Microsoft Defender for Azure Cosmos DB Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Azure Cosmos DB (resource_type CosmosDbs)."},
			{ID: "8.1.7.2", Title: "Ensure That Microsoft Defender for Open-Source Relational Databases Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: open-source relational databases (resource_type OpenSourceRelationalDatabases)."},
			{ID: "8.1.7.3", Title: "Ensure That Microsoft Defender for (Managed Instance) Azure SQL Databases Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Azure SQL databases (resource_type SqlServers)."},
			{ID: "8.1.7.4", Title: "Ensure That Microsoft Defender for SQL Servers on Machines Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: SQL servers on machines (resource_type SqlServerVirtualMachines)."},
			{ID: "8.1.8.1", Title: "Ensure That Microsoft Defender for Key Vault Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Key Vault (resource_type KeyVaults)."},
			{ID: "8.1.9.1", Title: "Ensure That Microsoft Defender for Resource Manager Is Set To 'On'", Section: azDefender, Manual: true,
				Guidance: azDefendOn + "Plan: Resource Manager (resource_type Arm)."},
			{ID: "8.1.10", Title: "Ensure that Microsoft Defender for Cloud is Configured to Check VM Operating Systems for Updates", Section: azDefender, Manual: true,
				Guidance: azTenant + "Check that the 'System updates should be installed on your machines' recommendations are enabled and machines report through Azure Update Manager."},
			{ID: "8.1.11", Title: "Ensure that non-deprecated Microsoft Cloud Security Benchmark policies are not set to 'Disabled'", Section: azDefender, Manual: true,
				Guidance: azTenant + "In the Microsoft cloud security benchmark assignment, no non-deprecated policy has effect Disabled."},
			{ID: "8.1.12", Title: "Ensure That 'All users with the following roles' is Set to 'Owner'", Section: azDefender, Manual: true,
				Guidance: azTenant + "Defender for Cloud > Environment settings > Email notifications (azurerm_security_center_contact in a subscription-level workflow)."},
			{ID: "8.1.13", Title: "Ensure 'Additional email addresses' is Configured with a Security Contact Email", Section: azDefender, Manual: true,
				Guidance: azTenant + "Defender for Cloud > Environment settings > Email notifications: add the security team's address."},
			{ID: "8.1.14", Title: "Ensure that 'Notify about alerts with the following severity (or higher)' is Enabled", Section: azDefender, Manual: true,
				Guidance: azTenant + "Defender for Cloud > Environment settings > Email notifications: notify about alerts of severity High (or lower)."},
			{ID: "8.1.15", Title: "Ensure that 'Notify about attack paths with the following risk level (or higher)' is Enabled", Section: azDefender, Manual: true,
				Guidance: azTenant + "Defender for Cloud > Environment settings > Email notifications: notify about attack paths of risk level Critical (or lower)."},
			{ID: "8.1.16", Title: "Ensure that Microsoft Defender External Attack Surface Monitoring (EASM) is Enabled", Section: azDefender, Manual: true,
				Guidance: azTenant + "Create a Defender EASM workspace and discovery for your domains and IP ranges."},
			{ID: "8.2.1", Title: "Ensure That Microsoft Defender for IoT Hub Is Set To 'On'", Section: azDefender, Manual: true, AppliesTo: []string{"azurerm_iothub"},
				Guidance: "Enable Defender for IoT on each IoT hub (azurerm_iot_security_solution), then attest it."},

			// 8.3 Key Vault
			{ID: "8.3.1", Title: "Ensure that the Expiration Date is Set for all Keys in Key Vaults using RBAC", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_expiration"}, AppliesTo: []string{"azurerm_key_vault_key"},
				Guidance: "Nimbo checks that every azurerm_key_vault_key in the design sets expiration_date, whatever the vault's permission model, so 8.3.1 and 8.3.2 share the result."},
			{ID: "8.3.2", Title: "Ensure that the Expiration Date is set for All Keys in Key Vaults using access policies (legacy)", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_expiration"}, AppliesTo: []string{"azurerm_key_vault_key"},
				Guidance: "Nimbo checks that every azurerm_key_vault_key in the design sets expiration_date, whatever the vault's permission model, so 8.3.1 and 8.3.2 share the result."},
			{ID: "8.3.3", Title: "Ensure that the Expiration Date is set for All Secrets in Key Vaults using RBAC", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_expiration"}, AppliesTo: []string{"azurerm_key_vault_secret"},
				Guidance: "Nimbo checks that every azurerm_key_vault_secret in the design sets expiration_date, whatever the vault's permission model, so 8.3.3 and 8.3.4 share the result."},
			{ID: "8.3.4", Title: "Ensure that the Expiration Date is set for All Secrets in Key Vaults using access policies (legacy)", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_expiration"}, AppliesTo: []string{"azurerm_key_vault_secret"},
				Guidance: "Nimbo checks that every azurerm_key_vault_secret in the design sets expiration_date, whatever the vault's permission model, so 8.3.3 and 8.3.4 share the result."},
			{ID: "8.3.5", Title: "Ensure 'Purge protection' is Set to 'Enabled'", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_purge_protection"}, AppliesTo: azVaults,
				Guidance: "Nimbo checks that every Key Vault sets purge_protection_enabled = true (it defaults to false)."},
			{ID: "8.3.6", Title: "Ensure that Role Based Access Control for Azure Key Vault is Enabled", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_rbac"}, AppliesTo: azVaults,
				Guidance: "Nimbo checks that every Key Vault sets rbac_authorization_enabled = true (or the older enable_rbac_authorization)."},
			{ID: "8.3.7", Title: "Ensure Public Network Access is Disabled", Section: azKeyVault,
				Rules: []string{"bp_azure_public_network_access"}, AppliesTo: azVaults,
				Guidance: "Nimbo checks that every Key Vault sets public_network_access_enabled = false (it defaults to true)."},
			{ID: "8.3.8", Title: "Ensure Private Endpoints are Used to Access Azure Key Vault", Section: azKeyVault,
				Rules: []string{"bp_azure_private_endpoint"}, AppliesTo: azVaults,
				Guidance: "Nimbo checks that an azurerm_private_endpoint in the design connects to every Key Vault (private_service_connection.private_connection_resource_id)."},
			{ID: "8.3.9", Title: "Ensure Automatic Key Rotation is Enabled within Azure Key Vault", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_key_rotation"}, AppliesTo: []string{"azurerm_key_vault_key"},
				Guidance: "Nimbo checks that every azurerm_key_vault_key has rotation_policy { automatic { ... } }."},
			{ID: "8.3.10", Title: "Ensure that Azure Key Vault Managed HSM is Used when Required", Section: azKeyVault, Manual: true, AppliesTo: azVaults,
				Guidance: "Whether keys need a single-tenant FIPS 140-3 Level 3 HSM is a regulatory decision: if so, use azurerm_key_vault_managed_hardware_security_module, then attest it."},
			{ID: "8.3.11", Title: "Ensure Certificate 'Validity Period (in months)' is Less Than or Equal to '12'", Section: azKeyVault,
				Rules: []string{"bp_azure_kv_cert_validity"}, AppliesTo: []string{"azurerm_key_vault_certificate"},
				Guidance: "Nimbo flags azurerm_key_vault_certificate resources whose x509_certificate_properties.validity_in_months is above 12. Imported certificates have no policy to check."},

			// 8.4 Azure Bastion, 8.5 DDoS
			{ID: "8.4.1", Title: "Ensure an Azure Bastion Host Exists", Section: azSecurity, Manual: true,
				Guidance: azTenant + "At least one azurerm_bastion_host (in an AzureBastionSubnet) serves the subscription's virtual machines; it may live in a hub workflow."},
			{ID: "8.5", Title: "Ensure Azure DDoS Network Protection is Enabled on Virtual Networks", Section: azSecurity,
				Rules: []string{"bp_azure_vnet_ddos"}, AppliesTo: []string{"azurerm_virtual_network"},
				Guidance: "Nimbo checks that every azurerm_virtual_network has ddos_protection_plan { enable = true }."},

			// 9 Storage Services
			{ID: "9.1.1", Title: "Ensure Soft Delete for Azure File Shares is Enabled", Section: azFiles,
				Rules: []string{"bp_azure_file_share_soft_delete"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that storage accounts hosting an azurerm_storage_share have share_properties.retention_policy. Accounts without file shares have nothing to flag."},
			{ID: "9.1.2", Title: "Ensure 'SMB protocol version' is Set to 'SMB 3.1.1' or Higher for SMB file shares", Section: azFiles,
				Rules: []string{"bp_azure_smb_version"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that storage accounts hosting SMB file shares set share_properties.smb.versions to SMB3.1.1 only (all versions are allowed when unset). Accounts without SMB shares have nothing to flag."},
			{ID: "9.1.3", Title: "Ensure 'SMB channel encryption' is Set to 'AES-256-GCM' or Higher for SMB file shares", Section: azFiles,
				Rules: []string{"bp_azure_smb_encryption"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that storage accounts hosting SMB file shares set share_properties.smb.channel_encryption_type to AES-256-GCM only. Accounts without SMB shares have nothing to flag."},
			{ID: "9.2.1", Title: "Ensure That Soft Delete for Blobs on Azure Blob Storage Storage Accounts is Enabled", Section: azBlob,
				Rules: []string{"bp_azure_blob_soft_delete"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account with Blob storage has blob_properties.delete_retention_policy."},
			{ID: "9.2.2", Title: "Ensure that Soft Delete for Containers on Azure Blob Storage Storage Accounts is Enabled", Section: azBlob,
				Rules: []string{"bp_azure_container_soft_delete"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account with Blob storage has blob_properties.container_delete_retention_policy."},
			{ID: "9.2.3", Title: "Ensure 'Versioning' is Set to 'Enabled' on Azure Blob Storage Storage Accounts", Section: azBlob,
				Rules: []string{"bp_azure_blob_versioning"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account with Blob storage sets blob_properties.versioning_enabled = true (Data Lake Gen2 accounts, which don't support versioning, are skipped)."},
			{ID: "9.3.1.1", Title: "Ensure That 'Enable key rotation reminders' is Enabled for Each Storage Account", Section: azStorage, Manual: true, AppliesTo: azStorageAccount,
				Guidance: "Set a key expiration period (keyPolicy.keyExpirationPeriodInDays) on each account in the portal or with Azure Policy; the azurerm provider doesn't expose it. Then attest it."},
			{ID: "9.3.1.2", Title: "Ensure That Storage Account Access keys are Periodically Regenerated", Section: azStorage, Manual: true, AppliesTo: azStorageAccount,
				Guidance: "Regenerate the access keys at least every 90 days (or disable shared key access, 9.3.1.3), then attest it."},
			{ID: "9.3.1.3", Title: "Ensure 'Allow storage account key access' for Azure Storage Accounts is 'Disabled'", Section: azStorage,
				Rules: []string{"bp_azure_storage_shared_key"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account sets shared_access_key_enabled = false (it defaults to true)."},
			{ID: "9.3.2.1", Title: "Ensure Private Endpoints are Used to Access Storage Accounts", Section: azStorage,
				Rules: []string{"bp_azure_private_endpoint"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that an azurerm_private_endpoint in the design connects to every storage account (private_service_connection.private_connection_resource_id)."},
			{ID: "9.3.2.2", Title: "Ensure that 'Public Network Access' is 'Disabled' for Storage Accounts", Section: azStorage,
				Rules: []string{"bp_azure_public_network_access"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account sets public_network_access_enabled = false (it defaults to true)."},
			{ID: "9.3.2.3", Title: "Ensure Default Network Access Rule for Storage Accounts is Set to Deny", Section: azStorage,
				Rules: []string{"bp_azure_storage_default_deny"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account with a public endpoint has network rules (inline or azurerm_storage_account_network_rules) with default_action = \"Deny\"; accounts with public network access disabled pass."},
			{ID: "9.3.3.1", Title: "Ensure that 'Default to Microsoft Entra authorization in the Azure portal' is Set to 'Enabled'", Section: azStorage,
				Rules: []string{"bp_azure_storage_entra_default"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account sets default_to_oauth_authentication = true (it defaults to false)."},
			{ID: "9.3.4", Title: "Ensure that 'Secure transfer required' is Set to 'Enabled'", Section: azStorage,
				Rules: []string{"bp_azure_storage_secure_transfer"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo flags storage accounts that set https_traffic_only_enabled = false; the provider default is true."},
			{ID: "9.3.5", Title: "Ensure 'Allow trusted Microsoft services to access this resource' is Enabled for Storage Account Access", Section: azStorage,
				Rules: []string{"bp_azure_storage_trusted_services"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo flags storage network rules (inline or azurerm_storage_account_network_rules) whose bypass leaves out AzureServices; the default bypass keeps it."},
			{ID: "9.3.6", Title: "Ensure the 'Minimum TLS version' for Storage Accounts is Set to 'Version 1.2'", Section: azStorage,
				Rules: []string{"bp_azure_storage_min_tls"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo flags storage accounts that set min_tls_version to TLS1_0 or TLS1_1; the provider default for new accounts is TLS1_2."},
			{ID: "9.3.7", Title: "Ensure 'Cross Tenant Replication' is Not Enabled", Section: azStorage,
				Rules: []string{"bp_azure_storage_cross_tenant"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo flags storage accounts that set cross_tenant_replication_enabled = true; the provider default is false."},
			{ID: "9.3.8", Title: "Ensure that 'Allow Blob Anonymous Access' is Set to 'Disabled'", Section: azStorage,
				Rules: []string{"bp_public_bucket", "bp_azure_storage_anonymous_access"}, AppliesTo: azStorageAccount,
				Guidance: "Nimbo checks that every storage account sets allow_nested_items_to_be_public = false: the provider default is true, and setting it true is flagged as a public bucket (or, with a static website or Public tag, by the anonymous-access rule)."},
			{ID: "9.3.9", Title: "Ensure Azure Resource Manager Delete Locks are Applied to Azure Storage Accounts", Section: azStorage, Manual: true, AppliesTo: azStorageAccount,
				Guidance: "Add an azurerm_management_lock with lock_level = \"CanNotDelete\" on the storage accounts that must not be deleted, then attest it."},
			{ID: "9.3.10", Title: "Ensure Azure Resource Manager ReadOnly Locks are Considered for Azure Storage Accounts", Section: azStorage, Manual: true, AppliesTo: azStorageAccount,
				Guidance: "Consider a ReadOnly azurerm_management_lock for accounts whose configuration must not change (it also blocks listing keys, so test your clients), then attest the decision."},
			{ID: "9.3.11", Title: "Ensure Redundancy is Set to 'geo-redundant storage (GRS)' on Critical Azure Storage Accounts", Section: azStorage, Manual: true, AppliesTo: azStorageAccount,
				Guidance: "Which accounts are critical is your call: give those account_replication_type = \"GRS\", \"RAGRS\", \"GZRS\" or \"RAGZRS\", then attest it."},
		},
	})
}
