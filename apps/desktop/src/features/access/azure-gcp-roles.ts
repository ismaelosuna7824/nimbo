import type { AccessCatalog, WorkflowNode } from "@/lib/api/types"
import { nodeAddress } from "../canvas/catalog"

/** A built-in (Azure) or predefined (GCP) role of the pinned catalog. */
export type CatalogRole = NonNullable<AccessCatalog["roles"]>[number]

const LEVEL_LABEL = { read: "Read", write: "Write", manage: "Manage" } as const

/** "Suggested for Read" for roles the catalog suggests for the resource type. */
export function suggestedLabel(role: CatalogRole): string | undefined {
  return role.suggested ? `Suggested for ${LEVEL_LABEL[role.suggested]}` : undefined
}

/**
 * Roles matching `query` (name, ID, description or category; every word must match), best first: the catalog's
 * suggested roles keep their order on top, then exact matches, then names/IDs starting with the query, then the rest.
 * At most `limit` roles (the lists have hundreds to thousands).
 */
export function filterRoles(roles: CatalogRole[], query: string, limit = 60): CatalogRole[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean)
  if (words.length === 0) return roles.slice(0, limit)
  const q = words.join(" ")
  const scored: { role: CatalogRole; score: number; i: number }[] = []
  roles.forEach((role, i) => {
    const name = role.name.toLowerCase()
    const id = role.id.toLowerCase()
    const hay = `${name} ${id} ${(role.description ?? "").toLowerCase()} ${(role.category ?? "").toLowerCase()}`
    if (!words.every((w) => hay.includes(w))) return
    let score = 4
    if (name === q || id === q || id === `roles/${q}`) score = 1
    else if (name.startsWith(q) || id.startsWith(q) || id.startsWith(`roles/${q}`)) score = 2
    else if (name.includes(q) || id.includes(q)) score = 3
    if (role.suggested) score = 0
    scored.push({ role, score, i })
  })
  scored.sort((a, b) => a.score - b.score || a.i - b.i)
  return scored.slice(0, limit).map((s) => s.role)
}

/**
 * The node an HCL expression references first ("azurerm_key_vault.main.id", "${google_storage_bucket.b.name}/x"),
 * or undefined.
 */
export function referencedNode(expr: string, nodes: WorkflowNode[]): WorkflowNode | undefined {
  const byAddress = new Map(nodes.map((n) => [nodeAddress(n), n]))
  for (const m of expr.matchAll(/(?<![\w.-])((?:data\.)?[A-Za-z_][\w-]*\.[A-Za-z_][\w-]*)(?=[.[])/g)) {
    const n = byAddress.get(m[1])
    if (n) return n
  }
  return undefined
}
