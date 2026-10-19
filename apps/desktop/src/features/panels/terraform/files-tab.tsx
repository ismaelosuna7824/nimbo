import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query"
import { useDefaultLayout } from "react-resizable-panels"
import { Check, FileArchive, Files, GitCompare, RotateCcw, TriangleAlert, X } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Kbd } from "@/components/ui/kbd"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { ApiError, api } from "@/lib/api/client"
import type { Diagnostic, WorkflowFile, WorkflowFileContent } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { cn } from "@/lib/utils"
import { useUiStore } from "@/features/toolbar/ui-store"
import { setLeaveGuard, useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { copyText, formatBytes } from "../lib/format"
import { ChangesDialog, type PendingChange } from "./changes-dialog"
import { registerCloseFileTab } from "./close-file-tab"
import type { CodeEditorHandle, EditorDiagnostic } from "./code-editor"
import { downloadWorkflowFile } from "./download-file"
import { EditorTabs, PathBar } from "./editor-chrome"
import { baseName } from "./file-model"
import { FileTree, type OpenOptions } from "./file-tree"
import { QuickOpen } from "./quick-open"
import { useFilesRequestFor } from "./reveal-files"

// CodeMirror loads with the first file shown, not with the app.
const CodeEditor = lazy(() => import("./code-editor").then((m) => ({ default: m.CodeEditor })))

/** Largest file the daemon serves as text. */
const MAX_TEXT_BYTES = 2 << 20
const NO_FILES: WorkflowFile[] = []
const WRAP_KEY = "nimbo.files.wrap"
/** Opened first when the workflow is shown. */
const DEFAULT_FILE = "main.tf"

/** Unsaved text of one file and the content it was edited from. */
interface Buffer {
  base: string
  value: string
}

const NO_BUFFERS: Record<string, Buffer> = {}

interface ConfirmRequest {
  title: string
  description: string
  confirmLabel: string
  /** Extra action (e.g. Save) between Cancel and the confirm action. */
  altLabel?: string
  resolve: (choice: "confirm" | "alt" | "cancel") => void
}

interface ApplyError {
  message: string
  diagnostics: Diagnostic[]
}

const fileKey = (workflowId: string, updatedAt: string, path: string) => ["file", workflowId, updatedAt, path]

/** Drops the "main.tf:3: " prefix of a located diagnostic's detail; the file and line are shown separately. */
function withoutLocation(d: Diagnostic): Diagnostic {
  const prefix = `${d.file}:${d.line}: `
  return d.file && d.line && d.detail?.startsWith(prefix) ? { ...d, detail: d.detail.slice(prefix.length) } : d
}

/** The most recent cached content of a file from an earlier workflow version, if any. */
function latestCached(workflowId: string, path: string | null): WorkflowFileContent | undefined {
  let best: { at: string; data: WorkflowFileContent } | undefined
  for (const [key, data] of queryClient.getQueriesData<WorkflowFileContent>({ queryKey: ["file", workflowId] })) {
    const at = String(key[2])
    if (data && key[3] === path && (!best || at > best.at)) best = { at, data }
  }
  return best?.data
}

/** The Code tab: every file an export writes, in a tree with an editor. */
export function FilesTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const workflowName = useWorkflowStore((s) => s.workflow!.name)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const mode = useWorkflowStore((s) => s.mode)
  const visible = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "terraform")
  const pro = mode === "pro"

  const [tabs, setTabs] = useState<string[]>([])
  const [active, setActive] = useState<string | null>(null)
  const [buffers, setBuffers] = useState<Record<string, Buffer>>({})
  const [wrap, setWrap] = useState(() => localStorage.getItem(WRAP_KEY) === "1")
  const [applying, setApplying] = useState(false)
  const [savingPath, setSavingPath] = useState<string | null>(null)
  const [applyError, setApplyError] = useState<ApplyError | null>(null)
  const [quickOpen, setQuickOpen] = useState(false)
  const [review, setReview] = useState<"terraform" | string | null>(null)
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<CodeEditorHandle>(null)
  /** Applied once the editor shows the file (focus, find, reveal line). */
  const pendingReveal = useRef<{ path: string; focus?: boolean; find?: string; line?: number } | null>(null)
  const layout = useDefaultLayout({ id: "nimbo.files", storage: localStorage, onlySaveAfterUserInteractions: true })

  const listing = useQuery({
    queryKey: ["files", workflowId, updatedAt],
    queryFn: () => api.files(workflowId),
    placeholderData: keepPreviousData,
  })
  const files = listing.data?.files ?? NO_FILES
  const byPath = useMemo(() => new Map(files.map((f) => [f.path, f])), [files])
  const activeFile = active ? byPath.get(active) : undefined
  const tooLarge = !!activeFile && !activeFile.binary && activeFile.size > MAX_TEXT_BYTES

  const content = useQuery({
    queryKey: fileKey(workflowId, updatedAt, active ?? ""),
    queryFn: () => api.fileContent(workflowId, active!),
    enabled: !!activeFile && !activeFile.binary && !tooLarge,
    // While a newer version loads, keep showing this file: the previous result or its latest cached version.
    placeholderData: (prev: WorkflowFileContent | undefined) =>
      prev?.path === active ? prev : latestCached(workflowId, active),
    staleTime: Infinity,
    retry: false,
  })

  // Edits are only shown (and kept going) in Pro mode; in Guided mode they wait, read-only.
  const visibleBuffers = pro ? buffers : NO_BUFFERS
  const edits = useMemo(
    () => Object.fromEntries(Object.entries(visibleBuffers).map(([p, b]) => [p, b.value])),
    [visibleBuffers],
  )
  const savedText = content.data?.path === active ? content.data?.content : undefined
  const shownText = (active && visibleBuffers[active]?.value) ?? savedText
  const canEdit = pro && !!activeFile?.editable
  const isTerraform = (path: string) => {
    const f = byPath.get(path)
    return !!f && f.generated && f.kind === "terraform"
  }
  const tfDirty = Object.keys(visibleBuffers).filter(isTerraform)
  const tfDirtyKey = tfDirty.join("\n")

  // The current generated content of edited Terraform files: a difference means the diagram changed meanwhile.
  const latest = useQueries({
    queries: tfDirty.map((p) => ({
      queryKey: fileKey(workflowId, updatedAt, p),
      queryFn: () => api.fileContent(workflowId, p),
      staleTime: Infinity,
    })),
    combine: (results) => results.map((r) => r.data),
  })
  const conflicts = tfDirty.filter((p, i) => latest[i] && latest[i]!.content !== buffers[p]?.base)

  const baseRef = useRef<{ path: string | null; text: string | undefined }>({ path: null, text: undefined })
  baseRef.current = { path: active, text: savedText }

  // Open main.tf (or the first file) once the files are known; drop tabs of files that are gone.
  const initialized = useRef(false)
  useEffect(() => {
    if (listing.isPlaceholderData || !listing.data || files.length === 0) return
    if (!initialized.current) {
      initialized.current = true
      const first = byPath.has(DEFAULT_FILE) ? DEFAULT_FILE : files.find((f) => !f.binary)?.path
      if (first) {
        setTabs([first])
        setActive(first)
      }
      return
    }
    setTabs((t) => (t.every((p) => byPath.has(p)) ? t : t.filter((p) => byPath.has(p))))
    setActive((a) => (a && !byPath.has(a) ? null : a))
    setBuffers((b) =>
      Object.keys(b).every((p) => byPath.has(p)) ? b : Object.fromEntries(Object.entries(b).filter(([p]) => byPath.has(p))),
    )
  }, [listing.data, listing.isPlaceholderData, byPath, files])

  useEffect(() => {
    if (!active && tabs.length) setActive(tabs[tabs.length - 1])
  }, [active, tabs])

  const ask = useCallback(
    (req: Omit<ConfirmRequest, "resolve">) =>
      new Promise<"confirm" | "alt" | "cancel">((resolve) => setConfirm({ ...req, resolve })),
    [],
  )

  const openFile = useCallback((path: string, opts: OpenOptions & { line?: number } = {}) => {
    setTabs((t) => (t.includes(path) ? t : [...t, path]))
    setActive(path)
    if (opts.focusEditor || opts.find || opts.line) {
      pendingReveal.current = { path, focus: opts.focusEditor, find: opts.find, line: opts.line }
    }
  }, [])

  // "Open files" requests (e.g. a module's folder): open the file once the current listing has it.
  const filesRequest = useFilesRequestFor(workflowId)
  const openedRequest = useRef(0)
  useEffect(() => {
    if (!filesRequest || filesRequest.nonce === openedRequest.current || listing.isPlaceholderData || !listing.data) return
    openedRequest.current = filesRequest.nonce
    const target =
      filesRequest.open && byPath.has(filesRequest.open)
        ? filesRequest.open
        : files.find((f) => !f.binary && f.path.startsWith(filesRequest.filter))?.path
    if (target) openFile(target)
  }, [filesRequest, listing.data, listing.isPlaceholderData, byPath, files, openFile])

  // Run focus/find/reveal once the editor shows the requested file.
  useEffect(() => {
    const p = pendingReveal.current
    const ed = editorRef.current
    if (!p || p.path !== active || shownText === undefined || !ed) return
    pendingReveal.current = null
    // The editor applies the document in a layout effect; act after it painted.
    requestAnimationFrame(() => {
      if (p.line) ed.revealLine(p.line)
      else if (p.find) ed.find(p.find)
      else if (p.focus) ed.focus()
    })
  })

  const dropBuffers = useCallback((paths: string[]) => {
    setBuffers((b) => {
      const next = { ...b }
      for (const p of paths) delete next[p]
      return next
    })
  }, [])

  const onChange = useCallback((path: string, text: string) => {
    setBuffers((prev) => {
      const base = prev[path]?.base ?? (baseRef.current.path === path ? baseRef.current.text : undefined)
      if (base === undefined) return prev
      if (text === base) {
        if (!(path in prev)) return prev
        const next = { ...prev }
        delete next[path]
        return next
      }
      return { ...prev, [path]: { base, value: text } }
    })
  }, [])

  const save = useCallback(
    async (path: string) => {
      const b = buffers[path]
      if (!b || savingPath) return
      setSavingPath(path)
      try {
        await useWorkflowStore.getState().flush()
        const res = await api.putFileContent(workflowId, path, b.value)
        // Seed the new version so the editor never flashes the old text.
        queryClient.setQueryData(fileKey(workflowId, res.workflow.updatedAt, path), { ...res.file, content: b.value })
        useWorkflowStore.getState().setWorkflow(res.workflow)
        setBuffers((prev) => {
          if (prev[path]?.value !== b.value) return prev[path] ? { ...prev, [path]: { ...prev[path], base: b.value } } : prev
          const next = { ...prev }
          delete next[path]
          return next
        })
        toast.success(`Saved ${baseName(path)}`)
      } catch (err) {
        toast.error(`Couldn't save ${baseName(path)}`, { description: errorMessage(err) })
      } finally {
        setSavingPath(null)
      }
    },
    [buffers, savingPath, workflowId],
  )

  const apply = useCallback(async () => {
    if (applying || tfDirty.length === 0) return
    setApplying(true)
    setApplyError(null)
    setReview(null)
    try {
      await useWorkflowStore.getState().flush()
      // Unedited files are taken as they are now, so diagram changes to them are kept.
      const { files: current } = await api.terraform(workflowId)
      const code = { ...current }
      for (const p of tfDirty) code[p] = buffers[p].value
      const wf = await api.putTerraform(workflowId, code)
      // Load the re-generated open files first so the editor switches straight to them.
      await Promise.allSettled(
        tabs.filter((p) => byPath.get(p)?.generated).map((p) =>
          queryClient.fetchQuery({
            queryKey: fileKey(workflowId, wf.updatedAt, p),
            queryFn: () => api.fileContent(workflowId, p),
            staleTime: Infinity,
          }),
        ),
      )
      useWorkflowStore.getState().setWorkflow(wf)
      dropBuffers(tfDirty)
      const errors = wf.diagnostics.filter((d) => d.severity === "error").length
      toast.success("Code applied to the diagram", {
        description: errors ? `${errors} problem${errors === 1 ? "" : "s"} found — see the Problems tab.` : undefined,
      })
    } catch (err) {
      setApplyError(
        err instanceof ApiError
          ? { message: err.message, diagnostics: err.diagnostics.map(withoutLocation) }
          : { message: errorMessage(err), diagnostics: [] },
      )
    } finally {
      setApplying(false)
    }
    // tfDirtyKey stands for tfDirty.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [applying, tfDirtyKey, buffers, workflowId, tabs, byPath, dropBuffers])

  const closeTab = useCallback(
    async (path: string) => {
      if (pro && buffers[path]) {
        const asset = !isTerraform(path)
        const choice = await ask({
          title: `Discard changes to ${baseName(path)}?`,
          description: asset
            ? "Your edits to this file are not saved yet."
            : "Your edits to this file have not been applied to the diagram yet.",
          confirmLabel: "Discard",
          altLabel: asset && pro ? "Save" : undefined,
        })
        if (choice === "cancel") return
        if (choice === "alt") await save(path)
        else dropBuffers([path])
      }
      const i = tabs.indexOf(path)
      const next = tabs.filter((p) => p !== path)
      setTabs(next)
      if (active === path) setActive(next[Math.min(i, next.length - 1)] ?? null)
    },
    // isTerraform reads byPath.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [buffers, ask, pro, save, dropBuffers, byPath, tabs, active],
  )

  // Unsaved edits block switching or closing the workflow until the user discards them.
  const dirtyRef = useRef<string[]>([])
  dirtyRef.current = Object.keys(buffers)
  useEffect(
    () =>
      setLeaveGuard(async () => {
        const dirty = dirtyRef.current
        if (dirty.length === 0) return true
        const choice = await ask({
          title: "Discard unsaved file changes?",
          description: `${dirty.map(baseName).join(", ")} ${dirty.length === 1 ? "has" : "have"} changes that are not saved or applied yet.`,
          confirmLabel: "Discard changes",
        })
        return choice === "confirm"
      }),
    [ask],
  )

  // ⌘W closes the active tab while focus is in the viewer.
  const closeActiveRef = useRef<() => boolean>(() => false)
  closeActiveRef.current = () => {
    if (!active || !rootRef.current?.contains(document.activeElement)) return false
    void closeTab(active)
    return true
  }
  useEffect(() => registerCloseFileTab(() => closeActiveRef.current()), [])

  // ⌘P opens the file picker while the tab is visible.
  useEffect(() => {
    if (!visible) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key.toLowerCase() !== "p" || !(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey) return
      if (useUiStore.getState().commandOpen) return
      e.preventDefault()
      setQuickOpen((o) => !o)
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [visible])

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!(e.metaKey || e.ctrlKey) || e.shiftKey || e.altKey || e.key.toLowerCase() !== "s") return
    e.preventDefault()
    if (!pro) return
    if (active && buffers[active] && !isTerraform(active)) void save(active)
    else if (tfDirty.length) void apply()
  }

  const editorDiagnostics = useMemo<EditorDiagnostic[]>(
    () =>
      (applyError?.diagnostics ?? [])
        .filter((d) => d.file === active && d.line)
        .map((d) => ({ line: d.line!, severity: d.severity, message: d.detail ? `${d.message}: ${d.detail}` : d.message })),
    [applyError, active],
  )

  const pendingChanges = (paths: string[]): PendingChange[] =>
    paths.flatMap((p) => (buffers[p] ? [{ path: p, before: buffers[p].base, after: buffers[p].value }] : []))

  const download = async (path: string) => {
    try {
      if (await downloadWorkflowFile(workflowId, path)) toast.success(`Downloaded ${baseName(path)}`)
    } catch (err) {
      toast.error(`Couldn't download ${baseName(path)}`, { description: errorMessage(err) })
    }
  }

  const openExport = () => useWorkflowStore.getState().openDialog("export")

  if (listing.isError && !listing.data) {
    return (
      <div className="p-4 text-xs text-destructive">
        Could not list the workflow files: {errorMessage(listing.error)}
      </div>
    )
  }

  if (listing.data && files.length === 0) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Files />
          </EmptyMedia>
          <EmptyTitle>No files yet</EmptyTitle>
          <EmptyDescription>Add resources to the canvas and their Terraform code appears here.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const dirtyActive = !!active && !!visibleBuffers[active]
  const activeIsTf = !!active && isTerraform(active)
  const contentError = content.error instanceof ApiError ? content.error : null

  return (
    <div ref={rootRef} className="h-full min-h-0" onKeyDown={onKeyDown}>
      <ResizablePanelGroup
        orientation="horizontal"
        defaultLayout={layout.defaultLayout}
        onLayoutChanged={layout.onLayoutChanged}
      >
        <ResizablePanel id="files-tree" defaultSize="240px" minSize="160px" maxSize="50%">
          <FileTree
            workflowId={workflowId}
            workflowName={workflowName}
            updatedAt={updatedAt}
            files={files}
            loading={listing.isPending}
            activePath={active}
            edits={edits}
            canSearchContents={pro}
            onOpen={openFile}
            onQuickOpen={() => setQuickOpen(true)}
            onExport={openExport}
          />
        </ResizablePanel>
        <ResizableHandle />
        <ResizablePanel id="files-editor" minSize="30%">
          <div className="@container flex h-full min-h-0 flex-col">
            {tabs.length > 0 && (
              <EditorTabs
                tabs={tabs}
                active={active}
                files={byPath}
                modified={(p) => !!visibleBuffers[p]}
                onSelect={(p) => setActive(p)}
                onClose={(p) => void closeTab(p)}
              />
            )}
            {activeFile && (
              <PathBar
                file={activeFile}
                pro={pro}
                readOnly={!canEdit}
                dirty={dirtyActive}
                saving={savingPath === active}
                wrap={wrap}
                onSave={dirtyActive && !activeIsTf ? () => void save(active!) : undefined}
                onRevert={() => dropBuffers([active!])}
                onShowChanges={() => setReview(active)}
                onFind={shownText !== undefined ? () => editorRef.current?.openSearch() : undefined}
                onToggleWrap={() =>
                  setWrap((w) => {
                    localStorage.setItem(WRAP_KEY, w ? "0" : "1")
                    return !w
                  })
                }
                onCopy={
                  shownText !== undefined
                    ? () =>
                        void copyText(shownText).then(() => toast.success(`Copied ${baseName(activeFile.path)}`))
                    : undefined
                }
                onDownload={() => void download(activeFile.path)}
                onCopyPath={() => void copyText(activeFile.path).then(() => toast.success(`Copied ${activeFile.path}`))}
                onExport={openExport}
                onSwitchToPro={() => useWorkflowStore.getState().setMode("pro")}
              />
            )}
            {pro && conflicts.length > 0 && (
              <div className="flex shrink-0 items-center gap-2 border-b bg-amber-500/10 px-3 py-1 text-xs text-amber-700 dark:text-amber-300">
                <TriangleAlert className="size-3.5 shrink-0" />
                <span className="min-w-0 flex-1 truncate">
                  The diagram changed after you started editing {conflicts.map(baseName).join(", ")}. Applying replaces
                  it with your code.
                </span>
                <Button size="xs" variant="ghost" onClick={() => dropBuffers(conflicts)}>
                  <RotateCcw /> Reload code
                </Button>
              </div>
            )}

            <div className="relative min-h-0 flex-1">
              {!activeFile ? (
                listing.isPending ? (
                  <EditorSkeleton />
                ) : (
                  <Empty className="h-full">
                    <EmptyHeader>
                      <EmptyMedia variant="icon">
                        <Files />
                      </EmptyMedia>
                      <EmptyTitle>No file open</EmptyTitle>
                      <EmptyDescription>
                        Pick a file in the tree or press <Kbd>⌘P</Kbd> to find one.
                      </EmptyDescription>
                    </EmptyHeader>
                  </Empty>
                )
              ) : activeFile.binary || tooLarge || contentError?.code === "binary" || contentError?.code === "too_large" ? (
                <Empty className="h-full">
                  <EmptyHeader>
                    <EmptyMedia variant="icon">
                      <FileArchive />
                    </EmptyMedia>
                    <EmptyTitle className="font-mono text-sm">{baseName(activeFile.path)}</EmptyTitle>
                    <EmptyDescription>
                      {activeFile.binary ? "Binary file" : "Too large to show"} · {formatBytes(activeFile.size)}. It is
                      exported as it is.
                    </EmptyDescription>
                  </EmptyHeader>
                  <Button size="sm" variant="outline" onClick={() => void download(activeFile.path)}>
                    Download
                  </Button>
                </Empty>
              ) : content.isError && shownText === undefined ? (
                <div className="p-4 text-xs text-destructive">
                  Could not read {activeFile.path}: {errorMessage(content.error)}
                </div>
              ) : shownText === undefined ? (
                <EditorSkeleton />
              ) : (
                <Suspense fallback={<EditorSkeleton />}>
                  <CodeEditor
                    ref={editorRef}
                    docId={activeFile.path}
                    value={shownText}
                    language={activeFile.language}
                    // An older version shown while the current one loads is not a base for edits.
                    readOnly={!canEdit || (content.isPlaceholderData && !visibleBuffers[activeFile.path])}
                    wrap={wrap}
                    diagnostics={editorDiagnostics}
                    onChange={onChange}
                    keep={tabs}
                  />
                </Suspense>
              )}
              {content.isFetching && shownText !== undefined && (
                <Spinner className="absolute top-1.5 right-3 size-3.5 text-muted-foreground" />
              )}
            </div>

            {pro && applyError && (
              <div role="alert" className="max-h-36 shrink-0 overflow-auto border-t bg-destructive/5 text-xs">
                <div className="sticky top-0 flex items-center gap-2 bg-destructive/10 px-3 py-1 font-medium text-destructive backdrop-blur">
                  <TriangleAlert className="size-3.5 shrink-0" />
                  <span className="min-w-0 flex-1 truncate" title={applyError.message}>
                    Not applied: {applyError.message}
                  </span>
                  <Button
                    size="icon-xs"
                    variant="ghost"
                    aria-label="Dismiss"
                    className="text-destructive"
                    onClick={() => setApplyError(null)}
                  >
                    <X />
                  </Button>
                </div>
                {applyError.diagnostics.map((d, i) => (
                  <DiagnosticRow
                    key={i}
                    diagnostic={d}
                    nodeLabel={d.file ? `${d.file}${d.line ? `:${d.line}` : ""}` : undefined}
                    onSelect={d.file && byPath.has(d.file) ? () => openFile(d.file!, { line: d.line }) : undefined}
                  />
                ))}
              </div>
            )}
            {pro && tfDirty.length > 0 && (
              <div className="flex h-9 shrink-0 items-center gap-2 border-t bg-primary/6 px-3 text-xs">
                <span className="size-2 shrink-0 rounded-full bg-amber-500" aria-hidden />
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-medium">
                    {tfDirty.length === 1 ? baseName(tfDirty[0]) : `${tfDirty.length} Terraform files`} changed
                  </span>
                  <span className="hidden text-muted-foreground @[36rem]:inline">
                    {" "}
                    — applying re-imports the code into the diagram
                  </span>
                </span>
                <Button size="xs" variant="ghost" onClick={() => setReview("terraform")}>
                  <GitCompare /> Review
                </Button>
                <Button
                  size="xs"
                  variant="ghost"
                  onClick={() => {
                    dropBuffers(tfDirty)
                    setApplyError(null)
                  }}
                  disabled={applying}
                >
                  <X /> Discard
                </Button>
                <Button size="xs" onClick={() => void apply()} disabled={applying}>
                  {applying ? <Spinner /> : <Check />} Apply changes
                  <Kbd className="ml-0.5 hidden h-4 bg-primary-foreground/20 text-[10px] text-primary-foreground @[30rem]:inline-flex">
                    ⌘S
                  </Kbd>
                </Button>
              </div>
            )}
          </div>
        </ResizablePanel>
      </ResizablePanelGroup>

      <QuickOpen
        open={quickOpen}
        onOpenChange={setQuickOpen}
        files={files}
        recent={active ? [active, ...tabs.filter((t) => t !== active).reverse()] : tabs}
        onPick={(p) => openFile(p, { focusEditor: true })}
      />

      <ChangesDialog
        open={review !== null}
        onOpenChange={(o) => !o && setReview(null)}
        title={review === "terraform" ? "Review Terraform changes" : `Changes to ${review ? baseName(review) : ""}`}
        description={
          review === "terraform"
            ? "Applying re-imports this code into the diagram."
            : "Saving writes this file into the workflow's files."
        }
        changes={pendingChanges(review === "terraform" ? tfDirty : review ? [review] : [])}
        action={
          review === "terraform"
            ? { label: "Apply changes", run: () => void apply(), disabled: applying }
            : review
              ? {
                  label: "Save",
                  run: () => {
                    setReview(null)
                    void save(review)
                  },
                }
              : undefined
        }
      />

      <AlertDialog
        open={confirm !== null}
        onOpenChange={(o) => {
          if (!o && confirm) {
            confirm.resolve("cancel")
            setConfirm(null)
          }
        }}
      >
        <AlertDialogContent size={confirm?.altLabel ? "default" : "sm"}>
          <AlertDialogHeader>
            <AlertDialogTitle>{confirm?.title}</AlertDialogTitle>
            <AlertDialogDescription>{confirm?.description}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            {confirm?.altLabel && (
              <Button
                variant="outline"
                onClick={() => {
                  confirm.resolve("alt")
                  setConfirm(null)
                }}
              >
                {confirm.altLabel}
              </Button>
            )}
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                confirm?.resolve("confirm")
                setConfirm(null)
              }}
            >
              {confirm?.confirmLabel}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function EditorSkeleton() {
  return (
    <div className="flex h-full gap-3 overflow-hidden px-3 py-2" aria-label="Loading file">
      <div className="space-y-2 pt-0.5">
        {Array.from({ length: 8 }, (_, i) => (
          <Skeleton key={i} className="h-2.5 w-4" />
        ))}
      </div>
      <div className="flex-1 space-y-2 pt-0.5">
        {[45, 70, 30, 60, 0, 50, 75, 40].map((w, i) => (
          <Skeleton key={i} className={cn("h-2.5", w === 0 && "invisible")} style={{ width: `${w || 10}%` }} />
        ))}
      </div>
    </div>
  )
}
