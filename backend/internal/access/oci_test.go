package access

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const ociStatements = `[
    # Developers
    "Allow group app-developers to manage instance-family in compartment app-dev",
    "Allow group ${oci_identity_group.dev.name} to read objects in compartment ${oci_identity_compartment.app.name} where target.bucket.name='${oci_objectstorage_bucket.b.name}'",
    "allow dynamic-group 'Default'/'fn-dg', id ocid1.dynamicgroup.oc1..aaa to use keys in compartment id ocid1.compartment.oc1..bbb",
    "Allow group ops to inspect all-resources in compartment shared:network",
    "Endorse group NetworkAdmins to manage remote-peering-to in any-tenancy",
    "Allow group ops to {INSTANCE_READ, INSTANCE_UPDATE} in tenancy",
    local.extra_statement,
    "Allow any-user to read buckets in compartment app-dev where all {request.principal.type='fnfunc', target.bucket.name='b'}",
  ]`

func ociBody(statements string) *model.Body {
	return &model.Body{Attrs: []model.Attr{
		{Name: "compartment_id", Value: model.Value{Expr: "var.tenancy_ocid"}},
		{Name: "statements", Value: model.Value{Expr: statements}},
	}}
}

func TestOCIParse(t *testing.T) {
	pol := ReadOCIPolicy(ociBody(ociStatements))
	if !pol.Editable || len(pol.Statements) != 8 {
		t.Fatalf("policy %+v", pol)
	}
	s := pol.Statements
	if !s[0].Editable || s[0].SubjectType != "group" || !slices.Equal(s[0].Subjects, []string{"app-developers"}) || s[0].Verb != "manage" ||
		s[0].ResourceType != "instance-family" || s[0].Location != "compartment" || s[0].Compartment != "app-dev" || s[0].Condition != "" {
		t.Errorf("0: %+v", s[0])
	}
	if s[0].Source != `"Allow group app-developers to manage instance-family in compartment app-dev"` {
		t.Errorf("0 source %q", s[0].Source)
	}
	if !s[1].Editable || s[1].Subjects[0] != "${oci_identity_group.dev.name}" || s[1].Compartment != "${oci_identity_compartment.app.name}" ||
		s[1].Condition != "target.bucket.name='${oci_objectstorage_bucket.b.name}'" {
		t.Errorf("1: %+v", s[1])
	}
	if !s[2].Editable || s[2].SubjectType != "dynamic-group" || !slices.Equal(s[2].Subjects, []string{"'Default'/'fn-dg'", "id ocid1.dynamicgroup.oc1..aaa"}) ||
		s[2].Compartment != "id ocid1.compartment.oc1..bbb" {
		t.Errorf("2: %+v", s[2])
	}
	if !s[3].Editable || s[3].Compartment != "shared:network" || s[3].Verb != "inspect" || s[3].ResourceType != "all-resources" {
		t.Errorf("3: %+v", s[3])
	}
	for i, why := range map[int]string{4: "Endorse", 5: "Permission lists", 6: "expression"} {
		if s[i].Editable || !strings.Contains(s[i].Reason, why) || s[i].Source == "" {
			t.Errorf("%d: %+v", i, s[i])
		}
	}
	if s[6].Source != "local.extra_statement" {
		t.Errorf("6 source %q", s[6].Source)
	}
	if !s[7].Editable || s[7].SubjectType != "any-user" || len(s[7].Subjects) != 0 || s[7].Condition != "all {request.principal.type='fnfunc', target.bucket.name='b'}" {
		t.Errorf("7: %+v", s[7])
	}
}

