package lint

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// GCP rules backing the CIS Google Cloud Platform Foundation Benchmark
// (compliance/cis_gcp.go). The ones marked optIn are Level 2
// recommendations or checks the catalog templates and fresh starters don't
// follow (flow logs, OS Login metadata, Cloud SQL backups and flags, CMEK,
// ...): they stay out of Problems until the user turns them on, and
// compliance grading runs them anyway.
func init() {
	rules = append(rules,
		gcpRule(info("bp_gcp_sa_key", "GCP: no user-managed service account keys", CategorySecurity,
			"The design creates no google_service_account_key: workloads use attached service accounts, impersonation or Workload Identity Federation (CIS GCP 1.5)."),
			"https://cloud.google.com/iam/docs/best-practices-for-managing-service-account-keys", false, checkGCPServiceAccountKey),
		gcpRule(warning("bp_gcp_sa_admin_role", "GCP: service accounts have no admin roles", CategorySecurity,
			"Project, folder and organization IAM grants don't give a service account roles/owner, roles/editor or an *Admin role (CIS GCP 1.6)."),
			"https://cloud.google.com/iam/docs/best-practices-service-accounts", false, checkGCPSAAdminRole),
		gcpRule(info("bp_gcp_sa_user_project_role", "GCP: users impersonate service accounts one by one", CategorySecurity,
			"Users, groups and domains don't get roles/iam.serviceAccountUser or roles/iam.serviceAccountTokenCreator on the whole project; grant them on the service account instead (CIS GCP 1.7)."),
			"https://cloud.google.com/iam/docs/best-practices-service-accounts", false, checkGCPSAUserProjectRole),
		gcpRule(warning("bp_gcp_kms_public", "GCP: KMS keys are not public", CategorySecurity,
			"Key ring and crypto key IAM grants don't include allUsers or allAuthenticatedUsers (CIS GCP 1.10)."),
			"https://cloud.google.com/kms/docs/iam", false, checkGCPKMSPublic),
		gcpRule(info("bp_gcp_kms_rotation", "GCP: KMS keys rotate within 90 days", CategorySecurity,
			"Symmetric google_kms_crypto_key resources set rotation_period to 7776000s (90 days) or less (CIS GCP 1.11)."),
			"https://cloud.google.com/kms/docs/key-rotation", false, checkGCPKMSRotation),
		gcpRule(info("bp_gcp_apikey_api_targets", "GCP: API keys are restricted to the APIs they call", CategorySecurity,
			"google_apikeys_key resources list restrictions.api_targets (and not the cloudapis.googleapis.com catch-all) (CIS GCP 1.15)."),
			"https://cloud.google.com/docs/authentication/api-keys#api_key_restrictions", true, checkGCPAPIKeyTargets),
		gcpRule(info("bp_gcp_audit_logs", "GCP: audit logs cover all services", CategorySecurity,
			"Declared IAM audit configs enable ADMIN_READ, DATA_READ and DATA_WRITE for allServices with no exempted members (CIS GCP 2.1)."),
			"https://cloud.google.com/logging/docs/audit/configure-data-access", false, checkGCPAuditLogs),
		gcpRule(info("bp_gcp_log_bucket_lock", "GCP: log export buckets are locked", CategorySecurity,
			"Cloud Storage buckets that log sinks export to have a locked retention policy (Bucket Lock) (CIS GCP 2.4)."),
			"https://cloud.google.com/storage/docs/bucket-lock", true, checkGCPLogBucketLock),
		gcpRule(info("bp_gcp_dns_logging", "GCP: VPC networks log DNS queries", CategorySecurity,
			"Every VPC network is attached to a google_dns_policy with enable_logging = true (CIS GCP 2.13)."),
			"https://cloud.google.com/dns/docs/monitoring", true, checkGCPDNSLogging),
		gcpRule(info("bp_gcp_lb_logging", "GCP: HTTP(S) load balancers log requests", CategorySecurity,
			"Backend services of external HTTP(S) load balancers enable log_config (CIS GCP 2.17)."),
			"https://cloud.google.com/load-balancing/docs/https/https-logging-monitoring", true, checkGCPLBLogging),
		gcpRule(info("bp_gcp_default_network", "GCP: the default network is not used", CategorySecurity,
			"Projects created in the design set auto_create_network = false and no resource uses the \"default\" VPC network (CIS GCP 3.1)."),
			"https://cloud.google.com/vpc/docs/vpc#default-network", true, checkGCPDefaultNetwork),
		gcpRule(info("bp_gcp_dnssec", "GCP: public DNS zones sign with DNSSEC", CategorySecurity,
			"Public google_dns_managed_zone resources set dnssec_config { state = \"on\" } (CIS GCP 3.3)."),
			"https://cloud.google.com/dns/docs/dnssec-config", false, checkGCPDNSSEC),
		gcpRule(info("bp_gcp_dnssec_ksk_rsasha1", "GCP: DNSSEC key-signing keys don't use RSASHA1", CategorySecurity,
			"DNSSEC default_key_specs of type keySigning use an algorithm other than rsasha1 (CIS GCP 3.4)."),
			"https://cloud.google.com/dns/docs/dnssec-advanced#algorithms", false, gcpCheckDNSSECAlgorithm("keySigning")),
		gcpRule(info("bp_gcp_dnssec_zsk_rsasha1", "GCP: DNSSEC zone-signing keys don't use RSASHA1", CategorySecurity,
			"DNSSEC default_key_specs of type zoneSigning use an algorithm other than rsasha1 (CIS GCP 3.5)."),
			"https://cloud.google.com/dns/docs/dnssec-advanced#algorithms", false, gcpCheckDNSSECAlgorithm("zoneSigning")),
		gcpRule(info("bp_gcp_flow_logs", "GCP: subnets have VPC flow logs", CategorySecurity,
			"Subnets enable log_config with a 5-second aggregation interval, flow_sampling = 1, all metadata and no filter (CIS GCP 3.10)."),
			"https://cloud.google.com/vpc/docs/using-flow-logs", true, checkGCPFlowLogs),
		gcpRule(info("bp_gcp_block_project_ssh_keys", "GCP: VMs block project-wide SSH keys", CategorySecurity,
			"Instances and instance templates set metadata block-project-ssh-keys = \"true\" (CIS GCP 4.3)."),
			"https://cloud.google.com/compute/docs/connect/restrict-ssh-keys", true, checkGCPBlockProjectSSHKeys),
		gcpRule(info("bp_gcp_os_login", "GCP: VMs use OS Login", CategorySecurity,
			"OS Login is on for every instance: enable-oslogin = \"TRUE\" in the project metadata of the design or the VM's metadata, and no VM turns it off (CIS GCP 4.4)."),
			"https://cloud.google.com/compute/docs/oslogin/set-up-oslogin", true, checkGCPOSLogin),
		gcpRule(info("bp_gcp_serial_port", "GCP: serial port access is off", CategorySecurity,
			"Instances, instance templates and project metadata don't set serial-port-enable to true (CIS GCP 4.5)."),
			"https://cloud.google.com/compute/docs/troubleshooting/troubleshooting-using-serial-console", false, checkGCPSerialPort),
		gcpRule(info("bp_gcp_ip_forwarding", "GCP: VMs don't forward IP packets", CategorySecurity,
			"Instances and instance templates don't set can_ip_forward = true (CIS GCP 4.6)."),
			"https://cloud.google.com/vpc/docs/using-routes#canipforward", false, checkGCPIPForwarding),
		gcpRule(info("bp_gcp_shielded_vm", "GCP: Shielded VM stays on", CategorySecurity,
			"Instances and instance templates don't turn off vTPM or integrity monitoring in shielded_instance_config (CIS GCP 4.8)."),
			"https://cloud.google.com/compute/shielded-vm/docs/shielded-vm", true, checkGCPShieldedVM),
		gcpRule(info("bp_gcp_default_sa", "GCP: VMs don't run as the default service account", CategorySecurity,
			"Instances and instance templates attach a dedicated service account, not the Compute Engine default one (CIS GCP 4.1)."),
			"https://cloud.google.com/compute/docs/access/service-accounts#default_service_account", false, checkGCPDefaultSA),
		gcpRule(warning("bp_gcp_default_sa_full_access", "GCP: no default service account with full API access", CategorySecurity,
			"Instances and instance templates don't combine the Compute Engine default service account with the cloud-platform scope (CIS GCP 4.2)."),
			"https://cloud.google.com/compute/docs/access/service-accounts#scopes_best_practice", false, checkGCPDefaultSAFullAccess),
		gcpRule(info("bp_gcp_instance_public_ip", "GCP: VMs have no external IP", CategorySecurity,
			"Instance network interfaces have no access_config (external IPv4 address) (CIS GCP 4.9)."),
			"https://cloud.google.com/compute/docs/ip-addresses#externaladdresses", true, checkGCPInstancePublicIP),
		gcpRule(info("bp_gcp_bucket_uniform_access", "GCP: buckets use uniform bucket-level access", CategorySecurity,
			"google_storage_bucket resources set uniform_bucket_level_access = true so only IAM grants access, not per-object ACLs (CIS GCP 5.2)."),
			"https://cloud.google.com/storage/docs/uniform-bucket-level-access", false, checkGCPBucketUniformAccess),
		gcpRule(info("bp_gcp_sql_ssl", "GCP: Cloud SQL requires encrypted connections", CategorySecurity,
			"Cloud SQL instances set settings.ip_configuration.ssl_mode to ENCRYPTED_ONLY or TRUSTED_CLIENT_CERTIFICATE_REQUIRED (CIS GCP 6.4)."),
			"https://cloud.google.com/sql/docs/postgres/configure-ssl-instance", false, checkGCPSQLSSL),
		gcpRule(warning("bp_gcp_sql_open_network", "GCP: Cloud SQL is not open to the internet", CategorySecurity,
			"Cloud SQL authorized_networks don't include 0.0.0.0/0 (CIS GCP 6.5)."),
			"https://cloud.google.com/sql/docs/mysql/authorize-networks", false, checkGCPSQLOpenNetwork),
		gcpRule(info("bp_gcp_sql_public_ip", "GCP: Cloud SQL has no public IP", CategorySecurity,
			"Primary Cloud SQL instances set settings.ip_configuration.ipv4_enabled = false and connect through a private network (CIS GCP 6.7)."),
			"https://cloud.google.com/sql/docs/mysql/configure-private-ip", true, checkGCPSQLPublicIP),
		gcpRule(info("bp_gcp_sql_backups", "GCP: Cloud SQL has automated backups", CategorySecurity,
			"Primary Cloud SQL instances set settings.backup_configuration.enabled = true (CIS GCP 6.8)."),
			"https://cloud.google.com/sql/docs/mysql/backup-recovery/backups", true, checkGCPSQLBackups),
		gcpRule(warning("bp_gcp_bigquery_public", "GCP: BigQuery datasets are not public", CategorySecurity,
			"Dataset access entries and dataset IAM grants don't include allUsers or allAuthenticatedUsers (CIS GCP 7.1)."),
			"https://cloud.google.com/bigquery/docs/control-access-to-resources-iam", false, checkGCPBigQueryPublic),
		gcpRule(info("bp_gcp_bigquery_table_cmek", "GCP: BigQuery tables use customer-managed keys", CategorySecurity,
			"BigQuery tables set encryption_configuration.kms_key_name or belong to a dataset with a default CMEK (CIS GCP 7.2)."),
			"https://cloud.google.com/bigquery/docs/customer-managed-encryption", true, checkGCPBigQueryTableCMEK),
		gcpRule(info("bp_gcp_bigquery_dataset_cmek", "GCP: BigQuery datasets have a default customer-managed key", CategorySecurity,
			"BigQuery datasets set default_encryption_configuration.kms_key_name (CIS GCP 7.3)."),
			"https://cloud.google.com/bigquery/docs/customer-managed-encryption", true, checkGCPBigQueryDatasetCMEK),
		gcpRule(info("bp_gcp_dataproc_cmek", "GCP: Dataproc clusters use customer-managed keys", CategorySecurity,
			"Dataproc clusters set cluster_config.encryption_config.kms_key_name (CIS GCP 8.1)."),
			"https://cloud.google.com/dataproc/docs/concepts/configuring-clusters/customer-managed-encryption", true, checkGCPDataprocCMEK),
	)
	for _, f := range gcpSQLFlags {
		rules = append(rules, f.rule())
	}
}

