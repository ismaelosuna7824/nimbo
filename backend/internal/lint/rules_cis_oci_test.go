package lint

import (
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// TestRules runs these with the other cases.
func init() {
	for code, list := range ociCases {
		structureCases[code] = append(structureCases[code], list...)
	}
}

const ociVCN = `
resource "oci_core_vcn" "v" {
  compartment_id = var.c
  cidr_blocks    = ["10.0.0.0/16"]
}
`

func ociSecurityList(rule string) string {
	return `resource "oci_core_security_list" "s" {
  compartment_id = var.c
  vcn_id         = oci_core_vcn.v.id
  ingress_security_rules {
` + rule + `
  }
}
`
}

func ociNSGRule(body string) string {
	return `resource "oci_core_network_security_group_security_rule" "r" {
  network_security_group_id = var.nsg
  direction                 = "INGRESS"
` + body + `
}
`
}

const ociVault = `
resource "oci_kms_vault" "private" {
  compartment_id = var.c
  display_name   = "v"
  vault_type     = "VIRTUAL_PRIVATE"
}
resource "oci_kms_vault" "default" {
  compartment_id = var.c
  display_name   = "d"
  vault_type     = "DEFAULT"
}
`

func ociKey(vault, extra string) string {
	return `resource "oci_kms_key" "k" {
  compartment_id      = var.c
  display_name        = "k"
  management_endpoint = oci_kms_vault.` + vault + `.management_endpoint
  key_shape {
    algorithm = "AES"
    length    = 32
  }
` + extra + `
}
`
}

func ociBucket(extra string) string {
	return `resource "oci_objectstorage_bucket" "b" {
  compartment_id = var.c
  namespace      = var.ns
  name           = "data"
` + extra + `
}
`
}

func ociInstance(extra string) string {
	return `resource "oci_core_instance" "i" {
  compartment_id      = var.c
  availability_domain = var.ad
  shape               = "VM.Standard.E5.Flex"
  source_details {
    source_type = "image"
    source_id   = var.image
  }
` + extra + `
}
`
}

func ociPolicy(statements ...string) string {
	src := `resource "oci_identity_policy" "p" {
  compartment_id = var.c
  name           = "p"
  description    = "p"
  statements = [
`
	for _, s := range statements {
		src += "    " + s + ",\n"
	}
	return src + "  ]\n}\n"
}

const ociSubnet = ociVCN + `
resource "oci_core_subnet" "s" {
  compartment_id = var.c
  vcn_id         = oci_core_vcn.v.id
  cidr_block     = "10.0.1.0/24"
}
`

func ociLog(service, resource, category string) string {
	return `resource "oci_logging_log" "l" {
  display_name = "l"
  log_group_id = var.lg
  log_type     = "SERVICE"
  configuration {
    source {
      source_type = "OCISERVICE"
      service     = "` + service + `"
      resource    = ` + resource + `
      category    = "` + category + `"
    }
  }
}
`
}

var ociCases = map[string][]structureCase{
	"bp_oci_ssh_open": {
		{name: "security list tcp 22 from anywhere", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 22
      max = 22
    }`), want: 1},
		{name: "security list all protocols", src: ociVCN + ociSecurityList(`    protocol = "all"
    source   = "0.0.0.0/0"`), want: 1},
		{name: "security list tcp without options is every port", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "::/0"`), want: 1},
		{name: "security list port range around 22", src: ociVCN + ociSecurityList(`    protocol = 6
    source   = "0.0.0.0/0"
    tcp_options {
      min = 1
      max = 1024
    }`), want: 1},
		{name: "security list https only", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 443
      max = 443
    }`), want: 0},
		{name: "security list ssh from office", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "203.0.113.0/24"
    tcp_options {
      min = 22
      max = 22
    }`), want: 0},
		{name: "security list udp 22", src: ociVCN + ociSecurityList(`    protocol = "17"
    source   = "0.0.0.0/0"`), want: 0},
		{name: "security list ssh from a service cidr", src: ociVCN + ociSecurityList(`    protocol    = "6"
    source      = "all-iad-services-in-oracle-services-network"
    source_type = "SERVICE_CIDR_BLOCK"`), want: 0},
		{name: "nsg rule tcp 22 from anywhere", src: ociNSGRule(`  protocol    = "6"
  source      = "0.0.0.0/0"
  source_type = "CIDR_BLOCK"
  tcp_options {
    destination_port_range {
      min = 22
      max = 22
    }
  }`), want: 1},
		{name: "nsg rule all protocols", src: ociNSGRule(`  protocol = "all"
  source   = "0.0.0.0/0"`), want: 1},
		{name: "nsg rule source port 22 only", src: ociNSGRule(`  protocol = "6"
  source   = "0.0.0.0/0"
  tcp_options {
    source_port_range {
      min = 22
      max = 22
    }
    destination_port_range {
      min = 443
      max = 443
    }
  }`), want: 0},
		{name: "nsg egress rule", src: `resource "oci_core_network_security_group_security_rule" "r" {
  network_security_group_id = var.nsg
  direction                 = "EGRESS"
  protocol                  = "all"
  destination               = "0.0.0.0/0"
}`, want: 0},
	},
	"bp_oci_rdp_open": {
		{name: "security list rdp", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 3389
      max = 3389
    }`), want: 1},
		{name: "security list ssh is not rdp", src: ociVCN + ociSecurityList(`    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 22
      max = 22
    }`), want: 0},
		{name: "default security list all traffic", src: ociVCN + `resource "oci_core_default_security_list" "d" {
  manage_default_resource_id = oci_core_vcn.v.default_security_list_id
  ingress_security_rules {
    protocol = "all"
    source   = "0.0.0.0/0"
  }
}`, want: 1},
		{name: "nsg rule rdp", src: ociNSGRule(`  protocol = "6"
  source   = "0.0.0.0/0"
  tcp_options {
    destination_port_range {
      min = 3389
      max = 3389
    }
  }`), want: 1},
		{name: "nsg rule rdp from a variable", src: ociNSGRule(`  protocol = "6"
  source   = var.admin_cidr
  tcp_options {
    destination_port_range {
      min = 3389
      max = 3389
    }
  }`), want: 0},
	},
	"bp_oci_default_security_list": {
		{name: "vcn keeps the default list", src: ociVCN, want: 1},
		{name: "managed and closed", src: ociVCN + `resource "oci_core_default_security_list" "d" {
  manage_default_resource_id = oci_core_vcn.v.default_security_list_id
  ingress_security_rules {
    protocol = "1"
    source   = "10.0.0.0/16"
  }
}`, want: 0},
		{name: "managed but open ingress", src: ociVCN + `resource "oci_core_default_security_list" "d" {
  manage_default_resource_id = oci_core_vcn.v.default_security_list_id
  ingress_security_rules {
    protocol = "6"
    source   = "0.0.0.0/0"
    tcp_options {
      min = 443
      max = 443
    }
  }
}`, want: 1},
		{name: "managed but all egress", src: ociVCN + `resource "oci_core_default_security_list" "d" {
  manage_default_resource_id = oci_core_vcn.v.default_security_list_id
  egress_security_rules {
    protocol    = "all"
    destination = "0.0.0.0/0"
  }
}`, want: 1},
		{name: "icmp egress is allowed", src: ociVCN + `resource "oci_core_default_security_list" "d" {
  manage_default_resource_id = oci_core_vcn.v.default_security_list_id
  egress_security_rules {
    protocol    = "1"
    destination = "0.0.0.0/0"
  }
}`, want: 0},
	},
	"bp_oci_unrestricted_endpoint": {
		{name: "adb open to all", src: `resource "oci_database_autonomous_database" "a" {
  compartment_id = var.c
  db_name        = "a"
}`, want: 1},
		{name: "adb allowlists anywhere", src: `resource "oci_database_autonomous_database" "a" {
  compartment_id  = var.c
  db_name         = "a"
  whitelisted_ips = ["0.0.0.0/0"]
}`, want: 1},
		{name: "adb allowlist", src: `resource "oci_database_autonomous_database" "a" {
  compartment_id  = var.c
  db_name         = "a"
  whitelisted_ips = ["203.0.113.0/24"]
}`, want: 0},
		{name: "adb private endpoint", src: `resource "oci_database_autonomous_database" "a" {
  compartment_id = var.c
  db_name        = "a"
  subnet_id      = var.subnet
}`, want: 0},
		{name: "adb dedicated", src: `resource "oci_database_autonomous_database" "a" {
  compartment_id                   = var.c
  db_name                          = "a"
  autonomous_container_database_id = var.acd
}`, want: 0},
		{name: "oic without network details", src: `resource "oci_integration_integration_instance" "o" {
  compartment_id            = var.c
  display_name              = "o"
  integration_instance_type = "ENTERPRISEX"
  is_byol                   = false
  message_packs             = 1
}`, want: 1},
		{name: "oic allowlisted", src: `resource "oci_integration_integration_instance" "o" {
  compartment_id            = var.c
  display_name              = "o"
  integration_instance_type = "ENTERPRISEX"
  is_byol                   = false
  message_packs             = 1
  network_endpoint_details {
    network_endpoint_type = "PUBLIC"
    allowlisted_http_ips  = ["203.0.113.0/24"]
  }
}`, want: 0},
		{name: "oic allowlists anywhere", src: `resource "oci_integration_integration_instance" "o" {
  compartment_id            = var.c
  display_name              = "o"
  integration_instance_type = "ENTERPRISEX"
  is_byol                   = false
  message_packs             = 1
  network_endpoint_details {
    network_endpoint_type = "PUBLIC"
    allowlisted_http_ips  = ["0.0.0.0/0"]
  }
}`, want: 1},
		{name: "oac public without allowlist", src: `resource "oci_analytics_analytics_instance" "a" {
  compartment_id = var.c
  name           = "a"
  feature_set    = "ENTERPRISE_ANALYTICS"
  license_type   = "LICENSE_INCLUDED"
  capacity {
    capacity_type  = "OLPU_COUNT"
    capacity_value = 2
  }
  network_endpoint_details {
    network_endpoint_type = "PUBLIC"
  }
}`, want: 1},
		{name: "oac public with allowlist", src: `resource "oci_analytics_analytics_instance" "a" {
  compartment_id = var.c
  name           = "a"
  feature_set    = "ENTERPRISE_ANALYTICS"
  license_type   = "LICENSE_INCLUDED"
  capacity {
    capacity_type  = "OLPU_COUNT"
    capacity_value = 2
  }
  network_endpoint_details {
    network_endpoint_type = "PUBLIC"
    whitelisted_ips       = ["203.0.113.0/24"]
  }
}`, want: 0},
		{name: "oac private", src: `resource "oci_analytics_analytics_instance" "a" {
  compartment_id = var.c
  name           = "a"
  feature_set    = "ENTERPRISE_ANALYTICS"
  license_type   = "LICENSE_INCLUDED"
  capacity {
    capacity_type  = "OLPU_COUNT"
    capacity_value = 2
  }
  network_endpoint_details {
    network_endpoint_type = "PRIVATE"
    vcn_id                = var.vcn
    subnet_id             = var.subnet
  }
}`, want: 0},
	},
	"bp_oci_legacy_imds": {
		{name: "default", src: ociInstance(""), want: 1},
		{name: "explicitly enabled", src: ociInstance(`  instance_options {
    are_legacy_imds_endpoints_disabled = false
  }`), want: 1},
		{name: "disabled", src: ociInstance(`  instance_options {
    are_legacy_imds_endpoints_disabled = true
  }`), want: 0},
	},
	"bp_oci_secure_boot": {
		{name: "default", src: ociInstance(""), want: 1},
		{name: "shielded", src: ociInstance(`  platform_config {
    type                   = "AMD_VM"
    is_secure_boot_enabled = true
  }`), want: 0},
	},
	"bp_oci_in_transit_encryption": {
		{name: "default", src: ociInstance(""), want: 1},
		{name: "top level", src: ociInstance(`  is_pv_encryption_in_transit_enabled = true`), want: 0},
		{name: "launch options", src: ociInstance(`  launch_options {
    is_pv_encryption_in_transit_enabled = true
  }`), want: 0},
		{name: "off", src: ociInstance(`  is_pv_encryption_in_transit_enabled = false`), want: 1},
	},
	"bp_oci_flow_logs": {
		{name: "no flow log", src: ociSubnet, want: 1},
		{name: "subnet flow log", src: ociSubnet + ociLog("flowlogs", "oci_core_subnet.s.id", "all"), want: 0},
		{name: "vcn flow log", src: ociSubnet + ociLog("flowlogs", "oci_core_vcn.v.id", "vcn"), want: 0},
		{name: "log of another service", src: ociSubnet + ociLog("objectstorage", "oci_core_subnet.s.id", "write"), want: 1},
		{name: "flow log per subnet with for_each", src: ociVCN + `resource "oci_core_subnet" "s" {
  for_each       = { a = "10.0.1.0/24", b = "10.0.2.0/24" }
  compartment_id = var.c
  vcn_id         = oci_core_vcn.v.id
  cidr_block     = each.value
}
resource "oci_logging_log" "l" {
  for_each     = oci_core_subnet.s
  display_name = each.key
  log_group_id = var.lg
  log_type     = "SERVICE"
  configuration {
    source {
      source_type = "OCISERVICE"
      service     = "flowlogs"
      resource    = each.value.id
      category    = "all"
    }
  }
}`, want: 0},
	},
	"bp_oci_key_rotation": {
		{name: "private vault key without rotation", src: ociVault + ociKey("private", ""), want: 1},
		{name: "private vault key rotating yearly", src: ociVault + ociKey("private", `  is_auto_rotation_enabled = true
  auto_key_rotation_details {
    rotation_interval_in_days = 365
  }`), want: 0},
		{name: "private vault key rotating every two years", src: ociVault + ociKey("private", `  is_auto_rotation_enabled = true
  auto_key_rotation_details {
    rotation_interval_in_days = 730
  }`), want: 1},
		{name: "default vault key can't auto-rotate", src: ociVault + ociKey("default", ""), want: 0},
	},
	"bp_oci_bucket_write_logs": {
		{name: "no log", src: ociBucket(""), want: 1},
		{name: "write log", src: ociBucket("") + ociLog("objectstorage", "oci_objectstorage_bucket.b.name", "write"), want: 0},
		{name: "read log only", src: ociBucket("") + ociLog("objectstorage", "oci_objectstorage_bucket.b.name", "read"), want: 1},
		{name: "write log by constant name", src: ociBucket("") + ociLog("objectstorage", `"data"`, "write"), want: 0},
	},
	"bp_oci_public_bucket": {
		{name: "private by default", src: ociBucket(""), want: 0},
		{name: "object read", src: ociBucket(`  access_type = "ObjectRead"`), want: 1},
		{name: "object read without list", src: ociBucket(`  access_type = "ObjectReadWithoutList"`), want: 1},
		{name: "public by intent", src: ociBucket(`  access_type   = "ObjectRead"
  freeform_tags = { Public = "true" }`), want: 0},
		{name: "explicitly private", src: ociBucket(`  access_type = "NoPublicAccess"`), want: 0},
	},
	"bp_oci_customer_managed_key": {
		{name: "bucket without key", src: ociBucket(""), want: 1},
		{name: "bucket with key", src: ociBucket(`  kms_key_id = var.key`), want: 0},
		{name: "volume without key", src: `resource "oci_core_volume" "v" {
  compartment_id      = var.c
  availability_domain = var.ad
}`, want: 1},
		{name: "volume with key", src: `resource "oci_core_volume" "v" {
  compartment_id      = var.c
  availability_domain = var.ad
  kms_key_id          = oci_kms_key.k.id
}`, want: 0},
		{name: "boot volume without key", src: `resource "oci_core_boot_volume" "b" {
  compartment_id      = var.c
  availability_domain = var.ad
  source_details {
    type = "bootVolume"
    id   = var.src
  }
}`, want: 1},
		{name: "instance image boot without key", src: ociInstance(""), want: 1},
		{name: "instance image boot with key", src: `resource "oci_core_instance" "i" {
  compartment_id      = var.c
  availability_domain = var.ad
  shape               = "VM.Standard.E5.Flex"
  source_details {
    source_type = "image"
    source_id   = var.image
    kms_key_id  = var.key
  }
}`, want: 0},
		{name: "instance from an existing boot volume", src: `resource "oci_core_instance" "i" {
  compartment_id      = var.c
  availability_domain = var.ad
  shape               = "VM.Standard.E5.Flex"
  source_details {
    source_type = "bootVolume"
    source_id   = var.boot
  }
}`, want: 0},
		{name: "file system without key", src: `resource "oci_file_storage_file_system" "f" {
  compartment_id      = var.c
  availability_domain = var.ad
}`, want: 1},
		{name: "file system with empty key", src: `resource "oci_file_storage_file_system" "f" {
  compartment_id      = var.c
  availability_domain = var.ad
  kms_key_id          = ""
}`, want: 1},
	},
	"bp_oci_bucket_versioning": {
		{name: "default", src: ociBucket(""), want: 1},
		{name: "disabled", src: ociBucket(`  versioning = "Disabled"`), want: 1},
		{name: "enabled", src: ociBucket(`  versioning = "Enabled"`), want: 0},
		{name: "variable", src: ociBucket(`  versioning = var.versioning`), want: 0},
	},
	"bp_oci_policy_manage_all": {
		{name: "group manages the tenancy", src: ociPolicy(`"Allow group Ops to manage all-resources in tenancy"`), want: 1},
		{name: "any-user manages the tenancy", src: ociPolicy(`"allow any-user to manage all-resources in tenancy"`), want: 1},
		{name: "group named by reference", src: ociPolicy(`"Allow group ${var.group} to manage all-resources in tenancy"`), want: 1},
		{name: "administrators restated", src: ociPolicy(`"Allow group 'Default'/'Administrators' to manage all-resources in tenancy"`), want: 0},
		{name: "compartment scope", src: ociPolicy(`"Allow group Ops to manage all-resources in compartment app"`), want: 0},
		{name: "service family", src: ociPolicy(`"Allow group Ops to manage instance-family in tenancy"`), want: 0},
		{name: "two bad statements", src: ociPolicy(`"Allow group A to manage all-resources in tenancy"`, `"Allow dynamic-group B to manage all-resources in tenancy where request.region = 'iad'"`), want: 2},
	},
	"bp_oci_policy_admin_group": {
		{name: "use groups unrestricted", src: ociPolicy(`"Allow group IAMAdmins to use groups in tenancy"`), want: 1},
		{name: "manage users unrestricted", src: ociPolicy(`"Allow group IAMAdmins to manage users in tenancy"`), want: 1},
		{name: "excludes administrators", src: ociPolicy(`"Allow group IAMAdmins to use groups in tenancy where target.group.name != 'Administrators'"`), want: 0},
		{name: "inspect is fine", src: ociPolicy(`"Allow group IAMAdmins to inspect groups in tenancy"`), want: 0},
		{name: "compartment scope", src: ociPolicy(`"Allow group Ops to manage all-resources in compartment id ${var.c}"`), want: 0},
		{name: "administrators", src: ociPolicy(`"Allow group Administrators to manage users in tenancy"`), want: 0},
	},
	"bp_oci_storage_admin_delete": {
		{name: "manage object-family", src: ociPolicy(`"Allow group Storage to manage object-family in compartment app"`), want: 1},
		{name: "manage volumes in tenancy", src: ociPolicy(`"Allow group Storage to manage volumes in tenancy"`), want: 1},
		{name: "deletes excluded", src: ociPolicy(`"Allow group Storage to manage object-family in compartment app where all {request.permission != 'BUCKET_DELETE', request.permission != 'OBJECT_DELETE'}"`), want: 0},
		{name: "one delete still allowed", src: ociPolicy(`"Allow group Storage to manage object-family in compartment app where request.permission != 'BUCKET_DELETE'"`), want: 1},
		{name: "storage admin grants only deletes", src: ociPolicy(`"Allow group StorageAdmins to manage volumes in tenancy where request.permission = 'VOLUME_DELETE'"`), want: 0},
		{name: "use is not manage", src: ociPolicy(`"Allow group Storage to use object-family in compartment app"`), want: 0},
		{name: "dynamic group workload", src: ociPolicy(`"Allow dynamic-group Fn to manage objects in compartment app"`), want: 0},
		{name: "non-storage resource", src: ociPolicy(`"Allow group Net to manage virtual-network-family in compartment app"`), want: 0},
	},
}

// Opt-in rules stay out of a default Check (Problems) and run when enabled.
func TestOCIOptInRules(t *testing.T) {
	wf := importHCL(t, ociSubnet+ociBucket("")+ociInstance(""))
	optIn := []string{"bp_oci_default_security_list", "bp_oci_legacy_imds", "bp_oci_secure_boot", "bp_oci_in_transit_encryption",
		"bp_oci_flow_logs", "bp_oci_bucket_write_logs", "bp_oci_customer_managed_key", "bp_oci_bucket_versioning"}
	got := codes(Check(wf, testCatalog, model.BestPracticeSettings{}, nil))
	for _, code := range optIn {
		if got[code] != 0 {
			t.Errorf("%s ran without being enabled", code)
		}
	}
	got = codes(Check(wf, testCatalog, model.BestPracticeSettings{EnabledRules: optIn}, nil))
	for _, code := range optIn {
		if got[code] == 0 {
			t.Errorf("%s enabled but found nothing: %v", code, got)
		}
	}
}
