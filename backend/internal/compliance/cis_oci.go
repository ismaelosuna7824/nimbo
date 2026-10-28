package compliance

import (
	"encoding/json"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// CIS Oracle Cloud Infrastructure Foundations Benchmark v3.1.1. Recommendation numbers and titles follow the benchmark
// (and Oracle's CIS compliance script, which implements the same audit steps). Controls whose evidence lives in the
// tenancy (identity domains, users and their credentials, tag defaults, Events rules, Cloud Guard) are manual: a design
// declaring some of them doesn't prove the rest of the tenancy does.

const (
	ociIAM      = "Identity and Access Management"
	ociNetwork  = "Networking"
	ociCompute  = "Compute"
	ociLogging  = "Logging and Monitoring"
	ociObject   = "Storage - Object Storage"
	ociBlock    = "Storage - Block Volumes"
	ociFile     = "Storage - File Storage Service"
	ociAssets   = "Asset Management"
	ociTenancy  = "Check it in the OCI Console or with Oracle's CIS compliance script (oci-cis-landingzone-quickstart/scripts/cis_reports.py), then attest it. "
	ociNotified = "Create an Events rule in the root compartment (include subcompartments) for these event types with a Notifications topic action that has an active subscription; the CIS OCI Landing Zone creates them. "
)

var (
	ociSecurityLists = []string{"oci_core_security_list", "oci_core_default_security_list"}
	ociNSGRules      = []string{"oci_core_network_security_group_security_rule"}
	ociBucket        = []string{"oci_objectstorage_bucket"}
	ociInstance      = []string{"oci_core_instance"}
	ociPolicy        = []string{"oci_identity_policy"}
)

// ociLiteral returns the constant value of body attribute name (nil when
// unset or an expression).
func ociLiteral(b *model.Body, name string) any {
	for _, a := range b.Attrs {
		if a.Name != name {
			continue
		}
		raw := a.Value.Literal
		if len(raw) == 0 && a.Value.Expr != "" {
			raw, _ = hclcodec.LiteralFromExpr(a.Value.Expr)
		}
		var v any
		if json.Unmarshal(raw, &v) != nil {
			return nil
		}
		return v
	}
	return nil
}

// ociServerlessADB: CIS 2.8 is about serverless Autonomous Databases;
// dedicated ones run in an Autonomous Container Database inside a VCN.
func ociServerlessADB(n *model.Node) bool {
	for _, a := range n.Body.Attrs {
		if a.Name == "autonomous_container_database_id" {
			return false
		}
	}
	return ociLiteral(&n.Body, "is_dedicated") != true
}

// ociOwnsBootVolume: an instance booted from an existing boot volume has
// no boot volume key of its own (the oci_core_boot_volume carries it).
func ociOwnsBootVolume(n *model.Node) bool {
	if n.Type != "oci_core_instance" {
		return true
	}
	for _, blk := range n.Body.Blocks {
		if t, ok := ociLiteral(&blk.Body, "source_type").(string); blk.Type == "source_details" && ok && strings.EqualFold(t, "bootVolume") {
			return false
		}
	}
	return true
}

func init() {
	Register(Framework{
		ID:       "cis-oci-3.1",
		Name:     "CIS Oracle Cloud Infrastructure Foundations Benchmark",
		Version:  "3.1.1",
		Platform: "oci",
		Source:   "https://www.cisecurity.org/benchmark/oracle_cloud",
		Controls: []Control{
			// 1 Identity and Access Management
			{ID: "1.1", Title: "Ensure service level admins are created to manage resources of particular service", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Create a group per service (network, database, storage admins…) with policies on that service's resource families in its compartments, instead of groups that manage all-resources."},
			{ID: "1.2", Title: "Ensure permissions on all resources are given only to the tenancy administrator group", Section: ociIAM,
				Rules: []string{"bp_oci_policy_manage_all"}, AppliesTo: ociPolicy,
				Guidance: "Nimbo reads the statements of the oci_identity_policy resources in the design and flags any \"manage all-resources in tenancy\" not granted to the Administrators group. Policies created outside this design aren't seen."},
			{ID: "1.3", Title: "Ensure IAM administrators cannot update tenancy Administrators group", Section: ociIAM,
				Rules: []string{"bp_oci_policy_admin_group"}, AppliesTo: ociPolicy,
				Guidance: "Nimbo flags policy statements in the design that grant use or manage on users, groups or all-resources in tenancy without where target.group.name != 'Administrators'. Policies created outside this design aren't seen."},
			{ID: "1.4", Title: "Ensure IAM password policy requires minimum length of 14 or greater", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Set it in each identity domain's password policy (Identity > Domains > Authentication settings)."},
			{ID: "1.5", Title: "Ensure IAM password policy expires passwords within 365 days", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Set the password expiry of each identity domain's password policy to 365 days or less."},
			{ID: "1.6", Title: "Ensure IAM password policy prevents password reuse", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Set the number of previous passwords remembered in each identity domain's password policy (24 is the usual value)."},
			{ID: "1.7", Title: "Ensure MFA is enabled for all users with a console password", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Enable MFA in each identity domain (sign-on policy requiring a second factor for console access)."},
			{ID: "1.8", Title: "Ensure user API keys rotate within 90 days or less", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Rotate user API keys at least every 90 days; prefer instance, resource or workload principals for automation."},
			{ID: "1.9", Title: "Ensure user customer secret keys rotate within 90 days or less", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Rotate customer secret keys (S3-compatible API) at least every 90 days."},
			{ID: "1.10", Title: "Ensure user auth tokens rotate within 90 days or less", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Rotate auth tokens (OCIR, Swift) at least every 90 days."},
			{ID: "1.11", Title: "Ensure user IAM Database Passwords rotate within 90 days", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Rotate IAM database passwords at least every 90 days."},
			{ID: "1.12", Title: "Ensure API keys are not created for tenancy administrator users", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Members of the Administrators group have no API keys; automation runs as a less privileged user or principal."},
			{ID: "1.13", Title: "Ensure all OCI IAM user accounts have a valid and current email address", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Every user has a verified, current email address (needed for password resets and notices)."},
			{ID: "1.14", Title: "Ensure Instance Principal authentication is used for OCI instances, OCI Cloud Databases and OCI Functions to access OCI resources", Section: ociIAM, Manual: true,
				Guidance: "Workloads that call OCI APIs authenticate as instance or resource principals through a dynamic group (oci_identity_dynamic_group) instead of user API keys stored on the host. Nimbo can't see how your code authenticates; attest it once confirmed."},
			{ID: "1.15", Title: "Ensure storage service-level admins cannot delete resources they manage", Section: ociIAM,
				Rules: []string{"bp_oci_storage_admin_delete"}, AppliesTo: ociPolicy,
				Guidance: "Nimbo flags policy statements in the design that let a group manage object, volume or file storage (or all-resources) without excluding the delete permissions, unless the statement grants only the deletes. Policies created outside this design aren't seen."},
			{ID: "1.16", Title: "Ensure OCI IAM credentials unused for 45 days or more are disabled", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Disable console passwords and API keys unused for 45 days."},
			{ID: "1.17", Title: "Ensure there is only one active API Key for any single OCI IAM user", Section: ociIAM, Manual: true,
				Guidance: ociTenancy + "Delete extra API keys so each user has at most one active key."},

			// 2 Networking
			{ID: "2.1", Title: "Ensure no security lists allow ingress from 0.0.0.0/0 to port 22", Section: ociNetwork,
				Rules: []string{"bp_oci_ssh_open"}, AppliesTo: ociSecurityLists,
				Guidance: "Nimbo flags security list ingress rules from 0.0.0.0/0 (or ::/0) whose protocol is all or TCP with a port range including 22 (no tcp_options means every port). Rules whose source is a variable can't be judged."},
			{ID: "2.2", Title: "Ensure no security lists allow ingress from 0.0.0.0/0 to port 3389", Section: ociNetwork,
				Rules: []string{"bp_oci_rdp_open"}, AppliesTo: ociSecurityLists,
				Guidance: "Nimbo flags security list ingress rules from 0.0.0.0/0 (or ::/0) whose protocol is all or TCP with a port range including 3389."},
			{ID: "2.3", Title: "Ensure no network security groups allow ingress from 0.0.0.0/0 to port 22", Section: ociNetwork,
				Rules: []string{"bp_oci_ssh_open"}, AppliesTo: ociNSGRules,
				Guidance: "Nimbo flags ingress oci_core_network_security_group_security_rule resources from 0.0.0.0/0 (or ::/0) whose protocol is all or TCP with a destination port range including 22."},
			{ID: "2.4", Title: "Ensure no network security groups allow ingress from 0.0.0.0/0 to port 3389", Section: ociNetwork,
				Rules: []string{"bp_oci_rdp_open"}, AppliesTo: ociNSGRules,
				Guidance: "Nimbo flags ingress network security group rules from 0.0.0.0/0 (or ::/0) whose protocol is all or TCP with a destination port range including 3389."},
			{ID: "2.5", Title: "Ensure the default security list of every VCN restricts all traffic except ICMP within VCN", Section: ociNetwork,
				Rules: []string{"bp_oci_default_security_list"}, AppliesTo: []string{"oci_core_vcn", "oci_core_default_security_list"},
				Guidance: "Nimbo checks that every VCN in the design manages its default security list (oci_core_default_security_list on its default_security_list_id) and that the list has no ingress from 0.0.0.0/0 and no egress to it except ICMP. A VCN that leaves the default list alone keeps SSH open to the internet."},
			{ID: "2.6", Title: "Ensure Oracle Integration Cloud (OIC) access is restricted to allowed sources", Section: ociNetwork,
				Rules: []string{"bp_oci_unrestricted_endpoint"}, AppliesTo: []string{"oci_integration_integration_instance"},
				Guidance: "Nimbo checks that each Integration instance has network_endpoint_details with an allowlist (IPs or VCNs) that doesn't contain 0.0.0.0/0."},
			{ID: "2.7", Title: "Ensure Oracle Analytics Cloud (OAC) access is restricted to allowed sources or deployed within a Virtual Cloud Network", Section: ociNetwork,
				Rules: []string{"bp_oci_unrestricted_endpoint"}, AppliesTo: []string{"oci_analytics_analytics_instance"},
				Guidance: "Nimbo checks that each Analytics instance has a private endpoint, or a public one with whitelisted IPs, VCNs or services that don't include 0.0.0.0/0."},
			{ID: "2.8", Title: "Ensure Oracle Autonomous Shared Database (ADB) access is restricted or deployed within a VCN", Section: ociNetwork,
				Rules: []string{"bp_oci_unrestricted_endpoint"}, AppliesTo: []string{"oci_database_autonomous_database"}, AppliesToNode: ociServerlessADB,
				Guidance: "Nimbo checks that each serverless Autonomous Database has a private endpoint (subnet_id) or whitelisted_ips without 0.0.0.0/0. Dedicated databases (in an Autonomous Container Database) are out of scope."},

			// 3 Compute
			{ID: "3.1", Title: "Ensure Compute Instance Legacy Metadata service endpoint is disabled", Section: ociCompute,
				Rules: []string{"bp_oci_legacy_imds"}, AppliesTo: ociInstance,
				Guidance: "Nimbo checks that each instance sets instance_options.are_legacy_imds_endpoints_disabled = true."},
			{ID: "3.2", Title: "Ensure Secure Boot is enabled on Compute Instance", Section: ociCompute,
				Rules: []string{"bp_oci_secure_boot"}, AppliesTo: ociInstance,
				Guidance: "Nimbo checks that each instance sets platform_config.is_secure_boot_enabled = true. Shielded instances need a supporting shape and image."},
			{ID: "3.3", Title: "Ensure In-transit Encryption is enabled on Compute Instance", Section: ociCompute,
				Rules: []string{"bp_oci_in_transit_encryption"}, AppliesTo: ociInstance,
				Guidance: "Nimbo checks that each instance sets is_pv_encryption_in_transit_enabled = true (top level or in launch_options)."},

			// 4 Logging and Monitoring
			{ID: "4.1", Title: "Ensure default tags are used on resources", Section: ociLogging, Manual: true,
				Guidance: ociTenancy + "Create a tag default in the root compartment (e.g. CreatedBy = ${iam.principal.name}) so every resource records who created it."},
			{ID: "4.2", Title: "Create at least one notification topic and subscription to receive monitoring alerts", Section: ociLogging, Manual: true,
				Guidance: ociTenancy + "Have at least one Notifications topic with an active subscription that receives security alerts."},
			{ID: "4.3", Title: "Ensure a notification is configured for Identity Provider changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: identity provider create, delete and update."},
			{ID: "4.4", Title: "Ensure a notification is configured for IdP group mapping changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: IdP group mapping add, remove and update."},
			{ID: "4.5", Title: "Ensure a notification is configured for IAM group changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: group create, delete and update (including membership)."},
			{ID: "4.6", Title: "Ensure a notification is configured for IAM policy changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: policy create, delete and update."},
			{ID: "4.7", Title: "Ensure a notification is configured for user changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: user create, delete, update and capabilities/credentials changes."},
			{ID: "4.8", Title: "Ensure a notification is configured for VCN changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: VCN create, delete and update."},
			{ID: "4.9", Title: "Ensure a notification is configured for changes to route tables", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: route table create, delete, update and compartment change."},
			{ID: "4.10", Title: "Ensure a notification is configured for security list changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: security list create, delete, update and compartment change."},
			{ID: "4.11", Title: "Ensure a notification is configured for network security group changes", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: network security group create, delete, update, rule changes and compartment change."},
			{ID: "4.12", Title: "Ensure a notification is configured for changes to network gateways", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: create, delete, update and compartment change of DRGs, DRG attachments, internet, NAT, service and local peering gateways."},
			{ID: "4.13", Title: "Ensure VCN flow logging is enabled for all subnets", Section: ociLogging,
				Rules: []string{"bp_oci_flow_logs"}, AppliesTo: []string{"oci_core_subnet"},
				Guidance: "Nimbo checks that every subnet in the design is covered by an oci_logging_log with source service \"flowlogs\" for the subnet or its VCN. Capture filters that sample or skip traffic aren't checked."},
			{ID: "4.14", Title: "Ensure Cloud Guard is enabled in the root compartment of the tenancy", Section: ociLogging, Manual: true,
				Guidance: ociTenancy + "Enable Cloud Guard with the root compartment as target (oci_cloud_guard_cloud_guard_configuration status = \"ENABLED\" in a tenancy-level workflow)."},
			{ID: "4.15", Title: "Ensure a notification is configured for Oracle Cloud Guard problems detected", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event types: Cloud Guard problem detected, dismissed and remediated."},
			{ID: "4.16", Title: "Ensure customer created Customer Managed Key (CMK) is rotated at least annually", Section: ociLogging,
				Rules: []string{"bp_oci_key_rotation"}, AppliesTo: []string{"oci_kms_key"},
				Guidance: "Nimbo checks that keys in a virtual private vault enable automatic rotation every 365 days or less. Keys in a default (virtual) vault can't rotate automatically and aren't flagged: rotate them by hand at least yearly."},
			{ID: "4.17", Title: "Ensure write level Object Storage logging is enabled for all buckets", Section: ociLogging,
				Rules: []string{"bp_oci_bucket_write_logs"}, AppliesTo: ociBucket,
				Guidance: "Nimbo checks that every bucket has an oci_logging_log with source service \"objectstorage\", category \"write\" and the bucket as resource."},
			{ID: "4.18", Title: "Ensure a notification is configured for Local OCI User Authentication", Section: ociLogging, Manual: true,
				Guidance: ociNotified + "Event type: interactive login of local (non-federated) users."},

			// 5 Storage
			{ID: "5.1.1", Title: "Ensure no Object Storage buckets are publicly visible", Section: ociObject,
				Rules: []string{"bp_oci_public_bucket"}, AppliesTo: ociBucket,
				Guidance: "Nimbo flags buckets with access_type ObjectRead or ObjectReadWithoutList. Buckets tagged Public = \"true\" in freeform_tags are taken as deliberate and not flagged, but an auditor still counts them against this control: document them as exceptions."},
			{ID: "5.1.2", Title: "Ensure Object Storage Buckets are encrypted with a Customer Managed Key (CMK)", Section: ociObject,
				Rules: []string{"bp_oci_customer_managed_key"}, AppliesTo: ociBucket,
				Guidance: "Nimbo checks that each bucket sets kms_key_id to a Vault key."},
			{ID: "5.1.3", Title: "Ensure Versioning is Enabled for Object Storage Buckets", Section: ociObject,
				Rules: []string{"bp_oci_bucket_versioning"}, AppliesTo: ociBucket,
				Guidance: "Nimbo checks that each bucket sets versioning = \"Enabled\"."},
			{ID: "5.2.1", Title: "Ensure Block Volumes are encrypted with Customer Managed Keys", Section: ociBlock,
				Rules: []string{"bp_oci_customer_managed_key"}, AppliesTo: []string{"oci_core_volume"},
				Guidance: "Nimbo checks that each oci_core_volume sets kms_key_id. Volumes created inline by an instance (launch_volume_attachments) aren't checked."},
			{ID: "5.2.2", Title: "Ensure Boot Volumes are encrypted with Customer Managed Key", Section: ociBlock,
				Rules: []string{"bp_oci_customer_managed_key"}, AppliesTo: []string{"oci_core_instance", "oci_core_boot_volume"}, AppliesToNode: ociOwnsBootVolume,
				Guidance: "Nimbo checks that instances booting from an image set source_details.kms_key_id and that oci_core_boot_volume resources set kms_key_id."},
			{ID: "5.3.1", Title: "Ensure File Storage Systems are encrypted with Customer Managed Keys", Section: ociFile,
				Rules: []string{"bp_oci_customer_managed_key"}, AppliesTo: []string{"oci_file_storage_file_system"},
				Guidance: "Nimbo checks that each file system sets kms_key_id."},

			// 6 Asset Management
			{ID: "6.1", Title: "Create at least one compartment in your tenancy to store cloud resources", Section: ociAssets, Manual: true,
				Guidance: ociTenancy + "Organize resources in compartments (by environment or team) so policies and quotas can be scoped."},
			{ID: "6.2", Title: "Ensure no resources are created in the root compartment", Section: ociAssets, Manual: true,
				Guidance: "Point compartment_id at a compartment, not the tenancy OCID (Nimbo's OCI starters default to the tenancy root for the local emulator; override var.compartment_ocid for real deployments). Nimbo can't tell from an OCID whether it is the root; attest it once confirmed."},
		},
	})
}
