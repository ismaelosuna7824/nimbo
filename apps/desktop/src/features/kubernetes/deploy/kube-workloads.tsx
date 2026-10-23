import { Fragment, useMemo } from "react"
import { CircleAlert, History, RotateCcw, ScrollText, TriangleAlert } from "lucide-react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { techIconFor } from "@/components/tech-icons"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import type { KubeNode, KubePod, KubeProblem, KubeWorkloadStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useNow } from "@/features/panels/lib/use-now"
import { ComposeStateLabel, RowAction } from "@/features/docker/deploy/compose-services"
import { kubeProblemTone, podAge, workloadPods, workloadRestarts } from "./kube-model"

/** Kinds `kubectl rollout restart` works on. */
const RESTARTABLE = new Set(["Deployment", "StatefulSet", "DaemonSet"])

const PHASE_CLASS: Record<string, string> = {
  Running: "text-emerald-700 dark:text-emerald-400",
  Succeeded: "text-muted-foreground",
  Pending: "text-amber-700 dark:text-amber-400",
  Failed: "text-destructive",
  Unknown: "text-muted-foreground",
}

/** Icon of a workload's main container from its graph facts or the image the cluster reports. */
function workloadIcon(w: KubeWorkloadStatus, node: KubeNode | undefined) {
  const image = node?.workload?.containers[0]?.image ?? w.images[0]
  return techIconFor({ image, stack: node?.stack, kind: "service" })
}

/**
 * One row per workload (state, ready/desired, updated, restarts, rollout line, per-workload actions) with its pods
 * underneath (phase, ready, restarts, reason, node, age).
 */
export function KubeWorkloads({
  workloads,
  nodes,
  problems,
  busy,
  starting,
  onRestart,
  onLogs,
  onEvents,
}: {
  workloads: KubeWorkloadStatus[]
  nodes: KubeNode[]
  problems: KubeProblem[]
  /** A run is in progress or the cluster is down: per-workload actions wait. */
  busy: boolean
  /** "restart:<nodeId>" being started. */
  starting: string | null
  onRestart: (nodeId: string) => void
  onLogs: (nodeId: string, pod?: string) => void
  onEvents: (nodeId: string) => void
}) {
  const byId = useMemo(() => new Map(nodes.map((n) => [n.id, n])), [nodes])
  // Ages only need minute precision.
  const now = useNow(true, 10_000)
  return (
    <table className="w-full text-xs">
      <thead className="sticky top-0 z-10 bg-background text-left text-[11px] text-muted-foreground">
        <tr className="border-b">
          <th className="px-3 py-1.5 font-medium">Workload</th>
          <th className="px-2 py-1.5 font-medium">State</th>
          <th className="px-2 py-1.5 font-medium" title="Ready pods / desired replicas">
            Ready
          </th>
          <th className="px-2 py-1.5 font-medium" title="Pods running the current template">
            Updated
          </th>
          <th className="px-2 py-1.5 font-medium">Restarts</th>
          <th className="px-2 py-1.5 font-medium">Node · Age</th>
          <th className="px-3 py-1.5" aria-label="Actions" />
        </tr>
      </thead>
      <tbody>
        {workloads.map((w) => (
          <WorkloadRows
            key={w.nodeId}
            workload={w}
            node={byId.get(w.nodeId)}
            problems={problems.filter((p) => p.nodeId === w.nodeId)}
            now={now}
            busy={busy}
            starting={starting}
            onRestart={onRestart}
            onLogs={onLogs}
            onEvents={onEvents}
          />
        ))}
      </tbody>
    </table>
  )
}