func gcpRule(r model.BestPracticeRule, docs string, optIn bool, check func(*pass)) rule {
	r.Docs = docs
	r.DefaultOff = optIn
	return rule{r, check}
}

// gcpInstanceTypes are the resources that describe a VM's metadata, network
// interfaces and service account.
var gcpInstanceTypes = []string{"google_compute_instance", "google_compute_instance_template", "google_compute_region_instance_template"}

// gcpMemberRE finds the IAM members in an attribute's source: allUsers,
// allAuthenticatedUsers or a "kind:" prefix (user:, serviceAccount:, ...).
// Reading the source keeps members built from interpolations
// ("serviceAccount:${google_service_account.app.email}").
var gcpMemberRE = regexp.MustCompile(`"(allUsers|allAuthenticatedUsers|[A-Za-z]+:)`)

// gcpMembers returns the member kinds granted in attrs of b.
func gcpMembers(b *model.Body, attrs ...string) []string {
	var out []string
	for _, name := range attrs {
		if a := getAttr(b, name); a != nil {
			for _, m := range gcpMemberRE.FindAllStringSubmatch(source(a.Value), -1) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// gcpPublicMember returns the first public member (allUsers,
// allAuthenticatedUsers) granted in attrs of b, or "".
func gcpPublicMember(b *model.Body, attrs ...string) string {
	for _, m := range gcpMembers(b, attrs...) {
		if m == "allUsers" || m == "allAuthenticatedUsers" {
			return m
		}
	}
	return ""
}

// gcpTruthy reads a GCP metadata or flag value ("true", "TRUE", "1", true).
func gcpTruthy(v any) (on, known bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case float64:
		return t != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "on":
			return true, true
		case "false", "0", "off":
			return false, true
		}
	}
	return false, false
}

// gcpMetadata returns the constant metadata map of b; ok is false when
// metadata is set but not constant (merge(), locals), so it can't be read.
func gcpMetadata(b *model.Body) (m map[string]any, ok bool) {
	a := getAttr(b, "metadata")
	if a == nil {
		return nil, true
	}
	m, ok = literal(a.Value).(map[string]any)
	return m, ok
}

// gcpMetadataFlag reads metadata key of b: set is false when absent or not
// constant.
func gcpMetadataFlag(b *model.Body, key string) (on, set bool) {
	m, _ := gcpMetadata(b)
	v, found := m[key]
	if !found {
		return false, false
	}
	return gcpTruthy(v)
}

// gcpRefNode returns the node v refers to first (a resource or data source).
func (p *pass) gcpRefNode(v model.Value) *model.Node {
	for _, r := range references(v) {
		parts := strings.Split(r, ".")
		for k := len(parts); k >= 2; k-- {
			if n := p.byAddr[strings.Join(parts[:k], ".")]; n != nil {
				return n
			}
		}
	}
	return nil
}

// gcpFirstBody returns the body of the first typ block of b, or nil.
func gcpFirstBody(b *model.Body, typ string) *model.Body {
	if bs := blocks(b, typ); len(bs) > 0 {
		return &bs[0].Body
	}
	return nil
}

// ---------------------------------------------------------------------------
// IAM (CIS 1.5, 1.6, 1.7, 1.10, 1.11, 1.15)

func checkGCPServiceAccountKey(p *pass) {
	for _, n := range p.ofType("google_service_account_key") {
		p.report(finding{
			node: n, attribute: "service_account_id",
			message: fmt.Sprintf("%s creates a user-managed service account key", addr(n)),
			why:     "User-managed keys are long-lived credentials: they leak through repositories, laptops and CI logs, and Terraform stores the private key in its state. Google-managed keys rotate on their own and never leave Google.",
			fix:     "Attach the service account to the workload (VM, Cloud Run, GKE Workload Identity), use Workload Identity Federation for CI and other clouds, or impersonate the account; then remove the key.",
		})
	}
}

var gcpIAMGrantTypes = []string{
	"google_project_iam_member", "google_project_iam_binding",
	"google_folder_iam_member", "google_folder_iam_binding",
	"google_organization_iam_member", "google_organization_iam_binding",
}

func gcpAdminRole(role string) bool {
	return role == "roles/owner" || role == "roles/editor" || strings.HasSuffix(strings.ToLower(role), "admin")
}

func checkGCPSAAdminRole(p *pass) {
	for _, n := range p.ofType(gcpIAMGrantTypes...) {
		role, _ := attrString(&n.Body, "role")
		if !gcpAdminRole(role) || !slices.Contains(gcpMembers(&n.Body, "member", "members"), "serviceAccount:") {
			continue
		}
		p.report(finding{
			node: n, attribute: "role",
			message: fmt.Sprintf("%s grants %s to a service account", addr(n), role),
			why:     "A service account with owner, editor or an admin role can change almost anything; whoever runs code as it (a compromised VM, a leaked key, a user allowed to impersonate it) gets the same power.",
			fix:     "Grant the service account only the predefined or custom roles its workload needs (e.g. roles/storage.objectViewer), not owner, editor or *Admin roles.",
		})
	}
}

func checkGCPSAUserProjectRole(p *pass) {
	for _, n := range p.ofType("google_project_iam_member", "google_project_iam_binding") {
		role, _ := attrString(&n.Body, "role")
		if role != "roles/iam.serviceAccountUser" && role != "roles/iam.serviceAccountTokenCreator" {
			continue
		}
		human := slices.ContainsFunc(gcpMembers(&n.Body, "member", "members"), func(m string) bool {
			return m == "user:" || m == "group:" || m == "domain:"
		})
		if !human {
			continue
		}
		p.report(finding{
			node: n, attribute: "role",
			message: fmt.Sprintf("%s grants %s on the whole project", addr(n), role),
			why:     "At project level the role lets its members act as every service account in the project, including ones with far more access than they have.",
			fix:     "Grant the role on the specific service account (google_service_account_iam_member) the user needs to impersonate.",
		})
	}
}

func checkGCPKMSPublic(p *pass) {
	for _, n := range p.ofType("google_kms_crypto_key_iam_member", "google_kms_crypto_key_iam_binding", "google_kms_key_ring_iam_member", "google_kms_key_ring_iam_binding") {
		if m := gcpPublicMember(&n.Body, "member", "members"); m != "" {
			p.report(finding{
				node: n, attribute: "member",
				message: fmt.Sprintf("%s grants a KMS role to %s", addr(n), m),
				why:     "Anyone on the internet (or any Google account) could use or manage the key and so decrypt or destroy the data it protects.",
				fix:     "Grant KMS roles to specific users, groups or service accounts.",
			})
		}
	}
}

// gcpMaxRotation is 90 days in seconds.
const gcpMaxRotation = 90 * 24 * 3600

func checkGCPKMSRotation(p *pass) {
	for _, n := range p.ofType("google_kms_crypto_key") {
		b := &n.Body
		// Asymmetric and MAC keys can't rotate automatically.
		if purpose, ok := attrString(b, "purpose"); ok && purpose != "ENCRYPT_DECRYPT" {
			continue
		}
		a := getAttr(b, "rotation_period")
		if a == nil {
			p.report(finding{
				node: n, attribute: "rotation_period",
				message: fmt.Sprintf("%s never rotates", addr(n)),
				why:     "Without rotation one key version encrypts data for years; rotating limits how much data a compromised version exposes.",
				fix:     "Set rotation_period = \"7776000s\" (90 days) or less.",
			})
			continue
		}
		s, ok := literal(a.Value).(string)
		if !ok {
			continue
		}
		secs, err := strconv.ParseFloat(strings.TrimSuffix(s, "s"), 64)
		if err != nil || secs <= gcpMaxRotation {
			continue
		}
		p.report(finding{
			node: n, attribute: "rotation_period",
			message: fmt.Sprintf("%s rotates every %.0f days", addr(n), secs/86400),
			why:     "Rotating at least every 90 days limits how much data a compromised key version exposes.",
			fix:     "Set rotation_period = \"7776000s\" (90 days) or less.",
		})
	}
}

func checkGCPAPIKeyTargets(p *pass) {
	for _, n := range p.ofType("google_apikeys_key") {
		var services []string
		restricted := false
		if r := gcpFirstBody(&n.Body, "restrictions"); r != nil {
			for _, t := range blocks(r, "api_targets") {
				restricted = true
				services = append(services, attrStrings(&t.Body, "service")...)
			}
		}
		if restricted && !slices.Contains(services, "cloudapis.googleapis.com") {
			continue
		}
		p.report(finding{
			node: n, attribute: "restrictions",
			message: fmt.Sprintf("%s can call every Google API", addr(n)),
			why:     "API keys are bearer secrets embedded in apps and web pages; an unrestricted key that leaks can be used against any API enabled in the project.",
			fix:     "Add restrictions { api_targets { service = \"<api>.googleapis.com\" } } for each API the key calls.",
		})
	}
}

// ---------------------------------------------------------------------------
// Logging (CIS 2.1, 2.4, 2.13, 2.17)

func checkGCPAuditLogs(p *pass) {
	configs := p.ofType("google_project_iam_audit_config", "google_folder_iam_audit_config", "google_organization_iam_audit_config")
	allServices := slices.ContainsFunc(configs, func(n *model.Node) bool {
		s, _ := attrString(&n.Body, "service")
		return s == "allServices"
	})
	why := "Data access audit logs record who read or changed data; without them an incident can't be investigated. Exempted members are invisible in the logs."
	for _, n := range configs {
		service, _ := attrString(&n.Body, "service")
		if !allServices {
			p.report(finding{
				node: n, attribute: "service",
				message: fmt.Sprintf("%s configures audit logs for %s only", addr(n), service),
				why:     why,
				fix:     "Add an audit config with service = \"allServices\" and audit_log_config blocks for ADMIN_READ, DATA_READ and DATA_WRITE.",
			})
			continue
		}
		types := map[string]bool{}
		exempted := false
		for _, c := range blocks(&n.Body, "audit_log_config") {
			t, _ := attrString(&c.Body, "log_type")
			types[t] = true
			exempted = exempted || getAttr(&c.Body, "exempted_members") != nil
		}
		var missing []string
		if service == "allServices" {
			for _, t := range []string{"ADMIN_READ", "DATA_READ", "DATA_WRITE"} {
				if !types[t] {
					missing = append(missing, t)
				}
			}
		}
		switch {
		case len(missing) > 0:
			p.report(finding{
				node: n, attribute: "audit_log_config",
				message: fmt.Sprintf("%s doesn't log %s for all services", addr(n), strings.Join(missing, ", ")),
				why:     why,
				fix:     "Add audit_log_config blocks with log_type ADMIN_READ, DATA_READ and DATA_WRITE.",
			})
		case exempted:
			p.report(finding{
				node: n, attribute: "audit_log_config",
				message: fmt.Sprintf("%s exempts members from audit logging", addr(n)),
				why:     why,
				fix:     "Remove exempted_members so every principal is logged.",
			})
		}
	}
}

func checkGCPLogBucketLock(p *pass) {
	for _, n := range p.ofType("google_logging_project_sink", "google_logging_folder_sink", "google_logging_organization_sink", "google_logging_billing_account_sink") {
		a := getAttr(&n.Body, "destination")
		if a == nil || !strings.Contains(source(a.Value), "storage.googleapis.com/") {
			continue
		}
		bucket := p.gcpRefNode(a.Value)
		if bucket == nil || bucket.Type != "google_storage_bucket" {
			continue
		}
		if rp := gcpFirstBody(&bucket.Body, "retention_policy"); rp != nil && attrNotFalse(rp, "is_locked") {
			continue
		}
		p.report(finding{
			node: n, attribute: "destination",
			message: fmt.Sprintf("%s exports logs to %s, which has no locked retention policy", addr(n), addr(bucket)),
			why:     "An attacker (or a mistake) with access to the bucket could delete the exported logs and with them the trail of what happened; a locked retention policy makes them undeletable for the period.",
			fix:     fmt.Sprintf("Add retention_policy { retention_period = <seconds> is_locked = true } to %s (locking can't be undone).", addr(bucket)),
		})
	}
}

func checkGCPDNSLogging(p *pass) {
	policies := p.ofType("google_dns_policy")
	for _, n := range p.ofType("google_compute_network") {
		logged := slices.ContainsFunc(policies, func(pol *model.Node) bool {
			if !attrIsTrue(&pol.Body, "enable_logging") {
				return false
			}
			for _, nw := range blocks(&pol.Body, "networks") {
				if a := getAttr(&nw.Body, "network_url"); a != nil && referencesNode(a.Value, n) {
					return true
				}
			}
			return false
		})
		if logged {
			continue
		}
		p.report(finding{
			node:    n,
			message: fmt.Sprintf("%s has no DNS policy with logging", addr(n)),
			why:     "DNS query logs show which domains workloads resolve: they reveal malware calling home and data exfiltration over DNS.",
			fix:     fmt.Sprintf("Add a google_dns_policy with enable_logging = true and networks { network_url = %s.id }.", addr(n)),
		})
	}
}

func checkGCPLBLogging(p *pass) {
	for _, n := range p.ofType("google_compute_backend_service", "google_compute_region_backend_service") {
		b := &n.Body
		scheme, schemeOK := attrString(b, "load_balancing_scheme")
		protocol, protoOK := attrString(b, "protocol")
		if n.Type == "google_compute_backend_service" {
			// Global backend services default to an external HTTP load balancer.
			if getAttr(b, "load_balancing_scheme") == nil {
				scheme, schemeOK = "EXTERNAL", true
			}
			if getAttr(b, "protocol") == nil {
				protocol, protoOK = "HTTP", true
			}
		}
		if !schemeOK || !protoOK || (scheme != "EXTERNAL" && scheme != "EXTERNAL_MANAGED") ||
			(protocol != "HTTP" && protocol != "HTTPS" && protocol != "HTTP2") {
			continue
		}
		if lc := gcpFirstBody(b, "log_config"); lc != nil && attrNotFalse(lc, "enable") {
			continue
		}
		p.report(finding{
			node: n, attribute: "log_config",
			message: fmt.Sprintf("%s doesn't log load balancer requests", addr(n)),
			why:     "Request logs of an internet-facing load balancer are the record of who called the app; they are needed to investigate attacks and debug outages.",
			fix:     "Add log_config { enable = true sample_rate = 1 }.",
		})
	}
}

// ---------------------------------------------------------------------------
// Networking (CIS 3.1, 3.3-3.5, 3.10)

func (p *pass) gcpDefaultNetwork(v model.Value) bool {
	if s, ok := literal(v).(string); ok {
		return s == "default" || strings.HasSuffix(s, "/networks/default")
	}
	n := p.gcpRefNode(v)
	if n == nil || n.Kind != model.KindData || n.Type != "google_compute_network" {
		return false
	}
	name, _ := attrString(&n.Body, "name")
	return name == "default"
}

func checkGCPDefaultNetwork(p *pass) {
	why := "The default network has a subnet in every region and firewall rules allowing SSH, RDP and ICMP from anywhere, plus internal traffic between everything; designs should use their own VPC with explicit rules."
	for _, n := range p.resources() {
		if !strings.HasPrefix(n.Type, "google_") {
			continue
		}
		switch n.Type {
		case "google_project":
			if hasLiteralFalse(&n.Body, "auto_create_network") {
				continue
			}
			if a := getAttr(&n.Body, "auto_create_network"); a != nil && literal(a.Value) == nil {
				continue
			}
			p.report(finding{
				node: n, attribute: "auto_create_network",
				message: fmt.Sprintf("%s creates the default network", addr(n)),
				why:     why,
				fix:     "Set auto_create_network = false and create a custom VPC (google_compute_network with auto_create_subnetworks = false).",
			})
			continue
		case "google_compute_network":
			if name, _ := attrString(&n.Body, "name"); name == "default" {
				p.report(finding{
					node: n, attribute: "name",
					message: fmt.Sprintf("%s is the default network", addr(n)),
					why:     why,
					fix:     "Give the network its own name and explicit firewall rules.",
				})
			}
			continue
		}
		reported := false
		walkAttrs(&n.Body, "", func(path string, a *model.Attr) {
			if reported || (a.Name != "network" && a.Name != "private_network") || !p.gcpDefaultNetwork(a.Value) {
				return
			}
			reported = true
			p.report(finding{
				node: n, attribute: path,
				message: fmt.Sprintf("%s uses the default network", addr(n)),
				why:     why,
				fix:     "Attach it to a custom VPC network defined in the design.",
			})
		})
	}
}

func checkGCPDNSSEC(p *pass) {
	for _, n := range p.ofType("google_dns_managed_zone") {
		if vis, _ := attrString(&n.Body, "visibility"); vis == "private" {
			continue // DNSSEC applies to public zones only
		}
		if d := gcpFirstBody(&n.Body, "dnssec_config"); d != nil {
			if a := getAttr(d, "state"); a != nil {
				if s, ok := literal(a.Value).(string); !ok || s == "on" {
					continue
				}
			}
		}
		p.report(finding{
			node: n, attribute: "dnssec_config",
			message: fmt.Sprintf("%s is not signed with DNSSEC", addr(n)),
			why:     "Without DNSSEC resolvers can't tell a forged answer from a real one, so DNS spoofing and cache poisoning can redirect your users.",
			fix:     "Add dnssec_config { state = \"on\" } and add the DS record at your registrar.",
		})
	}
}

// gcpCheckDNSSECAlgorithm flags RSASHA1 for DNSSEC keys of keyType
// (keySigning or zoneSigning).
func gcpCheckDNSSECAlgorithm(keyType string) func(*pass) {
	return func(p *pass) {
		for _, n := range p.ofType("google_dns_managed_zone") {
			d := gcpFirstBody(&n.Body, "dnssec_config")
			if d == nil {
				continue
			}
			for i, spec := range blocks(d, "default_key_specs") {
				kt, _ := attrString(&spec.Body, "key_type")
				alg, _ := attrString(&spec.Body, "algorithm")
				if kt != keyType || !strings.EqualFold(alg, "rsasha1") {
					continue
				}
				p.report(finding{
					node: n, attribute: fmt.Sprintf("dnssec_config[0].default_key_specs[%d].algorithm", i),
					message: fmt.Sprintf("%s signs with RSASHA1 (%s key)", addr(n), keyType),
					why:     "SHA-1 is broken for collision resistance; signatures made with it can be forged with practical effort.",
					fix:     "Use algorithm = \"rsasha256\" (or ecdsap256sha256) for the key.",
				})
			}
		}
	}
}

func checkGCPFlowLogs(p *pass) {
	for _, n := range p.ofType("google_compute_subnetwork") {
		b := &n.Body
		// Proxy-only, PSC and NAT subnets don't support flow logs.
		if a := getAttr(b, "purpose"); a != nil {
			if s, ok := literal(a.Value).(string); !ok || s != "PRIVATE" {
				continue
			}
		}
		why := "Flow logs record the traffic in and out of the subnet's VMs: they are the evidence for spotting scans, exfiltration and unexpected connections. CIS asks for every flow (5-second aggregation, 100% sampling, all metadata)."
		lc := gcpFirstBody(b, "log_config")
		if lc == nil {
			p.report(finding{
				node: n, attribute: "log_config",
				message: fmt.Sprintf("%s has no VPC flow logs", addr(n)),
				why:     why,
				fix:     "Add log_config { aggregation_interval = \"INTERVAL_5_SEC\" flow_sampling = 1 metadata = \"INCLUDE_ALL_METADATA\" }.",
			})
			continue
		}
		var off []string
		if s, ok := attrString(lc, "aggregation_interval"); ok && s != "INTERVAL_5_SEC" {
			off = append(off, "aggregation_interval = \""+s+"\"")
		}
		if getAttr(lc, "flow_sampling") == nil {
			off = append(off, "flow_sampling defaults to 0.5")
		} else if f, ok := attrNumber(lc, "flow_sampling"); ok && f != 1 {
			off = append(off, fmt.Sprintf("flow_sampling = %g", f))
		}
		if s, ok := attrString(lc, "metadata"); ok && s != "INCLUDE_ALL_METADATA" {
			off = append(off, "metadata = \""+s+"\"")
		}
		if s, ok := attrString(lc, "filter_expr"); ok && s != "true" {
			off = append(off, "a filter_expr")
		}
		if len(off) == 0 {
			continue
		}
		p.report(finding{
			node: n, attribute: "log_config",
			message: fmt.Sprintf("%s logs only part of its flows (%s)", addr(n), strings.Join(off, ", ")),
			why:     why,
			fix:     "In log_config set aggregation_interval = \"INTERVAL_5_SEC\", flow_sampling = 1, metadata = \"INCLUDE_ALL_METADATA\" and no filter_expr.",
		})
	}
}

// ---------------------------------------------------------------------------
// Virtual machines (CIS 4.1-4.6, 4.8, 4.9)

func checkGCPBlockProjectSSHKeys(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		if _, ok := gcpMetadata(&n.Body); !ok {
			continue
		}
		if on, _ := gcpMetadataFlag(&n.Body, "block-project-ssh-keys"); on {
			continue
		}
		p.report(finding{
			node: n, attribute: "metadata",
			message: fmt.Sprintf("%s accepts project-wide SSH keys", addr(n)),
			why:     "A project-wide SSH key opens every VM in the project; one leaked key then reaches all of them.",
			fix:     "Add block-project-ssh-keys = \"true\" to metadata and use OS Login or instance-level keys.",
		})
	}
}

