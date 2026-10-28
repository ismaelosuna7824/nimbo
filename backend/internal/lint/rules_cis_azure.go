package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Azure rules backing the CIS Microsoft Azure Foundations Benchmark
// (compliance/cis_azure.go). The default ones only flag a setting written
// the insecure way; the opt-in ones (private networking, soft delete, Key
// Vault hardening, ...) are compliance requirements the catalog templates
// and fresh starters don't follow (a starter storage account keeps its
// public endpoint, a starter Key Vault has purge protection off), so they
// stay out of Problems until the user turns them on. Compliance grading runs
// them anyway.
func init() {
	rules = append(rules,
		// Storage accounts (CIS 9.x).
		azRule(warning("bp_azure_storage_secure_transfer", "Azure: storage requires secure transfer", CategorySecurity,
			"Storage accounts don't turn off https_traffic_only_enabled, so requests over plain HTTP are refused (CIS Azure 9.3.4)."),
			"https://learn.microsoft.com/azure/storage/common/storage-require-secure-transfer", false, checkAzStorageSecureTransfer),
		azRule(warning("bp_azure_storage_min_tls", "Azure: storage accepts TLS 1.2 or later only", CategorySecurity,
			"Storage accounts don't lower min_tls_version to TLS1_0 or TLS1_1 (CIS Azure 9.3.6)."),
			"https://learn.microsoft.com/azure/storage/common/transport-layer-security-configure-minimum-version", false, checkAzStorageMinTLS),
		azRule(info("bp_azure_storage_cross_tenant", "Azure: no cross-tenant replication", CategorySecurity,
			"Storage accounts don't set cross_tenant_replication_enabled = true, so object replication can't copy data to another Entra tenant (CIS Azure 9.3.7)."),
			"https://learn.microsoft.com/azure/storage/common/object-replication-prevent-cross-tenant-policies", false, checkAzStorageCrossTenant),
		azRule(info("bp_azure_storage_trusted_services", "Azure: storage firewalls let trusted Azure services in", CategorySecurity,
			"Storage network rules that set bypass keep \"AzureServices\" in it, so Azure Backup, Monitor and other trusted services still reach a locked-down account (CIS Azure 9.3.5)."),
			"https://learn.microsoft.com/azure/storage/common/storage-network-security#grant-access-to-trusted-azure-services", false, checkAzStorageTrustedServices),
		azRule(info("bp_azure_storage_anonymous_access", "Azure: storage disallows anonymous blob access", CategorySecurity,
			"Storage accounts set allow_nested_items_to_be_public = false; the provider default (true) lets any container be made public (CIS Azure 9.3.8)."),
			"https://learn.microsoft.com/azure/storage/blobs/anonymous-read-access-prevent", true, checkAzStorageAnonymousAccess),
		azRule(info("bp_azure_storage_shared_key", "Azure: storage disallows shared key access", CategorySecurity,
			"Storage accounts set shared_access_key_enabled = false so every request is authorized with Microsoft Entra ID (CIS Azure 9.3.1.3)."),
			"https://learn.microsoft.com/azure/storage/common/shared-key-authorization-prevent", true, checkAzStorageSharedKey),
		azRule(info("bp_azure_storage_default_deny", "Azure: storage firewalls deny by default", CategorySecurity,
			"Storage accounts with a public endpoint have network rules with default_action = \"Deny\" (inline or azurerm_storage_account_network_rules) (CIS Azure 9.3.2.3)."),
			"https://learn.microsoft.com/azure/storage/common/storage-network-security", true, checkAzStorageDefaultDeny),
		azRule(info("bp_azure_storage_entra_default", "Azure: the portal uses Microsoft Entra authorization for storage", CategorySecurity,
			"Storage accounts set default_to_oauth_authentication = true so the Azure portal authorizes data access with Entra ID instead of the account key (CIS Azure 9.3.3.1)."),
			"https://learn.microsoft.com/azure/storage/blobs/authorize-data-operations-portal#default-to-microsoft-entra-authorization-in-the-azure-portal", true, checkAzStorageEntraDefault),
		azRule(info("bp_azure_blob_soft_delete", "Azure: deleted blobs can be recovered", CategorySecurity,
			"Storage accounts with Blob storage have blob_properties.delete_retention_policy (blob soft delete) (CIS Azure 9.2.1)."),
			"https://learn.microsoft.com/azure/storage/blobs/soft-delete-blob-overview", true, checkAzBlobSoftDelete),
		azRule(info("bp_azure_container_soft_delete", "Azure: deleted containers can be recovered", CategorySecurity,
			"Storage accounts with Blob storage have blob_properties.container_delete_retention_policy (container soft delete) (CIS Azure 9.2.2)."),
			"https://learn.microsoft.com/azure/storage/blobs/soft-delete-container-overview", true, checkAzContainerSoftDelete),
		azRule(info("bp_azure_blob_versioning", "Azure: blobs keep previous versions", CategorySecurity,
			"Storage accounts with Blob storage set blob_properties.versioning_enabled = true (CIS Azure 9.2.3)."),
			"https://learn.microsoft.com/azure/storage/blobs/versioning-overview", true, checkAzBlobVersioning),
		azRule(info("bp_azure_file_share_soft_delete", "Azure: deleted file shares can be recovered", CategorySecurity,
			"Storage accounts that host file shares have share_properties.retention_policy (file share soft delete) (CIS Azure 9.1.1)."),
			"https://learn.microsoft.com/azure/storage/files/storage-files-prevent-file-share-deletion", false, checkAzFileShareSoftDelete),
		azRule(info("bp_azure_smb_version", "Azure: SMB file shares require SMB 3.1.1", CategorySecurity,
			"Storage accounts that host SMB file shares set share_properties.smb.versions = [\"SMB3.1.1\"] (CIS Azure 9.1.2)."),
			"https://learn.microsoft.com/azure/storage/files/files-smb-protocol#smb-security-settings", true, checkAzSMBVersion),
		azRule(info("bp_azure_smb_encryption", "Azure: SMB file shares encrypt with AES-256-GCM", CategorySecurity,
			"Storage accounts that host SMB file shares set share_properties.smb.channel_encryption_type = [\"AES-256-GCM\"] (CIS Azure 9.1.3)."),
			"https://learn.microsoft.com/azure/storage/files/files-smb-protocol#smb-security-settings", true, checkAzSMBEncryption),

		// Private networking shared by storage, Key Vault and Databricks.
		azRule(info("bp_azure_public_network_access", "Azure: services turn off public network access", CategorySecurity,
			"Storage accounts, Key Vaults and Databricks workspaces set public_network_access_enabled = false and are reached through private endpoints (CIS Azure 9.3.2.2, 8.3.7, 2.1.10)."),
			"https://learn.microsoft.com/security/benchmark/azure/mcsb-network-security#ns-2-secure-cloud-native-services-with-network-controls", true, checkAzPublicNetworkAccess),
		azRule(info("bp_azure_private_endpoint", "Azure: services are reached through private endpoints", CategorySecurity,
			"Storage accounts, Key Vaults and Databricks workspaces have an azurerm_private_endpoint connected to them (CIS Azure 9.3.2.1, 8.3.8, 2.1.11)."),
			"https://learn.microsoft.com/azure/private-link/private-endpoint-overview", true, checkAzPrivateEndpoint),
		azRule(info("bp_azure_diagnostic_logs", "Azure: Key Vaults and Databricks send their logs", CategorySecurity,
			"Key Vaults have a diagnostic setting with the AuditEvent category (or the audit/allLogs group) and Databricks workspaces one with any log category (CIS Azure 6.1.1.4, 2.1.7)."),
			"https://learn.microsoft.com/azure/key-vault/general/logging", true, checkAzDiagnosticLogs),

		// Key Vault (CIS 8.3).
		azRule(info("bp_azure_kv_purge_protection", "Azure: Key Vaults have purge protection", CategorySecurity,
			"Key Vaults set purge_protection_enabled = true so deleted vaults and objects can't be purged before the retention period ends (CIS Azure 8.3.5)."),
			"https://learn.microsoft.com/azure/key-vault/general/soft-delete-overview#purge-protection", true, checkAzKVPurgeProtection),
		azRule(info("bp_azure_kv_rbac", "Azure: Key Vaults use Azure RBAC", CategorySecurity,
			"Key Vaults set rbac_authorization_enabled = true instead of legacy access policies (CIS Azure 8.3.6)."),
			"https://learn.microsoft.com/azure/key-vault/general/rbac-guide", true, checkAzKVRBAC),
		azRule(info("bp_azure_kv_expiration", "Azure: Key Vault keys and secrets expire", CategorySecurity,
			"Key Vault keys and secrets set expiration_date (CIS Azure 8.3.1-8.3.4)."),
			"https://learn.microsoft.com/azure/key-vault/general/about-keys-secrets-certificates#object-types", true, checkAzKVExpiration),
		azRule(info("bp_azure_kv_key_rotation", "Azure: Key Vault keys rotate automatically", CategorySecurity,
			"Key Vault keys have a rotation_policy with an automatic block (CIS Azure 8.3.9)."),
			"https://learn.microsoft.com/azure/key-vault/keys/how-to-configure-key-rotation", true, checkAzKVKeyRotation),
		azRule(info("bp_azure_kv_cert_validity", "Azure: Key Vault certificates are valid 12 months at most", CategorySecurity,
			"Certificates Key Vault issues don't set validity_in_months above 12 (CIS Azure 8.3.11)."),
			"https://learn.microsoft.com/azure/key-vault/certificates/about-certificates", false, checkAzKVCertValidity),

		// Networking (CIS 7.x).
		azRule(warning("bp_azure_nsg_udp_internet", "Azure: no amplification-prone UDP ports open to the internet", CategorySecurity,
			"Inbound NSG rules don't allow UDP (or any protocol) from the internet to DNS 53, NTP 123, SNMP 161, CLDAP 389 or SSDP 1900 (CIS Azure 7.3)."),
			"https://learn.microsoft.com/azure/virtual-network/network-security-groups-overview", false, checkAzNSGUDPInternet),
		azRule(info("bp_azure_flow_log_retention", "Azure: flow logs are kept 90 days or more", CategorySecurity,
			"Network Watcher flow logs with a retention policy keep records 90 days or more (0 = forever) (CIS Azure 7.5, 7.8)."),
			"https://learn.microsoft.com/azure/network-watcher/vnet-flow-logs-overview", false, checkAzFlowLogRetention),
		azRule(info("bp_azure_subnet_nsg", "Azure: subnets have a network security group", CategorySecurity,
			"Every subnet is associated with a network security group (azurerm_subnet_network_security_group_association, or security_group on an inline subnet); gateway, firewall and route server subnets are exempt (CIS Azure 7.11)."),
			"https://learn.microsoft.com/azure/virtual-network/network-security-group-how-it-works", false, checkAzSubnetNSG),
		azRule(warning("bp_azure_appgw_tls", "Azure: Application Gateways accept TLS 1.2 or later only", CategorySecurity,
			"Application Gateway SSL policies don't set min_protocol_version TLSv1_0/TLSv1_1 or the legacy AppGwSslPolicy20150501/20170401 policies (CIS Azure 7.12)."),
			"https://learn.microsoft.com/azure/application-gateway/application-gateway-ssl-policy-overview", false, checkAzAppGwTLS),
		azRule(info("bp_azure_appgw_waf", "Azure: Application Gateways run the WAF", CategorySecurity,
			"Application Gateways use the WAF_v2 (or WAF) tier with a Web Application Firewall policy or an enabled waf_configuration (CIS Azure 7.10)."),
			"https://learn.microsoft.com/azure/web-application-firewall/ag/ag-overview", false, checkAzAppGwWAF),
		azRule(info("bp_azure_appgw_http2", "Azure: Application Gateways enable HTTP/2", CategorySecurity,
			"Application Gateways set http2_enabled = true (CIS Azure 7.13)."),
			"https://learn.microsoft.com/azure/application-gateway/configuration-listeners#http2-support", false, checkAzAppGwHTTP2),
		azRule(info("bp_azure_waf_request_body", "Azure: WAF inspects request bodies", CategorySecurity,
			"Web Application Firewall policies and Application Gateway waf_configuration don't set request_body_check = false (CIS Azure 7.14)."),
			"https://learn.microsoft.com/azure/web-application-firewall/ag/application-gateway-waf-request-size-limits", false, checkAzWAFRequestBody),
		azRule(info("bp_azure_waf_bot_protection", "Azure: WAF policies block bad bots", CategorySecurity,
			"Web Application Firewall policies include the Microsoft_BotManagerRuleSet managed rule set (CIS Azure 7.15)."),
			"https://learn.microsoft.com/azure/web-application-firewall/ag/bot-protection-overview", false, checkAzWAFBotProtection),
		azRule(info("bp_azure_vpn_entra_auth", "Azure: point-to-site VPN uses Microsoft Entra ID", CategorySecurity,
			"VPN gateways with a point-to-site configuration authenticate clients with Microsoft Entra ID only (vpn_auth_types = [\"AAD\"]), not certificates or RADIUS (CIS Azure 7.9)."),
			"https://learn.microsoft.com/azure/vpn-gateway/openvpn-azure-ad-tenant", false, checkAzVPNEntraAuth),
		azRule(info("bp_azure_vnet_ddos", "Azure: virtual networks have DDoS Network Protection", CategorySecurity,
			"Virtual networks enable a DDoS protection plan (ddos_protection_plan { enable = true }) (CIS Azure 8.5)."),
			"https://learn.microsoft.com/azure/ddos-protection/ddos-protection-overview", true, checkAzVNetDDoS),

		// Databricks (CIS 2.1).
		azRule(info("bp_azure_databricks_vnet", "Azure: Databricks runs in your virtual network", CategorySecurity,
			"Databricks workspaces are deployed into a customer-managed VNet (custom_parameters.virtual_network_id) (CIS Azure 2.1.1)."),
			"https://learn.microsoft.com/azure/databricks/security/network/classic/vnet-inject", false, checkAzDatabricksVNet),
		azRule(warning("bp_azure_databricks_public_ip", "Azure: Databricks clusters have no public IPs", CategorySecurity,
			"Databricks workspaces don't set custom_parameters.no_public_ip = false (secure cluster connectivity) (CIS Azure 2.1.9)."),
			"https://learn.microsoft.com/azure/databricks/security/network/classic/secure-cluster-connectivity", false, checkAzDatabricksPublicIP),

		// Identity (CIS 5.4).
		azRule(warning("bp_azure_custom_admin_role", "Azure: no custom subscription administrator roles", CategorySecurity,
			"Custom role definitions don't allow every action (\"*\") at a subscription, management group or root scope (CIS Azure 5.4)."),
			"https://learn.microsoft.com/azure/role-based-access-control/custom-roles", false, checkAzCustomAdminRole),
	)
}

func azRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass)) rule {
	r.Docs = docs
	r.DefaultOff = optIn
	return rule{r, check}
}

// ---------------------------------------------------------------------------
// Helpers

// azUnsetOr reports whether the bool attribute name is unset or set to the
// literal v. A reference or expression counts as a decision made elsewhere.
func azUnsetOr(b *model.Body, name string, v bool) bool {
	if getAttr(b, name) == nil {
		return true
	}
	got, set := attrBool(b, name)
	return set && got == v
}

// azNested returns the body of the first block at path ("blob_properties",
// "delete_retention_policy"), nil when a block is missing.
func azNested(b *model.Body, path ...string) *model.Body {
	for _, typ := range path {
		bs := blocks(b, typ)
		if len(bs) == 0 {
			return nil
		}
		b = &bs[0].Body
	}
	return b
}

// azHasDynamic reports whether b generates typ blocks with a dynamic block,
// whose content Nimbo can't evaluate (counted as intent).
func azHasDynamic(b *model.Body, typ string) bool {
	for _, d := range blocks(b, "dynamic") {
		if len(d.Labels) == 1 && d.Labels[0] == typ {
			return true
		}
	}
	return false
}

var azIndex = regexp.MustCompile(`\[\d+\]`)

// azReferrers returns the resources of typ with an attribute at one of
// paths (dotted, nested blocks without index:
// "private_service_connection.private_connection_resource_id") that refers
// to n.
func (p *pass) azReferrers(n *model.Node, typ string, paths ...string) []*model.Node {
	var out []*model.Node
	for _, o := range p.ofType(typ) {
		found := false
		walkAttrs(&o.Body, "", func(path string, a *model.Attr) {
			if !found && slices.Contains(paths, azIndex.ReplaceAllString(path, "")) && referencesNode(a.Value, n) {
				found = true
			}
		})
		if found {
			out = append(out, o)
		}
	}
	return out
}