function WorkloadRows({
  workload: w,
  node,
  problems,
  now,
  busy,
  starting,
  onRestart,
  onLogs,
  onEvents,
}: {
  workload: KubeWorkloadStatus
  node: KubeNode | undefined
  problems: KubeProblem[]
  now: number
  busy: boolean
  starting: string | null
  onRestart: (nodeId: string) => void
  onLogs: (nodeId: string, pod?: string) => void
  onEvents: (nodeId: string) => void
}) {
  const icon = useMemo(() => workloadIcon(w, node), [w, node])
  const pods = workloadPods(w)
  const restarts = workloadRestarts(w)
  const tone = kubeProblemTone(problems)
  const job = w.kind === "Job" || w.kind === "CronJob"
  return (
    <Fragment>
      <tr
        data-testid={`kube-workload-${w.nodeId}`}
        data-state={w.state}
        className="border-t bg-muted/10 hover:bg-muted/40"
      >
        <td className="px-3 py-1.5">
          <div className="flex min-w-0 items-center gap-2">
            <span className="relative shrink-0">
              <TechIcon icon={icon} size={20} />
              <K8sIcon icon={w.kind} size={12} className="absolute -right-1 -bottom-1" />
            </span>
            <div className="min-w-0">
              <div className="flex items-center gap-1.5">
                <span className="truncate font-medium">{w.name}</span>
                <span className="shrink-0 text-[10px] text-muted-foreground">
                  {w.kind} · {w.namespace}
                </span>
                {tone && (
                  <span
                    title={problems.map((p) => p.message).join("\n")}
                    className={cn("inline-flex shrink-0", tone === "error" ? "text-destructive" : "text-amber-600 dark:text-amber-400")}
                  >
                    {tone === "error" ? <CircleAlert className="size-3" /> : <TriangleAlert className="size-3" />}
                  </span>
                )}
              </div>
              {w.images.length > 0 && (
                <div className="truncate font-mono text-[10px] text-muted-foreground" title={w.images.join("\n")}>
                  {w.images.join(", ")}
                </div>
              )}
              {w.rollout && (
                <div className="truncate text-[10px] text-muted-foreground" data-testid="kube-rollout">
                  {w.rollout}
                </div>
              )}
            </div>
          </div>
        </td>
        <td className="px-2 py-1.5">
          <ComposeStateLabel state={w.state} />
        </td>
        <td className={cn("px-2 py-1.5 tabular-nums", w.ready < w.desired && "text-amber-700 dark:text-amber-400")}>
          {job ? `${w.succeeded ?? 0} done${w.failed ? ` · ${w.failed} failed` : ""}` : `${w.ready}/${w.desired}`}
        </td>
        <td className="px-2 py-1.5 tabular-nums">{job ? "—" : w.updated}</td>
        <td className={cn("px-2 py-1.5 tabular-nums", restarts > 0 && "text-amber-700 dark:text-amber-400")}>{restarts}</td>
        <td className="px-2 py-1.5 text-muted-foreground tabular-nums">
          {pods.total === 0 ? (w.lastSchedule ? `last run ${podAge(w.lastSchedule, now)} ago` : "no pods") : `${pods.total} pod${pods.total === 1 ? "" : "s"}`}
        </td>
        <td className="px-3 py-1.5">
          <div className="flex justify-end gap-0.5">
            {RESTARTABLE.has(w.kind) && (
              <RowAction
                label={`Restart ${w.name} (rollout restart)`}
                disabled={busy || pods.total === 0}
                loading={starting === `restart:${w.nodeId}`}
                onClick={() => onRestart(w.nodeId)}
              >
                <RotateCcw />
              </RowAction>
            )}
            <RowAction label={`Events of ${w.name}`} onClick={() => onEvents(w.nodeId)}>
              <History />
            </RowAction>
            <RowAction label={`Logs of ${w.name}`} onClick={() => onLogs(w.nodeId)}>
              <ScrollText />
            </RowAction>
          </div>
        </td>
      </tr>
      {w.pods.map((p) => (
        <PodRow key={p.name} pod={p} now={now} onLogs={() => onLogs(w.nodeId, p.name)} />
      ))}
    </Fragment>
  )
}

function PodRow({ pod: p, now, onLogs }: { pod: KubePod; now: number; onLogs: () => void }) {
  const containers = p.containers.filter((c) => !c.init)
  const ready = containers.filter((c) => c.ready).length
  const oom = p.containers.some((c) => c.oomKilled)
  return (
    <tr data-testid={`kube-pod-${p.name}`} data-phase={p.phase} className="text-[11px] hover:bg-muted/40">
      <td className="py-1 pr-3 pl-10">
        <div className="truncate font-mono" title={p.ip ? `${p.name} · ${p.ip}` : p.name}>
          {p.name}
        </div>
      </td>
      <td className="px-2 py-1">
        <span className={cn("font-medium", PHASE_CLASS[p.phase] ?? "text-muted-foreground")}>{p.phase}</span>
        {(p.reason || oom) && (
          <Badge
            variant="outline"
            className="ml-1.5 h-4 border-destructive/30 bg-destructive/10 px-1 text-[10px] text-destructive"
            title={p.message}
          >
            {p.reason ?? "OOMKilled"}
          </Badge>
        )}
      </td>
      <td className={cn("px-2 py-1 tabular-nums", !p.ready && "text-amber-700 dark:text-amber-400")}>
        {ready}/{containers.length}
      </td>
      <td className="px-2 py-1" />
      <td className={cn("px-2 py-1 tabular-nums", p.restarts > 0 && "text-amber-700 dark:text-amber-400")}>{p.restarts}</td>
      <td className="px-2 py-1 text-muted-foreground">
        <span className="tabular-nums">
          {p.node ?? "—"} · {podAge(p.startedAt, now)}
        </span>
      </td>
      <td className="px-3 py-1">
        <div className="flex justify-end">
          <RowAction label={`Logs of ${p.name}`} onClick={onLogs}>
            <ScrollText />
          </RowAction>
        </div>
      </td>
    </tr>
  )
}
