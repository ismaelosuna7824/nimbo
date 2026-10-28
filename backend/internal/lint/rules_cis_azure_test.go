package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// azStorage is a storage account with extra (attributes and blocks).
func azStorage(extra string) string {
	return fmt.Sprintf(`resource "azurerm_storage_account" "s" {
  name                     = "st1"
  resource_group_name      = "rg"
  location                 = "eastus"
  account_tier             = "Standard"
  account_replication_type = "LRS"
%s
}
`, extra)
}

func azVault(extra string) string {
	return fmt.Sprintf(`resource "azurerm_key_vault" "kv" {
  name                = "kv1"
  resource_group_name = "rg"
  location            = "eastus"
  tenant_id           = "00000000-0000-0000-0000-000000000000"
  sku_name            = "standard"
%s
}
`, extra)
}

func azNSGRule(protocol, source, port string) string {
	return fmt.Sprintf(`resource "azurerm_network_security_rule" "r" {
  name                        = "r"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = %q
  source_port_range           = "*"
  destination_port_range      = %q
  source_address_prefix       = %q
  destination_address_prefix  = "*"
  resource_group_name         = "rg"
  network_security_group_name = "nsg"
}
`, protocol, port, source)
}

func azAppGw(extra string) string {
	return fmt.Sprintf(`resource "azurerm_application_gateway" "gw" {
  name                = "gw"
  resource_group_name = "rg"
  location            = "eastus"
%s
}
`, extra)
}

const azShare = `
resource "azurerm_storage_share" "sh" {
  name               = "share"
  storage_account_id = azurerm_storage_account.s.id
  quota              = 50
}
`

const azNFSShare = `
resource "azurerm_storage_share" "sh" {
  name               = "share"
  storage_account_id = azurerm_storage_account.s.id
  quota              = 100
  enabled_protocol   = "NFS"
}
`

const azDatabricksVNet = `
  custom_parameters {
    virtual_network_id  = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v"
    public_subnet_name  = "pub"
    private_subnet_name = "priv"
    no_public_ip        = %s
  }
`

func azDatabricks(extra string) string {
	return fmt.Sprintf(`resource "azurerm_databricks_workspace" "db" {
  name                = "db"
  resource_group_name = "rg"
  location            = "eastus"
  sku                 = "premium"
%s
}
`, extra)
}

func azRoleDef(actions, scopes string) string {
	return fmt.Sprintf(`data "azurerm_subscription" "primary" {}

resource "azurerm_role_definition" "r" {
  name  = "custom"
  scope = data.azurerm_subscription.primary.id
  permissions {
    actions = %s
  }
  assignable_scopes = %s
}
`, actions, scopes)
}

