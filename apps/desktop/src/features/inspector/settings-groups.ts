// Which group of the inspector's Settings a Terraform attribute or nested block belongs to, from its name alone (plus
// its description when the name says nothing). Provider-agnostic: the same words mean the same thing in aws_*,
// azurerm_*, google_* and oci_* schemas.
//
// Names are split into "_" tokens and matched against each group's words; a word ending in "*" matches any token
// starting with it (subnet* → subnetwork). A name often holds words of several groups ("storage_encrypted",
// "vpc_security_group_ids", "copy_tags_to_snapshot"); it goes to the more specific one, which is the first group in
// RULES order:
//
//   1. Tags & labels         only the tag maps themselves (tags, labels, freeform_tags, *_tags, *_labels)
//   2. Logging & monitoring  what an attribute is *for* beats how it's secured: monitoring_role_arn, performance_insights_kms_key_id
//   3. Networking            security_group is a network firewall; network_policy, ip_allocation_policy are network settings
//   4. Backup & protection   snapshot, deletion protection, replication, restore… (copy_tags_to_snapshot, backup_retention_period)
//   5. Security & encryption keys, IAM, TLS, passwords, identities (storage_encrypted, kms_key_id, iam_instance_profile)
//   6. Storage & data        disks, volumes, buckets, tables (allocated_storage, data_storage_size_in_gb)
//   7. Capacity & scaling    sizes, counts, tiers, SKUs, node pools (instance_type, account_tier, memory_size)
//   8. General               identity and placement: name, description, region, image, engine, *_version
//   9. Advanced              everything else
//
// EXACT overrides names whose words point to the wrong group (hash_key is a table key, not a secret; bucket is the
// S3 bucket's name; s3_key/s3_bucket of a Lambda say where its code is).

export type SettingsGroupId = "general" | "networking" | "security" | "capacity" | "storage" | "logging" | "backup" | "tags" | "advanced"

/** Display order of the groups (after "Required"). */
export const SETTINGS_GROUPS: readonly { id: SettingsGroupId; title: string }[] = [
  { id: "general", title: "General" },
  { id: "networking", title: "Networking" },
  { id: "security", title: "Security & encryption" },
  { id: "capacity", title: "Capacity & scaling" },
  { id: "storage", title: "Storage & data" },
  { id: "logging", title: "Logging & monitoring" },
  { id: "backup", title: "Backup & protection" },
  { id: "tags", title: "Tags & labels" },
  { id: "advanced", title: "Advanced" },
]

const EXACT: Record<string, SettingsGroupId> = {
  // Keys of a table's data model.
  hash_key: "storage",
  range_key: "storage",
  attribute: "storage",
  // Where a function's code comes from: what it runs, like an image.
  s3_bucket: "general",
  s3_key: "general",
  s3_object_version: "general",
  filename: "general",
  source_code_hash: "general",
  // The bucket's own name.
  bucket: "general",
  bucket_prefix: "general",
  // Queue behavior, not access control.
  redrive_policy: "advanced",
  redrive_allow_policy: "advanced",
  maintenance_policy: "advanced",
  // Boot scripts.
  user_data: "advanced",
  user_data_base64: "advanced",
  custom_data: "advanced",
  // IMDS settings (IMDSv2) are a security control.
  metadata_options: "security",
  credit_specification: "capacity",
  eviction_policy: "capacity",
  source_dest_check: "networking",
  disable_api_stop: "backup",
  multi_az: "backup",
  retention_in_days: "logging",
  // The engine's version, like engine_version, not a database setting.
  database_version: "general",
  db_version: "general",
}

interface Rule {
  group: SettingsGroupId
  words: string[]
  /** Multi-token phrases matched anywhere in the name. */
  phrases?: string[]
  /** The name is not this group when it holds one of these words (private_key is a secret, not a network). */
  unless?: string[]
  /** Matches only on the last token (engine_version, not allow_major_version_upgrade). */
  last?: string[]
}