// azBlobAccount: the storage account has the Blob service (every kind but
// premium FileStorage).
func azBlobAccount(n *model.Node) bool {
	kind, _ := attrString(&n.Body, "account_kind")
	return kind != "FileStorage"
}

// azShareAccounts returns the storage accounts hosting file shares; smbOnly
// leaves out the shares created with enabled_protocol = "NFS".
func (p *pass) azShareAccounts(smbOnly bool) []*model.Node {
	var out []*model.Node
	for _, n := range p.ofType("azurerm_storage_account") {
		for _, s := range p.azReferrers(n, "azurerm_storage_share", "storage_account_id", "storage_account_name") {
			if proto, _ := attrString(&s.Body, "enabled_protocol"); smbOnly && strings.EqualFold(proto, "NFS") {
				continue
			}
			out = append(out, n)
			break
		}
	}
	return out
}

// azNetworkRules returns the network rule bodies of a storage account:
// its inline network_rules block and the azurerm_storage_account_network_rules
// resources that point at it.
func (p *pass) azNetworkRules(n *model.Node) []*model.Body {
	var out []*model.Body
	for _, b := range blocks(&n.Body, "network_rules") {
		out = append(out, &b.Body)
	}
	for _, r := range p.azReferrers(n, "azurerm_storage_account_network_rules", "storage_account_id") {
		out = append(out, &r.Body)
	}
	return out
}

// ---------------------------------------------------------------------------
// Storage accounts

func checkAzStorageSecureTransfer(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if hasLiteralFalse(&n.Body, "https_traffic_only_enabled") {
			p.report(finding{
				node: n, attribute: "https_traffic_only_enabled",
				message: fmt.Sprintf("%s accepts requests over plain HTTP", addr(n)),
				why:     "Without secure transfer, data, account keys in connection strings and SAS tokens can travel unencrypted and be read or altered on the way.",
				fix:     "Remove https_traffic_only_enabled (it defaults to true) or set it to true; clients must use https:// endpoints.",
			})
		}
	}
}

