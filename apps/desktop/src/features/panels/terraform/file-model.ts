import type { FileLanguage, WorkflowFile } from "@/lib/api/types"

/** Files above this size are not fetched for content search. */
export const SEARCH_MAX_BYTES = 1 << 20

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

export function dirName(path: string): string {
  const i = path.lastIndexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

/** A relative path ("./api", "../x/.env") resolved from a folder of the workflow; null when it leaves the workflow. */
export function joinPath(dir: string, rel: string): string | null {
  if (rel.startsWith("/")) return null
  const out = dir ? dir.split("/") : []
  for (const seg of rel.split("/")) {
    if (seg === "" || seg === ".") continue
    if (seg === "..") {
      if (!out.length) return null
      out.pop()
    } else out.push(seg)
  }
  return out.join("/")
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

/** Builds the folder tree (plus empty folders); folders before files, each sorted by name. */
export function buildTree(files: WorkflowFile[], emptyDirs: string[] = []): DirNode {
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
  for (const d of emptyDirs) dirOf(d)
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
  dockerfile: "Dockerfile",
  java: "Java",
  kotlin: "Kotlin",
  csharp: "C#",
  rust: "Rust",
  php: "PHP",
  ruby: "Ruby",
  sql: "SQL",
  toml: "TOML",
  ini: "INI",
  properties: "Properties",
  xml: "XML",
  html: "HTML",
  css: "CSS",
  nginx: "NGINX config",
  makefile: "Makefile",
  protobuf: "Protocol Buffers",
  text: "Plain text",
}

/** Why a file can't be edited in Pro mode (undefined: it can). */
export function readOnlyReason(file: WorkflowFile): string | undefined {
  return file.editable ? undefined : (file.readOnlyReason ?? "Read-only")
}

/** The syntax of a file by its name (as the daemon names it). */
export function languageOf(path: string): FileLanguage {
  const base = baseName(path).toLowerCase()
  if (base.endsWith(".tf.json") || base.endsWith(".tfvars.json")) return "json"
  if (base === ".env" || base.startsWith(".env.") || base.endsWith(".env")) return "shell"
  if (/^(dockerfile|containerfile)(\.[\w.-]+)?$|\.dockerfile$/.test(base)) return "dockerfile"
  const named = NAME_LANGUAGE[base]
  if (named) return named
  const ext = base.includes(".") ? base.slice(base.lastIndexOf(".")) : ""
  if (ext === ".conf") {
    // nginx keeps its server blocks in nginx/… or conf.d/…; other .conf files are INI-like.
    const dirs = path.toLowerCase().split("/").slice(0, -1)
    return dirs.includes("nginx") || dirs.includes("conf.d") ? "nginx" : "ini"
  }
  return EXT_LANGUAGE[ext] ?? "text"
}

const NAME_LANGUAGE: Record<string, FileLanguage> = {
  gemfile: "ruby",
  rakefile: "ruby",
  makefile: "makefile",
  gnumakefile: "makefile",
  "nginx.conf": "nginx",
  ".editorconfig": "ini",
}

const EXT_LANGUAGE: Record<string, FileLanguage> = {
  ".tf": "hcl",
  ".tfvars": "hcl",
  ".hcl": "hcl",
  ".json": "json",
  ".js": "javascript",
  ".mjs": "javascript",
  ".cjs": "javascript",
  ".jsx": "javascript",
  ".ts": "typescript",
  ".mts": "typescript",
  ".cts": "typescript",
  ".tsx": "typescript",
  ".py": "python",
  ".go": "go",
  ".yaml": "yaml",
  ".yml": "yaml",
  ".md": "markdown",
  ".markdown": "markdown",
  ".sh": "shell",
  ".bash": "shell",
  ".zsh": "shell",
  ".java": "java",
  ".kt": "kotlin",
  ".kts": "kotlin",
  ".cs": "csharp",
  ".csx": "csharp",
  ".rs": "rust",
  ".php": "php",
  ".phtml": "php",
  ".rb": "ruby",
  ".rake": "ruby",
  ".gemspec": "ruby",
  ".sql": "sql",
  ".toml": "toml",
  ".properties": "properties",
  ".xml": "xml",
  ".xsd": "xml",
  ".xsl": "xml",
  ".plist": "xml",
  ".csproj": "xml",
  ".fsproj": "xml",
  ".vbproj": "xml",
  ".props": "xml",
  ".targets": "xml",
  ".html": "html",
  ".htm": "html",
  ".css": "css",
  ".scss": "css",
  ".less": "css",
  ".proto": "protobuf",
  ".mk": "makefile",
  ".nginx": "nginx",
  ".ini": "ini",
  ".cfg": "ini",
  ".cnf": "ini",
}

/** File types the Code tab creates; names without an extension (Makefile) are accepted too. */
export const NEW_FILE_EXTENSIONS = [".tf", ".tfvars", ".tftpl", ".md", ".json", ".yaml", ".yml", ".sh", ".py", ".js", ".ts", ".txt"]

/** HCL the format endpoint understands (terraform fmt). */
export const isFormattable = (path: string) => /\.(tf|tfvars|hcl)$/.test(path)

/** YAML the editor re-indents itself (⌥⇧F): comments, key order and values are kept. */
export const isYaml = (path: string) => /\.ya?ml$/i.test(path)

/**
 * Checks a name typed in the tree: a file or folder name, or a relative path of them ("modules/net/main.tf"),
 * without absolute paths, "..", hidden names or characters other systems reject; files need a supported type.
 * Docker workspaces hold whole build contexts: any file type and dotfiles (.env, .dockerignore) are accepted.
 * Returns the problem, or undefined.
 */
export function nameProblem(input: string, kind: "file" | "folder", docker = false): string | undefined {
  const name = input.trim()
  if (!name) return "Enter a name"
  if (name.startsWith("/") || /^[A-Za-z]:/.test(name)) return "Use a path relative to the workflow files"
  if (name.includes("\\")) return "Use / to separate folders"
  const segments = name.split("/")
  for (const s of segments) {
    if (!s) return "Folder names can't be empty"
    if (s === "." || s === "..") return "“.” and “..” are not allowed"
    if (s.startsWith(".") && !docker) return "Hidden names (starting with “.”) are not exported"
    if (/[:*?"<>|]/.test(s) || /\p{Cc}/u.test(s)) return 'Names can\'t contain : * ? " < > | or control characters'
    if (s.length > 128) return "Names can't be longer than 128 characters"
  }
  if (kind === "folder" || docker) return undefined
  const base = segments[segments.length - 1].toLowerCase()
  if (/\.tfstate|^terraform\.tfstate/.test(base)) return "Terraform state is not stored with the workflow"
  const dot = base.lastIndexOf(".")
  if (dot >= 0 && !NEW_FILE_EXTENSIONS.includes(base.slice(dot)))
    return `Unsupported file type “${base.slice(dot)}” — use ${NEW_FILE_EXTENSIONS.join(", ")} or a name without extension`
  return undefined
}
