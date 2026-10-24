package access

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// GCP access editor: a google_<x>_iam_member (one member) or
// google_<x>_iam_binding (a members list) is "who" (member/members),
// "can do" (role) and "on what" (the resource attribute: bucket,
// secret_id, topic, name, project, ...). Each is its own attribute, so a
// write replaces only what changed: the other attributes (location,
// project), the condition block and comments stay as written. A role that
// is an expression, a members list that isn't a list literal, or dynamic
// blocks make the binding read-only.

var gcpIAMTypeRe = regexp.MustCompile(`^google_[a-z0-9_]+_iam_(member|binding)$`)

// gcpNotResource are attributes of IAM resources that never name the
// resource itself.
var gcpNotResource = []string{"role", "member", "members", "condition", "location", "region", "zone", "project",
	"count", "for_each", "provider", "depends_on", "lifecycle"}

type gcpProvider struct{}

func init() { Register(gcpProvider{}) }

func (gcpProvider) Name() string { return "gcp" }

func (gcpProvider) Supports(n *model.Node) bool {
	return n.Kind == model.KindResource && gcpIAMTypeRe.MatchString(n.Type)
}

func (gcpProvider) View(wf *model.Workflow, n *model.Node) (*model.AccessView, error) {
	b := ReadGCPBinding(n)
	v := &model.AccessView{Provider: "gcp", GCP: &b, Warnings: GCPWarnings(n.Type, b)}
	v.Related = gcpRelated(wf, n, b)
	return v, nil
}

func (gcpProvider) Apply(wf *model.Workflow, n *model.Node, edit model.AccessEdit) error {
	if edit.GCP == nil {
		return Editf("access: the edit of %s has no gcp binding", Address(n))
	}
	return WriteGCPBinding(wf, n, *edit.GCP)
}

// GCPResourceAttr is the attribute of an IAM type naming the resource,
// from the known targets or, for other types, the first attribute of b
// that isn't a role, member, location or meta-argument (else "project").
func GCPResourceAttr(iamType string, b *model.Body) string {
	if t := GCPIAMTargetOf(iamType); t != nil {
		return t.Attr
	}
	if b != nil {
		for _, a := range b.Attrs {
			if !slices.Contains(gcpNotResource, a.Name) {
				return a.Name
			}
		}
	}
	return "project"
}

// ReadGCPBinding parses an IAM member or binding.
func ReadGCPBinding(n *model.Node) model.GCPIAMBinding {
	b := &n.Body
	out := model.GCPIAMBinding{Kind: "member", Members: []string{}, Editable: true}
	if strings.HasSuffix(n.Type, "_iam_binding") {
		out.Kind = "binding"
	}
	out.ResourceAttr = GCPResourceAttr(n.Type, b)
	if a := GetAttr(b, out.ResourceAttr); a != nil {
		out.Resource = strings.TrimSpace(Source(a.Value))
	}
	var reasons []string
	if a := GetAttr(b, "role"); a != nil {
		if role, ok := iamConstString(a.Value); ok {
			out.Role = role
		} else {
			out.Role = strings.TrimSpace(Source(a.Value))
			reasons = append(reasons, fmt.Sprintf("the role is the expression %s", out.Role))
		}
	}
	if out.Kind == "member" {
		if a := GetAttr(b, "member"); a != nil {
			out.Members = append(out.Members, strings.TrimSpace(Source(a.Value)))
		}
	} else if a := GetAttr(b, "members"); a != nil {
		src := strings.TrimSpace(Source(a.Value))
		if items, ok := gcpListItems(src); ok {
			out.Members = items
		} else {
			out.Members = append(out.Members, src)
			reasons = append(reasons, fmt.Sprintf("members is the expression %s, not a list", src))
		}
	}
	for _, blk := range Blocks(b, "condition") {
		out.Condition = gcpRenderBlock(blk)
	}
	if len(Blocks(b, "condition")) > 1 {
		reasons = append(reasons, "it has more than one condition block")
	}
	if len(Blocks(b, "dynamic")) > 0 {
		reasons = append(reasons, "it has dynamic blocks")
	}
	if len(reasons) > 0 {
		out.Editable = false
		out.Reason = "Shown read-only because " + strings.Join(reasons, " and ") + ". Edit it in code."
	}
	return out
}

// gcpListItems splits a list constructor into its element sources.
func gcpListItems(src string) ([]string, bool) {
	e, diags := hclsyntax.ParseExpression([]byte(src+"\n"), "members", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, false
	}
	t, ok := e.(*hclsyntax.TupleConsExpr)
	if !ok {
		return nil, false
	}
	out := make([]string, 0, len(t.Exprs))
	for _, x := range t.Exprs {
		r := x.Range()
		out = append(out, strings.TrimSpace(src[r.Start.Byte:r.End.Byte]))
	}
	return out, true
}

