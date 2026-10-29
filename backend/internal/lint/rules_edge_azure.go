package lint

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Azure edge, cluster and messaging rules (see rules_edge_aws.go).
func init() {
	rules = append(rules,
		edgeRule(info("bp_azure_apim_rate_limit", "Azure: API Management rate-limits callers", CategorySecurity,
			"API Management services have a policy (service, product, API or operation) with rate-limit, rate-limit-by-key, quota or quota-by-key."),
			"https://learn.microsoft.com/azure/api-management/api-management-sample-flexible-throttling", false, checkAzAPIMRateLimit,
			model.PillarSecurity, model.PillarReliability, model.PillarCost),
		edgeRule(warning("bp_azure_messaging_tls", "Azure: Service Bus and Event Hubs accept TLS 1.2 or later only", CategorySecurity,
			"Service Bus and Event Hubs namespaces don't lower minimum_tls_version to 1.0 or 1.1."),
			"https://learn.microsoft.com/azure/service-bus-messaging/transport-layer-security-enforce-minimum-version", false, checkAzMessagingTLS,
			model.PillarSecurity),
		edgeRule(warning("bp_azure_aks_rbac", "Azure: AKS clusters use Kubernetes RBAC", CategorySecurity,
			"AKS clusters don't set role_based_access_control_enabled = false."),
			"https://learn.microsoft.com/azure/aks/concepts-identity", false, checkAzAKSRBAC,
			model.PillarSecurity),
		edgeRule(warning("bp_azure_aks_api_ranges", "Azure: AKS API servers are not open to the internet", CategorySecurity,
			"AKS clusters are private (private_cluster_enabled = true) or restrict their API server with api_server_access_profile.authorized_ip_ranges."),
			"https://learn.microsoft.com/azure/aks/api-server-authorized-ip-ranges", true, checkAzAKSAPIRanges,
			model.PillarSecurity),
		edgeRule(info("bp_azure_aks_azure_policy", "Azure: AKS clusters enforce Azure Policy", CategorySecurity,
			"AKS clusters set azure_policy_enabled = true, so pod security and other guardrails are enforced at admission."),
			"https://learn.microsoft.com/azure/governance/policy/concepts/policy-for-kubernetes", true, checkAzAKSAzurePolicy,
			model.PillarSecurity, model.PillarOperations),
		edgeRule(info("bp_azure_kv_default_deny", "Azure: Key Vault firewalls deny by default", CategorySecurity,
			"Key Vaults with a public endpoint have network_acls with default_action = \"Deny\" and the allowed IP ranges or subnets."),
			"https://learn.microsoft.com/azure/key-vault/general/network-security", true, checkAzKVDefaultDeny,
			model.PillarSecurity),
		edgeRule(info("bp_azure_law_retention", "Azure: Log Analytics keeps logs 90 days or more", CategoryOperations,
			"Log Analytics workspaces set retention_in_days to 90 or more (the default is 30), so incidents can still be investigated weeks later."),
			"https://learn.microsoft.com/azure/azure-monitor/logs/data-retention-configure", true, checkAzLAWRetention,
			model.PillarOperations, model.PillarSecurity),
	)
}

// azAPIMPolicyTypes are the API Management policy resources, with the
// attributes naming their service.
var azAPIMPolicyTypes = []string{
	"azurerm_api_management_policy", "azurerm_api_management_product_policy",
	"azurerm_api_management_api_policy", "azurerm_api_management_api_operation_policy",
}

func checkAzAPIMRateLimit(p *pass) {
	for _, n := range p.ofType("azurerm_api_management") {
		policies, unknown := p.referrers(n, nameOf(n, "name"), []string{"api_management_name", "api_management_id"}, azAPIMPolicyTypes...)
		if unknown {
			continue
		}
		ok := false
		for _, pol := range policies {
			if getAttr(&pol.Body, "xml_link") != nil {
				ok = true
				break
			}
			a := getAttr(&pol.Body, "xml_content")
			if a == nil {
				continue
			}
			src := source(a.Value)
			// file() or templatefile() content can't be read: counted as intent.
			if strings.Contains(src, "<rate-limit") || strings.Contains(src, "<quota") || strings.Contains(src, "file(") {
				ok = true
				break
			}
		}
		if ok {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s has no rate-limit or quota policy", addr(n)),
			why:     "Without a rate limit one subscription key or client IP can flood the backends, scrape the API or brute-force it, and every call costs (OWASP API4:2023).",
			fix:     "Add <rate-limit calls=\"100\" renewal-period=\"60\" /> (per subscription; any tier) or <rate-limit-by-key calls=\"100\" renewal-period=\"60\" counter-key=\"@(context.Request.IpAddress)\" /> (not in Consumption) to the inbound section of an azurerm_api_management_api_policy or product policy.",
		})
	}
}

