package compliance

import (
	"encoding/json"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// CIS Google Cloud Platform Foundation Benchmark v5.0.0. Recommendation numbers and titles follow the benchmark.
// Controls whose evidence lives in the organization or the project outside the design (Cloud Identity accounts, MFA,
// organization policies, log-based alerts, API key usage, processes) are manual: a design declaring some of them
// doesn't prove the rest of the organization does. Automatic controls only judge the resources in the design.
const (
	gcpIAM     = "Identity and Access Management"
	gcpLogging = "Logging and Monitoring"
	gcpNetwork = "Networking"
	gcpVM      = "Virtual Machines"
	gcpStorage = "Storage"
	gcpSQL     = "Cloud SQL Database Services"
	gcpBQ      = "BigQuery"
	gcpProc    = "Dataproc"
	gcpConsole = "Check it in the Google Cloud console (or with gcloud / Security Command Center), then attest it. "
	gcpAlert   = "Create a log-based metric (google_logging_metric) with the benchmark's filter and a google_monitoring_alert_policy on it with a notification channel, in every project (or once on an aggregated sink). The filter: "
	gcpFlag    = "Nimbo reads settings.database_flags of the Cloud SQL instances in the design (an unset flag counts with Cloud SQL's default; flags from dynamic blocks can't be read and pass). "
)

var (
	gcpInstances = []string{"google_compute_instance", "google_compute_instance_template", "google_compute_region_instance_template"}
	gcpSQLDB     = []string{"google_sql_database_instance"}
	gcpIAMGrants = []string{
		"google_project_iam_member", "google_project_iam_binding",
		"google_folder_iam_member", "google_folder_iam_binding",
		"google_organization_iam_member", "google_organization_iam_binding",
	}
	gcpMySQL     = gcpSQLEngine("MYSQL")
	gcpPostgres  = gcpSQLEngine("POSTGRES")
	gcpSQLServer = gcpSQLEngine("SQLSERVER")
)

// gcpSQLEngine narrows a database-flag control to Cloud SQL instances whose database_version starts with prefix, so a
// PostgreSQL instance is not applicable to the SQL Server flags instead of "met". A version that is an expression can't
// be read and applies.
func gcpSQLEngine(prefix string) func(*model.Node) bool {
	return func(n *model.Node) bool {
		for _, a := range n.Body.Attrs {
			if a.Name != "database_version" {
				continue
			}
			raw := a.Value.Literal
			if len(raw) == 0 && a.Value.Expr != "" {
				raw, _ = hclcodec.LiteralFromExpr(a.Value.Expr)
			}
			var v string
			if json.Unmarshal(raw, &v) != nil {
				return true
			}
			return strings.HasPrefix(v, prefix)
		}
		return true
	}
}

func init() {
	Register(Framework{
		ID:       "cis-gcp-5.0",
		Name:     "CIS Google Cloud Platform Foundation Benchmark",
		Version:  "5.0.0",
		Platform: "google",
		Source:   "https://www.cisecurity.org/benchmark/google_cloud_computing_platform",
		Controls: []Control{
			// 1 Identity and Access Management
			{ID: "1.1.1", Title: "Ensure Super Admin Email Address Is Not Tied To A Single User", Section: gcpIAM, Manual: true,
				Guidance: "Check it in the Google Admin console, then attest it. Use a dedicated super admin account (not a person's daily account), with at least two break-glass super admins protected by security keys."},
			{ID: "1.1.2", Title: "Ensure Super Admin Account Is Not Used For Google Cloud Administration", Section: gcpIAM, Manual: true,
				Guidance: "Check it in the Google Admin console and the organization IAM policy, then attest it. Super admins manage Cloud Identity only; grant Organization Administrator to a separate group for Google Cloud."},
			{ID: "1.1.3", Title: "Ensure Folders Are Structured By Environment And Sensitivity", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Group projects in folders per environment (prod, non-prod) and data sensitivity so IAM and organization policies are applied per folder."},
			{ID: "1.1.4", Title: "Ensure Organization Policies Are Configured For Centralized Constraints", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Set organization policy constraints at the organization or folder level (for example iam.disableServiceAccountKeyCreation, compute.requireOsLogin, storage.uniformBucketLevelAccess, sql.restrictPublicIp)."},
			{ID: "1.2", Title: "Ensure that Corporate Login Credentials are Used", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "IAM members must be accounts of your Cloud Identity / Workspace domain, not personal Gmail accounts; enforce it with the iam.allowedPolicyMemberDomains constraint."},
			{ID: "1.3", Title: "Ensure that Multi-Factor Authentication is 'Enabled' for All Non-Service Accounts", Section: gcpIAM, Manual: true,
				Guidance: "Check it in the Google Admin console (Security > 2-Step Verification), then attest it. Enforce 2-Step Verification for every user."},
			{ID: "1.4", Title: "Ensure that Security Key Enforcement is Enabled for All Admin Accounts", Section: gcpIAM, Manual: true,
				Guidance: "Check it in the Google Admin console, then attest it. Require security keys as the 2-Step Verification method for users with Organization Administrator or super admin roles."},
			{ID: "1.5", Title: "Ensure That There Are Only GCP-Managed Service Account Keys for Each Service Account", Section: gcpIAM,
				Rules: []string{"bp_gcp_sa_key"}, AppliesTo: []string{"google_service_account", "google_service_account_key"},
				Guidance: "Nimbo flags every google_service_account_key in the design (each one is a user-managed key). Keys created outside the design aren't seen; the iam.disableServiceAccountKeyCreation organization policy prevents them."},
			{ID: "1.6", Title: "Ensure That Service Account Has No Admin Privileges", Section: gcpIAM,
				Rules: []string{"bp_gcp_sa_admin_role"}, AppliesTo: gcpIAMGrants,
				Guidance: "Nimbo flags project, folder and organization IAM members/bindings in the design that give a serviceAccount: member roles/owner, roles/editor or a role ending in Admin. Grants made outside the design, through google_*_iam_policy or with members from variables aren't seen."},
			{ID: "1.7", Title: "Ensure That IAM Users Are Not Assigned the Service Account User or Service Account Token Creator Roles at Project Level", Section: gcpIAM,
				Rules: []string{"bp_gcp_sa_user_project_role"}, AppliesTo: []string{"google_project_iam_member", "google_project_iam_binding"},
				Guidance: "Nimbo flags project-level grants of roles/iam.serviceAccountUser or roles/iam.serviceAccountTokenCreator to user:, group: or domain: members in the design. Grant them on the service account (google_service_account_iam_member) instead."},
			{ID: "1.8", Title: "Ensure User-Managed/External Keys for Service Accounts Are Rotated Every 90 Days or Fewer", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "List user-managed keys (gcloud iam service-accounts keys list --managed-by=user) and rotate any older than 90 days. With no user-managed keys (control 1.5) there is nothing to rotate."},
			{ID: "1.9", Title: "Ensure That Separation of Duties Is Enforced While Assigning Service Account Related Roles to Users", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "No user should hold both roles/iam.serviceAccountAdmin and roles/iam.serviceAccountUser; review the project IAM policy."},
			{ID: "1.10", Title: "Ensure That Cloud KMS Cryptokeys Are Not Anonymously or Publicly Accessible", Section: gcpIAM,
				Rules: []string{"bp_gcp_kms_public"}, AppliesTo: []string{"google_kms_key_ring", "google_kms_crypto_key", "google_kms_key_ring_iam_member", "google_kms_key_ring_iam_binding", "google_kms_crypto_key_iam_member", "google_kms_crypto_key_iam_binding"},
				Guidance: "Nimbo flags key ring and crypto key IAM members/bindings in the design granted to allUsers or allAuthenticatedUsers. Grants made outside the design or through *_iam_policy resources aren't seen."},
			{ID: "1.11", Title: "Ensure KMS Encryption Keys Are Rotated Within a Period of 90 Days", Section: gcpIAM,
				Rules: []string{"bp_gcp_kms_rotation"}, AppliesTo: []string{"google_kms_crypto_key"},
				Guidance: "Nimbo checks that every symmetric google_kms_crypto_key in the design sets rotation_period to 7776000s (90 days) or less. Asymmetric and MAC keys can't rotate automatically and are skipped."},
			{ID: "1.12", Title: "Ensure That Separation of Duties Is Enforced While Assigning KMS Related Roles to Users", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "No user should hold roles/cloudkms.admin together with roles/cloudkms.cryptoKeyEncrypterDecrypter, cryptoKeyEncrypter or cryptoKeyDecrypter."},
			{ID: "1.13", Title: "Ensure API Keys Only Exist for Active Services", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Delete API keys nobody uses (APIs & Services > Credentials); prefer service accounts or OAuth over API keys."},
			{ID: "1.14", Title: "Ensure API Keys Are Restricted To Use by Only Specified Hosts and Apps", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Give every API key an application restriction: restrictions.browser_key_restrictions (referrers), server_key_restrictions (IPs), android_key_restrictions or ios_key_restrictions in google_apikeys_key."},
			{ID: "1.15", Title: "Ensure API Keys Are Restricted to Only APIs That Application Needs Access", Section: gcpIAM,
				Rules: []string{"bp_gcp_apikey_api_targets"}, AppliesTo: []string{"google_apikeys_key"},
				Guidance: "Nimbo checks that every google_apikeys_key in the design lists restrictions.api_targets and none of them is the cloudapis.googleapis.com catch-all. Keys created outside the design aren't seen."},
			{ID: "1.16", Title: "Ensure API Keys Are Rotated Every 90 Days", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Regenerate API keys older than 90 days and update their clients."},
			{ID: "1.17", Title: "Ensure Essential Contacts is Configured for Organization", Section: gcpIAM, Manual: true,
				Guidance: gcpConsole + "Configure Essential Contacts for the organization (google_essential_contacts_contact with parent = \"organizations/<id>\") for at least the Legal, Security, Suspension, Technical and Technical Incidents categories."},
			{ID: "1.18", Title: "Ensure Secrets are Not Stored in Cloud Functions Environment Variables by Using Secret Manager", Section: gcpIAM, Manual: true,
				Guidance: "Review the environment variables of the Cloud Functions in the design and outside it, then attest it. Keep secrets in Secret Manager and reference them with secret_environment_variables (google_cloudfunctions_function) or service_config.secret_environment_variables (google_cloudfunctions2_function)."},

			// 2 Logging and Monitoring
			{ID: "2.1", Title: "Ensure That Cloud Audit Logging Is Configured Properly", Section: gcpLogging,
				Rules: []string{"bp_gcp_audit_logs"}, AppliesTo: []string{"google_project_iam_audit_config", "google_folder_iam_audit_config", "google_organization_iam_audit_config"},
				Guidance: "Nimbo checks the IAM audit configs declared in the design: one for allServices with ADMIN_READ, DATA_READ and DATA_WRITE and no exempted members. Without audit configs in the design the control is not applicable; audit configs set on the organization or folder outside it must be checked in the console."},
			{ID: "2.2", Title: "Ensure Google Workspace/Cloud Identity Data Sharing with Google Cloud is Enabled for Admin Audit Logging", Section: gcpLogging, Manual: true,
				Guidance: "Check it in the Google Admin console (Account > Account settings > Legal and compliance > Sharing options), then attest it."},
			{ID: "2.3", Title: "Ensure That Sinks Are Configured for All Log Entries", Section: gcpLogging, Manual: true,
				Guidance: gcpConsole + "Export every log entry with a sink without filter, ideally at the organization level (google_logging_organization_sink with include_children = true) to a bucket, BigQuery dataset or Pub/Sub topic."},
			{ID: "2.4", Title: "Ensure That Retention Policies on Cloud Storage Buckets Used for Exporting Logs Are Configured Using Bucket Lock", Section: gcpLogging,
				Rules: []string{"bp_gcp_log_bucket_lock"}, AppliesTo: []string{"google_logging_project_sink", "google_logging_folder_sink", "google_logging_organization_sink", "google_logging_billing_account_sink"},
				Guidance: "Nimbo checks that log sinks in the design exporting to a google_storage_bucket of the design point at a bucket with retention_policy { is_locked = true }. Buckets outside the design aren't seen."},
			{ID: "2.5", Title: "Ensure Log Metric Filter and Alerts Exist for Project Ownership Assignments/Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "(protoPayload.serviceName=\"cloudresourcemanager.googleapis.com\") AND (ProjectOwnership OR projectOwnerInvitee) OR (protoPayload.serviceData.policyDelta.bindingDeltas.action=\"REMOVE\" AND protoPayload.serviceData.policyDelta.bindingDeltas.role=\"roles/owner\") OR (protoPayload.serviceData.policyDelta.bindingDeltas.action=\"ADD\" AND protoPayload.serviceData.policyDelta.bindingDeltas.role=\"roles/owner\")."},
			{ID: "2.6", Title: "Ensure That the Log Metric Filter and Alerts Exist for Audit Configuration Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "protoPayload.methodName=\"SetIamPolicy\" AND protoPayload.serviceData.policyDelta.auditConfigDeltas:*."},
			{ID: "2.7", Title: "Ensure That the Log Metric Filter and Alerts Exist for Custom Role Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "resource.type=\"iam_role\" AND (protoPayload.methodName=\"google.iam.admin.v1.CreateRole\" OR protoPayload.methodName=\"google.iam.admin.v1.DeleteRole\" OR protoPayload.methodName=\"google.iam.admin.v1.UpdateRole\")."},
			{ID: "2.8", Title: "Ensure That the Log Metric Filter and Alerts Exist for VPC Network Firewall Rule Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "resource.type=\"gce_firewall_rule\" AND (protoPayload.methodName:\"compute.firewalls.patch\" OR protoPayload.methodName:\"compute.firewalls.insert\" OR protoPayload.methodName:\"compute.firewalls.delete\")."},
			{ID: "2.9", Title: "Ensure That the Log Metric Filter and Alerts Exist for VPC Network Route Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "resource.type=\"gce_route\" AND (protoPayload.methodName:\"compute.routes.delete\" OR protoPayload.methodName:\"compute.routes.insert\")."},
			{ID: "2.10", Title: "Ensure That the Log Metric Filter and Alerts Exist for VPC Network Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "resource.type=\"gce_network\" AND (protoPayload.methodName:\"compute.networks.insert\" OR protoPayload.methodName:\"compute.networks.patch\" OR protoPayload.methodName:\"compute.networks.delete\" OR protoPayload.methodName:\"compute.networks.removePeering\" OR protoPayload.methodName:\"compute.networks.addPeering\")."},
			{ID: "2.11", Title: "Ensure That the Log Metric Filter and Alerts Exist for Cloud Storage IAM Permission Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "resource.type=\"gcs_bucket\" AND protoPayload.methodName=\"storage.setIamPermissions\"."},
			{ID: "2.12", Title: "Ensure That the Log Metric Filter and Alerts Exist for SQL Instance Configuration Changes", Section: gcpLogging, Manual: true,
				Guidance: gcpAlert + "protoPayload.methodName=\"cloudsql.instances.update\"."},
			{ID: "2.13", Title: "Ensure That Cloud DNS Logging Is Enabled for All VPC Networks", Section: gcpLogging,
				Rules: []string{"bp_gcp_dns_logging"}, AppliesTo: []string{"google_compute_network"},
				Guidance: "Nimbo checks that every google_compute_network in the design is listed (networks.network_url) in a google_dns_policy with enable_logging = true. Networks outside the design aren't seen."},
			{ID: "2.14", Title: "Ensure Cloud Asset Inventory Is Enabled", Section: gcpLogging, Manual: true,
				Guidance: gcpConsole + "Enable the Cloud Asset API in every project (google_project_service with service = \"cloudasset.googleapis.com\")."},
			{ID: "2.15", Title: "Ensure 'Access Transparency' is 'Enabled'", Section: gcpLogging, Manual: true,
				Guidance: gcpConsole + "Enable Access Transparency for the organization (IAM & Admin > Settings); it needs a supported support plan."},
			{ID: "2.16", Title: "Ensure 'Access Approval' is 'Enabled'", Section: gcpLogging, Manual: true,
				Guidance: gcpConsole + "Enable Access Approval for the organization or projects (google_organization_access_approval_settings / google_project_access_approval_settings) with notification emails."},
			{ID: "2.17", Title: "Ensure Logging is enabled for HTTP(S) Load Balancer", Section: gcpLogging,
				Rules: []string{"bp_gcp_lb_logging"}, AppliesTo: []string{"google_compute_backend_service", "google_compute_region_backend_service"},
				Guidance: "Nimbo checks that backend services of external HTTP(S) load balancers in the design (EXTERNAL or EXTERNAL_MANAGED, protocol HTTP, HTTPS or HTTP2) set log_config { enable = true }. Internal and TCP/SSL backend services are skipped."},

			// 3 Networking
			{ID: "3.1", Title: "Ensure That the Default Network Does Not Exist in a Project", Section: gcpNetwork,
				Rules: []string{"bp_gcp_default_network"}, AppliesTo: []string{
					"google_project", "google_compute_network", "google_compute_subnetwork", "google_compute_firewall",
					"google_compute_instance", "google_compute_instance_template", "google_compute_region_instance_template",
					"google_compute_router", "google_compute_route", "google_compute_forwarding_rule", "google_compute_global_address",
					"google_container_cluster", "google_sql_database_instance", "google_vpc_access_connector", "google_service_networking_connection",
				},
				Guidance: "Nimbo flags google_project resources that don't set auto_create_network = false and resources that use the network named \"default\". A default network already existing in a project not created by this design isn't seen: delete it in the console (gcloud compute networks delete default) and enforce compute.skipDefaultNetworkCreation."},
			{ID: "3.2", Title: "Ensure Legacy Networks Do Not Exist for Older Projects", Section: gcpNetwork, Manual: true,
				Guidance: gcpConsole + "Legacy (non-subnet) networks can no longer be created, so a design can't add one; list networks with gcloud compute networks list and migrate any in legacy mode."},
			{ID: "3.3", Title: "Ensure That DNSSEC Is Enabled for Cloud DNS", Section: gcpNetwork,
				Rules: []string{"bp_gcp_dnssec"}, AppliesTo: []string{"google_dns_managed_zone"},
				Guidance: "Nimbo checks that public google_dns_managed_zone resources in the design set dnssec_config { state = \"on\" }. Private zones don't support DNSSEC and pass."},
			{ID: "3.4", Title: "Ensure That RSASHA1 Is Not Used for the Key-Signing Key in Cloud DNS DNSSEC", Section: gcpNetwork,
				Rules: []string{"bp_gcp_dnssec_ksk_rsasha1"}, AppliesTo: []string{"google_dns_managed_zone"},
				Guidance: "Nimbo flags managed zones whose dnssec_config.default_key_specs of key_type keySigning use algorithm rsasha1. Zones without explicit key specs use Cloud DNS's default (RSASHA256) and pass."},
			{ID: "3.5", Title: "Ensure That RSASHA1 Is Not Used for the Zone-Signing Key in Cloud DNS DNSSEC", Section: gcpNetwork,
				Rules: []string{"bp_gcp_dnssec_zsk_rsasha1"}, AppliesTo: []string{"google_dns_managed_zone"},
				Guidance: "Nimbo flags managed zones whose dnssec_config.default_key_specs of key_type zoneSigning use algorithm rsasha1. Zones without explicit key specs use Cloud DNS's default (RSASHA256) and pass."},
			{ID: "3.6", Title: "Ensure That SSH Access Is Restricted From the Internet", Section: gcpNetwork,
				Rules: []string{"bp_open_ingress"}, Counts: exposesPort(22), AppliesTo: []string{"google_compute_firewall"},
				Guidance: "Nimbo flags ingress firewall rules allowing 0.0.0.0/0 or ::/0 to port 22 (TCP/UDP ranges including it, no ports, or protocol all). Only the open-ingress findings about SSH count here. Reach VMs through Identity-Aware Proxy (source range 35.235.240.0/20) instead."},
			{ID: "3.7", Title: "Ensure That RDP Access Is Restricted From the Internet", Section: gcpNetwork,
				Rules: []string{"bp_open_ingress"}, Counts: exposesPort(3389), AppliesTo: []string{"google_compute_firewall"},
				Guidance: "Nimbo flags ingress firewall rules allowing 0.0.0.0/0 or ::/0 to port 3389 (TCP/UDP ranges including it, no ports, or protocol all). Only the open-ingress findings about RDP count here. Reach VMs through Identity-Aware Proxy (source range 35.235.240.0/20) instead."},
			{ID: "3.8", Title: "Ensure VPC Service Controls Is Enabled for Supported Google Cloud Services", Section: gcpNetwork, Manual: true,
				Guidance: gcpConsole + "Put the projects holding sensitive data in a VPC Service Controls perimeter (google_access_context_manager_service_perimeter) restricting the supported services."},
			{ID: "3.9", Title: "Ensure Private Service Connect is Used for Access to Google APIs", Section: gcpNetwork, Manual: true,
				Guidance: gcpConsole + "Reach Google APIs through a Private Service Connect endpoint (google_compute_global_address with purpose PRIVATE_SERVICE_CONNECT and a google_compute_global_forwarding_rule targeting all-apis or vpc-sc) and private DNS for googleapis.com."},
			{ID: "3.10", Title: "Ensure that VPC Flow Logs is Enabled for Every Subnet in a VPC Network", Section: gcpNetwork,
				Rules: []string{"bp_gcp_flow_logs"}, AppliesTo: []string{"google_compute_subnetwork"},
				Guidance: "Nimbo checks that every regular (PRIVATE) subnet in the design has log_config with aggregation_interval INTERVAL_5_SEC, flow_sampling = 1, metadata INCLUDE_ALL_METADATA and no filter_expr. Proxy-only, PSC and NAT subnets don't support flow logs and are skipped."},
			{ID: "3.11", Title: "Ensure No HTTPS or SSL Proxy Load Balancers Permit SSL Policies With Weak Cipher Suites", Section: gcpNetwork, Manual: true,
				Guidance: gcpConsole + "Attach to every target HTTPS/SSL proxy a google_compute_ssl_policy with profile MODERN and min_tls_version TLS_1_2, or profile RESTRICTED, or CUSTOM without weak features; proxies without an SSL policy use the GCP default, which allows TLS 1.0."},
			{ID: "3.12", Title: "Use Identity Aware Proxy (IAP) to Ensure Only Traffic From Google IP Addresses are 'Allowed'", Section: gcpNetwork, Manual: true,
				Guidance: gcpConsole + "Enable IAP on the backend services or for TCP forwarding, and allow ingress to the VMs only from 35.235.240.0/20 (IAP) and the load balancer ranges 130.211.0.0/22 and 35.191.0.0/16."},

			// 4 Virtual Machines
			{ID: "4.1", Title: "Ensure That Instances Are Not Configured To Use the Default Service Account", Section: gcpVM,
				Rules: []string{"bp_gcp_default_sa"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo flags instances and instance templates in the design whose service_account block has no email, the <number>-compute@developer.gserviceaccount.com address or the google_compute_default_service_account data source. VMs without a service_account block run with no service account and pass."},
			{ID: "4.2", Title: "Ensure That Instances Are Not Configured To Use the Default Service Account With Full Access to All Cloud APIs", Section: gcpVM,
				Rules: []string{"bp_gcp_default_sa_full_access"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo flags instances and instance templates in the design that use the Compute Engine default service account with the cloud-platform scope."},
			{ID: "4.3", Title: "Ensure “Block Project-Wide SSH Keys” Is Enabled for VM Instances", Section: gcpVM,
				Rules: []string{"bp_gcp_block_project_ssh_keys"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo checks that instances and instance templates in the design set block-project-ssh-keys = \"true\" in metadata. Metadata built from expressions (merge(), locals) can't be read and passes."},
			{ID: "4.4", Title: "Ensure Oslogin Is Enabled for a Project", Section: gcpVM,
				Rules: []string{"bp_gcp_os_login"}, AppliesTo: append([]string{"google_compute_project_metadata", "google_compute_project_metadata_item"}, gcpInstances...),
				Guidance: "Nimbo checks that each VM in the design has OS Login: enable-oslogin = \"TRUE\" in its metadata, or in a google_compute_project_metadata(_item) of the design, and that no VM or project metadata turns it off. Project metadata set outside the design isn't seen (the compute.requireOsLogin organization policy enforces it)."},
			{ID: "4.5", Title: "Ensure ‘Enable Connecting to Serial Ports’ Is Not Enabled for VM Instance", Section: gcpVM,
				Rules: []string{"bp_gcp_serial_port"}, AppliesTo: append([]string{"google_compute_project_metadata", "google_compute_project_metadata_item"}, gcpInstances...),
				Guidance: "Nimbo flags instances, instance templates and project metadata in the design that set serial-port-enable to true (it is off by default). The compute.disableSerialPortAccess organization policy enforces it."},
			{ID: "4.6", Title: "Ensure That IP Forwarding Is Not Enabled on Instances", Section: gcpVM,
				Rules: []string{"bp_gcp_ip_forwarding"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo flags instances and instance templates in the design with can_ip_forward = true. NAT or network appliances that need it are documented exceptions."},
			{ID: "4.7", Title: "Ensure VM Disks for Critical VMs Are Encrypted With Customer-Supplied Encryption Keys (CSEK)", Section: gcpVM, Manual: true,
				Guidance: "Decide which VMs are critical, then attest it. Their disks set disk_encryption_key { raw_key or rsa_encrypted_key }; note a raw CSEK in Terraform is stored in the state, so supply it from a secret store."},
			{ID: "4.8", Title: "Ensure Compute Instances Are Launched With Shielded VM Enabled", Section: gcpVM,
				Rules: []string{"bp_gcp_shielded_vm"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo flags instances and instance templates in the design that set enable_vtpm or enable_integrity_monitoring to false (both are on by default with Shielded VM images). Check that custom images support Shielded VM."},
			{ID: "4.9", Title: "Ensure That Compute Instances Do Not Have Public IP Addresses", Section: gcpVM,
				Rules: []string{"bp_gcp_instance_public_ip"}, AppliesTo: gcpInstances,
				Guidance: "Nimbo flags instances and instance templates in the design with an access_config block (external IPv4 address) on a network interface."},
			{ID: "4.10", Title: "Ensure That App Engine Applications Enforce HTTPS Connections", Section: gcpVM, Manual: true,
				Guidance: "Check the app.yaml of each App Engine service (handlers with secure: always), then attest it."},
			{ID: "4.11", Title: "Ensure That Compute Instances Have Confidential Computing Enabled", Section: gcpVM, Manual: true,
				Guidance: "Review the VMs that process sensitive data, then attest it. Confidential VMs need supported machine types (N2D, C2D, C3…) and set confidential_instance_config { enable_confidential_compute = true } (or confidential_instance_type) with on_host_maintenance = \"TERMINATE\"."},
			{ID: "4.12", Title: "Ensure the Latest Operating System Updates Are Installed On Your Virtual Machines in All Projects", Section: gcpVM, Manual: true,
				Guidance: gcpConsole + "Use VM Manager OS patch management (google_os_config_patch_deployment) or rebuild images regularly, and check the patch compliance report."},

			// 5 Storage
			{ID: "5.1", Title: "Ensure That Cloud Storage Bucket Is Not Anonymously or Publicly Accessible", Section: gcpStorage,
				Rules: []string{"bp_public_bucket"}, AppliesTo: []string{"google_storage_bucket", "google_storage_bucket_iam_member", "google_storage_bucket_iam_binding", "google_storage_bucket_access_control"},
				Guidance: "Nimbo flags bucket IAM members/bindings and bucket ACLs in the design granted to allUsers or allAuthenticatedUsers. Buckets meant to be public (a website block or a Public = \"true\" label) pass as documented exceptions; object ACLs, *_iam_policy resources and grants outside the design aren't seen."},
			{ID: "5.2", Title: "Ensure That Cloud Storage Buckets Have Uniform Bucket-Level Access Enabled", Section: gcpStorage,
				Rules: []string{"bp_gcp_bucket_uniform_access"}, AppliesTo: []string{"google_storage_bucket"},
				Guidance: "Nimbo checks that every google_storage_bucket in the design sets uniform_bucket_level_access = true."},

			// 6 Cloud SQL Database Services
			{ID: "6.1.1", Title: "Ensure That a MySQL Instance Does Not Allow Anyone To Connect With Administrative Privileges", Section: gcpSQL, Manual: true,
				Guidance: "Check the MySQL users of each instance, then attest it: root and other admin users (google_sql_user) have strong passwords and no host \"%\"; prefer IAM database authentication."},
			{ID: "6.1.2", Title: "Ensure ‘Skip_show_database’ Database Flag for Cloud SQL MySQL Instance Is Set to ‘On’", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_mysql_skip_show_database"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpMySQL,
				Guidance: gcpFlag + "MySQL instances need skip_show_database = on."},
			{ID: "6.1.3", Title: "Ensure That the ‘Local_infile’ Database Flag for a Cloud SQL MySQL Instance Is Set to ‘Off’", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_mysql_local_infile"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpMySQL,
				Guidance: gcpFlag + "MySQL instances need local_infile = off set explicitly."},
			{ID: "6.2.1", Title: "Ensure ‘Log_error_verbosity’ Database Flag for Cloud SQL PostgreSQL Instance Is Set to ‘DEFAULT’ or Stricter", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_error_verbosity"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_error_verbosity = default or terse."},
			{ID: "6.2.2", Title: "Ensure That the ‘Log_connections’ Database Flag for Cloud SQL PostgreSQL Instance Is Set to ‘On’", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_connections"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_connections = on."},
			{ID: "6.2.3", Title: "Ensure That the ‘Log_disconnections’ Database Flag for Cloud SQL PostgreSQL Instance Is Set to ‘On’", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_disconnections"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_disconnections = on."},
			{ID: "6.2.4", Title: "Ensure ‘Log_statement’ Database Flag for Cloud SQL PostgreSQL Instance Is Set Appropriately", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_statement"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_statement = ddl (the benchmark's recommendation; mod and all, which also log DDL, pass)."},
			{ID: "6.2.5", Title: "Ensure that the ‘Log_min_messages’ Flag for a Cloud SQL PostgreSQL Instance is set at minimum to 'Warning'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_min_messages"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_min_messages = warning or a higher level (error, log, fatal, panic)."},
			{ID: "6.2.6", Title: "Ensure ‘Log_min_error_statement’ Database Flag for Cloud SQL PostgreSQL Instance Is Set to ‘Error’ or Stricter", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_min_error_statement"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_min_error_statement = error or stricter (log, fatal, panic)."},
			{ID: "6.2.7", Title: "Ensure That the ‘Log_min_duration_statement’ Database Flag for Cloud SQL PostgreSQL Instance Is Set to '-1' (Disabled)", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_log_min_duration_statement"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need log_min_duration_statement = -1 (the default)."},
			{ID: "6.2.8", Title: "Ensure That 'cloudsql.enable_pgaudit' Database Flag for each Cloud Sql Postgresql Instance Is Set to 'on' For Centralized Logging", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_pg_pgaudit"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpPostgres,
				Guidance: gcpFlag + "PostgreSQL instances need cloudsql.enable_pgaudit = on; then CREATE EXTENSION pgaudit in each database and enable Data Access audit logs for Cloud SQL (control 2.1)."},
			{ID: "6.3.1", Title: "Ensure 'external scripts enabled' Database Flag for Cloud SQL SQL Server Instance Is Set to 'off'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_external_scripts"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need \"external scripts enabled\" = off (the default)."},
			{ID: "6.3.2", Title: "Ensure 'cross db ownership chaining' Database Flag for Cloud SQL SQL Server Instance Is Set to 'off'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_cross_db_chaining"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need \"cross db ownership chaining\" = off (the default)."},
			{ID: "6.3.3", Title: "Ensure 'user Connections' Database Flag for Cloud SQL SQL Server Instance Is Set to a Non-limiting Value", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_user_connections"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need \"user connections\" = 0 (unlimited, the default)."},
			{ID: "6.3.4", Title: "Ensure 'user options' Database Flag for Cloud SQL SQL Server Instance Is Not Configured", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_user_options"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances must not set \"user options\"."},
			{ID: "6.3.5", Title: "Ensure 'remote access' Database Flag for Cloud SQL SQL Server Instance Is Set to 'off'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_remote_access"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need \"remote access\" = off set explicitly (Cloud SQL defaults it to on)."},
			{ID: "6.3.6", Title: "Ensure '3625 (trace flag)' Database Flag for all Cloud SQL SQL Server Instances Is Set to 'on'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_trace_3625"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need the 3625 flag = on."},
			{ID: "6.3.7", Title: "Ensure 'contained database authentication' Database Flag for Cloud SQL SQL Server Instance Is Set to 'off'", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_sqlserver_contained_db_auth"}, AppliesTo: gcpSQLDB, AppliesToNode: gcpSQLServer,
				Guidance: gcpFlag + "SQL Server instances need \"contained database authentication\" = off (the default)."},
			{ID: "6.4", Title: "Ensure That the Cloud SQL Database Instance Requires All Incoming Connections To Use SSL", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_ssl"}, AppliesTo: gcpSQLDB,
				Guidance: "Nimbo checks that Cloud SQL instances in the design set settings.ip_configuration.ssl_mode to ENCRYPTED_ONLY or TRUSTED_CLIENT_CERTIFICATE_REQUIRED."},
			{ID: "6.5", Title: "Ensure That Cloud SQL Database Instances Do Not Implicitly Whitelist All Public IP Addresses", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_open_network"}, AppliesTo: gcpSQLDB,
				Guidance: "Nimbo flags Cloud SQL instances in the design whose settings.ip_configuration.authorized_networks include 0.0.0.0/0."},
			{ID: "6.6", Title: "Ensure Cloud SQL Database Instances Have IAM Database Authentication Enabled", Section: gcpSQL, Manual: true,
				Guidance: "Review the instances, then attest it: set the cloudsql.iam_authentication (PostgreSQL) or cloudsql_iam_authentication (MySQL) database flag to on and create IAM database users (google_sql_user with type CLOUD_IAM_USER or CLOUD_IAM_SERVICE_ACCOUNT)."},
			{ID: "6.7", Title: "Ensure That Cloud SQL Database Instances Do Not Have Public IPs", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_public_ip"}, AppliesTo: gcpSQLDB,
				Guidance: "Nimbo checks that primary Cloud SQL instances in the design set settings.ip_configuration.ipv4_enabled = false (it defaults to true). Read replicas inherit the primary's setting and are skipped."},
			{ID: "6.8", Title: "Ensure That Cloud SQL Database Instances Are Configured With Automated Backups", Section: gcpSQL,
				Rules: []string{"bp_gcp_sql_backups"}, AppliesTo: gcpSQLDB,
				Guidance: "Nimbo checks that primary Cloud SQL instances in the design set settings.backup_configuration { enabled = true }. Read replicas can't have automated backups and are skipped."},
			{ID: "6.9", Title: "Ensure Cloud SQL Database Instances Have Deletion Protection Enabled", Section: gcpSQL, Manual: true,
				Guidance: "Review the production instances, then attest it: set settings.deletion_protection_enabled = true (protection in Google Cloud, against deletes from any tool) and deletion_protection = true (Terraform's own guard)."},

			// 7 BigQuery
			{ID: "7.1", Title: "Ensure That BigQuery Datasets Are Not Anonymously or Publicly Accessible", Section: gcpBQ,
				Rules: []string{"bp_gcp_bigquery_public"}, AppliesTo: []string{"google_bigquery_dataset", "google_bigquery_dataset_access", "google_bigquery_dataset_iam_member", "google_bigquery_dataset_iam_binding"},
				Guidance: "Nimbo flags dataset access blocks, google_bigquery_dataset_access and dataset IAM members/bindings in the design granted to allUsers or allAuthenticatedUsers. Grants outside the design or through google_bigquery_dataset_iam_policy aren't seen."},
			{ID: "7.2", Title: "Ensure That All BigQuery Tables Are Encrypted With Customer-Managed Encryption Key (CMEK)", Section: gcpBQ,
				Rules: []string{"bp_gcp_bigquery_table_cmek"}, AppliesTo: []string{"google_bigquery_table"},
				Guidance: "Nimbo checks that BigQuery tables in the design set encryption_configuration.kms_key_name or belong to a dataset of the design with a default CMEK. Views and external tables store no data in BigQuery and pass."},
			{ID: "7.3", Title: "Ensure That a Default Customer-Managed Encryption Key (CMEK) Is Specified for All BigQuery Data Sets", Section: gcpBQ,
				Rules: []string{"bp_gcp_bigquery_dataset_cmek"}, AppliesTo: []string{"google_bigquery_dataset"},
				Guidance: "Nimbo checks that BigQuery datasets in the design set default_encryption_configuration.kms_key_name."},
			{ID: "7.4", Title: "Ensure all data in BigQuery has been classified", Section: gcpBQ, Manual: true,
				Guidance: "Classify the datasets (Sensitive Data Protection discovery scans, Dataplex tags or policy tags on columns), then attest it."},

			// 8 Dataproc
			{ID: "8.1", Title: "Ensure that Dataproc Cluster is encrypted using Customer-Managed Encryption Key", Section: gcpProc,
				Rules: []string{"bp_gcp_dataproc_cmek"}, AppliesTo: []string{"google_dataproc_cluster"},
				Guidance: "Nimbo checks that Dataproc clusters in the design set cluster_config.encryption_config.kms_key_name."},
		},
	})
}