// gcpProjectMetadata returns the project metadata nodes of the design that
// set key, split by value.
func (p *pass) gcpProjectMetadata(key string) (on, off []*model.Node) {
	for _, n := range p.ofType("google_compute_project_metadata", "google_compute_project_metadata_item") {
		var v bool
		var known bool
		if n.Type == "google_compute_project_metadata" {
			v, known = gcpMetadataFlag(&n.Body, key)
		} else if k, _ := attrString(&n.Body, "key"); k == key {
			if a := getAttr(&n.Body, "value"); a != nil {
				v, known = gcpTruthy(literal(a.Value))
			}
		}
		switch {
		case known && v:
			on = append(on, n)
		case known:
			off = append(off, n)
		}
	}
	return on, off
}

func checkGCPOSLogin(p *pass) {
	why := "OS Login ties SSH access to IAM identities: access is granted and revoked with roles, keys don't outlive their users, and logins are audited."
	projectOn, projectOff := p.gcpProjectMetadata("enable-oslogin")
	for _, n := range projectOff {
		p.report(finding{
			node: n, attribute: "metadata",
			message: fmt.Sprintf("%s turns OS Login off for the project", addr(n)),
			why:     why,
			fix:     "Set enable-oslogin = \"TRUE\" in the project metadata.",
		})
	}
	for _, n := range p.ofType(gcpInstanceTypes...) {
		if _, ok := gcpMetadata(&n.Body); !ok {
			continue
		}
		on, set := gcpMetadataFlag(&n.Body, "enable-oslogin")
		switch {
		case set && on, !set && len(projectOn) > 0:
			continue
		case set:
			p.report(finding{
				node: n, attribute: "metadata",
				message: fmt.Sprintf("%s turns OS Login off", addr(n)),
				why:     why,
				fix:     "Remove enable-oslogin = \"FALSE\" from the metadata (or set it to \"TRUE\").",
			})
		default:
			p.report(finding{
				node: n, attribute: "metadata",
				message: fmt.Sprintf("%s doesn't enable OS Login", addr(n)),
				why:     why,
				fix:     "Add enable-oslogin = \"TRUE\" to the metadata, or set it in a google_compute_project_metadata(_item) of the design.",
			})
		}
	}
}

