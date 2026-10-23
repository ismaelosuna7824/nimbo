import type { ImportProject } from "@/lib/api/types"

/**
 * Which scanned projects start selected: all but reusable modules nothing calls and, where sibling Dockerfile folders
 * are also offered as one workflow (a group with `members`), the group instead of its members.
 */
export function initialSelection(projects: ImportProject[]): Record<string, boolean> {
  const grouped = new Set(projects.flatMap((p) => p.members ?? []))
  return Object.fromEntries(
    projects.map((p) => [p.path, !grouped.has(p.path) && !p.diagnostics.some((d) => d.code === "unused_module")]),
  )
}

/** Selects or clears one project; a Dockerfile group and its members exclude each other (one workflow or N). */
export function toggleProject(
  projects: ImportProject[],
  selected: Record<string, boolean>,
  path: string,
  on: boolean,
): Record<string, boolean> {
  const next = { ...selected, [path]: on }
  if (!on) return next
  for (const p of projects) {
    if (p.path === path) p.members?.forEach((m) => (next[m] = false))
    else if (p.members?.includes(path)) next[p.path] = false
  }
  return next
}

/** Selects every project (a Dockerfile group rather than its members) or none. */
export function selectAll(projects: ImportProject[], on: boolean): Record<string, boolean> {
  const grouped = new Set(projects.flatMap((p) => p.members ?? []))
  return Object.fromEntries(projects.map((p) => [p.path, on && !grouped.has(p.path)]))
}