// gcpRenderBlock renders a nested block as HCL (display only).
func gcpRenderBlock(blk *model.Block) string {
	var sb strings.Builder
	sb.WriteString(blk.Type)
	for _, l := range blk.Labels {
		fmt.Fprintf(&sb, " %q", l)
	}
	sb.WriteString(" {\n")
	for _, a := range blk.Body.Attrs {
		fmt.Fprintf(&sb, "  %s = %s\n", a.Name, strings.TrimSpace(Source(a.Value)))
	}
	for i := range blk.Body.Blocks {
		for _, line := range strings.Split(gcpRenderBlock(&blk.Body.Blocks[i]), "\n") {
			sb.WriteString("  " + line + "\n")
		}
	}
	sb.WriteString("}")
	src := sb.String()
	if out, diags := hclcodec.Format("condition.tf", []byte(src)); len(diags) == 0 {
		return strings.TrimSpace(string(out))
	}
	return src
}

var gcpRoleRe = regexp.MustCompile(`^(roles/[A-Za-z0-9_.]+|(projects|organizations)/[^/]+/roles/[A-Za-z0-9_.]+)$`)

// WriteGCPBinding writes b into n, replacing only the attributes whose
// value changed. The condition is read-only; read-only bindings are
// refused.
func WriteGCPBinding(wf *model.Workflow, n *model.Node, b model.GCPIAMBinding) error {
	cur := ReadGCPBinding(n)
	if !cur.Editable {
		return Editf("access: %s can't be edited visually: %s", Address(n), cur.Reason)
	}
	if b.Kind != "" && b.Kind != cur.Kind {
		return Editf("access: %s is an IAM %s, not a %s", Address(n), cur.Kind, b.Kind)
	}
	if b.ResourceAttr != "" && b.ResourceAttr != cur.ResourceAttr {
		return Editf("access: %s names its resource with %s, not %s", Address(n), cur.ResourceAttr, b.ResourceAttr)
	}
	if strings.TrimSpace(b.Condition) != strings.TrimSpace(cur.Condition) {
		return Editf("access: the condition of %s is shown read-only: edit it in code", Address(n))
	}
	role := strings.TrimSpace(b.Role)
	resource := strings.TrimSpace(b.Resource)
	members := make([]string, 0, len(b.Members))
	for i, m := range b.Members {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if err := hclcodec.ValidateExpr(m); err != nil {
			return Editf("access: member %d: %v", i+1, err)
		}
		if slices.Contains(members, m) {
			return Editf("access: %s is listed twice", m)
		}
		members = append(members, m)
	}
	switch {
	case !gcpRoleRe.MatchString(role):
		return Editf("access: %q is not a role ID (roles/<service>.<role>, or projects/<p>/roles/<custom>)", role)
	case resource == "":
		return Editf("access: pick the resource (%s)", cur.ResourceAttr)
	case len(members) == 0:
		return Editf("access: add who gets the role (a member)")
	case cur.Kind == "member" && len(members) > 1:
		return Editf("access: an IAM member grants one member; use a google_*_iam_binding for several")
	}
	if err := hclcodec.ValidateExpr(resource); err != nil {
		return Editf("access: %s: %v", cur.ResourceAttr, err)
	}

	if resource != cur.Resource {
		SetExpr(&n.Body, cur.ResourceAttr, resource)
		gcpRepoint(wf, &n.Body, cur.ResourceAttr, cur.Resource, resource)
	}
	if role != cur.Role {
		SetExpr(&n.Body, "role", iamQuote(role))
	}
	if !slices.Equal(members, cur.Members) {
		if cur.Kind == "member" {
			SetExpr(&n.Body, "member", members[0])
		} else {
			SetExpr(&n.Body, "members", gcpRenderList(members))
		}
	}
	return nil
}

// gcpRepoint moves the companion attributes of the resource (project,
// location, region, zone, dataset_id, ...) that read the old resource node
// to the same attribute of the new one.
func gcpRepoint(wf *model.Workflow, b *model.Body, attr, oldSrc, newSrc string) {
	oldNode, _ := iamRefNode(wf, oldSrc)
	newNode, _ := iamRefNode(wf, newSrc)
	if oldNode == nil || newNode == nil || oldNode == newNode {
		return
	}
	prefix := Address(oldNode) + "."
	for i := range b.Attrs {
		a := &b.Attrs[i]
		if a.Name == attr {
			continue
		}
		src := strings.TrimSpace(Source(a.Value))
		if rest, ok := strings.CutPrefix(src, prefix); ok && hclsyntax.ValidIdentifier(rest) {
			SetExpr(b, a.Name, Address(newNode)+"."+rest)
		}
	}
}

func gcpRenderList(items []string) string {
	one := "[" + strings.Join(items, ", ") + "]"
	if len(one) <= 80 && !strings.Contains(one, "\n") {
		return one
	}
	return "[\n  " + strings.Join(items, ",\n  ") + ",\n]"
}

// gcpPublicMembers are the members that make a grant public.
var gcpPublicMembers = []string{"allUsers", "allAuthenticatedUsers"}