func checkGCPSerialPort(p *pass) {
	report := func(n *model.Node) {
		p.report(finding{
			node: n, attribute: "metadata",
			message: fmt.Sprintf("%s enables interactive serial port access", addr(n)),
			why:     "The serial console accepts connections from any IP address and bypasses the firewall rules of the network.",
			fix:     "Remove serial-port-enable from the metadata (it is off by default) or set it to \"false\".",
		})
	}
	on, _ := p.gcpProjectMetadata("serial-port-enable")
	for _, n := range on {
		report(n)
	}
	for _, n := range p.ofType(gcpInstanceTypes...) {
		if v, set := gcpMetadataFlag(&n.Body, "serial-port-enable"); set && v {
			report(n)
		}
	}
}

func checkGCPIPForwarding(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		if attrIsTrue(&n.Body, "can_ip_forward") {
			p.report(finding{
				node: n, attribute: "can_ip_forward",
				message: fmt.Sprintf("%s forwards IP packets", addr(n)),
				why:     "With IP forwarding the VM can send and receive packets for other addresses, so a compromised VM can route around network controls.",
				fix:     "Set can_ip_forward = false unless the VM is a NAT or network appliance (then document the exception); use Cloud NAT for egress.",
			})
		}
	}
}

func checkGCPShieldedVM(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		sc := gcpFirstBody(&n.Body, "shielded_instance_config")
		if sc == nil {
			continue // vTPM and integrity monitoring are on by default
		}
		var off []string
		for _, f := range []string{"enable_vtpm", "enable_integrity_monitoring"} {
			if hasLiteralFalse(sc, f) {
				off = append(off, f)
			}
		}
		if len(off) == 0 {
			continue
		}
		p.report(finding{
			node: n, attribute: "shielded_instance_config",
			message: fmt.Sprintf("%s turns off %s", addr(n), strings.Join(off, " and ")),
			why:     "Shielded VM's vTPM and integrity monitoring detect boot-level malware and rootkits that change the VM's boot sequence.",
			fix:     "Set enable_vtpm = true and enable_integrity_monitoring = true (and enable_secure_boot = true when the image allows it).",
		})
	}
}

