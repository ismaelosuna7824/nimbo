package advisor

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Azure recommendations: APIM rate limiting, WAF policies on Front Door and
// Application Gateway (and Front Door in front of public apps in
// production), Key Vault and Entra ID instead of literal credentials, blob
// recovery, private endpoints, autoscale, alerts, Application Insights and
// Service Bus dead-lettering.

func azureRecs(d *design) []rec {
	var out []rec
	azureAPIM(d, &out)
	azureFrontDoorWAF(d, &out)
	azureAppGatewayWAF(d, &out)
	azurePublicApps(d, &out)
	azureSecrets(d, &out)
	azureStorageProtection(d, &out)
	azurePrivateData(d, &out)
	azureServiceBusDLQ(d, &out)
	azureAutoscale(d, &out)
	azureAlerts(d, &out)
	azureTracing(d, &out)
	azureSmells(d, &out)
	return out
}

var azureWebApps = []string{"azurerm_linux_web_app", "azurerm_windows_web_app", "azurerm_linux_function_app", "azurerm_windows_function_app"}

// azureRG is the resource_group_name and location expressions of n (or
// placeholders).
func azureRG(n *model.Node) (rg, location string) {
	return cmp.Or(expr(&n.Body, "resource_group_name"), "var.resource_group_name"), cmp.Or(expr(&n.Body, "location"), "var.location")
}

// alnum is name without characters other than letters and digits (WAF
// policy names).
func alnum(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, name)
}

func dashed(name string) string { return strings.ReplaceAll(name, "_", "-") }

func azureAPIM(d *design, out *[]rec) {
	limited := false
	for _, p := range d.ofType("azurerm_api_management_policy", "azurerm_api_management_api_policy", "azurerm_api_management_product_policy", "azurerm_api_management_api_operation_policy") {
		x := expr(&p.Body, "xml_content")
		limited = limited || strings.Contains(x, "<rate-limit") || strings.Contains(x, "<quota")
	}
	var nodes []*model.Node
	var parts []string
	for _, apim := range d.ofType("azurerm_api_management") {
		if t, _ := str(&apim.Body, "virtual_network_type"); t == "Internal" {
			continue
		}
		d.public = true
		if limited || d.referredBy(apim, "azurerm_api_management_policy") {
			continue
		}
		nodes = append(nodes, apim)
		parts = append(parts, fmt.Sprintf(`resource "azurerm_api_management_policy" %q {
  api_management_id = %s.id
  xml_content = <<-XML
    <policies>
      <inbound>
        <rate-limit-by-key calls="100" renewal-period="60" counter-key="@(context.Request.IpAddress)" />
      </inbound>
      <backend>
        <forward-request />
      </backend>
      <outbound />
      <on-error />
    </policies>
  XML
}`, d.unique("azurerm_api_management_policy", apim.Name), addr(apim)))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "azure_apim_rate_limit",
		Pattern: "rate-limiting",
		Title:   "Rate-limit the public API Management gateway per client",
		Why: "Without a rate-limit policy one client can exhaust the gateway's capacity and the backends behind it (OWASP API4:2023). A global rate-limit-by-key policy keyed on the client IP " +
			"(or subscription) answers 429 before the backend sees the flood. The Consumption tier doesn't support rate-limit-by-key: use rate-limit per subscription at product or API scope there.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "azurerm_api_management_policy", Purpose: "global inbound rate-limit-by-key: 100 calls per minute per client IP"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Internal (VNet-only) API Management instances.",
		Rules:   []string{"bp_azure_apim_rate_limit"},
		Docs:    "https://learn.microsoft.com/en-us/azure/api-management/rate-limit-by-key-policy",
	})
}

