variable "location" {
  type        = string
  default     = "eastus"
  description = "Azure region."
}

variable "name" {
  type        = string
  default     = "nimbodemo"
  description = "Base name: lowercase letters and digits only (used for the storage account)."
}
