import { Activity } from "lucide-react"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import type { ComposeServiceStatus, ServiceUsage } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { UsageMeter, UsageSparkline } from "./components"
import { cpuLimitLabel, cpuOfLimit, formatBytes, formatDuration, formatPct, memLimitLabel, memOfLimit } from "./model"

const dash = <span className="text-muted-foreground">—</span>

/** A row of the usage table: usage is keyed by `name` (compose service, or workload node id in Kubernetes). */
export type ResourceRow = Pick<ComposeServiceStatus, "name" | "state" | "startupMs"> & { label?: string }

/** Live `docker stats` of every service: CPU and memory against their limits, network, disk, PIDs, startup time. */
export function ComposeResources({
  services,
  heading = "Service",
  emptyHint = "Start the project with Up to see the CPU, memory, network and disk use of every service, sampled with docker stats.",
}: {
  services: ResourceRow[]
  heading?: string
  emptyHint?: string
}) {
  const usage = useWorkflowStore((s) => s.composeUsage)
  const info = useWorkflowStore((s) => s.composeStats)
  const live = (s: ResourceRow): ServiceUsage[] | undefined =>
    s.state === "running" || s.state === "starting" ? usage[s.name] : undefined

  if (!services.some((s) => live(s)?.length || s.startupMs !== undefined)) {
    return (
      <Empty className="h-full" data-testid="compose-stats-empty">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Activity />
          </EmptyMedia>
          <EmptyTitle>No live usage yet</EmptyTitle>
          <EmptyDescription>
            {info?.message ?? emptyHint}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="flex flex-col">
      <table className="w-full text-xs" data-testid="compose-stats-table">
        <thead className="sticky top-0 z-10 bg-background text-left text-[11px] text-muted-foreground">
          <tr className="border-b">
            <th className="px-3 py-1.5 font-medium">{heading}</th>
            <th className="px-2 py-1.5 font-medium" title="docker stats CPU divided by the service's CPU limit (host CPUs when unlimited)">
              CPU (% of limit)
            </th>
            <th className="px-2 py-1.5 font-medium">Memory</th>
            <th className="px-2 py-1.5 font-medium" title="Received / sent since the container started">
              Network ↓/↑
            </th>
            <th className="px-2 py-1.5 font-medium" title="Read / written since the container started">
              Disk r/w
            </th>
            <th className="px-2 py-1.5 font-medium">PIDs</th>
            <th className="px-2 py-1.5 font-medium" title="From container start until it runs (healthy when it has a health check)">
              Startup
            </th>
            <th className="px-3 py-1.5 font-medium">Last minute</th>
          </tr>
        </thead>
        <tbody className="divide-y">
          {services.map((s) => {
            const history = live(s) ?? []
            const u = history[history.length - 1]
            const cpu = u ? cpuOfLimit(u) : 0
            const mem = u ? memOfLimit(u) : 0
            return (
              <tr key={s.name} data-testid="compose-stats-row" data-service={s.name} className="hover:bg-muted/40">
                <td className="px-3 py-1.5 font-medium">
                  {s.label ?? s.name}
                  {u && u.containers > 1 && <span className="ml-1 text-muted-foreground tabular-nums">×{u.containers}</span>}
                </td>
                <td className="w-44 px-2 py-1.5">
                  {u ? (
                    <div className="flex flex-col gap-0.5" title={`docker stats: ${u.cpuPercent.toFixed(1)}% (100% = one core)`}>
                      <div className="flex items-baseline gap-1 tabular-nums">
                        <span className="font-medium">{formatPct(cpu)}</span>
                        <span className="truncate text-[10px] text-muted-foreground">of {cpuLimitLabel(u.cpuLimit, u.cpuLimited)}</span>
                      </div>
                      <UsageMeter pct={cpu} />
                    </div>
                  ) : (
                    dash
                  )}
                </td>
                <td className="w-48 px-2 py-1.5">
                  {u ? (
                    <div className="flex flex-col gap-0.5">
                      <div className="flex items-baseline gap-1 tabular-nums">
                        <span className="font-medium">{formatBytes(u.memBytes)}</span>
                        <span className="truncate text-[10px] text-muted-foreground">/ {memLimitLabel(u.memLimit, u.memLimited)}</span>
                      </div>
                      <UsageMeter pct={mem} />
                    </div>
                  ) : (
                    dash
                  )}
                </td>
                <td className="px-2 py-1.5 tabular-nums">
                  {u ? `${formatBytes(u.netRxBytes)} / ${formatBytes(u.netTxBytes)}` : dash}
                </td>
                <td className="px-2 py-1.5 tabular-nums">
                  {u ? `${formatBytes(u.blockReadBytes)} / ${formatBytes(u.blockWriteBytes)}` : dash}
                </td>
                <td className="px-2 py-1.5 tabular-nums">{u ? u.pids : dash}</td>
                <td className="px-2 py-1.5 tabular-nums">{s.startupMs !== undefined ? formatDuration(s.startupMs) : dash}</td>
                <td className="px-3 py-1.5">
                  <UsageSparkline history={history} intervalMs={info?.intervalMs} className="h-5 w-24" />
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
      {info && (
        <p className="px-3 py-1.5 text-[11px] text-muted-foreground">
          This machine: {cpuLimitLabel(info.hostCpus, true)} · {formatBytes(info.hostMemory)} memory
          {info.intervalMs > 0 && ` · sampled every ${formatDuration(info.intervalMs)}`}
          {!info.streaming && " · not sampling (no running containers)"}
        </p>
      )}
    </div>
  )
}
