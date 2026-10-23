import { lazy, Suspense, useState } from "react"
import { Check, Info, Square, X } from "lucide-react"
import { toast } from "sonner"
import { PixelProgress } from "@/components/arcade/pixel-progress"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { KubeWorkloadStatus, LoadTestRun, ThresholdResult } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { LoadResourcesDetail, LoadVerdictBanner } from "@/features/docker/perf/load-resources"
import { timeDomain } from "@/features/docker/perf/model"
import { relabelResources, workloadLabels } from "@/features/kubernetes/deploy/kube-model"
import { StatusBadge } from "../components/status-badge"
import { formatDuration, formatMs, formatNumber, formatPercent } from "../lib/format"
import { DENSE_TABLE } from "../variables/blocks-editors"
import { computeKpis, EXPLAIN, formatThreshold, METRIC_META, modeUnit, runDurationSec, stabilityLabel, totalDuration } from "./metrics"
import { useLoadRun } from "./use-load-run"

// recharts loads with the first run shown, not with the tab.
const LoadCharts = lazy(() => import("./charts").then((m) => ({ default: m.LoadCharts })))
const ResourceCharts = lazy(() => import("./charts").then((m) => ({ default: m.ResourceCharts })))

const TONE_TEXT = {
  ok: "text-emerald-600 dark:text-emerald-400",
  warn: "text-amber-600 dark:text-amber-400",
  bad: "text-destructive",
  idle: "text-muted-foreground",
}

function Kpi({
  label,
  value,
  sub,
  hint,
  tone,
}: {
  label: string
  value: string
  sub?: string
  hint: string
  tone?: keyof typeof TONE_TEXT
}) {
  return (
    <div data-slot="kpi" className="min-w-0 rounded-lg border px-2.5 py-1.5">
      <div className="flex items-center gap-1 text-[11px] text-muted-foreground">
        <span className="truncate">{label}</span>
        <Tooltip>
          <TooltipTrigger asChild>
            <Info className="size-3 shrink-0 cursor-help" />
          </TooltipTrigger>
          <TooltipContent className="max-w-64">{hint}</TooltipContent>
        </Tooltip>
      </div>
      <div className={cn("truncate text-base font-semibold tabular-nums", tone && TONE_TEXT[tone])}>{value}</div>
      {sub && <div className="truncate text-[10px] text-muted-foreground">{sub}</div>}
    </div>
  )
}


function formatActual(t: ThresholdResult): string {
  const unit = METRIC_META[t.metric].unit
  return unit === "ratio" ? formatPercent(t.actual) : unit === "ms" ? formatMs(t.actual) : `${formatNumber(t.actual, 1)} req/s`
}

