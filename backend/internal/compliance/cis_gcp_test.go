package compliance_test

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/compliance"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// The CIS GCP pack grades a real design through the real rules, opt-in ones
// included, each control on its own resource types.
func TestCISGCPGradesADesign(t *testing.T) {
	src := `
resource "google_compute_network" "main" {
  name                    = "main"
  auto_create_subnetworks = false
}
resource "google_compute_subnetwork" "app" {
  name          = "app"
  network       = google_compute_network.main.id
  ip_cidr_range = "10.0.0.0/24"
}
resource "google_compute_firewall" "ssh" {
  name          = "ssh"
  network       = google_compute_network.main.id
  source_ranges = ["0.0.0.0/0"]
  allow {
    protocol = "tcp"
    ports    = ["22"]
  }
}
resource "google_service_account" "app" { account_id = "app" }
resource "google_compute_instance" "a" {
  name         = "a"
  machine_type = "e2-small"
  zone         = "us-central1-a"
  boot_disk {
    initialize_params { image = "debian-cloud/debian-12" }
  }
  network_interface { subnetwork = google_compute_subnetwork.app.id }
  service_account {
    email  = google_service_account.app.email
    scopes = ["cloud-platform"]
  }
  metadata = {
    block-project-ssh-keys = "true"
    enable-oslogin         = "TRUE"
  }
}
resource "google_compute_instance" "b" {
  name         = "b"
  machine_type = "e2-small"
  zone         = "us-central1-a"
  boot_disk {
    initialize_params { image = "debian-cloud/debian-12" }
  }
  network_interface { subnetwork = google_compute_subnetwork.app.id }
  service_account { scopes = ["cloud-platform"] }
}
resource "google_storage_bucket" "data" {
  name                        = "data"
  location                    = "US"
  uniform_bucket_level_access = true
}
resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = "POSTGRES_16"
  region           = "us-central1"
  settings {
    tier = "db-custom-2-7680"
    ip_configuration { ssl_mode = "ENCRYPTED_ONLY" }
    backup_configuration { enabled = true }
  }
}
`
	wf, _, err := hclcodec.Import("gcp", map[string][]byte{"main.tf": []byte(src)})
	if err != nil {
		t.Fatal(err)
	}
	hclcodec.Normalize(wf)
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	fw, ok := compliance.Lookup("cis-gcp-5.0")
	if !ok {
		t.Fatal("cis-gcp-5.0 is not registered")
	}
	cfg := compliance.GradingSettings(model.BestPracticeSettings{}, fw)
	r := compliance.Evaluate(fw, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
	want := map[string]compliance.Status{
		"1.3":   compliance.StatusManual,
		"1.5":   compliance.StatusMet,           // a service account, no keys
		"1.11":  compliance.StatusNotApplicable, // no KMS keys
		"2.13":  compliance.StatusViolated,      // no DNS policy (opt-in rule)
		"3.1":   compliance.StatusMet,           // custom VPC everywhere (opt-in rule)
		"3.6":   compliance.StatusViolated,      // SSH open to the world
		"3.10":  compliance.StatusViolated,      // no flow logs (opt-in rule)
		"4.1":   compliance.StatusViolated,      // b runs as the default service account
		"4.2":   compliance.StatusViolated,      // ... with the cloud-platform scope
		"4.3":   compliance.StatusViolated,      // only a blocks project-wide keys (opt-in rule)
		"4.4":   compliance.StatusViolated,      // only a enables OS Login (opt-in rule)
		"4.5":   compliance.StatusMet,
		"5.2":   compliance.StatusMet,
		"6.2.2": compliance.StatusViolated, // log_connections left off (opt-in rule)
		"6.2.7": compliance.StatusMet,      // log_min_duration_statement default -1
		"6.4":   compliance.StatusMet,
		"6.7":   compliance.StatusViolated, // public IP by default (opt-in rule)
		"6.8":   compliance.StatusMet,
		"6.1.2": compliance.StatusNotApplicable, // MySQL flags don't apply to the PostgreSQL instance
		"6.3.5": compliance.StatusNotApplicable, // nor SQL Server flags (whose rule would flag the default)
		"6.3.6": compliance.StatusNotApplicable,
		"7.1":   compliance.StatusNotApplicable,
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

// 3.11 is graded from the proxies' SSL policies: a proxy without one uses
// the TLS 1.0 default, a RESTRICTED policy passes.
func TestCISGCPSSLPolicyControl(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	fw, _ := compliance.Lookup("cis-gcp-5.0")
	cfg := compliance.GradingSettings(model.BestPracticeSettings{}, fw)
	proxy := func(extra string) string {
		return `resource "google_compute_target_https_proxy" "p" {
  name             = "p"
  url_map          = "u"
  ssl_certificates = ["c"]
` + extra + `
}
`
	}
	cases := map[string]struct {
		src  string
		want compliance.Status
	}{
		"no proxy":          {`resource "google_compute_network" "n" { name = "n" }`, compliance.StatusNotApplicable},
		"default policy":    {proxy(""), compliance.StatusViolated},
		"compatible policy": {proxy(`  ssl_policy = google_compute_ssl_policy.s.id`) + `resource "google_compute_ssl_policy" "s" { name = "s" }`, compliance.StatusViolated},
		"restricted policy": {proxy(`  ssl_policy = google_compute_ssl_policy.s.id`) + `resource "google_compute_ssl_policy" "s" {
  name    = "s"
  profile = "RESTRICTED"
}`, compliance.StatusMet},
	}
	for name, tc := range cases {
		wf, _, err := hclcodec.Import("gcp", map[string][]byte{"main.tf": []byte(tc.src)})
		if err != nil {
			t.Fatal(err)
		}
		hclcodec.Normalize(wf)
		r := compliance.Evaluate(fw, wf, lint.Check(wf, cat, cfg, nil), cfg, nil)
		for _, c := range r.Controls {
			if c.ID == "3.11" && c.Status != tc.want {
				t.Errorf("%s: 3.11 = %q, want %q", name, c.Status, tc.want)
			}
		}
	}
}

// The Cloud SQL flag controls apply by engine; a database_version that is an
// expression can't be read, so every engine's controls apply to it.
func TestCISGCPFlagControlsByEngine(t *testing.T) {
	fw, _ := compliance.Lookup("cis-gcp-5.0")
	instance := func(version string) *model.Node {
		return &model.Node{Kind: model.KindResource, Type: "google_sql_database_instance", Name: "db",
			Body: model.Body{Attrs: []model.Attr{{Name: "database_version", Value: model.Value{Expr: version}}}}}
	}
	cases := []struct {
		version string
		want    map[string]bool // control -> applies
	}{
		{`"POSTGRES_16"`, map[string]bool{"6.1.2": false, "6.2.2": true, "6.3.5": false}},
		{`"MYSQL_8_0"`, map[string]bool{"6.1.2": true, "6.2.2": false, "6.3.5": false}},
		{`"SQLSERVER_2022_STANDARD"`, map[string]bool{"6.1.2": false, "6.2.2": false, "6.3.5": true}},
		{`var.db_version`, map[string]bool{"6.1.2": true, "6.2.2": true, "6.3.5": true}},
	}
	for _, tc := range cases {
		n := instance(tc.version)
		for _, c := range fw.Controls {
			want, ok := tc.want[c.ID]
			if !ok {
				continue
			}
			if c.AppliesToNode == nil {
				t.Fatalf("control %s has no AppliesToNode", c.ID)
			}
			if got := c.AppliesToNode(n); got != want {
				t.Errorf("%s on %s: applies = %v, want %v", c.ID, tc.version, got, want)
			}
		}
	}
}
