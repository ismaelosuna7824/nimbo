package catalog

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// GCP grants: dragging a google_service_account (or a workload that runs
// as one: a Cloud Run service or job, a Cloud Function, a VM or instance
// template, a GKE node pool or cluster) onto a resource with
// resource-level IAM writes a google_<resource>_iam_member granting the
// level's least-privilege predefined role (access.GCPIAMTargetFor) to
// "serviceAccount:${<sa>.email}" on that one resource. A workload whose
// service account isn't a google_service_account of the workflow (the
// Compute Engine default account, a variable) gets no grant: give it a
// dedicated service account first.

func init() { registerGranter(gcpGranter{}) }

type gcpGranter struct{}

const gcpSAType = "google_service_account"

// gcpWorkloadSA is where each workload type names the service account it
// runs as (nested block path, attribute last).
var gcpWorkloadSA = map[string][]string{
	"google_cloud_run_v2_service":             {"template", "service_account"},
	"google_cloud_run_v2_job":                 {"template", "template", "service_account"},
	"google_cloud_run_service":                {"template", "spec", "service_account_name"},
	"google_cloudfunctions2_function":         {"service_config", "service_account_email"},
	"google_cloudfunctions_function":          {"service_account_email"},
	"google_compute_instance":                 {"service_account", "email"},
	"google_compute_instance_template":        {"service_account", "email"},
	"google_compute_region_instance_template": {"service_account", "email"},
	"google_container_node_pool":              {"node_config", "service_account"},
	"google_container_cluster":                {"node_config", "service_account"},
}

// gcpGrant is a resolved grant.
type gcpGrant struct {
	sa       *model.Node
	target   *access.GCPIAMTarget
	role     string
	member   string
	typ      string
	name     string
	existing *model.Node
}

