import { describe, expect, test } from "bun:test"
import { classifySetting, SETTINGS_GROUPS, type SettingsGroupId } from "./settings-groups"

// Real attribute and block names of the providers' schemas, grouped as the inspector shows them.
const CASES: Record<string, Partial<Record<SettingsGroupId, string[]>>> = {
  aws_cloudwatch_log_group: {
    general: ["name", "name_prefix", "region"],
    logging: ["retention_in_days", "log_group_class"],
    security: ["kms_key_id"],
    backup: ["skip_destroy", "deletion_protection_enabled"],
    tags: ["tags"],
  },
  aws_instance: {
    general: ["ami", "availability_zone", "placement_group"],
    networking: ["subnet_id", "vpc_security_group_ids", "associate_public_ip_address", "private_ip", "ipv6_address_count", "source_dest_check", "network_interface"],
    security: ["key_name", "iam_instance_profile", "get_password_data", "metadata_options"],
    capacity: ["instance_type", "cpu_options", "credit_specification"],
    storage: ["ebs_optimized", "root_block_device", "ebs_block_device"],
    logging: ["monitoring"],
    backup: ["disable_api_termination", "disable_api_stop"],
    tags: ["tags", "volume_tags"],
    advanced: ["user_data", "hibernation", "tenancy", "timeouts"],
  },
  aws_db_instance: {
    general: ["identifier", "engine", "engine_version"],
    networking: ["db_subnet_group_name", "publicly_accessible", "port", "vpc_security_group_ids"],
    security: ["storage_encrypted", "kms_key_id", "password", "username", "iam_database_authentication_enabled", "ca_cert_identifier"],
    capacity: ["instance_class"],
    storage: ["allocated_storage", "max_allocated_storage", "iops", "db_name"],
    logging: ["performance_insights_enabled", "monitoring_interval", "monitoring_role_arn", "enabled_cloudwatch_logs_exports"],
    backup: ["backup_retention_period", "deletion_protection", "skip_final_snapshot", "copy_tags_to_snapshot", "multi_az", "restore_to_point_in_time"],
    advanced: ["apply_immediately", "allow_major_version_upgrade", "blue_green_update"],
  },
  aws_dynamodb_table: {
    storage: ["hash_key", "range_key", "attribute", "global_secondary_index", "ttl"],
    capacity: ["billing_mode", "read_capacity", "write_capacity"],
    backup: ["point_in_time_recovery"],
    security: ["server_side_encryption"],
  },
  aws_lambda_function: {
    general: ["function_name", "runtime", "handler", "filename", "s3_bucket", "s3_key", "image_uri"],
    capacity: ["memory_size", "reserved_concurrent_executions", "architectures"],
    security: ["role", "kms_key_arn"],
    networking: ["vpc_config"],
    logging: ["tracing_config", "logging_config"],
  },
  aws_s3_bucket: {
    general: ["bucket", "bucket_prefix"],
    security: ["acl", "policy", "server_side_encryption_configuration"],
    backup: ["versioning", "object_lock_enabled", "force_destroy", "replication_configuration"],
    storage: ["lifecycle_rule"],
    logging: ["logging"],
  },
  aws_sqs_queue: {
    security: ["kms_master_key_id", "sqs_managed_sse_enabled", "policy"],
    storage: ["message_retention_seconds"],
    advanced: ["redrive_policy", "visibility_timeout_seconds"],
  },
  azurerm_storage_account: {
    general: ["name", "location", "resource_group_name", "account_kind"],
    capacity: ["account_tier", "access_tier"],
    backup: ["account_replication_type", "cross_tenant_replication_enabled", "immutability_policy"],
    networking: ["public_network_access_enabled", "network_rules", "allow_nested_items_to_be_public"],
    security: ["min_tls_version", "https_traffic_only_enabled", "shared_access_key_enabled", "infrastructure_encryption_enabled", "customer_managed_key", "identity"],
    storage: ["blob_properties", "share_properties", "is_hns_enabled", "large_file_share_enabled"],
    tags: ["tags"],
    advanced: ["static_website", "queue_properties"],
  },
  azurerm_kubernetes_cluster: {
    general: ["kubernetes_version"],
    capacity: ["default_node_pool", "sku_tier"],
    networking: ["dns_prefix", "network_profile", "private_cluster_enabled"],
    security: ["identity", "key_vault_secrets_provider", "role_based_access_control_enabled", "workload_identity_enabled"],
    logging: ["oms_agent", "monitor_metrics"],
  },
  azurerm_linux_virtual_machine: {
    capacity: ["size"],
    security: ["admin_username", "admin_ssh_key", "disable_password_authentication", "secure_boot_enabled"],
    storage: ["os_disk"],
    networking: ["network_interface_ids"],
    logging: ["boot_diagnostics"],
    advanced: ["custom_data"],
  },
  google_container_cluster: {
    general: ["name", "location", "project", "description", "min_master_version"],
    networking: ["network", "subnetwork", "ip_allocation_policy", "private_cluster_config", "master_authorized_networks_config", "network_policy"],
    security: ["master_auth", "workload_identity_config", "database_encryption", "binary_authorization", "enable_shielded_nodes"],
    capacity: ["initial_node_count", "node_config", "node_pool", "cluster_autoscaling", "remove_default_node_pool"],
    logging: ["logging_service", "monitoring_service", "logging_config", "monitoring_config"],
    backup: ["deletion_protection"],
    tags: ["resource_labels"],
    advanced: ["release_channel", "addons_config", "maintenance_policy", "enable_autopilot"],
  },
  google_compute_instance: {
    capacity: ["machine_type", "guest_accelerator"],
    storage: ["boot_disk", "attached_disk"],
    security: ["service_account", "shielded_instance_config"],
    tags: ["labels", "tags"],
  },
  google_sql_database_instance: {
    general: ["database_version", "region"],
    security: ["root_password", "encryption_key_name"],
  },
  oci_core_instance: {
    general: ["display_name", "compartment_id", "availability_domain", "fault_domain", "image"],
    capacity: ["shape", "shape_config"],
    networking: ["subnet_id", "create_vnic_details", "hostname_label"],
    security: ["is_pv_encryption_in_transit_enabled"],
    backup: ["preserve_boot_volume"],
    tags: ["freeform_tags", "defined_tags"],
    advanced: ["metadata", "agent_config", "launch_options", "source_details"],
  },
  oci_database_autonomous_database: {
    capacity: ["compute_count", "cpu_core_count", "is_auto_scaling_enabled"],
    storage: ["data_storage_size_in_gb", "db_name"],
    backup: ["backup_retention_period_in_days"],
    security: ["admin_password", "kms_key_id"],
    networking: ["subnet_id", "whitelisted_ips", "private_endpoint_label"],
  },
}

