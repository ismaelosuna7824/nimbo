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
import { DockerBadge } from "@/features/docker/badge"
import { KubernetesBadge } from "@/features/kubernetes/badge"
import { DiagnosticRow } from "../components/diagnostic-row"
import { formatBytes } from "../lib/format"
import { ImportTooLargeError, readFileList, useImportSource, type PendingImport } from "./import-source"
import { initialSelection, selectAll, toggleProject } from "./import-selection"

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
      // Every project is preselected except reusable modules nothing calls and the folders of a Dockerfile group.
      setSelected(initialSelection(res.projects))
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
    const picked = await openDialog({ directory: true, multiple: false, title: "Choose a Terraform, Docker Compose or Kubernetes folder" })
    if (typeof picked === "string") void runScan({ source: { dir: picked }, label: picked.split(/[\\/]/).pop() || picked })
  }

  const fromBrowser = async (read: () => Promise<PendingImport | null>) => {
    try {
      const p = await read()
      if (!p) {
        toast.error("No project files found", {
          description: "Choose a folder with .tf files, a compose file, a Dockerfile, Kubernetes manifests, a kustomization or a Helm chart.",
        })
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
        toast.success(`Imported “${first.name}”`, {
          description: `${
            first.kind === "docker"
              ? plural(first.compose?.graph.nodes.filter((n) => n.kind === "service").length ?? 0, "service")
              : first.kind === "kubernetes"
                ? plural(first.kubernetes?.graph.nodes.length ?? 0, "object")
                : plural(first.nodes.length, "node")
          }${review}`,
        })
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
          <DialogTitle>Import project</DialogTitle>
          <DialogDescription>
            {step === "source"
              ? "Pick the folder of an existing Terraform repository, Docker Compose project or Kubernetes app. Each Terraform project (a folder you run terraform in), each compose project and each set of Kubernetes manifests, Kustomize tree or Helm chart becomes a workflow; local modules and build contexts come along."
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
            <Spinner /> Looking for Terraform, Docker Compose and Kubernetes projects in {source?.label}…
          </div>
        ) : scan ? (
          <ScanResults
            scan={scan}
            label={source?.label ?? ""}
            omitted={source?.omitted ?? 0}
            selected={selected}
            names={names}
            onSelect={(path, on) => setSelected((s) => toggleProject(scan.projects, s, path, on))}
            onSelectAll={(on) => setSelected(selectAll(scan.projects, on))}
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
  const selectable = Object.values(selectAll(scan.projects, true)).filter(Boolean).length
  if (scan.projects.length === 0) {
    return (
      <p className="rounded-md border border-dashed px-4 py-8 text-center text-sm text-muted-foreground">
        No projects in {label}. Pick a folder that contains Terraform (<code>.tf</code> files), a Docker Compose project
        (<code>compose.yaml</code>, <code>docker-compose.yml</code> or a <code>Dockerfile</code>) or Kubernetes manifests (
        <code>*.yaml</code> with <code>apiVersion</code>/<code>kind</code>, <code>kustomization.yaml</code> or a Helm{" "}
        <code>Chart.yaml</code>).
      </p>
    )
  }
  return (
    <div className="flex min-h-0 flex-col gap-3">
      <div className="flex items-center gap-2 text-sm">
        <Checkbox
          aria-label="Select all projects"
          checked={count >= selectable ? true : count === 0 ? false : "indeterminate"}
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
  const layered = p.layout === "environments"
  const docker = p.kind === "docker"
  const kubernetes = p.kind === "kubernetes"
  const envs = docker
    ? []
    : layered || kubernetes
      ? (p.environments ?? [])
      : (p.tfvarsFiles ?? []).map((f) => f.replace(/^.*\//, "").replace(/(\.auto)?\.tfvars$/, "").replace(/^terraform$/, "default"))
  return (
    <Collapsible className={cn("flex flex-col gap-2 px-3 py-2.5", !checked && "opacity-60")} data-testid="import-project" data-kind={p.kind ?? "terraform"}>
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
            {p.members?.length
              ? `${p.members.join(", ")} as one workflow`
              : p.path === "." || p.path === "kubernetes:."
                ? "folder root"
                : p.path.replace(/^kubernetes:/, "")}
          </span>
          {p.outside && p.outside.length > 0 && (
            <span className="truncate font-mono text-[11px] text-muted-foreground" data-testid="import-outside">
              also imports{" "}
              {p.outside.map((o) => `${o.path}${o.dir ? "/" : ""} (${formatBytes(o.bytes)})`).join(", ")}
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center justify-end gap-1">
          {kubernetes ? (
            <>
              <KubernetesBadge />
              <Stat icon={LayersIcon}>{plural(p.objects ?? 0, "object")}</Stat>
              {(p.kubeSources ?? []).map((s) => (
                <Stat
                  key={`${s.kind}:${s.path}`}
                  icon={s.kind === "helm" ? PackageIcon : FileCode2Icon}
                  tip={
                    s.kind === "helm"
                      ? `Helm chart ${s.chart ? `${s.chart.name} ${s.chart.version}` : ""} in ${s.path}/, deployed as release ${s.release || s.chart?.name || s.path}`
                      : s.kind === "kustomize"
                        ? `Kustomization in ${s.path}/ (kustomize build)`
                        : `Manifests in ${s.path}`
                  }
                >
                  {s.kind === "helm" ? `Helm ${s.chart?.name ?? s.path}` : s.kind === "kustomize" ? "Kustomize" : "Manifests"}
                </Stat>
              ))}
              {p.bytes !== undefined && <Stat icon={PackageIcon}>{formatBytes(p.bytes)}</Stat>}
            </>
          ) : docker ? (
            <>
              <DockerBadge />
              <Stat icon={LayersIcon}>{plural(p.services ?? 0, "service")}</Stat>
              <Stat
                icon={FileCode2Icon}
                tip={
                  p.members?.length
                    ? `A compose.yaml is created in ${p.path.replace(/^dockerfiles:/, "")}/ building one service per folder: ${(p.dockerfiles ?? []).join(", ")}. Or import each folder on its own below.`
                    : p.composeFiles?.length
                      ? `Compose files, in order: ${p.composeFiles.join(", ")}${p.dockerfiles?.length ? `. Dockerfiles: ${p.dockerfiles.join(", ")}` : ""}`
                      : "No compose file: a compose.yaml building this Dockerfile is created"
                }
              >
                {p.members?.length
                  ? plural(p.members.length, "Dockerfile")
                  : p.composeFiles?.length
                    ? plural(p.composeFiles.length, "compose file")
                    : "Dockerfile only"}
              </Stat>
              {p.outside && p.outside.length > 0 && (
                <Stat
                  icon={FolderOpenIcon}
                  tip={
                    <>
                      Referenced by the compose files from outside this folder, imported with it. The workflow keeps the folder
                      layout from their common parent, so the compose files stay unchanged:
                      <ul className="mt-1 font-mono text-[11px]">
                        {p.outside.map((o) => (
                          <li key={o.path}>
                            {o.path}
                            {o.dir ? "/" : ""} · {o.kinds.join(", ")} · {plural(o.files, "file")}, {formatBytes(o.bytes)}
                          </li>
                        ))}
                      </ul>
                    </>
                  }
                >
                  {plural(p.outside.length, "outside path")}
                </Stat>
              )}
              {p.bytes !== undefined && <Stat icon={PackageIcon}>{formatBytes(p.bytes)}</Stat>}
            </>
          ) : (
            <>
              {p.clouds.map((c) => (
                <CloudBadge key={c} cloud={c} />
              ))}
              <Stat icon={LayersIcon} tip={p.dataSources ? `${plural(p.dataSources, "data source")} too` : undefined}>
                {plural(p.resources, "resource")}
              </Stat>
              {p.modules > 0 && <Stat icon={PackageIcon}>{plural(p.modules, "module")}</Stat>}
            </>
          )}
          {envs.length > 0 && (
            <Stat
              icon={FileCode2Icon}
              tip={
                layered ? (
                  <>
                    One root per environment calling one service module: imported as one workflow with the environments{" "}
                    {envs.join(", ")} (values from each root's terraform.tfvars); exported in the same layout.
                  </>
                ) : kubernetes ? (
                  <>Kustomize overlays and Helm values files become environments: {envs.join(", ")}</>
                ) : (
                  <>Each .tfvars file becomes an environment: {p.tfvarsFiles.join(", ")}</>
                )
              }
            >
              {layered ? `${plural(envs.length, "environment")} · one state each` : envs.length === 1 ? `env ${envs[0]}` : plural(envs.length, "environment")}
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