func (gcpGranter) plans(c *Catalog, wf *model.Workflow, a, b *model.Node) []accessPlan {
	bl := newBuilder(c, wf)
	for _, pair := range [][2]*model.Node{{a, b}, {b, a}} {
		identity, resource := pair[0], pair[1]
		sa := gcpServiceAccount(bl, identity)
		t := gcpTarget(resource)
		if sa == nil || t == nil || resource == sa {
			continue
		}
		var out []accessPlan
		for _, l := range model.AccessLevels {
			if g, ok := gcpResolve(bl, sa, t, identity, resource, l); ok {
				out = append(out, accessPlan{level: l, desc: g.describe(identity, resource), identity: identity, resource: resource})
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func (gcpGranter) apply(x *conn, p accessPlan) error {
	sa, t := gcpServiceAccount(x.b, p.identity), gcpTarget(p.resource)
	if sa == nil || t == nil {
		return access.Editf("catalog: %s doesn't run as a google_service_account of this workflow", address(p.identity))
	}
	g, ok := gcpResolve(x.b, sa, t, p.identity, p.resource, p.level)
	if !ok {
		return access.Editf("catalog: no %s role for %s", p.level, address(p.resource))
	}
	if g.existing != nil {
		return nil
	}
	n := x.helper(model.KindResource, g.typ, g.name)
	for _, r := range t.Refs {
		setAttr(&n.Body, r[0], expr(ref(p.resource, r[1])))
	}
	setAttr(&n.Body, "role", lit(g.role))
	setAttr(&n.Body, "member", expr(g.member))
	return nil
}

// gcpTarget is the IAM a grant on n writes, or nil.
func gcpTarget(n *model.Node) *access.GCPIAMTarget {
	if n.Kind != model.KindResource && !(n.Kind == model.KindData && n.Type == "google_project") {
		return nil
	}
	return access.GCPIAMTargetFor(n.Type)
}

// gcpServiceAccount is the google_service_account n is or runs as.
func gcpServiceAccount(b *builder, n *model.Node) *model.Node {
	if n.Type == gcpSAType && (n.Kind == model.KindResource || n.Kind == model.KindData) {
		return n
	}
	path, ok := gcpWorkloadSA[n.Type]
	if !ok || n.Kind != model.KindResource {
		return nil
	}
	body := &n.Body
	for _, blk := range path[:len(path)-1] {
		nb := getBlock(body, blk)
		if nb == nil {
			return nil
		}
		body = &nb.Body
	}
	a := getAttr(body, path[len(path)-1])
	if a == nil {
		return nil
	}
	sa, _ := b.resolve(expr(valueSource(a.Value)))
	if sa == nil || sa.Type != gcpSAType {
		return nil
	}
	return sa
}

func gcpResolve(b *builder, sa *model.Node, t *access.GCPIAMTarget, identity, resource *model.Node, l model.AccessLevel) (gcpGrant, bool) {
	roles := t.Roles[slices.Index(model.AccessLevels, l)]
	if len(roles) == 0 {
		return gcpGrant{}, false
	}
	g := gcpGrant{sa: sa, target: t, role: roles[0], member: `"serviceAccount:${` + ref(sa, "email") + `}"`, typ: t.IAM + "_member"}
	main := ref(resource, t.Refs[0][1])
	for _, n := range b.all() {
		if n.Kind == model.KindResource && (n.Type == g.typ || n.Type == t.IAM+"_binding") && g.same(n, main) {
			g.existing = n
			return g, true
		}
	}
	g.name = b.uniqueName(model.KindResource, g.typ, identity.Name+"_"+resource.Name+"_"+string(l))
	return g, true
}

// same reports whether the member or binding n already grants g on the
// resource whose main reference is main.
func (g gcpGrant) same(n *model.Node, main string) bool {
	flat := func(name string) string {
		if a := getAttr(&n.Body, name); a != nil {
			return strings.Join(strings.Fields(valueSource(a.Value)), "")
		}
		return ""
	}
	if flat(g.target.Attr) != main || getBlock(&n.Body, "condition") != nil {
		return false
	}
	if a := getAttr(&n.Body, "role"); a == nil {
		return false
	} else if role, ok := constString(a.Value); !ok || role != g.role {
		return false
	}
	member := strings.Join(strings.Fields(g.member), "")
	if strings.HasSuffix(n.Type, "_binding") {
		for _, m := range listItems(valueSource(getAttrValue(&n.Body, "members"))) {
			if strings.Join(strings.Fields(m), "") == member {
				return true
			}
		}
		return false
	}
	return flat("member") == member
}

func getAttrValue(b *model.Body, name string) model.Value {
	if a := getAttr(b, name); a != nil {
		return a.Value
	}
	return model.Value{}
}

// describe is the exact preview of what apply writes.
func (g gcpGrant) describe(identity, resource *model.Node) string {
	var sb strings.Builder
	who := address(g.sa)
	if identity != g.sa {
		who = fmt.Sprintf("%s (the service account %s runs as)", address(g.sa), address(identity))
	}
	if g.existing != nil {
		fmt.Fprintf(&sb, "Nothing to write: %s already grants %s on %s to %s.", address(g.existing), g.role, address(resource), who)
		return sb.String()
	}
	fmt.Fprintf(&sb, "Creates %s.%s:", g.typ, g.name)
	for _, r := range g.target.Refs {
		fmt.Fprintf(&sb, " %s = %s,", r[0], ref(resource, r[1]))
	}
	fmt.Fprintf(&sb, " role = %q, member = %s", g.role, g.member)
	if identity != g.sa {
		fmt.Fprintf(&sb, " (the service account %s runs as)", address(identity))
	}
	sb.WriteString(".")
	if r, ok := access.GCPRole(g.role); ok {
		if r.Description != "" {
			fmt.Fprintf(&sb, " %s: %s", r.Name, strings.TrimSuffix(r.Description, ".")+".")
		}
		if r.Privileged {
			fmt.Fprintf(&sb, " Warning: %s is a basic role", g.role)
			if g.target.Project {
				sb.WriteString(" on the whole " + strings.TrimSuffix(strings.TrimPrefix(g.target.IAM, "google_"), "_iam"))
			}
			sb.WriteString("; prefer a predefined role on the one resource the service account needs.")
		}
	}
	return sb.String()
}
