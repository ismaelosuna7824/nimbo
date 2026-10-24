package access

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Azure access editor: an azurerm_role_assignment is "who" (principal_id),
// "can do" (role_definition_name, a built-in role, or role_definition_id)
// and "on what" (scope). Each field is its own attribute, so a write only
// replaces the attributes whose value changed; everything else
// (condition, description, name, comments) stays as written. A role name
// that is an expression, or both role attributes at once, is read-only.

// AzureRoleAssignmentType is the Terraform type the Azure editor edits.
const AzureRoleAssignmentType = "azurerm_role_assignment"

// AzurePrincipalTypes are the values of principal_type.
var AzurePrincipalTypes = []string{"User", "Group", "ServicePrincipal"}

type azureProvider struct{}

func init() { Register(azureProvider{}) }

func (azureProvider) Name() string { return "azure" }

func (azureProvider) Supports(n *model.Node) bool {
	return n.Kind == model.KindResource && n.Type == AzureRoleAssignmentType
}

func (azureProvider) View(wf *model.Workflow, n *model.Node) (*model.AccessView, error) {
	ra := ReadAzureRoleAssignment(&n.Body)
	v := &model.AccessView{Provider: "azure", Azure: &ra, Warnings: AzureWarnings(wf, ra)}
	v.Related = azureRelated(wf, n, ra)
	return v, nil
}

func (azureProvider) Apply(_ *model.Workflow, n *model.Node, edit model.AccessEdit) error {
	if edit.Azure == nil {
		return Editf("access: the edit of %s has no azure role assignment", Address(n))
	}
	return WriteAzureRoleAssignment(n, *edit.Azure)
}

// ReadAzureRoleAssignment parses the fields of a role assignment body.
func ReadAzureRoleAssignment(b *model.Body) model.AzureRoleAssignment {
	ra := model.AzureRoleAssignment{Editable: true}
	src := func(name string) (string, bool) {
		a := GetAttr(b, name)
		if a == nil {
			return "", false
		}
		return strings.TrimSpace(Source(a.Value)), true
	}
	ra.Scope, _ = src("scope")
	ra.PrincipalID, _ = src("principal_id")
	ra.RoleID, _ = src("role_definition_id")
	var reasons []string
	if a := GetAttr(b, "role_definition_name"); a != nil {
		if name, ok := iamConstString(a.Value); ok {
			ra.RoleName = name
		} else {
			reasons = append(reasons, fmt.Sprintf("role_definition_name is the expression %s, not a role name", strings.TrimSpace(Source(a.Value))))
		}
	}
	if GetAttr(b, "role_definition_name") != nil && ra.RoleID != "" {
		reasons = append(reasons, "both role_definition_name and role_definition_id are set")
	}
	if a := GetAttr(b, "principal_type"); a != nil {
		if t, ok := iamConstString(a.Value); ok {
			ra.PrincipalType = t
		} else {
			reasons = append(reasons, fmt.Sprintf("principal_type is the expression %s", strings.TrimSpace(Source(a.Value))))
		}
	}
	if len(Blocks(b, "dynamic")) > 0 {
		reasons = append(reasons, "it has dynamic blocks")
	}
	if len(reasons) > 0 {
		ra.Editable = false
		ra.Reason = "Shown read-only because " + strings.Join(reasons, " and ") + ". Edit it in code."
	}
	return ra
}

// WriteAzureRoleAssignment writes ra into n, replacing only the attributes
// whose value changed. Read-only assignments are refused.
func WriteAzureRoleAssignment(n *model.Node, ra model.AzureRoleAssignment) error {
	cur := ReadAzureRoleAssignment(&n.Body)
	if !cur.Editable {
		return Editf("access: %s can't be edited visually: %s", Address(n), cur.Reason)
	}
	ra.Scope = strings.TrimSpace(ra.Scope)
	ra.PrincipalID = strings.TrimSpace(ra.PrincipalID)
	ra.RoleName = strings.TrimSpace(ra.RoleName)
	ra.RoleID = strings.TrimSpace(ra.RoleID)
	ra.PrincipalType = strings.TrimSpace(ra.PrincipalType)
	switch {
	case ra.Scope == "":
		return Editf("access: pick what the role applies to (scope)")
	case ra.PrincipalID == "":
		return Editf("access: pick who gets the role (principal_id)")
	case ra.RoleName == "" && ra.RoleID == "":
		return Editf("access: pick a role")
	case ra.RoleName != "" && ra.RoleID != "":
		return Editf("access: set either a role name or a role definition ID, not both")
	case ra.PrincipalType != "" && !slices.Contains(AzurePrincipalTypes, ra.PrincipalType):
		return Editf("access: principal_type must be one of %s", strings.Join(AzurePrincipalTypes, ", "))
	}
	for _, f := range []struct{ name, src string }{{"scope", ra.Scope}, {"principal_id", ra.PrincipalID}, {"role_definition_id", ra.RoleID}} {
		if f.src == "" {
			continue
		}
		if err := hclcodec.ValidateExpr(f.src); err != nil {
			return Editf("access: %s: %v", f.name, err)
		}
	}

	if ra.Scope != cur.Scope {
		SetExpr(&n.Body, "scope", ra.Scope)
	}
	if ra.RoleName != "" {
		if ra.RoleName != cur.RoleName {
			SetExpr(&n.Body, "role_definition_name", iamQuote(ra.RoleName))
		}
		Unset(&n.Body, "role_definition_id")
	} else {
		if ra.RoleID != cur.RoleID {
			SetExpr(&n.Body, "role_definition_id", ra.RoleID)
		}
		Unset(&n.Body, "role_definition_name")
	}
	if ra.PrincipalID != cur.PrincipalID {
		SetExpr(&n.Body, "principal_id", ra.PrincipalID)
	}
	switch {
	case ra.PrincipalType == "":
		Unset(&n.Body, "principal_type")
	case ra.PrincipalType != cur.PrincipalType:
		SetExpr(&n.Body, "principal_type", iamQuote(ra.PrincipalType))
	}
	return nil
}

