package lint

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// bp_open_ingress findings carry the exposed ports in Facet, so compliance
// controls about one port count only their findings.
func TestOpenIngressFacet(t *testing.T) {
	sg := func(proto, from, to string) string {
		return `resource "aws_security_group" "s" {
  ingress {
    protocol    = "` + proto + `"
    from_port   = ` + from + `
    to_port     = ` + to + `
    cidr_blocks = ["0.0.0.0/0"]
  }
}`
	}
	cases := []struct {
		name, src, facet string
		ssh, rdp         bool
	}{
		{"ssh", sg("tcp", "22", "22"), "22", true, false},
		{"database", sg("tcp", "5432", "5432"), "5432", false, false},
		{"wide range", sg("tcp", "0", "65535"), "22,23,445,1433,1521,2379,3306,3389,5432,5439,5984,6379,9042,9200,11211,27017", true, true},
		{"all protocols", sg("-1", "0", "0"), "all", true, true},
		{"gcp port list", `resource "google_compute_firewall" "f" {
  name          = "f"
  network       = "n"
  source_ranges = ["0.0.0.0/0"]
  allow {
    protocol = "tcp"
    ports    = ["3389", "20-23", "22"]
  }
}`, "22,23,3389", true, true},
		{"azure any port", `resource "azurerm_network_security_rule" "r" {
  name                        = "any"
  priority                    = 100
  direction                   = "Inbound"
  access                      = "Allow"
  protocol                    = "Tcp"
  source_port_range           = "*"
  destination_port_range      = "*"
  source_address_prefix       = "Internet"
  destination_address_prefix  = "*"
  resource_group_name         = "rg"
  network_security_group_name = "nsg"
}`, "all", true, true},
	}
	for _, tc := range cases {
		got := findings(t, tc.src, "bp_open_ingress")
		if len(got) != 1 {
			t.Fatalf("%s: %d findings", tc.name, len(got))
		}
		d := got[0]
		if d.Facet != tc.facet || ExposesPort(d, 22) != tc.ssh || ExposesPort(d, 3389) != tc.rdp {
			t.Errorf("%s: facet %q, ssh %v, rdp %v", tc.name, d.Facet, ExposesPort(d, 22), ExposesPort(d, 3389))
		}
	}
	if ExposesPort(model.Diagnostic{Code: "bp_sg_admin_ipv4", Facet: "all"}, 22) {
		t.Error("ExposesPort reads only bp_open_ingress findings")
	}
}