// gcpDefaultSA: a service_account block uses the Compute Engine default
// service account (email unset, the default address or its data source).
func gcpDefaultSA(sa *model.Body) bool {
	a := getAttr(sa, "email")
	if a == nil {
		return true
	}
	if strings.Contains(source(a.Value), "-compute@developer.gserviceaccount.com") {
		return true
	}
	return slices.ContainsFunc(references(a.Value), func(r string) bool {
		return strings.HasPrefix(r, "data.google_compute_default_service_account.")
	})
}

func checkGCPDefaultSA(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		for i, sa := range blocks(&n.Body, "service_account") {
			if !gcpDefaultSA(&sa.Body) {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("service_account[%d].email", i),
				message: fmt.Sprintf("%s runs as the Compute Engine default service account", addr(n)),
				why:     "The default service account is shared by every VM of the project and often holds the Editor role, so one compromised VM can reach everything the others can.",
				fix:     "Create a google_service_account for this workload with only the roles it needs and set service_account { email = google_service_account.<name>.email }.",
			})
		}
	}
}

func checkGCPDefaultSAFullAccess(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		for i, sa := range blocks(&n.Body, "service_account") {
			scopes := attrStrings(&sa.Body, "scopes")
			full := slices.Contains(scopes, "cloud-platform") || slices.Contains(scopes, "https://www.googleapis.com/auth/cloud-platform")
			if !full || !gcpDefaultSA(&sa.Body) {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("service_account[%d].scopes", i),
				message: fmt.Sprintf("%s gives the default service account full access to all Cloud APIs", addr(n)),
				why:     "Any process on the VM can then use every permission of the default service account (usually Editor on the project).",
				fix:     "Use a dedicated service account with only the roles it needs (scopes = [\"cloud-platform\"] is fine then), or restrict the scopes.",
			})
		}
	}
}

