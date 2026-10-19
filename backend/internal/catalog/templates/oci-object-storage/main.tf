data "oci_objectstorage_namespace" "ns" {
  compartment_id = var.compartment_ocid
}

resource "oci_objectstorage_bucket" "assets" {
  compartment_id = var.compartment_ocid
  namespace      = data.oci_objectstorage_namespace.ns.namespace
  name           = "${local.name_prefix}-assets"
  access_type    = "NoPublicAccess"
  storage_tier   = "Standard"
  freeform_tags  = local.common_tags
}