// AzureWarnings are the least-privilege warnings of a role assignment.
func AzureWarnings(wf *model.Workflow, ra model.AzureRoleAssignment) []model.AccessWarning {
	out := []model.AccessWarning{}
	role, known := azureAssignedRole(wf, ra)
	if known && role.Privileged {
		out = append(out, model.AccessWarning{
			Code:     "azure_privileged_role",
			Severity: "warning",
			Message:  fmt.Sprintf("%s is a privileged administrator role: %s", role.Name, strings.TrimSuffix(role.Description, ".")+"."),
			Fix:      azurePrivilegedFix(wf, ra.Scope),
			Path:     "role",
		})
	}
	if kind := AzureScopeKind(wf, ra.Scope); kind != "" {
		out = append(out, model.AccessWarning{
			Code:     "azure_broad_scope",
			Severity: "warning",
			Message:  fmt.Sprintf("The role applies to the whole %s: every resource in it, including ones created later.", kind),
			Fix:      "Scope it to the one resource the identity needs (Link to… its id, for example azurerm_key_vault.main.id).",
			Path:     "scope",
		})
	}
	return out
}

func azurePrivilegedFix(wf *model.Workflow, scope string) string {
	if n, _ := iamRefNode(wf, scope); n != nil {
		if roles := AzureSuggestedRoles(n.Type); roles[0] != "" {
			return fmt.Sprintf("Pick the narrowest built-in role for what the identity does on %s, for example %s (read) or %s (write).", Address(n), roles[0], roles[1])
		}
	}
	return "Pick the narrowest built-in role for what the identity does (for example Key Vault Secrets User or Storage Blob Data Reader)."
}

var azureGUIDRe = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// azureAssignedRole is the built-in role an assignment grants: by name, by
// the GUID in role_definition_id, or by the name of the data
// "azurerm_role_definition" role_definition_id reads.
func azureAssignedRole(wf *model.Workflow, ra model.AzureRoleAssignment) (model.AccessRole, bool) {
	if ra.RoleName != "" {
		return AzureRole(ra.RoleName)
	}
	if ra.RoleID == "" {
		return model.AccessRole{}, false
	}
	if n, _ := iamRefNode(wf, ra.RoleID); n != nil && n.Kind == model.KindData && n.Type == "azurerm_role_definition" {
		if a := GetAttr(&n.Body, "name"); a != nil {
			if name, ok := iamConstString(a.Value); ok {
				return AzureRole(name)
			}
		}
		if a := GetAttr(&n.Body, "role_definition_id"); a != nil {
			if id, ok := iamConstString(a.Value); ok {
				return AzureRoleByID(id)
			}
		}
	}
	// ".../providers/Microsoft.Authorization/roleDefinitions/<guid>": the
	// last GUID (the first one may be the subscription's).
	if ids := azureGUIDRe.FindAllString(ra.RoleID, -1); len(ids) > 0 {
		return AzureRoleByID(ids[len(ids)-1])
	}
	return model.AccessRole{}, false
}

