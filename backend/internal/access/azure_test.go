package access

import (
	"errors"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// iamImport, iamNode and iamSave are the Azure and GCP tests' helpers:
// import main.tf, find a node, and run the save pipeline (apply,
// normalize, export, re-import).
func iamImport(t *testing.T, src string) *model.Workflow {
	t.Helper()
	wf, diags, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			t.Fatalf("import: %+v", d)
		}
	}
	return wf
}

func iamNode(t *testing.T, wf *model.Workflow, addr string) *model.Node {
	t.Helper()
	for i := range wf.Nodes {
		if Address(&wf.Nodes[i]) == addr {
			return &wf.Nodes[i]
		}
	}
	t.Fatalf("no node %s", addr)
	return nil
}

func iamSave(t *testing.T, wf *model.Workflow, addr string, edit model.AccessEdit) string {
	t.Helper()
	n := iamNode(t, wf, addr)
	edit.NodeID = n.ID
	if err := Apply(wf, n, edit); err != nil {
		t.Fatal(err)
	}
	for _, d := range hclcodec.Normalize(wf) {
		if d.Severity == model.SeverityError {
			t.Fatalf("normalize: %+v", d)
		}
	}
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	iamImport(t, string(files["main.tf"]))
	return string(files["main.tf"])
}

func iamExport(t *testing.T, wf *model.Workflow) string {
	t.Helper()
	files, err := hclcodec.Export(wf, hclcodec.ExportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return string(files["main.tf"])
}

func iamCodes(ws []model.AccessWarning) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.Code+"@"+w.Path)
	}
	return out
}

const azureSrc = `data "azurerm_subscription" "current" {}

resource "azurerm_resource_group" "main" {
  name     = "rg"
  location = "eastus"
}

resource "azurerm_key_vault" "main" {
  name                = "kv"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  tenant_id           = "00000000-0000-0000-0000-000000000000"
  sku_name            = "standard"
}

resource "azurerm_linux_web_app" "api" {
  name                = "api"
  resource_group_name = azurerm_resource_group.main.name
  location            = azurerm_resource_group.main.location
  service_plan_id     = "plan"

  identity {
    type = "SystemAssigned"
  }

  site_config {}
}

# The API reads its secrets.
resource "azurerm_role_assignment" "api_secrets" {
  scope                = azurerm_key_vault.main.id # the vault only
  role_definition_name = "Key Vault Secrets User"
  principal_id         = azurerm_linux_web_app.api.identity[0].principal_id
  description          = "API reads secrets"
  condition_version    = "2.0"
  condition            = "((!(ActionMatches{'Microsoft.KeyVault/vaults/secrets/getSecret/action'})))"
}

resource "azurerm_role_assignment" "computed" {
  scope                = azurerm_key_vault.main.id
  role_definition_name = var.role
  principal_id         = azurerm_linux_web_app.api.identity[0].principal_id
}

resource "azurerm_role_assignment" "owner" {
  scope                = data.azurerm_subscription.current.id
  role_definition_name = "Owner"
  principal_id         = azurerm_linux_web_app.api.identity[0].principal_id
}

resource "azurerm_role_assignment" "by_id" {
  scope              = "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg"
  role_definition_id = "/subscriptions/00000000-0000-0000-0000-000000000000/providers/Microsoft.Authorization/roleDefinitions/b24988ac-6180-42a0-ab88-20f7382dd24c"
  principal_id       = var.principal
  principal_type     = "ServicePrincipal"
}

resource "azurerm_role_assignment" "nested" {
  scope                = "${azurerm_resource_group.main.id}/providers/Microsoft.KeyVault/vaults/kv"
  role_definition_name = "Key Vault Secrets User"
  principal_id         = var.principal
}

variable "role" {
  type = string
}

variable "principal" {
  type = string
}
`

func azureView(t *testing.T, wf *model.Workflow, addr string) *model.AccessView {
	t.Helper()
	v, err := View(wf, iamNode(t, wf, addr))
	if err != nil {
		t.Fatal(err)
	}
	if v.Provider != "azure" || v.Azure == nil {
		t.Fatalf("view %+v", v)
	}
	return v
}