function Breakdown({ title, counts, total }: { title: string; counts: Record<string, number>; total: number }) {
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1])
  return (
    <div className="min-w-0 rounded-lg border p-2">
      <div className="mb-1 text-[11px] font-medium text-muted-foreground">{title}</div>
      {entries.length === 0 ? (
        <p className="text-xs text-muted-foreground">None</p>
      ) : (
        <div className="flex flex-col gap-1">
          {entries.map(([k, n]) => (
            <div key={k} className="grid grid-cols-[6rem_1fr_auto] items-center gap-2 text-xs">
              <span
                className={cn(
                  "truncate font-mono",
                  /^[45]/.test(k) || title.startsWith("Error") ? "text-destructive" : undefined,
                )}
                title={k}
              >
                {k}
              </span>
              <div className="h-1.5 overflow-hidden rounded-full bg-muted">
                <div
                  data-meter
                  className={cn("h-full rounded-full", /^2/.test(k) ? "bg-emerald-500" : "bg-destructive")}
                  style={{ width: `${total ? (n / total) * 100 : 0}%` }}
                />
              </div>
              <span className="tabular-nums text-muted-foreground">{formatNumber(n)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

const NO_WORKLOADS: KubeWorkloadStatus[] = []

export function RunResults({ runId }: { runId: string }) {
  const { run, samples, loading } = useLoadRun(runId)
  const [stopping, setStopping] = useState(false)
  // Kubernetes: usage is keyed by workload node id; verdicts, peaks and charts show workload names.
  const kubeWorkloads = useWorkflowStore((s) => (s.workflow?.kind === "kubernetes" ? (s.kube?.workloads ?? NO_WORKLOADS) : undefined))

  if (!run) {
    return <div className="flex h-full items-center justify-center">{loading ? <Spinner /> : "Run not found."}</div>
  }

  const kpis = computeKpis(samples, run.summary)
  const running = run.status === "running"
  const unit = modeUnit(run.spec.profile.mode)
  const stability = stabilityLabel(kpis.stability)
  const planned = totalDuration(run.spec.profile.stages)
  const elapsed = runDurationSec(run)
  const resources = run.resources && kubeWorkloads ? relabelResources(run.resources, workloadLabels(kubeWorkloads)) : run.resources
  // Container usage shares the traffic charts' seconds axis.
  const domain = resources ? timeDomain(samples, resources) : undefined
  const chartFallback = (
    <div className="flex h-full items-center justify-center rounded-lg border">
      <Spinner />
    </div>
  )

  const stop = async () => {
    setStopping(true)
    try {
      await api.stopLoadTest(run.id)
    } catch (err) {
      toast.error("Could not stop the test", { description: errorMessage(err) })
    } finally {
      setStopping(false)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-2 overflow-auto p-2">
      <div className="flex flex-wrap items-center gap-2">
        <Verdict run={run} />
        <span className="text-sm font-semibold">{run.spec.name}</span>
        <span className="truncate text-xs text-muted-foreground">
          {run.spec.target.label} · {run.spec.profile.label ?? run.spec.profile.preset} ·{" "}
          {running ? `${formatDuration(elapsed * 1000)} of ${formatDuration(planned * 1000)}` : formatDuration(elapsed * 1000)}
        </span>
        {running && (
          <Button size="xs" variant="outline" className="ml-auto" disabled={stopping} onClick={() => void stop()}>
            {stopping ? <Spinner /> : <Square />} Stop
          </Button>
        )}
      </div>
      {running && <PixelProgress value={planned > 0 ? (elapsed / planned) * 100 : undefined} label="Load test progress" />}
      {run.error && <p className="text-xs text-destructive">{run.error}</p>}
      {resources && <LoadVerdictBanner resources={resources} />}

      <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 xl:grid-cols-8">
        <Kpi label="Requests" value={formatNumber(kpis.requests)} hint={EXPLAIN.requests} />
        <Kpi
          label={running ? "Current req/s" : "Peak req/s"}
          value={formatNumber(running ? kpis.currentRps : kpis.peakRps, 1)}
          hint={EXPLAIN.rps}
        />
        <Kpi label="Average req/s" value={formatNumber(kpis.avgRps, 1)} hint={EXPLAIN.rps} />
        <Kpi label={running ? "p95 (now)" : "p95 latency"} value={formatMs(kpis.p95)} sub={`p50 ${formatMs(kpis.p50)}`} hint={EXPLAIN.speed} />
        <Kpi label={running ? "p99 (now)" : "p99 latency"} value={formatMs(kpis.p99)} hint={EXPLAIN.speed} />
        <Kpi
          label="Error rate"
          value={formatPercent(kpis.errorRate)}
          sub={`${formatNumber(kpis.errors)} failed`}
          tone={kpis.errorRate > 0.05 ? "bad" : kpis.errorRate > 0 ? "warn" : "ok"}
          hint={EXPLAIN.errors}
        />
        <Kpi
          label="Stability"
          value={stability.label}
          sub={kpis.stability !== undefined ? `variation ${formatPercent(kpis.stability, 0)}` : undefined}
          tone={stability.tone}
          hint={EXPLAIN.stability}
        />
        <Kpi
          label="Breaking point"
          value={kpis.breakingPoint !== undefined ? `${formatNumber(kpis.breakingPoint)} ${unit}` : "Not reached"}
          tone={kpis.breakingPoint !== undefined ? "warn" : "ok"}
          hint={EXPLAIN.capacity}
        />
      </div>

      <div className="h-56 shrink-0">
        {samples.length === 0 ? (
          <div className="flex h-full items-center justify-center rounded-lg border text-xs text-muted-foreground">
            {running ? (
              <span className="flex items-center gap-2">
                <Spinner /> Warming up…
              </span>
            ) : (
              "No samples were recorded."
            )}
          </div>
        ) : (
          <Suspense fallback={chartFallback}>
            <LoadCharts samples={samples} mode={run.spec.profile.mode} domain={domain} />
          </Suspense>
        )}
      </div>

      {resources && resources.services.some((s) => s.timeline.length > 0) && (
        <div className="h-56 shrink-0">
          <Suspense fallback={chartFallback}>
            <ResourceCharts resources={resources} domain={domain} />
          </Suspense>
        </div>
      )}
      {resources && <LoadResourcesDetail resources={resources} />}

      {(run.thresholds?.length || run.summary) && (
        <div className="grid grid-cols-1 gap-2 lg:grid-cols-3">
          <div className="min-w-0 rounded-lg border">
            <div className="px-2 pt-2 text-[11px] font-medium text-muted-foreground">Pass criteria</div>
            <Table className={DENSE_TABLE}>
              <TableHeader>
                <TableRow>
                  <TableHead>Criterion</TableHead>
                  <TableHead>Actual</TableHead>
                  <TableHead className="w-12">Result</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(run.thresholds ?? run.spec.thresholds.map((t) => ({ ...t, actual: NaN, passed: false }))).map((t, i) => (
                  <TableRow key={i}>
                    <TableCell className="font-mono">{formatThreshold(t)}</TableCell>
                    <TableCell className="tabular-nums">{run.thresholds ? formatActual(t) : "—"}</TableCell>
                    <TableCell>
                      {run.thresholds &&
                        (t.passed ? (
                          <Check className="size-4 text-emerald-600 dark:text-emerald-400" aria-label="passed" />
                        ) : (
                          <X className="size-4 text-destructive" aria-label="failed" />
                        ))}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
          {run.summary && (
            <>
              {run.spec.target.kind === "http" && (
                <Breakdown title="Status codes" counts={run.summary.statusCodes ?? {}} total={run.summary.requests} />
              )}
              <Breakdown title="Error kinds" counts={run.summary.errorKinds ?? {}} total={run.summary.requests} />
            </>
          )}
        </div>
      )}
    </div>
  )
}

export function Verdict({ run }: { run: LoadTestRun }) {
  if (run.status === "running") return <StatusBadge status="running" label="Live" />
  return <StatusBadge status={run.status} label={run.status === "failed" ? "Failed criteria" : undefined} />
}
