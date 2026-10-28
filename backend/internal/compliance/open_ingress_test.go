package compliance_test

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The SSH and RDP controls graded on bp_open_ingress count only the
// findings about their port: an open database port violates neither.
func TestOpenIngressControlsCountTheirPort(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	azure := func(ports string) string {
		return `resource "azurerm_network_security_group" "nsg" {
  name                = "nsg"
  location            = "westeurope"
  resource_group_name = "rg"
  security_rule {
    name                       = "in"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "` + ports + `"
    source_address_prefix      = "Internet"
    destination_address_prefix = "*"
  }
}`
	}
	gcp := func(ports string) string {
		return `resource "google_compute_firewall" "f" {
  name          = "f"
  network       = "n"
  source_ranges = ["0.0.0.0/0"]
  allow {
    protocol = "tcp"
    ports    = ["` + ports + `"]
  }
}`
	}
	type want struct{ ssh, rdp compliance.Status }
	met, violated := compliance.StatusMet, compliance.StatusViolated
	for _, fw := range []struct {
		id, ssh, rdp string
		src          func(string) string
	}{
		{"cis-azure", "7.2", "7.1", azure},
		{"cis-gcp", "3.6", "3.7", gcp},
	} {
		var f compliance.Framework
		for _, x := range compliance.Frameworks() {
			if strings.HasPrefix(x.ID, fw.id+"-") {
				f = x
			}
		}
		if f.ID == "" {
			t.Fatalf("no %s framework", fw.id)
		}
		for ports, w := range map[string]want{
			"5432":    {met, met},
			"22":      {violated, met},
			"3389":    {met, violated},
			"0-65535": {violated, violated},
		} {
			wf, _, err := hclcodec.Import("t", map[string][]byte{"main.tf": []byte(fw.src(ports))})
			if err != nil {
				t.Fatal(err)
			}
			hclcodec.Normalize(wf)
			cfg := compliance.GradingSettings(model.BestPracticeSettings{}, f)
			r := compliance.Evaluate(f, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
			got := want{}
			for _, c := range r.Controls {
				switch c.ID {
				case fw.ssh:
					got.ssh = c.Status
				case fw.rdp:
					got.rdp = c.Status
				}
			}
			if got != w {
				t.Errorf("%s, port %s open: SSH %s, RDP %s; want %s, %s", f.ID, ports, got.ssh, got.rdp, w.ssh, w.rdp)
			}
		}
	}
}
