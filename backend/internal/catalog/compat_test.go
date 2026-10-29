package catalog

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The table covers every embedded provider with the index's source and
// version; Recommended follows the rule and avoids every known issue.
func TestProviderCompatTable(t *testing.T) {
	c := mustLoad(t)
	pcs := c.ProviderCompats()
	if len(pcs) != len(c.providers) || len(pcs) != len(providerMeta) {
		t.Fatalf("%d entries for %d providers", len(pcs), len(c.providers))
	}
	for i, ip := range c.providers {
		p, pc := ip.info, pcs[i]
		if pc.Name != p.Name || pc.Source != p.Source || pc.Catalog != p.Version {
			t.Errorf("entry %d = %s %s %s, want %s %s %s", i, pc.Name, pc.Source, pc.Catalog, p.Name, p.Source, p.Version)
		}
		got, ok := c.ProviderCompat(p.Name)
		if !ok || got.Recommended != pc.Recommended {
			t.Errorf("ProviderCompat(%s) = %+v, %v", p.Name, got, ok)
		}
		s := version.Must(version.NewVersion(p.Version)).Segments()
		if want := fmt.Sprintf("~> %d.%d", s[0], s[1]); pc.Recommended != want {
			t.Errorf("%s recommended %q, want %q", p.Name, pc.Recommended, want)
		}
		if version.Must(version.NewVersion(pc.LocalMinimum)).GreaterThan(version.Must(version.NewVersion(pc.Catalog))) {
			t.Errorf("%s local minimum %s is newer than the catalog's %s", p.Name, pc.LocalMinimum, pc.Catalog)
		}
		var all []string // services of the issues the recommended constraint avoids
		for _, is := range pc.Issues {
			if !pc.AffectsCatalog(is) {
				all = append(all, is.Services...)
			} else if len(is.Services) == 0 {
				t.Errorf("%s issue %s breaks every resource of the catalog release", p.Name, is.Versions)
			}
			if is.Message == "" || is.Fix == "" {
				t.Errorf("%s issue %s lacks message or fix", p.Name, is.Versions)
			}
			for _, s := range is.Services {
				if !strings.HasPrefix(s, p.Name+"_") {
					t.Errorf("%s issue %s: service %q is not one of its resource types", p.Name, is.Versions, s)
				}
			}
		}
		if cc := pc.CheckConstraint(pc.Recommended, all); !cc.OK() {
			t.Errorf("%s: recommended %q has problems %+v", p.Name, pc.Recommended, cc)
		}
		if vc := pc.CheckVersion(pc.Catalog, all); vc.BelowMinimum || vc.OtherMajor || len(vc.Issues) > 0 {
			t.Errorf("%s: catalog release %s has problems %+v", p.Name, pc.Catalog, vc)
		}
	}
	if _, ok := c.ProviderCompat("google-beta"); ok {
		t.Error("google-beta is not embedded")
	}
	// Callers get copies.
	g, _ := c.ProviderCompat("google")
	g.Issues[0].Message = "changed"
	if g2, _ := c.ProviderCompat("google"); g2.Issues[0].Message == "changed" {
		t.Error("ProviderCompat shares its issues")
	}
}

