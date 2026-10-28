import { useMutation, useQuery } from "@tanstack/react-query"
import { BellRingIcon, CloudUploadIcon, ExternalLinkIcon, EyeIcon, EyeOffIcon, InfoIcon, RadarIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { openExternal } from "@/components/open-external"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { CopyButton } from "@/features/settings/common"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { api } from "@/lib/api/client"
import type { MonitoringSummary, MonitoringThresholds, Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { HTTP_SOURCES, alertsByTarget, formatCost, httpCoverage } from "./monitoring"
import { hasMonitoring, useMonitoringDialog } from "./monitoring-store"

const ICONS: Record<string, string> = {
  prometheus: "prometheus",
  grafana: "grafana",
  cadvisor: "cadvisor",
  "kube-prometheus-stack": "prometheus",
}

/** Add monitoring: previews what gets added and its cost, applies it, then shows the URLs and Grafana's credentials. */
export function MonitoringDialog({ workflow }: { workflow: Workflow }) {
  const open = useMonitoringDialog((s) => s.open)
  const setOpen = useMonitoringDialog((s) => s.setOpen)
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="flex max-h-[88vh] flex-col sm:max-w-2xl" data-testid="monitoring-dialog">
        {open && <MonitoringBody workflow={workflow} onDone={() => setOpen(false)} />}
      </DialogContent>
    </Dialog>
  )
}

/** Mounted only while open: the preview refreshes every time the dialog opens. */
function MonitoringBody({ workflow, onDone }: { workflow: Workflow; onDone: () => void }) {
  const kube = workflow.kind === "kubernetes"
  const added = hasMonitoring(workflow)
  const busy = useRunBusy()
  const version = useWorkflowStore((s) => s.version)
  const preview = useQuery({
    queryKey: ["monitoring", workflow.id, version],
    queryFn: () => api.monitoring(workflow.id),
    retry: false,
  })
  // Threshold edits (empty = keep the current value).
  const [thresholds, setThresholds] = useState<Partial<MonitoringThresholds>>({})
  const apply = useMutation({
    mutationFn: () => api.addMonitoring(workflow.id, Object.keys(thresholds).length ? { thresholds } : undefined),
    onSuccess: (res) => {
      useWorkflowStore.getState().setWorkflow(res.workflow)
      toast.success(added ? "Monitoring updated" : "Monitoring added", { description: "Deploy to start it." })
    },
    onError: (err) => toast.error(added ? "Couldn't update monitoring" : "Couldn't add monitoring", { description: errorMessage(err) }),
  })
  const done = apply.data?.summary
  const summary = done ?? preview.data
  const title = done ? (added ? "Monitoring updated" : "Monitoring added") : added ? "Update monitoring" : "Add monitoring"

  return (
    <>
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <TechIcon icon="prometheus" size={18} />
          <TechIcon icon="grafana" size={18} />
          {title}
        </DialogTitle>
        <DialogDescription>
          {kube
            ? "Metrics, alerts and dashboards for this workflow's workloads: the kube-prometheus-stack chart in the monitoring namespace, plus alerts, scrape targets and a dashboard generated from your workloads."
            : "Metrics, alerts and dashboards for every service: Prometheus, Grafana and cAdvisor in their own compose file (monitoring/compose.yaml); your services stay as they are. cAdvisor already measures every container (CPU, memory, network, restarts) for the container alerts and the Containers dashboard."}
          {added &&
            !done &&
            (kube
              ? " It's already here: updating regenerates the alerts, scrape targets and dashboard from the current workloads and keeps the chart values and Grafana password."
              : " It's already here: updating regenerates its files from the current services and keeps the Grafana password and ports.")}
        </DialogDescription>
      </DialogHeader>

      <div className="-mx-4 flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 text-sm">
        {preview.isPending && !done && (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-14" />
            <Skeleton className="h-14" />
            <Skeleton className="h-24" />
          </div>
        )}
        {preview.isError && !done && <p className="text-destructive">{errorMessage(preview.error)}</p>}
        {summary &&
          (done ? <Result summary={summary} /> : <Preview summary={summary} thresholds={thresholds} onThresholds={setThresholds} />)}
      </div>

      <DialogFooter>
        {done ? (
          <>
            <Button variant="outline" onClick={onDone}>
              Close
            </Button>
            <Button
              disabled={busy}
              onClick={() => {
                onDone()
                void runAction("apply")
              }}
            >
              <CloudUploadIcon />
              Deploy
            </Button>
          </>
        ) : (
          <>
            <Button variant="outline" onClick={onDone}>
              Cancel
            </Button>
            <Button disabled={!preview.data || apply.isPending} onClick={() => apply.mutate()} data-testid="monitoring-apply">
              {apply.isPending ? <Spinner /> : <RadarIcon />}
              {added ? "Update monitoring" : "Add monitoring"}
            </Button>
          </>
        )}
      </DialogFooter>
    </>
  )
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-1.5">
      <h3 className="text-xs font-medium text-muted-foreground">{title}</h3>
      {children}
    </section>
  )
}