func checkAzStorageMinTLS(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if v, _ := attrString(&n.Body, "min_tls_version"); v == "TLS1_0" || v == "TLS1_1" {
			p.report(finding{
				node: n, attribute: "min_tls_version",
				message: fmt.Sprintf("%s accepts %s", addr(n), strings.Replace(v, "_", ".", 1)),
				why:     "TLS 1.0 and 1.1 have known weaknesses and are deprecated; allowing them lets clients negotiate a breakable connection.",
				fix:     "Set min_tls_version = \"TLS1_2\" (the default for new accounts).",
			})
		}
	}
}

func checkAzStorageCrossTenant(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if attrIsTrue(&n.Body, "cross_tenant_replication_enabled") {
			p.report(finding{
				node: n, attribute: "cross_tenant_replication_enabled",
				message: fmt.Sprintf("%s allows object replication to other tenants", addr(n)),
				why:     "Cross-tenant replication lets anyone who can write a replication policy copy the account's blobs to a tenant your organization doesn't control.",
				fix:     "Set cross_tenant_replication_enabled = false (the default) unless replication to a partner tenant is required.",
			})
		}
	}
}

func checkAzStorageTrustedServices(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		for _, b := range p.azNetworkRules(n) {
			if getAttr(b, "bypass") == nil {
				continue // defaults to AzureServices
			}
			bypass := attrStrings(b, "bypass")
			if len(bypass) == 0 || slices.ContainsFunc(bypass, func(s string) bool { return strings.EqualFold(s, "AzureServices") }) {
				continue // not constant, or trusted services kept
			}
			p.report(finding{
				node: n, attribute: "network_rules",
				message: fmt.Sprintf("%s network rules don't let trusted Azure services in (bypass = %s)", addr(n), strings.Join(bypass, ", ")),
				why:     "Without the AzureServices bypass, trusted services such as Azure Backup, Monitor, Event Grid and Defender can't reach the account once its firewall denies by default, so backups, logs and threat detection silently stop.",
				fix:     "Add \"AzureServices\" to bypass (the default).",
			})
			break
		}
	}
}

func checkAzStorageAnonymousAccess(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		var msg string
		switch {
		case getAttr(&n.Body, "allow_nested_items_to_be_public") == nil:
			msg = fmt.Sprintf("%s lets containers be made public (allow_nested_items_to_be_public defaults to true)", addr(n))
		case attrIsTrue(&n.Body, "allow_nested_items_to_be_public") && p.publicIntent(n):
			// Without public intent, bp_public_bucket reports it.
			msg = fmt.Sprintf("%s allows anonymous blob access", addr(n))
		default:
			continue
		}
		p.report(finding{
			node: n, attribute: "allow_nested_items_to_be_public",
			message: msg,
			why:     "While the account allows it, any container can be switched to anonymous read access, and everything in it becomes world-readable.",
			fix:     "Set allow_nested_items_to_be_public = false. A static website ($web) keeps working: its endpoint doesn't depend on this setting; share other content with SAS tokens or a CDN.",
		})
	}
}

func checkAzStorageSharedKey(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if azUnsetOr(&n.Body, "shared_access_key_enabled", true) {
			p.report(finding{
				node: n, attribute: "shared_access_key_enabled",
				message: fmt.Sprintf("%s accepts shared key (account key) authorization", addr(n)),
				why:     "Account keys grant full access to all data, never expire on their own and leave no identity in the logs; Entra ID authorization is scoped by RBAC and auditable.",
				fix:     "Set shared_access_key_enabled = false, give apps RBAC roles (Storage Blob Data Contributor, ...) on managed identities, and set storage_use_azuread = true in the azurerm provider so Terraform uses Entra ID too.",
			})
		}
	}
}

func checkAzStorageDefaultDeny(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if hasLiteralFalse(&n.Body, "public_network_access_enabled") {
			continue // no public endpoint: network rules don't apply
		}
		deny := false
		for _, b := range p.azNetworkRules(n) {
			a := getAttr(b, "default_action")
			v, _ := attrString(b, "default_action")
			deny = deny || a != nil && (strings.EqualFold(v, "Deny") || literal(a.Value) == nil)
		}
		if !deny {
			p.report(finding{
				node: n, attribute: "network_rules",
				message: fmt.Sprintf("%s accepts connections from any network", addr(n)),
				why:     "With the default Allow action, the account's public endpoint answers every network on the internet; leaked keys or SAS tokens work from anywhere.",
				fix:     "Add network_rules { default_action = \"Deny\" } with the ip_rules / virtual_network_subnet_ids that need access, or set public_network_access_enabled = false and use a private endpoint.",
			})
		}
	}
}

func checkAzStorageEntraDefault(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if azUnsetOr(&n.Body, "default_to_oauth_authentication", false) {
			p.report(finding{
				node: n, attribute: "default_to_oauth_authentication",
				message: fmt.Sprintf("%s lets the portal use the account key by default", addr(n)),
				why:     "Portal data access with the account key bypasses RBAC and leaves no user identity in the logs.",
				fix:     "Set default_to_oauth_authentication = true.",
			})
		}
	}
}

func checkAzBlobSoftDelete(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if azBlobAccount(n) && azNested(&n.Body, "blob_properties", "delete_retention_policy") == nil {
			p.report(finding{
				node: n, attribute: "blob_properties",
				message: fmt.Sprintf("%s permanently deletes blobs at once", addr(n)),
				why:     "Without soft delete, a blob deleted or overwritten by mistake, a buggy job or ransomware is gone for good.",
				fix:     "Add blob_properties { delete_retention_policy { days = 7 } } (1-365 days).",
			})
		}
	}
}

func checkAzContainerSoftDelete(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		if azBlobAccount(n) && azNested(&n.Body, "blob_properties", "container_delete_retention_policy") == nil {
			p.report(finding{
				node: n, attribute: "blob_properties",
				message: fmt.Sprintf("%s permanently deletes containers at once", addr(n)),
				why:     "Without container soft delete, deleting a container drops every blob in it with no way back.",
				fix:     "Add blob_properties { container_delete_retention_policy { days = 7 } } (1-365 days).",
			})
		}
	}
}