const RULES: Rule[] = [
  { group: "tags", words: ["tags", "labels"], last: ["tags", "labels"] },
  {
    group: "logging",
    words: ["log*", "monitor*", "metric*", "alarm*", "insight*", "trac*", "audit*", "diagnostic*", "telemetry", "cloudwatch", "oms", "notification*", "alert*", "observability"],
  },
  {
    group: "networking",
    words: [
      "vpc", "vnet", "subnet*", "network*", "ip", "ips", "ipv4", "ipv6", "cidr", "port", "ports", "dns", "fqdn", "hostname",
      "endpoint*", "gateway*", "nat", "route*", "routing", "ingress", "egress", "firewall*", "peering", "vnic", "nic", "proxy",
      "listener*", "public", "publicly", "private", "networking",
    ],
    phrases: ["security_group", "security_groups", "load_balancer"],
    unless: ["key", "kms", "password", "secret", "encryption", "encrypted"],
  },
  {
    group: "backup",
    words: [
      "backup*", "snapshot*", "versioning", "recovery", "restore", "deletion", "protection", "termination", "destroy", "preserve",
      "lock", "immutability", "retain", "replication", "pitr", "disaster", "failover", "delete",
    ],
    phrases: ["point_in_time", "soft_delete"],
  },
  {
    group: "security",
    words: [
      "kms", "iam", "tls", "ssl", "sse", "https", "password*", "passwd", "secret*", "credential*", "token*", "key", "keys", "encrypt*",
      "cert*", "auth*", "role", "roles", "policy", "policies", "identity", "admin", "username", "acl", "grant", "rbac", "abac", "sas",
      "shielded", "secure", "vtpm", "confidential", "principal", "signing", "oidc", "permission*", "sensitive",
    ],
    phrases: ["service_account"],
    // When a cluster or engine upgrades is scheduling, not access control (upgrade_policy).
    unless: ["upgrade", "maintenance"],
  },
  {
    group: "storage",
    words: [
      "storage", "disk*", "volume*", "ebs", "block", "bucket*", "blob*", "share", "file", "files", "nfs*", "efs", "iops", "table",
      "database", "db", "retention", "lifecycle", "stream", "ttl", "index", "hns", "tablespace",
    ],
    phrases: ["file_system", "character_set"],
  },
  {
    group: "capacity",
    words: [
      "size", "sizes", "count", "capacity", "memory", "cpu", "cpus", "vcpu", "vcpus", "ocpus", "shape", "tier", "sku", "replica",
      "replicas", "autoscaling", "autoscaler", "scaling", "scale", "throughput", "concurrency", "concurrent", "compute", "node",
      "nodes", "billing", "provisioned", "architectures", "accelerator",
    ],
    phrases: ["instance_type", "instance_class", "machine_type", "auto_scaling"],
  },
  {
    group: "general",
    words: [
      "name", "prefix", "description", "display", "title", "identifier", "comment", "region", "location", "zone", "availability",
      "project", "compartment", "placement", "fault", "engine", "runtime", "handler", "ami", "image", "platform", "kind", "edition",
    ],
    phrases: ["resource_group"],
    last: ["version"],
  },
]

/** Description words that place an attribute whose name matched nothing (checked on its first sentence). */
const DESCRIPTION_HINTS: [RegExp, SettingsGroupId][] = [
  [/\b(KMS|encrypt\w*|TLS|SSL|certificates?)\b/i, "security"],
  [/\b(subnets?|VPC|security groups?|IP address(es)?|CIDR)\b/i, "networking"],
  [/\b(snapshots?|backups?)\b/i, "backup"],
  [/\b(logs?|logging|metrics|monitoring)\b/i, "logging"],
]

function hasWord(tokens: string[], word: string) {
  return word.endsWith("*") ? tokens.some((t) => t.startsWith(word.slice(0, -1))) : tokens.includes(word)
}

function matches(rule: Rule, name: string, tokens: string[]) {
  if (rule.unless?.some((w) => hasWord(tokens, w))) return false
  if (rule.last?.includes(tokens[tokens.length - 1])) return true
  // A `last` word only counts at the end (tags of copy_tags_to_snapshot are the snapshot's).
  if (rule.words.some((w) => !rule.last?.includes(w) && hasWord(tokens, w))) return true
  return !!rule.phrases?.some((p) => `_${name}_`.includes(`_${p}_`))
}

/** The Settings group of attribute or nested block `name` (`description` helps names that match nothing). */
export function classifySetting(name: string, description?: string): SettingsGroupId {
  const exact = EXACT[name]
  if (exact) return exact
  const tokens = name.toLowerCase().split("_")
  const rule = RULES.find((r) => matches(r, name.toLowerCase(), tokens))
  if (rule) return rule.group
  const sentence = description?.split(/\.\s/)[0] ?? ""
  return DESCRIPTION_HINTS.find(([re]) => re.test(sentence))?.[1] ?? "advanced"
}
