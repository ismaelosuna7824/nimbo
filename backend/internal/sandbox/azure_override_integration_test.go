//go:build docker

package sandbox

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const azureStorageMain = `
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.9" }
  }
}

resource "azurerm_resource_group" "rg" {
  name     = "nimbo-it-rg"
  location = "eastus"
}

resource "azurerm_storage_account" "plain" {
  name                     = "nimboitplain"
  resource_group_name      = azurerm_resource_group.rg.name
  location                 = azurerm_resource_group.rg.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_account" "tagged" {
  name                     = "nimboittagged"
  resource_group_name      = azurerm_resource_group.rg.name
  location                 = azurerm_resource_group.rg.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
  tags                     = { owner = "OWNER" }
  lifecycle {
    ignore_changes = [tags]
  }
}
`

// TestAzureStorageAccountsStable: floci-az reports queue and table
// encryption key types it wasn't asked for; with the sandbox override the
// post-deploy check finds no drift, and the user's own ignore_changes
// still applies.
func TestAzureStorageAccountsStable(t *testing.T) {
	m, _ := newTestManager(t)
	wfID := testWorkflowID("it-azsa")
	t.Cleanup(func() { cleanupSandbox(t, m, wfID) })
	wf := &model.Workflow{ID: wfID, Clouds: []model.Cloud{model.CloudAzure}}
	files := func(owner string) map[string][]byte {
		return map[string][]byte{"main.tf": []byte(strings.ReplaceAll(azureStorageMain, "OWNER", owner))}
	}

	apply := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionApply, Files: files("a")})
	if len(apply.Drift) > 0 {
		t.Errorf("post-deploy drift: %+v", apply.Drift)
	}
	// The user's ignore_changes = [tags] survives the override.
	plan := runAndWait(t, m, RunRequest{Workflow: wf, Action: model.ActionPlan, Files: files("b")})
	if p := plan.Plan; p == nil || *p != (model.PlanSummary{}) {
		t.Errorf("plan after apply = %+v, want no changes: %+v", p, plan.Changes)
	}
}
