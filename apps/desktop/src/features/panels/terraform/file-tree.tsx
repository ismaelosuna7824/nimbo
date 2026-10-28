import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react"
import { useQueries } from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import {
  ChevronRight,
  Copy,
  Download,
  FilePlus,
  FilePlus2,
  FileSearch,
  FolderPlus,
  Link2,
  Package,
  Pencil,
  Search,
  Trash2,
  X,
} from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuShortcut,
  ContextMenuTrigger,
} from "@/components/ui/context-menu"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Kbd } from "@/components/ui/kbd"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { WorkflowFile } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { copyText, formatBytes } from "../lib/format"
import { FileIcon, FolderIcon } from "./file-icon"
import {
  ancestors,
  baseName,
  buildTree,
  countMatches,
  dirName,
  flattenTree,
  matchesFilter,
  nameProblem,
  SEARCH_MAX_BYTES,
  type TreeRow,
} from "./file-model"
import type { PendingStatus } from "./pending-files"
import { useFilesRequestFor } from "./reveal-files"

const ROW_HEIGHT = 24
/** Trees up to this many files start with every folder open; bigger ones with the top level only. */
const SMALL_TREE = 40
const DRAG_TYPE = "application/x-nimbo-file"

export interface OpenOptions {
  /** Move focus to the editor (Enter) instead of staying in the tree (click, Space). */
  focusEditor?: boolean
  /** Select the first match of this text once the file is shown. */
  find?: string
}

export type EntryKind = "file" | "folder"

export interface FileTreeHandle {
  /** Starts typing the name of a new file or folder in dir (default: the folder of the cursor's row). */
  startCreate: (kind: EntryKind, dir?: string) => void
}

interface FileTreeProps {
  ref?: Ref<FileTreeHandle>
  workflowId: string
  workflowName: string
  updatedAt: string
  files: WorkflowFile[]
  loading: boolean
  activePath: string | null
  /** Unsaved text per path: shown as modified and searched instead of the saved content. */
  edits: Record<string, string>
  /** New and moved files, not applied yet. */
  pending: Record<string, PendingStatus>
  /** Empty folders made in the tree; they exist only in the tree until a file is put in them. */
  emptyDirs: string[]
  /** Content search (Pro). */
  canSearchContents: boolean
  /** Pro: files and folders can be created, renamed, moved, duplicated and deleted. */
  canEdit: boolean
  /** Why path can't be created (or be the new path of from): a clash, a place the layout doesn't store. */
  validatePath: (path: string, kind: EntryKind, from?: string) => string | undefined
  onOpen: (path: string, opts?: OpenOptions) => void
  onQuickOpen: () => void
  onExport: () => void
  onCreate: (path: string, kind: EntryKind) => void
  onRename: (from: string, to: string) => void
  onDelete: (path: string, kind: EntryKind) => void
  onDuplicate: (path: string) => void
  /** Terraform workflows only: scaffold a local module. */
  onNewModule?: () => void
  /** Docker and Kubernetes workspaces: create a file from a template (Dockerfile per stack, .env, …) in a folder. */
  onNewFromTemplate?: (dir: string) => void
  /** Docker workspace: build contexts take any file type and dotfiles. */
  docker?: boolean
}

type Row = TreeRow | { type: "input"; path: string; name: string; depth: number }

/** The name being typed: a new entry in parent, or a new name for path. */
type Draft = { mode: "create"; kind: EntryKind; parent: string } | { mode: "rename"; kind: EntryKind; path: string }

const rowKey = (r: Row) => `${r.type}:${r.path}`
const rowId = (key: string) => `file-tree-${key.replace(/[^\w-]/g, "_")}`
const join = (dir: string, name: string) => (dir ? `${dir}/${name}` : name)