func checkGCPInstancePublicIP(p *pass) {
	for _, n := range p.ofType(gcpInstanceTypes...) {
		for i, ni := range blocks(&n.Body, "network_interface") {
			if len(blocks(&ni.Body, "access_config")) == 0 {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("network_interface[%d].access_config", i),
				message: fmt.Sprintf("%s has an external IP address", addr(n)),
				why:     "A VM with an external IP can be reached from the internet as soon as a firewall rule allows it; private VMs behind a load balancer, IAP or Cloud NAT expose much less.",
				fix:     "Remove access_config; reach the VM through Identity-Aware Proxy or a load balancer and give it egress with Cloud NAT.",
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Storage (CIS 5.2)

func checkGCPBucketUniformAccess(p *pass) {
	for _, n := range p.ofType("google_storage_bucket") {
		if attrNotFalse(&n.Body, "uniform_bucket_level_access") {
			continue
		}
		p.report(finding{
			node: n, attribute: "uniform_bucket_level_access",
			message: fmt.Sprintf("%s allows per-object ACLs", addr(n)),
			why:     "With fine-grained ACLs any object can be shared on its own, outside the bucket's IAM policy; access becomes hard to audit and easy to leak.",
			fix:     "Set uniform_bucket_level_access = true.",
		})
	}
}

// ---------------------------------------------------------------------------
// Cloud SQL (CIS 6.1-6.5, 6.7, 6.8)

// gcpSQLSettings returns the settings and ip_configuration bodies of a
// Cloud SQL instance (nil when absent).
func gcpSQLSettings(n *model.Node) (settings, ip *model.Body) {
	settings = gcpFirstBody(&n.Body, "settings")
	if settings != nil {
		ip = gcpFirstBody(settings, "ip_configuration")
	}
	return settings, ip
}

// gcpSQLReplica: read replicas inherit backups and connectivity from their
// primary.
func gcpSQLReplica(n *model.Node) bool { return getAttr(&n.Body, "master_instance_name") != nil }

func checkGCPSQLSSL(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		settings, ip := gcpSQLSettings(n)
		if settings == nil {
			continue
		}
		var mode string
		if ip != nil {
			a := getAttr(ip, "ssl_mode")
			if a != nil {
				s, ok := literal(a.Value).(string)
				if !ok || s == "ENCRYPTED_ONLY" || s == "TRUSTED_CLIENT_CERTIFICATE_REQUIRED" {
					continue
				}
				mode = s
			}
		}
		msg := fmt.Sprintf("%s accepts unencrypted connections", addr(n))
		if mode != "" {
			msg = fmt.Sprintf("%s accepts unencrypted connections (ssl_mode = %q)", addr(n), mode)
		}
		p.report(finding{
			node: n, attribute: "settings[0].ip_configuration[0].ssl_mode",
			message: msg,
			why:     "Unencrypted database connections expose credentials and query results to anyone who can observe the traffic.",
			fix:     "Set ssl_mode = \"ENCRYPTED_ONLY\" (or \"TRUSTED_CLIENT_CERTIFICATE_REQUIRED\") in settings.ip_configuration.",
		})
	}
}

func checkGCPSQLOpenNetwork(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		_, ip := gcpSQLSettings(n)
		if ip == nil {
			continue
		}
		for i, an := range blocks(ip, "authorized_networks") {
			v, _ := attrString(&an.Body, "value")
			if !isOpenCIDR(v) && v != "0.0.0.0" {
				continue
			}
			p.report(finding{
				node: n, attribute: fmt.Sprintf("settings[0].ip_configuration[0].authorized_networks[%d].value", i),
				message: fmt.Sprintf("%s allows connections from %s", addr(n), v),
				why:     "The database's public IP then accepts connections from the whole internet; it is scanned and brute-forced within minutes.",
				fix:     "Authorize only your office/VPN CIDRs, or remove the public IP and connect through private IP or the Cloud SQL Auth Proxy.",
			})
		}
	}
}

func checkGCPSQLPublicIP(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		settings, ip := gcpSQLSettings(n)
		if settings == nil || gcpSQLReplica(n) {
			continue
		}
		if ip != nil {
			if a := getAttr(ip, "ipv4_enabled"); a != nil {
				if v, set := attrBool(ip, "ipv4_enabled"); !set || !v {
					continue
				}
			}
		}
		p.report(finding{
			node: n, attribute: "settings[0].ip_configuration[0].ipv4_enabled",
			message: fmt.Sprintf("%s has a public IP address", addr(n)),
			why:     "A public IP puts the database on the internet: only authorized networks and its credentials stand between it and attackers.",
			fix:     "Set ipv4_enabled = false and private_network = <VPC id> in settings.ip_configuration (needs private services access: google_service_networking_connection).",
		})
	}
}

func checkGCPSQLBackups(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		settings, _ := gcpSQLSettings(n)
		if settings == nil || gcpSQLReplica(n) {
			continue
		}
		if bc := gcpFirstBody(settings, "backup_configuration"); bc != nil && attrNotFalse(bc, "enabled") {
			continue
		}
		p.report(finding{
			node: n, attribute: "settings[0].backup_configuration",
			message: fmt.Sprintf("%s has no automated backups", addr(n)),
			why:     "Without automated backups a bad migration, a deleted table or ransomware loses the data for good.",
			fix:     "Add backup_configuration { enabled = true } to settings (add point_in_time_recovery_enabled = true for PostgreSQL, binary_log_enabled = true for MySQL).",
		})
	}
}