// frontDoorWAF is a Front Door firewall policy (Microsoft default rule set
// on Premium, a per-IP rate-limit rule) and its security policy.
func frontDoorWAF(d *design, profile, rg, domain string) (string, string) {
	pol := d.unique("azurerm_cdn_frontdoor_firewall_policy", strings.TrimPrefix(profile, "azurerm_cdn_frontdoor_profile.")+"_waf")
	sec := d.unique("azurerm_cdn_frontdoor_security_policy", strings.TrimPrefix(profile, "azurerm_cdn_frontdoor_profile.")+"_waf")
	return "azurerm_cdn_frontdoor_firewall_policy." + pol, fmt.Sprintf(`resource "azurerm_cdn_frontdoor_firewall_policy" %q {
  name = %q
  resource_group_name = %s
  sku_name = %s.sku_name
  enabled = true
  mode = "Prevention"

  custom_rule {
    name = "RateLimitPerClientIp"
    type = "RateLimitRule"
    action = "Block"
    priority = 1
    rate_limit_duration_in_minutes = 1
    rate_limit_threshold = 300
    # Matches every request: all valid requests carry a Host header.
    match_condition {
      match_variable = "RequestHeader"
      selector = "Host"
      operator = "GreaterThan"
      match_values = ["0"]
    }
  }

  # Managed rule sets need the Premium_AzureFrontDoor SKU.
  managed_rule {
    type = "Microsoft_DefaultRuleSet"
    version = "2.1"
    action = "Block"
  }
}

resource "azurerm_cdn_frontdoor_security_policy" %q {
  name = %q
  cdn_frontdoor_profile_id = %s.id

  security_policies {
    firewall {
      cdn_frontdoor_firewall_policy_id = azurerm_cdn_frontdoor_firewall_policy.%s.id
      association {
        patterns_to_match = ["/*"]
        domain {
          cdn_frontdoor_domain_id = %s
        }
      }
    }
  }
}`, pol, alnum(pol), rg, profile, sec, dashed(sec), profile, pol, domain)
}

func azureFrontDoorWAF(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, p := range d.ofType("azurerm_cdn_frontdoor_profile") {
		d.public = true
		if d.referredBy(p, "azurerm_cdn_frontdoor_security_policy") {
			continue
		}
		nodes = append(nodes, p)
		domain := "<frontdoor-endpoint-or-custom-domain>.id"
		if eps := d.referrers(p, "azurerm_cdn_frontdoor_endpoint"); len(eps) > 0 {
			domain = addr(eps[0]) + ".id"
		}
		rg, _ := azureRG(p)
		_, hcl := frontDoorWAF(d, addr(p), rg, domain)
		parts = append(parts, hcl)
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "azure_frontdoor_waf",
		Pattern: "gateway-offloading",
		Title:   "Attach a WAF policy with rate limiting to Azure Front Door",
		Why:     "Front Door is the public entry point: a WAF policy in Prevention mode blocks common exploits (Microsoft default rule set) and a rate-limit rule stops a single client from flooding every origin behind it.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_cdn_frontdoor_firewall_policy", Purpose: "Prevention mode, Microsoft default rule set (Premium) and a per-client-IP rate limit"},
			{Type: "azurerm_cdn_frontdoor_security_policy", Purpose: "associates the policy with the endpoint's domains"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    wafSkip,
		Rules:   []string{"bp_azure_waf_bot_protection", "bp_azure_waf_request_body"},
		Docs:    "https://learn.microsoft.com/en-us/azure/web-application-firewall/afds/waf-front-door-rate-limit",
	})
}

