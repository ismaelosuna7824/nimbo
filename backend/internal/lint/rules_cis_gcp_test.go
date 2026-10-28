package lint

import "fmt"

// gcpCISCases are the cases of the CIS GCP rules; init adds them to
// structureCases so TestRules runs them with the other rules.
var gcpCISCases = map[string][]structureCase{
	"bp_gcp_sa_key": {
		{name: "key", src: `resource "google_service_account_key" "k" { service_account_id = "sa" }`, want: 1},
		{name: "no key", src: `resource "google_service_account" "a" { account_id = "app" }`, want: 0},
	},
	"bp_gcp_sa_admin_role": {
		{name: "editor to sa", src: `resource "google_service_account" "a" { account_id = "app" }
resource "google_project_iam_member" "m" {
  project = "p"
  role    = "roles/editor"
  member  = "serviceAccount:${google_service_account.a.email}"
}`, want: 1},
		{name: "admin binding with sa", src: `resource "google_folder_iam_binding" "b" {
  folder  = "folders/1"
  role    = "roles/compute.admin"
  members = ["user:a@example.com", "serviceAccount:ci@p.iam.gserviceaccount.com"]
}`, want: 1},
		{name: "viewer to sa", src: `resource "google_project_iam_member" "m" {
  project = "p"
  role    = "roles/storage.objectViewer"
  member  = "serviceAccount:ci@p.iam.gserviceaccount.com"
}`, want: 0},
		{name: "owner to user", src: `resource "google_project_iam_member" "m" {
  project = "p"
  role    = "roles/owner"
  member  = "user:a@example.com"
}`, want: 0},
	},
	"bp_gcp_sa_user_project_role": {
		{name: "user at project", src: `resource "google_project_iam_member" "m" {
  project = "p"
  role    = "roles/iam.serviceAccountUser"
  member  = "user:a@example.com"
}`, want: 1},
		{name: "group token creator", src: `resource "google_project_iam_binding" "b" {
  project = "p"
  role    = "roles/iam.serviceAccountTokenCreator"
  members = ["group:devs@example.com"]
}`, want: 1},
		{name: "service account at project", src: `resource "google_project_iam_member" "m" {
  project = "p"
  role    = "roles/iam.serviceAccountUser"
  member  = "serviceAccount:ci@p.iam.gserviceaccount.com"
}`, want: 0},
		{name: "on the service account", src: `resource "google_service_account_iam_member" "m" {
  service_account_id = "projects/p/serviceAccounts/x"
  role               = "roles/iam.serviceAccountUser"
  member             = "user:a@example.com"
}`, want: 0},
	},
	"bp_gcp_kms_public": {
		{name: "allUsers", src: `resource "google_kms_crypto_key_iam_member" "m" {
  crypto_key_id = "k"
  role          = "roles/cloudkms.cryptoKeyDecrypter"
  member        = "allUsers"
}`, want: 1},
		{name: "allAuthenticatedUsers on ring", src: `resource "google_kms_key_ring_iam_binding" "b" {
  key_ring_id = "r"
  role        = "roles/cloudkms.viewer"
  members     = ["allAuthenticatedUsers"]
}`, want: 1},
		{name: "service account", src: `resource "google_kms_crypto_key_iam_member" "m" {
  crypto_key_id = "k"
  role          = "roles/cloudkms.cryptoKeyDecrypter"
  member        = "serviceAccount:ci@p.iam.gserviceaccount.com"
}`, want: 0},
	},
	"bp_gcp_kms_rotation": {
		{name: "no rotation", src: `resource "google_kms_crypto_key" "k" {
  name     = "k"
  key_ring = "r"
}`, want: 1},
		{name: "yearly", src: `resource "google_kms_crypto_key" "k" {
  name            = "k"
  key_ring        = "r"
  rotation_period = "31536000s"
}`, want: 1},
		{name: "90 days", src: `resource "google_kms_crypto_key" "k" {
  name            = "k"
  key_ring        = "r"
  rotation_period = "7776000s"
}`, want: 0},
		{name: "asymmetric", src: `resource "google_kms_crypto_key" "k" {
  name     = "k"
  key_ring = "r"
  purpose  = "ASYMMETRIC_SIGN"
}`, want: 0},
	},
	"bp_gcp_apikey_api_targets": {
		{name: "unrestricted", src: `resource "google_apikeys_key" "k" { name = "k" }`, want: 1},
		{name: "browser only", src: `resource "google_apikeys_key" "k" {
  name = "k"
  restrictions {
    browser_key_restrictions { allowed_referrers = ["https://example.com/*"] }
  }
}`, want: 1},
		{name: "all cloud apis", src: `resource "google_apikeys_key" "k" {
  name = "k"
  restrictions {
    api_targets { service = "cloudapis.googleapis.com" }
  }
}`, want: 1},
		{name: "targeted", src: `resource "google_apikeys_key" "k" {
  name = "k"
  restrictions {
    api_targets { service = "maps-backend.googleapis.com" }
  }
}`, want: 0},
	},
	"bp_gcp_audit_logs": {
		{name: "complete", src: `resource "google_project_iam_audit_config" "a" {
  project = "p"
  service = "allServices"
  audit_log_config { log_type = "ADMIN_READ" }
  audit_log_config { log_type = "DATA_READ" }
  audit_log_config { log_type = "DATA_WRITE" }
}`, want: 0},
		{name: "missing data read", src: `resource "google_project_iam_audit_config" "a" {
  project = "p"
  service = "allServices"
  audit_log_config { log_type = "ADMIN_READ" }
  audit_log_config { log_type = "DATA_WRITE" }
}`, want: 1},
		{name: "exempted", src: `resource "google_project_iam_audit_config" "a" {
  project = "p"
  service = "allServices"
  audit_log_config { log_type = "ADMIN_READ" }
  audit_log_config { log_type = "DATA_READ" }
  audit_log_config {
    log_type         = "DATA_WRITE"
    exempted_members = ["user:a@example.com"]
  }
}`, want: 1},
		{name: "one service only", src: `resource "google_project_iam_audit_config" "a" {
  project = "p"
  service = "storage.googleapis.com"
  audit_log_config { log_type = "DATA_READ" }
}`, want: 1},
		{name: "none declared", src: `resource "google_storage_bucket" "b" {
  name     = "b"
  location = "US"
}`, want: 0},
	},
	"bp_gcp_log_bucket_lock": {
		{name: "unlocked", src: `resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "US"
}
resource "google_logging_project_sink" "s" {
  name        = "all"
  destination = "storage.googleapis.com/${google_storage_bucket.logs.name}"
}`, want: 1},
		{name: "locked", src: `resource "google_storage_bucket" "logs" {
  name     = "logs"
  location = "US"
  retention_policy {
    retention_period = 31536000
    is_locked        = true
  }
}
resource "google_logging_project_sink" "s" {
  name        = "all"
  destination = "storage.googleapis.com/${google_storage_bucket.logs.name}"
}`, want: 0},
		{name: "bigquery sink", src: `resource "google_logging_project_sink" "s" {
  name        = "all"
  destination = "bigquery.googleapis.com/projects/p/datasets/logs"
}`, want: 0},
	},
	"bp_gcp_dns_logging": {
		{name: "no policy", src: `resource "google_compute_network" "n" {
  name                    = "n"
  auto_create_subnetworks = false
}`, want: 1},
		{name: "logged", src: `resource "google_compute_network" "n" {
  name                    = "n"
  auto_create_subnetworks = false
}
resource "google_dns_policy" "logging" {
  name           = "logging"
  enable_logging = true
  networks { network_url = google_compute_network.n.id }
}`, want: 0},
		{name: "policy without logging", src: `resource "google_compute_network" "n" {
  name                    = "n"
  auto_create_subnetworks = false
}
resource "google_dns_policy" "p" {
  name = "p"
  networks { network_url = google_compute_network.n.id }
}`, want: 1},
	},
	"bp_gcp_lb_logging": {
		{name: "default external http", src: `resource "google_compute_backend_service" "b" { name = "b" }`, want: 1},
		{name: "logging on", src: `resource "google_compute_backend_service" "b" {
  name                  = "b"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  log_config {
    enable      = true
    sample_rate = 1
  }
}`, want: 0},
		{name: "internal tcp", src: `resource "google_compute_region_backend_service" "b" {
  name                  = "b"
  load_balancing_scheme = "INTERNAL"
  protocol              = "TCP"
}`, want: 0},
		{name: "regional external https", src: `resource "google_compute_region_backend_service" "b" {
  name                  = "b"
  load_balancing_scheme = "EXTERNAL_MANAGED"
  protocol              = "HTTPS"
}`, want: 1},
	},
	"bp_gcp_default_network": {
		{name: "project default", src: `resource "google_project" "p" {
  name       = "p"
  project_id = "p"
}`, want: 1},
		{name: "project without", src: `resource "google_project" "p" {
  name                = "p"
  project_id          = "p"
  auto_create_network = false
}`, want: 0},
		{name: "firewall on default", src: `resource "google_compute_firewall" "f" {
  name    = "f"
  network = "default"
  allow { protocol = "icmp" }
}`, want: 1},
		{name: "vm on default data source", src: `data "google_compute_network" "def" { name = "default" }
resource "google_compute_instance" "vm" {
  name         = "vm"
  machine_type = "e2-small"
  zone         = "us-central1-a"
  boot_disk {
    initialize_params { image = "debian-cloud/debian-12" }
  }
  network_interface { network = data.google_compute_network.def.self_link }
}`, want: 1},
		{name: "custom vpc", src: `resource "google_compute_network" "n" {
  name                    = "n"
  auto_create_subnetworks = false
}
resource "google_compute_firewall" "f" {
  name    = "f"
  network = google_compute_network.n.id
  allow { protocol = "icmp" }
}`, want: 0},
	},
	"bp_gcp_dnssec": {
		{name: "unsigned public", src: `resource "google_dns_managed_zone" "z" {
  name     = "z"
  dns_name = "example.com."
}`, want: 1},
		{name: "signed", src: `resource "google_dns_managed_zone" "z" {
  name     = "z"
  dns_name = "example.com."
  dnssec_config { state = "on" }
}`, want: 0},
		{name: "private", src: `resource "google_dns_managed_zone" "z" {
  name       = "z"
  dns_name   = "internal."
  visibility = "private"
}`, want: 0},
	},
	"bp_gcp_dnssec_ksk_rsasha1": {
		{name: "ksk sha1", src: gcpDNSZone("keySigning", "rsasha1"), want: 1},
		{name: "zsk sha1 only", src: gcpDNSZone("zoneSigning", "rsasha1"), want: 0},
		{name: "ksk sha256", src: gcpDNSZone("keySigning", "rsasha256"), want: 0},
	},
	"bp_gcp_dnssec_zsk_rsasha1": {
		{name: "zsk sha1", src: gcpDNSZone("zoneSigning", "rsasha1"), want: 1},
		{name: "ksk sha1 only", src: gcpDNSZone("keySigning", "rsasha1"), want: 0},
	},
	"bp_gcp_flow_logs": {
		{name: "no logs", src: `resource "google_compute_subnetwork" "s" {
  name          = "s"
  network       = "n"
  ip_cidr_range = "10.0.0.0/24"
}`, want: 1},
		{name: "sampled", src: `resource "google_compute_subnetwork" "s" {
  name          = "s"
  network       = "n"
  ip_cidr_range = "10.0.0.0/24"
  log_config { aggregation_interval = "INTERVAL_5_SEC" }
}`, want: 1},
		{name: "full", src: `resource "google_compute_subnetwork" "s" {
  name          = "s"
  network       = "n"
  ip_cidr_range = "10.0.0.0/24"
  log_config {
    aggregation_interval = "INTERVAL_5_SEC"
    flow_sampling        = 1
    metadata             = "INCLUDE_ALL_METADATA"
  }
}`, want: 0},
		{name: "proxy only", src: `resource "google_compute_subnetwork" "s" {
  name          = "s"
  network       = "n"
  ip_cidr_range = "10.1.0.0/24"
  purpose       = "REGIONAL_MANAGED_PROXY"
  role          = "ACTIVE"
}`, want: 0},
	},
	"bp_gcp_block_project_ssh_keys": {
		{name: "no metadata", src: gcpVM(``), want: 1},
		{name: "blocked", src: gcpVM(`metadata = { block-project-ssh-keys = "true" }`), want: 0},
		{name: "template blocked off", src: `resource "google_compute_instance_template" "t" {
  machine_type = "e2-small"
  disk { source_image = "debian-cloud/debian-12" }
  network_interface { network = "n" }
  metadata = { block-project-ssh-keys = "false" }
}`, want: 1},
	},
	"bp_gcp_os_login": {
		{name: "not enabled", src: gcpVM(``), want: 1},
		{name: "on the vm", src: gcpVM(`metadata = { enable-oslogin = "TRUE" }`), want: 0},
		{name: "project metadata", src: gcpVM(``) + `
resource "google_compute_project_metadata_item" "oslogin" {
  key   = "enable-oslogin"
  value = "TRUE"
}`, want: 0},
		{name: "vm overrides project", src: gcpVM(`metadata = { enable-oslogin = "FALSE" }`) + `
resource "google_compute_project_metadata" "m" {
  metadata = { enable-oslogin = "TRUE" }
}`, want: 1},
		{name: "project off", src: `resource "google_compute_project_metadata_item" "oslogin" {
  key   = "enable-oslogin"
  value = "FALSE"
}`, want: 1},
	},
	"bp_gcp_serial_port": {
		{name: "vm enabled", src: gcpVM(`metadata = { serial-port-enable = "true" }`), want: 1},
		{name: "project enabled", src: `resource "google_compute_project_metadata_item" "serial" {
  key   = "serial-port-enable"
  value = "1"
}`, want: 1},
		{name: "default", src: gcpVM(``), want: 0},
		{name: "disabled", src: gcpVM(`metadata = { serial-port-enable = "false" }`), want: 0},
	},
	"bp_gcp_ip_forwarding": {
		{name: "forwarding", src: gcpVM(`can_ip_forward = true`), want: 1},
		{name: "default", src: gcpVM(``), want: 0},
	},
	"bp_gcp_shielded_vm": {
		{name: "vtpm off", src: gcpVM(`shielded_instance_config {
    enable_vtpm = false
  }`), want: 1},
		{name: "defaults", src: gcpVM(``), want: 0},
		{name: "all on", src: gcpVM(`shielded_instance_config {
    enable_secure_boot          = true
    enable_vtpm                 = true
    enable_integrity_monitoring = true
  }`), want: 0},
	},
	"bp_gcp_default_sa": {
		{name: "email unset", src: gcpVM(`service_account { scopes = ["cloud-platform"] }`), want: 1},
		{name: "default email", src: gcpVM(`service_account {
    email  = "123-compute@developer.gserviceaccount.com"
    scopes = ["logging-write"]
  }`), want: 1},
		{name: "default data source", src: `data "google_compute_default_service_account" "d" {}
` + gcpVM(`service_account {
    email  = data.google_compute_default_service_account.d.email
    scopes = ["logging-write"]
  }`), want: 1},
		{name: "dedicated", src: `resource "google_service_account" "app" { account_id = "app" }
` + gcpVM(`service_account {
    email  = google_service_account.app.email
    scopes = ["cloud-platform"]
  }`), want: 0},
		{name: "no service account", src: gcpVM(``), want: 0},
	},
	"bp_gcp_default_sa_full_access": {
		{name: "default full", src: gcpVM(`service_account { scopes = ["cloud-platform"] }`), want: 1},
		{name: "default limited", src: gcpVM(`service_account { scopes = ["logging-write", "monitoring-write"] }`), want: 0},
		{name: "dedicated full", src: `resource "google_service_account" "app" { account_id = "app" }
` + gcpVM(`service_account {
    email  = google_service_account.app.email
    scopes = ["https://www.googleapis.com/auth/cloud-platform"]
  }`), want: 0},
	},
	"bp_gcp_instance_public_ip": {
		{name: "external ip", src: `resource "google_compute_instance" "vm" {
  name         = "vm"
  machine_type = "e2-small"
  zone         = "us-central1-a"
  boot_disk {
    initialize_params { image = "debian-cloud/debian-12" }
  }
  network_interface {
    network = "n"
    access_config {}
  }
}`, want: 1},
		{name: "private", src: gcpVM(``), want: 0},
	},
	"bp_gcp_bucket_uniform_access": {
		{name: "unset", src: `resource "google_storage_bucket" "b" {
  name     = "b"
  location = "US"
}`, want: 1},
		{name: "false", src: `resource "google_storage_bucket" "b" {
  name                        = "b"
  location                    = "US"
  uniform_bucket_level_access = false
}`, want: 1},
		{name: "on", src: `resource "google_storage_bucket" "b" {
  name                        = "b"
  location                    = "US"
  uniform_bucket_level_access = true
}`, want: 0},
	},
	"bp_gcp_sql_ssl": {
		{name: "unset", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "allow unencrypted", src: gcpSQL("POSTGRES_16", `ip_configuration { ssl_mode = "ALLOW_UNENCRYPTED_AND_ENCRYPTED" }`), want: 1},
		{name: "encrypted only", src: gcpSQL("POSTGRES_16", `ip_configuration { ssl_mode = "ENCRYPTED_ONLY" }`), want: 0},
	},
	"bp_gcp_sql_open_network": {
		{name: "world", src: gcpSQL("MYSQL_8_0", `ip_configuration {
      authorized_networks {
        name  = "all"
        value = "0.0.0.0/0"
      }
    }`), want: 1},
		{name: "office", src: gcpSQL("MYSQL_8_0", `ip_configuration {
      authorized_networks { value = "203.0.113.0/24" }
    }`), want: 0},
	},
	"bp_gcp_sql_public_ip": {
		{name: "default public", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "private", src: gcpSQL("POSTGRES_16", `ip_configuration {
      ipv4_enabled    = false
      private_network = "projects/p/global/networks/n"
    }`), want: 0},
		{name: "replica", src: `resource "google_sql_database_instance" "r" {
  name                 = "r"
  database_version     = "POSTGRES_16"
  region               = "us-central1"
  master_instance_name = "primary"
  settings { tier = "db-f1-micro" }
}`, want: 0},
	},
	"bp_gcp_sql_backups": {
		{name: "none", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "disabled", src: gcpSQL("POSTGRES_16", `backup_configuration { enabled = false }`), want: 1},
		{name: "enabled", src: gcpSQL("POSTGRES_16", `backup_configuration { enabled = true }`), want: 0},
	},
	"bp_gcp_bigquery_public": {
		{name: "access block", src: `resource "google_bigquery_dataset" "d" {
  dataset_id = "d"
  access {
    role          = "READER"
    special_group = "allAuthenticatedUsers"
  }
}`, want: 1},
		{name: "iam member", src: `resource "google_bigquery_dataset_iam_member" "m" {
  dataset_id = "d"
  role       = "roles/bigquery.dataViewer"
  member     = "allUsers"
}`, want: 1},
		{name: "dataset access", src: `resource "google_bigquery_dataset_access" "a" {
  dataset_id = "d"
  role       = "READER"
  iam_member = "allUsers"
}`, want: 1},
		{name: "private", src: `resource "google_bigquery_dataset" "d" {
  dataset_id = "d"
  access {
    role          = "OWNER"
    user_by_email = "a@example.com"
  }
}`, want: 0},
	},
	"bp_gcp_bigquery_table_cmek": {
		{name: "google key", src: `resource "google_bigquery_dataset" "d" { dataset_id = "d" }
resource "google_bigquery_table" "t" {
  dataset_id = google_bigquery_dataset.d.dataset_id
  table_id   = "t"
}`, want: 1},
		{name: "dataset default", src: `resource "google_bigquery_dataset" "d" {
  dataset_id = "d"
  default_encryption_configuration { kms_key_name = "projects/p/locations/us/keyRings/r/cryptoKeys/k" }
}
resource "google_bigquery_table" "t" {
  dataset_id = google_bigquery_dataset.d.dataset_id
  table_id   = "t"
}`, want: 0},
		{name: "table key", src: `resource "google_bigquery_table" "t" {
  dataset_id = "d"
  table_id   = "t"
  encryption_configuration { kms_key_name = "projects/p/locations/us/keyRings/r/cryptoKeys/k" }
}`, want: 0},
		{name: "view", src: `resource "google_bigquery_table" "t" {
  dataset_id = "d"
  table_id   = "t"
  view {
    query          = "SELECT 1"
    use_legacy_sql = false
  }
}`, want: 0},
	},
	"bp_gcp_bigquery_dataset_cmek": {
		{name: "no default", src: `resource "google_bigquery_dataset" "d" { dataset_id = "d" }`, want: 1},
		{name: "default key", src: `resource "google_bigquery_dataset" "d" {
  dataset_id = "d"
  default_encryption_configuration { kms_key_name = "projects/p/locations/us/keyRings/r/cryptoKeys/k" }
}`, want: 0},
	},
	"bp_gcp_dataproc_cmek": {
		{name: "google key", src: `resource "google_dataproc_cluster" "c" {
  name   = "c"
  region = "us-central1"
}`, want: 1},
		{name: "cmek", src: `resource "google_dataproc_cluster" "c" {
  name   = "c"
  region = "us-central1"
  cluster_config {
    encryption_config { kms_key_name = "projects/p/locations/us-central1/keyRings/r/cryptoKeys/k" }
  }
}`, want: 0},
	},
	"bp_gcp_sql_mysql_skip_show_database": {
		{name: "unset", src: gcpSQL("MYSQL_8_0", ``), want: 1},
		{name: "on", src: gcpSQL("MYSQL_8_0", gcpFlag("skip_show_database", "on")), want: 0},
		{name: "postgres", src: gcpSQL("POSTGRES_16", ``), want: 0},
	},
	"bp_gcp_sql_mysql_local_infile": {
		{name: "unset", src: gcpSQL("MYSQL_8_0", ``), want: 1},
		{name: "on", src: gcpSQL("MYSQL_8_0", gcpFlag("local_infile", "on")), want: 1},
		{name: "off", src: gcpSQL("MYSQL_8_0", gcpFlag("local_infile", "off")), want: 0},
	},
	"bp_gcp_sql_pg_log_error_verbosity": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 0},
		{name: "terse", src: gcpSQL("POSTGRES_16", gcpFlag("log_error_verbosity", "terse")), want: 0},
		{name: "verbose", src: gcpSQL("POSTGRES_16", gcpFlag("log_error_verbosity", "verbose")), want: 1},
	},
	"bp_gcp_sql_pg_log_connections": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "on", src: gcpSQL("POSTGRES_16", gcpFlag("log_connections", "on")), want: 0},
		{name: "dynamic flags", src: gcpSQL("POSTGRES_16", `dynamic "database_flags" {
      for_each = var.flags
      content {
        name  = database_flags.key
        value = database_flags.value
      }
    }`), want: 0},
	},
	"bp_gcp_sql_pg_log_disconnections": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "on", src: gcpSQL("POSTGRES_16", gcpFlag("log_disconnections", "on")), want: 0},
	},
	"bp_gcp_sql_pg_log_statement": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "ddl", src: gcpSQL("POSTGRES_16", gcpFlag("log_statement", "ddl")), want: 0},
		{name: "mod", src: gcpSQL("POSTGRES_16", gcpFlag("log_statement", "mod")), want: 0},
	},
	"bp_gcp_sql_pg_log_min_messages": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 0},
		{name: "debug", src: gcpSQL("POSTGRES_16", gcpFlag("log_min_messages", "debug1")), want: 1},
		{name: "error", src: gcpSQL("POSTGRES_16", gcpFlag("log_min_messages", "error")), want: 0},
	},
	"bp_gcp_sql_pg_log_min_error_statement": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 0},
		{name: "notice", src: gcpSQL("POSTGRES_16", gcpFlag("log_min_error_statement", "notice")), want: 1},
	},
	"bp_gcp_sql_pg_log_min_duration_statement": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 0},
		{name: "logs slow queries", src: gcpSQL("POSTGRES_16", gcpFlag("log_min_duration_statement", "500")), want: 1},
		{name: "disabled", src: gcpSQL("POSTGRES_16", gcpFlag("log_min_duration_statement", "-1")), want: 0},
	},
	"bp_gcp_sql_pg_pgaudit": {
		{name: "default", src: gcpSQL("POSTGRES_16", ``), want: 1},
		{name: "on", src: gcpSQL("POSTGRES_16", gcpFlag("cloudsql.enable_pgaudit", "on")), want: 0},
	},
	"bp_gcp_sql_sqlserver_external_scripts": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 0},
		{name: "on", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("external scripts enabled", "on")), want: 1},
	},
	"bp_gcp_sql_sqlserver_cross_db_chaining": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 0},
		{name: "on", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("cross db ownership chaining", "on")), want: 1},
	},
	"bp_gcp_sql_sqlserver_user_connections": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 0},
		{name: "capped", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("user connections", "100")), want: 1},
	},
	"bp_gcp_sql_sqlserver_user_options": {
		{name: "unset", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 0},
		{name: "set", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("user options", "16")), want: 1},
	},
	"bp_gcp_sql_sqlserver_remote_access": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 1},
		{name: "off", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("remote access", "off")), want: 0},
	},
	"bp_gcp_sql_sqlserver_trace_3625": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 1},
		{name: "on", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("3625", "on")), want: 0},
	},
	"bp_gcp_sql_sqlserver_contained_db_auth": {
		{name: "default", src: gcpSQL("SQLSERVER_2022_STANDARD", ``), want: 0},
		{name: "on", src: gcpSQL("SQLSERVER_2022_STANDARD", gcpFlag("contained database authentication", "on")), want: 1},
	},
}

