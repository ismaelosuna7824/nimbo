import type { FileChangesRequest, WorkflowFile, WorkflowFileKind } from "@/lib/api/types"
import { ancestors, languageOf } from "./file-model"

/**
 * File operations of the Code tab not applied yet. Edited contents live in the tab's buffers (keyed by the path
 * shown); this holds the structure: new files, moves (renames) and deletions of files on the server, and empty
 * folders, which exist only here until a file is put in them (the backend stores files, not folders).
 */
export interface PendingFiles {
  /** New files (not on the server yet). */
  created: Record<string, true>
  /** Moved files: path shown → path on the server. */
  moved: Record<string, string>
  /** Deleted files: paths on the server. */
  deleted: Record<string, true>
  /** Empty folders made in the tree. */
  folders: string[]
}

export const NO_PENDING: PendingFiles = { created: {}, moved: {}, deleted: {}, folders: [] }

/** Unsaved text of one file and the content it was edited from ("" for a new file). */
export interface Buffer {
  base: string
  value: string
}

export type PendingStatus = "added" | "moved"

export function hasStructureChanges(p: PendingFiles): boolean {
  return Object.keys(p.created).length + Object.keys(p.moved).length + Object.keys(p.deleted).length > 0
}

/** Path of a shown file on the server (undefined for a new file). */
export function serverPath(p: PendingFiles, path: string): string | undefined {
  if (p.created[path]) return undefined
  return p.moved[path] ?? path
}

function kindOf(path: string): WorkflowFileKind {
  if (path.startsWith("modules/")) return "module"
  if (path.endsWith(".tfvars")) return "tfvars"
  if (!path.includes("/") && path.endsWith(".tf")) return "terraform"
  return "asset"
}

/** A file created in the tree. */
export function newFile(path: string, size = 0): WorkflowFile {
  return {
    path,
    size,
    kind: kindOf(path),
    generated: false,
    editable: true,
    movable: true,
    binary: false,
    language: languageOf(path),
  }
}

/**
 * The files the tree shows: the server's with pending moves and deletions applied, plus new files. A path is shown
 * once: right after an apply the server lists files the pending state still holds, and the server's entry wins.
 */
export function viewFiles(
  server: WorkflowFile[],
  p: PendingFiles,
  buffers: Record<string, Buffer>,
): { files: WorkflowFile[]; status: Record<string, PendingStatus> } {
  const status: Record<string, PendingStatus> = {}
  if (!hasStructureChanges(p)) return { files: server, status }
  const onServer = new Set(server.map((f) => f.path))
  const movedTo = new Map(Object.entries(p.moved).filter(([to]) => !onServer.has(to)).map(([to, from]) => [from, to]))
  const files: WorkflowFile[] = []
  for (const f of server) {
    if (p.deleted[f.path]) continue
    const to = movedTo.get(f.path)
    if (to === undefined) {
      files.push(f)
      continue
    }
    files.push({ ...f, path: to, kind: f.generated ? f.kind : kindOf(to), language: languageOf(to) })
    status[to] = "moved"
  }
  for (const path of Object.keys(p.created)) {
    if (onServer.has(path)) continue
    files.push(newFile(path, new TextEncoder().encode(buffers[path]?.value ?? "").length))
    status[path] = "added"
  }
  return { files, status }
}

/** Every path shown at or below path (a file, or a folder's files). */
function scope(files: WorkflowFile[], path: string): string[] {
  return files.filter((f) => f.path === path || f.path.startsWith(`${path}/`)).map((f) => f.path)
}

/** Adds a new file; its folders are no longer empty. */
export function createFile(p: PendingFiles, path: string): PendingFiles {
  const dirs = new Set(ancestors(path))
  return { ...p, created: { ...p.created, [path]: true }, folders: p.folders.filter((d) => !dirs.has(d)) }
}

export function createFolder(p: PendingFiles, path: string): PendingFiles {
  return p.folders.includes(path) ? p : { ...p, folders: [...p.folders, path] }
}

/** Renames (moves) a file or a folder with everything in it; renames lists [old, new] paths of the files. */
export function renamePath(
  p: PendingFiles,
  files: WorkflowFile[],
  from: string,
  to: string,
): { pending: PendingFiles; renames: [string, string][] } {
  const created = { ...p.created }
  const moved = { ...p.moved }
  const renames: [string, string][] = []
  for (const old of scope(files, from)) {
    const next = to + old.slice(from.length)
    renames.push([old, next])
    if (created[old]) {
      delete created[old]
      created[next] = true
      continue
    }
    const origin = moved[old] ?? old
    delete moved[old]
    if (origin !== next) moved[next] = origin
  }
  const destDirs = new Set(ancestors(to))
  const folders = p.folders
    .map((d) => (d === from || d.startsWith(`${from}/`) ? to + d.slice(from.length) : d))
    .filter((d) => !destDirs.has(d) || d === to)
  return { pending: { ...p, created, moved, folders: [...new Set(folders)] }, renames }
}

/** Deletes a file or a folder with everything in it; removed lists the paths that disappear. */
export function deletePath(
  p: PendingFiles,
  files: WorkflowFile[],
  path: string,
): { pending: PendingFiles; removed: string[] } {
  const created = { ...p.created }
  const moved = { ...p.moved }
  const deleted = { ...p.deleted }
  const removed = scope(files, path)
  for (const old of removed) {
    if (created[old]) {
      delete created[old]
      continue
    }
    deleted[moved[old] ?? old] = true
    delete moved[old]
  }
  return { pending: { created, moved, deleted, folders: p.folders.filter((d) => d !== path && !d.startsWith(`${path}/`)) }, removed }
}

/**
 * Orders moves so that none lands on a file another move has not taken away yet; a cycle (a swap) goes through a
 * temporary name.
 */
export function orderMoves(moves: { from: string; to: string }[]): { from: string; to: string }[] {
  const todo = moves.map((m) => ({ ...m }))
  const out: { from: string; to: string }[] = []
  let tmp = 0
  while (todo.length) {
    const i = todo.findIndex((m) => !todo.some((o) => o !== m && o.from === m.to))
    if (i >= 0) {
      out.push(todo.splice(i, 1)[0])
      continue
    }
    const m = todo[0]
    const parked = `${m.from}-moving-${++tmp}`
    out.push({ from: m.from, to: parked })
    m.from = parked
  }
  return out
}

/** The request applying everything pending: moves first, then deletions and contents (shown paths). */
export function changesRequest(p: PendingFiles, buffers: Record<string, Buffer>): FileChangesRequest {
  const changes: Record<string, string | null> = {}
  for (const path of Object.keys(p.deleted)) changes[path] = null
  for (const path of Object.keys(p.created)) changes[path] = buffers[path]?.value ?? ""
  for (const [path, b] of Object.entries(buffers)) changes[path] = b.value
  const moves = orderMoves(Object.entries(p.moved).map(([to, from]) => ({ from, to })))
  return { changes, ...(moves.length ? { moves } : {}) }
}

/** Distinct files with pending changes (edited, new, moved or deleted). */
export function pendingPaths(p: PendingFiles, buffers: Record<string, Buffer>): string[] {
  return [
    ...new Set([...Object.keys(buffers), ...Object.keys(p.created), ...Object.keys(p.moved), ...Object.keys(p.deleted)]),
  ]
}
