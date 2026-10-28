import type { AccessField, ResourceAccess } from "@/lib/api/types"

/** Identifiers of a deployed resource worth having at hand, most useful first. */
const KEYS = [
  "arn",
  "name",
  "id",
  "url",
  "invoke_url",
  "function_url",
  "queue_url",
  "endpoint",
  "api_endpoint",
  "invoke_arn",
  "bucket",
  "bucket_domain_name",
  "dns_name",
  "address",
  "ip_address",
  "self_link",
]

/** "Queue URL" → "queue_url": a usage field's label as the state attribute it shows. */
function attributeOf(label: string) {
  return label
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_|_$/g, "")
}

/**
 * The deployed resource's identifiers that the usage card (`access`) doesn't already show, for the Connect section:
 * a state value is left out when a usage field has its name ("ARN" ~ arn) or its value, and when an earlier
 * identifier has the same value (a log group's id is its name).
 */
export function liveHighlights(values: Record<string, unknown>, access?: ResourceAccess): AccessField[] {
  const fields = access?.fields ?? []
  const seen = new Set<string>([...fields.map((f) => f.value), ...(access?.endpoints ?? []).map((e) => e.url)])
  const named = new Set(fields.map((f) => attributeOf(f.label)))
  const out: AccessField[] = []
  for (const key of KEYS) {
    const v = values[key]
    if (typeof v !== "string" && typeof v !== "number") continue
    const value = String(v)
    if (value === "" || seen.has(value) || named.has(key)) continue
    seen.add(value)
    out.push({ label: key, value })
  }
  return out
}
