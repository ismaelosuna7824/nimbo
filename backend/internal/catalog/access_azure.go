package catalog

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Azure grants: dragging an identity (a user-assigned managed identity or
// a workload with a managed identity: web and function apps, container
// apps, VMs, AKS's kubelet identity, ...) onto a resource writes an
// azurerm_role_assignment of the least-privilege built-in role for the
// level (access.AzureSuggestedRoles) with scope = that resource's ID. A
// workload without an identity block gets identity { type =
// "SystemAssigned" } first. Cosmos DB for NoSQL Read/Write use the
// account's own data-plane roles (azurerm_cosmosdb_sql_role_assignment):
// Azure RBAC has no built-in role that reads or writes Cosmos DB data.

func init() { registerGranter(azureGranter{}) }

type azureGranter struct{}

// azureWorkloads are the resource types whose system-assigned identity
// (identity[0].principal_id) can be granted roles.
var azureWorkloads = []string{
	"azurerm_linux_web_app", "azurerm_windows_web_app", "azurerm_linux_web_app_slot", "azurerm_windows_web_app_slot",
	"azurerm_linux_function_app", "azurerm_windows_function_app", "azurerm_linux_function_app_slot",
	"azurerm_windows_function_app_slot", "azurerm_function_app_flex_consumption",
	"azurerm_container_app", "azurerm_container_app_job", "azurerm_container_group",
	"azurerm_linux_virtual_machine", "azurerm_windows_virtual_machine",
	"azurerm_linux_virtual_machine_scale_set", "azurerm_windows_virtual_machine_scale_set",
	"azurerm_logic_app_standard", "azurerm_logic_app_workflow", "azurerm_api_management",
	"azurerm_data_factory", "azurerm_automation_account", "azurerm_spring_cloud_app",
}

const (
	azureUAIType  = "azurerm_user_assigned_identity"
	azureAKSType  = "azurerm_kubernetes_cluster"
	azureCosmos   = "azurerm_cosmosdb_account"
	azureSQLRoleA = "azurerm_cosmosdb_sql_role_assignment"
)

// azureCosmosDataRoles are the Cosmos DB for NoSQL built-in data-plane
// role definitions ("Azure Cosmos DB for NoSQL data plane role-based
// access control reference":
// https://learn.microsoft.com/en-us/azure/cosmos-db/nosql/reference-data-plane-roles).
var azureCosmosDataRoles = map[model.AccessLevel][2]string{
	model.AccessRead:  {"00000000-0000-0000-0000-000000000001", "Cosmos DB Built-in Data Reader"},
	model.AccessWrite: {"00000000-0000-0000-0000-000000000002", "Cosmos DB Built-in Data Contributor"},
}

// azurePrincipal is how a grant names an identity.
type azurePrincipal struct {
	node *model.Node // whose attribute is the principal (the identity, or its user-assigned identity)
	expr string      // principal_id expression
	// addIdentity: the workload has no identity block; the grant adds a
	// system-assigned one.
	addIdentity bool
}

// azureGrant is a resolved grant.
type azureGrant struct {
	who       azurePrincipal
	role      string // built-in role name (role assignment)
	cosmosID  string // data-plane role definition (Cosmos DB)
	scope     string
	typ, name string // the node written
	existing  *model.Node
}

func (azureGranter) plans(c *Catalog, wf *model.Workflow, a, b *model.Node) []accessPlan {
	bl := newBuilder(c, wf)
	for _, pair := range [][2]*model.Node{{a, b}, {b, a}} {
		identity, resource := pair[0], pair[1]
		who, ok := azureResolvePrincipal(c, bl, identity)
		if !ok || !azureIsTarget(resource) {
			continue
		}
		var out []accessPlan
		for _, l := range model.AccessLevels {
			if g, ok := azureResolve(bl, who, identity, resource, l); ok {
				out = append(out, accessPlan{level: l, desc: g.describe(identity, resource), identity: identity, resource: resource})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func (azureGranter) apply(x *conn, p accessPlan) error {
	who, ok := azureResolvePrincipal(x.c, x.b, p.identity)
	if !ok {
		return access.Editf("catalog: %s has no managed identity Nimbo can grant roles to", address(p.identity))
	}
	g, ok := azureResolve(x.b, who, p.identity, p.resource, p.level)
	if !ok {
		return access.Editf("catalog: no %s role for %s", p.level, address(p.resource))
	}
	if g.existing != nil {
		return nil
	}
	if who.addIdentity {
		id := model.Block{Type: "identity", Body: emptyBody()}
		setAttr(&id.Body, "type", lit("SystemAssigned"))
		who.node.Body.Blocks = append(who.node.Body.Blocks, id)
	}
	n := x.helper(model.KindResource, g.typ, g.name)
	if g.typ == azureSQLRoleA {
		setAttr(&n.Body, "resource_group_name", expr(ref(p.resource, "resource_group_name")))
		setAttr(&n.Body, "account_name", expr(ref(p.resource, "name")))
		setAttr(&n.Body, "role_definition_id", expr(g.cosmosRoleExpr(p.resource)))
		setAttr(&n.Body, "principal_id", expr(who.expr))
		setAttr(&n.Body, "scope", expr(g.scope))
		return nil
	}
	setAttr(&n.Body, "scope", expr(g.scope))
	setAttr(&n.Body, "role_definition_name", lit(g.role))
	setAttr(&n.Body, "principal_id", expr(who.expr))
	return nil
}

// azureIsTarget reports whether grants can scope roles to n.
func azureIsTarget(n *model.Node) bool {
	if n.Kind != model.KindResource && n.Kind != model.KindData {
		return false
	}
	roles := access.AzureSuggestedRoles(n.Type)
	return roles != [3]string{}
}

// azureResolvePrincipal works out the principal_id of an identity node.
func azureResolvePrincipal(c *Catalog, b *builder, n *model.Node) (azurePrincipal, bool) {
	switch {
	case n.Type == azureUAIType && (n.Kind == model.KindResource || n.Kind == model.KindData):
		return azurePrincipal{node: n, expr: ref(n, "principal_id")}, true
	case n.Kind != model.KindResource:
		return azurePrincipal{}, false
	case n.Type == azureAKSType:
		// Pods and image pulls run as the kubelet identity, which AKS
		// creates for clusters with a managed identity.
		if getBlock(&n.Body, "service_principal") != nil {
			return azurePrincipal{}, false
		}
		return azurePrincipal{node: n, expr: ref(n, "kubelet_identity[0].object_id"), addIdentity: getBlock(&n.Body, "identity") == nil}, true
	case !slices.Contains(azureWorkloads, n.Type) || !azureHasIdentity(c, n.Type):
		return azurePrincipal{}, false
	}
	id := getBlock(&n.Body, "identity")
	if id == nil {
		return azurePrincipal{node: n, expr: ref(n, "identity[0].principal_id"), addIdentity: true}, true
	}
	t := getAttr(&id.Body, "type")
	if t == nil {
		return azurePrincipal{}, false
	}
	typ, ok := constString(t.Value)
	if !ok {
		return azurePrincipal{}, false
	}
	if strings.Contains(typ, "SystemAssigned") {
		return azurePrincipal{node: n, expr: ref(n, "identity[0].principal_id")}, true
	}
	// User-assigned only: the first identity of identity_ids in the graph.
	ids := getAttr(&id.Body, "identity_ids")
	if ids == nil {
		return azurePrincipal{}, false
	}
	for _, item := range listItems(valueSource(ids.Value)) {
		if uai, attr := b.resolve(expr(item)); uai != nil && uai.Type == azureUAIType && attr == "id" {
			return azurePrincipal{node: uai, expr: ref(uai, "principal_id")}, true
		}
	}
	return azurePrincipal{}, false
}

// azureHasIdentity reports whether the provider schema of typ has an
// identity block exposing principal_id.
func azureHasIdentity(c *Catalog, typ string) bool {
	s, ok := c.Schema(model.KindResource, typ)
	if !ok {
		return false
	}
	for _, blk := range s.Block.Blocks {
		if blk.Name == "identity" {
			return slices.ContainsFunc(blk.Block.Attributes, func(a model.SchemaAttribute) bool { return a.Name == "principal_id" })
		}
	}
	return false
}

// azureScope is the ARM ID expression of a resource.
func azureScope(n *model.Node) string {
	// A container declared with storage_account_name has a data-plane URL
	// as id; its ARM ID is resource_manager_id.
	if n.Type == "azurerm_storage_container" && getAttr(&n.Body, "storage_account_id") == nil && getAttr(&n.Body, "storage_account_name") != nil {
		return ref(n, "resource_manager_id")
	}
	return ref(n, "id")
}

// azureResolve works out the grant of level l (false: no role for it).
func azureResolve(b *builder, who azurePrincipal, identity, resource *model.Node, l model.AccessLevel) (azureGrant, bool) {
	g := azureGrant{who: who, scope: azureScope(resource), typ: access.AzureRoleAssignmentType}
	if resource.Type == azureCosmos && l != model.AccessManage {
		if !azureCosmosNoSQL(resource) {
			return g, false
		}
		g.typ, g.cosmosID = azureSQLRoleA, azureCosmosDataRoles[l][0]
	} else {
		g.role = access.AzureSuggestedRoles(resource.Type)[slices.Index(model.AccessLevels, l)]
		if g.role == "" {
			return g, false
		}
	}
	for _, n := range b.all() {
		if n.Kind == model.KindResource && n.Type == g.typ && g.same(n, resource) {
			g.existing = n
			return g, true
		}
	}
	g.name = b.uniqueName(model.KindResource, g.typ, identity.Name+"_"+resource.Name+"_"+string(l))
	return g, true
}

// azureCosmosNoSQL reports whether an account serves the API for NoSQL
// (kind GlobalDocumentDB, the default).
func azureCosmosNoSQL(n *model.Node) bool {
	a := getAttr(&n.Body, "kind")
	if a == nil {
		return true
	}
	k, ok := constString(a.Value)
	return ok && k == "GlobalDocumentDB"
}

func (g azureGrant) cosmosRoleExpr(account *model.Node) string {
	return `"${` + ref(account, "id") + `}/sqlRoleDefinitions/` + g.cosmosID + `"`
}

// same reports whether n already grants g.
func (g azureGrant) same(n, resource *model.Node) bool {
	src := func(name string) string {
		if a := getAttr(&n.Body, name); a != nil {
			return strings.Join(strings.Fields(valueSource(a.Value)), "")
		}
		return ""
	}
	if src("scope") != g.scope || src("principal_id") != g.who.expr {
		return false
	}
	if g.typ == azureSQLRoleA {
		return src("role_definition_id") == strings.Join(strings.Fields(g.cosmosRoleExpr(resource)), "")
	}
	a := getAttr(&n.Body, "role_definition_name")
	if a == nil {
		return false
	}
	name, ok := constString(a.Value)
	return ok && strings.EqualFold(name, g.role)
}

// describe is the exact preview of what apply writes.
func (g azureGrant) describe(identity, resource *model.Node) string {
	var sb strings.Builder
	if g.existing != nil {
		role := g.role
		if g.typ == azureSQLRoleA {
			role = azureCosmosDataRoles[model.AccessRead][1]
			if g.cosmosID == azureCosmosDataRoles[model.AccessWrite][0] {
				role = azureCosmosDataRoles[model.AccessWrite][1]
			}
		}
		fmt.Fprintf(&sb, "Nothing to write: %s already grants %s on %s to %s.", address(g.existing), role, address(resource), g.who.expr)
		return sb.String()
	}
	if g.who.addIdentity {
		fmt.Fprintf(&sb, "Adds identity { type = \"SystemAssigned\" } to %s, then creates", address(identity))
	} else {
		sb.WriteString("Creates")
	}
	if g.typ == azureSQLRoleA {
		role := azureCosmosDataRoles[model.AccessRead][1]
		if g.cosmosID == azureCosmosDataRoles[model.AccessWrite][0] {
			role = azureCosmosDataRoles[model.AccessWrite][1]
		}
		fmt.Fprintf(&sb, " %s.%s: role_definition_id = %s (%s), scope = %s, principal_id = %s.",
			g.typ, g.name, g.cosmosRoleExpr(resource), role, g.scope, g.who.expr)
		return sb.String()
	}
	fmt.Fprintf(&sb, " %s.%s: scope = %s, role_definition_name = %q, principal_id = %s.", g.typ, g.name, g.scope, g.role, g.who.expr)
	if r, ok := access.AzureRole(g.role); ok && r.Description != "" {
		fmt.Fprintf(&sb, " %s: %s", r.Name, strings.TrimSuffix(r.Description, ".")+".")
	}
	if r, ok := access.AzureRole(g.role); ok && r.Privileged {
		fmt.Fprintf(&sb, " Warning: %s is a privileged administrator role.", g.role)
	}
	switch resource.Type {
	case "azurerm_resource_group":
		sb.WriteString(" Warning: the role applies to every resource in the resource group; prefer granting it on the one resource the identity needs.")
	case "azurerm_subscription":
		sb.WriteString(" Warning: the role applies to every resource in the subscription; prefer granting it on the one resource the identity needs.")
	}
	return sb.String()
}

// ---------------------------------------------------------------------------
// Helpers shared by the Azure and GCP granters.

// constString is the constant string value of v.
func constString(v model.Value) (string, bool) {
	if len(v.Literal) > 0 {
		var s string
		if err := json.Unmarshal(v.Literal, &s); err == nil {
			return s, true
		}
		return "", false
	}
	raw, ok := hclcodec.LiteralFromExpr(strings.TrimSpace(v.Expr))
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// listItems splits a list constructor's source into element sources ([]
// for anything else).
func listItems(src string) []string {
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "list", hcl.InitialPos)
	if diags.HasErrors() {
		return nil
	}
	t, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(t.Exprs))
	for _, x := range t.Exprs {
		r := x.Range()
		out = append(out, strings.TrimSpace(src[r.Start.Byte:r.End.Byte]))
	}
	return out
}
