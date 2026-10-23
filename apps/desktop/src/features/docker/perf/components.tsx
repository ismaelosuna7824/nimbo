import { CircleAlertIcon, TriangleAlertIcon } from "lucide-react"
import { useShallow } from "zustand/react/shallow"
import type { ComposeProblem, ComposeServiceStatus, ServiceUsage } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { cpuOfLimit, formatDuration, problemTone, sparklineCount, sparklinePoints, usageLine, usageTone } from "./model"

const STROKE = { ok: "stroke-primary", warning: "stroke-amber-500", error: "stroke-destructive" }
const FILL = { ok: "fill-primary/10", warning: "fill-amber-500/15", error: "fill-destructive/15" }
const BAR = { ok: "bg-primary", warning: "bg-amber-500", error: "bg-destructive" }

const NO_USAGE: ServiceUsage[] = []

const BADGE_LABEL: Record<ComposeProblem["kind"], string> = { oom: "OOM", restarts: "restarts", exit: "exited", unhealthy: "unhealthy" }

/** CPU (% of limit) of the last ~60 s as a tiny area chart; nothing until two samples exist. */
export function UsageSparkline({
  history,
  intervalMs,
  className,
  testId,
}: {
  history: ServiceUsage[]
  intervalMs?: number
  className?: string
  testId?: string
}) {
  const w = 60
  const h = 16
  const points = sparklinePoints(history, w, h, sparklineCount(intervalMs))
  if (!points) return null
  const last = history[history.length - 1]
  const tone = usageTone(cpuOfLimit(last))
  const firstX = points.slice(0, points.indexOf(","))
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className={cn("h-4 w-15 shrink-0", className)}
      data-testid={testId}
      role="img"
      aria-label="CPU over the last minute, % of limit"
    >
      <polygon points={`${firstX},${h} ${points} ${w},${h}`} className={FILL[tone]} />
      <polyline points={points} fill="none" className={STROKE[tone]} strokeWidth={1.25} vectorEffect="non-scaling-stroke" />
    </svg>
  )
}

/** Horizontal fill bar of a % (clamped), coloured by how close it is to the limit. */
export function UsageMeter({ pct, className }: { pct: number; className?: string }) {
  return (
    <div className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}>
      <div data-meter className={cn("h-full rounded-full", BAR[usageTone(pct)])} style={{ width: `${Math.min(100, Math.max(0, pct))}%` }} />
    </div>
  )
}

/**
 * Live usage line of a service card (sparkline, "CPU 23% · 118 MiB", "ready in 3.2 s"); nothing without data.
 * Kubernetes workload cards pass the workload node id as `name` and their workload status.
 */
export function ServiceCardUsage({ name, status }: { name: string; status: Pick<ComposeServiceStatus, "state" | "startupMs"> | undefined }) {
  const history = useWorkflowStore((s) => s.composeUsage[name]) ?? NO_USAGE
  const intervalMs = useWorkflowStore((s) => s.composeStats?.intervalMs)
  const running = status?.state === "running" || status?.state === "starting"
  const last = running ? history[history.length - 1] : undefined
  const startup = status?.startupMs
  if (!last && startup === undefined) return null
  return (
    <div className="flex min-w-0 items-center gap-1.5 text-[10px] text-muted-foreground tabular-nums">
      {last && (
        <>
          <UsageSparkline history={history} intervalMs={intervalMs} testId={`service-sparkline-${name}`} />
          <span className="truncate" title="CPU as % of the service's limit (host CPUs when unlimited) · memory used">
            {usageLine(last)}
          </span>
        </>
      )}
      {startup !== undefined && <span className="ml-auto shrink-0">ready in {formatDuration(startup)}</span>}
    </div>
  )
}

/** Red (OOM, failed exit) or amber (restarts, unhealthy) badge of a service's container problems. */
export function ServiceProblemBadge({ name }: { name: string }) {
  const problems = useWorkflowStore(useShallow((s) => (s.compose?.problems ?? []).filter((p) => p.service === name)))
  const tone = problemTone(problems)
  if (!tone) return null
  const Icon = tone === "error" ? CircleAlertIcon : TriangleAlertIcon
  return (
    <span
      data-testid={`service-problem-${name}`}
      data-severity={tone}
      title={problems.map((p) => p.message).join("\n")}
      className={cn(
        "inline-flex h-4.5 shrink-0 items-center gap-0.5 rounded-full px-1.5 text-[10px] font-medium",
        tone === "error" ? "bg-destructive/12 text-destructive" : "bg-amber-500/12 text-amber-600 dark:text-amber-400",
      )}
    >
      <Icon className="size-2.5" />
      {problems.length === 1 ? BADGE_LABEL[problems[0].kind] : `${problems.length} problems`}
    </span>
  )
}