func TestAzureViewReference(t *testing.T) {
	wf := iamImport(t, azureSrc)
	v := azureView(t, wf, "azurerm_role_assignment.api_secrets")
	want := model.AzureRoleAssignment{
		Scope: "azurerm_key_vault.main.id", RoleName: "Key Vault Secrets User",
		PrincipalID: "azurerm_linux_web_app.api.identity[0].principal_id", Editable: true,
	}
	if *v.Azure != want {
		t.Errorf("view %+v\nwant %+v", *v.Azure, want)
	}
	if len(v.Warnings) != 0 {
		t.Errorf("warnings %+v", v.Warnings)
	}
	if len(v.Related) != 2 || v.Related[0].Address != "azurerm_linux_web_app.api" || v.Related[1].Address != "azurerm_key_vault.main" {
		t.Errorf("related %+v", v.Related)
	}
}

func TestAzureRoundTripKeepsEverythingElse(t *testing.T) {
	wf := iamImport(t, azureSrc)
	before := iamExport(t, wf)
	ra := *azureView(t, wf, "azurerm_role_assignment.api_secrets").Azure

	// Unchanged: byte-identical.
	if got := iamSave(t, wf, "azurerm_role_assignment.api_secrets", model.AccessEdit{Azure: &ra}); got != before {
		t.Fatalf("unchanged edit rewrote the file:\n%s", got)
	}

	ra.RoleName = "Key Vault Secrets Officer"
	ra.PrincipalType = "ServicePrincipal"
	got := iamSave(t, iamImport(t, before), "azurerm_role_assignment.api_secrets", model.AccessEdit{Azure: &ra})
	want := strings.Replace(before, `role_definition_name = "Key Vault Secrets User"
  principal_id`, `role_definition_name = "Key Vault Secrets Officer"
  principal_id`, 1)
	want = strings.Replace(want, `condition            = "((!(ActionMatches{'Microsoft.KeyVault/vaults/secrets/getSecret/action'})))"
}`, `condition            = "((!(ActionMatches{'Microsoft.KeyVault/vaults/secrets/getSecret/action'})))"
  principal_type       = "ServicePrincipal"
}`, 1)
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	for _, keep := range []string{"# The API reads its secrets.", "# the vault only", `description          = "API reads secrets"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %q", keep)
		}
	}
}

func TestAzureSwitchToRoleDefinitionID(t *testing.T) {
	wf := iamImport(t, azureSrc)
	ra := *azureView(t, wf, "azurerm_role_assignment.api_secrets").Azure
	ra.RoleName = ""
	ra.RoleID = "data.azurerm_role_definition.reader.id"
	ra.Scope = `"${azurerm_key_vault.main.id}"`
	iamSave(t, wf, "azurerm_role_assignment.api_secrets", model.AccessEdit{Azure: &ra})
	n := iamNode(t, wf, "azurerm_role_assignment.api_secrets")
	if GetAttr(&n.Body, "role_definition_name") != nil || Source(GetAttr(&n.Body, "role_definition_id").Value) != ra.RoleID {
		t.Errorf("body %+v", n.Body.Attrs)
	}
	if got := ReadAzureRoleAssignment(&n.Body); got.RoleID != ra.RoleID || got.RoleName != "" || got.Scope != ra.Scope {
		t.Errorf("read back %+v", got)
	}
}

func TestAzureReadOnlyFallback(t *testing.T) {
	wf := iamImport(t, azureSrc)
	v := azureView(t, wf, "azurerm_role_assignment.computed")
	if v.Azure.Editable || !strings.Contains(v.Azure.Reason, "var.role") || v.Azure.Scope != "azurerm_key_vault.main.id" {
		t.Fatalf("view %+v", v.Azure)
	}
	before := iamExport(t, wf)
	edit := *v.Azure
	edit.RoleName = "Reader"
	n := iamNode(t, wf, "azurerm_role_assignment.computed")
	var ee *EditError
	if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, Azure: &edit}); !errors.As(err, &ee) {
		t.Fatalf("apply on read-only = %v", err)
	}
	if after := iamExport(t, wf); after != before {
		t.Errorf("refused edit changed the file")
	}
	// Both role attributes at once.
	both := ReadAzureRoleAssignment(&model.Body{Attrs: []model.Attr{
		{Name: "role_definition_name", Value: model.Value{Expr: `"Reader"`}},
		{Name: "role_definition_id", Value: model.Value{Expr: "var.id"}},
	}})
	if both.Editable {
		t.Errorf("both role attributes editable: %+v", both)
	}
}

func TestAzureEditValidation(t *testing.T) {
	wf := iamImport(t, azureSrc)
	n := iamNode(t, wf, "azurerm_role_assignment.api_secrets")
	base := *azureView(t, wf, "azurerm_role_assignment.api_secrets").Azure
	for name, mut := range map[string]func(*model.AzureRoleAssignment){
		"no scope":     func(r *model.AzureRoleAssignment) { r.Scope = "" },
		"no principal": func(r *model.AzureRoleAssignment) { r.PrincipalID = " " },
		"no role":      func(r *model.AzureRoleAssignment) { r.RoleName = "" },
		"both roles":   func(r *model.AzureRoleAssignment) { r.RoleID = "var.x" },
		"bad type":     func(r *model.AzureRoleAssignment) { r.PrincipalType = "Robot" },
		"bad expr":     func(r *model.AzureRoleAssignment) { r.Scope = "azurerm_key_vault.main.id +" },
	} {
		ra := base
		mut(&ra)
		var ee *EditError
		if err := Apply(wf, n, model.AccessEdit{NodeID: n.ID, Azure: &ra}); !errors.As(err, &ee) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestAzureWarnings(t *testing.T) {
	wf := iamImport(t, azureSrc)
	cases := map[string][]string{
		"azurerm_role_assignment.owner":  {"azure_privileged_role@role", "azure_broad_scope@scope"},
		"azurerm_role_assignment.by_id":  {"azure_privileged_role@role", "azure_broad_scope@scope"},
		"azurerm_role_assignment.nested": {},
	}
	for addr, want := range cases {
		v := azureView(t, wf, addr)
		if got := iamCodes(v.Warnings); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: warnings %v, want %v", addr, got, want)
		}
	}
	owner := azureView(t, wf, "azurerm_role_assignment.owner")
	if !strings.Contains(owner.Warnings[0].Message, "Owner") || !strings.Contains(owner.Warnings[1].Message, "subscription") {
		t.Errorf("messages %+v", owner.Warnings)
	}
	if !strings.Contains(azureView(t, wf, "azurerm_role_assignment.by_id").Warnings[1].Message, "resource group") {
		t.Errorf("by_id scope kind")
	}
	for scope, want := range map[string]string{
		`"/subscriptions/${data.azurerm_subscription.current.subscription_id}"`:               "subscription",
		`"/providers/Microsoft.Management/managementGroups/root"`:                             "management group",
		`azurerm_resource_group.main.id`:                                                      "resource group",
		`"${data.azurerm_subscription.current.id}/resourceGroups/rg"`:                         "resource group",
		`azurerm_key_vault.main.id`:                                                           "",
		`"/subscriptions/x/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/st"`: "",
	} {
		if got := AzureScopeKind(wf, scope); got != want {
			t.Errorf("AzureScopeKind(%s) = %q, want %q", scope, got, want)
		}
	}
}

func TestAzureCatalog(t *testing.T) {
	c, err := Catalog("azure", CatalogQuery{ResourceType: "azurerm_key_vault"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Source.URL != "https://learn.microsoft.com/en-us/azure/role-based-access-control/built-in-roles" || len(c.Source.Retrieved) != 10 {
		t.Errorf("source %+v", c.Source)
	}
	if len(c.Roles) < 400 {
		t.Fatalf("%d roles", len(c.Roles))
	}
	first := c.Roles[0]
	if first.Name != "Key Vault Secrets User" || first.Suggested != model.AccessRead || first.ID != "4633458b-17de-408a-b874-0445c86b69e6" || first.Description == "" {
		t.Errorf("first role %+v", first)
	}
	for _, name := range []string{"Owner", "Contributor", "User Access Administrator", "Role Based Access Control Administrator"} {
		if r, ok := AzureRole(name); !ok || !r.Privileged {
			t.Errorf("%s: %+v %v", name, r, ok)
		}
	}
	if r, ok := AzureRole("Reader"); !ok || r.Privileged {
		t.Errorf("Reader %+v", r)
	}
	// Every suggested role is a pinned built-in role.
	for typ, levels := range azureSuggestions {
		for _, roles := range levels {
			for _, name := range roles {
				if _, ok := AzureRole(name); !ok {
					t.Errorf("%s suggests unknown role %q", typ, name)
				}
			}
		}
	}
	s, _ := Catalog("azure", CatalogQuery{Search: "acrpull"})
	if len(s.Roles) != 1 || s.Roles[0].Name != "AcrPull" {
		t.Errorf("search %+v", s.Roles)
	}
}