func checkAzMessagingTLS(p *pass) {
	for _, n := range p.ofType("azurerm_servicebus_namespace", "azurerm_eventhub_namespace") {
		v, _ := attrString(&n.Body, "minimum_tls_version")
		if v != "1.0" && v != "1.1" {
			continue
		}
		p.report(finding{
			node: n, attribute: "minimum_tls_version",
			message: fmt.Sprintf("%s accepts TLS %s", addr(n), v),
			why:     "TLS 1.0 and 1.1 have known weaknesses; clients that negotiate them expose messages and connection strings to downgrade attacks, and Azure is retiring them.",
			fix:     "Set minimum_tls_version = \"1.2\" (the default) and update old client SDKs.",
		})
	}
}

func checkAzAKSRBAC(p *pass) {
	for _, n := range p.ofType("azurerm_kubernetes_cluster") {
		if !hasLiteralFalse(&n.Body, "role_based_access_control_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "role_based_access_control_enabled",
			message: fmt.Sprintf("%s turns Kubernetes RBAC off", addr(n)),
			why:     "Without RBAC every authenticated identity can do anything in the cluster: read every secret, run privileged pods, delete workloads.",
			fix:     "Remove role_based_access_control_enabled = false (it can't be changed after creation without recreating the cluster) and add azure_active_directory_role_based_access_control { azure_rbac_enabled = true }.",
		})
	}
}

func checkAzAKSAPIRanges(p *pass) {
	for _, n := range p.ofType("azurerm_kubernetes_cluster") {
		if attrNotFalse(&n.Body, "private_cluster_enabled") || azHasDynamic(&n.Body, "api_server_access_profile") {
			continue
		}
		if prof := azNested(&n.Body, "api_server_access_profile"); prof != nil {
			if nonEmpty(prof, "authorized_ip_ranges") || attrNotFalse(prof, "virtual_network_integration_enabled") {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "api_server_access_profile",
			message: fmt.Sprintf("%s exposes its API server to the whole internet", addr(n)),
			why:     "A public Kubernetes API server is scanned constantly; a leaked kubeconfig or a control-plane vulnerability is then exploitable from anywhere.",
			fix:     "Set private_cluster_enabled = true, or add api_server_access_profile { authorized_ip_ranges = [<office/VPN/CI CIDRs>] }.",
		})
	}
}

func checkAzAKSAzurePolicy(p *pass) {
	for _, n := range p.ofType("azurerm_kubernetes_cluster") {
		if attrNotFalse(&n.Body, "azure_policy_enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "azure_policy_enabled",
			message: fmt.Sprintf("%s doesn't enforce Azure Policy", addr(n)),
			why:     "Without admission policies nothing stops a privileged pod, a hostPath mount or an image from an untrusted registry from being deployed.",
			fix:     "Set azure_policy_enabled = true and assign the Kubernetes pod security baseline initiative to the cluster's scope.",
		})
	}
}

func checkAzKVDefaultDeny(p *pass) {
	for _, n := range p.ofType("azurerm_key_vault") {
		if hasLiteralFalse(&n.Body, "public_network_access_enabled") || azHasDynamic(&n.Body, "network_acls") {
			continue
		}
		if acl := azNested(&n.Body, "network_acls"); acl != nil {
			if v, ok := attrString(acl, "default_action"); !ok || v == "Deny" {
				continue
			}
		}
		p.report(finding{
			node: n, attribute: "network_acls",
			message: fmt.Sprintf("%s accepts requests from any network", addr(n)),
			why:     "With the firewall open, a leaked token or managed identity credential reads the vault's secrets from anywhere on the internet.",
			fix:     "Add network_acls { default_action = \"Deny\", bypass = \"AzureServices\", ip_rules = [...], virtual_network_subnet_ids = [...] }, or turn public access off and use a private endpoint.",
		})
	}
}

func checkAzLAWRetention(p *pass) {
	for _, n := range p.ofType("azurerm_log_analytics_workspace") {
		days, ok := attrNumber(&n.Body, "retention_in_days")
		if getAttr(&n.Body, "retention_in_days") != nil && (!ok || days >= 90) {
			continue
		}
		msg := fmt.Sprintf("%s keeps logs %d days", addr(n), int(days))
		if !ok {
			msg = fmt.Sprintf("%s keeps logs 30 days (the default)", addr(n))
		}
		p.report(finding{
			node: n, attribute: "retention_in_days",
			message: msg,
			why:     "Breaches are often found weeks after they started; with a 30-day retention the sign-in, audit and diagnostic logs that show how are already gone.",
			fix:     "Set retention_in_days = 90 or more (or archive to a storage account with a lifecycle policy), per environment.",
		})
	}
}