func checkAzBlobVersioning(p *pass) {
	for _, n := range p.ofType("azurerm_storage_account") {
		// Blob versioning isn't available on Data Lake Gen2 (hierarchical
		// namespace) accounts.
		if !azBlobAccount(n) || attrIsTrue(&n.Body, "is_hns_enabled") {
			continue
		}
		props := azNested(&n.Body, "blob_properties")
		if props == nil || azUnsetOr(props, "versioning_enabled", false) {
			p.report(finding{
				node: n, attribute: "blob_properties",
				message: fmt.Sprintf("%s doesn't keep previous blob versions", addr(n)),
				why:     "Without versioning, an overwrite replaces the data for good; soft delete alone doesn't protect against overwrites.",
				fix:     "Set blob_properties { versioning_enabled = true } (add a lifecycle rule to expire old versions and keep costs down).",
			})
		}
	}
}

func checkAzFileShareSoftDelete(p *pass) {
	for _, n := range p.azShareAccounts(false) {
		if azNested(&n.Body, "share_properties", "retention_policy") == nil {
			p.report(finding{
				node: n, attribute: "share_properties",
				message: fmt.Sprintf("%s hosts file shares without soft delete", addr(n)),
				why:     "Without soft delete, deleting a file share (by mistake or by an attacker with the account key) destroys every file in it.",
				fix:     "Add share_properties { retention_policy { days = 7 } } (1-365 days).",
			})
		}
	}
}

// azSMBOnly reads the share_properties.smb set attr of a storage account:
// only reports whether it holds nothing but want (false when unset: every
// value is allowed); ok is false when it isn't constant.
func azSMBOnly(n *model.Node, attr, want string) (only, ok bool) {
	smb := azNested(&n.Body, "share_properties", "smb")
	if smb == nil || getAttr(smb, attr) == nil {
		return false, true
	}
	vals := attrStrings(smb, attr)
	if len(vals) == 0 {
		return false, false
	}
	return !slices.ContainsFunc(vals, func(v string) bool { return v != want }), true
}

func checkAzSMBVersion(p *pass) {
	for _, n := range p.azShareAccounts(true) {
		if only, ok := azSMBOnly(n, "versions", "SMB3.1.1"); ok && !only {
			p.report(finding{
				node: n, attribute: "share_properties",
				message: fmt.Sprintf("%s accepts SMB versions older than 3.1.1", addr(n)),
				why:     "SMB 2.1 and 3.0 lack the pre-authentication integrity checks and AES-GCM encryption of 3.1.1 and are open to downgrade attacks.",
				fix:     "Set share_properties { smb { versions = [\"SMB3.1.1\"] } } (check that every client supports SMB 3.1.1).",
			})
		}
	}
}

func checkAzSMBEncryption(p *pass) {
	for _, n := range p.azShareAccounts(true) {
		if only, ok := azSMBOnly(n, "channel_encryption_type", "AES-256-GCM"); ok && !only {
			p.report(finding{
				node: n, attribute: "share_properties",
				message: fmt.Sprintf("%s accepts SMB channel encryption weaker than AES-256-GCM", addr(n)),
				why:     "Allowing AES-128 ciphers lets clients negotiate weaker channel encryption than the strongest one Azure Files supports.",
				fix:     "Set share_properties { smb { channel_encryption_type = [\"AES-256-GCM\"] } } (check that every client supports it).",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Private networking and logs

var azPrivateTypes = []string{"azurerm_storage_account", "azurerm_key_vault", "azurerm_databricks_workspace"}

func checkAzPublicNetworkAccess(p *pass) {
	for _, n := range p.ofType(azPrivateTypes...) {
		if azUnsetOr(&n.Body, "public_network_access_enabled", true) {
			p.report(finding{
				node: n, attribute: "public_network_access_enabled",
				message: fmt.Sprintf("%s is reachable from public networks", addr(n)),
				why:     "A public endpoint can be reached from the whole internet; leaked credentials, tokens or keys then work from anywhere, and the service is exposed to scanning and brute force.",
				fix:     "Set public_network_access_enabled = false and reach the service through an azurerm_private_endpoint in your virtual network.",
			})
		}
	}
}

func checkAzPrivateEndpoint(p *pass) {
	for _, n := range p.ofType(azPrivateTypes...) {
		if len(p.azReferrers(n, "azurerm_private_endpoint", "private_service_connection.private_connection_resource_id")) == 0 {
			p.report(finding{
				node:    n,
				message: fmt.Sprintf("%s has no private endpoint", addr(n)),
				why:     "Without a private endpoint, traffic to the service leaves your virtual network for its public endpoint, which then can't be turned off.",
				fix:     "Add an azurerm_private_endpoint in a private subnet whose private_service_connection points at this resource's id (with the matching subresource, e.g. \"blob\" or \"vault\") and a private DNS zone group.",
			})
		}
	}
}

// azLogged reports whether one of the diagnostic settings sends a log
// category accepted by ok (a dynamic enabled_log counts).
func azLogged(settings []*model.Node, ok func(category, group string) bool) bool {
	for _, s := range settings {
		if azHasDynamic(&s.Body, "enabled_log") {
			return true
		}
		for _, l := range blocks(&s.Body, "enabled_log") {
			c, _ := attrString(&l.Body, "category")
			g, _ := attrString(&l.Body, "category_group")
			if ok(c, g) {
				return true
			}
		}
	}
	return false
}

func checkAzDiagnosticLogs(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault", "azurerm_databricks_workspace") {
		settings := p.azReferrers(n, "azurerm_monitor_diagnostic_setting", "target_resource_id")
		logged := azLogged(settings, func(c, g string) bool {
			if n.Type == "azurerm_databricks_workspace" {
				return c != "" || g != ""
			}
			return strings.EqualFold(c, "AuditEvent") || strings.EqualFold(g, "audit") || strings.EqualFold(g, "allLogs")
		})
		if logged {
			continue
		}
		what := "audit events (AuditEvent)"
		if n.Type == "azurerm_databricks_workspace" {
			what = "diagnostic logs"
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s doesn't send its %s anywhere", addr(n), what),
			why:     "Without diagnostic logs there is no record of who read a secret or used the workspace, so misuse can't be detected or investigated.",
			fix:     "Add an azurerm_monitor_diagnostic_setting with target_resource_id = this resource's id, a log_analytics_workspace_id (or storage account) and enabled_log { category_group = \"audit\" } (or \"allLogs\").",
		})
	}
}

// ---------------------------------------------------------------------------
// Key Vault

func checkAzKVPurgeProtection(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault") {
		if azUnsetOr(&n.Body, "purge_protection_enabled", false) {
			p.report(finding{
				node: n, attribute: "purge_protection_enabled",
				message: fmt.Sprintf("%s can be purged before its retention period ends", addr(n)),
				why:     "Without purge protection, a deleted vault or key can be purged at once, permanently losing every key and the data encrypted with it.",
				fix:     "Set purge_protection_enabled = true. It can't be turned off again, and a purged-protected vault name stays reserved for soft_delete_retention_days after deletion.",
			})
		}
	}
}

func checkAzKVRBAC(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault") {
		if azUnsetOr(&n.Body, "rbac_authorization_enabled", false) && azUnsetOr(&n.Body, "enable_rbac_authorization", false) {
			p.report(finding{
				node: n, attribute: "rbac_authorization_enabled",
				message: fmt.Sprintf("%s uses legacy access policies", addr(n)),
				why:     "Access policies grant vault-wide permissions, can't be scoped to one secret and are managed apart from the rest of Azure RBAC and PIM.",
				fix:     "Set rbac_authorization_enabled = true and grant azurerm_role_assignment roles (Key Vault Secrets User, ...) instead of access_policy blocks.",
			})
		}
	}
}

func checkAzKVExpiration(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault_key", "azurerm_key_vault_secret") {
		if getAttr(&n.Body, "expiration_date") == nil {
			p.report(finding{
				node: n, attribute: "expiration_date",
				message: fmt.Sprintf("%s never expires", addr(n)),
				why:     "Keys and secrets without an expiration date stay valid forever, so a leaked one is never retired by itself.",
				fix:     "Set expiration_date (RFC 3339, e.g. \"2027-01-01T00:00:00Z\") and rotate before it.",
			})
		}
	}
}

