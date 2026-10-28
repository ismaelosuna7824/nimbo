import { useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { isTauri } from "@tauri-apps/api/core"
import { open as openDialog } from "@tauri-apps/plugin-dialog"
import { ChevronRight, CircleCheck, Download, FolderOpen } from "lucide-react"
import { toast } from "sonner"
import { useShallow } from "zustand/react/shallow"
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
import { Label } from "@/components/ui/label"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { CIProvider, ExportPipeline, WorkflowFile, WorkflowLayout } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

type LayoutChoice = "auto" | WorkflowLayout

const LAYOUT_HELP: Record<WorkflowLayout, string> = {
  single:
    "One root module and one state. Environments are variable files (environments/<name>.tfvars) of that root, so they share its state: fine for one environment.",
  environments:
    "The resources become a service module (modules/<service>) and every environment gets its own root module, backend key and state (environments/<env>), with a Makefile to init/plan/apply one at a time.",
}

export function ExportDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  // What the export depends on, not the whole workflow: the dialog stays mounted, and every edit replaces the workflow.
  const workflow = useWorkflowStore(
    useShallow(
      (s) =>
        s.workflow && {
          id: s.workflow.id,
          name: s.workflow.name,
          kind: s.workflow.kind,
          layout: s.workflow.layout,
          environments: s.workflow.environments,
          version: s.version,
        },
    ),
  )
  const tauri = isTauri()
  const kubernetes = workflow?.kind === "kubernetes"
  // Docker and Kubernetes workspaces export their files as they are.
  const asIs = workflow?.kind === "docker" || kubernetes
  const what = asIs ? "Project" : "Terraform"
  const [environments, setEnvironments] = useState(true)
  const [pipeline, setPipeline] = useState<ExportPipeline>({ ci: "none", smoke: false })
  const [busy, setBusy] = useState(false)
  const [written, setWritten] = useState<{ dir: string; files: string[] } | null>(null)

  // Automatic: one root per environment from two environments on (the backend falls back to a single root when the
  // environments layout is impossible; the tree preview shows what is written).
  const layout: WorkflowLayout = workflow?.layout ?? ((workflow?.environments.length ?? 0) >= 2 ? "environments" : "single")
  const choice: LayoutChoice = workflow?.layout ?? "auto"
  // The environments layout always writes each environment's values: they are its roots' inputs.
  const withValues = layout === "environments" || environments

  // The tree the export writes, from the saved workflow (refreshed after a layout change is saved).
  const preview = useQuery({
    queryKey: ["files", workflow?.id, workflow?.version],
    queryFn: () => api.files(workflow!.id),
    enabled: open && !!workflow,
    placeholderData: keepPreviousData,
  })

  const close = (o: boolean) => {
    if (!o) setWritten(null)
    onOpenChange(o)
  }

  const chooseLayout = (v: string) => {
    if (!v || !workflow) return
    const next = v === "auto" ? undefined : (v as WorkflowLayout)
    useWorkflowStore.getState().update((wf) => ({ ...wf, layout: next }))
    void useWorkflowStore.getState().flush()
  }

  const exportToFolder = async () => {
    if (!workflow) return
    const dir = await openDialog({ directory: true, multiple: false, title: `Export ${what.toLowerCase()} to folder` })
    if (typeof dir !== "string") return
    setBusy(true)
    try {
      await useWorkflowStore.getState().flush()
      setWritten(await api.exportTo(workflow.id, dir, withValues, pipeline))
    } catch (err) {
      toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  const downloadZip = async () => {
    if (!workflow) return
    setBusy(true)
    try {
      await useWorkflowStore.getState().flush()
      const res = await fetch(api.exportZipUrl(workflow.id, withValues, pipeline))
      if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
      const url = URL.createObjectURL(await res.blob())
      const a = document.createElement("a")
      a.href = url
      a.download = `${workflow.name.replace(/[^\w.-]+/g, "-") || workflow.id}.zip`
      a.click()
      URL.revokeObjectURL(url)
      toast.success(`${what} exported`, { description: a.download })
      close(false)
    } catch (err) {
      toast.error("Export failed", { description: errorMessage(err) })
    } finally {
      setBusy(false)
    }
  }

  const hasEnvs = (workflow?.environments.length ?? 0) > 0

  return (
    <Dialog open={open && !!workflow} onOpenChange={close}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{asIs ? "Export project" : "Export Terraform"}</DialogTitle>
          <DialogDescription>
            {kubernetes ? (
              <>
                The manifests, kustomizations, Helm charts and Dockerfiles as they are, ready for <code>kubectl apply</code>,{" "}
                <code>kustomize build</code> or <code>helm install</code> on any cluster. Nothing Nimbo-specific is added.
              </>
            ) : asIs ? (
              <>
                The compose files, Dockerfiles and build contexts as they are, ready for <code>docker compose up</code>{" "}
                anywhere. Nothing Nimbo-specific is added.
              </>
            ) : (
              <>
                Clean, cloud-ready Terraform: no sandbox settings are included, so the same code deploys to your real
                account with <code>terraform apply</code>.
              </>
            )}
          </DialogDescription>
        </DialogHeader>

        {written ? (
          <div className="flex flex-col gap-2 text-xs">
            <p className="flex items-center gap-1.5 font-medium text-emerald-600 dark:text-emerald-400">
              <CircleCheck className="size-4" /> Wrote {written.files.length} file{written.files.length === 1 ? "" : "s"}
            </p>
            <p className="truncate font-mono text-muted-foreground" title={written.dir}>
              {written.dir}
            </p>
            <ul className="max-h-48 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px]">
              {written.files.map((f) => (
                <li key={f}>{f}</li>
              ))}
            </ul>
          </div>
        ) : (
          <div className="flex flex-col gap-4">
            {asIs ? (
              <TreePreview files={preview.data?.files} loading={preview.isPending} error={preview.error} />
            ) : (
              <>
                <div className="grid gap-1.5">
                  <Label>Layout</Label>
                  <ToggleGroup
                    type="single"
                    variant="outline"
                    size="sm"
                    value={choice}
                    onValueChange={chooseLayout}
                    aria-label="Export layout"
                    className="justify-start"
                  >
                    <ToggleGroupItem value="auto">Automatic</ToggleGroupItem>
                    <ToggleGroupItem value="single">Single root</ToggleGroupItem>
                    <ToggleGroupItem value="environments">Root per environment</ToggleGroupItem>
                  </ToggleGroup>
                  <p className="text-xs text-muted-foreground">
                    {choice === "auto" && (
                      <>
                        Automatic picks a root per environment once the workflow has two or more environments, otherwise
                        a single root.{" "}
                      </>
                    )}
                    {LAYOUT_HELP[layout]}
                  </p>
                  <TreePreview files={preview.data?.files} loading={preview.isPending} error={preview.error} />
                </div>

                {layout === "single" && (
                  <div className="flex items-start gap-2">
                    <Checkbox
                      id="export-envs"
                      checked={environments}
                      onCheckedChange={(v) => setEnvironments(v === true)}
                      className="mt-0.5"
                    />
                    <div className="grid gap-0.5">
                      <Label htmlFor="export-envs">Include environments</Label>
                      <p className="text-xs text-muted-foreground">
                        {hasEnvs
                          ? `Writes each environment's values (${workflow!.environments
                              .map((e) => e.file ?? `environments/${e.name}.tfvars`)
                              .join(", ")}) and a .env.example per environment. Imported variable files keep their name. Secret values are left blank.`
                          : "This workflow has no environments yet."}
                      </p>
                    </div>
                  </div>
                )}
              </>
            )}
            <PipelineSection kind={workflow?.kind} value={pipeline} onChange={setPipeline} />
          </div>
        )}

        <DialogFooter>
          {written ? (
            <Button onClick={() => close(false)}>Done</Button>
          ) : (
            <>
              <Button variant="ghost" onClick={() => close(false)}>
                Cancel
              </Button>
              {tauri ? (
                <Button disabled={busy} onClick={() => void exportToFolder()}>
                  {busy ? <Spinner /> : <FolderOpen />} Choose folder & export
                </Button>
              ) : (
                <Button disabled={busy} onClick={() => void downloadZip()}>
                  {busy ? <Spinner /> : <Download />} Download .zip
                </Button>
              )}
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

const CI_NAMES: Record<Exclude<CIProvider, "none">, string> = { github: "GitHub Actions", gitlab: "GitLab CI" }

/** What the pipeline does for a workflow kind, after "checks on every push". */
function pipelineJobs(kind: string | undefined, ci: Exclude<CIProvider, "none">): string {
  if (kind === "docker") return `images built and pushed to the ${ci === "github" ? "GitHub" : "GitLab"} registry, deploys over SSH`
  if (kind === "kubernetes") return "renders validated with kubeconform, kubectl/Helm deploys with each environment's kubeconfig"
  return `a plan per environment on ${ci === "github" ? "pull" : "merge"} requests, applies from the default branch`
}

/** The optional CI/CD pipeline of the export, collapsed to one line until the user wants one. */
function PipelineSection({
  kind,
  value,
  onChange,
}: {
  kind: string | undefined
  value: ExportPipeline
  onChange: (value: ExportPipeline) => void
}) {
  const [open, setOpen] = useState(false)
  const ci = value.ci === "none" ? null : value.ci
  const file = ci === "github" ? ".github/workflows/<name>.yml" : ".gitlab-ci.yml"
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="rounded-md border">
      <CollapsibleTrigger className="group/ci flex w-full items-baseline gap-2 px-3 py-2 text-left">
        <ChevronRight className="size-3.5 self-center text-muted-foreground transition-transform group-data-[state=open]/ci:rotate-90" />
        <span className="shrink-0 text-sm font-medium">CI/CD pipeline</span>
        <span className="min-w-0 truncate text-xs text-muted-foreground">
          {ci ? `${CI_NAMES[ci]}${value.smoke ? " + smoke check" : ""}` : "Add GitHub Actions or GitLab CI to the export"}
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="grid gap-3 border-t px-3 py-3">
        <ToggleGroup
          type="single"
          variant="outline"
          size="sm"
          value={value.ci}
          onValueChange={(v) => v && onChange({ ...value, ci: v as CIProvider })}
          aria-label="CI/CD pipeline"
          className="justify-start"
        >
          <ToggleGroupItem value="none">None</ToggleGroupItem>
          <ToggleGroupItem value="github">GitHub Actions</ToggleGroupItem>
          <ToggleGroupItem value="gitlab">GitLab CI</ToggleGroupItem>
        </ToggleGroup>
        <p className="text-xs text-muted-foreground">
          {ci ? (
            <>
              Writes <code>{file}</code>: checks on every push, {pipelineJobs(kind, ci)}, one environment after the
              other with production last
              {ci === "github"
                ? " (add required reviewers to its GitHub environment to gate it)"
                : " (production-like environments wait for a manual start)"}
              . Its header and the README list the secrets and variables to set.
            </>
          ) : (
            "Only the project files are exported."
          )}
        </p>
        <div className="flex items-start gap-2">
          <Checkbox
            id="export-smoke"
            checked={value.smoke}
            disabled={!ci}
            onCheckedChange={(v) => onChange({ ...value, smoke: v === true })}
            className="mt-0.5"
          />
          <div className="grid gap-0.5">
            <Label htmlFor="export-smoke" className={ci ? undefined : "text-muted-foreground"}>
              Smoke check after deploy
            </Label>
            <p className="text-xs text-muted-foreground">
              Requests each environment&apos;s <code>SMOKE_URL</code> with retries. Load tests need Nimbo&apos;s daemon,
              so they stay in the app.
            </p>
          </div>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}

interface TreeEntry {
  name: string
  /** Files directly in a folder shown collapsed. */
  count?: number
  children?: TreeEntry[]
}

/** Folders up to two levels deep, deeper content counted: the shape of the export at a glance. */
function buildTree(files: WorkflowFile[]): TreeEntry[] {
  const root: TreeEntry = { name: "", children: [] }
  for (const f of files) {
    const parts = f.path.split("/")
    let node = root
    for (let i = 0; i < parts.length; i++) {
      if (i === 2) {
        // Deeper than modules/<name>/ or environments/<env>/: counted, not listed.
        node.count = (node.count ?? 0) + 1
        break
      }
      const last = i === parts.length - 1
      const name = last ? parts[i] : `${parts[i]}/`
      let child = node.children!.find((c) => c.name === name)
      if (!child) {
        child = { name, children: last ? undefined : [] }
        node.children!.push(child)
      }
      node = child
    }
  }
  const sort = (entries: TreeEntry[]) => {
    entries.sort((a, b) => Number(!a.children) - Number(!b.children) || a.name.localeCompare(b.name))
    for (const e of entries) if (e.children) sort(e.children)
  }
  sort(root.children!)
  return root.children!
}

function TreePreview({ files, loading, error }: { files?: WorkflowFile[]; loading: boolean; error: unknown }) {
  const tree = useMemo(() => buildTree(files ?? []), [files])
  if (error) return <p className="text-xs text-destructive">{errorMessage(error)}</p>
  if (loading && !files) return <Spinner className="size-4" />
  const lines: { text: string; dim?: boolean }[] = []
  const walk = (entries: TreeEntry[], prefix: string) => {
    entries.forEach((e, i) => {
      const last = i === entries.length - 1
      const count = e.count && (!e.children || e.children.length === 0) ? ` (${e.count} file${e.count === 1 ? "" : "s"})` : ""
      lines.push({ text: `${prefix}${last ? "└── " : "├── "}${e.name}${count}`, dim: !e.children })
      if (e.children && e.children.length > 0) walk(e.children, prefix + (last ? "    " : "│   "))
    })
  }
  walk(tree, "")
  return (
    <pre className="max-h-56 overflow-auto rounded-md border bg-muted/30 p-2 font-mono text-[11px] leading-4" aria-label="Exported files">
      {lines.map((l, i) => (
        <div key={i} className={l.dim ? "text-muted-foreground" : undefined}>
          {l.text}
        </div>
      ))}
    </pre>
  )
}
