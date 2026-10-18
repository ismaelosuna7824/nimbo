import { lazy, Suspense, useCallback, useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Check, Copy, Download, FileCode2, Pencil, RotateCcw, TriangleAlert, X } from "lucide-react"
import { toast } from "sonner"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ApiError, api } from "@/lib/api/client"
import type { Diagnostic } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { copyText } from "../lib/format"

// CodeMirror loads with the first file shown, not with the app.
const CodeEditor = lazy(() => import("./code-editor").then((m) => ({ default: m.CodeEditor })))

const NO_FILES: Record<string, string> = {}
const FILE_ORDER = ["versions.tf", "providers.tf", "variables.tf", "main.tf", "outputs.tf"]

function sortFiles(names: string[]): string[] {
  const rank = (n: string) => {
    const i = FILE_ORDER.indexOf(n)
    return i === -1 ? FILE_ORDER.length : i
  }
  return [...names].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b))
}

interface EditSession {
  buffer: Record<string, string>
  original: Record<string, string>
  /** workflow.updatedAt when editing started, to warn about concurrent diagram edits. */
  baseUpdatedAt: string
}

export function TerraformTab() {
  const workflow = useWorkflowStore((s) => s.workflow)
  const mode = useWorkflowStore((s) => s.mode)
  const openDialog = useWorkflowStore((s) => s.openDialog)
  const [activeFile, setActiveFile] = useState<string | null>(null)
  const [session, setSession] = useState<EditSession | null>(null)
  const [applying, setApplying] = useState(false)
  const [applyError, setApplyError] = useState<{ message: string; diagnostics: Diagnostic[] } | null>(null)

  const query = useQuery({
    queryKey: ["terraform", workflow?.id, workflow?.updatedAt],
    queryFn: () => api.terraform(workflow!.id),
    enabled: !!workflow,
    placeholderData: keepPreviousData,
  })

  const editing = session !== null && mode === "pro"
  const files = editing ? session.buffer : (query.data?.files ?? NO_FILES)
  const names = useMemo(() => sortFiles(Object.keys(files)), [files])
  const current =
    activeFile && activeFile in files ? activeFile : names.includes("main.tf") ? "main.tf" : (names[0] ?? null)
  const changed = editing ? names.filter((n) => session.buffer[n] !== session.original[n]) : []

  const startEditing = () => {
    if (!workflow || !query.data) return
    setApplyError(null)
    setSession({ buffer: { ...query.data.files }, original: query.data.files, baseUpdatedAt: workflow.updatedAt })
  }

  const discard = () => {
    setSession(null)
    setApplyError(null)
  }

  const apply = useCallback(async () => {
    if (!workflow || !session || applying) return
    setApplying(true)
    setApplyError(null)
    try {
      await useWorkflowStore.getState().flush()
      const wf = await api.putTerraform(workflow.id, session.buffer)
      useWorkflowStore.getState().setWorkflow(wf)
      setSession(null)
      const errors = wf.diagnostics.filter((d) => d.severity === "error").length
      toast.success("Code applied to the diagram", {
        description: errors ? `${errors} problem${errors === 1 ? "" : "s"} found — see the Problems tab.` : undefined,
      })
    } catch (err) {
      setApplyError(
        err instanceof ApiError
          ? { message: err.message, diagnostics: err.diagnostics }
          : { message: err instanceof Error ? err.message : String(err), diagnostics: [] },
      )
    } finally {
      setApplying(false)
    }
  }, [workflow, session, applying])

  const onChange = useCallback(
    (text: string) => {
      if (!current) return
      setSession((s) => (s ? { ...s, buffer: { ...s.buffer, [current]: text } } : s))
    },
    [current],
  )

  if (!workflow) return null

  const copyCurrent = async () => {
    if (!current) return
    await copyText(files[current] ?? "")
    toast.success(`Copied ${current}`)
  }

  return (
    <div
      className="flex h-full min-h-0 flex-col"
      onKeyDown={(e) => {
        if (editing && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") {
          e.preventDefault()
          void apply()
        }
      }}
    >
      <div className="flex h-9 shrink-0 items-center gap-1 border-b px-2">
        <div className="flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto" role="tablist">
          {names.map((name) => (
            <button
              key={name}
              type="button"
              role="tab"
              aria-selected={name === current}
              onClick={() => setActiveFile(name)}
              className={cn(
                "flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 font-mono text-xs text-muted-foreground outline-none hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50",
                name === current && "bg-muted text-foreground",
              )}
            >
              <FileCode2 className="size-3.5" />
              {name}
              {changed.includes(name) && <span className="size-1.5 rounded-full bg-amber-500" aria-label="modified" />}
            </button>
          ))}
          {query.isFetching && <Spinner className="ml-1 size-3.5 text-muted-foreground" />}
        </div>
        {editing ? (
          <>
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="shrink-0 border-amber-500/40 text-amber-700 dark:text-amber-300">
                  Editing
                </Badge>
              </TooltipTrigger>
              <TooltipContent>Apply (⌘S) re-imports this code into the diagram</TooltipContent>
            </Tooltip>
            <Button size="sm" variant="ghost" onClick={discard} disabled={applying}>
              <X /> Discard
            </Button>
            <Button size="sm" onClick={() => void apply()} disabled={applying || changed.length === 0}>
              {applying ? <Spinner /> : <Check />} Apply
            </Button>
          </>
        ) : (
          <>
            {mode === "guided" && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge variant="outline" className="shrink-0 text-muted-foreground">
                    Read-only
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-64">
                  Generated from your diagram. Switch to Pro mode to edit the code directly.
                </TooltipContent>
              </Tooltip>
            )}
            <Button size="sm" variant="ghost" onClick={() => void copyCurrent()} disabled={!current}>
              <Copy /> Copy
            </Button>
            <Button size="sm" variant="ghost" onClick={() => openDialog("export")}>
              <Download /> Export…
            </Button>
            {mode === "pro" && (
              <Button size="sm" variant="outline" onClick={startEditing} disabled={!query.data}>
                <Pencil /> Edit code
              </Button>
            )}
          </>
        )}
      </div>

      {editing && session.baseUpdatedAt !== workflow.updatedAt && (
        <div className="flex shrink-0 items-center gap-2 border-b bg-amber-500/10 px-3 py-1 text-xs text-amber-700 dark:text-amber-300">
          <TriangleAlert className="size-3.5" />
          The diagram changed after you started editing. Applying replaces it with this code.
          <Button size="xs" variant="ghost" className="ml-auto" onClick={startEditing}>
            <RotateCcw /> Reload code
          </Button>
        </div>
      )}

      {applyError && (
        <Alert variant="destructive" className="m-2 shrink-0 py-2">
          <TriangleAlert />
          <AlertTitle>Could not apply the code</AlertTitle>
          <AlertDescription>
            <p>{applyError.message}</p>
            {applyError.diagnostics.length > 0 && (
              <div className="-mx-3 mt-1 max-h-40 w-full overflow-auto">
                {applyError.diagnostics.map((d, i) => (
                  <DiagnosticRow key={i} diagnostic={d} />
                ))}
              </div>
            )}
          </AlertDescription>
        </Alert>
      )}

      <div className="min-h-0 flex-1">
        {query.isError && !query.data ? (
          <div className="p-4 text-xs text-destructive">
            Could not generate Terraform: {query.error instanceof Error ? query.error.message : String(query.error)}
          </div>
        ) : current ? (
          <Suspense
            fallback={
              <div className="flex h-full items-center justify-center">
                <Spinner />
              </div>
            }
          >
            <CodeEditor key={current} value={files[current] ?? ""} readOnly={!editing} onChange={onChange} />
          </Suspense>
        ) : query.isPending ? (
          <div className="flex h-full items-center justify-center">
            <Spinner />
          </div>
        ) : (
          <div className="p-4 text-xs text-muted-foreground">
            Add resources to the canvas and their Terraform code appears here.
          </div>
        )}
      </div>
    </div>
  )
}
