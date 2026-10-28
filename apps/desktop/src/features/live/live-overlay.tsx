import { useQuery } from "@tanstack/react-query"
import { Panel } from "@xyflow/react"
import { ActivityIcon, FlameIcon, SquareIcon, XIcon } from "lucide-react"
import { memo, useEffect, useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { LiveTraffic, LoadSample, LoadTestRun, WorkflowKind } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { openMonitoringDialog } from "@/features/monitoring/monitoring-store"
import { StatusBadge } from "@/features/panels/components/status-badge"
import { useNow } from "@/features/panels/lib/use-now"
import { revealLoadTest } from "@/features/panels/loadtest/reveal"
import { SCENARIO_LABEL } from "@/features/panels/loadtest/scenario-result"
import { EMPTY_LIVE, stopRun, useLiveStore } from "./live-store"
import { useRunClock } from "./run-clock"
import {
  entryPath,
  faultLive,
  faultText,
  measuredEdges,
  measuredLive,
  MIN_RPS,
  shareRecord,
  targetLive,
  trafficLine,
  type EdgeLive,
  type FaultLive,
  type FlowEdge,
  type NodeLive,
} from "./traffic"

/** Prometheus scrapes every 15 s: polling faster shows the same numbers. */
const POLL_MS = 5000
const NO_SAMPLES: LoadSample[] = []

const TONE_TEXT = { ok: "text-foreground", warning: "text-amber-700 dark:text-amber-400", error: "text-destructive" }

/** A node id as people read it: "service:db" → "db", "Deployment/shop/api" → "api". */
function nodeName(id: string): string {
  return id.slice(Math.max(id.lastIndexOf(":"), id.lastIndexOf("/")) + 1)
}

/**
 * Live traffic of the workflow's load test on its canvas (drawn inside the canvas' React Flow): feeds the live store
 * the cards and edges read, polls the workflow's Prometheus while the run lasts, tells the verdict of a failure test
 * when it ends, and shows the run's numbers, its incident and what is measured in a panel at the bottom.
 * `edges` are the canvas' edges (caller → callee, or along the route).
 */
export const LiveTrafficOverlay = memo(function LiveTrafficOverlay({ kind, edges }: { kind: WorkflowKind; edges: FlowEdge[] }) {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const lingering = useRunClock((s) => s.lingering)
  // The running run, else one that ended moments ago (its final numbers).
  const run = useWorkflowStore((s) => s.loadTests.find((r) => r.status === "running") ?? s.loadTests.find((r) => lingering[r.id]))
  const samples = useWorkflowStore((s) => (run ? s.loadSamples[run.id] : undefined)) ?? NO_SAMPLES
  const start = useRunClock((s) => (run ? s.loadStart[run.id] : undefined))
  const running = run?.status === "running"
  const [dismissed, setDismissed] = useState<string | null>(null)
  const shown = !!run && dismissed !== run.id

  const traffic = useQuery<LiveTraffic>({
    queryKey: ["traffic", workflowId, run?.id],
    queryFn: () => api.traffic(workflowId!),
    enabled: kind !== "" && !!workflowId && running && shown,
    refetchInterval: (q) => (q.state.data?.status === "no_monitoring" ? false : POLL_MS),
  }).data

  const live = useMemo(() => {
    const nodes: Record<string, NodeLive> = {}
    const flows: Record<string, EdgeLive> = {}
    if (!run || !shown) return { nodes, edges: flows }
    const receiving = new Map<string, number>()
    if (traffic?.status === "ok") {
      for (const n of traffic.nodes) {
        if (n.rps < MIN_RPS) continue
        receiving.set(n.nodeId, n.rps)
        nodes[n.nodeId] = measuredLive(n, !running)
      }
    }
    const target = run.spec.target.nodeId
    const own = targetLive(run, samples)
    // The target's numbers come from the generator: every request, every second, client side.
    if (target && own) nodes[target] = own
    if (target && running) for (const id of entryPath(kind, edges, target)) flows[id] = {}
    if (running) Object.assign(flows, measuredEdges(kind, edges, receiving))
    return { nodes, edges: flows }
  }, [run, shown, traffic, samples, running, kind, edges])
  // On the samples' clock: a traffic spike's phase moves with each second of traffic (and only needs that clock).
  const last = samples[samples.length - 1]
  const at = start !== undefined && last ? start + last.t * 1000 : 0
  const fault = useMemo(() => (run ? faultLive(run, kind, start, at) : null), [run, kind, start, at])

  useEffect(() => {
    useLiveStore.setState((prev) => ({
      active: shown,
      nodes: shareRecord(prev.nodes, live.nodes),
      edges: shareRecord(prev.edges, live.edges),
      fault: fault && (fault.phase !== "done" || shown) ? (prev.fault && sameFault(prev.fault, fault) ? prev.fault : fault) : null,
    }))
  }, [shown, live, fault])
  useEffect(() => () => useLiveStore.setState(EMPTY_LIVE), [])

  if (!run || !shown) return null
  return <LivePanel kind={kind} run={run} samples={samples} traffic={traffic} fault={fault} onDismiss={() => setDismissed(run.id)} />
})

function sameFault(a: FaultLive, b: FaultLive) {
  return a.phase === b.phase && a.until === b.until && a.label === b.label && a.verdict === b.verdict && a.message === b.message && a.detail === b.detail
}

function LivePanel({
  kind,
  run,
  samples,
  traffic,
  fault,
  onDismiss,
}: {
  kind: WorkflowKind
  run: LoadTestRun
  samples: LoadSample[]
  traffic: LiveTraffic | undefined
  fault: FaultLive | null
  onDismiss: () => void
}) {
  const running = run.status === "running"
  const now = useNow(fault?.until !== undefined)
  const incident = fault ? faultText(fault, now) : undefined
  const own = targetLive(run, samples)
  const target = run.spec.target
  const scenario = run.spec.scenario
  return (
    <Panel position="bottom-center" className="!mb-3">
      <div
        data-testid="live-traffic-panel"
        data-run={run.id}
        className="flex w-[34rem] max-w-[calc(100vw-30rem)] flex-col gap-1 rounded-xl border bg-background/95 px-3 py-2 text-xs shadow-md backdrop-blur"
      >
        <div className="flex min-w-0 items-center gap-2">
          {running ? (
            <span className="flex shrink-0 items-center gap-1.5 font-medium">
              <ActivityIcon className="size-3.5 animate-pulse text-primary" />
              Load test
            </span>
          ) : (
            <StatusBadge status={run.status} className="h-5 shrink-0" />
          )}
          <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground" title={target.label}>
            {target.label}
          </span>
          {own && (
            <span data-testid="live-traffic-target" className={cn("ml-auto shrink-0 font-mono text-[11px] tabular-nums", TONE_TEXT[own.tone])}>
              {trafficLine(own)}
            </span>
          )}
          <div className={cn("flex shrink-0 items-center gap-0.5", !own && "ml-auto")}>
            {running && (
              <Button size="xs" variant="outline" onClick={() => void stopRun(run.id)} data-testid="live-traffic-stop">
                <SquareIcon className="fill-current" />
                Stop
              </Button>
            )}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button size="icon-xs" variant="ghost" aria-label="Hide live traffic" onClick={onDismiss}>
                  <XIcon />
                </Button>
              </TooltipTrigger>
              <TooltipContent>Hide live traffic</TooltipContent>
            </Tooltip>
          </div>
        </div>
        {scenario && (
          <div className="flex min-w-0 items-center gap-1.5" data-testid="live-traffic-incident">
            <FlameIcon className="size-3 shrink-0 text-muted-foreground" />
            <span className="truncate">
              {SCENARIO_LABEL[scenario.id]}
              {scenario.id !== "traffic_spike" && <span className="font-mono"> · {run.scenario?.targetName ?? nodeName(scenario.target)}</span>}
              {incident && <span className="text-muted-foreground"> · {incident}</span>}
            </span>
            {!running && (
              <Button size="xs" variant="link" className="ml-auto h-auto shrink-0 p-0 text-xs" onClick={() => revealLoadTest(run.id)}>
                View results
              </Button>
            )}
          </div>
        )}
        <Legend kind={kind} traffic={traffic} />
      </div>
    </Panel>
  )
}

/** What the canvas measures, and how to see more. */
function Legend({ kind, traffic }: { kind: WorkflowKind; traffic: LiveTraffic | undefined }) {
  const base = "Target: Nimbo's load generator."
  if (kind === "") return <p className="text-[11px] text-muted-foreground">{base} Requests inside your infrastructure are not measured.</p>
  const usage = "CPU on cards: live container stats."
  let perService
  switch (traffic?.status) {
    case "ok":
      perService = `Other services and moving edges: Prometheus, ${traffic.window}.`
      break
    case "not_running":
      perService = "Deploy to start Prometheus and see the requests each service receives."
      break
    case "unreachable":
      perService = `Prometheus did not answer (${traffic.message}).`
      break
    case "no_monitoring":
      perService = (
        <>
          <Button size="xs" variant="link" className="h-auto p-0 text-[11px]" onClick={openMonitoringDialog}>
            Add monitoring
          </Button>{" "}
          to see the requests each service receives.
        </>
      )
      break
    default:
      perService = undefined
  }
  return (
    <p className="text-[11px] text-pretty text-muted-foreground" data-testid="live-traffic-legend" data-source={traffic?.status ?? ""}>
      {base} {usage} {perService}
    </p>
  )
}
