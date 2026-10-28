package compliance_test

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The CIS OCI pack grades a real design through the real rules, opt-in
// ones included, each control on its own resource types.
func TestCISOCIGradesADesign(t *testing.T) {
	src := `
resource "oci_core_vcn" "v" {
  compartment_id = var.c
  cidr_blocks    = ["10.0.0.0/16"]
}
resource "oci_core_security_list" "admin" {
  compartment_id = var.c
  vcn_id         = oci_core_vcn.v.id
  ingress_security_rules {
    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 22
      max = 22
    }
  }
}
resource "oci_core_subnet" "s" {
  compartment_id    = var.c
  vcn_id            = oci_core_vcn.v.id
  cidr_block        = "10.0.1.0/24"
  security_list_ids = [oci_core_security_list.admin.id]
}
resource "oci_objectstorage_bucket" "b" {
  compartment_id = var.c
  namespace      = var.ns
  name           = "data"
  kms_key_id     = var.key
}
`
	wf, _, err := hclcodec.Import("oci", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	fw, ok := compliance.Lookup("cis-oci-3.1")
	if !ok {
		t.Fatal("cis-oci-3.1 is not registered")
	}
	cfg := compliance.GradingSettings(model.BestPracticeSettings{}, fw)
	r := compliance.Evaluate(fw, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
	want := map[string]compliance.Status{
		"2.1":   compliance.StatusViolated,      // SSH open on the only security list
		"2.2":   compliance.StatusMet,           // no RDP
		"2.3":   compliance.StatusNotApplicable, // no NSG rules
		"2.5":   compliance.StatusViolated,      // default security list left as created (opt-in rule)
		"4.13":  compliance.StatusViolated,      // no flow log (opt-in rule)
		"5.1.1": compliance.StatusMet,
		"5.1.2": compliance.StatusMet,      // kms_key_id set
		"5.1.3": compliance.StatusViolated, // no versioning (opt-in rule)
		"5.2.1": compliance.StatusNotApplicable,
		"1.7":   compliance.StatusManual,
	}
	got := map[string]compliance.Status{}
	for _, c := range r.Controls {
		got[c.ID] = c.Status
	}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("control %s = %q, want %q", id, got[id], status)
		}
	}
}

// A dedicated Autonomous Database is outside CIS 2.8, and an instance booted
// from an existing boot volume has no boot volume key of its own (5.2.2).
func TestCISOCINarrowedControls(t *testing.T) {
	src := `
resource "oci_database_autonomous_database" "a" {
  compartment_id                   = var.c
  db_name                          = "a"
  autonomous_container_database_id = var.acd
}
resource "oci_core_instance" "i" {
  compartment_id      = var.c
  availability_domain = var.ad
  shape               = "VM.Standard.E5.Flex"
  source_details {
    source_type = "bootVolume"
    source_id   = var.boot
  }
}
`
	wf, _, err := hclcodec.Import("oci", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	fw, _ := compliance.Lookup("cis-oci-3.1")
	r := compliance.Evaluate(fw, wf, nil, model.BestPracticeSettings{}, nil)
	for _, c := range r.Controls {
		if (c.ID == "2.8" || c.ID == "5.2.2") && c.Status != compliance.StatusNotApplicable {
			t.Errorf("control %s = %q, want not applicable", c.ID, c.Status)
		}
	}
}