func TestOCIUnparseableKeptVerbatim(t *testing.T) {
	for _, src := range []string{
		`"Allow group a to read buckets"`,                      // no location
		`"Allow group a to read buckets in tenancy extra"`,     // trailing words
		`"Allow group a to fly buckets in tenancy"`,            // unknown verb
		`"Allow users a to read buckets in tenancy"`,           // unknown subject type
		`"Allow group a to read buckets in region us-ashburn"`, // unknown location
		`"Define tenancy Acceptor as ocid1.tenancy.oc1..aaa"`,
		`"Allow group %{if true}a%{endif} to read buckets in tenancy"`,
		`"${local.statement}"`,
		`format("Allow group %s to read buckets in tenancy", var.g)`,
		"<<EOT\nAllow group a to read buckets in tenancy\nEOT",
	} {
		st := ParseOCIElement(src)
		if st.Editable || st.Source != src || st.Reason == "" {
			t.Errorf("%s: %+v", src, st)
		}
	}
}

func TestOCIRoundTrip(t *testing.T) {
	b := ociBody(ociStatements)
	pol := ReadOCIPolicy(b)

	// Untouched: the attribute stays exactly as written (comments included).
	if err := WriteOCIPolicy(b, pol.Statements); err != nil {
		t.Fatal(err)
	}
	if got := GetAttr(b, "statements").Value.Expr; got != ociStatements {
		t.Fatalf("unchanged write rewrote the list:\n%s", got)
	}

	// Edit one statement, add one: the others stay byte for byte.
	edited := slices.Clone(pol.Statements)
	edited[0].Verb = "use"
	edited[1].Condition = "target.bucket.name='${oci_objectstorage_bucket.b.name}' AND request.permission='OBJECT_READ'"
	edited = append(edited, model.OCIStatement{
		Editable: true, SubjectType: "group", Subjects: []string{`${var.groups["dev"]}`}, Verb: "read",
		ResourceType: "buckets", Location: "compartment", Compartment: "${oci_identity_compartment.app.name}",
	})
	if err := WriteOCIPolicy(b, edited); err != nil {
		t.Fatal(err)
	}
	got := GetAttr(b, "statements").Value.Expr
	if !strings.Contains(got, "# Developers") {
		t.Errorf("in-place edit lost the comment:\n%s", got)
	}
	for _, keep := range pol.Statements[2:] {
		if !strings.Contains(got, keep.Source) {
			t.Errorf("lost %s:\n%s", keep.Source, got)
		}
	}
	for _, want := range []string{
		`"Allow group app-developers to use instance-family in compartment app-dev"`,
		`"Allow group ${oci_identity_group.dev.name} to read objects in compartment ${oci_identity_compartment.app.name} where target.bucket.name='${oci_objectstorage_bucket.b.name}' AND request.permission='OBJECT_READ'"`,
		`"Allow group ${var.groups["dev"]} to read buckets in compartment ${oci_identity_compartment.app.name}"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s:\n%s", want, got)
		}
	}
	back := ReadOCIPolicy(b)
	if len(back.Statements) != 9 || !back.Statements[8].Editable || back.Statements[8].Subjects[0] != `${var.groups["dev"]}` {
		t.Fatalf("reread %+v", back.Statements)
	}
	if back.Statements[2].Source != pol.Statements[2].Source || back.Statements[6].Source != "local.extra_statement" {
		t.Errorf("sources changed: %q %q", back.Statements[2].Source, back.Statements[6].Source)
	}
	// Reordering keeps sources too.
	rev := slices.Clone(back.Statements)
	slices.Reverse(rev)
	if err := WriteOCIPolicy(b, rev); err != nil {
		t.Fatal(err)
	}
	if again := ReadOCIPolicy(b); again.Statements[0].Source != back.Statements[8].Source || again.Statements[8].Source != back.Statements[0].Source {
		t.Errorf("reordered %+v", again.Statements)
	}
}

func TestOCIQuotesAndEscapes(t *testing.T) {
	src := `"Allow group \"quoted\" to read objects in tenancy where target.bucket.name='${var.names["a b"]}'"`
	st := ParseOCIElement(src)
	if !st.Editable || st.Subjects[0] != `"quoted"` || st.Condition != `target.bucket.name='${var.names["a b"]}'` {
		t.Fatalf("%+v", st)
	}
	st.Verb = "inspect"
	out, err := RenderOCIStatement(st)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"Allow group \"quoted\" to inspect objects in tenancy where target.bucket.name='${var.names["a b"]}'"`; out != want {
		t.Errorf("got %s\nwant %s", out, want)
	}
	// A typed literal ${ is escaped, %{ too.
	if got := QuoteOCITemplate("a ${ b %{x}"); got != `"a $${ b %%{x}"` {
		t.Errorf("escape %s", got)
	}
}

func TestOCIReadOnlyProtected(t *testing.T) {
	b := ociBody(ociStatements)
	pol := ReadOCIPolicy(b)
	var ee *EditError

	// Removing a read-only statement.
	err := WriteOCIPolicy(b, slices.Delete(slices.Clone(pol.Statements), 4, 5))
	if !errors.As(err, &ee) || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("remove read-only: %v", err)
	}
	// Changing one.
	changed := slices.Clone(pol.Statements)
	changed[5].Editable, changed[5].SubjectType, changed[5].Verb = true, "group", "read"
	if err := WriteOCIPolicy(b, changed); !errors.As(err, &ee) {
		t.Errorf("change read-only: %v", err)
	}
	// Inventing one.
	added := append(slices.Clone(pol.Statements), model.OCIStatement{Source: "local.other"})
	if err := WriteOCIPolicy(b, added); !errors.As(err, &ee) {
		t.Errorf("add read-only: %v", err)
	}
	// Invalid statements.
	for _, bad := range []model.OCIStatement{
		{Editable: true, SubjectType: "group", Verb: "read", ResourceType: "buckets", Location: "tenancy"},
		{Editable: true, SubjectType: "group", Subjects: []string{"a"}, Verb: "own", ResourceType: "buckets", Location: "tenancy"},
		{Editable: true, SubjectType: "group", Subjects: []string{"a"}, Verb: "read", ResourceType: "buckets", Location: "compartment"},
		{Editable: true, SubjectType: "group", Subjects: []string{"a to b"}, Verb: "read", ResourceType: "buckets", Location: "tenancy"},
		{Editable: true, SubjectType: "any-user", Subjects: []string{"a"}, Verb: "read", ResourceType: "buckets", Location: "tenancy"},
	} {
		if err := WriteOCIPolicy(b, append(slices.Clone(pol.Statements), bad)); !errors.As(err, &ee) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	if GetAttr(b, "statements").Value.Expr != ociStatements {
		t.Error("a refused edit changed the policy")
	}

	// A policy whose statements come from elsewhere is read-only as a whole.
	ref := ociBody("concat(local.base, var.extra)")
	rp := ReadOCIPolicy(ref)
	if rp.Editable || len(rp.Statements) != 1 || rp.Statements[0].Source != "concat(local.base, var.extra)" {
		t.Fatalf("%+v", rp)
	}
	if err := WriteOCIPolicy(ref, nil); !errors.As(err, &ee) {
		t.Errorf("write read-only policy: %v", err)
	}
	if _, err := AppendOCIStatements(ref, []model.OCIStatement{{Editable: true, SubjectType: "any-group", Verb: "read", ResourceType: "buckets", Location: "tenancy"}}); !errors.As(err, &ee) {
		t.Errorf("append to read-only policy: %v", err)
	}
}

func TestOCIAppendCreatesAndDedupes(t *testing.T) {
	b := &model.Body{}
	st := model.OCIStatement{Editable: true, SubjectType: "group", Subjects: []string{"${oci_identity_group.g.name}"}, Verb: "read", ResourceType: "buckets", Location: "tenancy"}
	added, err := AppendOCIStatements(b, []model.OCIStatement{st})
	if err != nil || !added {
		t.Fatal(added, err)
	}
	if got := GetAttr(b, "statements").Value.Expr; got != "[\n  \"Allow group ${oci_identity_group.g.name} to read buckets in tenancy\",\n]" {
		t.Errorf("created %s", got)
	}
	if added, err := AppendOCIStatements(b, []model.OCIStatement{st}); err != nil || added {
		t.Errorf("duplicate appended: %v %v", added, err)
	}
}

func TestOCIWarnings(t *testing.T) {
	codes := func(stmts ...string) []string {
		pol := ReadOCIPolicy(ociBody("[" + strings.Join(stmts, ", ") + "]"))
		var out []string
		for _, w := range OCIWarnings(pol) {
			out = append(out, w.Severity+":"+w.Code+"@"+w.Path)
		}
		return out
	}
	if got := codes(`"Allow group app-developers to manage all-resources in tenancy"`); !slices.Equal(got, []string{"warning:oci_manage_all@statements/0", "warning:oci_tenancy_scope@statements/0"}) {
		t.Errorf("admin grant: %v", got)
	}
	if got := codes(`"Allow group Administrators to manage all-resources in tenancy"`); !slices.Equal(got, []string{"warning:oci_manage_all@statements/0"}) {
		t.Errorf("administrators: %v", got)
	}
	if got := codes(`"Allow any-user to read objects in compartment a"`, `"Allow any-user to read objects in compartment a where request.principal.type='fnfunc'"`); !slices.Equal(got, []string{"warning:oci_any_user@statements/0"}) {
		t.Errorf("any-user: %v", got)
	}
	if got := codes(`"Allow group dev to manage virtual-network-family in compartment app"`, `"Allow group dev to use virtual-network-family in compartment app"`); !slices.Equal(got, []string{"info:oci_manage_verb@statements/0"}) {
		t.Errorf("manage verb: %v", got)
	}
	if got := codes(`"Allow group dev to manage instance-family in compartment app"`, "local.x"); len(got) != 0 {
		t.Errorf("least privilege: %v", got)
	}
}

func TestOCIProviderAndCatalog(t *testing.T) {
	n := &model.Node{Kind: model.KindResource, Type: OCIPolicyType, Name: "p", Body: *ociBody(`["Allow group a to manage all-resources in tenancy"]`)}
	wf := &model.Workflow{Nodes: []model.Node{*n}}
	v, err := View(wf, &wf.Nodes[0])
	if err != nil || v.Provider != "oci" || v.OCI == nil || len(v.Warnings) != 2 {
		t.Fatalf("%+v %v", v, err)
	}
	st := v.OCI.Statements[0]
	st.ResourceType, st.Location, st.Compartment = "instance-family", "compartment", "app-dev"
	if err := Apply(wf, &wf.Nodes[0], model.AccessEdit{NodeID: n.ID, OCI: &model.OCIPolicy{Statements: []model.OCIStatement{st}}}); err != nil {
		t.Fatal(err)
	}
	if got := GetAttr(&wf.Nodes[0].Body, "statements").Value.Expr; !strings.Contains(got, `"Allow group a to manage instance-family in compartment app-dev"`) {
		t.Errorf("applied %s", got)
	}
	if err := Apply(wf, &wf.Nodes[0], model.AccessEdit{NodeID: n.ID}); err == nil {
		t.Error("edit without oci accepted")
	}

	cat, err := Catalog("oci", CatalogQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if cat.Source.URL == "" || cat.Source.Retrieved == "" || len(cat.Verbs) != 4 || cat.Verbs[0].Name != "inspect" || cat.Verbs[3].Name != "manage" {
		t.Fatalf("catalog %+v", cat.Source)
	}
	names := map[string]bool{}
	for _, rt := range cat.ResourceTypes {
		names[rt.Name] = true
	}
	for _, want := range []string{"all-resources", "virtual-network-family", "instance-family", "volume-family", "object-family", "buckets", "objects",
		"database-family", "autonomous-database-family", "cluster-family", "vaults", "keys", "secret-family", "file-family", "functions-family"} {
		if !names[want] {
			t.Errorf("catalog lacks %s", want)
		}
	}
	if known, family := OCIResourceType("object-family"); !known || !family {
		t.Error("object-family is a family")
	}
	if known, family := OCIResourceType("buckets"); !known || family {
		t.Error("buckets is an individual type")
	}
	found, err := Catalog("oci", CatalogQuery{Search: "bucket"})
	if err != nil || len(found.ResourceTypes) != 1 || found.ResourceTypes[0].Name != "buckets" {
		t.Errorf("search %+v %v", found.ResourceTypes, err)
	}
}
