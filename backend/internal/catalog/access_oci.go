package catalog

import (
	"fmt"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// OCI grants: dragging an IAM group or dynamic group onto an OCI resource
// writes policy statements ("Allow group ${g.name} to read buckets in
// compartment ${c.name}") into the oci_identity_policy attached to the
// resource's compartment, or a new one. The verbs follow Oracle's Common
// Policies (https://docs.oracle.com/en-us/iaas/Content/Identity/Concepts/commonpolicies.htm):
// Read → read, Write → use, Manage → manage of the resource's family, with
// the bucket, key, vault and secret statements narrowed to that one
// resource by a where condition.

func init() { registerGranter(ociGranter{}) }

type ociGranter struct{}

// ociGrantStmt is one statement of a grant (subject and location are
// filled from the pair).
type ociGrantStmt struct{ verb, resourceType, condition string }

// ociFamily grants read/use/manage of one resource-type to the whole
// compartment.
func ociFamily(rt string) func(model.AccessLevel, *model.Node) []ociGrantStmt {
	return func(l model.AccessLevel, _ *model.Node) []ociGrantStmt {
		return []ociGrantStmt{{verb: ociVerbOf(l), resourceType: rt}}
	}
}

// ociScoped grants read/use/manage of one resource-type, limited to the
// resource by the condition variable (target.key.id = the key's OCID).
func ociScoped(rt, variable string) func(model.AccessLevel, *model.Node) []ociGrantStmt {
	return func(l model.AccessLevel, n *model.Node) []ociGrantStmt {
		return []ociGrantStmt{{verb: ociVerbOf(l), resourceType: rt, condition: fmt.Sprintf("%s='${%s}'", variable, ref(n, "id"))}}
	}
}

func ociVerbOf(l model.AccessLevel) string {
	switch l {
	case model.AccessRead:
		return "read"
	case model.AccessWrite:
		return "use"
	}
	return "manage"
}

// ociBucket follows "Let users download objects from Object Storage
// buckets" and "Let users write objects to Object Storage buckets" (Common
// Policies), limited to the bucket with target.bucket.name. Write lets the
// group list, read, create and overwrite objects but not delete them.
func ociBucket(l model.AccessLevel, n *model.Node) []ociGrantStmt {
	name := fmt.Sprintf("target.bucket.name='${%s}'", ref(n, "name"))
	switch l {
	case model.AccessRead:
		return []ociGrantStmt{{"read", "buckets", ""}, {"read", "objects", name}}
	case model.AccessWrite:
		return []ociGrantStmt{{"read", "buckets", ""}, {"manage", "objects",
			"all {" + name + ", any {request.permission='OBJECT_INSPECT', request.permission='OBJECT_READ', request.permission='OBJECT_CREATE', request.permission='OBJECT_OVERWRITE'}}"}}
	}
	return []ociGrantStmt{{"manage", "object-family", name}}
}

// ociGrantTargets maps OCI resource types to their statements.
var ociGrantTargets = map[string]func(model.AccessLevel, *model.Node) []ociGrantStmt{
	"oci_objectstorage_bucket":           ociBucket,
	"oci_core_vcn":                       ociFamily("virtual-network-family"),
	"oci_core_subnet":                    ociFamily("virtual-network-family"),
	"oci_core_security_list":             ociFamily("virtual-network-family"),
	"oci_core_network_security_group":    ociFamily("virtual-network-family"),
	"oci_core_route_table":               ociFamily("virtual-network-family"),
	"oci_core_internet_gateway":          ociFamily("virtual-network-family"),
	"oci_core_nat_gateway":               ociFamily("virtual-network-family"),
	"oci_core_service_gateway":           ociFamily("virtual-network-family"),
	"oci_core_dhcp_options":              ociFamily("virtual-network-family"),
	"oci_core_drg":                       ociFamily("virtual-network-family"),
	"oci_core_local_peering_gateway":     ociFamily("virtual-network-family"),
	"oci_core_public_ip":                 ociFamily("virtual-network-family"),
	"oci_core_instance":                  ociFamily("instance-family"),
	"oci_core_instance_configuration":    ociFamily("compute-management-family"),
	"oci_core_instance_pool":             ociFamily("compute-management-family"),
	"oci_core_volume":                    ociFamily("volume-family"),
	"oci_core_volume_group":              ociFamily("volume-family"),
	"oci_kms_vault":                      ociScoped("vaults", "target.vault.id"),
	"oci_kms_key":                        ociScoped("keys", "target.key.id"),
	"oci_vault_secret":                   ociScoped("secret-family", "target.secret.id"),
	"oci_database_autonomous_database":   ociFamily("autonomous-database-family"),
	"oci_database_db_system":             ociFamily("database-family"),
	"oci_containerengine_cluster":        ociFamily("cluster-family"),
	"oci_containerengine_node_pool":      ociFamily("cluster-family"),
	"oci_file_storage_file_system":       ociFamily("file-family"),
	"oci_file_storage_mount_target":      ociFamily("file-family"),
	"oci_functions_application":          ociFamily("functions-family"),
	"oci_functions_function":             ociFamily("functions-family"),
	"oci_streaming_stream":               ociFamily("stream-family"),
	"oci_streaming_stream_pool":          ociFamily("stream-family"),
	"oci_ons_notification_topic":         ociFamily("ons-family"),
	"oci_logging_log_group":              ociFamily("logging-family"),
	"oci_logging_log":                    ociFamily("logging-family"),
	"oci_artifacts_container_repository": ociFamily("repos"),
	"oci_load_balancer_load_balancer":    ociFamily("load-balancers"),
	"oci_load_balancer":                  ociFamily("load-balancers"),
	"oci_apigateway_gateway":             ociFamily("api-gateway-family"),
	"oci_apigateway_deployment":          ociFamily("api-gateway-family"),
	// A compartment: everything in it (warned as broad in the policy view).
	"oci_identity_compartment": ociFamily("all-resources"),
}

// ociSubjectTypes are the identity types and their policy subject type.
var ociSubjectTypes = map[string]string{
	"oci_identity_group":         "group",
	"oci_identity_dynamic_group": "dynamic-group",
}

// ociGrant is a resolved grant: where it applies and the policy it goes
// into (existing, or the name of a new one).
type ociGrant struct {
	compartment *model.Node // nil: in tenancy
	tenancyExpr string      // compartment_id of a new policy when compartment is nil
	policy      *model.Node
	newName     string
	stmts       []model.OCIStatement
}

func (ociGranter) plans(c *Catalog, wf *model.Workflow, a, b *model.Node) []accessPlan {
	identity, resource := a, b
	if ociSubjectTypes[identity.Type] == "" {
		identity, resource = b, a
	}
	if identity.Kind != model.KindResource || resource.Kind != model.KindResource || ociSubjectTypes[identity.Type] == "" || ociGrantTargets[resource.Type] == nil {
		return nil
	}
	bl := newBuilder(c, wf)
	var out []accessPlan
	for _, l := range model.AccessLevels {
		g, ok := ociResolve(bl, identity, resource, l)
		if !ok {
			return nil
		}
		out = append(out, accessPlan{level: l, desc: g.describe(identity, resource), identity: identity, resource: resource})
	}
	return out
}

func (ociGranter) apply(x *conn, p accessPlan) error {
	g, ok := ociResolve(x.b, p.identity, p.resource, p.level)
	if !ok {
		return access.Editf("catalog: %s has no compartment_id to attach a policy to", address(p.resource))
	}
	pol := g.policy
	if pol == nil {
		pol = x.helper(model.KindResource, access.OCIPolicyType, g.newName)
		if g.compartment != nil {
			setAttr(&pol.Body, "compartment_id", expr(ref(g.compartment, "id")))
		} else {
			setAttr(&pol.Body, "compartment_id", expr(g.tenancyExpr))
		}
		setAttr(&pol.Body, "name", x.b.nameValue(access.OCIPolicyType, strings.ReplaceAll(pol.Name, "_", "-")))
		setAttr(&pol.Body, "description", lit(fmt.Sprintf("Access of %s to %s", p.identity.Name, g.where())))
	}
	_, err := access.AppendOCIStatements(&pol.Body, g.stmts)
	return err
}

// ociResolve works out a grant: the resource's compartment (the node its
// compartment_id references, or the resource itself), the statements, and
// the policy attached to that compartment.
func ociResolve(b *builder, identity, resource *model.Node, l model.AccessLevel) (ociGrant, bool) {
	var g ociGrant
	if resource.Type == "oci_identity_compartment" {
		g.compartment = resource
	} else if a := getAttr(&resource.Body, "compartment_id"); a != nil {
		if n, attr := b.resolve(a.Value); n != nil && n.Type == "oci_identity_compartment" && attr == "id" {
			g.compartment = n
		} else {
			g.tenancyExpr = strings.TrimSpace(valueSource(a.Value))
		}
	}
	if g.compartment == nil && g.tenancyExpr == "" {
		if a := getAttr(&identity.Body, "compartment_id"); a != nil {
			g.tenancyExpr = strings.TrimSpace(valueSource(a.Value))
		}
		if g.tenancyExpr == "" {
			return g, false
		}
	}
	subject := ociSubjectTypes[identity.Type]
	for _, s := range ociGrantTargets[resource.Type](l, resource) {
		st := model.OCIStatement{
			Editable: true, SubjectType: subject, Subjects: []string{"${" + ref(identity, "name") + "}"},
			Verb: s.verb, ResourceType: s.resourceType, Location: "tenancy", Condition: s.condition,
		}
		if g.compartment != nil {
			st.Location, st.Compartment = "compartment", "${"+ref(g.compartment, "name")+"}"
		}
		g.stmts = append(g.stmts, st)
	}
	for _, n := range b.all() {
		if n.Kind != model.KindResource || n.Type != access.OCIPolicyType {
			continue
		}
		a := getAttr(&n.Body, "compartment_id")
		if a == nil {
			continue
		}
		src := strings.TrimSpace(valueSource(a.Value))
		attached := g.compartment != nil && referencesNode(src, address(g.compartment)) || g.compartment == nil && src == g.tenancyExpr
		if attached && access.ReadOCIPolicy(&n.Body).Editable {
			g.policy = n
			return g, true
		}
	}
	hint := identity.Name + "_tenancy"
	if g.compartment != nil {
		hint = identity.Name + "_" + g.compartment.Name
	}
	g.newName = b.uniqueName(model.KindResource, access.OCIPolicyType, hint)
	return g, true
}

func (g ociGrant) where() string {
	if g.compartment == nil {
		return "the tenancy"
	}
	return "compartment " + address(g.compartment)
}

// describe is the exact preview of what apply writes.
func (g ociGrant) describe(identity, resource *model.Node) string {
	quoted := make([]string, len(g.stmts))
	for i, st := range g.stmts {
		quoted[i] = `"` + access.OCIStatementText(st) + `"`
	}
	var sb strings.Builder
	if g.policy != nil {
		fmt.Fprintf(&sb, "Adds to %s: %s.", address(g.policy), strings.Join(quoted, ", "))
	} else {
		attached := g.tenancyExpr
		if g.compartment != nil {
			attached = ref(g.compartment, "id")
		}
		fmt.Fprintf(&sb, "Creates oci_identity_policy.%s (compartment_id = %s) with %s.", g.newName, attached, strings.Join(quoted, ", "))
	}
	if g.compartment == nil {
		fmt.Fprintf(&sb, " Warning: %s is not in an oci_identity_compartment of this workflow, so %s gets this in every compartment of the tenancy.", address(resource), identity.Name)
	}
	return sb.String()
}