func azureAppGatewayWAF(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, gw := range d.ofType("azurerm_application_gateway") {
		d.public = true
		if set(&gw.Body, "firewall_policy_id") || set(&gw.Body, "waf_configuration") {
			continue
		}
		nodes = append(nodes, gw)
		rg, loc := azureRG(gw)
		pol := d.unique("azurerm_web_application_firewall_policy", label(gw, "waf"))
		parts = append(parts, fmt.Sprintf(`resource "azurerm_web_application_firewall_policy" %q {
  name = %q
  resource_group_name = %s
  location = %s

  policy_settings {
    enabled = true
    mode = "Prevention"
  }

  managed_rules {
    managed_rule_set {
      type = "OWASP"
      version = "3.2"
    }
  }

  custom_rules {
    name = "RateLimitPerClientIp"
    priority = 1
    rule_type = "RateLimitRule"
    action = "Block"
    rate_limit_duration = "OneMin"
    rate_limit_threshold = 300
    group_rate_limit_by = "ClientAddr"
    match_conditions {
      match_variables {
        variable_name = "RemoteAddr"
      }
      operator = "IPMatch"
      # Not 255.255.255.255/32 = every client address.
      negation_condition = true
      match_values = ["255.255.255.255/32"]
    }
  }
}

# Merge into the existing %s (WAF policies need the WAF_v2 SKU):
resource %q %q {
  firewall_policy_id = azurerm_web_application_firewall_policy.%s.id
}`, pol, dashed(pol), rg, loc, addr(gw), gw.Type, gw.Name, pol))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "azure_appgw_waf",
		Pattern: "gateway-offloading",
		Title:   "Attach a WAF policy with rate limiting to the Application Gateway",
		Why:     "An Application Gateway without a WAF policy forwards SQL injection, XSS and floods to the backends. The policy adds the OWASP rule set in Prevention mode and a per-client-IP rate limit.",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_web_application_firewall_policy", Purpose: "OWASP 3.2 managed rules in Prevention mode plus a per-client-IP rate limit"},
			{Type: "azurerm_application_gateway", Purpose: "firewall_policy_id (WAF_v2 SKU)"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Internal-only gateways with no internet listener.",
		Rules:   []string{"bp_azure_appgw_waf"},
		Docs:    "https://learn.microsoft.com/en-us/azure/web-application-firewall/ag/rate-limiting-overview",
	})
}

