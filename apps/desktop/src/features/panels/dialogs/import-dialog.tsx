import { useCallback, useEffect, useRef, useState } from "react"
import { isTauri } from "@tauri-apps/api/core"
import { open as openDialog } from "@tauri-apps/plugin-dialog"
import {
  ArrowLeftIcon,
  ChevronRightIcon,
  DatabaseIcon,
  FileCode2Icon,
  FolderOpenIcon,
  FolderSearchIcon,
  LayersIcon,
  PackageIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { toast } from "sonner"
import { CloudBadge } from "@/components/cloud"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { ApiError, api } from "@/lib/api/client"
import type { Diagnostic, ImportProject, ImportScan } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { ImportTooLargeError, readFileList, useImportSource, type PendingImport } from "./import-source"

type Failure = { message: string; diagnostics: Diagnostic[] }

const toFailure = (err: unknown): Failure =>
  err instanceof ApiError ? { message: err.message, diagnostics: err.diagnostics } : { message: errorMessage(err), diagnostics: [] }

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? "" : "s"}`

export function ImportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const tauri = isTauri()
  const [source, setSource] = useState<PendingImport | null>(null)
  const [scan, setScan] = useState<ImportScan | null>(null)
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [names, setNames] = useState<Record<string, string>>({})
  const [pasting, setPasting] = useState(false)
  const [pasted, setPasted] = useState("")
  const [busy, setBusy] = useState<"scan" | "import" | null>(null)
  const [error, setError] = useState<Failure | null>(null)
  const folderInput = useRef<HTMLInputElement>(null)

  const reset = () => {
    setSource(null)
    setScan(null)
    setSelected({})
    setNames({})
    setPasting(false)
    setPasted("")
    setError(null)
  }

  const runScan = useCallback(async (p: PendingImport) => {
    setSource(p)
    setScan(null)
    setError(null)
    setBusy("scan")
    try {
      const res = await api.scanImport(p.source)
      setScan(res)
      // Every project is preselected except reusable modules nothing calls.
      setSelected(Object.fromEntries(res.projects.map((pr) => [pr.path, !pr.diagnostics.some((d) => d.code === "unused_module")])))
      setNames(Object.fromEntries(res.projects.map((pr) => [pr.path, pr.name])))
    } catch (err) {
      setError(toFailure(err))
    } finally {
      setBusy(null)
    }
  }, [])

  // A folder dropped on the window lands here: scan it right away (step 2).
  useEffect(
    () =>
      useImportSource.subscribe(({ pending }) => {
        if (!pending) return
        useImportSource.getState().setPending(null)
        void runScan(pending)
      }),
    [runScan],
  )

  const pickFolder = async () => {
    const picked = await openDialog({ directory: true, multiple: false, title: "Choose a Terraform folder" })
    if (typeof picked === "string") void runScan({ source: { dir: picked }, label: picked.split(/[\\/]/).pop() || picked })
  }

  const fromBrowser = async (read: () => Promise<PendingImport | null>) => {
    try {
      const p = await read()
      if (!p) {
        toast.error("No Terraform files found", { description: "Choose a folder that contains .tf files." })
        return
      }
      void runScan(p)
    } catch (err) {
      toast.error(err instanceof ImportTooLargeError ? "Folder too large" : "Couldn't read the folder", {
        description: errorMessage(err),
      })
    }
  }

  const chosen = scan?.projects.filter((p) => selected[p.path]) ?? []

  const submit = async () => {
    if (!source || chosen.length === 0) return
    setBusy("import")
    setError(null)
    try {
      const { workflows } = await api.importTerraform({
        ...source.source,
        projects: chosen.map((p) => p.path),
        names: Object.fromEntries(chosen.map((p) => [p.path, names[p.path]?.trim() || p.name])),
      })
      void queryClient.invalidateQueries({ queryKey: ["workflows"] })
      const first = workflows[0]
      await useWorkflowStore.getState().open(first.id)
      const problems = workflows.reduce((n, wf) => n + wf.diagnostics.filter((d) => d.severity !== "info").length, 0)
      const review = problems ? ` · ${plural(problems, "problem")} to review` : ""
      if (workflows.length === 1) {
        toast.success(`Imported “${first.name}”`, { description: `${plural(first.nodes.length, "node")}${review}` })
      } else {
        toast.success(`Imported ${workflows.length} workflows`, {
          description: `Opened “${first.name}”; ${workflows.length - 1 === 1 ? `“${workflows[1].name}” is` : `the other ${workflows.length - 1} are`} on Home${review}`,
        })
      }
      reset()
      onOpenChange(false)
    } catch (err) {
      setError(toFailure(err))
    } finally {
      setBusy(null)
    }
  }

  const close = (o: boolean) => {
    if (!o) reset()
    onOpenChange(o)
  }

  const step = source ? "review" : "source"

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent className={cn(step === "review" ? "sm:max-w-3xl" : "sm:max-w-lg")}>
        <DialogHeader>
          <DialogTitle>Import Terraform</DialogTitle>
          <DialogDescription>
            {step === "source"
              ? "Pick the folder of an existing Terraform repository. Each project (a folder you run terraform in) becomes a workflow; local modules come along."
              : "Choose which projects to import. Each one becomes its own workflow."}
          </DialogDescription>
        </DialogHeader>

        {step === "source" ? (
          <div className="flex flex-col gap-3">
            {/* Drops are handled window-wide by FolderDrop, which hands the folder back to this dialog. */}
            <div className="flex flex-col items-center gap-3 rounded-xl border-2 border-dashed px-6 py-8 text-center">
              <FolderSearchIcon className="size-8 text-muted-foreground" />
              <div className="text-sm">
                <p className="font-medium">Drop a folder anywhere on the window</p>
                <p className="text-muted-foreground">A single project or a whole repo with environments and modules</p>
              </div>
              <Button
                onClick={() => (tauri ? void pickFolder() : folderInput.current?.click())}
                disabled={busy !== null}
              >
                <FolderOpenIcon /> Choose folder…
              </Button>
              <input
                ref={(el) => {
                  folderInput.current = el
                  el?.setAttribute("webkitdirectory", "")
                }}
                type="file"
                className="hidden"
                data-testid="import-folder-input"
                onChange={(e) => {
                  const list = e.target.files
                  if (list?.length) void fromBrowser(() => readFileList(list)).then(() => (e.target.value = ""))
                }}
              />
            </div>
            <Collapsible open={pasting} onOpenChange={setPasting}>
              <CollapsibleTrigger asChild>
                <Button variant="link" size="sm" className="h-auto px-0 text-xs text-muted-foreground">
                  <FileCode2Icon /> Or paste HCL instead
                </Button>
              </CollapsibleTrigger>
              <CollapsibleContent className="flex flex-col gap-2 pt-2">
                <Textarea
                  rows={8}
                  value={pasted}
                  onChange={(e) => setPasted(e.target.value)}
                  spellCheck={false}
                  className="font-mono text-xs"
                  placeholder={'resource "aws_s3_bucket" "site" {\n  bucket = "my-site"\n}'}
                />
                <Button
                  variant="outline"
                  size="sm"
                  className="self-end"
                  disabled={!pasted.trim() || busy !== null}
                  onClick={() => void runScan({ source: { files: { "main.tf": pasted } }, label: "Pasted HCL" })}
                >
                  Continue
                </Button>
              </CollapsibleContent>
            </Collapsible>
          </div>
        ) : busy === "scan" ? (
          <div className="flex items-center justify-center gap-2 py-12 text-sm text-muted-foreground">
            <Spinner /> Looking for Terraform projects in {source?.label}…
          </div>
        ) : scan ? (
          <ScanResults
            scan={scan}
            label={source?.label ?? ""}
            omitted={source?.omitted ?? 0}
            selected={selected}
            names={names}
            onSelect={(path, on) => setSelected((s) => ({ ...s, [path]: on }))}
            onSelectAll={(on) => setSelected(Object.fromEntries(scan.projects.map((p) => [p.path, on])))}
            onName={(path, name) => setNames((n) => ({ ...n, [path]: name }))}
          />
        ) : null}

        {error && (
          <div className="max-h-48 overflow-y-auto rounded-md border border-destructive/40 bg-destructive/5 text-xs">
            <p className="px-3 py-2 font-medium text-destructive">{error.message}</p>
            {error.diagnostics.map((d, i) => (
              <DiagnosticRow key={i} diagnostic={d} />
            ))}
          </div>
        )}

        <DialogFooter>
          {step === "review" && (
            <Button variant="ghost" className="mr-auto" disabled={busy !== null} onClick={reset}>
              <ArrowLeftIcon /> Choose another folder
            </Button>
          )}
          <Button variant="ghost" onClick={() => close(false)}>
            Cancel
          </Button>
          {step === "review" && (
            <Button disabled={chosen.length === 0 || busy !== null} onClick={() => void submit()}>
              {busy === "import" && <Spinner />}
              {chosen.length > 1 ? `Import ${chosen.length} workflows` : "Import"}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

interface ScanResultsProps {
  scan: ImportScan
  label: string
  omitted: number
  selected: Record<string, boolean>
  names: Record<string, string>
  onSelect: (path: string, on: boolean) => void
  onSelectAll: (on: boolean) => void
  onName: (path: string, name: string) => void
}

function ScanResults({ scan, label, omitted, selected, names, onSelect, onSelectAll, onName }: ScanResultsProps) {
  const count = scan.projects.filter((p) => selected[p.path]).length
  if (scan.projects.length === 0) {
    return (
      <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
        No Terraform projects in {label}. Pick a folder that contains <code>.tf</code> files.
      </p>
    )
  }
  return (
    <div className="flex min-h-0 flex-col gap-3">
      <div className="flex items-center gap-2 text-sm">
        <Checkbox
          aria-label="Select all projects"
          checked={count === scan.projects.length ? true : count === 0 ? false : "indeterminate"}
          onCheckedChange={(v) => onSelectAll(v === true)}
        />
        <span>
          Found <span className="font-medium">{plural(scan.projects.length, "project")}</span> in{" "}
          <span className="font-mono text-xs" title={scan.root}>
            {label}
          </span>
        </span>
        <span className="ml-auto text-xs text-muted-foreground">
          {count} of {scan.projects.length} selected
        </span>
      </div>
      <div className="max-h-[50vh] divide-y overflow-y-auto rounded-lg border">
        {scan.projects.map((p) => (
          <ProjectRow
            key={p.path}
            project={p}
            checked={!!selected[p.path]}
            name={names[p.path] ?? p.name}
            onCheck={(on) => onSelect(p.path, on)}
            onName={(n) => onName(p.path, n)}
          />
        ))}
      </div>
      {(scan.skipped.length > 0 || omitted > 0) && (
        <Collapsible>
          <CollapsibleTrigger className="group flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            <ChevronRightIcon className="size-3.5 transition-transform group-data-[state=open]:rotate-90" />
            {[
              scan.skipped.length > 0 && `${plural(scan.skipped.length, "folder")} not offered as projects`,
              omitted > 0 && `${plural(omitted, "file")} not uploaded (binary or over 1 MB)`,
            ]
              .filter(Boolean)
              .join(" · ")}
          </CollapsibleTrigger>
          <CollapsibleContent className="pt-1.5 pl-4.5">
            <ul className="flex flex-col gap-0.5 font-mono text-[11px] text-muted-foreground">
              {scan.skipped.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
            {omitted > 0 && (
              <p className="mt-1 text-[11px] text-muted-foreground">
                Browser uploads only carry text files. Import from the desktop app to keep binaries such as zipped
                Lambda packages.
              </p>
            )}
          </CollapsibleContent>
        </Collapsible>
      )}
    </div>
  )
}

function Stat({ icon: Icon, children, tip }: { icon: typeof LayersIcon; children: React.ReactNode; tip?: React.ReactNode }) {
  const badge = (
    <span className="inline-flex h-5 items-center gap-1 rounded-full bg-muted px-2 text-xs whitespace-nowrap text-muted-foreground">
      <Icon className="size-3" />
      {children}
    </span>
  )
  if (!tip) return badge
  return (
    <Tooltip>
      <TooltipTrigger asChild>{badge}</TooltipTrigger>
      <TooltipContent className="max-w-xs">{tip}</TooltipContent>
    </Tooltip>
  )
}

function ProjectRow({
  project: p,
  checked,
  name,
  onCheck,
  onName,
}: {
  project: ImportProject
  checked: boolean
  name: string
  onCheck: (on: boolean) => void
  onName: (name: string) => void
}) {
  const problems = p.diagnostics.filter((d) => d.severity !== "info")
  const unusedModule = p.diagnostics.find((d) => d.code === "unused_module")
  const errors = problems.filter((d) => d.severity === "error").length
  const envs = p.tfvarsFiles.map((f) => f.replace(/^.*\//, "").replace(/(\.auto)?\.tfvars$/, "").replace(/^terraform$/, "default"))
  return (
    <Collapsible className={cn("flex flex-col gap-2 px-3 py-2.5", !checked && "opacity-60")}>
      <div className="flex items-center gap-3">
        <Checkbox checked={checked} onCheckedChange={(v) => onCheck(v === true)} aria-label={`Import ${p.path}`} />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <Input
            value={name}
            onChange={(e) => onName(e.target.value)}
            disabled={!checked}
            aria-label={`Workflow name for ${p.path}`}
            className="h-7 max-w-64 text-sm font-medium"
          />
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            {p.path === "." ? "folder root" : p.path}
          </span>
        </div>
        <div className="flex flex-wrap items-center justify-end gap-1">
          {p.clouds.map((c) => (
            <CloudBadge key={c} cloud={c} />
          ))}
          <Stat icon={LayersIcon} tip={p.dataSources ? `${plural(p.dataSources, "data source")} too` : undefined}>
            {plural(p.resources, "resource")}
          </Stat>
          {p.modules > 0 && <Stat icon={PackageIcon}>{plural(p.modules, "module")}</Stat>}
          {envs.length > 0 && (
            <Stat icon={FileCode2Icon} tip={<>Each .tfvars file becomes an environment: {p.tfvarsFiles.join(", ")}</>}>
              {envs.length === 1 ? `env ${envs[0]}` : plural(envs.length, "environment")}
            </Stat>
          )}
          {unusedModule && (
            <Stat icon={PackageIcon} tip={`${unusedModule.message}. ${unusedModule.detail ?? ""}`}>
              unused module?
            </Stat>
          )}
          {p.hasBackend && (
            <Stat icon={DatabaseIcon} tip="This project stores its state remotely. In the sandbox it will run with local state, so your real state is never touched.">
              backend → local state
            </Stat>
          )}
          {problems.length > 0 && (
            <CollapsibleTrigger asChild>
              <button
                type="button"
                className={cn(
                  "inline-flex h-5 items-center gap-1 rounded-full px-2 text-xs whitespace-nowrap",
                  errors ? "bg-destructive/12 text-destructive" : "bg-amber-500/12 text-amber-600 dark:text-amber-400",
                )}
              >
                <TriangleAlertIcon className="size-3" />
                {plural(problems.length, errors ? "problem" : "warning")}
              </button>
            </CollapsibleTrigger>
          )}
        </div>
      </div>
      {problems.length > 0 && (
        <CollapsibleContent className="rounded-md border text-xs">
          {problems.map((d, i) => (
            <DiagnosticRow key={i} diagnostic={d} />
          ))}
        </CollapsibleContent>
      )}
    </Collapsible>
  )
}