describe("classifySetting", () => {
  for (const [type, groups] of Object.entries(CASES)) {
    test(type, () => {
      for (const [group, names] of Object.entries(groups)) {
        for (const name of names) expect({ name, group: classifySetting(name) }).toEqual({ name, group: group as SettingsGroupId })
      }
    })
  }

  test("ambiguous names go to the more specific group", () => {
    // A security group is a network firewall, not a credential.
    expect(classifySetting("vpc_security_group_ids")).toBe("networking")
    // Encryption of storage is about security; the size of storage is storage.
    expect(classifySetting("storage_encrypted")).toBe("security")
    expect(classifySetting("data_storage_size_in_gb")).toBe("storage")
    // Tags copied to snapshots configure snapshots; only the tag maps themselves are Tags.
    expect(classifySetting("copy_tags_to_snapshot")).toBe("backup")
    // The role that publishes metrics belongs with the metrics.
    expect(classifySetting("monitoring_role_arn")).toBe("logging")
    // A private key is a secret even though "private" is a network word.
    expect(classifySetting("private_key")).toBe("security")
    // A version is general only at the end of the name.
    expect(classifySetting("engine_version")).toBe("general")
    expect(classifySetting("allow_major_version_upgrade")).toBe("advanced")
    // "policy" is access control, except for schedules of upgrades.
    expect(classifySetting("upgrade_policy")).toBe("advanced")
  })

  test("words match whole tokens, not substrings", () => {
    // "description" holds "ip", "script" holds "ip": neither is networking.
    expect(classifySetting("description")).toBe("general")
    expect(classifySetting("metadata_startup_script")).toBe("advanced")
    // "tags" in the middle of a name isn't the tag map.
    expect(classifySetting("propagate_tags")).toBe("tags")
  })

  test("the description places names that match nothing", () => {
    expect(classifySetting("cmk_arn", "ARN of the KMS key used to encrypt the data. More text.")).toBe("security")
    expect(classifySetting("launch_targets", "IDs of the subnets to launch into.")).toBe("networking")
    // The name wins over the description.
    expect(classifySetting("placement_target", "IDs of the subnets to launch into.")).toBe("general")
    expect(classifySetting("vended", "Where vended logs are delivered.")).toBe("logging")
    expect(classifySetting("vended", "Something else entirely. It mentions logs later.")).toBe("advanced")
  })

  test("every group has a title, in display order ending with Advanced", () => {
    expect(SETTINGS_GROUPS.map((g) => g.id)).toEqual(["general", "networking", "security", "capacity", "storage", "logging", "backup", "tags", "advanced"])
  })
})
