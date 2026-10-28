package compliance_test

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The CIS Azure pack grades a real design through the real rules, opt-in
// ones included, each control on its own resource types.
func TestCISAzureGradesADesign(t *testing.T) {
	src := `
resource "azurerm_storage_account" "logs" {
  name                            = "logs"
  resource_group_name             = "rg"
  location                        = "eastus"
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  min_tls_version                 = "TLS1_0"
  allow_nested_items_to_be_public = false
  shared_access_key_enabled       = false
}
resource "azurerm_storage_account" "data" {
  name                            = "data"
  resource_group_name             = "rg"
  location                        = "eastus"
  account_tier                    = "Standard"
  account_replication_type        = "LRS"
  allow_nested_items_to_be_public = false
}
resource "azurerm_key_vault" "kv" {
  name                      = "kv"
  resource_group_name       = "rg"
  location                  = "eastus"
  tenant_id                 = "00000000-0000-0000-0000-000000000000"
  sku_name                  = "standard"
  purge_protection_enabled  = true
  rbac_authorization_enabled = true
}
resource "azurerm_network_security_group" "nsg" {
  name                = "nsg"
  resource_group_name = "rg"
  location            = "eastus"
  security_rule {
    name                       = "ssh"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "22"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }
}
resource "azurerm_virtual_network" "v" {
  name                = "v"
  resource_group_name = "rg"
  location            = "eastus"
  address_space       = ["10.0.0.0/16"]
}
resource "azurerm_subnet" "gw" {
  name                 = "GatewaySubnet"
  resource_group_name  = "rg"
  virtual_network_name = azurerm_virtual_network.v.name
  address_prefixes     = ["10.0.255.0/27"]
}
resource "azurerm_network_watcher_flow_log" "f" {
  name                 = "f"
  network_watcher_name = "nw"
  resource_group_name  = "rg"
  target_resource_id   = azurerm_virtual_network.v.id
  storage_account_id   = azurerm_storage_account.logs.id
  enabled              = true
  retention_policy {
    enabled = true
    days    = 30
  }
}
resource "azurerm_network_watcher_flow_log" "legacy" {
  name                 = "legacy"
  network_watcher_name = "nw"
  resource_group_name  = "rg"
  target_resource_id   = azurerm_network_security_group.nsg.id
  storage_account_id   = azurerm_storage_account.logs.id
  enabled              = true
  retention_policy {
    enabled = true
    days    = 90
  }
}
`
	wf, _, err := hclcodec.Import("azure", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	fw, ok := compliance.Lookup("cis-azure-6.0")
	if !ok {
		t.Fatal("cis-azure-6.0 is not registered")
	}
	cfg := compliance.GradingSettings(model.BestPracticeSettings{}, fw)
	r := compliance.Evaluate(fw, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
	want := map[string]compliance.Status{
		"9.3.6":   compliance.StatusViolated,      // TLS 1.0 on one of the two accounts
		"9.3.1.3": compliance.StatusViolated,      // shared key left on one account (opt-in rule)
		"9.3.8":   compliance.StatusMet,           // anonymous access off on both
		"9.3.4":   compliance.StatusMet,           // secure transfer by default
		"9.3.2.2": compliance.StatusViolated,      // public endpoints (opt-in rule, storage types only)
		"8.3.5":   compliance.StatusMet,           // purge protection
		"8.3.7":   compliance.StatusViolated,      // vault public endpoint
		"6.1.1.4": compliance.StatusViolated,      // no diagnostic setting for the vault
		"7.2":     compliance.StatusViolated,      // SSH open
		"7.3":     compliance.StatusMet,           // no UDP
		"7.11":    compliance.StatusNotApplicable, // only a GatewaySubnet (no NSG support) and a vnet without inline subnets
		"7.8":     compliance.StatusViolated,      // virtual network flow log kept 30 days
		"7.5":     compliance.StatusMet,           // the NSG flow log keeps 90 days; the 30-day VNet one doesn't count here
		"2.1.1":   compliance.StatusNotApplicable, // no Databricks
		"5.1.3":   compliance.StatusManual,
	}
	got := map[string]compliance.Status{}
	for _, c := range r.Controls {
		got[c.ID] = c.Status
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("control %s = %q, want %q", id, got[id], status)
		}
	}
}
