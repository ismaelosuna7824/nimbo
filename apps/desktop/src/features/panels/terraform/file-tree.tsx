import { useEffect, useMemo, useRef, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ChevronRight, Download, FileSearch, Search, X } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupButton, InputGroupInput } from "@/components/ui/input-group"
import { Kbd } from "@/components/ui/kbd"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { WorkflowFile } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { formatBytes } from "../lib/format"
import { FileIcon, FolderIcon } from "./file-icon"
import {
  ancestors,
  buildTree,
  countMatches,
  dirName,
  flattenTree,
  matchesFilter,
  SEARCH_MAX_BYTES,
  type TreeRow,
} from "./file-model"
import { useFilesRequestFor } from "./reveal-files"

const ROW_HEIGHT = 24
/** Trees up to this many files start with every folder open; bigger ones with the top level only. */
const SMALL_TREE = 40

export interface OpenOptions {
  /** Move focus to the editor (Enter) instead of staying in the tree (click, Space). */
  focusEditor?: boolean
  /** Select the first match of this text once the file is shown. */
  find?: string
}

interface FileTreeProps {
  workflowId: string
  workflowName: string
  updatedAt: string
  files: WorkflowFile[]
  loading: boolean
  activePath: string | null
  /** Unsaved text per path: shown as modified and searched instead of the saved content. */
  edits: Record<string, string>
  /** Content search (Pro). */
  canSearchContents: boolean
  onOpen: (path: string, opts?: OpenOptions) => void
  onQuickOpen: () => void
  onExport: () => void
}

const rowKey = (r: TreeRow) => `${r.type}:${r.path}`
const rowId = (key: string) => `file-tree-${key.replace(/[^\w-]/g, "_")}`

export function FileTree({
  workflowId,
  workflowName,
  updatedAt,
  files,
  loading,
  activePath,
  edits,
  canSearchContents,
  onOpen,
  onQuickOpen,
  onExport,
}: FileTreeProps) {
  const [filter, setFilter] = useState("")
  const [searchContents, setSearchContents] = useState(false)
  const [overrides, setOverrides] = useState<Record<string, boolean>>({})
  const [cursor, setCursor] = useState<string | null>(null)
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

  const searchable = useMemo(
    () => (contentMode ? files.filter((f) => !f.binary && f.size <= SEARCH_MAX_BYTES) : []),
    [contentMode, files],
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

  const { rows, matchTotal } = useMemo(() => {
    if (contentMode) {
      const counts: Record<string, number> = {}
      let total = 0
      for (const f of searchable) {
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
      rows: flattenTree(buildTree(files), (dir, depth) => overrides[dir] ?? (small || depth === 0)),
      matchTotal: 0,
    }
  }, [contentMode, searchable, edits, contents.text, q, files, overrides, small])

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

  useEffect(() => {
    const i = rows.findIndex((r) => r.type === "file" && r.path === activePath)
    if (i >= 0) virtualizer.scrollToIndex(i, { align: "auto" })
    // Scroll when the active file changes or its row appears, not on every re-render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activePath, rows.length])

  const toggle = (dir: string, open: boolean) => {
    if (!q) setOverrides((o) => ({ ...o, [dir]: open }))
  }

  const moveTo = (i: number) => {
    if (rows.length === 0) return
    const next = Math.min(Math.max(0, i), rows.length - 1)
    setCursor(rowKey(rows[next]))
    virtualizer.scrollToIndex(next, { align: "auto" })
  }

  const activate = (row: TreeRow, opts?: OpenOptions) => {
    setCursor(rowKey(row))
    if (row.type === "dir") toggle(row.path, !row.open)
    else onOpen(row.path, { ...opts, find: contentMode ? q : undefined })
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

  return (
    <div className="@container flex h-full min-h-0 flex-col bg-sidebar/40">
      <div className="flex h-10 shrink-0 items-center gap-1 border-b pr-1.5 pl-3">
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
              <span className="hidden @[15rem]:inline">Export…</span>
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
        <div
          ref={treeRef}
          role="tree"
          aria-label="Files"
          tabIndex={0}
          aria-activedescendant={activeDescendant}
          onKeyDown={onTreeKeyDown}
          className="group/tree min-h-0 flex-1 overflow-auto pb-2 outline-none"
        >
          <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
            {virtualizer.getVirtualItems().map((item) => {
              const row = rows[item.index]
              const key = rowKey(row)
              const isCursor = item.index === effectiveCursor
              const isActive = row.type === "file" && row.path === activePath
              const modified = row.type === "file" && edits[row.path] !== undefined
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
                      ? row.name
                      : `${row.name}${row.file.generated ? ", generated" : ""}${modified ? ", modified" : ""}`
                  }
                  title={row.path}
                  onClick={() => activate(row)}
                  className={cn(
                    "absolute inset-x-0 top-0 flex cursor-default items-center gap-1.5 pr-2 text-xs select-none",
                    "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    isActive && "bg-primary/12 text-foreground hover:bg-primary/15",
                    isCursor && "group-focus-visible/tree:ring-1 group-focus-visible/tree:ring-primary/70 group-focus-visible/tree:ring-inset",
                  )}
                  style={{ height: ROW_HEIGHT, transform: `translateY(${item.start}px)`, paddingLeft: 8 + row.depth * 12 }}
                >
                  {row.type === "dir" ? (
                    <>
                      <ChevronRight
                        aria-hidden
                        className={cn("size-3 shrink-0 transition-transform", row.open && "rotate-90")}
                      />
                      <FolderIcon path={row.path} open={row.open} />
                      <span className="min-w-0 truncate">{row.name}</span>
                      {!row.open && (
                        <span className="ml-auto text-[10px] text-muted-foreground/70 tabular-nums">{row.count}</span>
                      )}
                    </>
                  ) : (
                    <>
                      <span className="w-3 shrink-0" />
                      <FileIcon file={row.file} />
                      <span className={cn("min-w-0 truncate", isActive && "font-medium")}>{row.name}</span>
                      <span className="ml-auto flex shrink-0 items-center gap-1.5">
                        {row.matches !== undefined && (
                          <Badge variant="secondary" className="h-4 min-w-4 rounded-sm px-1 text-[10px] tabular-nums">
                            {row.matches}
                          </Badge>
                        )}
                        {row.file.generated && (
                          <span className="rounded-sm border border-border/60 px-1 text-[10px] leading-3.5 text-muted-foreground/70">
                            <span className="@[15rem]:hidden">gen</span>
                            <span className="hidden @[15rem]:inline">generated</span>
                          </span>
                        )}
                        {modified && <span className="size-1.5 rounded-full bg-amber-500" aria-hidden />}
                      </span>
                    </>
                  )}
                </div>
              )
            })}
          </div>
        </div>
      )}
    </div>
  )
}