func TestParseCompatRejects(t *testing.T) {
	c := mustLoad(t)
	rest := `{"name": "azurerm", "localMinimum": "4.0.0"}, {"name": "google", "localMinimum": "7.0.0"}, {"name": "oci", "localMinimum": "9.0.0"},
	  {"name": "archive", "localMinimum": "2.0.0"}, {"name": "random", "localMinimum": "3.0.0"}`
	if _, err := parseCompat(c, []byte(`{"providers": [{"name": "aws", "localMinimum": "6.0.0"}, `+rest+`]}`)); err != nil {
		t.Fatalf("valid table: %v", err)
	}
	for name, aws := range map[string]string{
		"missing provider": ``,
		"unknown provider": `{"name": "awss", "localMinimum": "6.0.0"}, `,
		"duplicate":        `{"name": "aws", "localMinimum": "6.0.0"}, {"name": "aws", "localMinimum": "6.0.0"}, `,
		"bad minimum":      `{"name": "aws", "localMinimum": "six"}, `,
		"bad issue range":  `{"name": "aws", "localMinimum": "6.0.0", "issues": [{"versions": "<<6", "message": "m", "fix": "f"}]}, `,
	} {
		if _, err := parseCompat(c, []byte(`{"providers": [`+aws+rest+`]}`)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestCheckConstraint(t *testing.T) {
	g, _ := mustLoad(t).ProviderCompat("google")
	sql := []string{"google_sql_database_instance", "google_sql_database"}
	bucket := []string{"google_storage_bucket"}
	for _, tc := range []struct {
		constraint string
		types      []string
		want       ConstraintCheck // Issues compared by count
		issues     int
	}{
		{"~> 7.46", sql, ConstraintCheck{}, 0},
		{">= 7.26", sql, ConstraintCheck{}, 0},
		{">= 7.26.0, < 8.0.0", sql, ConstraintCheck{}, 0},
		{"~> 7.0", sql, ConstraintCheck{BelowMinimum: true}, 1},
		{"~> 7.0", bucket, ConstraintCheck{BelowMinimum: true}, 0},
		{"~> 7.25", sql, ConstraintCheck{BelowMinimum: true}, 1},
		{"~> 7.25.0", sql, ConstraintCheck{BelowMinimum: true, Broken: true}, 1},
		{"= 7.25.1", sql, ConstraintCheck{BelowMinimum: true, Broken: true}, 1},
		{"~> 6.0", sql, ConstraintCheck{OtherMajor: true, BelowMinimum: true, Broken: true}, 1},
		{"~> 6.0", bucket, ConstraintCheck{OtherMajor: true, BelowMinimum: true, Broken: true}, 0},
		{">= 7.26.0, < 7.26.1", sql, ConstraintCheck{}, 0},
		{"> 7.25.99, < 7.26.1", sql, ConstraintCheck{BelowMinimum: true}, 1},                        // 7.25.100 exists in principle
		{"!= 7.26.0, >= 7.26.0, < 7.26.1", sql, ConstraintCheck{OtherMajor: true, Broken: true}, 0}, // allows nothing
		{"latest", sql, ConstraintCheck{Invalid: true}, 0},
	} {
		got := g.CheckConstraint(tc.constraint, tc.types)
		issues := len(got.Issues)
		got.Issues = nil
		if !reflect.DeepEqual(got, tc.want) || issues != tc.issues {
			t.Errorf("%q %v = %+v with %d issues, want %+v with %d", tc.constraint, tc.types, got, issues, tc.want, tc.issues)
		}
	}
}

func TestCheckVersionAndStatus(t *testing.T) {
	g, _ := mustLoad(t).ProviderCompat("google")
	sql := []string{"google_sql_database"}
	if vc := g.CheckVersion("6.50.0", sql); !vc.BelowMinimum || !vc.OtherMajor || len(vc.Issues) != 1 {
		t.Errorf("6.50.0 = %+v", vc)
	}
	if vc := g.CheckVersion("6.50.0", []string{"google_sql_database_instance"}); len(vc.Issues) != 0 {
		t.Errorf("instances work on 6.50.0: %+v", vc)
	}
	if vc := g.CheckVersion("7.26.0", sql); vc.BelowMinimum || vc.OtherMajor || len(vc.Issues) != 0 {
		t.Errorf("7.26.0 = %+v", vc)
	}
	if vc := g.CheckVersion("8.9.0", sql); vc.BelowMinimum || !vc.OtherMajor || len(vc.Issues) != 0 {
		t.Errorf("8.9.0 = %+v", vc)
	}
	if vc := g.CheckVersion("nope", sql); !vc.Invalid {
		t.Errorf("invalid version = %+v", vc)
	}
	for _, tc := range []struct {
		constraint, locked string
		types              []string
		want               string
	}{
		{"~> 7.46", "7.46.1", sql, StatusOK},
		{"~> 7.46", "", sql, StatusOK},
		{"~> 6.0", "6.50.0", sql, StatusBrokenLocally},
		{"~> 6.0", "6.50.0", []string{"google_storage_bucket"}, StatusOutdated},
		{"~> 6.0", "", sql, StatusBrokenLocally},
		{"= 7.20.0", "", sql, StatusBrokenLocally},
		{"~> 7.0", "7.40.0", sql, StatusOutdated}, // a fresh lock is fine, the constraint isn't
		{"~> 7.0", "7.10.0", sql, StatusBrokenLocally},
		{"~> 7.0", "", []string{"google_storage_bucket"}, StatusOutdated},
		{"", "", nil, StatusOutdated},
		{">= 7.26", "8.9.0", sql, StatusOutdated},
	} {
		if got := g.Status(tc.constraint, tc.locked, tc.types); got != tc.want {
			t.Errorf("Status(%q, %q, %v) = %s, want %s", tc.constraint, tc.locked, tc.types, got, tc.want)
		}
	}
}

func TestIssueAffects(t *testing.T) {
	is := CompatIssue{Services: []string{"oci_functions_*", "oci_queue_queue"}}
	for types, want := range map[string]bool{
		"oci_functions_function":   true,
		"oci_functions_":           true,
		"oci_queue_queue":          true,
		"oci_queue_queue_x":        false,
		"oci_objectstorage_bucket": false,
	} {
		if got := is.Affects([]string{types}); got != want {
			t.Errorf("Affects(%s) = %v", types, got)
		}
	}
	if !(CompatIssue{}).Affects(nil) {
		t.Error("an issue without services affects every resource")
	}
}

func TestRequiredProvidersAndVersions(t *testing.T) {
	c := mustLoad(t)
	wf, _, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(`terraform {
  required_providers {
    google  = { source = "hashicorp/google", version = "~> 6.0" }
    aws     = "~> 6.69"
    random  = { source = "registry.terraform.io/hashicorp/random" }
    beta    = { source = "hashicorp/google-beta", version = "~> 7.0" }
    mygoogle = { source = "acme/google", version = "~> 1.0" }
  }
}
resource "google_sql_database" "d" {}
`)})
	if err != nil {
		t.Fatal(err)
	}
	rps := RequiredProviders(wf)
	want := []RequiredProvider{
		{Local: "google", Source: "hashicorp/google", Type: "google", Constraint: "~> 6.0"},
		{Local: "aws", Type: "aws", Constraint: "~> 6.69"},
		{Local: "random", Source: "registry.terraform.io/hashicorp/random", Type: "random"},
		{Local: "beta", Source: "hashicorp/google-beta", Type: "google-beta", Constraint: "~> 7.0"},
		{Local: "mygoogle", Source: "acme/google", Type: "google", Constraint: "~> 1.0"},
	}
	if !slices.Equal(rps, want) {
		t.Fatalf("RequiredProviders = %+v", rps)
	}
	if _, ok := c.CompatFor(rps[4]); ok {
		t.Error("acme/google matched hashicorp/google")
	}
	if _, ok := c.CompatFor(rps[2]); !ok {
		t.Error("registry address not matched")
	}
	got := c.ProviderVersions(wf, map[string]string{"google": "6.50.0", "aws": "6.69.1"})
	aws, _ := c.ProviderCompat("aws")
	random, _ := c.ProviderCompat("random")
	google, _ := c.ProviderCompat("google")
	wantPV := []model.ProviderVersion{
		{Name: "google", Constraint: "~> 6.0", Locked: "6.50.0", Recommended: google.Recommended, Status: StatusBrokenLocally},
		{Name: "aws", Constraint: "~> 6.69", Locked: "6.69.1", Recommended: aws.Recommended, Status: StatusOK},
		{Name: "random", Recommended: random.Recommended, Status: StatusOutdated},
	}
	if !slices.Equal(got, wantPV) {
		t.Errorf("ProviderVersions = %+v", got)
	}
	if ResourceTypes(wf)[0] != "google_sql_database" {
		t.Errorf("ResourceTypes = %v", ResourceTypes(wf))
	}
}

// add_node writes the recommended constraint.
func TestAddNodeWritesRecommendedConstraint(t *testing.T) {
	c := mustLoad(t)
	for _, typ := range []string{"aws_sqs_queue", "azurerm_resource_group", "google_storage_bucket", "oci_objectstorage_bucket", "random_id"} {
		wf := &model.Workflow{}
		if _, err := c.AddNode(wf, model.KindResource, typ, "", model.Position{}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		for _, rp := range RequiredProviders(wf) {
			pc, ok := c.CompatFor(rp)
			if !ok {
				t.Errorf("%s: %s not embedded", typ, rp.Local)
				continue
			}
			if rp.Constraint != pc.Recommended || rp.Source != pc.Source {
				t.Errorf("%s: required %s = %+v, want %s %s", typ, rp.Local, rp, pc.Source, pc.Recommended)
			}
		}
	}
}

// Every template requires the providers it uses with a constraint the
// catalog release satisfies and that allows no unverified release and no
// known-broken one except emulator gaps containing the catalog release.
func TestTemplateProviderConstraints(t *testing.T) {
	c := mustLoad(t)
	for _, tpl := range c.Templates() {
		files, _ := c.TemplateFiles(tpl.ID)
		wf, _, err := hclcodec.Import(tpl.ID, files)
		if err != nil {
			t.Fatalf("%s: %v", tpl.ID, err)
		}
		var all []string // the constraints avoid every known issue, used or not, but emulator gaps
		for _, rp := range RequiredProviders(wf) {
			if pc, ok := c.CompatFor(rp); ok {
				for _, is := range pc.Issues {
					if !pc.AffectsCatalog(is) {
						all = append(all, is.Services...)
					}
				}
			}
		}
		seen := 0
		for _, rp := range RequiredProviders(wf) {
			pc, ok := c.CompatFor(rp)
			if !ok {
				continue
			}
			seen++
			cs, err := version.NewConstraint(rp.Constraint)
			if err != nil {
				t.Errorf("%s: %s constraint %q: %v", tpl.ID, rp.Local, rp.Constraint, err)
				continue
			}
			if !cs.Check(version.Must(version.NewVersion(pc.Catalog))) {
				t.Errorf("%s: %s %q excludes the catalog release %s", tpl.ID, rp.Local, rp.Constraint, pc.Catalog)
			}
			if cc := pc.CheckConstraint(rp.Constraint, all); !cc.OK() {
				t.Errorf("%s: %s %q: %+v", tpl.ID, rp.Local, rp.Constraint, cc)
			}
		}
		if seen == 0 {
			t.Errorf("%s requires no embedded provider", tpl.ID)
		}
	}
}

// oracle/oci's functions gap contains the catalog release: the status says
// so, while a constraint can't avoid it (WithoutGaps).
func TestEmulatorGap(t *testing.T) {
	c := mustLoad(t)
	oci, _ := c.ProviderCompat("oci")
	fn := []string{"oci_functions_application", "oci_functions_function"}
	if len(oci.Issues) != 1 || !oci.AffectsCatalog(oci.Issues[0]) {
		t.Fatalf("oci issues = %+v", oci.Issues)
	}
	if got := oci.Status(oci.Recommended, oci.Catalog, fn); got != StatusBrokenLocally {
		t.Errorf("functions on the catalog release = %s", got)
	}
	if got := oci.Status(oci.Recommended, oci.Catalog, []string{"oci_objectstorage_bucket"}); got != StatusOK {
		t.Errorf("buckets on the catalog release = %s", got)
	}
	if got := oci.Status("= 9.2.0", "9.2.0", fn); got != StatusOK {
		t.Errorf("functions on 9.2.0 = %s", got)
	}
	if cc := oci.WithoutGaps().CheckConstraint(oci.Recommended, fn); !cc.OK() {
		t.Errorf("without gaps = %+v", cc)
	}
	if len(oci.Issues) != 1 {
		t.Error("WithoutGaps changed its receiver")
	}
	google, _ := c.ProviderCompat("google")
	if len(google.WithoutGaps().Issues) != 1 || google.AffectsCatalog(google.Issues[0]) {
		t.Error("google's < 7.26 issue is avoidable")
	}
}

// Types inside an emulator gap of the catalog release are not local.
func TestEmulatorGapLocalSupport(t *testing.T) {
	c := mustLoad(t)
	for typ, want := range map[string]bool{"oci_functions_function": false, "oci_functions_application": false, "oci_objectstorage_bucket": true} {
		s, ok := c.Schema(model.KindResource, typ)
		if !ok || s.FlociSupported != want {
			t.Errorf("%s floci supported = %v, want %v", typ, s.FlociSupported, want)
		}
	}
	wf := &model.Workflow{Nodes: []model.Node{{ID: "n1", Kind: model.KindResource, Type: "oci_functions_application", Name: "app"}}}
	var found bool
	for _, d := range c.Validate(wf) {
		if d.Code == CodeFlociUnsupported {
			found = true
			if !strings.Contains(d.Message, "provider release Nimbo targets") || !strings.Contains(d.Detail, "20260325") || !strings.Contains(d.Detail, `"9.2.0"`) {
				t.Errorf("diagnostic = %+v", d)
			}
		}
	}
	if !found {
		t.Error("no floci_unsupported for an oci functions application")
	}
}
