output "storage_account_name" {
  value = azurerm_storage_account.main.name
}

output "queue_name" {
  value = azurerm_storage_queue.jobs.name
}

output "container_name" {
  value = azurerm_storage_container.uploads.name
}