// The Azure CIS rule cases run with the other rule cases (TestRules).
func init() {
	privateEndpoint := `
resource "azurerm_private_endpoint" "pe" {
  name                = "pe"
  resource_group_name = "rg"
  location            = "eastus"
  subnet_id           = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v/subnets/s"
  private_service_connection {
    name                           = "c"
    is_manual_connection           = false
    private_connection_resource_id = azurerm_storage_account.s.id
    subresource_names              = ["blob"]
  }
}
`
	diag := func(target, log string) string {
		return fmt.Sprintf(`
resource "azurerm_monitor_diagnostic_setting" "d" {
  name                       = "d"
  target_resource_id         = %s
  log_analytics_workspace_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.OperationalInsights/workspaces/w"
  enabled_log {
    %s
  }
}
`, target, log)
	}
	subnet := func(name string) string {
		return fmt.Sprintf(`resource "azurerm_subnet" "sn" {
  name                 = %q
  resource_group_name  = "rg"
  virtual_network_name = "v"
  address_prefixes     = ["10.0.1.0/24"]
}
`, name)
	}
	assoc := `
resource "azurerm_subnet_network_security_group_association" "a" {
  subnet_id                 = azurerm_subnet.sn.id
  network_security_group_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/n"
}
`
	flowLog := func(enabled, days string) string {
		return fmt.Sprintf(`resource "azurerm_network_watcher_flow_log" "f" {
  name                 = "f"
  network_watcher_name = "nw"
  resource_group_name  = "rg"
  target_resource_id   = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/virtualNetworks/v"
  storage_account_id   = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/s"
  enabled              = true
  retention_policy {
    enabled = %s
    days    = %s
  }
}
`, enabled, days)
	}
	wafPolicy := func(settings, ruleSets string) string {
		return fmt.Sprintf(`resource "azurerm_web_application_firewall_policy" "w" {
  name                = "w"
  resource_group_name = "rg"
  location            = "eastus"
  policy_settings {
    %s
  }
  managed_rules {
    managed_rule_set {
      type    = "OWASP"
      version = "3.2"
    }
    %s
  }
}
`, settings, ruleSets)
	}
	vpn := func(config string) string {
		return fmt.Sprintf(`resource "azurerm_virtual_network_gateway" "g" {
  name                = "g"
  resource_group_name = "rg"
  location            = "eastus"
  type                = "Vpn"
  sku                 = "VpnGw1"
  vpn_client_configuration {
    address_space = ["172.16.0.0/24"]
    %s
  }
}
`, config)
	}

	cases := map[string][]structureCase{
		"bp_azure_storage_secure_transfer": {
			{name: "https disabled", src: azStorage(`  https_traffic_only_enabled = false`), want: 1},
			{name: "default", src: azStorage(""), want: 0},
			{name: "https enabled", src: azStorage(`  https_traffic_only_enabled = true`), want: 0},
		},
		"bp_azure_storage_min_tls": {
			{name: "TLS 1.0", src: azStorage(`  min_tls_version = "TLS1_0"`), want: 1},
			{name: "TLS 1.1", src: azStorage(`  min_tls_version = "TLS1_1"`), want: 1},
			{name: "TLS 1.2", src: azStorage(`  min_tls_version = "TLS1_2"`), want: 0},
			{name: "default", src: azStorage(""), want: 0},
		},
		"bp_azure_storage_cross_tenant": {
			{name: "enabled", src: azStorage(`  cross_tenant_replication_enabled = true`), want: 1},
			{name: "default", src: azStorage(""), want: 0},
		},
		"bp_azure_storage_trusted_services": {
			{name: "inline bypass without AzureServices", src: azStorage(`  network_rules {
    default_action = "Deny"
    bypass         = ["Logging"]
  }`), want: 1},
			{name: "network rules resource with None", src: azStorage("") + `
resource "azurerm_storage_account_network_rules" "n" {
  storage_account_id = azurerm_storage_account.s.id
  default_action     = "Deny"
  bypass             = ["None"]
}
`, want: 1},
			{name: "AzureServices kept", src: azStorage(`  network_rules {
    default_action = "Deny"
    bypass         = ["AzureServices", "Logging"]
  }`), want: 0},
			{name: "default bypass", src: azStorage(`  network_rules {
    default_action = "Deny"
  }`), want: 0},
		},
		"bp_azure_storage_anonymous_access": {
			{name: "provider default", src: azStorage(""), want: 1},
			{name: "public website keeps it on", src: azStorage(`  allow_nested_items_to_be_public = true
  static_website {
    index_document = "index.html"
  }`), want: 1},
			{name: "off", src: azStorage(`  allow_nested_items_to_be_public = false`), want: 0},
			{name: "on without intent is bp_public_bucket's", src: azStorage(`  allow_nested_items_to_be_public = true`), want: 0},
		},
		"bp_azure_storage_shared_key": {
			{name: "default", src: azStorage(""), want: 1},
			{name: "off", src: azStorage(`  shared_access_key_enabled = false`), want: 0},
			{name: "variable", src: azStorage(`  shared_access_key_enabled = var.keys`), want: 0},
		},
		"bp_azure_storage_default_deny": {
			{name: "no network rules", src: azStorage(""), want: 1},
			{name: "allow", src: azStorage(`  network_rules {
    default_action = "Allow"
  }`), want: 1},
			{name: "deny inline", src: azStorage(`  network_rules {
    default_action = "Deny"
  }`), want: 0},
			{name: "deny resource", src: azStorage("") + `
resource "azurerm_storage_account_network_rules" "n" {
  storage_account_id = azurerm_storage_account.s.id
  default_action     = "Deny"
}
`, want: 0},
			{name: "no public endpoint", src: azStorage(`  public_network_access_enabled = false`), want: 0},
		},
		"bp_azure_storage_entra_default": {
			{name: "default", src: azStorage(""), want: 1},
			{name: "on", src: azStorage(`  default_to_oauth_authentication = true`), want: 0},
		},
		"bp_azure_blob_soft_delete": {
			{name: "no blob properties", src: azStorage(""), want: 1},
			{name: "retention", src: azStorage(`  blob_properties {
    delete_retention_policy {
      days = 7
    }
  }`), want: 0},
			{name: "file storage", src: azStorage(`  account_kind = "FileStorage"`), want: 0},
		},
		"bp_azure_container_soft_delete": {
			{name: "only blob retention", src: azStorage(`  blob_properties {
    delete_retention_policy {
      days = 7
    }
  }`), want: 1},
			{name: "retention", src: azStorage(`  blob_properties {
    container_delete_retention_policy {
      days = 7
    }
  }`), want: 0},
		},
		"bp_azure_blob_versioning": {
			{name: "default", src: azStorage(""), want: 1},
			{name: "off", src: azStorage(`  blob_properties {
    versioning_enabled = false
  }`), want: 1},
			{name: "on", src: azStorage(`  blob_properties {
    versioning_enabled = true
  }`), want: 0},
			{name: "data lake", src: azStorage(`  is_hns_enabled = true`), want: 0},
		},
		"bp_azure_file_share_soft_delete": {
			{name: "share without retention", src: azStorage("") + azShare, want: 1},
			{name: "share with retention", src: azStorage(`  share_properties {
    retention_policy {
      days = 7
    }
  }`) + azShare, want: 0},
			{name: "no share", src: azStorage(""), want: 0},
		},
		"bp_azure_smb_version": {
			{name: "all versions by default", src: azStorage("") + azShare, want: 1},
			{name: "old version allowed", src: azStorage(`  share_properties {
    smb {
      versions = ["SMB3.0", "SMB3.1.1"]
    }
  }`) + azShare, want: 1},
			{name: "3.1.1 only", src: azStorage(`  share_properties {
    smb {
      versions = ["SMB3.1.1"]
    }
  }`) + azShare, want: 0},
			{name: "NFS share", src: azStorage("") + azNFSShare, want: 0},
		},
		"bp_azure_smb_encryption": {
			{name: "all ciphers by default", src: azStorage("") + azShare, want: 1},
			{name: "AES-256-GCM only", src: azStorage(`  share_properties {
    smb {
      channel_encryption_type = ["AES-256-GCM"]
    }
  }`) + azShare, want: 0},
			{name: "no share", src: azStorage(""), want: 0},
		},
		"bp_azure_public_network_access": {
			{name: "storage default", src: azStorage(""), want: 1},
			{name: "vault default", src: azVault(""), want: 1},
			{name: "databricks on", src: azDatabricks(`  public_network_access_enabled = true`), want: 1},
			{name: "storage off", src: azStorage(`  public_network_access_enabled = false`), want: 0},
			{name: "vault off", src: azVault(`  public_network_access_enabled = false`), want: 0},
		},
		"bp_azure_private_endpoint": {
			{name: "none", src: azStorage(""), want: 1},
			{name: "endpoint", src: azStorage("") + privateEndpoint, want: 0},
			{name: "endpoint for another resource", src: azStorage("") + azVault("") + privateEndpoint, want: 1},
		},
		"bp_azure_diagnostic_logs": {
			{name: "vault without setting", src: azVault(""), want: 1},
			{name: "vault with metrics only category", src: azVault("") + diag("azurerm_key_vault.kv.id", `category = "AzurePolicyEvaluationDetails"`), want: 1},
			{name: "vault audit group", src: azVault("") + diag("azurerm_key_vault.kv.id", `category_group = "audit"`), want: 0},
			{name: "vault AuditEvent", src: azVault("") + diag("azurerm_key_vault.kv.id", `category = "AuditEvent"`), want: 0},
			{name: "databricks any log", src: azDatabricks("") + diag("azurerm_databricks_workspace.db.id", `category = "clusters"`), want: 0},
			{name: "databricks without setting", src: azDatabricks(""), want: 1},
		},
		"bp_azure_kv_purge_protection": {
			{name: "starter default", src: azVault(`  purge_protection_enabled = false`), want: 1},
			{name: "unset", src: azVault(""), want: 1},
			{name: "on", src: azVault(`  purge_protection_enabled = true`), want: 0},
		},
		"bp_azure_kv_rbac": {
			{name: "access policies", src: azVault(""), want: 1},
			{name: "rbac", src: azVault(`  rbac_authorization_enabled = true`), want: 0},
			{name: "legacy attribute", src: azVault(`  enable_rbac_authorization = true`), want: 0},
		},
		"bp_azure_kv_expiration": {
			{name: "secret without expiry", src: `resource "azurerm_key_vault_secret" "s" {
  name         = "s"
  value        = var.v
  key_vault_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
}
`, want: 1},
			{name: "key with expiry", src: `resource "azurerm_key_vault_key" "k" {
  name            = "k"
  key_vault_id    = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
  key_type        = "RSA"
  key_size        = 2048
  key_opts        = ["decrypt", "encrypt"]
  expiration_date = "2027-01-01T00:00:00Z"
}
`, want: 0},
		},
		"bp_azure_kv_key_rotation": {
			{name: "no policy", src: `resource "azurerm_key_vault_key" "k" {
  name         = "k"
  key_vault_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
  key_type     = "RSA"
  key_size     = 2048
  key_opts     = ["decrypt", "encrypt"]
}
`, want: 1},
			{name: "automatic rotation", src: `resource "azurerm_key_vault_key" "k" {
  name         = "k"
  key_vault_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
  key_type     = "RSA"
  key_size     = 2048
  key_opts     = ["decrypt", "encrypt"]
  rotation_policy {
    expire_after = "P1Y"
    automatic {
      time_before_expiry = "P30D"
    }
  }
}
`, want: 0},
		},
		"bp_azure_kv_cert_validity": {
			{name: "24 months", src: azCert(24), want: 1},
			{name: "12 months", src: azCert(12), want: 0},
		},
		"bp_azure_nsg_udp_internet": {
			{name: "DNS from anywhere", src: azNSGRule("Udp", "*", "53"), want: 1},
			{name: "any protocol range from Internet", src: azNSGRule("*", "Internet", "100-200"), want: 1},
			{name: "all ports", src: azNSGRule("Udp", "0.0.0.0/0", "*"), want: 1},
			{name: "TCP", src: azNSGRule("Tcp", "*", "53"), want: 0},
			{name: "private source", src: azNSGRule("Udp", "10.0.0.0/8", "53"), want: 0},
			{name: "other UDP port", src: azNSGRule("Udp", "*", "4500"), want: 0},
			{name: "inline rule", src: `resource "azurerm_network_security_group" "n" {
  name                = "n"
  resource_group_name = "rg"
  location            = "eastus"
  security_rule {
    name                       = "ntp"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Udp"
    source_port_range          = "*"
    destination_port_range     = "123"
    source_address_prefix      = "Internet"
    destination_address_prefix = "*"
  }
}
`, want: 1},
		},
		"bp_azure_flow_log_retention": {
			{name: "30 days", src: flowLog("true", "30"), want: 1},
			{name: "90 days", src: flowLog("true", "90"), want: 0},
			{name: "forever", src: flowLog("true", "0"), want: 0},
			{name: "no retention policy", src: flowLog("false", "7"), want: 0},
		},
		"bp_azure_subnet_nsg": {
			{name: "no association", src: subnet("app"), want: 1},
			{name: "associated", src: subnet("app") + assoc, want: 0},
			{name: "gateway subnet", src: subnet("GatewaySubnet"), want: 0},
			{name: "inline subnet", src: `resource "azurerm_virtual_network" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "eastus"
  address_space       = ["10.0.0.0/16"]
  subnet {
    name             = "app"
    address_prefixes = ["10.0.1.0/24"]
  }
  subnet {
    name             = "db"
    address_prefixes = ["10.0.2.0/24"]
    security_group   = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/networkSecurityGroups/n"
  }
}
`, want: 1},
		},
		"bp_azure_appgw_tls": {
			{name: "custom TLS 1.1", src: azAppGw(`  ssl_policy {
    policy_type          = "Custom"
    min_protocol_version = "TLSv1_1"
  }`), want: 1},
			{name: "legacy predefined", src: azAppGw(`  ssl_policy {
    policy_type = "Predefined"
    policy_name = "AppGwSslPolicy20150501"
  }`), want: 1},
			{name: "profile policy", src: azAppGw(`  ssl_profile {
    name = "p"
    ssl_policy {
      policy_type          = "Custom"
      min_protocol_version = "TLSv1_0"
    }
  }`), want: 1},
			{name: "2022 policy", src: azAppGw(`  ssl_policy {
    policy_type = "Predefined"
    policy_name = "AppGwSslPolicy20220101"
  }`), want: 0},
			{name: "strict 2017 policy", src: azAppGw(`  ssl_policy {
    policy_type = "Predefined"
    policy_name = "AppGwSslPolicy20170401S"
  }`), want: 0},
			{name: "default policy", src: azAppGw(""), want: 0},
		},
		"bp_azure_appgw_waf": {
			{name: "standard tier", src: azAppGw(`  sku {
    name = "Standard_v2"
    tier = "Standard_v2"
  }`), want: 1},
			{name: "WAF tier without WAF", src: azAppGw(`  sku {
    name = "WAF_v2"
    tier = "WAF_v2"
  }`), want: 1},
			{name: "WAF policy", src: azAppGw(`  sku {
    name = "WAF_v2"
    tier = "WAF_v2"
  }
  firewall_policy_id = azurerm_web_application_firewall_policy.w.id`) + wafPolicy("", ""), want: 0},
			{name: "disabled WAF policy", src: azAppGw(`  sku {
    name = "WAF_v2"
    tier = "WAF_v2"
  }
  firewall_policy_id = azurerm_web_application_firewall_policy.w.id`) + wafPolicy("enabled = false", ""), want: 1},
			{name: "waf configuration", src: azAppGw(`  sku {
    name = "WAF_v2"
    tier = "WAF_v2"
  }
  waf_configuration {
    enabled          = true
    firewall_mode    = "Prevention"
    rule_set_version = "3.2"
  }`), want: 0},
		},
		"bp_azure_appgw_http2": {
			{name: "default", src: azAppGw(""), want: 1},
			{name: "on", src: azAppGw(`  http2_enabled = true`), want: 0},
			{name: "deprecated attribute", src: azAppGw(`  enable_http2 = true`), want: 0},
		},
		"bp_azure_waf_request_body": {
			{name: "policy off", src: wafPolicy("request_body_check = false", ""), want: 1},
			{name: "gateway off", src: azAppGw(`  waf_configuration {
    enabled            = true
    firewall_mode      = "Prevention"
    rule_set_version   = "3.2"
    request_body_check = false
  }`), want: 1},
			{name: "default", src: wafPolicy("", ""), want: 0},
		},
		"bp_azure_waf_bot_protection": {
			{name: "OWASP only", src: wafPolicy("", ""), want: 1},
			{name: "bot manager", src: wafPolicy("", `managed_rule_set {
      type    = "Microsoft_BotManagerRuleSet"
      version = "1.1"
    }`), want: 0},
		},
		"bp_azure_vpn_entra_auth": {
			{name: "certificate", src: vpn(`root_certificate {
      name             = "root"
      public_cert_data = "MIIC"
    }`), want: 1},
			{name: "mixed types", src: vpn(`vpn_auth_types = ["AAD", "Certificate"]`), want: 1},
			{name: "entra only", src: vpn(`vpn_auth_types = ["AAD"]
    aad_tenant     = "https://login.microsoftonline.com/t/"
    aad_audience   = "41b23e61-6c1e-4545-b367-cd054e0ed4b4"
    aad_issuer     = "https://sts.windows.net/t/"`), want: 0},
		},
		"bp_azure_vnet_ddos": {
			{name: "no plan", src: `resource "azurerm_virtual_network" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "eastus"
  address_space       = ["10.0.0.0/16"]
}
`, want: 1},
			{name: "plan", src: `resource "azurerm_virtual_network" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "eastus"
  address_space       = ["10.0.0.0/16"]
  ddos_protection_plan {
    id     = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.Network/ddosProtectionPlans/p"
    enable = true
  }
}
`, want: 0},
		},
		"bp_azure_databricks_vnet": {
			{name: "managed network", src: azDatabricks(""), want: 1},
			{name: "injected", src: azDatabricks(fmt.Sprintf(azDatabricksVNet, "true")), want: 0},
		},
		"bp_azure_databricks_public_ip": {
			{name: "public IPs", src: azDatabricks(fmt.Sprintf(azDatabricksVNet, "false")), want: 1},
			{name: "secure cluster connectivity", src: azDatabricks(fmt.Sprintf(azDatabricksVNet, "true")), want: 0},
			{name: "default", src: azDatabricks(""), want: 0},
		},
		"bp_azure_custom_admin_role": {
			{name: "star at subscription", src: azRoleDef(`["*"]`, `[data.azurerm_subscription.primary.id]`), want: 1},
			{name: "star at literal subscription", src: azRoleDef(`["*"]`, `["/subscriptions/00000000-0000-0000-0000-000000000000"]`), want: 1},
			{name: "star at resource group", src: azRoleDef(`["*"]`, `["/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg"]`), want: 0},
			{name: "scoped actions", src: azRoleDef(`["Microsoft.Storage/storageAccounts/read"]`, `[data.azurerm_subscription.primary.id]`), want: 0},
		},
	}
	for code, cs := range cases {
		structureCases[code] = append(structureCases[code], cs...)
	}
}

