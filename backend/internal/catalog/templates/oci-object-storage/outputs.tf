output "namespace" {
  value       = data.oci_objectstorage_namespace.ns.namespace
  description = "Object Storage namespace of the tenancy."
}

output "bucket_name" {
  value       = oci_objectstorage_bucket.assets.name
  description = "Name of the assets bucket."
}