var (
	azureMGScopeRe  = regexp.MustCompile(`(?i)^/providers/Microsoft\.Management/managementGroups/[^/]+/?$`)
	azureSubScopeRe = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/?$`)
	azureRGScopeRe  = regexp.MustCompile(`(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/?$`)
)

// AzureScopeKind classifies a broad scope expression: "management group",
// "subscription" or "resource group"; "" for anything narrower (or
// unknown). References count by the type they point at
// (data.azurerm_subscription.x.id, azurerm_resource_group.x.id) and
// literal or interpolated IDs by their shape ("/subscriptions/${...}").
func AzureScopeKind(wf *model.Workflow, scope string) string {
	shape, ok := azureScopeShape(wf, scope)
	if !ok {
		return ""
	}
	switch {
	case azureMGScopeRe.MatchString(shape):
		return "management group"
	case azureSubScopeRe.MatchString(shape):
		return "subscription"
	case azureRGScopeRe.MatchString(shape):
		return "resource group"
	}
	return ""
}

// azureScopeShape renders scope as an ID with every interpolation replaced
// by a stand-in: an ID of the kind of node it references, or "x".
func azureScopeShape(wf *model.Workflow, scope string) (string, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(strings.TrimSpace(scope)+"\n"), "scope", hcl.InitialPos)
	if diags.HasErrors() {
		return "", false
	}
	part := func(x hclsyntax.Expression) string {
		if s, ok := iamExprConstString(x); ok {
			return s
		}
		if t, ok := x.(*hclsyntax.ScopeTraversalExpr); ok {
			if n, attr := iamTraversalNode(wf, t.Traversal); n != nil && attr == "id" {
				switch n.Type {
				case "azurerm_subscription":
					return "/subscriptions/x"
				case "azurerm_resource_group":
					return "/subscriptions/x/resourceGroups/x"
				case "azurerm_management_group":
					return "/providers/Microsoft.Management/managementGroups/x"
				}
			}
		}
		return "x"
	}
	switch x := e.(type) {
	case *hclsyntax.TemplateWrapExpr:
		return part(x.Wrapped), true
	case *hclsyntax.TemplateExpr:
		var sb strings.Builder
		for _, p := range x.Parts {
			sb.WriteString(part(p))
		}
		return sb.String(), true
	}
	return part(e), true
}

// azureRelated links the identity and the scope of an assignment.
func azureRelated(wf *model.Workflow, n *model.Node, ra model.AzureRoleAssignment) []model.AccessRelated {
	var out []model.AccessRelated
	add := func(src, label string) {
		if m, _ := iamRefNode(wf, src); m != nil && m.ID != n.ID && !slices.ContainsFunc(out, func(r model.AccessRelated) bool { return r.NodeID == m.ID }) {
			out = append(out, model.AccessRelated{NodeID: m.ID, Address: Address(m), Label: label})
		}
	}
	add(ra.PrincipalID, "Who (identity)")
	add(ra.Scope, "On what (scope)")
	return out
}

// ---------------------------------------------------------------------------
// HCL helpers shared by the Azure and GCP editors.

// iamConstString is the constant string value of v, if it is one.
func iamConstString(v model.Value) (string, bool) {
	src := strings.TrimSpace(Source(v))
	if src == "" {
		return "", false
	}
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "value", hcl.InitialPos)
	if diags.HasErrors() {
		return "", false
	}
	return iamExprConstString(e)
}

func iamExprConstString(e hclsyntax.Expression) (string, bool) {
	if len(e.Variables()) > 0 {
		return "", false
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsNull() || !v.Type().Equals(cty.String) {
		return "", false
	}
	return v.AsString(), true
}

// iamQuote quotes s as an HCL string literal (no interpolation).
func iamQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "${", "$${", "%{", "%%{")
	return `"` + r.Replace(s) + `"`
}

// iamRefNode is the node the first reference of the expression src points
// at, and the attribute path read from it ("id", "identity[0].principal_id").
func iamRefNode(wf *model.Workflow, src string) (*model.Node, string) {
	if wf == nil || strings.TrimSpace(src) == "" {
		return nil, ""
	}
	e, diags := hclsyntax.ParseExpression([]byte(strings.TrimSpace(src)+"\n"), "value", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, ""
	}
	for _, t := range e.Variables() {
		if n, attr := iamTraversalNode(wf, t); n != nil {
			return n, attr
		}
	}
	return nil, ""
}

// iamTraversalNode resolves a traversal ("azurerm_key_vault.main.id",
// "data.google_project.p.number") to its node and the rest of the path.
func iamTraversalNode(wf *model.Workflow, t hcl.Traversal) (*model.Node, string) {
	if wf == nil || len(t) < 2 {
		return nil, ""
	}
	names := make([]string, 0, len(t))
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			names = append(names, s.Name)
		case hcl.TraverseAttr:
			names = append(names, s.Name)
		default:
			names = append(names, "")
		}
	}
	kind, typ, name, rest := model.KindResource, names[0], names[1], 2
	if names[0] == "data" {
		if len(names) < 3 {
			return nil, ""
		}
		kind, typ, name, rest = model.KindData, names[1], names[2], 3
	}
	if typ == "" || name == "" {
		return nil, ""
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Kind == kind && n.Type == typ && n.Name == name {
			return n, iamPath(t[rest:])
		}
	}
	return nil, ""
}

// iamPath renders traversal steps as "identity[0].principal_id".
func iamPath(t hcl.Traversal) string {
	var sb strings.Builder
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseAttr:
			if sb.Len() > 0 {
				sb.WriteByte('.')
			}
			sb.WriteString(s.Name)
		case hcl.TraverseIndex:
			if s.Key.Type() == cty.Number {
				fmt.Fprintf(&sb, "[%s]", s.Key.AsBigFloat().Text('f', -1))
			} else if s.Key.Type() == cty.String {
				fmt.Fprintf(&sb, "[%q]", s.Key.AsString())
			}
		case hcl.TraverseSplat:
			sb.WriteString("[*]")
		}
	}
	return sb.String()
}