func azCert(months int) string {
	return fmt.Sprintf(`resource "azurerm_key_vault_certificate" "c" {
  name         = "c"
  key_vault_id = "/subscriptions/x/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/kv"
  certificate_policy {
    issuer_parameters {
      name = "Self"
    }
    key_properties {
      exportable = true
      key_size   = 2048
      key_type   = "RSA"
      reuse_key  = true
    }
    secret_properties {
      content_type = "application/x-pkcs12"
    }
    x509_certificate_properties {
      key_usage          = ["digitalSignature"]
      subject            = "CN=example"
      validity_in_months = %d
    }
  }
}
`, months)
}

// Every Azure CIS rule documents its practice with an official source.
func TestAzureCISRulesHaveDocs(t *testing.T) {
	n := 0
	for _, r := range Rules() {
		if !strings.HasPrefix(r.Code, "bp_azure_") {
			continue
		}
		n++
		if !strings.HasPrefix(r.Docs, "https://learn.microsoft.com/") || r.Title == "" || r.Description == "" {
			t.Errorf("%s: title, description and a Microsoft Learn docs link are required (docs %q)", r.Code, r.Docs)
		}
	}
	if n == 0 {
		t.Fatal("no Azure CIS rules registered")
	}
}

// The storage starter is quiet in Problems (opt-in rules off) yet graded
// by the opt-in rules: anonymous access, shared key and public endpoint.
func TestAzureStorageStarterOptIn(t *testing.T) {
	wf := importHCL(t, azStorage(""))
	for _, d := range Check(wf, testCatalog, model.BestPracticeSettings{}, nil) {
		if strings.HasPrefix(d.Code, "bp_azure_") {
			t.Errorf("default settings report %s: %s", d.Code, d.Message)
		}
	}
	for _, code := range []string{"bp_azure_storage_anonymous_access", "bp_azure_storage_shared_key", "bp_azure_public_network_access"} {
		if got := findings(t, azStorage(""), code); len(got) != 1 {
			t.Errorf("%s: %d findings with opt-in rules on, want 1", code, len(got))
		}
	}
}
