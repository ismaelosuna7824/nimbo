variable "name" {
  type = string
}

output "id" {
  value = lower(replace(var.name, "_", "-"))
}
