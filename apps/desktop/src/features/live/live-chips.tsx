import { EdgeLabelRenderer } from "@xyflow/react"
import { ActivityIcon, CircleCheck, CircleMinus, CircleX, FlameIcon, SquareIcon, TriangleAlert } from "lucide-react"
import { memo } from "react"
import { RunningFlow } from "@/components/canvas-card"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useNow } from "@/features/panels/lib/use-now"
import { stopRun, useEdgeLive, useNodeFault, useNodeLive, useTrafficShown } from "./live-store"
import { faultText, formatRps, trafficLine, type FaultLive, type NodeLive, type TrafficTone } from "./traffic"

const TONE: Record<TrafficTone, string> = {
  ok: "border-border bg-background text-foreground",
  warning: "border-amber-500/50 bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  error: "border-destructive/50 bg-red-50 text-destructive dark:bg-red-950",
}

const SOURCE: Record<string, string> = {
  otel: "the service's own HTTP metrics",
  traefik: "the Traefik ingress",
  "ingress-nginx": "the NGINX ingress",
}

function TrafficChip({ id, live }: { id: string; live: NodeLive }) {
  const where =
    live.role === "target"
      ? `The load test's requests to this node, measured by Nimbo's load generator (${live.final ? "whole run" : "last 3 s"}).`
      : `Requests this service received, measured by Prometheus from ${SOURCE[live.source ?? ""] ?? "its metrics"} (between the last two scrapes).`
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-testid={`live-traffic-${id}`}
          data-role={live.role}
          data-tone={live.tone}
          className={cn("inline-flex h-5 items-center gap-1 rounded-full border px-1.5 font-mono text-[10px] font-medium whitespace-nowrap shadow-xs tabular-nums", TONE[live.tone])}
        >
          <ActivityIcon className={cn("size-2.5 shrink-0", live.tone === "ok" && "text-primary")} />
          {trafficLine(live)}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">{where}</TooltipContent>
    </Tooltip>
  )
}

const FAULT_TONE: Record<FaultLive["phase"], string> = {
  pending: "border-border bg-background text-muted-foreground",
  active: "border-amber-500/50 bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  reverted: "border-border bg-background text-foreground",
  failed: "border-amber-500/50 bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  done: "border-border bg-background text-foreground",
}
const VERDICT_TONE = {
  survived: "border-emerald-500/40 bg-background text-emerald-700 dark:text-emerald-400",
  degraded: "border-amber-500/50 bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-300",
  failed: "border-destructive/50 bg-red-50 text-destructive dark:bg-red-950",
  inconclusive: "border-border bg-background text-muted-foreground",
}
const VERDICT_ICON = { survived: CircleCheck, degraded: TriangleAlert, failed: CircleX, inconclusive: CircleMinus }

function FaultChip({ fault }: { fault: FaultLive }) {
  const counting = fault.until !== undefined
  const now = useNow(counting)
  const running = fault.phase !== "done"
  const Icon = fault.phase === "done" && fault.verdict ? VERDICT_ICON[fault.verdict] : FlameIcon
  const tone = fault.phase === "done" && fault.verdict ? VERDICT_TONE[fault.verdict] : FAULT_TONE[fault.phase]
  const explain = fault.phase === "done" || fault.phase === "failed" ? fault.message : fault.detail
  return (
    <span
      data-testid={`live-fault-${fault.nodeId}`}
      data-phase={fault.phase}
      data-verdict={fault.verdict}
      className={cn("pointer-events-auto inline-flex h-5 items-center gap-1 rounded-full border pr-0.5 pl-1.5 text-[10px] font-medium whitespace-nowrap shadow-xs tabular-nums", tone, !running && "pr-1.5")}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex items-center gap-1">
            <Icon className={cn("size-2.5 shrink-0", fault.phase === "active" && "animate-pulse")} />
            {faultText(fault, now)}
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-72">
          {explain ?? "Failure test: Nimbo breaks this on purpose during the load test and puts it back afterwards."}
        </TooltipContent>
      </Tooltip>
      {running && (
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label="Stop the test and revert the fault"
              className="nodrag inline-flex size-4 items-center justify-center rounded-full hover:bg-foreground/10"
              onClick={(e) => {
                e.stopPropagation()
                void stopRun(fault.runId)
              }}
              onDoubleClick={(e) => e.stopPropagation()}
            >
              <SquareIcon className="size-2 fill-current" />
            </button>
          </TooltipTrigger>
          <TooltipContent>Stop the test and revert the fault</TooltipContent>
        </Tooltip>
      )}
    </span>
  )
}

/**
 * A card's live state, floating over its top edge so it never moves the card's content: the incident on it (left)
 * and the requests it receives (right). Subscribes to this node's entries only. `className` moves it clear of other
 * corner markers.
 */
export const NodeLiveBadges = memo(function NodeLiveBadges({ id, className }: { id: string; className?: string }) {
  const live = useNodeLive(id)
  const fault = useNodeFault(id)
  if (!live && !fault) return null
  return (
    <div className={cn("pointer-events-none absolute -top-2.5 right-2 left-2 z-10 flex items-center gap-1", className)}>
      {fault && <FaultChip fault={fault} />}
      {live && (
        <span className="pointer-events-auto ml-auto">
          <TrafficChip id={id} live={live} />
        </span>
      )}
    </div>
  )
})

/**
 * What moves along an edge: while a run's traffic is shown, a dot where requests are known to flow (with the
 * receiver's rate when this edge is its only way in); otherwise the running-containers flow (`running`).
 */
export function EdgeFlow({ id, path, labelX, labelY, running }: { id: string; path: string; labelX: number; labelY: number; running: boolean }) {
  const shown = useTrafficShown()
  const live = useEdgeLive(id)
  if (!shown) return running ? <RunningFlow path={path} /> : null
  if (!live) return null
  return (
    <>
      <path d={path} fill="none" className="stroke-primary/50" strokeWidth={2.5} data-testid={`live-edge-${id}`} />
      <circle r={3.5} className="fill-primary motion-reduce:hidden">
        <animateMotion dur="1s" repeatCount="indefinite" path={path} />
      </circle>
      {live.rps !== undefined && (
        <EdgeLabelRenderer>
          <div
            data-testid="live-edge-rate"
            className="nodrag nopan pointer-events-none absolute rounded-md border border-primary/40 bg-background px-1.5 py-0.5 font-mono text-[10px] text-primary shadow-sm tabular-nums"
            style={{ transform: `translate(-50%, -150%) translate(${labelX}px, ${labelY}px)` }}
          >
            {formatRps(live.rps)}
          </div>
        </EdgeLabelRenderer>
      )}
    </>
  )
}
