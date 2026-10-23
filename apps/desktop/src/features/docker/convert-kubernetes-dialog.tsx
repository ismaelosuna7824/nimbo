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
import { Switch } from "@/components/ui/switch"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { api } from "@/lib/api/client"
import type { ConvertKubernetesFormat, ConvertKubernetesRequest, KubeConvertLoadTest, KubeConvertReport, KubeSizingSource, Workflow } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { DNS_LABEL, MAX_REPLICAS, TARGET_CPU, autoscalingError, describeLoadTest, formatDescription, sizingNote, suggestKubeName } from "./convert-kubernetes"
import { useWorkflowStore } from "@/state/workflow-store"

const FORMATS: { value: ConvertKubernetesFormat; label: string }[] = [
  { value: "manifests", label: "Manifests" },
  { value: "kustomize", label: "Kustomize" },
  { value: "helm", label: "Helm chart" },
]

const SOURCES: Record<KubeSizingSource, string> = { load_test: "Load test", compose: "Compose", default: "Default" }

/** Preview tabs shrink with the dialog on short windows (16rem, at least 10rem: the form scrolls instead). */
const TAB = "min-h-0 flex-col data-[state=active]:flex"
const PANE = "h-64 min-h-40 rounded-md border"

/** Debounces a value (typing in the name/namespace fields); compare by value (strings). */
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
  const [format, setFormat] = useState<ConvertKubernetesFormat>("kustomize")
  const [name, setName] = useState("")
  const [namespace, setNamespace] = useState("")
  const [sizeFromLoadTest, setSizeFromLoadTest] = useState(true)
  const [autoscale, setAutoscale] = useState(true)
  const [maxReplicas, setMaxReplicas] = useState("5")
  const [targetCpu, setTargetCpu] = useState("70")
  const suggested = suggestKubeName(workflow.name)
  const environments = (workflow.compose?.environments ?? []).map((e) => e.name)
  const effName = name.trim() || suggested
  const effNamespace = namespace.trim() || effName
  const nameError = DNS_LABEL.test(effName) ? "" : "Lower-case letters, digits and '-', starting with a letter."
  const nsError = DNS_LABEL.test(effNamespace) ? "" : "Lower-case letters, digits and '-', starting with a letter."
  const hpaError = autoscale ? autoscalingError(Number(maxReplicas), Number(targetCpu)) : ""
  const valid = !nameError && !nsError && !hpaError
  const request: ConvertKubernetesRequest = {
    format,
    name: effName,
    namespace: effNamespace,
    sizeFromLoadTest,
    autoscaling: autoscale ? { enabled: true, maxReplicas: Number(maxReplicas), targetCpu: Number(targetCpu) } : { enabled: false },
  }
  const debounced = useDebounced(JSON.stringify(request), 300)
  const body = useMemo(() => JSON.parse(debounced) as ConvertKubernetesRequest, [debounced])
  const bodyHpa = body.autoscaling?.enabled ? autoscalingError(body.autoscaling.maxReplicas ?? 0, body.autoscaling.targetCpu ?? 0) : ""

  const preview = useQuery({
    queryKey: ["convert-kubernetes", workflow.id, body],
    queryFn: () => api.convertToKubernetes(workflow.id, { ...body, dryRun: true }),
    enabled: DNS_LABEL.test(body.name ?? "") && DNS_LABEL.test(body.namespace ?? "") && !bodyHpa,
    placeholderData: keepPreviousData,
    retry: false,
  })
  const loadTest = preview.data?.report.loadTest

  const create = useMutation({
    mutationFn: () => api.convertToKubernetes(workflow.id, request),
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
          Creates a new Kubernetes workflow from {workflow.name}'s compose services (this one stays as it is): workloads with probes,
          sized requests/limits, Services, ConfigMaps, Secrets, PersistentVolumeClaims and an Ingress.
        </DialogDescription>
      </DialogHeader>

      {/* Short windows scroll the form; the preview keeps a usable height. */}
      <div className="-mx-4 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4">
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
          <p className="text-xs text-muted-foreground" data-testid="convert-format-description">
            {formatDescription(format, environments)}
          </p>
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

        <div className="grid grid-cols-2 gap-3">
          <SizingControl
            loadTest={loadTest}
            loading={!preview.data && !preview.isError}
            checked={sizeFromLoadTest}
            onCheckedChange={setSizeFromLoadTest}
          />
          <AutoscalingControl
            manifests={format === "manifests"}
            enabled={autoscale}
            onEnabledChange={setAutoscale}
            maxReplicas={maxReplicas}
            onMaxReplicasChange={setMaxReplicas}
            targetCpu={targetCpu}
            onTargetCpuChange={setTargetCpu}
            error={hpaError}
          />
        </div>

        <div className="flex min-h-0 flex-1 flex-col">
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

/** Whether requests/limits come from the workflow's last load test (toggle), or why they can't. */
function SizingControl({
  loadTest,
  loading,
  checked,
  onCheckedChange,
}: {
  loadTest?: KubeConvertLoadTest
  loading: boolean
  checked: boolean
  onCheckedChange: (v: boolean) => void
}) {
  return (
    <div className="flex flex-col gap-1.5 rounded-md border p-2.5" data-testid="convert-sizing">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor="convert-size-from-load-test">Size from load test</Label>
        <Switch
          id="convert-size-from-load-test"
          size="sm"
          checked={!!loadTest && checked}
          disabled={!loadTest}
          onCheckedChange={onCheckedChange}
        />
      </div>
      <p className="text-xs text-muted-foreground">
        {loading ? (
          "Looking for a load test…"
        ) : loadTest ? (
          <>
            <span className="font-medium text-foreground">{describeLoadTest(loadTest)}</span>
            {loadTest.target ? ` against ${loadTest.target}` : ""}: requests/limits from measured usage; explicit compose values win.
          </>
        ) : (
          "No load test yet: compose values and defaults. Run one in the Load test panel to size from real usage."
        )}
      </p>
    </div>
  )
}

/** Prod HorizontalPodAutoscalers: on/off, max replicas and the CPU target (Kustomize and Helm only). */
function AutoscalingControl({
  manifests,
  enabled,
  onEnabledChange,
  maxReplicas,
  onMaxReplicasChange,
  targetCpu,
  onTargetCpuChange,
  error,
}: {
  manifests: boolean
  enabled: boolean
  onEnabledChange: (v: boolean) => void
  maxReplicas: string
  onMaxReplicasChange: (v: string) => void
  targetCpu: string
  onTargetCpuChange: (v: string) => void
  error: string
}) {
  return (
    <div className="flex flex-col gap-1.5 rounded-md border p-2.5" data-testid="convert-autoscaling">
      <div className="flex items-center justify-between gap-2">
        <Label htmlFor="convert-autoscaling">Autoscaling in prod</Label>
        <Switch id="convert-autoscaling" size="sm" checked={enabled && !manifests} disabled={manifests} onCheckedChange={onEnabledChange} />
      </div>
      {manifests ? (
        <p className="text-xs text-muted-foreground">Kustomize or Helm only: plain manifests have no prod environment.</p>
      ) : (
        <>
          <div className={cn("flex items-center gap-2 text-xs", !enabled && "opacity-50")}>
            <Label htmlFor="convert-max-replicas" className="text-xs font-normal text-muted-foreground">
              Max replicas
            </Label>
            <Input
              id="convert-max-replicas"
              type="number"
              min={MAX_REPLICAS.min}
              max={MAX_REPLICAS.max}
              value={maxReplicas}
              disabled={!enabled}
              aria-invalid={error.startsWith("Max")}
              onChange={(e) => onMaxReplicasChange(e.target.value)}
              className="h-7 w-16"
            />
            <Label htmlFor="convert-target-cpu" className="ml-1 text-xs font-normal text-muted-foreground">
              Target CPU
            </Label>
            <Input
              id="convert-target-cpu"
              type="number"
              min={TARGET_CPU.min}
              max={TARGET_CPU.max}
              value={targetCpu}
              disabled={!enabled}
              aria-invalid={error.startsWith("Target")}
              onChange={(e) => onTargetCpuChange(e.target.value)}
              className="h-7 w-16"
            />
            <span className="text-muted-foreground">%</span>
          </div>
          <p className={cn("text-xs", error ? "text-destructive" : "text-muted-foreground")}>
            {error || (enabled ? "HPA per stateless service, from 2 replicas; % of the CPU request." : "Fixed replicas in prod.")}
          </p>
        </>
      )}
    </div>
  )
}

/** Per-service requests/limits, where they come from, and the prod autoscaler. */
function SizingTable({ report }: { report: KubeConvertReport }) {
  const hpa = new Map(report.autoscaling.map((h) => [h.service, h]))
  return (
    <table className="w-full text-left text-xs" data-testid="convert-sizing-table">
      <thead className="sticky top-0 bg-background text-muted-foreground">
        <tr>
          <th className="px-3 py-1.5 font-medium">Service</th>
          <th className="px-3 py-1.5 font-medium">Requests</th>
          <th className="px-3 py-1.5 font-medium">Limits</th>
          <th className="px-3 py-1.5 font-medium">Source</th>
          <th className="px-3 py-1.5 font-medium">Prod replicas</th>
        </tr>
      </thead>
      <tbody>
        {report.sizing.map((s) => {
          const h = hpa.get(s.service)
          return (
            <tr key={s.service} className="border-t align-top">
              <td className="max-w-64 px-3 py-1.5">
                <div className="font-medium">{s.service}</div>
                <div className="truncate text-muted-foreground" title={s.detail}>
                  {sizingNote(s)}
                </div>
              </td>
              <td className="px-3 py-1.5 font-mono whitespace-nowrap">
                {s.requests.cpu} / {s.requests.memory}
              </td>
              <td className="px-3 py-1.5 font-mono whitespace-nowrap">
                {s.limits.cpu} / {s.limits.memory}
              </td>
              <td className="px-3 py-1.5">
                <span className="flex items-center gap-1">
                  <Badge variant={s.source === "load_test" ? "default" : "secondary"}>{SOURCES[s.source]}</Badge>
                  {s.censored && (
                    <span title={`Hit its Docker ${s.censored === "memory" ? "memory" : "CPU"} limit during the test: sized above what it measured.`}>
                      <AlertTriangleIcon className="size-3.5 text-amber-500" aria-label="Limited during the test" />
                    </span>
                  )}
                </span>
              </td>
              <td className="px-3 py-1.5 whitespace-nowrap text-muted-foreground">
                {h ? `HPA ${h.minReplicas}–${h.maxReplicas} at ${h.targetCpu}% CPU` : "—"}
              </td>
            </tr>
          )
        })}
      </tbody>
    </table>
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
    <Tabs defaultValue={warnings.length ? "unmapped" : "sizing"} className={cn("flex min-h-0 flex-1 flex-col", stale && "opacity-70")}>
      <TabsList>
        <TabsTrigger value="sizing" data-testid="convert-sizing-tab">
          Sizing ({report.sizing.length})
        </TabsTrigger>
        <TabsTrigger value="objects">Objects ({report.objects.length})</TabsTrigger>
        <TabsTrigger value="mappings">Mapping ({report.mappings.length})</TabsTrigger>
        <TabsTrigger value="unmapped" data-testid="convert-unmapped-tab">
          Couldn't map ({warnings.length}
          {notes.length ? ` + ${notes.length} notes` : ""})
        </TabsTrigger>
        <TabsTrigger value="files">Files ({paths.length})</TabsTrigger>
      </TabsList>

      <TabsContent value="sizing" className={TAB}>
        <ScrollArea className={PANE}>
          <SizingTable report={report} />
        </ScrollArea>
      </TabsContent>

      <TabsContent value="objects" className={TAB}>
        <ScrollArea className={PANE}>
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

      <TabsContent value="mappings" className={TAB}>
        <ScrollArea className={PANE}>
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

      <TabsContent value="unmapped" className={TAB}>
        <ScrollArea className={PANE}>
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

      <TabsContent value="files" className={TAB}>
        <div className="grid h-64 min-h-40 grid-cols-[14rem_1fr] overflow-hidden rounded-md border">
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
