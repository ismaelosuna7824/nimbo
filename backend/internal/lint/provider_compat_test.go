package lint

import (
	"fmt"
	"strings"
	"testing"
)

// googleConstraint is a terraform block requiring hashicorp/google at
// constraint ("" = no version).
func googleConstraint(constraint string) string {
	version := ""
	if constraint != "" {
		version = fmt.Sprintf(", version = %q", constraint)
	}
	return fmt.Sprintf(`terraform {
  required_version = ">= 1.6"
  required_providers {
    google = { source = "hashicorp/google"%s }
  }
}
`, version)
}

// pinned is what Nimbo writes, so the other rules' cases stay quiet.
func TestPinnedIsRecommended(t *testing.T) {
	for _, name := range []string{"aws", "azurerm", "google"} {
		pc, _ := testCatalog.ProviderCompat(name)
		if !strings.Contains(pinned, fmt.Sprintf("%s = { source = %q, version = %q }", padded(name), pc.Source, pc.Recommended)) {
			t.Errorf("pinned lacks %s %s", name, pc.Recommended)
		}
	}
}

func padded(name string) string { return name + strings.Repeat(" ", len("azurerm")-len(name)) }

func TestProviderLocalCompatMessages(t *testing.T) {
	google, _ := testCatalog.ProviderCompat("google")
	fixWant := fmt.Sprintf("version = %q", google.Recommended)

	// The incident: a previous major pinned from memory, with Cloud SQL.
	ds := findings(t, googleConstraint("~> 6.0")+`resource "google_sql_database" "d" {}`, "bp_provider_local_compat")
	if len(ds) != 1 {
		t.Fatalf("findings = %+v", ds)
	}
	d := ds[0]
	if !strings.Contains(d.Message, `hashicorp/google version "~> 6.0" can't select a 7.x release`) || !strings.Contains(d.Message, google.Catalog) {
		t.Errorf("message = %q", d.Message)
	}
	if !strings.Contains(d.Message, "known to break google_sql_database on the local emulators") {
		t.Errorf("message lacks the issue: %q", d.Message)
	}
	for _, want := range []string{"Cloud SQL databases", "(< 7.26.0)", "terraform init -upgrade", google.Catalog} {
		if !strings.Contains(d.Detail, want) {
			t.Errorf("detail lacks %q: %s", want, d.Detail)
		}
	}
	if !strings.Contains(d.Fix, fixWant) || !strings.HasPrefix(d.Fix, "Require hashicorp/google 7.26 or newer") {
		t.Errorf("fix = %q", d.Fix)
	}

	// Within the major but able to keep a release that breaks a resource.
	ds = findings(t, googleConstraint("~> 7.0")+`resource "google_sql_database" "d" {}`, "bp_provider_local_compat")
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "allows releases known to break google_sql_database") || !strings.Contains(ds[0].Detail, "< 7.26.0") {
		t.Fatalf("findings = %+v", ds)
	}
	ds = findings(t, googleConstraint("= 7.20.0")+`resource "google_sql_database" "d" {}`, "bp_provider_local_compat")
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "only allows releases known to break google_sql_database") {
		t.Fatalf("findings = %+v", ds)
	}
	// Without the affected resource only the verified minimum matters.
	ds = findings(t, googleConstraint("~> 7.0")+`resource "google_storage_bucket" "b" {}`, "bp_provider_local_compat")
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "older than "+google.LocalMinimum) || strings.Contains(ds[0].Detail, "Cloud SQL") || !strings.Contains(ds[0].Fix, fixWant) {
		t.Fatalf("findings = %+v", ds)
	}

	// bp_provider_version suggests the recommended constraint too.
	ds = findings(t, `terraform { required_version = ">= 1.6" }
resource "google_storage_bucket" "b" {}`, "bp_provider_version")
	if len(ds) != 1 || !strings.Contains(ds[0].Fix, fixWant) || !strings.Contains(ds[0].Fix, `source = "hashicorp/google"`) {
		t.Fatalf("bp_provider_version = %+v", ds)
	}
}