// azurePublicApps recommends Front Door Premium with a WAF policy in front
// of public web, function and container apps that nothing fronts
// (production).
func azurePublicApps(d *design, out *[]rec) {
	if d.has("azurerm_cdn_frontdoor_profile", "azurerm_application_gateway", "azurerm_api_management") {
		return
	}
	var nodes []*model.Node
	var parts []string
	for _, app := range d.ofType(append([]string{"azurerm_container_app"}, azureWebApps...)...) {
		host := addr(app) + ".default_hostname"
		if app.Type == "azurerm_container_app" {
			if !isTrue(path(&app.Body, "ingress"), "external_enabled") {
				continue
			}
			host = addr(app) + ".ingress[0].fqdn"
		} else if isFalse(&app.Body, "public_network_access_enabled") {
			continue
		}
		d.public = true
		nodes = append(nodes, app)
		if len(nodes) > 1 {
			continue // one profile per design; more origins follow the same pattern
		}
		rg, _ := azureRG(app)
		profile := d.unique("azurerm_cdn_frontdoor_profile", "edge")
		ep := d.unique("azurerm_cdn_frontdoor_endpoint", "edge")
		og := d.unique("azurerm_cdn_frontdoor_origin_group", app.Name)
		origin := d.unique("azurerm_cdn_frontdoor_origin", app.Name)
		route := d.unique("azurerm_cdn_frontdoor_route", app.Name)
		parts = append(parts, fmt.Sprintf(`resource "azurerm_cdn_frontdoor_profile" %q {
  name = %q
  resource_group_name = %s
  sku_name = "Premium_AzureFrontDoor"
}

resource "azurerm_cdn_frontdoor_endpoint" %q {
  name = %q
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.%s.id
}

resource "azurerm_cdn_frontdoor_origin_group" %q {
  name = %q
  cdn_frontdoor_profile_id = azurerm_cdn_frontdoor_profile.%s.id

  load_balancing {}

  health_probe {
    protocol = "Https"
    interval_in_seconds = 60
    path = "/"
    request_type = "HEAD"
  }
}

resource "azurerm_cdn_frontdoor_origin" %q {
  name = %q
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.%s.id
  host_name = %s
  origin_host_header = %s
  certificate_name_check_enabled = true
}

resource "azurerm_cdn_frontdoor_route" %q {
  name = %q
  cdn_frontdoor_endpoint_id = azurerm_cdn_frontdoor_endpoint.%s.id
  cdn_frontdoor_origin_group_id = azurerm_cdn_frontdoor_origin_group.%s.id
  cdn_frontdoor_origin_ids = [azurerm_cdn_frontdoor_origin.%s.id]
  patterns_to_match = ["/*"]
  supported_protocols = ["Http", "Https"]
  https_redirect_enabled = true
  forwarding_protocol = "HttpsOnly"
}`, profile, dashed(profile), rg, ep, dashed(ep), profile, og, dashed(og), profile, origin, dashed(origin), og, host, host,
			route, dashed(route), ep, og, origin))
		_, waf := frontDoorWAF(d, "azurerm_cdn_frontdoor_profile."+profile, rg, "azurerm_cdn_frontdoor_endpoint."+ep+".id")
		parts = append(parts, waf)
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > 1 {
		parts = append(parts, fmt.Sprintf("# Add an origin group, origin and route the same way for: %s", strings.Join(addrs(nodes[1:]), ", ")))
	}
	d.add(out, prioEdge, Recommendation{
		ID:      "azure_public_app_frontdoor",
		Pattern: "gateway-offloading",
		Title:   "Serve the public apps through Azure Front Door with a WAF policy",
		Why: "The apps answer the internet directly, so nothing filters exploits or rate-limits clients. Front Door Premium with a WAF policy does both at the edge and caches static content; " +
			"then restrict the apps to Front Door (access restrictions on the AzureFrontDoor.Backend service tag with your profile's X-Azure-FDID header, or Private Link origins).",
		Pillars: pillarsEdge,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_cdn_frontdoor_profile", Purpose: "Premium_AzureFrontDoor profile (WAF managed rules need Premium)"},
			{Type: "azurerm_cdn_frontdoor_endpoint", Purpose: "public endpoint"},
			{Type: "azurerm_cdn_frontdoor_origin_group", Purpose: "health-probed group of the app's origins"},
			{Type: "azurerm_cdn_frontdoor_origin", Purpose: "the app's hostname"},
			{Type: "azurerm_cdn_frontdoor_route", Purpose: "HTTPS-only route to the app"},
			{Type: "azurerm_cdn_frontdoor_firewall_policy", Purpose: "Microsoft default rule set plus a per-client-IP rate limit"},
			{Type: "azurerm_cdn_frontdoor_security_policy", Purpose: "attaches the WAF policy to the endpoint"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development apps and apps that only serve authenticated internal users.",
		Docs:    "https://learn.microsoft.com/en-us/azure/frontdoor/front-door-overview",
	})
}

// azureSecrets recommends Entra ID authentication for databases with a
// password in the configuration and Key Vault references for app settings
// holding literal secrets.
func azureSecrets(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, db := range d.ofType("azurerm_mssql_server", "azurerm_postgresql_flexible_server") {
		pw := "administrator_login_password"
		patch := "  azuread_administrator {\n    login_username = \"<entra-admin-group>\"\n    object_id = \"<entra-admin-group-object-id>\"\n    azuread_authentication_only = true\n  }"
		if db.Type == "azurerm_postgresql_flexible_server" {
			pw = "administrator_password"
			patch = "  authentication {\n    active_directory_auth_enabled = true\n    password_auth_enabled = false\n  }"
		}
		if _, literal := str(&db.Body, pw); !literal {
			continue
		}
		nodes = append(nodes, db)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s and remove %s:\nresource %q %q {\n%s\n}", addr(db), pw, db.Type, db.Name, patch))
		add[db.Type] = Addition{Type: db.Type, Purpose: "Microsoft Entra ID authentication only: no database password to store or leak"}
	}
	vault := "var.key_vault_id"
	if kvs := d.ofType("azurerm_key_vault"); len(kvs) > 0 {
		vault = addr(kvs[0]) + ".id"
	}
	for _, app := range d.ofType(azureWebApps...) {
		settings, _ := literal(&app.Body, "app_settings").(map[string]any)
		for _, k := range sortedKeys(settings) {
			v, ok := settings[k].(string)
			if !ok || v == "" || !secretKey.MatchString(k) || strings.HasPrefix(v, "@Microsoft.KeyVault") {
				continue
			}
			nodes = append(nodes, app)
			sec := d.unique("azurerm_key_vault_secret", label(app, strings.ToLower(k)))
			parts = append(parts, fmt.Sprintf(`# Set the value outside the configuration (portal, CLI or a pipeline secret).
resource "azurerm_key_vault_secret" %q {
  name = %q
  value = var.%s
  key_vault_id = %s
}

# In %s: give the app a managed identity with the "Key Vault Secrets User" role, then
#   app_settings = { %s = "@Microsoft.KeyVault(SecretUri=${azurerm_key_vault_secret.%s.versionless_id})" }`,
				sec, dashed(sec), sec, vault, addr(app), k, sec))
			add["azurerm_key_vault_secret"] = Addition{Type: "azurerm_key_vault_secret", Purpose: "holds each secret; the app setting becomes a Key Vault reference resolved with the app's managed identity"}
			break
		}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioSecrets, Recommendation{
		ID:      "azure_key_vault_secrets",
		Pattern: "external-configuration-store",
		Title:   "Replace literal credentials with Microsoft Entra ID and Key Vault references",
		Why:     "Credentials written in Terraform end up in state, plan output and the app's configuration. Entra ID authentication removes database passwords entirely; Key Vault references keep app secrets in the vault, read with the app's managed identity and audited.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Non-secret settings (URLs, feature flags).",
		Rules:   []string{"bp_sensitive_not_marked"},
		Docs:    "https://learn.microsoft.com/en-us/azure/app-service/app-service-key-vault-references",
	})
}

func azureStorageProtection(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, sa := range d.ofType("azurerm_storage_account") {
		bp := path(&sa.Body, "blob_properties")
		if isTrue(bp, "versioning_enabled") && set(bp, "delete_retention_policy") {
			continue
		}
		nodes = append(nodes, sa)
		parts = append(parts, fmt.Sprintf(`# Merge into the existing %s:
resource %q %q {
  blob_properties {
    versioning_enabled = true
    delete_retention_policy {
      days = 7
    }
    container_delete_retention_policy {
      days = 7
    }
  }
}`, addr(sa), sa.Type, sa.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioData, Recommendation{
		ID:      "azure_storage_recovery",
		Title:   "Turn on blob versioning and soft delete",
		Why:     "An overwritten or deleted blob or container is gone for good by default. Versioning and soft delete keep it recoverable for the retention period.",
		Pillars: pillarsData,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "azurerm_storage_account", Purpose: "blob_properties: versioning plus 7-day blob and container soft delete"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Accounts that only hold reproducible data (build artifacts, caches).",
		Rules:   []string{"bp_azure_blob_versioning", "bp_azure_blob_soft_delete", "bp_azure_container_soft_delete"},
		Docs:    "https://learn.microsoft.com/en-us/azure/storage/blobs/data-protection-overview",
	})
}

// azurePrivateEndpointTargets maps data services to their private
// endpoint subresource.
var azurePrivateEndpointTargets = map[string]string{
	"azurerm_storage_account":            "blob",
	"azurerm_mssql_server":               "sqlServer",
	"azurerm_postgresql_flexible_server": "postgresqlServer",
	"azurerm_cosmosdb_account":           "Sql",
	"azurerm_key_vault":                  "vault",
}

func azurePrivateData(d *design, out *[]rec) {
	subnet := "var.private_endpoint_subnet_id"
	if subnets := d.ofType("azurerm_subnet"); len(subnets) > 0 {
		subnet = addr(subnets[0]) + ".id"
	}
	var nodes []*model.Node
	var parts []string
	for _, n := range d.ofType(sortedKeys(azurePrivateEndpointTargets)...) {
		if isFalse(&n.Body, "public_network_access_enabled") || d.referredBy(n, "azurerm_private_endpoint") {
			continue
		}
		nodes = append(nodes, n)
		rg, loc := azureRG(n)
		pe := d.unique("azurerm_private_endpoint", n.Name)
		parts = append(parts, fmt.Sprintf(`resource "azurerm_private_endpoint" %q {
  name = %q
  resource_group_name = %s
  location = %s
  subnet_id = %s

  private_service_connection {
    name = %q
    private_connection_resource_id = %s.id
    subresource_names = [%q]
    is_manual_connection = false
  }
}

# Merge into the existing %s once clients use the private endpoint:
resource %q %q {
  public_network_access_enabled = false
}`, pe, dashed(pe), rg, loc, subnet, dashed(pe), addr(n), azurePrivateEndpointTargets[n.Type], addr(n), n.Type, n.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioPrivate, Recommendation{
		ID:    "azure_private_endpoints",
		Title: "Reach the data services through private endpoints and close public access",
		Why: "With public network access on, the service's endpoint is reachable from the internet and only its credentials and firewall rules protect it. A private endpoint gives it an address inside your VNet; " +
			"add a private DNS zone (privatelink.*) so names resolve to it.",
		Pillars: pillarsSecret,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_private_endpoint", Purpose: "private address in the VNet for each data service"},
			{Type: "azurerm_private_dns_zone", Purpose: "privatelink zone so the service name resolves to the private address"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development subscriptions without a VNet: keep public access with firewall rules there.",
		Rules:   []string{"bp_azure_public_network_access", "bp_azure_private_endpoint"},
		Docs:    "https://learn.microsoft.com/en-us/azure/private-link/private-endpoint-overview",
	})
}

func azureServiceBusDLQ(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	for _, q := range d.ofType("azurerm_servicebus_queue", "azurerm_servicebus_subscription") {
		if isTrue(&q.Body, "dead_lettering_on_message_expiration") || set(&q.Body, "forward_dead_lettered_messages_to") {
			continue
		}
		nodes = append(nodes, q)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  dead_lettering_on_message_expiration = true\n  max_delivery_count = 10\n}", addr(q), q.Type, q.Name))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioAsync, Recommendation{
		ID:      "azure_servicebus_dead_letter",
		Pattern: "competing-consumers",
		Title:   "Dead-letter expired Service Bus messages",
		Why:     "Messages that exceed max_delivery_count go to the dead-letter subqueue, but expired ones are silently dropped unless dead-lettering on expiration is on. Monitor the dead-letter count and replay from it.",
		Pillars: pillarsAsync,
		Nodes:   addrs(nodes),
		Add:     []Addition{{Type: "azurerm_servicebus_queue", Purpose: "dead_lettering_on_message_expiration = true and a bounded max_delivery_count"}},
		Snippet: joinLines(parts...),
		Scope:   ScopeRecommended,
		Skip:    "Messages that are safe to drop when they expire (telemetry).",
		Docs:    "https://learn.microsoft.com/en-us/azure/service-bus-messaging/service-bus-dead-letter-queues",
	})
}

func azureAutoscale(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	add := map[string]Addition{}
	for _, n := range d.ofType("azurerm_service_plan", "azurerm_linux_virtual_machine_scale_set", "azurerm_windows_virtual_machine_scale_set") {
		if d.referredBy(n, "azurerm_monitor_autoscale_setting") {
			continue
		}
		if n.Type == "azurerm_service_plan" {
			if sku, _ := str(&n.Body, "sku_name"); strings.HasPrefix(sku, "F") || strings.HasPrefix(sku, "D") || strings.HasPrefix(sku, "Y") || strings.HasPrefix(sku, "FC") {
				continue // free, shared and consumption plans scale by themselves or not at all
			}
		}
		nodes = append(nodes, n)
		metric, ns := "Percentage CPU", "Microsoft.Compute/virtualMachineScaleSets"
		if n.Type == "azurerm_service_plan" {
			metric, ns = "CpuPercentage", "Microsoft.Web/serverfarms"
		}
		rg, loc := azureRG(n)
		rule := func(op, threshold, dir string) string {
			return fmt.Sprintf(`    rule {
      metric_trigger {
        metric_name = %q
        metric_namespace = %q
        metric_resource_id = %s.id
        time_grain = "PT1M"
        statistic = "Average"
        time_window = "PT5M"
        time_aggregation = "Average"
        operator = %q
        threshold = %s
      }
      scale_action {
        direction = %q
        type = "ChangeCount"
        value = "1"
        cooldown = "PT5M"
      }
    }`, metric, ns, addr(n), op, threshold, dir)
		}
		as := d.unique("azurerm_monitor_autoscale_setting", n.Name)
		parts = append(parts, fmt.Sprintf(`resource "azurerm_monitor_autoscale_setting" %q {
  name = %q
  resource_group_name = %s
  location = %s
  target_resource_id = %s.id

  profile {
    name = "cpu"
    capacity {
      default = 2
      minimum = 2
      maximum = 10
    }
%s
%s
  }
}`, as, dashed(as), rg, loc, addr(n), rule("GreaterThan", "70", "Increase"), rule("LessThan", "30", "Decrease")))
		add["azurerm_monitor_autoscale_setting"] = Addition{Type: "azurerm_monitor_autoscale_setting", Purpose: "2 to 10 instances: scale out above 70% CPU, in below 30%"}
	}
	for _, app := range d.ofType("azurerm_container_app") {
		if set(path(&app.Body, "template"), "max_replicas") {
			continue
		}
		nodes = append(nodes, app)
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s:\nresource %q %q {\n  template {\n    min_replicas = 1\n    max_replicas = 10\n  }\n}", addr(app), app.Type, app.Name))
		add["azurerm_container_app"] = Addition{Type: "azurerm_container_app", Purpose: "template min_replicas / max_replicas bounds (HTTP scaling is on by default)"}
	}
	if len(nodes) == 0 {
		return
	}
	adds := []Addition{}
	for _, k := range sortedKeys(add) {
		adds = append(adds, add[k])
	}
	d.add(out, prioScaling, Recommendation{
		ID:      "azure_autoscale",
		Title:   "Scale the compute tiers automatically, within bounds",
		Why:     "A fixed instance count is too small for a peak and paid for in full while idle. Autoscale rules add instances before saturation; the minimum keeps a spare across failures and the maximum caps cost.",
		Pillars: pillarsScaling,
		Nodes:   addrs(nodes),
		Add:     adds,
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development plans and fixed-size batch workloads.",
		Rules:   []string{"bp_azure_autoscale"},
		Docs:    "https://learn.microsoft.com/en-us/azure/azure-monitor/autoscale/autoscale-best-practices",
	})
}

func azureAlerts(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	group := ""
	for _, app := range d.ofType(azureWebApps...) {
		if d.referredBy(app, "azurerm_monitor_metric_alert") {
			continue
		}
		nodes = append(nodes, app)
		if len(nodes) > 3 {
			continue
		}
		rg, _ := azureRG(app)
		if group == "" {
			group = d.unique("azurerm_monitor_action_group", "alerts")
			parts = append(parts, fmt.Sprintf("resource \"azurerm_monitor_action_group\" %q {\n  name = %q\n  resource_group_name = %s\n  short_name = \"alerts\"\n}", group, dashed(group), rg))
		}
		alert := func(suffix, metric, aggregation, threshold string) string {
			n := d.unique("azurerm_monitor_metric_alert", label(app, suffix))
			return fmt.Sprintf(`resource "azurerm_monitor_metric_alert" %q {
  name = %q
  resource_group_name = %s
  scopes = [%s.id]
  frequency = "PT1M"
  window_size = "PT5M"

  criteria {
    metric_namespace = "Microsoft.Web/sites"
    metric_name = %q
    aggregation = %q
    operator = "GreaterThan"
    threshold = %s
  }

  action {
    action_group_id = azurerm_monitor_action_group.%s.id
  }
}`, n, dashed(n), rg, addr(app), metric, aggregation, threshold, group)
		}
		parts = append(parts, alert("5xx", "Http5xx", "Total", "5"), alert("latency", "HttpResponseTime", "Average", "1"))
	}
	if len(nodes) == 0 {
		return
	}
	if len(nodes) > 3 {
		parts = append(parts, "# Same pattern for: "+strings.Join(addrs(nodes[3:]), ", "))
	}
	d.add(out, prioAlarms, Recommendation{
		ID:    "azure_alerts",
		Title: "Alert on 5xx errors and response time of the apps",
		Why: "Users notice errors and slow responses first; metric alerts on server errors and response time page you before they do. " +
			"Platform metrics give averages; use Application Insights request duration for p95.",
		Pillars: pillarsAlarms,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_monitor_metric_alert", Purpose: "Http5xx over 5 in 5 minutes and average response time above 1 s"},
			{Type: "azurerm_monitor_action_group", Purpose: "who is notified (add e-mail, SMS or webhook receivers)"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Development apps nobody is on call for.",
		Docs:    "https://learn.microsoft.com/en-us/azure/azure-monitor/alerts/alerts-metric-overview",
	})
}

func azureTracing(d *design, out *[]rec) {
	var nodes []*model.Node
	var parts []string
	ai := ""
	for _, app := range d.ofType(azureWebApps...) {
		if strings.Contains(expr(&app.Body, "app_settings"), "APPLICATIONINSIGHTS_CONNECTION_STRING") || len(d.referenced(app, "azurerm_application_insights")) > 0 {
			continue
		}
		nodes = append(nodes, app)
		if ai == "" {
			rg, loc := azureRG(app)
			ai = d.unique("azurerm_application_insights", "app")
			ws := "var.log_analytics_workspace_id"
			if w := d.ofType("azurerm_log_analytics_workspace"); len(w) > 0 {
				ws = addr(w[0]) + ".id"
			}
			parts = append(parts, fmt.Sprintf("resource \"azurerm_application_insights\" %q {\n  name = %q\n  resource_group_name = %s\n  location = %s\n  application_type = \"web\"\n  workspace_id = %s\n}", ai, dashed(ai), rg, loc, ws))
		}
		parts = append(parts, fmt.Sprintf("# Merge into the existing %s (add the key to its app_settings):\nresource %q %q {\n  app_settings = {\n    APPLICATIONINSIGHTS_CONNECTION_STRING = azurerm_application_insights.%s.connection_string\n  }\n}", addr(app), app.Type, app.Name, ai))
	}
	if len(nodes) == 0 {
		return
	}
	d.add(out, prioTracing, Recommendation{
		ID:      "azure_app_insights",
		Title:   "Trace requests with Application Insights",
		Why:     "Application Insights records every request with its dependencies (database, HTTP calls), so a slow p95 or a failure points at the hop that caused it; metrics alone can't.",
		Pillars: pillarsTracing,
		Nodes:   addrs(nodes),
		Add: []Addition{
			{Type: "azurerm_application_insights", Purpose: "workspace-based Application Insights resource"},
			{Type: "azurerm_linux_web_app", Purpose: "APPLICATIONINSIGHTS_CONNECTION_STRING app setting"},
		},
		Snippet: joinLines(parts...),
		Scope:   ScopeProduction,
		Skip:    "Low-traffic internal tools: logs and metrics are enough.",
		Docs:    "https://learn.microsoft.com/en-us/azure/azure-monitor/app/app-insights-overview",
	})
}