func checkAzKVKeyRotation(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault_key") {
		if azNested(&n.Body, "rotation_policy", "automatic") == nil {
			p.report(finding{
				node: n, attribute: "rotation_policy",
				message: fmt.Sprintf("%s doesn't rotate automatically", addr(n)),
				why:     "Keys that are never rotated protect more data for longer, so a compromised key exposes more.",
				fix:     "Add rotation_policy { expire_after = \"P1Y\" automatic { time_before_expiry = \"P30D\" } }.",
			})
		}
	}
}

func checkAzKVCertValidity(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault_certificate") {
		props := azNested(&n.Body, "certificate_policy", "x509_certificate_properties")
		if props == nil {
			continue // imported certificate
		}
		if m, ok := attrNumber(props, "validity_in_months"); ok && m > 12 {
			p.report(finding{
				node: n, attribute: "certificate_policy",
				message: fmt.Sprintf("%s is valid for %d months", addr(n), int(m)),
				why:     "Long-lived certificates stay usable for longer if their private key leaks, and public CAs no longer issue TLS certificates valid this long.",
				fix:     "Set validity_in_months to 12 or less and let Key Vault renew it (lifetime_action AutoRenew).",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Networking

// azUDPPorts are the amplification-prone UDP ports CIS 7.3 lists.
var azUDPPorts = []int{53, 123, 161, 389, 1900}

func azInternetSource(s string) bool {
	switch strings.ToLower(s) {
	case "*", "0.0.0.0", "internet", "any":
		return true
	}
	return strings.HasSuffix(s, "/0")
}

func checkAzNSGUDPInternet(p *pass) {
	check := func(n *model.Node, b *model.Body, path string) {
		dir, _ := attrString(b, "direction")
		access, _ := attrString(b, "access")
		proto, _ := attrString(b, "protocol")
		if !strings.EqualFold(dir, "Inbound") || !strings.EqualFold(access, "Allow") || !(strings.EqualFold(proto, "Udp") || proto == "*") {
			return
		}
		sources := append(attrStrings(b, "source_address_prefixes"), attrStrings(b, "source_address_prefix")...)
		if !slices.ContainsFunc(sources, azInternetSource) {
			return
		}
		var open []string
		for _, r := range parsePorts(append(attrStrings(b, "destination_port_ranges"), attrStrings(b, "destination_port_range")...)) {
			for _, port := range azUDPPorts {
				if r[0] == allPorts || r[0] <= port && port <= r[1] {
					open = append(open, fmt.Sprint(port))
				}
			}
		}
		if len(open) == 0 {
			return
		}
		slices.Sort(open)
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s allows UDP from the internet to port %s", addr(n), strings.Join(slices.Compact(open), ", ")),
			why:     "DNS, NTP, SNMP, CLDAP and SSDP answer small spoofed UDP requests with large replies, so a host exposing them gets used to amplify DDoS attacks on others.",
			fix:     "Restrict the source to the networks that need the service, use protocol = \"Tcp\" if UDP isn't needed, or remove the rule.",
		})
	}
	for _, n := range p.resources() {
		switch n.Type {
		case "azurerm_network_security_rule":
			check(n, &n.Body, "protocol")
		case "azurerm_network_security_group":
			for i, blk := range blocks(&n.Body, "security_rule") {
				check(n, &blk.Body, fmt.Sprintf("security_rule[%d]", i))
			}
		}
	}
}

func checkAzFlowLogRetention(p *pass) {
	for _, n := range p.ofType("azurerm_network_watcher_flow_log") {
		r := azNested(&n.Body, "retention_policy")
		if r == nil || !attrIsTrue(r, "enabled") {
			continue // no retention policy: records are kept
		}
		if days, ok := attrNumber(r, "days"); ok && days > 0 && days < 90 {
			p.report(finding{
				node: n, attribute: "retention_policy",
				message: fmt.Sprintf("%s keeps flow logs %d days", addr(n), int(days)),
				why:     "Intrusions are often found weeks later; flow logs deleted before then can't show where traffic came from or went.",
				fix:     "Set retention_policy { days = 90 } or more (0 keeps them forever).",
			})
		}
	}
}

// azNoNSGSubnets are the subnets Azure doesn't let you (or advises not to)
// attach a network security group to.
var azNoNSGSubnets = []string{"GatewaySubnet", "AzureFirewallSubnet", "AzureFirewallManagementSubnet", "RouteServerSubnet"}

func checkAzSubnetNSG(p *pass) {
	why := "A subnet without a network security group accepts any traffic its route table delivers; one VM exposed by mistake is reachable on every port."
	fix := "Associate a network security group: azurerm_subnet_network_security_group_association with this subnet_id."
	for _, n := range p.ofType("azurerm_subnet") {
		if name, _ := attrString(&n.Body, "name"); slices.Contains(azNoNSGSubnets, name) {
			continue
		}
		if len(p.azReferrers(n, "azurerm_subnet_network_security_group_association", "subnet_id")) == 0 {
			p.report(finding{node: n, message: fmt.Sprintf("%s has no network security group", addr(n)), why: why, fix: fix})
		}
	}
	for _, n := range p.ofType("azurerm_virtual_network") {
		for i, s := range blocks(&n.Body, "subnet") {
			name, _ := attrString(&s.Body, "name")
			if slices.Contains(azNoNSGSubnets, name) || getAttr(&s.Body, "security_group") != nil {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("subnet[%d]", i),
				message: fmt.Sprintf("%s subnet %q has no network security group", addr(n), name),
				why:     why,
				fix:     "Set security_group on the inline subnet to a network security group id.",
			})
		}
	}
}

// azLegacySSLPolicies are the predefined Application Gateway SSL policies
// with a minimum below TLS 1.2.
var azLegacySSLPolicies = map[string]string{"AppGwSslPolicy20150501": "TLS 1.0", "AppGwSslPolicy20170401": "TLS 1.1"}

func checkAzAppGwTLS(p *pass) {
	for _, n := range p.ofType("azurerm_application_gateway") {
		type pol struct {
			body *model.Body
			path string
		}
		var pols []pol
		for i, b := range blocks(&n.Body, "ssl_policy") {
			pols = append(pols, pol{&b.Body, fmt.Sprintf("ssl_policy[%d]", i)})
		}
		for i, prof := range blocks(&n.Body, "ssl_profile") {
			for _, b := range blocks(&prof.Body, "ssl_policy") {
				pols = append(pols, pol{&b.Body, fmt.Sprintf("ssl_profile[%d].ssl_policy", i)})
			}
		}
		for _, sp := range pols {
			version := ""
			if v, _ := attrString(sp.body, "min_protocol_version"); v == "TLSv1_0" || v == "TLSv1_1" {
				version = "TLS " + strings.Replace(strings.TrimPrefix(v, "TLSv"), "_", ".", 1)
			} else if name, _ := attrString(sp.body, "policy_name"); azLegacySSLPolicies[name] != "" {
				version = azLegacySSLPolicies[name]
			}
			if version == "" {
				continue
			}
			p.report(finding{
				node: n, attribute: sp.path,
				message: fmt.Sprintf("%s accepts %s", addr(n), version),
				why:     "TLS 1.0 and 1.1 are deprecated and breakable; Azure stopped supporting them on Application Gateway on 31 August 2025.",
				fix:     "Use policy_type = \"Predefined\" with policy_name = \"AppGwSslPolicy20220101\" (or \"AppGwSslPolicy20220101S\"), or a custom policy with min_protocol_version = \"TLSv1_2\".",
			})
			break
		}
	}
}

func checkAzAppGwWAF(p *pass) {
	for _, n := range p.ofType("azurerm_application_gateway") {
		sku := azNested(&n.Body, "sku")
		if sku == nil {
			continue
		}
		tier, ok := attrString(sku, "tier")
		if !ok {
			continue
		}
		msg := ""
		switch {
		case tier != "WAF" && tier != "WAF_v2":
			msg = fmt.Sprintf("%s uses the %s tier, without a Web Application Firewall", addr(n), tier)
		case getAttr(&n.Body, "firewall_policy_id") != nil:
			if pol := p.referencedNode(&n.Body, "firewall_policy_id"); pol != nil {
				if settings := azNested(&pol.Body, "policy_settings"); settings != nil && hasLiteralFalse(settings, "enabled") {
					msg = fmt.Sprintf("%s uses a disabled WAF policy", addr(n))
				}
			}
		default:
			if waf := azNested(&n.Body, "waf_configuration"); waf == nil || hasLiteralFalse(waf, "enabled") {
				msg = fmt.Sprintf("%s has the WAF tier but no enabled WAF", addr(n))
			}
		}
		if msg != "" {
			p.report(finding{
				node: n, attribute: "sku",
				message: msg,
				why:     "Without a WAF, SQL injection, cross-site scripting and other OWASP Top 10 attacks reach the backends unfiltered.",
				fix:     "Use sku { name = \"WAF_v2\", tier = \"WAF_v2\" } and set firewall_policy_id to an azurerm_web_application_firewall_policy in Prevention mode.",
			})
		}
	}
}

func checkAzAppGwHTTP2(p *pass) {
	for _, n := range p.ofType("azurerm_application_gateway") {
		if azUnsetOr(&n.Body, "http2_enabled", false) && azUnsetOr(&n.Body, "enable_http2", false) {
			p.report(finding{
				node: n, attribute: "http2_enabled",
				message: fmt.Sprintf("%s serves clients over HTTP/1.1 only", addr(n)),
				why:     "HTTP/2 multiplexes requests over one connection, which lowers latency and the number of TLS handshakes clients make.",
				fix:     "Set http2_enabled = true (HTTP/2 is used between clients and the gateway; backends keep HTTP/1.1).",
			})
		}
	}
}

func checkAzWAFRequestBody(p *pass) {
	report := func(n *model.Node, attr string) {
		p.report(finding{
			node: n, attribute: attr,
			message: fmt.Sprintf("%s doesn't inspect request bodies", addr(n)),
			why:     "Without body inspection the WAF only sees URLs and headers; injection payloads sent in POST bodies pass unchecked.",
			fix:     "Remove request_body_check or set it to true (the default).",
		})
	}
	for _, n := range p.ofType("azurerm_web_application_firewall_policy") {
		if s := azNested(&n.Body, "policy_settings"); s != nil && hasLiteralFalse(s, "request_body_check") {
			report(n, "policy_settings")
		}
	}
	for _, n := range p.ofType("azurerm_application_gateway") {
		if w := azNested(&n.Body, "waf_configuration"); w != nil && hasLiteralFalse(w, "request_body_check") {
			report(n, "waf_configuration")
		}
	}
}

func checkAzWAFBotProtection(p *pass) {
	for _, n := range p.ofType("azurerm_web_application_firewall_policy") {
		rules := azNested(&n.Body, "managed_rules")
		if rules == nil || azHasDynamic(rules, "managed_rule_set") {
			continue
		}
		bot := false
		for _, s := range blocks(rules, "managed_rule_set") {
			a := getAttr(&s.Body, "type")
			t, _ := attrString(&s.Body, "type")
			bot = bot || t == "Microsoft_BotManagerRuleSet" || a != nil && literal(a.Value) == nil
		}
		if !bot {
			p.report(finding{
				node: n, attribute: "managed_rules",
				message: fmt.Sprintf("%s has no bot protection rule set", addr(n)),
				why:     "Malicious bots scrape content, stuff credentials and probe for vulnerabilities; the bot manager rule set blocks known bad bots by IP reputation.",
				fix:     "Add managed_rule_set { type = \"Microsoft_BotManagerRuleSet\", version = \"1.1\" } to managed_rules.",
			})
		}
	}
}

func checkAzVPNEntraAuth(p *pass) {
	for _, n := range p.ofType("azurerm_virtual_network_gateway") {
		for i, c := range blocks(&n.Body, "vpn_client_configuration") {
			var other []string
			if getAttr(&c.Body, "vpn_auth_types") != nil {
				for _, t := range attrStrings(&c.Body, "vpn_auth_types") {
					if t != "AAD" {
						other = append(other, t)
					}
				}
			} else {
				if len(blocks(&c.Body, "root_certificate")) > 0 {
					other = append(other, "Certificate")
				}
				if getAttr(&c.Body, "radius_server_address") != nil || len(blocks(&c.Body, "radius_server")) > 0 {
					other = append(other, "Radius")
				}
			}
			if len(other) == 0 {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("vpn_client_configuration[%d]", i),
				message: fmt.Sprintf("%s authenticates point-to-site clients with %s", addr(n), strings.Join(other, " and ")),
				why:     "Certificates and RADIUS accounts live outside Microsoft Entra ID, so Conditional Access, MFA and central account revocation don't apply to VPN sign-ins.",
				fix:     "Set vpn_auth_types = [\"AAD\"] with aad_tenant, aad_audience and aad_issuer, and vpn_client_protocols = [\"OpenVPN\"].",
			})
		}
	}
}

