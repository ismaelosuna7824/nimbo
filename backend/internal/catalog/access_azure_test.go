package catalog_test

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/challenge"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/learn"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Helpers of the Azure and GCP grant tests (cloud* names keep them apart
// from the other providers' tests in this package).

var cloudCat = func() func(t *testing.T) *catalog.Catalog {
	var c *catalog.Catalog
	return func(t *testing.T) *catalog.Catalog {
		t.Helper()
		if c == nil {
			var err error
			if c, err = catalog.Load(); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}
}()

func cloudImport(t *testing.T, src string) *model.Workflow {
	t.Helper()
	wf, _, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	return wf
}

func cloudNode(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if hclcodec.Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("no node %s", addr)
	return nil
}

// cloudGrants are the access recipes offered between two nodes.
func cloudGrants(t *testing.T, c *catalog.Catalog, wf *model.Workflow, src, dst string) []model.ConnectionRecipe {
	t.Helper()
	var out []model.ConnectionRecipe
	for _, r := range c.Connections(wf, cloudNode(t, wf, src).ID, cloudNode(t, wf, dst).ID) {
		if r.Group == model.AccessRecipeGroup {
			out = append(out, r)
		}
	}
	return out
}

// cloudGrant applies an access level (it must be offered) and normalizes
// like a save; it returns the recipe.
func cloudGrant(t *testing.T, c *catalog.Catalog, wf *model.Workflow, src, dst string, l model.AccessLevel) model.ConnectionRecipe {
	t.Helper()
	rs := cloudGrants(t, c, wf, src, dst)
	i := slices.IndexFunc(rs, func(r model.ConnectionRecipe) bool { return r.Level == l })
	if i < 0 {
		t.Fatalf("%s not offered for %s -> %s: %+v", l, src, dst, rs)
	}
	if err := c.Connect(wf, cloudNode(t, wf, src).ID, cloudNode(t, wf, dst).ID, rs[i].ID); err != nil {
		t.Fatalf("%s: %v", rs[i].ID, err)
	}
	hclcodec.Normalize(wf)
	for _, d := range c.Validate(wf) {
		if d.Severity == model.SeverityError {
			t.Errorf("after %s %s -> %s: %s %s", l, src, dst, d.Code, d.Message)
		}
	}
	return rs[i]
}

func cloudAttr(t *testing.T, n *model.Node, name string) string {
	t.Helper()
	a := access.GetAttr(&n.Body, name)
	if a == nil {
		t.Fatalf("%s.%s unset", hclcodec.Address(n), name)
	}
	return access.Source(a.Value)
}

func cloudNodesOf(wf *model.Workflow, typ string) []*model.Node {
	var out []*model.Node
	for i := range wf.Nodes {
		if wf.Nodes[i].Type == typ {
			out = append(out, &wf.Nodes[i])
		}
	}
	return out
}

// cloudChallenge loads a shipped challenge and imports its starter.
func cloudChallenge(t *testing.T, c *catalog.Catalog, file, id string) (model.Challenge, *model.Workflow) {
	t.Helper()
	data, err := fs.ReadFile(learn.Migrations(), file)
	if err != nil {
		t.Fatal(err)
	}
	m, err := learn.ParseMigration(file, data, challenge.Validate)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range m.Ops {
		if op.Kind != "challenge" || op.ID != id || op.Challenge == nil {
			continue
		}
		ch := *op.Challenge
		ch.ID = op.ID
		st, err := challenge.ResolveStarter(c, ch.Starter)
		if err != nil {
			t.Fatal(err)
		}
		wf, err := challenge.ImportTerraform(c, ch.Title, st.Files)
		if err != nil {
			t.Fatal(err)
		}
		return ch, wf
	}
	t.Fatalf("challenge %s not in %s", id, file)
	return model.Challenge{}, nil
}

// cloudGrade exports wf as a save would and grades the challenge's static
// checks: passed and failed check IDs.
func cloudGrade(t *testing.T, c *catalog.Catalog, ch model.Challenge, wf *model.Workflow) (passed, failed []string) {
	t.Helper()
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{ResourceFile: c.ResourceFile})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := challenge.ImportTerraform(c, ch.Title, files)
	if err != nil {
		t.Fatal(err)
	}
	a, err := challenge.Analyze(context.Background(), c, saved, t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range ch.Checks {
		if k.Type != model.CheckValidate && k.Type != model.CheckRulesClean && k.Type != model.CheckHas {
			continue
		}
		if r := challenge.GradeStatic(k, a); r.Passed {
			passed = append(passed, k.ID)
		} else {
			failed = append(failed, k.ID)
		}
	}
	return passed, failed
}

// TestAzureAppServiceKeyVaultChallengeGrant drags the web app of
// azure-app-service-key-vault onto the vault and picks Read: the web app
// gets a system-assigned identity and Key Vault Secrets User on the vault,
// which passes the challenge's identity and least-privilege checks.
func TestAzureAppServiceKeyVaultChallengeGrant(t *testing.T) {
	c := cloudCat(t)
	ch, wf := cloudChallenge(t, c, "20261023140100_azure_intermediate.yaml", "azure-app-service-key-vault")
	_, failedBefore := cloudGrade(t, c, ch, wf)
	if !slices.Contains(failedBefore, "least-privilege") || !slices.Contains(failedBefore, "identity") {
		t.Fatalf("starter already passes the IAM checks: failed %v", failedBefore)
	}

	rs := cloudGrants(t, c, wf, "azurerm_linux_web_app.api", "azurerm_key_vault.main")
	if len(rs) != 3 || rs[0].Level != model.AccessRead || rs[1].Level != model.AccessWrite || rs[2].Level != model.AccessManage {
		t.Fatalf("grants %+v", rs)
	}
	wantRead := `Adds identity { type = "SystemAssigned" } to azurerm_linux_web_app.api, then creates azurerm_role_assignment.api_main_read: ` +
		`scope = azurerm_key_vault.main.id, role_definition_name = "Key Vault Secrets User", principal_id = azurerm_linux_web_app.api.identity[0].principal_id. ` +
		`Key Vault Secrets User: Read secret contents.`
	if !strings.HasPrefix(rs[0].Description, wantRead) {
		t.Errorf("read preview:\n%s\nwant prefix\n%s", rs[0].Description, wantRead)
	}
	if !strings.Contains(rs[1].Description, `"Key Vault Secrets Officer"`) || !strings.Contains(rs[2].Description, `"Key Vault Administrator"`) {
		t.Errorf("write/manage previews %q / %q", rs[1].Description, rs[2].Description)
	}
	// Dragging the vault onto the app offers the same.
	if back := cloudGrants(t, c, wf, "azurerm_key_vault.main", "azurerm_linux_web_app.api"); len(back) != 3 || back[0].Description != rs[0].Description {
		t.Errorf("reverse %+v", back)
	}

	cloudGrant(t, c, wf, "azurerm_linux_web_app.api", "azurerm_key_vault.main", model.AccessRead)
	app := cloudNode(t, wf, "azurerm_linux_web_app.api")
	ids := access.Blocks(&app.Body, "identity")
	if len(ids) != 1 || access.Source(access.GetAttr(&ids[0].Body, "type").Value) != `"SystemAssigned"` {
		t.Fatalf("identity %+v", ids)
	}
	ra := cloudNode(t, wf, "azurerm_role_assignment.api_main_read")
	v, err := access.View(wf, ra)
	if err != nil {
		t.Fatal(err)
	}
	want := model.AzureRoleAssignment{Scope: "azurerm_key_vault.main.id", RoleName: "Key Vault Secrets User", PrincipalID: "azurerm_linux_web_app.api.identity[0].principal_id", Editable: true}
	if *v.Azure != want || len(v.Warnings) != 0 {
		t.Errorf("assignment %+v warnings %+v", *v.Azure, v.Warnings)
	}

	// Granting again writes nothing.
	again := cloudGrants(t, c, wf, "azurerm_linux_web_app.api", "azurerm_key_vault.main")
	if !strings.HasPrefix(again[0].Description, "Nothing to write: azurerm_role_assignment.api_main_read already grants Key Vault Secrets User") {
		t.Errorf("again %q", again[0].Description)
	}
	n := len(wf.Nodes)
	cloudGrant(t, c, wf, "azurerm_linux_web_app.api", "azurerm_key_vault.main", model.AccessRead)
	if len(wf.Nodes) != n || len(access.Blocks(&app.Body, "identity")) != 1 {
		t.Errorf("duplicate grant wrote: %d -> %d nodes", n, len(wf.Nodes))
	}

	passed, failed := cloudGrade(t, c, ch, wf)
	for _, id := range []string{"least-privilege", "identity", "validate"} {
		if !slices.Contains(passed, id) {
			t.Errorf("check %s fails after the grant (failed: %v)", id, failed)
		}
	}
	// What the challenge still needs (inspector edits, not IAM).
	t.Logf("azure-app-service-key-vault after the grant: passed %v, still failing %v", passed, failed)
}

const azureGrantSrc = `resource "azurerm_resource_group" "main" {
  name     = "rg"
  location = "eastus"
}

resource "azurerm_user_assigned_identity" "jobs" {
  name                = "jobs"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
}

resource "azurerm_storage_account" "data" {
  name                     = "stdata"
  resource_group_name      = azurerm_resource_group.main.name
  location                 = azurerm_resource_group.main.location
  account_tier             = "Standard"
  account_replication_type = "LRS"
}

resource "azurerm_storage_container" "reports" {
  name               = "reports"
  storage_account_id = azurerm_storage_account.data.id
}

resource "azurerm_container_app" "worker" {
  name                         = "worker"
  resource_group_name          = azurerm_resource_group.main.name
  container_app_environment_id = "env"
  revision_mode                = "Single"

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.jobs.id]
  }

  template {
    container {
      name   = "w"
      image  = "img"
      cpu    = 0.25
      memory = "0.5Gi"
    }
  }
}

resource "azurerm_kubernetes_cluster" "aks" {
  name                = "aks"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  dns_prefix          = "aks"

  default_node_pool {
    name       = "default"
    vm_size    = "Standard_D2s_v5"
    node_count = 1
  }

  identity {
    type = "SystemAssigned"
  }
}

resource "azurerm_kubernetes_cluster" "legacy" {
  name                = "legacy"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  dns_prefix          = "legacy"

  default_node_pool {
    name       = "default"
    vm_size    = "Standard_D2s_v5"
    node_count = 1
  }

  service_principal {
    client_id     = "id"
    client_secret = "secret"
  }
}

resource "azurerm_container_registry" "acr" {
  name                = "acr"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Standard"
}

resource "azurerm_cosmosdb_account" "db" {
  name                = "db"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  offer_type          = "Standard"

  consistency_policy {
    consistency_level = "Session"
  }

  geo_location {
    location          = "eastus"
    failover_priority = 0
  }
}

resource "azurerm_servicebus_namespace" "bus" {
  name                = "bus"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  sku                 = "Standard"
}
`

func TestAzureGrantUserAssignedIdentityOnContainer(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	r := cloudGrant(t, c, wf, "azurerm_user_assigned_identity.jobs", "azurerm_storage_container.reports", model.AccessWrite)
	want := `Creates azurerm_role_assignment.jobs_reports_write: scope = azurerm_storage_container.reports.id, role_definition_name = "Storage Blob Data Contributor", principal_id = azurerm_user_assigned_identity.jobs.principal_id.`
	if !strings.HasPrefix(r.Description, want) {
		t.Errorf("preview %q", r.Description)
	}
	ra := cloudNode(t, wf, "azurerm_role_assignment.jobs_reports_write")
	if cloudAttr(t, ra, "scope") != "azurerm_storage_container.reports.id" || cloudAttr(t, ra, "principal_id") != "azurerm_user_assigned_identity.jobs.principal_id" {
		t.Errorf("assignment %+v", ra.Body.Attrs)
	}
}

func TestAzureGrantUserAssignedWorkload(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	// The container app runs as its user-assigned identity: the grant goes
	// to that identity and the app is left alone.
	before := len(cloudNode(t, wf, "azurerm_container_app.worker").Body.Blocks)
	r := cloudGrant(t, c, wf, "azurerm_container_app.worker", "azurerm_servicebus_namespace.bus", model.AccessRead)
	if !strings.Contains(r.Description, `role_definition_name = "Azure Service Bus Data Receiver", principal_id = azurerm_user_assigned_identity.jobs.principal_id.`) {
		t.Errorf("preview %q", r.Description)
	}
	if got := len(cloudNode(t, wf, "azurerm_container_app.worker").Body.Blocks); got != before {
		t.Errorf("container app blocks %d -> %d", before, got)
	}
	if len(cloudNodesOf(wf, access.AzureRoleAssignmentType)) != 1 {
		t.Errorf("assignments %d", len(cloudNodesOf(wf, access.AzureRoleAssignmentType)))
	}
}

func TestAzureGrantAKSKubeletPullsFromRegistry(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	r := cloudGrant(t, c, wf, "azurerm_kubernetes_cluster.aks", "azurerm_container_registry.acr", model.AccessRead)
	if !strings.Contains(r.Description, `role_definition_name = "AcrPull", principal_id = azurerm_kubernetes_cluster.aks.kubelet_identity[0].object_id.`) {
		t.Errorf("preview %q", r.Description)
	}
	// A cluster with a service principal has no managed identity to grant.
	if rs := cloudGrants(t, c, wf, "azurerm_kubernetes_cluster.legacy", "azurerm_container_registry.acr"); len(rs) != 0 {
		t.Errorf("service principal cluster grants %+v", rs)
	}
}

func TestAzureGrantResourceGroupWarns(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	rs := cloudGrants(t, c, wf, "azurerm_user_assigned_identity.jobs", "azurerm_resource_group.main")
	if len(rs) != 3 || !strings.Contains(rs[0].Description, `"Reader"`) || !strings.Contains(rs[2].Description, `"Owner"`) {
		t.Fatalf("grants %+v", rs)
	}
	for _, r := range rs {
		if !strings.Contains(r.Description, "Warning: the role applies to every resource in the resource group") {
			t.Errorf("%s preview has no warning: %q", r.Level, r.Description)
		}
	}
	if !strings.Contains(rs[2].Description, "Warning: Owner is a privileged administrator role.") {
		t.Errorf("manage preview %q", rs[2].Description)
	}
	cloudGrant(t, c, wf, "azurerm_user_assigned_identity.jobs", "azurerm_resource_group.main", model.AccessManage)
	v, err := access.View(wf, cloudNodesOf(wf, access.AzureRoleAssignmentType)[0])
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, w := range v.Warnings {
		codes = append(codes, w.Code)
	}
	if !slices.Equal(codes, []string{"azure_privileged_role", "azure_broad_scope"}) {
		t.Errorf("warnings %v", codes)
	}
}

func TestAzureGrantCosmosDataPlane(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	r := cloudGrant(t, c, wf, "azurerm_user_assigned_identity.jobs", "azurerm_cosmosdb_account.db", model.AccessRead)
	want := `Creates azurerm_cosmosdb_sql_role_assignment.jobs_db_read: role_definition_id = "${azurerm_cosmosdb_account.db.id}/sqlRoleDefinitions/00000000-0000-0000-0000-000000000001" (Cosmos DB Built-in Data Reader), scope = azurerm_cosmosdb_account.db.id, principal_id = azurerm_user_assigned_identity.jobs.principal_id.`
	if r.Description != want {
		t.Errorf("preview\n%s\nwant\n%s", r.Description, want)
	}
	n := cloudNode(t, wf, "azurerm_cosmosdb_sql_role_assignment.jobs_db_read")
	if cloudAttr(t, n, "account_name") != "azurerm_cosmosdb_account.db.name" || cloudAttr(t, n, "resource_group_name") != "azurerm_cosmosdb_account.db.resource_group_name" {
		t.Errorf("body %+v", n.Body.Attrs)
	}
	m := cloudGrants(t, c, wf, "azurerm_user_assigned_identity.jobs", "azurerm_cosmosdb_account.db")[2]
	if !strings.Contains(m.Description, `azurerm_role_assignment.jobs_db_manage: scope = azurerm_cosmosdb_account.db.id, role_definition_name = "DocumentDB Account Contributor"`) {
		t.Errorf("manage %q", m.Description)
	}
}

func TestAzureNoGrantBetweenResources(t *testing.T) {
	c := cloudCat(t)
	wf := cloudImport(t, azureGrantSrc)
	// Neither a storage account nor a registry is a workload identity.
	if rs := cloudGrants(t, c, wf, "azurerm_storage_account.data", "azurerm_container_registry.acr"); len(rs) != 0 {
		t.Errorf("grants %+v", rs)
	}
}
