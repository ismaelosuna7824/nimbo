import { create } from "zustand"
import type { ImportSource } from "@/lib/api/types"

/** A folder picked or dropped outside the dialog, waiting to be scanned. */
export interface PendingImport {
  source: ImportSource
  /** Shown while scanning, e.g. the folder name. */
  label: string
  /** Files left out of a browser upload (binary or too large). */
  omitted?: number
}

interface ImportSourceState {
  pending: PendingImport | null
  setPending: (p: PendingImport | null) => void
}

/** Hand-off from window drag & drop to the import dialog, which scans `pending` when it opens. */
export const useImportSource = create<ImportSourceState>((set) => ({
  pending: null,
  setPending: (pending) => set({ pending }),
}))

/** Directory names never read: same set the daemon skips (plus every hidden directory). */
const SKIPPED_DIRS: Record<string, true> = { node_modules: true, vendor: true, ".terragrunt-cache": true }
const skippedDir = (name: string) => name.startsWith(".") || SKIPPED_DIRS[name] === true
/**
 * Text files worth sending to the daemon from a browser upload (Terraform, Docker build contexts: compose files,
 * Dockerfiles, app sources, and Kubernetes manifests, kustomizations, Helm charts); binaries (zips, images) are left out.
 */
const TEXT_FILE =
  /\.(tf|tfvars|hcl|json|ya?ml|sh|bash|ps1|py|js|mjs|cjs|jsx|ts|tsx|vue|svelte|go|mod|sum|rb|java|kt|kts|gradle|properties|cs|csproj|fsproj|sln|razor|cshtml|rs|php|txt|md|tpl|tftpl|toml|lock|ini|cfg|conf|sql|xml|html|css|scss|example|env|dockerignore|gitignore|helmignore|dockerfile)$|(^|\/)(Dockerfile(\.[\w.-]+)?|Containerfile|Makefile|Gemfile|Procfile|requirements\.txt)$/i
const MAX_FILE_BYTES = 1 << 20
const MAX_TOTAL_BYTES = 18 << 20
const MAX_FILES = 5000

/** Whether a relative slash path is inside a skipped directory or is Terraform working data/state. */
function skipped(path: string) {
  const parts = path.split("/")
  const name = parts[parts.length - 1]
  if (parts.slice(0, -1).some(skippedDir)) return true
  return name.startsWith(".terraform") || name.includes(".tfstate") || name === ".DS_Store"
}

export class ImportTooLargeError extends Error {}

/** Reads text files into an upload map, skipping what the daemon ignores anyway. */
async function collect(entries: { path: string; file: () => Promise<File> }[]) {
  const files: Record<string, string> = {}
  let omitted = 0
  let total = 0
  let count = 0
  for (const e of entries) {
    if (skipped(e.path)) continue
    if (!TEXT_FILE.test(e.path)) {
      omitted++
      continue
    }
    const f = await e.file()
    if (f.size > MAX_FILE_BYTES) {
      omitted++
      continue
    }
    total += f.size
    if (++count > MAX_FILES || total > MAX_TOTAL_BYTES) {
      throw new ImportTooLargeError(
        "This folder is too large to upload from the browser. Choose a narrower folder (one project or a stacks folder), or use the desktop app.",
      )
    }
    files[e.path] = await f.text()
  }
  return { files, omitted }
}

/** Upload from an `<input type=file webkitdirectory>` or multi-file selection. */
export async function readFileList(list: FileList): Promise<PendingImport | null> {
  const all = Array.from(list)
  const { files, omitted } = await collect(
    all.map((f) => ({ path: f.webkitRelativePath || f.name, file: () => Promise.resolve(f) })),
  )
  if (Object.keys(files).length === 0) return null
  const folder = all[0]?.webkitRelativePath?.split("/")[0]
  return { source: { files }, label: folder || `${all.length} files`, omitted }
}

function readEntries(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  const reader = dir.createReader()
  const out: FileSystemEntry[] = []
  return new Promise((resolve, reject) => {
    // readEntries returns batches (~100 in Chromium) until an empty one.
    const next = () =>
      reader.readEntries((batch) => {
        if (batch.length === 0) return resolve(out)
        out.push(...batch)
        next()
      }, reject)
    next()
  })
}

/** Upload from a browser drop (folders keep their structure via webkitGetAsEntry). */
export async function readDataTransfer(dt: DataTransfer): Promise<PendingImport | null> {
  const roots = Array.from(dt.items)
    .map((i) => i.webkitGetAsEntry())
    .filter((e): e is FileSystemEntry => e !== null)
  const entries: { path: string; file: () => Promise<File> }[] = []
  const walk = async (entry: FileSystemEntry) => {
    const path = entry.fullPath.replace(/^\/+/, "")
    if (entry.isDirectory) {
      const name = entry.name
      if (path.includes("/") && skippedDir(name)) return
      for (const child of await readEntries(entry as FileSystemDirectoryEntry)) await walk(child)
      if (entries.length > MAX_FILES * 2) throw new ImportTooLargeError("This folder has too many files to upload from the browser.")
    } else {
      entries.push({
        path,
        file: () => new Promise<File>((resolve, reject) => (entry as FileSystemFileEntry).file(resolve, reject)),
      })
    }
  }
  for (const r of roots) await walk(r)
  const { files, omitted } = await collect(entries)
  if (Object.keys(files).length === 0) return null
  return { source: { files }, label: roots.length === 1 ? roots[0].name : `${roots.length} items`, omitted }
}

/**
 * Source for a path dropped onto the desktop window: a folder, or the folder of a dropped .tf, compose file, Dockerfile,
 * Chart.yaml or kustomization.yaml.
 */
export function pendingFromPath(path: string): PendingImport {
  const clean = path.replace(/[\\/]+$/, "")
  const dir = /\.(tf|tfvars)$|(^|[\\/])((docker-)?compose[\w.-]*\.ya?ml|Dockerfile[\w.-]*|Chart\.ya?ml|kustomization\.ya?ml)$/i.test(clean)
    ? clean.replace(/[\\/][^\\/]+$/, "")
    : clean
  return { source: { dir }, label: dir.split(/[\\/]/).pop() || dir }
}