// gcpSQLFlag is a CIS database flag recommendation for one engine.
type gcpSQLFlag struct {
	code, cis, title string
	engine           string // database_version prefix: MYSQL, POSTGRES, SQLSERVER
	flag             string
	def              string   // value Cloud SQL uses when the flag is unset ("" = the check needs it set)
	allowed          []string // compliant values (lower case); empty = the flag must not be set
	want             string   // recommended value for the fix
	why, docs        string
}

var gcpSQLFlags = []gcpSQLFlag{
	{"bp_gcp_sql_mysql_skip_show_database", "6.1.2", "GCP: MySQL hides databases from SHOW DATABASES", "MYSQL", "skip_show_database", "off", []string{"on"}, "on",
		"Users without the SHOW DATABASES privilege then can't list databases they can't use, which reveals less to a compromised account.", "https://cloud.google.com/sql/docs/mysql/flags"},
	{"bp_gcp_sql_mysql_local_infile", "6.1.3", "GCP: MySQL disables LOAD DATA LOCAL", "MYSQL", "local_infile", "", []string{"off"}, "off",
		"LOAD DATA LOCAL lets a malicious server or injected SQL read files from the client machine.", "https://cloud.google.com/sql/docs/mysql/flags"},
	{"bp_gcp_sql_pg_log_error_verbosity", "6.2.1", "GCP: PostgreSQL logs errors at DEFAULT verbosity or terser", "POSTGRES", "log_error_verbosity", "default", []string{"default", "terse"}, "default",
		"VERBOSE error logs include source details and may leak sensitive data into the logs.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_connections", "6.2.2", "GCP: PostgreSQL logs connections", "POSTGRES", "log_connections", "off", []string{"on"}, "on",
		"Connection logs show who connected and from where, the starting point for investigating unauthorized access.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_disconnections", "6.2.3", "GCP: PostgreSQL logs disconnections", "POSTGRES", "log_disconnections", "off", []string{"on"}, "on",
		"Disconnection logs give the end and length of every session, completing the connection audit trail.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_statement", "6.2.4", "GCP: PostgreSQL logs DDL statements", "POSTGRES", "log_statement", "none", []string{"ddl", "mod", "all"}, "ddl",
		"Logging DDL (schema changes) records who changed tables, roles and permissions; CIS recommends ddl.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_min_messages", "6.2.5", "GCP: PostgreSQL logs warnings and above", "POSTGRES", "log_min_messages", "warning", []string{"warning", "error", "log", "fatal", "panic"}, "warning",
		"Below WARNING the logs fill with debug noise that hides the messages that matter.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_min_error_statement", "6.2.6", "GCP: PostgreSQL logs statements that cause errors", "POSTGRES", "log_min_error_statement", "error", []string{"error", "log", "fatal", "panic"}, "error",
		"Logging the statements behind errors helps spot SQL injection attempts and broken clients without logging every query.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_log_min_duration_statement", "6.2.7", "GCP: PostgreSQL doesn't log statement durations", "POSTGRES", "log_min_duration_statement", "-1", []string{"-1"}, "-1",
		"Duration logging writes full statements, which can include passwords and personal data, into the logs.", "https://cloud.google.com/sql/docs/postgres/flags"},
	{"bp_gcp_sql_pg_pgaudit", "6.2.8", "GCP: PostgreSQL enables pgAudit", "POSTGRES", "cloudsql.enable_pgaudit", "off", []string{"on"}, "on",
		"pgAudit writes detailed session and object audit logs to Cloud Logging, as many audits require.", "https://cloud.google.com/sql/docs/postgres/pg-audit"},
	{"bp_gcp_sql_sqlserver_external_scripts", "6.3.1", "GCP: SQL Server disables external scripts", "SQLSERVER", "external scripts enabled", "off", []string{"off"}, "off",
		"External scripts (R, Python) run code outside the database engine's security boundary.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_cross_db_chaining", "6.3.2", "GCP: SQL Server disables cross-database ownership chaining", "SQLSERVER", "cross db ownership chaining", "off", []string{"off"}, "off",
		"Ownership chaining across databases lets a user of one database reach objects in another without permissions there.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_user_connections", "6.3.3", "GCP: SQL Server doesn't cap user connections", "SQLSERVER", "user connections", "0", []string{"0"}, "0",
		"A low connection cap lets a few clients lock everyone else out (denial of service).", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_user_options", "6.3.4", "GCP: SQL Server leaves user options unset", "SQLSERVER", "user options", "", nil, "",
		"Global user options change the behavior of every session and can disable safety settings.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_remote_access", "6.3.5", "GCP: SQL Server disables remote access", "SQLSERVER", "remote access", "on", []string{"off"}, "off",
		"Remote access lets stored procedures run on and from remote servers, a path for abuse and lateral movement.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_trace_3625", "6.3.6", "GCP: SQL Server masks error details (trace flag 3625)", "SQLSERVER", "3625", "off", []string{"on"}, "on",
		"Trace flag 3625 hides parameters in error messages from non-sysadmin users, so errors don't leak data.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
	{"bp_gcp_sql_sqlserver_contained_db_auth", "6.3.7", "GCP: SQL Server disables contained database authentication", "SQLSERVER", "contained database authentication", "off", []string{"off"}, "off",
		"Contained database users authenticate inside the database, bypassing server-level login policies.", "https://cloud.google.com/sql/docs/sqlserver/flags"},
}

func (f gcpSQLFlag) rule() rule {
	engine := map[string]string{"MYSQL": "MySQL", "POSTGRES": "PostgreSQL", "SQLSERVER": "SQL Server"}[f.engine]
	desc := fmt.Sprintf("Cloud SQL %s instances set the %q database flag to %s (CIS GCP %s).", engine, f.flag, f.want, f.cis)
	if len(f.allowed) == 0 {
		desc = fmt.Sprintf("Cloud SQL %s instances don't set the %q database flag (CIS GCP %s).", engine, f.flag, f.cis)
	}
	return gcpRule(info(f.code, f.title, CategorySecurity, desc), f.docs, true, f.check)
}

func (f gcpSQLFlag) check(p *pass) {
	for _, n := range p.ofType("google_sql_database_instance") {
		version, _ := attrString(&n.Body, "database_version")
		settings, _ := gcpSQLSettings(n)
		if !strings.HasPrefix(version, f.engine) || settings == nil {
			continue
		}
		// Flags from dynamic blocks can't be read.
		if slices.ContainsFunc(blocks(settings, "dynamic"), func(b *model.Block) bool { return slices.Contains(b.Labels, "database_flags") }) {
			continue
		}
		value, set, known := f.def, false, true
		for _, fl := range blocks(settings, "database_flags") {
			if name, _ := attrString(&fl.Body, "name"); name != f.flag {
				continue
			}
			set = true
			a := getAttr(&fl.Body, "value")
			if a == nil {
				break
			}
			switch v := literal(a.Value).(type) {
			case string:
				value = strings.ToLower(strings.TrimSpace(v))
			case float64:
				value = strconv.FormatFloat(v, 'f', -1, 64)
			default:
				known = false
			}
		}
		if !known {
			continue
		}
		var msg, fix string
		switch {
		case len(f.allowed) == 0 && !set:
			continue
		case len(f.allowed) == 0:
			msg = fmt.Sprintf("%s sets the %q database flag", addr(n), f.flag)
			fix = fmt.Sprintf("Remove the %q database_flags block from settings.", f.flag)
		case slices.Contains(f.allowed, value):
			continue
		case set:
			msg = fmt.Sprintf("%s sets %s = %q", addr(n), f.flag, value)
		case f.def == "":
			msg = fmt.Sprintf("%s doesn't set the %s flag", addr(n), f.flag)
		default:
			msg = fmt.Sprintf("%s leaves %s at its default (%s)", addr(n), f.flag, f.def)
		}
		if fix == "" {
			fix = fmt.Sprintf("Add database_flags { name = %q value = %q } to settings.", f.flag, f.want)
		}
		p.report(finding{node: n, attribute: "settings[0].database_flags", message: msg, why: f.why, fix: fix})
	}
}

// ---------------------------------------------------------------------------
// BigQuery and Dataproc (CIS 7.1-7.3, 8.1)

func checkGCPBigQueryPublic(p *pass) {
	why := "Anyone on the internet (or with any Google account) could query the dataset, and pay-per-query billing may land on you."
	for _, n := range p.ofType("google_bigquery_dataset", "google_bigquery_dataset_access", "google_bigquery_dataset_iam_member", "google_bigquery_dataset_iam_binding") {
		bodies := []*model.Body{&n.Body}
		paths := []string{""}
		if n.Type == "google_bigquery_dataset" {
			bodies, paths = nil, nil
			for i, a := range blocks(&n.Body, "access") {
				bodies = append(bodies, &a.Body)
				paths = append(paths, fmt.Sprintf("access[%d]", i))
			}
		}
		for i, b := range bodies {
			m := gcpPublicMember(b, "member", "members", "special_group", "iam_member")
			if m == "" {
				continue
			}
			p.report(finding{
				node: n, attribute: paths[i],
				message: fmt.Sprintf("%s grants dataset access to %s", addr(n), m),
				why:     why,
				fix:     "Grant access to specific users, groups, service accounts or authorized views.",
			})
			break
		}
	}
}

func gcpHasKMSKey(b *model.Body, block string) bool {
	c := gcpFirstBody(b, block)
	return c != nil && getAttr(c, "kms_key_name") != nil
}

func checkGCPBigQueryTableCMEK(p *pass) {
	for _, n := range p.ofType("google_bigquery_table") {
		b := &n.Body
		// Views store no data; external tables keep it in the source.
		if len(blocks(b, "view")) > 0 || len(blocks(b, "external_data_configuration")) > 0 || gcpHasKMSKey(b, "encryption_configuration") {
			continue
		}
		if ds := p.referencedNode(b, "dataset_id"); ds != nil && ds.Type == "google_bigquery_dataset" && gcpHasKMSKey(&ds.Body, "default_encryption_configuration") {
			continue
		}
		p.report(finding{
			node: n, attribute: "encryption_configuration",
			message: fmt.Sprintf("%s is encrypted with a Google-managed key", addr(n)),
			why:     "A customer-managed key (Cloud KMS) lets you control, rotate, audit and revoke access to the data's encryption key.",
			fix:     "Add encryption_configuration { kms_key_name = google_kms_crypto_key.<name>.id }, or set a default CMEK on its dataset.",
		})
	}
}

func checkGCPBigQueryDatasetCMEK(p *pass) {
	for _, n := range p.ofType("google_bigquery_dataset") {
		if gcpHasKMSKey(&n.Body, "default_encryption_configuration") {
			continue
		}
		p.report(finding{
			node: n, attribute: "default_encryption_configuration",
			message: fmt.Sprintf("%s has no default customer-managed key", addr(n)),
			why:     "Without a default CMEK new tables are encrypted with Google-managed keys you can't rotate, audit or revoke.",
			fix:     "Add default_encryption_configuration { kms_key_name = google_kms_crypto_key.<name>.id } (the BigQuery service agent needs roles/cloudkms.cryptoKeyEncrypterDecrypter on the key).",
		})
	}
}

func checkGCPDataprocCMEK(p *pass) {
	for _, n := range p.ofType("google_dataproc_cluster") {
		if cc := gcpFirstBody(&n.Body, "cluster_config"); cc != nil && gcpHasKMSKey(cc, "encryption_config") {
			continue
		}
		p.report(finding{
			node: n, attribute: "cluster_config",
			message: fmt.Sprintf("%s encrypts its disks with a Google-managed key", addr(n)),
			why:     "A customer-managed key (Cloud KMS) lets you control, rotate, audit and revoke access to the cluster's data.",
			fix:     "Add cluster_config { encryption_config { kms_key_name = google_kms_crypto_key.<name>.id } }.",
		})
	}
}
