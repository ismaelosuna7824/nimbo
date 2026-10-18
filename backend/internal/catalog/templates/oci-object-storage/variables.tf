variable "region" {
  type        = string
  default     = "us-ashburn-1"
  description = "OCI region."
}

variable "compartment_ocid" {
  type        = string
  default     = "ocid1.tenancy.oc1..flocilocaltenancy0000000000000000000000000000000000000000"
  description = "Compartment OCID for the bucket (defaults to the local Floci tenancy root compartment)."
}

variable "bucket_name" {
  type        = string
  default     = "nimbo-assets"
  description = "Bucket name, unique within the namespace."
}