func init() {
	for code, cs := range gcpCISCases {
		structureCases[code] = append(structureCases[code], cs...)
	}
}

// gcpVM renders a private VM with extra attributes/blocks.
func gcpVM(extra string) string {
	return fmt.Sprintf(`resource "google_compute_instance" "vm" {
  name         = "vm"
  machine_type = "e2-small"
  zone         = "us-central1-a"
  boot_disk {
    initialize_params { image = "debian-cloud/debian-12" }
  }
  network_interface { network = "n" }
  %s
}`, extra)
}

// gcpSQL renders a Cloud SQL instance with extra settings content.
func gcpSQL(version, settings string) string {
	return fmt.Sprintf(`resource "google_sql_database_instance" "db" {
  name             = "db"
  database_version = %q
  region           = "us-central1"
  settings {
    tier = "db-custom-2-7680"
    %s
  }
}`, version, settings)
}

func gcpFlag(name, value string) string {
	return fmt.Sprintf(`database_flags {
      name  = %q
      value = %q
    }`, name, value)
}

func gcpDNSZone(keyType, algorithm string) string {
	return fmt.Sprintf(`resource "google_dns_managed_zone" "z" {
  name     = "z"
  dns_name = "example.com."
  dnssec_config {
    state = "on"
    default_key_specs {
      algorithm  = %q
      key_length = 2048
      key_type   = %q
      kind       = "dnsKeySpec"
    }
  }
}`, algorithm, keyType)
}
