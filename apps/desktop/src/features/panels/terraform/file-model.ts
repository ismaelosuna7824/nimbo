import type { WorkflowFile } from "@/lib/api/types"

/** Files above this size are not fetched for content search. */
export const SEARCH_MAX_BYTES = 1 << 20

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

export function dirName(path: string): string {
  const i = path.lastIndexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

/** All ancestor directories of a file, outermost first ("a/b/c.tf" → ["a", "a/b"]). */
export function ancestors(path: string): string[] {
  const out: string[] = []
  for (let i = path.indexOf("/"); i >= 0; i = path.indexOf("/", i + 1)) out.push(path.slice(0, i))
  return out
}

export interface DirNode {
  path: string
  name: string
  dirs: DirNode[]
  files: WorkflowFile[]
}

const byName = (a: string, b: string) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" })

/** Builds the folder tree; folders before files, each sorted by name. */
export function buildTree(files: WorkflowFile[]): DirNode {
  const root: DirNode = { path: "", name: "", dirs: [], files: [] }
  const dirs = new Map<string, DirNode>([["", root]])
  const dirOf = (path: string): DirNode => {
    let node = dirs.get(path)
    if (node) return node
    const parent = dirOf(dirName(path))
    node = { path, name: baseName(path), dirs: [], files: [] }
    parent.dirs.push(node)
    dirs.set(path, node)
    return node
  }
  for (const f of files) dirOf(dirName(f.path)).files.push(f)
  const sort = (d: DirNode) => {
    d.dirs.sort((a, b) => byName(a.name, b.name))
    d.files.sort((a, b) => byName(baseName(a.path), baseName(b.path)))
    d.dirs.forEach(sort)
  }
  sort(root)
  return root
}

export type TreeRow =
  | { type: "dir"; path: string; name: string; depth: number; open: boolean; count: number }
  | { type: "file"; path: string; name: string; depth: number; file: WorkflowFile; matches?: number }

/** The visible rows of the tree, depth-first. */
export function flattenTree(root: DirNode, isOpen: (dir: string, depth: number) => boolean): TreeRow[] {
  const rows: TreeRow[] = []
  const walk = (d: DirNode, depth: number) => {
    for (const sub of d.dirs) {
      const open = isOpen(sub.path, depth)
      rows.push({ type: "dir", path: sub.path, name: sub.name, depth, open, count: countFiles(sub) })
      if (open) walk(sub, depth + 1)
    }
    for (const f of d.files) rows.push({ type: "file", path: f.path, name: baseName(f.path), depth, file: f })
  }
  walk(root, 0)
  return rows
}

function countFiles(d: DirNode): number {
  return d.files.length + d.dirs.reduce((n, s) => n + countFiles(s), 0)
}

/** Case-insensitive: every whitespace-separated term is part of the path. */
export function matchesFilter(path: string, filter: string): boolean {
  const p = path.toLowerCase()
  return filter
    .toLowerCase()
    .split(/\s+/)
    .every((term) => !term || p.includes(term))
}

/** Case-insensitive occurrences of needle in text. */
export function countMatches(text: string, needle: string): number {
  if (!needle) return 0
  const hay = text.toLowerCase()
  const n = needle.toLowerCase()
  let count = 0
  for (let i = hay.indexOf(n); i >= 0; i = hay.indexOf(n, i + n.length)) count++
  return count
}

export const LANGUAGE_LABEL: Record<WorkflowFile["language"], string> = {
  hcl: "HCL",
  json: "JSON",
  javascript: "JavaScript",
  typescript: "TypeScript",
  python: "Python",
  go: "Go",
  yaml: "YAML",
  markdown: "Markdown",
  shell: "Shell",
  text: "Plain text",
}

/** Why a file can't be edited in Pro mode (undefined: it can). */
export function readOnlyReason(file: WorkflowFile): string | undefined {
  if (file.editable) return undefined
  if (file.binary) return "Binary file"
  if (file.kind === "tfvars") return "Generated from the environment values — edit them in Variables & Environments"
  if (file.kind === "env") return "Generated from the environment variables — edit them in Variables & Environments"
  if (file.kind === "scaffold")
    return "Generated on every export from the workflow (description, variables, outputs) — delete its first line in your exported copy to keep your own"
  if (baseName(file.path) === ".terraform.lock.hcl") return "Written by terraform init; kept as imported"
  return "Too large to edit here"
}