func checkAzVNetDDoS(p *pass) {
	for _, n := range p.ofType("azurerm_virtual_network") {
		plan := azNested(&n.Body, "ddos_protection_plan")
		if plan == nil || hasLiteralFalse(plan, "enable") {
			p.report(finding{
				node: n, attribute: "ddos_protection_plan",
				message: fmt.Sprintf("%s has no DDoS Network Protection", addr(n)),
				why:     "The free infrastructure protection isn't tuned to your traffic; Network Protection adds adaptive tuning, attack telemetry and rapid response for public IPs in the network.",
				fix:     "Create one azurerm_network_ddos_protection_plan (billed per plan, shared by up to 100 public IPs across virtual networks) and set ddos_protection_plan { id = ..., enable = true }.",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Databricks

func checkAzDatabricksVNet(p *pass) {
	for _, n := range p.ofType("azurerm_databricks_workspace") {
		if c := azNested(&n.Body, "custom_parameters"); c == nil || getAttr(c, "virtual_network_id") == nil {
			p.report(finding{
				node: n, attribute: "custom_parameters",
				message: fmt.Sprintf("%s runs its clusters in a Databricks-managed network", addr(n)),
				why:     "In the managed VNet you can't attach your own network security groups, routes or firewalls to cluster traffic.",
				fix:     "Set custom_parameters { virtual_network_id, public_subnet_name, private_subnet_name and both *_network_security_group_association_id } with subnets delegated to Microsoft.Databricks/workspaces.",
			})
		}
	}
}

func checkAzDatabricksPublicIP(p *pass) {
	for _, n := range p.ofType("azurerm_databricks_workspace") {
		if c := azNested(&n.Body, "custom_parameters"); c != nil && hasLiteralFalse(c, "no_public_ip") {
			p.report(finding{
				node: n, attribute: "custom_parameters",
				message: fmt.Sprintf("%s gives cluster nodes public IP addresses", addr(n)),
				why:     "Cluster nodes with public IPs are reachable from the internet; secure cluster connectivity keeps them private and opens no inbound ports.",
				fix:     "Remove no_public_ip or set it to true (the default).",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Identity

var azSubscriptionScope = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/?$`)

// azBroadScope reports whether v names a subscription, management group or
// the root scope: a constant path, or a reference to a subscription or
// management group outside a resource group path.
func azBroadScope(v model.Value) bool {
	for _, s := range stringsOf(v) {
		if s == "/" || azSubscriptionScope.MatchString(s) || strings.Contains(strings.ToLower(s), "/providers/microsoft.management/managementgroups/") {
			return true
		}
	}
	if strings.Contains(strings.ToLower(source(v)), "/resourcegroups/") {
		return false
	}
	for _, r := range references(v) {
		for _, prefix := range []string{"data.azurerm_subscription.", "azurerm_subscription.", "data.azurerm_management_group.", "azurerm_management_group."} {
			if strings.HasPrefix(r, prefix) {
				return true
			}
		}
	}
	return false
}

func checkAzCustomAdminRole(p *pass) {
	for _, n := range p.ofType("azurerm_role_definition") {
		all := false
		for _, perm := range blocks(&n.Body, "permissions") {
			all = all || slices.Contains(attrStrings(&perm.Body, "actions"), "*")
		}
		if !all {
			continue
		}
		scope := getAttr(&n.Body, "assignable_scopes")
		if scope == nil {
			scope = getAttr(&n.Body, "scope") // the default assignable scope
		}
		if scope == nil || !azBroadScope(scope.Value) {
			continue
		}
		p.report(finding{
			node: n, attribute: "permissions",
			message: fmt.Sprintf("%s is a custom role allowing every action at subscription scope or above", addr(n)),
			why:     "A custom role with \"*\" is an Owner equivalent hidden under another name: it escapes reviews of privileged roles and grants everything, including changing access.",
			fix:     "List only the actions the role needs (or use a built-in role) and assign it at the narrowest scope; use the built-in Owner role with PIM for real administrators.",
		})
	}
}