// GCPWarnings are the least-privilege warnings of an IAM member/binding.
func GCPWarnings(iamType string, b model.GCPIAMBinding) []model.AccessWarning {
	out := []model.AccessWarning{}
	level := "" // project, folder or organization for hierarchy IAM
	if t := GCPIAMTargetOf(iamType); t != nil && t.Project {
		level = strings.TrimSuffix(strings.TrimPrefix(t.IAM, "google_"), "_iam")
	}
	projectLevel := level != ""
	role, known := GCPRole(b.Role)
	switch {
	case known && role.Privileged:
		msg := fmt.Sprintf("%s (%s) is a basic role: it grants broad access to almost every service, far more than one workload needs.", b.Role, role.Name)
		if projectLevel {
			msg = fmt.Sprintf("%s (%s) is a basic role on the whole %s: broad access to almost every resource of every service in it.", b.Role, role.Name, level)
		}
		out = append(out, model.AccessWarning{
			Code:     "gcp_basic_role",
			Severity: "warning",
			Message:  msg,
			Fix:      "Grant a predefined role for what the member does, on the one resource it needs (for example roles/secretmanager.secretAccessor on the secret).",
			Path:     "role",
		})
	case projectLevel && (b.Role == "roles/iam.serviceAccountUser" || b.Role == "roles/iam.serviceAccountAdmin"):
		code, what := "gcp_service_account_user", "act as (and use the permissions of)"
		if b.Role == "roles/iam.serviceAccountAdmin" {
			code, what = "gcp_service_account_admin", "manage, and change who can use,"
		}
		out = append(out, model.AccessWarning{
			Code:     code,
			Severity: "info",
			Message:  fmt.Sprintf("%s on the %s lets the member %s every service account in the %s.", b.Role, level, what, level),
			Fix:      "Grant it on the one service account instead (google_service_account_iam_member with service_account_id = google_service_account.<name>.name).",
			Path:     "role",
		})
	case projectLevel:
		if t := gcpRoleTarget(b.Role); t != nil {
			out = append(out, model.AccessWarning{
				Code:     "gcp_project_scope",
				Severity: "warning",
				Message:  fmt.Sprintf("%s is granted on the whole %s: the member gets it on every %s in the %s.", b.Role, level, gcpNoun(t.ResourceType), level),
				Fix:      fmt.Sprintf("Grant it on the one %s instead (%s_member).", gcpNoun(t.ResourceType), t.IAM),
				Path:     "scope",
			})
		}
	}
	for i, m := range b.Members {
		if s, ok := iamConstString(model.Value{Expr: m}); ok && slices.Contains(gcpPublicMembers, s) {
			who := "anyone on the internet"
			if s == "allAuthenticatedUsers" {
				who = "anyone signed in to any Google account"
			}
			out = append(out, model.AccessWarning{
				Code:     "gcp_public_member",
				Severity: "warning",
				Message:  fmt.Sprintf("%s makes this public: %s gets %s.", s, who, b.Role),
				Fix:      "Grant the role to a group, a service account or a user instead (group:analysts@example.com).",
				Path:     fmt.Sprintf("members/%d", i),
			})
		}
	}
	return out
}

// gcpRoleTarget is the resource-level target a role is suggested for
// (roles/secretmanager.secretAccessor -> secrets), or nil.
func gcpRoleTarget(role string) *GCPIAMTarget {
	for i := range gcpIAMTargets {
		t := &gcpIAMTargets[i]
		if t.Project || t.ResourceType == "" {
			continue
		}
		for _, ids := range t.Roles {
			if slices.Contains(ids, role) {
				return t
			}
		}
	}
	return nil
}

// gcpNoun is a readable name of a resource type ("secret_manager_secret").
func gcpNoun(resourceType string) string {
	switch resourceType {
	case "google_storage_bucket":
		return "bucket"
	case "google_secret_manager_secret":
		return "secret"
	case "google_pubsub_topic":
		return "topic"
	case "google_pubsub_subscription":
		return "subscription"
	case "google_cloud_run_v2_service", "google_cloud_run_service":
		return "Cloud Run service"
	case "google_cloud_run_v2_job":
		return "Cloud Run job"
	case "google_bigquery_dataset":
		return "dataset"
	case "google_artifact_registry_repository":
		return "repository"
	case "google_kms_crypto_key":
		return "key"
	case "google_service_account":
		return "service account"
	}
	return strings.ReplaceAll(strings.TrimPrefix(resourceType, "google_"), "_", " ")
}

// gcpRelated links the resource and the service accounts of the members.
func gcpRelated(wf *model.Workflow, n *model.Node, b model.GCPIAMBinding) []model.AccessRelated {
	var out []model.AccessRelated
	add := func(src, label string) {
		if m, _ := iamRefNode(wf, src); m != nil && m.ID != n.ID && !slices.ContainsFunc(out, func(r model.AccessRelated) bool { return r.NodeID == m.ID }) {
			out = append(out, model.AccessRelated{NodeID: m.ID, Address: Address(m), Label: label})
		}
	}
	for _, m := range b.Members {
		add(m, "Who (member)")
	}
	add(b.Resource, "On what (resource)")
	return out
}
