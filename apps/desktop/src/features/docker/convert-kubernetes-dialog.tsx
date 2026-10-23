import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query"
import { AlertTriangleIcon, ArrowRightIcon, FileTextIcon, InfoIcon } from "lucide-react"
import { useEffect, useMemo, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { api } from "@/lib/api/client"
import type { ConvertKubernetesFormat, KubeConvertReport, Workflow } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { DNS_LABEL, suggestKubeName } from "./convert-kubernetes"
import { useWorkflowStore } from "@/state/workflow-store"

const FORMATS: { value: ConvertKubernetesFormat; label: string; description: string }[] = [
  { value: "manifests", label: "Manifests", description: "Plain YAML in k8s/: one file per service, applied as is." },
  { value: "kustomize", label: "Kustomize", description: "k8s/base plus overlays/dev and overlays/prod (2 replicas + PodDisruptionBudgets)." },
  { value: "helm", label: "Helm chart", description: "chart/ with values.yaml, values-dev.yaml and values-prod.yaml." },
]

/** Debounces a value (typing in the name/namespace fields). */
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value)
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms)
    return () => clearTimeout(t)
  }, [value, ms])
  return v
}

export function ConvertKubernetesDialog({
  workflow,
  open,
  onOpenChange,
}: {
  workflow: Workflow
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[88vh] flex-col sm:max-w-3xl" data-testid="convert-kubernetes-dialog">
        {open && <ConvertForm workflow={workflow} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

/** Mounted only while open: its state resets every time the dialog opens. */
function ConvertForm({ workflow, onDone }: { workflow: Workflow; onDone: () => void }) {
  const [format, setFormat] = useState<ConvertKubernetesFormat>("manifests")
  const [name, setName] = useState("")
  const [namespace, setNamespace] = useState("")
  const suggested = suggestKubeName(workflow.name)
  const effName = name.trim() || suggested
  const effNamespace = namespace.trim() || effName
  const nameError = DNS_LABEL.test(effName) ? "" : "Lower-case letters, digits and '-', starting with a letter."
  const nsError = DNS_LABEL.test(effNamespace) ? "" : "Lower-case letters, digits and '-', starting with a letter."
  const valid = !nameError && !nsError
  const body = useDebounced({ format, name: effName, namespace: effNamespace }, 300)

  const preview = useQuery({
    queryKey: ["convert-kubernetes", workflow.id, body],
    queryFn: () => api.convertToKubernetes(workflow.id, { ...body, dryRun: true }),
    enabled: DNS_LABEL.test(body.name) && DNS_LABEL.test(body.namespace),
    placeholderData: keepPreviousData,
    retry: false,
  })

  const create = useMutation({
    mutationFn: () => api.convertToKubernetes(workflow.id, { format, name: effName, namespace: effNamespace }),
    onSuccess: async (res) => {
      if (!res.workflow) return
      const warnings = res.report.unmapped.filter((u) => u.severity === "warning").length
      toast.success(`Created ${res.workflow.name}`, {
        description: warnings
          ? `${warnings} compose feature${warnings === 1 ? " wasn't" : "s weren't"} mapped (listed in the preview); review them before deploying.`
          : "Every compose feature was mapped. Deploy it on the local cluster from the toolbar.",
      })
      onDone()
      await useWorkflowStore.getState().open(res.workflow.id)
    },
    onError: (err) => toast.error("Couldn't convert the workflow", { description: errorMessage(err) }),
  })

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <TechIcon icon="docker" size={18} />
          <ArrowRightIcon className="size-4 text-muted-foreground" />
          <TechIcon icon="kubernetes" size={18} />
          Convert to Kubernetes
        </DialogTitle>
        <DialogDescription>
          Creates a new Kubernetes workflow from {workflow.name}'s compose services (this workflow stays as it is): workloads with probes,
          requests/limits and a non-root security context, Services named like the compose services, environment in ConfigMaps and
          Secrets, volumes as PersistentVolumeClaims, and an Ingress for the ports users reach. Images built here keep their Dockerfiles.
        </DialogDescription>
      </DialogHeader>

      <div className="flex flex-col gap-1.5">
        <Label>Format</Label>
        <ToggleGroup
          type="single"
          variant="outline"
          value={format}
          onValueChange={(v) => v && setFormat(v as ConvertKubernetesFormat)}
          aria-label="Format"
          className="w-full"
        >
          {FORMATS.map((f) => (
            <ToggleGroupItem key={f.value} value={f.value} className="flex-1" data-testid={`convert-format-${f.value}`}>
              {f.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
        <p className="text-xs text-muted-foreground">{FORMATS.find((f) => f.value === format)?.description}</p>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="convert-name">Application name</Label>
          <Input
            id="convert-name"
            value={name}
            placeholder={suggested}
            aria-invalid={!!nameError}
            onChange={(e) => setName(e.target.value)}
          />
          <p className={cn("text-xs", nameError ? "text-destructive" : "text-muted-foreground")}>
            {nameError || `Labels, ${format === "helm" ? "chart and release name, " : ""}hosts like api.${effName}.localhost`}
          </p>
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="convert-namespace">Namespace</Label>
          <Input
            id="convert-namespace"
            value={namespace}
            placeholder={effName}
            aria-invalid={!!nsError}
            onChange={(e) => setNamespace(e.target.value)}
          />
          <p className={cn("text-xs", nsError ? "text-destructive" : "text-muted-foreground")}>{nsError || "Where every object goes."}</p>
        </div>
      </div>

      <div className="min-h-0 flex-1">
        {preview.isError ? (
          <p className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">{errorMessage(preview.error)}</p>
        ) : !preview.data ? (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-8 w-full" />
            <Skeleton className="h-40 w-full" />
          </div>
        ) : (
          <ConvertPreview report={preview.data.report} files={preview.data.files ?? {}} stale={preview.isFetching} />
        )}
      </div>

      <DialogFooter>
        <Button variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button
          disabled={!valid || create.isPending || preview.isError}
          onClick={() => create.mutate()}
          data-testid="convert-kubernetes-create"
        >
          {create.isPending ? "Converting…" : "Create Kubernetes workflow"}
        </Button>
      </DialogFooter>
    </>
  )
}

function ConvertPreview({ report, files, stale }: { report: KubeConvertReport; files: Record<string, string>; stale: boolean }) {
  const warnings = report.unmapped.filter((u) => u.severity === "warning")
  const notes = report.unmapped.filter((u) => u.severity !== "warning")
  const byService = useMemo(() => {
    const m = new Map<string, KubeConvertReport["objects"]>()
    for (const o of report.objects) {
      const k = o.service ?? ""
      m.set(k, [...(m.get(k) ?? []), o])
    }
    return [...m.entries()].sort(([a], [b]) => (a === "" ? 1 : b === "" ? -1 : a.localeCompare(b)))
  }, [report.objects])
  const paths = Object.keys(files).sort()
  const [file, setFile] = useState<string | null>(null)
  const shown = file && files[file] !== undefined ? file : paths[0]

  return (
    <Tabs defaultValue={warnings.length ? "unmapped" : "objects"} className={cn("flex h-full min-h-0 flex-col", stale && "opacity-70")}>
      <TabsList>
        <TabsTrigger value="objects">Objects ({report.objects.length})</TabsTrigger>
        <TabsTrigger value="mappings">Mapping ({report.mappings.length})</TabsTrigger>
        <TabsTrigger value="unmapped" data-testid="convert-unmapped-tab">
          Couldn't map ({warnings.length}
          {notes.length ? ` + ${notes.length} notes` : ""})
        </TabsTrigger>
        <TabsTrigger value="files">Files ({paths.length})</TabsTrigger>
      </TabsList>

      <TabsContent value="objects" className="min-h-0">
        <ScrollArea className="h-64 rounded-md border">
          <div className="flex flex-col gap-3 p-3 text-sm">
            {report.builds.length > 0 && (
              <p className="text-xs text-muted-foreground">
                Built locally and loaded into the cluster: {report.builds.map((b) => `${b.image} (${b.context})`).join(", ")}
                {report.environments.length > 0 && ` · Environments: ${report.environments.join(", ")}`}
              </p>
            )}
            {byService.map(([svc, objs]) => (
              <div key={svc || "_shared"} className="flex flex-col gap-1">
                <span className="text-xs font-medium text-muted-foreground">{svc ? `Service ${svc}` : "Shared"}</span>
                <div className="flex flex-wrap gap-1.5">
                  {objs.map((o) => (
                    <Badge key={`${o.kind}/${o.name}/${o.file}`} variant="secondary" title={o.file}>
                      {o.kind} {o.name}
                    </Badge>
                  ))}
                </div>
              </div>
            ))}
          </div>
        </ScrollArea>
      </TabsContent>

      <TabsContent value="mappings" className="min-h-0">
        <ScrollArea className="h-64 rounded-md border">
          <table className="w-full text-left text-xs">
            <thead className="sticky top-0 bg-background text-muted-foreground">
              <tr>
                <th className="px-3 py-1.5 font-medium">Service</th>
                <th className="px-3 py-1.5 font-medium">Compose</th>
                <th className="px-3 py-1.5 font-medium">Kubernetes</th>
              </tr>
            </thead>
            <tbody>
              {report.mappings.map((m, i) => (
                <tr key={i} className="border-t align-top">
                  <td className="px-3 py-1.5 font-medium">{m.service}</td>
                  <td className="px-3 py-1.5 font-mono break-all">{m.from}</td>
                  <td className="px-3 py-1.5">{m.to}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </ScrollArea>
      </TabsContent>

      <TabsContent value="unmapped" className="min-h-0">
        <ScrollArea className="h-64 rounded-md border">
          {report.unmapped.length === 0 ? (
            <p className="p-3 text-sm text-muted-foreground">Every compose feature was mapped.</p>
          ) : (
            <ul className="flex flex-col divide-y text-sm" data-testid="convert-unmapped-list">
              {[...warnings, ...notes].map((u, i) => (
                <li key={i} className="flex gap-2 px-3 py-2">
                  {u.severity === "warning" ? (
                    <AlertTriangleIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
                  ) : (
                    <InfoIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                  )}
                  <div className="flex min-w-0 flex-col gap-0.5">
                    <span className="font-medium">
                      {u.service ? `${u.service} · ` : ""}
                      <span className="font-mono">{u.feature}</span>
                      {u.file && (
                        <span className="ml-2 text-xs font-normal text-muted-foreground">
                          {u.file}
                          {u.line ? `:${u.line}` : ""}
                        </span>
                      )}
                    </span>
                    <span className="text-xs text-muted-foreground">{u.detail}</span>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </ScrollArea>
      </TabsContent>

      <TabsContent value="files" className="min-h-0">
        <div className="grid h-64 grid-cols-[14rem_1fr] overflow-hidden rounded-md border">
          <ScrollArea className="border-r">
            <ul className="flex flex-col p-1 text-xs">
              {paths.map((p) => (
                <li key={p}>
                  <button
                    type="button"
                    onClick={() => setFile(p)}
                    className={cn(
                      "flex w-full items-center gap-1.5 rounded px-2 py-1 text-left font-mono hover:bg-muted",
                      p === shown && "bg-muted",
                    )}
                  >
                    <FileTextIcon className="size-3.5 shrink-0 text-muted-foreground" />
                    <span className="truncate">{p}</span>
                  </button>
                </li>
              ))}
            </ul>
          </ScrollArea>
          <ScrollArea>
            <pre className="p-3 font-mono text-xs whitespace-pre">{shown ? files[shown] : ""}</pre>
          </ScrollArea>
        </div>
      </TabsContent>
    </Tabs>
  )
}
