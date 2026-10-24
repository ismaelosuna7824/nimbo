package catalog

import (
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const ociGrantTF = `
variable "tenancy_ocid" {
  type = string
}

resource "oci_identity_compartment" "app" {
  compartment_id = var.tenancy_ocid
  name           = "app-dev"
  description    = "App"
}

resource "oci_identity_group" "dev" {
  compartment_id = var.tenancy_ocid
  name           = "app-developers"
  description    = "Developers"
}

resource "oci_identity_dynamic_group" "fn" {
  compartment_id = var.tenancy_ocid
  name           = "functions"
  description    = "Functions"
  matching_rule  = "ALL {resource.type = 'fnfunc'}"
}

resource "oci_objectstorage_bucket" "invoices" {
  compartment_id = oci_identity_compartment.app.id
  name           = "invoices"
  namespace      = "ns"
}

resource "oci_objectstorage_bucket" "shared" {
  compartment_id = var.tenancy_ocid
  name           = "shared"
  namespace      = "ns"
}

resource "oci_kms_vault" "v" {
  compartment_id = oci_identity_compartment.app.id
  display_name   = "v"
  vault_type     = "DEFAULT"
}

resource "oci_kms_key" "k" {
  compartment_id      = oci_identity_compartment.app.id
  display_name        = "k"
  management_endpoint = oci_kms_vault.v.management_endpoint
  key_shape {
    algorithm = "AES"
    length    = 32
  }
}

resource "oci_identity_policy" "app" {
  compartment_id = oci_identity_compartment.app.id
  name           = "app"
  description    = "App policies"
  statements = [
    # kept as written
    "Allow group ops to inspect all-resources in compartment app-dev",
    local.more,
  ]
}

locals {
  more = "Allow group ops to read buckets in compartment app-dev"
}
`

func ociGrantWorkflow(t *testing.T) *model.Workflow {
	t.Helper()
	wf, _, err := hclcodec.Import("oci", map[string][]byte{"main.tf": []byte(ociGrantTF)})
	if err != nil {
		t.Fatal(err)
	}
	return wf
}

func accessRecipes(c *Catalog, wf *model.Workflow, a, b *model.Node) []model.ConnectionRecipe {
	var out []model.ConnectionRecipe
	for _, r := range c.Connections(wf, a.ID, b.ID) {
		if r.Group == model.AccessRecipeGroup {
			out = append(out, r)
		}
	}
	return out
}

func TestOCIGrantBucketReadIntoExistingPolicy(t *testing.T) {
	c := mustLoad(t)
	wf := ociGrantWorkflow(t)
	dev, bucket := nodeByAddr(t, wf, "oci_identity_group.dev"), nodeByAddr(t, wf, "oci_objectstorage_bucket.invoices")
	before := attrExpr(t, nodeByAddr(t, wf, "oci_identity_policy.app"), "statements")

	rs := c.Connections(wf, dev.ID, bucket.ID)
	if len(rs) < 3 || rs[0].ID != "access:read" || rs[1].ID != "access:write" || rs[2].ID != "access:manage" || rs[0].Group != model.AccessRecipeGroup {
		t.Fatalf("recipes %+v", rs)
	}
	read := `"Allow group ${oci_identity_group.dev.name} to read buckets in compartment ${oci_identity_compartment.app.name}"`
	objects := `"Allow group ${oci_identity_group.dev.name} to read objects in compartment ${oci_identity_compartment.app.name} where target.bucket.name='${oci_objectstorage_bucket.invoices.name}'"`
	if want := "Adds to oci_identity_policy.app: " + read + ", " + objects + "."; rs[0].Description != want {
		t.Errorf("read desc:\n%s\nwant\n%s", rs[0].Description, want)
	}
	if !strings.Contains(rs[1].Description, "to manage objects in compartment ${oci_identity_compartment.app.name} where all {target.bucket.name='${oci_objectstorage_bucket.invoices.name}', any {request.permission='OBJECT_INSPECT', request.permission='OBJECT_READ', request.permission='OBJECT_CREATE', request.permission='OBJECT_OVERWRITE'}}") {
		t.Errorf("write desc %s", rs[1].Description)
	}
	if !strings.Contains(rs[2].Description, "to manage object-family in compartment ${oci_identity_compartment.app.name} where target.bucket.name='${oci_objectstorage_bucket.invoices.name}'") {
		t.Errorf("manage desc %s", rs[2].Description)
	}
	// The bucket dragged onto the group offers the same.
	if back := accessRecipes(c, wf, bucket, dev); len(back) != 3 || back[0].Description != rs[0].Description {
		t.Errorf("reverse %+v", back)
	}

	n := len(wf.Nodes)
	connect(t, c, wf, dev, bucket, "access:read")
	if len(wf.Nodes) != n {
		t.Errorf("a node was added: %d -> %d", n, len(wf.Nodes))
	}
	got := attrExpr(t, nodeByAddr(t, wf, "oci_identity_policy.app"), "statements")
	for _, keep := range []string{"# kept as written\n", `"Allow group ops to inspect all-resources in compartment app-dev"`, "local.more,\n    \"Allow group"} {
		if !strings.Contains(got, keep) {
			t.Errorf("lost %s:\n%s", keep, got)
		}
	}
	if !strings.Contains(got, read) || !strings.Contains(got, objects) {
		t.Errorf("statements:\n%s\n(before:\n%s)", got, before)
	}
	pol := access.ReadOCIPolicy(&nodeByAddr(t, wf, "oci_identity_policy.app").Body)
	if len(pol.Statements) != 4 || pol.Statements[1].Editable || !pol.Statements[3].Editable {
		t.Errorf("policy %+v", pol.Statements)
	}
	// Granting again adds nothing.
	connect(t, c, wf, dev, bucket, "access:read")
	if again := attrExpr(t, nodeByAddr(t, wf, "oci_identity_policy.app"), "statements"); again != got {
		t.Errorf("second grant changed the policy:\n%s", again)
	}
}

func TestOCIGrantCreatesPolicyInTenancy(t *testing.T) {
	c := mustLoad(t)
	wf := ociGrantWorkflow(t)
	dev, shared := nodeByAddr(t, wf, "oci_identity_group.dev"), nodeByAddr(t, wf, "oci_objectstorage_bucket.shared")
	rs := accessRecipes(c, wf, dev, shared)
	if len(rs) != 3 {
		t.Fatalf("recipes %+v", rs)
	}
	if !strings.HasPrefix(rs[2].Description, "Creates oci_identity_policy.dev_tenancy (compartment_id = var.tenancy_ocid) with \"Allow group ${oci_identity_group.dev.name} to manage object-family in tenancy where target.bucket.name='${oci_objectstorage_bucket.shared.name}'\".") ||
		!strings.Contains(rs[2].Description, "Warning:") {
		t.Errorf("desc %s", rs[2].Description)
	}
	connect(t, c, wf, dev, shared, "access:manage")
	p := nodeByAddr(t, wf, "oci_identity_policy.dev_tenancy")
	if got := attrExpr(t, p, "compartment_id"); got != "var.tenancy_ocid" {
		t.Errorf("compartment_id %s", got)
	}
	for _, a := range []string{"name", "description"} {
		attrExpr(t, p, a)
	}
	pol := access.ReadOCIPolicy(&p.Body)
	if len(pol.Statements) != 1 || pol.Statements[0].Location != "tenancy" || pol.Statements[0].Verb != "manage" {
		t.Errorf("policy %+v", pol)
	}
	view, err := access.View(wf, p)
	if err != nil || !slices.ContainsFunc(view.Warnings, func(w model.AccessWarning) bool { return w.Code == "oci_tenancy_scope" }) {
		t.Errorf("warnings %+v %v", view, err)
	}
}

func TestOCIGrantNewPolicyInCompartment(t *testing.T) {
	c := mustLoad(t)
	wf := ociGrantWorkflow(t)
	// Without the existing policy, a new one is attached to the compartment.
	wf.Nodes = slices.DeleteFunc(wf.Nodes, func(n model.Node) bool { return n.Type == "oci_identity_policy" })
	fn, key := nodeByAddr(t, wf, "oci_identity_dynamic_group.fn"), nodeByAddr(t, wf, "oci_kms_key.k")
	connect(t, c, wf, key, fn, "access:write")
	p := nodeByAddr(t, wf, "oci_identity_policy.fn_app")
	if got := attrExpr(t, p, "compartment_id"); got != "oci_identity_compartment.app.id" {
		t.Errorf("compartment_id %s", got)
	}
	want := `"Allow dynamic-group ${oci_identity_dynamic_group.fn.name} to use keys in compartment ${oci_identity_compartment.app.name} where target.key.id='${oci_kms_key.k.id}'"`
	if got := attrExpr(t, p, "statements"); !strings.Contains(got, want) {
		t.Errorf("statements %s", got)
	}
}

func TestOCIGrantTargetsArePinned(t *testing.T) {
	g := &model.Node{Kind: model.KindResource, Type: "oci_identity_group", Name: "g"}
	for typ, fn := range ociGrantTargets {
		for _, l := range model.AccessLevels {
			for _, s := range fn(l, &model.Node{Kind: model.KindResource, Type: typ, Name: "r"}) {
				if known, _ := access.OCIResourceType(s.resourceType); !known {
					t.Errorf("%s: %s is not in the pinned policy reference", typ, s.resourceType)
				}
				st := model.OCIStatement{Editable: true, SubjectType: "group", Subjects: []string{"${" + ref(g, "name") + "}"}, Verb: s.verb, ResourceType: s.resourceType, Location: "tenancy", Condition: s.condition}
				if _, err := access.RenderOCIStatement(st); err != nil {
					t.Errorf("%s %s: %v", typ, l, err)
				}
			}
		}
	}
	// Not an identity/resource pair: no grant.
	c := mustLoad(t)
	wf := ociGrantWorkflow(t)
	if rs := accessRecipes(c, wf, nodeByAddr(t, wf, "oci_kms_vault.v"), nodeByAddr(t, wf, "oci_kms_key.k")); len(rs) != 0 {
		t.Errorf("vault -> key: %+v", rs)
	}
}
