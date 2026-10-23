data "oci_identity_availability_domains" "ads" {
  compartment_id = var.compartment_ocid
}

resource "oci_core_vcn" "main" {
  compartment_id = var.compartment_ocid
  display_name   = local.name_prefix
  cidr_blocks    = [var.vcn_cidr]
  dns_label      = "edge"
  freeform_tags  = local.common_tags
}

resource "oci_core_internet_gateway" "main" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${local.name_prefix}-igw"
  enabled        = true
  freeform_tags  = local.common_tags
}

resource "oci_core_route_table" "public" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${local.name_prefix}-public"
  freeform_tags  = local.common_tags

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.main.id
  }
}

# Public subnet: the load balancer. Private subnet: the servers, with no
# public IPs and no route to the internet.
resource "oci_core_security_list" "tier" {
  for_each       = local.tiers
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.main.id
  display_name   = "${local.name_prefix}-${each.key}"
  freeform_tags  = local.common_tags

  dynamic "ingress_security_rules" {
    for_each = each.value.ingress
    content {
      description = ingress_security_rules.value.description
      protocol    = "6"
      source      = ingress_security_rules.value.cidr

      tcp_options {
        min = ingress_security_rules.value.port
        max = ingress_security_rules.value.port
      }
    }
  }

  dynamic "egress_security_rules" {
    for_each = each.value.egress
    content {
      description = egress_security_rules.value.description
      protocol    = "6"
      destination = egress_security_rules.value.cidr

      tcp_options {
        min = egress_security_rules.value.port
        max = egress_security_rules.value.port
      }
    }
  }
}

resource "oci_core_subnet" "tier" {
  for_each                   = local.tiers
  compartment_id             = var.compartment_ocid
  vcn_id                     = oci_core_vcn.main.id
  display_name               = "${local.name_prefix}-${each.key}"
  cidr_block                 = each.value.cidr
  dns_label                  = each.key
  prohibit_public_ip_on_vnic = !each.value.public
  route_table_id             = each.value.public ? oci_core_route_table.public.id : oci_core_vcn.main.default_route_table_id
  security_list_ids          = [oci_core_security_list.tier[each.key].id]
  freeform_tags              = local.common_tags
}