function Preview({
  summary,
  thresholds,
  onThresholds,
}: {
  summary: MonitoringSummary
  thresholds: Partial<MonitoringThresholds>
  onThresholds: (t: Partial<MonitoringThresholds>) => void
}) {
  const groups = alertsByTarget(summary.alerts)
  return (
    <>
      <Section title="What gets added">
        <ul className="flex flex-col divide-y rounded-md border">
          {summary.components.map((c) => (
            <li key={c.name} className="flex items-start gap-2.5 px-3 py-2">
              {ICONS[c.name] ? <TechIcon icon={ICONS[c.name]} size={16} className="mt-0.5" /> : <BellRingIcon className="mt-0.5 size-4 text-muted-foreground" />}
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="font-medium">{c.name}</span>
                  <Badge variant="outline" className="font-mono text-[10px]">
                    {c.image}
                  </Badge>
                </div>
                <p className="text-xs text-muted-foreground">{c.purpose}</p>
              </div>
            </li>
          ))}
        </ul>
      </Section>
      <Section title="Resource cost">
        <p>
          {formatCost(summary.cost)}{" "}
          <span className="text-muted-foreground">{summary.kind === "kubernetes" ? "requested on the cluster" : "reserved on Docker"}</span>
        </p>
      </Section>
      <Section title={`Alert rules (${summary.alerts.length})`}>
        <p className="text-[11px] text-muted-foreground">
          Rules Prometheus evaluates, not alerts firing now: each one fires only when its condition holds (e.g. ContainerDown when a
          container stops). Live alerts are in Prometheus and Grafana.
        </p>
        <ul className="flex flex-col gap-1">
          {groups.map((g) => (
            <li key={g.target} className="flex flex-wrap items-center gap-1">
              <span className="mr-1 font-mono text-xs">{g.target}</span>
              {g.alerts.map((a) => (
                <Badge key={a.name} variant="outline" className="gap-1 text-[10px]" title={`${a.severity}: ${a.summary}`}>
                  {a.name}
                  {a.severity === "critical" && <span className="text-[9px] font-normal text-muted-foreground uppercase">critical</span>}
                </Badge>
              ))}
            </li>
          ))}
        </ul>
      </Section>
      <Section title="Application metrics">
        {summary.scrapes.length > 0 ? (
          <ul className="flex flex-col gap-0.5 font-mono text-xs">
            {summary.scrapes.map((s) => (
              <li key={`${s.target}:${s.port}`}>
                {s.target}:{s.port}
                {s.path} <span className="font-sans text-muted-foreground">({s.via})</span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">
            {summary.kind === "kubernetes"
              ? "None declared: container metrics only. Name a Service port metrics (or annotate it prometheus.io/scrape) to scrape your app."
              : 'None declared: cAdvisor still covers every container (CPU, memory, network, restarts). For HTTP 5xx and p95 alerts, serve HTTP metrics on a port, label the service prometheus.io/scrape: "true" and prometheus.io/port (prometheus.io/path, default /metrics), then Update monitoring.'}
          </p>
        )}
      </Section>
      <HttpSection summary={summary} thresholds={thresholds} onThresholds={onThresholds} />
      <Links summary={summary} />
      <Notes notes={summary.notes} />
    </>
  )
}

const THRESHOLD_FIELDS: { key: keyof MonitoringThresholds; label: string; unit: string; step: string }[] = [
  { key: "errorPercent", label: "5xx above", unit: "% for 5m", step: "0.5" },
  { key: "latencyP95Ms", label: "p95 above", unit: "ms for 10m", step: "50" },
  { key: "minRequestsPerSecond", label: "only above", unit: "req/s", step: "0.1" },
]

/** Which request metrics each workload's HTTP alerts read, and the thresholds (editable before applying). */
function HttpSection({
  summary,
  thresholds,
  onThresholds,
}: {
  summary: MonitoringSummary
  thresholds: Partial<MonitoringThresholds>
  onThresholds: (t: Partial<MonitoringThresholds>) => void
}) {
  const cov = httpCoverage(summary.http, summary.kind)
  return (
    <Section title={`HTTP alerts (${cov.measured} of ${summary.http.length} ${summary.kind === "kubernetes" ? "workloads" : "services"})`}>
      <ul className="flex flex-col gap-1" data-testid="monitoring-http">
        {summary.http.map((h) => (
          <li key={h.target} className="flex flex-wrap items-center gap-1">
            <span className="mr-1 font-mono text-xs">{h.target}</span>
            {h.sources.length ? (
              h.sources.map((s) => (
                <Badge key={s} variant="secondary" className="text-[10px]" title={HTTP_SOURCES[s].hint}>
                  {HTTP_SOURCES[s].label}
                </Badge>
              ))
            ) : (
              <span className="text-xs text-muted-foreground">no request metrics</span>
            )}
          </li>
        ))}
      </ul>
      <div className="flex flex-wrap items-end gap-3">
        {THRESHOLD_FIELDS.map((f) => (
          <div key={f.key} className="flex flex-col gap-1">
            <Label htmlFor={`threshold-${f.key}`} className="text-xs font-normal text-muted-foreground">
              {f.label}
            </Label>
            <div className="flex items-center gap-1.5">
              <Input
                id={`threshold-${f.key}`}
                type="number"
                min="0"
                step={f.step}
                className="h-7 w-20"
                placeholder={String(summary.thresholds[f.key])}
                value={thresholds[f.key] ?? ""}
                onChange={(e) => {
                  const next = { ...thresholds }
                  const v = Number(e.target.value)
                  if (e.target.value === "" || !Number.isFinite(v)) delete next[f.key]
                  else next[f.key] = v
                  onThresholds(next)
                }}
              />
              <span className="text-xs text-muted-foreground">{f.unit}</span>
            </div>
          </div>
        ))}
      </div>
      {cov.notes.map((n) => (
        <p key={n} className="text-xs text-muted-foreground">
          {n}
        </p>
      ))}
    </Section>
  )
}

function Result({ summary }: { summary: MonitoringSummary }) {
  return (
    <>
      <p>
        {summary.alerts.length} alerts, {summary.scrapes.length} application metrics endpoint{summary.scrapes.length === 1 ? "" : "s"} and the{" "}
        {summary.dashboards.join(", ")} dashboard{summary.dashboards.length === 1 ? "" : "s"}. Deploy to start them.
      </p>
      <Links summary={summary} />
      <Section title={`Files (${summary.files.length})`}>
        <ul className="flex flex-col gap-0.5 font-mono text-xs text-muted-foreground">
          {summary.files.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      </Section>
      <Notes notes={summary.notes} />
    </>
  )
}

function Links({ summary }: { summary: MonitoringSummary }) {
  const cred = summary.credentials
  return (
    <Section title="Open">
      <div className="flex flex-col gap-1.5 rounded-md border p-3">
        {summary.links.map((l) => (
          <div key={l.name} className="flex items-center gap-2">
            <span className="w-24 font-medium">{l.name}</span>
            {l.running && l.url ? (
              <Button variant="link" size="sm" className="h-auto p-0 font-mono text-xs" onClick={() => void openExternal(l.url!)}>
                {l.url}
                <ExternalLinkIcon />
              </Button>
            ) : (
              <span className="text-xs text-muted-foreground">{l.url ? `${l.url} after Deploy` : "after Deploy (listed in Endpoints)"}</span>
            )}
          </div>
        ))}
        {cred && (
          <div className="flex items-center gap-2 border-t pt-1.5">
            <span className="w-24 font-medium">Grafana login</span>
            <span className="font-mono text-xs">{cred.user}</span>
            {cred.password ? <Secret value={cred.password} /> : <span className="text-xs text-muted-foreground">password generated when added</span>}
            <span className="ml-auto truncate text-xs text-muted-foreground" title={cred.source}>
              {cred.source}
            </span>
          </div>
        )}
      </div>
    </Section>
  )
}

function Secret({ value }: { value: string }) {
  const [shown, setShown] = useState(false)
  return (
    <span className="flex items-center gap-0.5">
      <span className="font-mono text-xs">{shown ? value : "••••••••"}</span>
      <Button variant="ghost" size="icon-xs" aria-label={shown ? "Hide password" : "Show password"} onClick={() => setShown(!shown)}>
        {shown ? <EyeOffIcon /> : <EyeIcon />}
      </Button>
      <CopyButton text={value} label="Copy password" />
    </span>
  )
}

function Notes({ notes }: { notes?: string[] }) {
  if (!notes?.length) return null
  return (
    <ul className="flex flex-col gap-1">
      {notes.map((n) => (
        <li key={n} className="flex gap-1.5 text-xs text-muted-foreground">
          <InfoIcon className="mt-0.5 size-3.5 shrink-0" />
          {n}
        </li>
      ))}
    </ul>
  )
}
