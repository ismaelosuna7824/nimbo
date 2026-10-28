import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react"
import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query"
import { useDefaultLayout } from "react-resizable-panels"
import { Check, FileArchive, Files, GitCompare, RotateCcw, TriangleAlert, X } from "lucide-react"
import { toast } from "sonner"
import { useShallow } from "zustand/react/shallow"
import { errorMessage } from "@/components/errors"
import { lazyModule, useLazyModule } from "@/components/lazy-module"
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
import { Checkbox } from "@/components/ui/checkbox"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Kbd } from "@/components/ui/kbd"
import { ResizableHandle, ResizablePanel, ResizablePanelGroup } from "@/components/ui/resizable"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ApiError, api } from "@/lib/api/client"
import type { Diagnostic, ModuleScaffold, WorkflowFile, WorkflowFileContent } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { cn } from "@/lib/utils"
import { useUiStore } from "@/features/toolbar/ui-store"
import { setLeaveGuard, useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { copyText, formatBytes } from "../lib/format"
import { useShownVersion } from "../lib/use-shown-version"
import { ChangesDialog, type PendingChange } from "./changes-dialog"
import { registerCloseFileTab } from "./close-file-tab"
import type { CodeEditorHandle, EditorDiagnostic } from "./code-editor"
import { downloadWorkflowFile } from "./download-file"
import { EditorTabs, PathBar } from "./editor-chrome"
import { ancestors, baseName, dirName, isFormattable, isYaml } from "./file-model"
import { FileTree, type EntryKind, type FileTreeHandle, type OpenOptions } from "./file-tree"
import { formatYaml, YamlFormatError } from "@/features/code-intel/yaml/format"
import { insertService } from "@/features/docker/compose-snippet"
import { NewFromTemplateDialog } from "./new-from-template-dialog"
import { NewModuleDialog } from "./new-module-dialog"
import {
  changesRequest,
  createFile,
  createFolder,
  deletePath,
  NO_PENDING,
  pendingPaths,
  renamePath,
  serverPath,
  viewFiles,
  type Buffer,
  type PendingFiles,
} from "./pending-files"
import { QuickOpen } from "./quick-open"
import { useFilesRequestFor } from "./reveal-files"

// CodeMirror loads with the first file shown, not with the app.
const CodeEditor = lazy(() => import("./code-editor").then((m) => ({ default: m.CodeEditor })))
// Each kind's language intelligence (schemas, completion, checks) loads with the first file shown in its workspace.
const terraformIntelCode = lazyModule(() => import("./intel"))
const dockerIntelCode = lazyModule(() => import("@/features/docker/intel"))
const kubernetesIntelCode = lazyModule(() => import("@/features/kubernetes/intel"))

/** Largest file the daemon serves as text. */
const MAX_TEXT_BYTES = 2 << 20
const NO_FILES: WorkflowFile[] = []
const WRAP_KEY = "nimbo.files.wrap"
const FORMAT_KEY = "nimbo.files.formatOnApply"
/** Opened first when the workflow is shown. */
const DEFAULT_FILE = "main.tf"
/** Generated code of an environment root (environments layout). */
const ENV_ROOT_CODE = /^environments\/[^/]+\/[^/]+\.tf$/

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
function latestCached(workflowId: string, path: string | undefined): WorkflowFileContent | undefined {
  let best: { at: string; data: WorkflowFileContent } | undefined
  for (const [key, data] of queryClient.getQueriesData<WorkflowFileContent>({ queryKey: ["file", workflowId] })) {
    const at = String(key[2])
    if (data && key[3] === path && (!best || at > best.at)) best = { at, data }
  }
  return best?.data
}

/** "a.tf" → "a copy.tf", "a copy 2.tf", … (the first name free). */
function copyName(path: string, free: (path: string) => boolean): string {
  const dir = dirName(path)
  const base = baseName(path)
  const dot = base.lastIndexOf(".")
  const stem = dot > 0 ? base.slice(0, dot) : base
  const ext = dot > 0 ? base.slice(dot) : ""
  for (let n = 1; ; n++) {
    const name = `${stem} copy${n > 1 ? ` ${n}` : ""}${ext}`
    const candidate = dir ? `${dir}/${name}` : name
    if (free(candidate)) return candidate
  }
}

/** The Code tab: every file an export writes, in a tree with an editor; Pro mode creates, edits and moves them. */
export function FilesTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const workflowName = useWorkflowStore((s) => s.workflow!.name)
  // The workflow version the files are read at: frozen while the tab is hidden, so saves don't refetch them meanwhile.
  const updatedAt = useShownVersion("terraform")
  const docker = useWorkflowStore((s) => s.workflow!.kind === "docker")
  const kubernetes = useWorkflowStore((s) => s.workflow!.kind === "kubernetes")
  // Compared item by item: a save replaces the compose object (e.g. a moved card) with the same file list.
  const composeFiles = useWorkflowStore(useShallow((s) => s.workflow!.compose?.files))
  const mode = useWorkflowStore((s) => s.mode)
  const visible = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "terraform")
  const pro = mode === "pro"

  const [tabs, setTabs] = useState<string[]>([])
  const [active, setActive] = useState<string | null>(null)
  const [buffers, setBuffers] = useState<Record<string, Buffer>>({})
  const [pending, setPending] = useState<PendingFiles>(NO_PENDING)
  const [wrap, setWrap] = useState(() => localStorage.getItem(WRAP_KEY) === "1")
  const [formatOnApply, setFormatOnApply] = useState(() => localStorage.getItem(FORMAT_KEY) === "1")
  const [applying, setApplying] = useState(false)
  const [savingPath, setSavingPath] = useState<string | null>(null)
  const [applyError, setApplyError] = useState<ApplyError | null>(null)
  const [quickOpen, setQuickOpen] = useState(false)
  const [moduleOpen, setModuleOpen] = useState(false)
  /** "New from template…": the folder the file goes in; null when closed. */
  const [templateDir, setTemplateDir] = useState<string | null>(null)
  const [review, setReview] = useState<"all" | string | null>(null)
  const [confirm, setConfirm] = useState<ConfirmRequest | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const editorRef = useRef<CodeEditorHandle>(null)
  const treeRef = useRef<FileTreeHandle>(null)
  /** Applied once the editor shows the file (focus, find, reveal line). */
  const pendingReveal = useRef<{ path: string; focus?: boolean; find?: string; line?: number } | null>(null)
  const layout = useDefaultLayout({ id: "nimbo.files", storage: localStorage, onlySaveAfterUserInteractions: true })

  const listing = useQuery({
    queryKey: ["files", workflowId, updatedAt],
    queryFn: () => api.files(workflowId),
    placeholderData: keepPreviousData,
  })
  const serverFiles = listing.data?.files ?? NO_FILES

  // Edits and file operations are only shown (and kept going) in Pro mode; in Guided mode they wait, read-only.
  const visibleBuffers = pro ? buffers : NO_BUFFERS
  const visiblePending = pro ? pending : NO_PENDING
  const { files, status } = useMemo(
    () => viewFiles(serverFiles, visiblePending, visibleBuffers),
    [serverFiles, visiblePending, visibleBuffers],
  )
  const byPath = useMemo(() => new Map(files.map((f) => [f.path, f])), [files])
  const serverByPath = useMemo(() => new Map(serverFiles.map((f) => [f.path, f])), [serverFiles])
  // Empty folders stay in the tree until a file is put in them.
  const emptyDirs = useMemo(() => {
    const dirs = new Set(files.flatMap((f) => ancestors(f.path)))
    return visiblePending.folders.filter((d) => !dirs.has(d))
  }, [files, visiblePending.folders])
  const activeFile = active ? byPath.get(active) : undefined
  const activeOrigin = active ? serverPath(visiblePending, active) : undefined
  const tooLarge = !!activeFile && !activeFile.binary && activeFile.size > MAX_TEXT_BYTES

  const content = useQuery({
    queryKey: fileKey(workflowId, updatedAt, activeOrigin ?? ""),
    queryFn: () => api.fileContent(workflowId, activeOrigin!),
    enabled: !!activeFile && !activeFile.binary && !tooLarge && activeOrigin !== undefined,
    // While a newer version loads, keep showing this file: the previous result or its latest cached version.
    placeholderData: (prev: WorkflowFileContent | undefined) =>
      prev?.path === activeOrigin ? prev : latestCached(workflowId, activeOrigin),
    staleTime: Infinity,
    retry: false,
  })

  const edits = useMemo(
    () => Object.fromEntries(Object.entries(visibleBuffers).map(([p, b]) => [p, b.value])),
    [visibleBuffers],
  )
  const savedText =
    active && visiblePending.created[active]
      ? ""
      : content.data && content.data.path === activeOrigin
        ? content.data.content
        : undefined
  const shownText = (active && visibleBuffers[active]?.value) ?? savedText
  const canEdit = pro && !!activeFile?.editable
  const isCode = (path: string) => {
    const f = byPath.get(path)
    return !!f && ((f.generated && f.kind === "terraform") || f.kind === "compose" || f.kind === "dockerfile")
  }
  const changed = pendingPaths(visiblePending, visibleBuffers).sort()
  const changedKey = changed.join("\n")
  // Edited generated code still at its place: compared with its current content to notice diagram changes.
  const codeEdits = Object.keys(visibleBuffers).filter((p) => isCode(p) && !status[p])

  // The current generated content of edited Terraform files: a difference means the diagram changed meanwhile.
  const latest = useQueries({
    queries: codeEdits.map((p) => ({
      queryKey: fileKey(workflowId, updatedAt, p),
      queryFn: () => api.fileContent(workflowId, p),
      staleTime: Infinity,
    })),
    combine: (results) => results.map((r) => r.data),
  })
  const conflicts = codeEdits.filter((p, i) => latest[i] && latest[i]!.content !== buffers[p]?.base)

  // The environments layout stores files in the service module or local modules only.
  const envLayout = useMemo(() => serverFiles.some((f) => f.generated && ENV_ROOT_CODE.test(f.path)), [serverFiles])

  const baseRef = useRef<{ path: string | null; text: string | undefined }>({ path: null, text: undefined })
  baseRef.current = { path: active, text: savedText }
  const pendingRef = useRef(pending)
  pendingRef.current = pending
  const buffersRef = useRef(buffers)
  buffersRef.current = buffers
  const filesRef = useRef(files)
  filesRef.current = files

  // Open main.tf (in the environments layout the service module's: the only generated code under modules/; in a Docker
  // workspace the first compose file) or the first file once the files are known; drop tabs of files that are gone.
  const initialized = useRef(false)
  useEffect(() => {
    if (listing.isPlaceholderData || !listing.data || files.length === 0) return
    if (!initialized.current) {
      initialized.current = true
      const preferred = docker ? composeFiles?.[0] : DEFAULT_FILE
      const first =
        preferred && byPath.has(preferred)
          ? preferred
          : (files.find((f) => f.generated && f.kind === "terraform" && /^modules\/[^/]+\/main\.tf$/.test(f.path)) ??
              // Split layouts (api.tf, compute.tf, …) have no main.tf: their first Terraform file, never scaffold
              // like .gitignore that sorts first.
              files.find((f) => f.kind === "terraform") ??
              files.find((f) => f.kind === "compose") ??
              files.find((f) => !f.binary))?.path
      if (first) {
        setTabs([first])
        setActive(first)
      }
      return
    }
    setTabs((t) => (t.every((p) => byPath.has(p)) ? t : t.filter((p) => byPath.has(p))))
    setActive((a) => (a && !byPath.has(a) ? null : a))
    if (pro) {
      setBuffers((b) =>
        Object.keys(b).every((p) => byPath.has(p)) ? b : Object.fromEntries(Object.entries(b).filter(([p]) => byPath.has(p))),
      )
    }
  }, [listing.data, listing.isPlaceholderData, byPath, files, pro, docker, composeFiles])

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
    if (target) openFile(target, target === filesRequest.open && filesRequest.line ? { line: filesRequest.line } : {})
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
      const created = !!pendingRef.current.created[path]
      const base =
        prev[path]?.base ?? (created ? "" : baseRef.current.path === path ? baseRef.current.text : undefined)
      if (base === undefined) return prev
      if (text === base && !created) {
        if (!(path in prev)) return prev
        const next = { ...prev }
        delete next[path]
        return next
      }
      return { ...prev, [path]: { base, value: text } }
    })
  }, [])

  /** Text of a shown file: its unsaved edits, else its content on the server ("" for a new file). */
  const textOf = useCallback(
    async (path: string): Promise<string> => {
      const b = buffersRef.current[path]
      if (b) return b.value
      const origin = serverPath(pendingRef.current, path)
      if (origin === undefined) return ""
      const res = await queryClient.fetchQuery({
        queryKey: fileKey(workflowId, updatedAt, origin),
        queryFn: () => api.fileContent(workflowId, origin),
        staleTime: Infinity,
      })
      return res.content
    },
    [workflowId, updatedAt],
  )

  /** Renamed tabs, buffers and the active file follow their file. */
  const followRenames = useCallback((renames: [string, string][]) => {
    if (renames.length === 0) return
    const to = new Map(renames)
    setTabs((t) => t.map((p) => to.get(p) ?? p))
    setActive((a) => (a ? (to.get(a) ?? a) : a))
    setBuffers((b) => Object.fromEntries(Object.entries(b).map(([p, v]) => [to.get(p) ?? p, v])))
  }, [])

  /** Why path can't be created (or be where from moves to). */
  const validatePath = useCallback(
    (path: string, kind: EntryKind, from?: string): string | undefined => {
      const inFrom = (p: string) => !!from && (p === from || p.startsWith(`${from}/`))
      if (from && kind === "folder" && path.startsWith(`${from}/`)) return "A folder can't be moved into itself"
      const lower = path.toLowerCase()
      for (const f of files) {
        if (inFrom(f.path)) continue
        const p = f.path.toLowerCase()
        if (p === lower) return `“${path}” already exists`
        if (p.startsWith(`${lower}/`)) return `A folder “${path}” already exists`
        if (lower.startsWith(`${p}/`)) return `“${f.path}” is a file, not a folder`
      }
      if (emptyDirs.some((d) => !inFrom(d) && d.toLowerCase() === lower)) return `A folder “${path}” already exists`
      for (const p of Object.keys(visiblePending.deleted)) {
        if (p.toLowerCase() === lower) return `“${path}” is deleted in your pending changes — apply or discard them first`
      }
      if (envLayout && !/^modules\/[^/]+\/./.test(path) && !(kind === "folder" && /^modules(\/[^/]+)?$/.test(path)))
        return "In the environments layout, files go in the service module (modules/<service>/) or a local module (modules/<name>/); the environment roots are generated"
      return undefined
    },
    [files, emptyDirs, visiblePending.deleted, envLayout],
  )

  const createEntry = useCallback(
    (path: string, kind: EntryKind) => {
      if (kind === "folder") {
        setPending((p) => createFolder(p, path))
        return
      }
      setPending((p) => createFile(p, path))
      openFile(path, { focusEditor: true })
    },
    [openFile],
  )

  /** Stages files written by a quick fix or a template: new ones are created, existing ones get an unsaved edit. */
  const writeFiles = useCallback(
    async (written: Record<string, string>) => {
      for (const [path, value] of Object.entries(written)) {
        const exists = filesRef.current.some((f) => f.path === path)
        if (!exists) {
          setPending((p) => createFile(p, path))
          setBuffers((b) => ({ ...b, [path]: { base: "", value } }))
          continue
        }
        const base = buffersRef.current[path]?.base ?? (await textOf(path))
        setBuffers((b) => ({ ...b, [path]: { base: b[path]?.base ?? base, value } }))
      }
      const first = Object.keys(written)[0]
      if (first) openFile(first)
    },
    [textOf, openFile],
  )

  // Completion, hover, go to definition, checks and quick fixes as you type: Terraform for .tf files; compose files,
  // Dockerfiles, .env and .dockerignore in a Docker workspace; manifests, Kustomize and Helm files in a Kubernetes
  // one. References reach unapplied files too. The editor gets them once their code has loaded.
  const dockerLang = useLazyModule(active && docker ? dockerIntelCode : undefined)
  const kubernetesLang = useLazyModule(active && kubernetes ? kubernetesIntelCode : undefined)
  const terraformLang = useLazyModule(active && !docker && !kubernetes ? terraformIntelCode : undefined)
  const intel = useMemo(() => {
    if (!active) return undefined
    const buffers = () => {
      const out: Record<string, string> = {}
      for (const p of Object.keys(pendingRef.current.created)) out[p] = ""
      for (const [p, b] of Object.entries(buffersRef.current)) out[p] = b.value
      return out
    }
    const open = (path: string, opts: { line?: number }) => openFile(path, { line: opts.line })
    if (docker) {
      return (
        dockerLang?.dockerIntel({
          workflowId,
          path: active,
          buffers,
          compose: () => useWorkflowStore.getState().workflow?.compose,
          files: () => filesRef.current.map((f) => f.path),
          read: textOf,
          openFile: (path, opts) => openFile(path, { line: opts.line }),
          writeFiles: (written) => void writeFiles(written),
        }) ?? undefined
      )
    }
    if (kubernetes) {
      return (
        kubernetesLang?.kubernetesIntel({
          workflowId,
          path: active,
          buffers,
          openFile: open,
          kube: () => useWorkflowStore.getState().workflow?.kubernetes,
        }) ?? undefined
      )
    }
    return active.endsWith(".tf") ? terraformLang?.terraformIntel({ workflowId, path: active, openFile: open, buffers }) : undefined
  }, [workflowId, active, openFile, docker, kubernetes, textOf, writeFiles, dockerLang, kubernetesLang, terraformLang])

  /** "New from template…" with a snippet: the service goes at the end of the compose file's services. */
  const insertSnippet = useCallback(
    async (composeFile: string, snippet: string) => {
      try {
        await writeFiles({ [composeFile]: insertService(await textOf(composeFile), snippet) })
      } catch (err) {
        toast.error(`Couldn't add the service to ${baseName(composeFile)}`, { description: errorMessage(err) })
      }
    },
    [writeFiles, textOf],
  )

  const renameEntry = useCallback(
    (from: string, to: string) => {
      const r = renamePath(pendingRef.current, files, from, to)
      setPending(r.pending)
      followRenames(r.renames)
    },
    [files, followRenames],
  )

  const deleteEntry = useCallback(
    async (path: string, kind: EntryKind) => {
      const inside = files.filter((f) => f.path === path || f.path.startsWith(`${path}/`))
      const choice = await ask({
        title: kind === "folder" ? `Delete folder ${baseName(path)}?` : `Delete ${baseName(path)}?`,
        description: `${
          kind === "folder" ? `${inside.length} file${inside.length === 1 ? "" : "s"} in ${path}/ will be deleted. ` : ""
        }Nothing is removed until you apply your changes; ${docker ? "services, networks and volumes declared in deleted compose files" : "Terraform blocks in deleted files"} then leave the diagram.`,
        confirmLabel: "Delete",
      })
      if (choice !== "confirm") return
      const r = deletePath(pendingRef.current, files, path)
      setPending(r.pending)
      dropBuffers(r.removed)
      const gone = new Set(r.removed)
      setTabs((t) => t.filter((p) => !gone.has(p)))
      setActive((a) => (a && gone.has(a) ? null : a))
      requestAnimationFrame(() => rootRef.current?.querySelector<HTMLElement>('[role="tree"]')?.focus())
    },
    [files, ask, dropBuffers, docker],
  )

  const duplicateEntry = useCallback(
    async (path: string) => {
      try {
        const text = await textOf(path)
        const copy = copyName(path, (p) => !validatePath(p, "file"))
        setPending((p) => createFile(p, copy))
        setBuffers((b) => ({ ...b, [copy]: { base: "", value: text } }))
        openFile(copy)
      } catch (err) {
        toast.error(`Couldn't duplicate ${baseName(path)}`, { description: errorMessage(err) })
      }
    },
    [textOf, validatePath, openFile],
  )

  const addModule = useCallback(
    async (scaffold: ModuleScaffold, addCall: boolean) => {
      let next = pendingRef.current
      const added: Record<string, Buffer> = {}
      for (const [path, text] of Object.entries(scaffold.files)) {
        next = createFile(next, path)
        added[path] = { base: "", value: text }
      }
      if (addCall) {
        const target = scaffold.call.path
        try {
          if (byPath.has(target)) {
            const text = await textOf(target)
            const base = buffersRef.current[target]?.base ?? text
            added[target] = { base, value: `${text.trimEnd()}\n\n${scaffold.call.block}` }
          } else {
            next = createFile(next, target)
            added[target] = { base: "", value: scaffold.call.block }
          }
        } catch (err) {
          toast.error(`Couldn't add the module call to ${target}`, { description: errorMessage(err) })
        }
      }
      setPending(next)
      setBuffers((b) => ({ ...b, ...added }))
      openFile(`${scaffold.dir}/main.tf`, { focusEditor: true })
      toast.success(`Module ${scaffold.name} added`, {
        description: "Write its resources in main.tf, then apply your changes to see module." + scaffold.name + ".",
      })
    },
    [byPath, textOf, openFile],
  )

  /** Saves the edits of one stored file on its own (⌘S on an asset). */
  const save = useCallback(
    async (path: string) => {
      const b = buffers[path]
      if (!b || savingPath) return
      setSavingPath(path)
      try {
        await useWorkflowStore.getState().flush()
        const wf = await api.applyFileChanges(workflowId, { changes: { [path]: b.value } })
        // Seed the new version so the editor never flashes the old text.
        const file = serverByPath.get(path)
        if (file) queryClient.setQueryData(fileKey(workflowId, wf.updatedAt, path), { ...file, content: b.value })
        useWorkflowStore.getState().setWorkflow(wf)
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
    [buffers, savingPath, workflowId, serverByPath],
  )

  /** Applies every pending change in one request: files are stored, Terraform code is re-imported. */
  const apply = useCallback(async () => {
    if (applying || changed.length === 0) return
    setApplying(true)
    setApplyError(null)
    setReview(null)
    const sentBuffers = buffers
    const sentPending = pending
    try {
      await useWorkflowStore.getState().flush()
      const wf = await api.applyFileChanges(workflowId, {
        ...changesRequest(sentPending, sentBuffers),
        ...(formatOnApply ? { format: true } : {}),
      })
      // Load the new listing and the open files first so the tree and editor switch straight to them.
      await Promise.allSettled([
        queryClient.fetchQuery({ queryKey: ["files", workflowId, wf.updatedAt], queryFn: () => api.files(workflowId) }),
        ...tabs
          .filter((p) => !byPath.get(p)?.binary)
          .map((p) =>
            queryClient.fetchQuery({
              queryKey: fileKey(workflowId, wf.updatedAt, p),
              queryFn: () => api.fileContent(workflowId, p),
              staleTime: Infinity,
            }),
          ),
      ])
      useWorkflowStore.getState().setWorkflow(wf)
      // What was applied is done; edits typed meanwhile stay.
      setBuffers((prev) => Object.fromEntries(Object.entries(prev).filter(([p, b]) => sentBuffers[p] !== b)))
      setPending((p) => (p === sentPending ? { ...NO_PENDING, folders: p.folders } : p))
      const errors = wf.diagnostics.filter((d) => d.severity === "error").length
      toast.success(changed.length === 1 ? `Applied ${baseName(changed[0])}` : `Applied ${changed.length} file changes`, {
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
    // changedKey stands for changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [applying, changedKey, buffers, pending, formatOnApply, workflowId, tabs, byPath])

  const discardAll = () => {
    setBuffers({})
    setPending(NO_PENDING)
    setApplyError(null)
  }

  const closeTab = useCallback(
    async (path: string) => {
      // A new file's text is the file itself: closing its tab keeps it.
      if (pro && buffers[path] && !pending.created[path]) {
        const asset = !isCode(path)
        const choice = await ask({
          title: `Discard changes to ${baseName(path)}?`,
          description: asset
            ? "Your edits to this file are not saved yet."
            : "Your edits to this file have not been applied to the diagram yet.",
          confirmLabel: "Discard",
          altLabel: asset && !status[path] ? "Save" : undefined,
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
    // isCode reads byPath.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [buffers, pending, status, ask, pro, save, dropBuffers, byPath, tabs, active],
  )

  // Unsaved edits and file operations block switching or closing the workflow until the user discards them.
  const dirtyRef = useRef<string[]>([])
  dirtyRef.current = pendingPaths(pending, buffers)
  useEffect(
    () =>
      setLeaveGuard(async () => {
        const dirty = dirtyRef.current
        if (dirty.length === 0) return true
        const names = dirty.length > 4 ? `${dirty.slice(0, 4).map(baseName).join(", ")} and ${dirty.length - 4} more` : dirty.map(baseName).join(", ")
        const choice = await ask({
          title: "Discard unsaved file changes?",
          description: `${names} ${dirty.length === 1 ? "has" : "have"} changes (edits, new, renamed or deleted files) that are not applied yet.`,
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

  const formatActive = async () => {
    if (!canEdit || !active || shownText === undefined) return
    if (isYaml(active)) {
      // Re-indents only: comments, key order and values stay as written.
      try {
        const formatted = formatYaml(shownText)
        if (formatted !== shownText) editorRef.current?.replaceDocument(formatted)
        else toast.success(`${baseName(active)} is already formatted`)
      } catch (err) {
        toast.error(`Couldn't format ${baseName(active)}`, { description: err instanceof YamlFormatError ? err.message : errorMessage(err) })
      }
      return
    }
    if (!isFormattable(active)) return
    try {
      const res = await api.formatCode(shownText, active)
      if (res.changed) editorRef.current?.replaceDocument(res.content)
      else toast.success(`${baseName(active)} is already formatted`)
    } catch (err) {
      const d = err instanceof ApiError ? err.diagnostics[0] : undefined
      toast.error(`Couldn't format ${baseName(active)}`, {
        description: d ? `Line ${d.line}: ${d.message}${d.detail ? ` — ${withoutLocation(d).detail}` : ""}` : errorMessage(err),
      })
    }
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    const mod = e.metaKey || e.ctrlKey
    if (e.altKey && e.shiftKey && !mod && e.code === "KeyF") {
      e.preventDefault()
      void formatActive()
      return
    }
    if (!mod || e.shiftKey || e.altKey) return
    const key = e.key.toLowerCase()
    if (key === "n") {
      e.preventDefault()
      treeRef.current?.startCreate("file")
    } else if (key === "s") {
      e.preventDefault()
      if (!pro) return
      if (active && buffers[active] && !isCode(active) && !status[active]) void save(active)
      else if (changed.length) void apply()
    }
  }

  const editorDiagnostics = useMemo<EditorDiagnostic[]>(
    () =>
      (applyError?.diagnostics ?? [])
        .filter((d) => d.file === active && d.line)
        .map((d) => ({ line: d.line!, severity: d.severity, message: d.detail ? `${d.message}: ${d.detail}` : d.message })),
    [applyError, active],
  )

  const pendingChanges = (paths: string[]): PendingChange[] =>
    paths.map((p) => {
      if (visiblePending.deleted[p]) return { path: p, status: "deleted" }
      const b = visibleBuffers[p]
      if (visiblePending.created[p]) return { path: p, status: "added", before: "", after: b?.value ?? "" }
      const from = visiblePending.moved[p]
      return { path: p, status: from ? "renamed" : "modified", from, before: b?.base, after: b?.value }
    })

  const download = async (path: string) => {
    const origin = serverPath(visiblePending, path)
    if (origin === undefined) {
      toast.info(`${baseName(path)} is new: apply your changes to download it`)
      return
    }
    try {
      if (await downloadWorkflowFile(workflowId, origin)) toast.success(`Downloaded ${baseName(path)}`)
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

  if (listing.data && files.length === 0 && !pro) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Files />
          </EmptyMedia>
          <EmptyTitle>No files yet</EmptyTitle>
          <EmptyDescription>
            {docker
              ? "Add services to the canvas and their compose file appears here."
              : "Add resources to the canvas and their Terraform code appears here."}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  const dirtyActive = !!active && !!visibleBuffers[active]
  const activeIsCode = !!active && isCode(active)
  const contentError = content.error instanceof ApiError ? content.error : null
  const reviewPaths = review === "all" ? changed : review ? [review] : []

  return (
    <div ref={rootRef} className="h-full min-h-0" onKeyDown={onKeyDown}>
      <ResizablePanelGroup
        orientation="horizontal"
        defaultLayout={layout.defaultLayout}
        onLayoutChanged={layout.onLayoutChanged}
      >
        <ResizablePanel id="files-tree" defaultSize="240px" minSize="160px" maxSize="50%">
          <FileTree
            ref={treeRef}
            workflowId={workflowId}
            workflowName={workflowName}
            updatedAt={updatedAt}
            files={files}
            loading={listing.isPending}
            activePath={active}
            edits={edits}
            pending={status}
            emptyDirs={emptyDirs}
            canSearchContents={pro}
            canEdit={pro}
            validatePath={validatePath}
            onOpen={openFile}
            onQuickOpen={() => setQuickOpen(true)}
            onExport={openExport}
            onCreate={createEntry}
            onRename={renameEntry}
            onDelete={(p, kind) => void deleteEntry(p, kind)}
            onDuplicate={(p) => void duplicateEntry(p)}
            onNewModule={docker || kubernetes ? undefined : () => setModuleOpen(true)}
            onNewFromTemplate={docker || kubernetes ? setTemplateDir : undefined}
            docker={docker || kubernetes}
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
                modified={(p) => !!visibleBuffers[p] || !!status[p]}
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
                onSave={dirtyActive && !activeIsCode && !status[active!] ? () => void save(active!) : undefined}
                onRevert={() => dropBuffers([active!])}
                onShowChanges={() => setReview(active)}
                onFind={shownText !== undefined ? () => editorRef.current?.openSearch() : undefined}
                onFormat={
                  canEdit && shownText !== undefined && (isFormattable(activeFile.path) || isYaml(activeFile.path))
                    ? () => void formatActive()
                    : undefined
                }
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
                        Pick a file in the tree or press <Kbd>⌘P</Kbd> to find one
                        {pro ? (
                          <>
                            ; <Kbd>⌘N</Kbd> creates one
                          </>
                        ) : null}
                        .
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
                    extensions={intel}
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
                    onSelect={d.file && byPath.has(d.file) ? () => openFile(d.file!, { line: d.line }) : undefined}
                  />
                ))}
              </div>
            )}
            {pro && changed.length > 0 && (
              <div className="flex h-9 shrink-0 items-center gap-2 border-t bg-primary/6 px-3 text-xs">
                <span className="size-2 shrink-0 rounded-full bg-amber-500" aria-hidden />
                <span className="min-w-0 flex-1 truncate">
                  <span className="font-medium">
                    {changed.length === 1 ? `${baseName(changed[0])} changed` : `${changed.length} files changed`}
                  </span>
                  <span className="hidden text-muted-foreground @[40rem]:inline">
                    {" "}
                    — applying stores the files and re-imports the code into the diagram
                  </span>
                </span>
                <Tooltip>
                  <TooltipTrigger asChild>
                    <label className="flex shrink-0 cursor-pointer items-center gap-1.5 text-muted-foreground">
                      <Checkbox
                        aria-label="Format on apply"
                        checked={formatOnApply}
                        onCheckedChange={(v) => {
                          localStorage.setItem(FORMAT_KEY, v === true ? "1" : "0")
                          setFormatOnApply(v === true)
                        }}
                        className="size-3.5"
                      />
                      <span className="hidden @[30rem]:inline">Format</span>
                    </label>
                  </TooltipTrigger>
                  <TooltipContent>Format the HCL you apply (terraform fmt)</TooltipContent>
                </Tooltip>
                <Button size="xs" variant="ghost" onClick={() => setReview("all")}>
                  <GitCompare /> Review
                </Button>
                <Button size="xs" variant="ghost" onClick={discardAll} disabled={applying}>
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

      <NewModuleDialog
        open={moduleOpen}
        onOpenChange={setModuleOpen}
        workflowId={workflowId}
        taken={(dir) =>
          files.some((f) => f.path.startsWith(`${dir}/`)) ||
          emptyDirs.includes(dir) ||
          Object.keys(visiblePending.deleted).some((p) => p.startsWith(`${dir}/`))
            ? `${dir}/ already exists`
            : undefined
        }
        onCreate={(sc, call) => void addModule(sc, call)}
      />

      <NewFromTemplateDialog
        dir={templateDir}
        onClose={() => setTemplateDir(null)}
        kinds={kubernetes ? ["kubernetes", "docker"] : ["docker"]}
        composeFiles={docker ? (composeFiles ?? []).filter((f) => byPath.has(f)) : []}
        validate={(p) => validatePath(p, "file")}
        onCreate={(path, content) => {
          createEntry(path, "file")
          setBuffers((b) => ({ ...b, [path]: { base: "", value: content } }))
        }}
        onInsert={(file, snippet) => void insertSnippet(file, snippet)}
      />

      <ChangesDialog
        open={review !== null}
        onOpenChange={(o) => !o && setReview(null)}
        title={review === "all" ? "Review changes" : `Changes to ${review ? baseName(review) : ""}`}
        description={
          review === "all"
            ? docker
              ? "Applying stores these files and reads the compose files and Dockerfiles back into the diagram."
              : "Applying stores these files and re-imports the Terraform code into the diagram."
            : "Saving writes this file into the workflow's files."
        }
        changes={pendingChanges(reviewPaths)}
        action={
          review === "all"
            ? { label: "Apply changes", run: () => void apply(), disabled: applying }
            : review && !isCode(review) && !status[review]
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
