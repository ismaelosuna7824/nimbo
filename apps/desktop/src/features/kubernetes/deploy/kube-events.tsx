import { useMemo } from "react"
import { History, TriangleAlert } from "lucide-react"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import type { KubeEvent, KubeWorkloadStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { formatRelative } from "@/features/panels/lib/format"
import { workloadEvents } from "./kube-model"
import { ALL } from "./kube-logs"

/** Cluster events (newest first), Warnings highlighted, optionally only those about one workload. */
export function KubeEvents({
  events,
  workloads,
  workload,
  onWorkloadChange,
}: {
  events: KubeEvent[]
  workloads: KubeWorkloadStatus[]
  /** Workload node id, or ALL. */
  workload: string
  onWorkloadChange: (nodeId: string) => void
}) {
  const selected = workloads.find((w) => w.nodeId === workload)
  const shown = useMemo(() => workloadEvents(events, selected), [events, selected])
  const warnings = shown.filter((e) => e.type === "Warning").length

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="kube-events">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2">
        <Select value={selected ? workload : ALL} onValueChange={onWorkloadChange}>
          <SelectTrigger size="sm" className="h-6 min-w-36 text-xs" aria-label="Workload">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All objects</SelectItem>
            {workloads.map((w) => (
              <SelectItem key={w.nodeId} value={w.nodeId}>
                {w.name} <span className="text-muted-foreground">· {w.kind}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-[11px] text-muted-foreground tabular-nums">
          {shown.length} event{shown.length === 1 ? "" : "s"}
          {warnings > 0 && <span className="text-amber-600 dark:text-amber-400"> · {warnings} warning{warnings === 1 ? "" : "s"}</span>}
        </span>
      </div>
      {shown.length === 0 ? (
        <Empty className="min-h-0 flex-1">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <History />
            </EmptyMedia>
            <EmptyTitle>No events</EmptyTitle>
            <EmptyDescription>
              Kubernetes records what happens to your objects here: scheduling, image pulls, probe failures, scaling.
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <ul className="min-h-0 flex-1 divide-y overflow-auto">
          {shown.map((e, i) => {
            const warning = e.type === "Warning"
            return (
              <li
                key={`${e.kind}/${e.namespace ?? ""}/${e.name}:${e.reason}:${e.lastSeen}:${i}`}
                data-testid="kube-event"
                data-type={e.type}
                className={cn("flex gap-2 px-3 py-1.5 text-xs", warning && "bg-amber-500/8")}
              >
                {warning ? (
                  <TriangleAlert className="mt-0.5 size-3.5 shrink-0 text-amber-600 dark:text-amber-400" />
                ) : (
                  <span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline gap-x-2">
                    <span className={cn("font-medium", warning && "text-amber-700 dark:text-amber-400")}>{e.reason}</span>
                    <span className="truncate font-mono text-[10px] text-muted-foreground">
                      {e.kind}/{e.name}
                    </span>
                    {e.count > 1 && <span className="text-[10px] text-muted-foreground tabular-nums">×{e.count}</span>}
                    <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{formatRelative(e.lastSeen)}</span>
                  </div>
                  <p className="mt-0.5 break-words text-muted-foreground">{e.message}</p>
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </div>
  )
}
