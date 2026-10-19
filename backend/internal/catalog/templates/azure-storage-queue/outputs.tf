output "storage_account_name" {
  value       = azurerm_storage_account.main.name
  description = "Name of the storage account."
}

output "queue_name" {
  value       = azurerm_storage_queue.jobs.name
  description = "Name of the jobs queue."
}

output "container_name" {
  value       = azurerm_storage_container.uploads.name
  description = "Name of the private uploads container."
}