export function FileTree({
  ref,
  workflowId,
  workflowName,
  updatedAt,
  files,
  loading,
  activePath,
  edits,
  pending,
  emptyDirs,
  canSearchContents,
  canEdit,
  validatePath,
  onOpen,
  onQuickOpen,
  onExport,
  onCreate,
  onRename,
  onDelete,
  onDuplicate,
  onNewModule,
  onNewFromTemplate,
  docker = false,
}: FileTreeProps) {
  const [filter, setFilter] = useState("")
  const [searchContents, setSearchContents] = useState(false)
  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const [cursor, setCursor] = useState<string | null>(null)
  const [draft, setDraftState] = useState<Draft | null>(null)
  // The draft as of the last change, for the input's blur, which runs after Enter/Escape unmounted it.
  const draftRef = useRef<Draft | null>(null)
  const setDraft = (d: Draft | null) => {
    draftRef.current = d
    setDraftState(d)
  }
  const [draftName, setDraftName] = useState("")
  const [draftError, setDraftError] = useState<string>()
  const [menuRow, setMenuRow] = useState<Row | null>(null)
  const [dragging, setDragging] = useState<string | null>(null)
  const [dropDir, setDropDir] = useState<string | null>(null)
  const treeRef = useRef<HTMLDivElement>(null)
  const filterRef = useRef<HTMLInputElement>(null)

  // "Open files" elsewhere in the app (e.g. a module's folder) filters the tree.
  const request = useFilesRequestFor(workflowId)
  const applied = useRef(0)
  useEffect(() => {
    if (!request || request.nonce === applied.current) return
    applied.current = request.nonce
    setFilter(request.filter)
    setSearchContents(false)
  }, [request])

  const q = filter.trim()
  const contentMode = canSearchContents && searchContents && q.length >= 2
  const small = files.length <= SMALL_TREE
  const totalSize = useMemo(() => files.reduce((n, f) => n + f.size, 0), [files])
  // Folders holding a file that can't move (generated, environment roots) can't be renamed or deleted.
  const fixedDirs = useMemo(() => new Set(files.filter((f) => !f.movable).flatMap((f) => ancestors(f.path))), [files])
  const emptySet = useMemo(() => new Set(emptyDirs), [emptyDirs])

  const searchable = useMemo(
    () => (contentMode ? files.filter((f) => !f.binary && f.size <= SEARCH_MAX_BYTES && !pending[f.path]) : []),
    [contentMode, files, pending],
  )
  // Same keys as the editor's content queries, so opened files are not fetched twice.
  const contents = useQueries({
    queries: searchable.map((f) => ({
      queryKey: ["file", workflowId, updatedAt, f.path],
      queryFn: () => api.fileContent(workflowId, f.path),
      staleTime: Infinity,
    })),
    combine: (results) => ({
      pending: results.some((r) => r.isPending),
      text: Object.fromEntries(results.flatMap((r) => (r.data ? [[r.data.path, r.data.content]] : []))) as Record<
        string,
        string
      >,
    }),
  })

  const { rows: treeRows, matchTotal } = useMemo(() => {
    if (contentMode) {
      const counts: Record<string, number> = {}
      let total = 0
      for (const f of files) {
        const text = edits[f.path] ?? contents.text[f.path]
        const n = text === undefined ? 0 : countMatches(text, q)
        if (n > 0) {
          counts[f.path] = n
          total += n
        }
      }
      const matched = files.filter((f) => counts[f.path])
      const rows = flattenTree(buildTree(matched), () => true).map((r) =>
        r.type === "file" ? { ...r, matches: counts[r.path] } : r,
      )
      return { rows, matchTotal: total }
    }
    if (q) return { rows: flattenTree(buildTree(files.filter((f) => matchesFilter(f.path, q))), () => true), matchTotal: 0 }
    return {
      rows: flattenTree(buildTree(files, emptyDirs), (dir, depth) => overrides[dir] ?? (small || depth === 0)),
      matchTotal: 0,
    }
  }, [contentMode, edits, contents.text, q, files, emptyDirs, overrides, small])

  // A new entry's name is typed in a row at the top of its folder.
  const rows = useMemo<Row[]>(() => {
    if (draft?.mode !== "create") return treeRows
    const at = draft.parent ? treeRows.findIndex((r) => r.type === "dir" && r.path === draft.parent) : -1
    const depth = at >= 0 ? treeRows[at].depth + 1 : 0
    return [
      ...treeRows.slice(0, at + 1),
      { type: "input", path: join(draft.parent, "\u0000new"), name: "", depth },
      ...treeRows.slice(at + 1),
    ]
  }, [draft, treeRows])

  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => treeRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 12,
  })

  const cursorIndex = cursor ? rows.findIndex((r) => rowKey(r) === cursor) : -1
  const effectiveCursor = cursorIndex >= 0 ? cursorIndex : rows.findIndex((r) => r.path === activePath)

  // Reveal the active file: open its folders and move the cursor to it.
  useEffect(() => {
    if (!activePath || q) return
    const closed = ancestors(activePath).filter((d, i) => !(overrides[d] ?? (small || i === 0)))
    if (closed.length) setOverrides((o) => ({ ...o, ...Object.fromEntries(closed.map((d) => [d, true])) }))
    setCursor(`file:${activePath}`)
    // Only when the active file changes; folders the user closes afterwards stay closed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath])

  // Scroll to the active file once when it changes (its row may only appear after its folders open), never because
  // the user opened or closed some other folder: that changes the row count but must keep the scroll where it is.
  // A hidden tree (the dock starts collapsed in Guided) can't scroll: it waits until it is laid out.
  const revealed = useRef<string | null>(null)
  const laidOut = (virtualizer.scrollRect?.height ?? 0) > 0
  useEffect(() => {
    if (!activePath || !laidOut || revealed.current === activePath) return
    const i = rows.findIndex((r) => r.type === "file" && r.path === activePath)
    if (i < 0) return
    revealed.current = activePath
    virtualizer.scrollToIndex(i, { align: "auto" })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath, rows.length, laidOut])

  const toggle = (dir: string, open: boolean) => {
    if (!q) setOverrides((o) => ({ ...o, [dir]: open }))
  }

  const moveTo = (i: number) => {
    if (rows.length === 0) return
    const next = Math.min(Math.max(0, i), rows.length - 1)
    setCursor(rowKey(rows[next]))
    virtualizer.scrollToIndex(next, { align: "auto" })
  }

  const activate = (row: Row, opts?: OpenOptions) => {
    if (row.type === "input") return
    setCursor(rowKey(row))
    if (row.type === "dir") toggle(row.path, !row.open)
    else onOpen(row.path, { ...opts, find: contentMode ? q : undefined })
  }

  const movable = (row: Row | null | undefined): row is TreeRow =>
    !!row && (row.type === "file" ? row.file.movable : row.type === "dir" && !fixedDirs.has(row.path))
  /** The folder a new entry goes in for a row: the folder itself, or the file's. */
  const folderOf = (row: Row | null | undefined) => (!row ? "" : row.type === "dir" ? row.path : dirName(row.path))

  const startCreate = (kind: EntryKind, dir?: string) => {
    if (!canEdit) return
    const parent = dir ?? folderOf(rows[effectiveCursor])
    setFilter("")
    setSearchContents(false)
    if (parent) setOverrides((o) => ({ ...o, ...Object.fromEntries([...ancestors(parent), parent].map((d) => [d, true])) }))
    setDraft({ mode: "create", kind, parent })
    setDraftName("")
    setDraftError(undefined)
  }
  useImperativeHandle(ref, () => ({ startCreate }))

  const startRename = (row: Row | null | undefined) => {
    if (!canEdit || !movable(row)) return
    setDraft({ mode: "rename", kind: row.type === "dir" ? "folder" : "file", path: row.path })
    setDraftName(row.name)
    setDraftError(undefined)
  }

  /** Validates and applies the typed name; false (with the problem shown) when it can't. */
  const commitDraft = (): boolean => {
    if (!draft) return true
    const name = draftName.trim()
    if (draft.mode === "rename" && name === baseName(draft.path)) {
      setDraft(null)
      return true
    }
    const path = join(draft.mode === "create" ? draft.parent : dirName(draft.path), name)
    const problem =
      nameProblem(name, draft.kind, docker) ?? validatePath(path, draft.kind, draft.mode === "rename" ? draft.path : undefined)
    if (problem) {
      setDraftError(problem)
      return false
    }
    setDraft(null)
    if (draft.mode === "create") onCreate(path, draft.kind)
    else onRename(draft.path, path)
    setCursor(`${draft.kind === "folder" ? "dir" : "file"}:${path}`)
    requestAnimationFrame(() => treeRef.current?.focus())
    return true
  }

  const cancelDraft = () => {
    setDraft(null)
    requestAnimationFrame(() => treeRef.current?.focus())
  }

  const remove = (row: Row | null | undefined) => {
    if (canEdit && movable(row)) onDelete(row.path, row.type === "dir" ? "folder" : "file")
  }

  const drop = (from: string, dir: string) => {
    const to = join(dir, baseName(from))
    if (to === from || dir === from || dir.startsWith(`${from}/`)) return
    const kind: EntryKind = files.some((f) => f.path === from) ? "file" : "folder"
    const problem = validatePath(to, kind, from)
    if (problem) toast.error(`Can't move ${baseName(from)}`, { description: problem })
    else onRename(from, to)
  }

  const onTreeKeyDown = (e: React.KeyboardEvent) => {
    const i = effectiveCursor
    const row = rows[i]
    switch (e.key) {
      case "ArrowDown":
        moveTo(i + 1)
        break
      case "ArrowUp":
        moveTo(i - 1)
        break
      case "Home":
        moveTo(0)
        break
      case "End":
        moveTo(rows.length - 1)
        break
      case "ArrowRight":
        if (row?.type === "dir") {
          if (!row.open) toggle(row.path, true)
          else moveTo(i + 1)
        }
        break
      case "ArrowLeft":
        if (row?.type === "dir" && row.open && !q) toggle(row.path, false)
        else if (row) {
          const parent = rows.findIndex((r) => r.type === "dir" && r.path === dirName(row.path))
          if (parent >= 0) moveTo(parent)
        }
        break
      case "Enter":
        if (row) activate(row, { focusEditor: true })
        break
      case " ":
        if (row) activate(row)
        break
      case "F2":
        startRename(row)
        break
      case "Delete":
      case "Backspace":
        remove(row)
        break
      default:
        if (e.key.length === 1 && !e.metaKey && !e.ctrlKey && !e.altKey) {
          // Typing in the tree filters it.
          filterRef.current?.focus()
          return
        }
        return
    }
    e.preventDefault()
  }

  const activeDescendant = rows[effectiveCursor] ? rowId(rowKey(rows[effectiveCursor])) : undefined
  const menuDir = folderOf(menuRow)

  const nameInput = (label: string) => (
    <div className="relative min-w-0 flex-1">
      <input
        autoFocus
        value={draftName}
        aria-label={label}
        aria-invalid={!!draftError}
        spellCheck={false}
        autoComplete="off"
        onFocus={(e) => {
          // Select the name without its extension, like other editors.
          const dot = draft?.kind === "file" ? e.currentTarget.value.lastIndexOf(".") : -1
          e.currentTarget.setSelectionRange(0, dot > 0 ? dot : e.currentTarget.value.length)
        }}
        onChange={(e) => {
          setDraftName(e.target.value)
          setDraftError(undefined)
        }}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          e.stopPropagation()
          if (e.key === "Enter") {
            e.preventDefault()
            commitDraft()
          } else if (e.key === "Escape") {
            e.preventDefault()
            cancelDraft()
          }
        }}
        onBlur={() => {
          if (draftRef.current && (!draftName.trim() || !commitDraft())) setDraft(null)
        }}
        className={cn(
          "h-5 w-full rounded-sm border bg-background px-1 font-mono text-xs text-foreground outline-none",
          draftError ? "border-destructive" : "border-primary/60",
        )}
      />
      {draftError && (
        <div
          role="alert"
          className="absolute top-full right-0 left-0 z-20 mt-0.5 rounded-sm bg-destructive px-1.5 py-1 text-[11px] leading-snug text-white shadow-md"
        >
          {draftError}
        </div>
      )}
    </div>
  )

  const editButton = (label: string, icon: React.ReactNode, onClick: () => void, kbd?: string) => (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>
          <Button variant="ghost" size="icon-xs" aria-label={label} disabled={!canEdit} onClick={onClick}>
            {icon}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent>
        {canEdit ? (
          <>
            {label}
            {kbd && <Kbd>{kbd}</Kbd>}
          </>
        ) : (
          "Switch to Pro mode to change files"
        )}
      </TooltipContent>
    </Tooltip>
  )

  return (
    <div className="@container flex h-full min-h-0 flex-col bg-sidebar/40">
      <div className="flex h-10 shrink-0 items-center gap-0.5 border-b pr-1.5 pl-3">
        <div className="min-w-0 flex-1 leading-tight">
          <div className="truncate text-xs font-medium" title={workflowName}>
            {workflowName}
          </div>
          <div className="truncate text-[11px] text-muted-foreground tabular-nums">
            {loading && files.length === 0 ? (
              "Loading files…"
            ) : (
              <>
                {files.length} {files.length === 1 ? "file" : "files"} · {formatBytes(totalSize)}
              </>
            )}
          </div>
        </div>
        {editButton("New file", <FilePlus />, () => startCreate("file"), "⌘N")}
        {editButton("New folder", <FolderPlus />, () => startCreate("folder"))}
        {onNewModule && editButton("New module…", <Package />, onNewModule)}
        {onNewFromTemplate && editButton("New from template…", <FilePlus2 />, () => onNewFromTemplate(""))}
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="icon-xs" aria-label="Go to file" onClick={onQuickOpen}>
              <Search />
            </Button>
          </TooltipTrigger>
          <TooltipContent>
            Go to file <Kbd>⌘P</Kbd>
          </TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button variant="ghost" size="xs" onClick={onExport} aria-label="Export…">
              <Download />
              <span className="hidden @[20rem]:inline">Export…</span>
            </Button>
          </TooltipTrigger>
          <TooltipContent>Export these files to a folder or a .zip</TooltipContent>
        </Tooltip>
      </div>

      <div className="shrink-0 px-2 pt-2 pb-1">
        <InputGroup className="h-7 rounded-md bg-background/60">
          <InputGroupAddon className="pl-2">
            {contentMode && contents.pending ? <Spinner className="size-3.5" /> : <Search className="size-3.5" />}
          </InputGroupAddon>
          <InputGroupInput
            ref={filterRef}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            placeholder={canSearchContents && searchContents ? "Search in files" : "Filter files"}
            aria-label={canSearchContents && searchContents ? "Search in files" : "Filter files"}
            className="h-7 px-1 text-xs md:text-xs"
            onKeyDown={(e) => {
              if (e.key === "Escape" && filter) {
                e.preventDefault()
                setFilter("")
              } else if (e.key === "ArrowDown") {
                e.preventDefault()
                treeRef.current?.focus()
                moveTo(0)
              } else if (e.key === "Enter") {
                const first = rows.find((r) => r.type === "file")
                if (first) activate(first, { focusEditor: true })
              }
            }}
          />
          <InputGroupAddon align="inline-end" className="gap-0.5 pr-1">
            {filter && (
              <InputGroupButton size="icon-xs" aria-label="Clear filter" onClick={() => setFilter("")}>
                <X />
              </InputGroupButton>
            )}
            {canSearchContents && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <InputGroupButton
                    size="icon-xs"
                    aria-label="Search in file contents"
                    aria-pressed={searchContents}
                    className={cn(searchContents && "bg-primary/15 text-primary hover:bg-primary/20 hover:text-primary")}
                    onClick={() => {
                      setSearchContents((v) => !v)
                      filterRef.current?.focus()
                    }}
                  >
                    <FileSearch />
                  </InputGroupButton>
                </TooltipTrigger>
                <TooltipContent>{searchContents ? "Searching file contents" : "Search in file contents"}</TooltipContent>
              </Tooltip>
            )}
          </InputGroupAddon>
        </InputGroup>
        {contentMode && !contents.pending && (
          <p className="px-1 pt-1 text-[11px] text-muted-foreground" aria-live="polite">
            {matchTotal === 0
              ? "No matches"
              : `${matchTotal} ${matchTotal === 1 ? "match" : "matches"} in ${rows.filter((r) => r.type === "file").length} ${
                  rows.filter((r) => r.type === "file").length === 1 ? "file" : "files"
                }`}
          </p>
        )}
      </div>

      {loading && files.length === 0 ? (
        <div className="space-y-2 px-3 py-2" aria-label="Loading files">
          {[70, 55, 80, 45, 65, 50].map((w, i) => (
            <div key={i} className="flex items-center gap-2" style={{ paddingLeft: i > 2 && i < 5 ? 14 : 0 }}>
              <Skeleton className="size-3.5 rounded-sm" />
              <Skeleton className="h-3" style={{ width: `${w}%` }} />
            </div>
          ))}
        </div>
      ) : rows.length === 0 ? (
        <p className="px-3 py-3 text-xs text-muted-foreground">
          {q ? (contentMode && contents.pending ? "Searching…" : `No files match “${q}”.`) : "No files yet."}
        </p>
      ) : (
        <ContextMenu onOpenChange={(open) => !open && setMenuRow(null)}>
          <ContextMenuTrigger asChild>
            <div
              ref={treeRef}
              role="tree"
              aria-label="Files"
              tabIndex={0}
              aria-activedescendant={activeDescendant}
              onKeyDown={onTreeKeyDown}
              onContextMenuCapture={() => setMenuRow(null)}
              onDragOver={(e) => {
                if (!dragging || !e.dataTransfer.types.includes(DRAG_TYPE)) return
                e.preventDefault()
                setDropDir("")
              }}
              onDragLeave={(e) => {
                if (e.currentTarget === e.target) setDropDir(null)
              }}
              onDrop={(e) => {
                const from = e.dataTransfer.getData(DRAG_TYPE)
                setDropDir(null)
                setDragging(null)
                if (from) {
                  e.preventDefault()
                  drop(from, "")
                }
              }}
              className={cn(
                "group/tree min-h-0 flex-1 overflow-auto pb-2 outline-none",
                dropDir === "" && "bg-primary/5 ring-1 ring-primary/40 ring-inset",
              )}
            >
              <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
                {virtualizer.getVirtualItems().map((item) => {
                  const row = rows[item.index]
                  const key = rowKey(row)
                  const style = {
                    height: ROW_HEIGHT,
                    transform: `translateY(${item.start}px)`,
                    paddingLeft: 8 + row.depth * 12,
                  }
                  if (row.type === "input") {
                    return (
                      <div
                        key={key}
                        id={rowId(key)}
                        className="absolute inset-x-0 top-0 z-10 flex items-center gap-1.5 pr-2 text-xs"
                        style={style}
                      >
                        <span className="w-3 shrink-0" />
                        {draft?.kind === "folder" ? (
                          <FolderPlus aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                        ) : (
                          <FilePlus aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
                        )}
                        {nameInput(draft?.kind === "folder" ? "New folder name" : "New file name")}
                      </div>
                    )
                  }
                  const isCursor = item.index === effectiveCursor
                  const isActive = row.type === "file" && row.path === activePath
                  const modified = row.type === "file" && edits[row.path] !== undefined
                  const status = row.type === "file" ? pending[row.path] : emptySet.has(row.path) ? "added" : undefined
                  const renaming = draft?.mode === "rename" && draft.path === row.path
                  const canDrag = canEdit && !draft && movable(row)
                  const isDrop = row.type === "dir" && dropDir === row.path
                  return (
                    <div
                      key={key}
                      id={rowId(key)}
                      role="treeitem"
                      aria-level={row.depth + 1}
                      aria-expanded={row.type === "dir" ? row.open : undefined}
                      aria-selected={isActive}
                      aria-label={
                        row.type === "dir"
                          ? `${row.name}${status ? ", new folder" : ""}`
                          : `${row.name}${row.file.generated ? ", generated" : ""}${
                              status === "added" ? ", new" : status === "moved" ? ", moved" : ""
                            }${modified ? ", modified" : ""}`
                      }
                      title={row.path}
                      draggable={canDrag}
                      onClick={() => activate(row)}
                      onContextMenu={() => {
                        setCursor(key)
                        setMenuRow(row)
                      }}
                      onDragStart={(e) => {
                        e.dataTransfer.setData(DRAG_TYPE, row.path)
                        e.dataTransfer.effectAllowed = "move"
                        setDragging(row.path)
                      }}
                      onDragEnd={() => {
                        setDragging(null)
                        setDropDir(null)
                      }}
                      onDragOver={(e) => {
                        if (!dragging || !e.dataTransfer.types.includes(DRAG_TYPE)) return
                        e.preventDefault()
                        e.stopPropagation()
                        setDropDir(row.type === "dir" ? row.path : dirName(row.path))
                      }}
                      onDrop={(e) => {
                        const from = e.dataTransfer.getData(DRAG_TYPE)
                        e.stopPropagation()
                        setDropDir(null)
                        setDragging(null)
                        if (from) {
                          e.preventDefault()
                          drop(from, row.type === "dir" ? row.path : dirName(row.path))
                        }
                      }}
                      className={cn(
                        "absolute inset-x-0 top-0 flex cursor-default items-center gap-1.5 pr-2 text-xs select-none",
                        "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                        isActive && "bg-primary/12 text-foreground hover:bg-primary/15",
                        isCursor &&
                          "group-focus-visible/tree:ring-1 group-focus-visible/tree:ring-primary/70 group-focus-visible/tree:ring-inset",
                        isDrop && "bg-primary/15 ring-1 ring-primary/50 ring-inset",
                        dragging === row.path && "opacity-50",
                        renaming && "z-10",
                      )}
                      style={style}
                    >
                      {row.type === "dir" ? (
                        <>
                          <ChevronRight
                            aria-hidden
                            className={cn("size-3 shrink-0 transition-transform", row.open && "rotate-90")}
                          />
                          <FolderIcon path={row.path} open={row.open} />
                          {renaming ? (
                            nameInput(`Rename ${row.name}`)
                          ) : (
                            <span className="min-w-0 truncate">{row.name}</span>
                          )}
                          {status && !renaming && <PendingMark status="added" />}
                          {!row.open && !renaming && (
                            <span className="ml-auto text-[10px] text-muted-foreground/70 tabular-nums">{row.count}</span>
                          )}
                        </>
                      ) : (
                        <>
                          <span className="w-3 shrink-0" />
                          <FileIcon file={row.file} />
                          {renaming ? (
                            nameInput(`Rename ${row.name}`)
                          ) : (
                            <span
                              className={cn(
                                "min-w-0 truncate",
                                isActive && "font-medium",
                                status === "added" && "text-emerald-700 dark:text-emerald-400",
                              )}
                            >
                              {row.name}
                            </span>
                          )}
                          {!renaming && (
                            <span className="ml-auto flex shrink-0 items-center gap-1.5">
                              {row.matches !== undefined && (
                                <Badge variant="secondary" className="h-4 min-w-4 rounded-sm px-1 text-[10px] tabular-nums">
                                  {row.matches}
                                </Badge>
                              )}
                              {status && <PendingMark status={status} />}
                              {row.file.generated && (
                                <span className="rounded-sm border border-border/60 px-1 text-[10px] leading-3.5 text-muted-foreground/70">
                                  <span className="@[15rem]:hidden">gen</span>
                                  <span className="hidden @[15rem]:inline">generated</span>
                                </span>
                              )}
                              {modified && <span className="size-1.5 rounded-full bg-amber-500" aria-hidden />}
                            </span>
                          )}
                        </>
                      )}
                    </div>
                  )
                })}
              </div>
            </div>
          </ContextMenuTrigger>
          {/* Focus stays where the action put it (e.g. the name input), not back on the tree. */}
          <ContextMenuContent className="w-56" onCloseAutoFocus={(e) => e.preventDefault()}>
            {canEdit ? (
              <>
                <ContextMenuItem onSelect={() => startCreate("file", menuDir)}>
                  <FilePlus /> New file…
                  <ContextMenuShortcut>⌘N</ContextMenuShortcut>
                </ContextMenuItem>
                <ContextMenuItem onSelect={() => startCreate("folder", menuDir)}>
                  <FolderPlus /> New folder…
                </ContextMenuItem>
                {onNewModule && (
                  <ContextMenuItem onSelect={onNewModule}>
                    <Package /> New module…
                  </ContextMenuItem>
                )}
                {onNewFromTemplate && (
                  <ContextMenuItem onSelect={() => onNewFromTemplate(menuDir)}>
                    <FilePlus2 /> New from template…
                  </ContextMenuItem>
                )}
                {menuRow && menuRow.type !== "input" && (
                  <>
                    <ContextMenuSeparator />
                    <ContextMenuItem disabled={!movable(menuRow)} onSelect={() => startRename(menuRow)}>
                      <Pencil /> Rename…
                      <ContextMenuShortcut>F2</ContextMenuShortcut>
                    </ContextMenuItem>
                    {menuRow.type === "file" && (
                      <ContextMenuItem disabled={menuRow.file.binary} onSelect={() => onDuplicate(menuRow.path)}>
                        <Copy /> Duplicate
                      </ContextMenuItem>
                    )}
                    <ContextMenuItem variant="destructive" disabled={!movable(menuRow)} onSelect={() => remove(menuRow)}>
                      <Trash2 /> Delete
                      <ContextMenuShortcut>⌫</ContextMenuShortcut>
                    </ContextMenuItem>
                  </>
                )}
              </>
            ) : (
              <ContextMenuItem disabled>Switch to Pro mode to change files</ContextMenuItem>
            )}
            {menuRow && menuRow.type !== "input" && (
              <>
                <ContextMenuSeparator />
                <ContextMenuItem
                  onSelect={() => void copyText(menuRow.path).then(() => toast.success(`Copied ${menuRow.path}`))}
                >
                  <Link2 /> Copy path
                </ContextMenuItem>
              </>
            )}
          </ContextMenuContent>
        </ContextMenu>
      )}
    </div>
  )
}

function PendingMark({ status }: { status: PendingStatus }) {
  return (
    <span
      aria-hidden
      title={status === "added" ? "New — not applied yet" : "Moved — not applied yet"}
      className={cn(
        "w-2.5 text-center font-mono text-[10px] font-semibold",
        status === "added" ? "text-emerald-600 dark:text-emerald-400" : "text-sky-600 dark:text-sky-400",
      )}
    >
      {status === "added" ? "A" : "R"}
    </span>
  )
}
