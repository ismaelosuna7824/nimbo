import { useEffect, useMemo, useRef, useState } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ArrowDownToLine, Copy, Eraser, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api/client"
import type { KubeLogLine, KubeWorkloadStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { StreamBadge, type StreamState } from "@/features/docker/deploy/compose-logs"
import { serviceColor } from "@/features/docker/deploy/compose-model"
import { copyText, formatClock } from "@/features/panels/lib/format"
import { appendKubeLogRows, kubeLogRowsText, type KubeLogRow } from "./kube-model"

/** Select value of "every pod" / "every container" (Radix Select items can't have an empty value). */
export const ALL = "__all__"
/** Lines each container sends before following. */
const TAIL = 200
/** Streamed lines are batched so a chatty pod doesn't re-render the list per line. */
const FLUSH_MS = 100

export interface LogSelection {
  /** Workload node id ("" until the workflow has workloads). */
  workload: string
  pod: string
  container: string
}

/**
 * Live `kubectl logs --follow` of a workload's pods (one pod or container when chosen). The stream only runs while
 * the viewer is visible; when the pods stop the daemon sends "end" and the stream reconnects by itself the next time
 * the cluster's status changes (e.g. after Deploy).
 */
export function KubeLogs({
  workflowId,
  workloads,
  selection,
  onSelectionChange,
  active,
  statusKey,
}: {
  workflowId: string
  workloads: KubeWorkloadStatus[]
  selection: LogSelection
  onSelectionChange: (s: LogSelection) => void
  active: boolean
  /** kubeKey of the cluster status: a change reconnects an ended stream. */
  statusKey: string
}) {
  const [rows, setRows] = useState<KubeLogRow[]>([])
  const [stream, setStream] = useState<StreamState>("connecting")
  const [generation, setGeneration] = useState(0)
  const [follow, setFollow] = useState(true)
  const scroller = useRef<HTMLDivElement>(null)

  const workload = workloads.find((w) => w.nodeId === selection.workload)
  const pods = useMemo(() => workload?.pods ?? [], [workload])
  const pod = pods.find((p) => p.name === selection.pod)
  const containers = useMemo(
    () => [...new Set((pod ? [pod] : pods).flatMap((p) => p.containers.filter((c) => !c.init).map((c) => c.name)))],
    [pod, pods],
  )
  const podNames = useMemo(() => pods.map((p) => p.name), [pods])
  // Pods with several containers name the container in each prefix.
  const multiKey = pods
    .filter((p) => p.containers.filter((c) => !c.init).length > 1)
    .map((p) => p.name)
    .join(",")

  useEffect(() => {
    if (!active || !selection.workload) return
    const multi = new Set(multiKey ? multiKey.split(",") : [])
    // The stream starts with the last TAIL lines again: start from an empty list.
    setRows([])
    setStream("connecting")
    const es = new EventSource(
      api.kubeLogsStreamUrl(workflowId, {
        workload: selection.workload,
        pod: selection.pod === ALL ? undefined : selection.pod,
        container: selection.container === ALL ? undefined : selection.container,
        tail: TAIL,
      }),
    )
    let pending: KubeLogLine[] = []
    let timer: number | undefined
    const flush = () => {
      window.clearTimeout(timer)
      timer = undefined
      if (pending.length === 0) return
      const batch = pending
      pending = []
      setRows((r) => appendKubeLogRows(r, batch, multi))
    }
    es.onopen = () => setStream("live")
    es.addEventListener("kube.log", (e) => {
      pending.push(JSON.parse((e as MessageEvent<string>).data) as KubeLogLine)
      timer ??= window.setTimeout(flush, FLUSH_MS)
    })
    // EventSource reconnects on its own after an error or a closed response, which would replay the tail: close it.
    es.addEventListener("end", () => {
      flush()
      es.close()
      setStream("ended")
    })
    es.onerror = () => {
      flush()
      es.close()
      setStream("error")
    }
    return () => {
      window.clearTimeout(timer)
      es.close()
    }
  }, [workflowId, selection.workload, selection.pod, selection.container, active, generation, multiKey])

  // Pods came back (or changed) after the stream ended: follow them again.
  const ended = stream === "ended" || stream === "error"
  const lastKey = useRef(statusKey)
  useEffect(() => {
    if (lastKey.current === statusKey) return
    lastKey.current = statusKey
    if (ended && active) setGeneration((g) => g + 1)
  }, [statusKey, ended, active])

  // eslint-disable-next-line react/incompatible-library -- no React Compiler; the virtualizer re-renders this component itself
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scroller.current,
    estimateSize: () => 17,
    overscan: 20,
    paddingStart: 4,
    paddingEnd: 4,
  })
  const totalSize = virtualizer.getTotalSize()

  useEffect(() => {
    const el = scroller.current
    if (el && follow) el.scrollTop = el.scrollHeight
  }, [rows.length, totalSize, follow])

  const width = useMemo(() => Math.min(32, rows.reduce((w, r) => Math.max(w, r.source.length), 0)), [rows])

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="kube-logs">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2">
        <Select
          value={selection.workload || undefined}
          onValueChange={(workload) => onSelectionChange({ workload, pod: ALL, container: ALL })}
        >
          <SelectTrigger size="sm" className="h-6 min-w-32 text-xs" aria-label="Workload">
            <SelectValue placeholder="Workload" />
          </SelectTrigger>
          <SelectContent>
            {workloads.map((w) => (
              <SelectItem key={w.nodeId} value={w.nodeId}>
                {w.name} <span className="text-muted-foreground">· {w.kind}</span>
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={selection.pod} onValueChange={(p) => onSelectionChange({ ...selection, pod: p, container: ALL })}>
          <SelectTrigger size="sm" className="h-6 min-w-28 text-xs" aria-label="Pod" disabled={!workload}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All pods</SelectItem>
            {pods.map((p) => (
              <SelectItem key={p.name} value={p.name}>
                {p.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {containers.length > 1 && (
          <Select value={selection.container} onValueChange={(c) => onSelectionChange({ ...selection, container: c })}>
            <SelectTrigger size="sm" className="h-6 min-w-28 text-xs" aria-label="Container">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ALL}>All containers</SelectItem>
              {containers.map((c) => (
                <SelectItem key={c} value={c}>
                  {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )}
        {selection.workload && <StreamBadge state={stream} />}
        {selection.workload && ended && (
          <Button size="xs" variant="ghost" onClick={() => setGeneration((g) => g + 1)}>
            <RefreshCw /> Reconnect
          </Button>
        )}
        <div className="ml-auto flex items-center gap-1">
          <Button
            size="xs"
            variant="ghost"
            disabled={rows.length === 0}
            onClick={() =>
              void copyText(kubeLogRowsText(rows)).then(
                () => toast.success("Logs copied"),
                () => toast.error("Couldn't copy to the clipboard"),
              )
            }
          >
            <Copy /> Copy
          </Button>
          <Button size="xs" variant="ghost" disabled={rows.length === 0} onClick={() => setRows([])}>
            <Eraser /> Clear
          </Button>
          <Button size="xs" variant={follow ? "secondary" : "ghost"} aria-pressed={follow} onClick={() => setFollow((f) => !f)}>
            <ArrowDownToLine /> Follow
          </Button>
        </div>
      </div>
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-auto bg-muted/20 font-mono text-[11.5px] leading-[1.45]"
        onScroll={(e) => {
          // Scrolling up pauses following; back at the bottom resumes it.
          const el = e.currentTarget
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24
          if (atBottom !== follow) setFollow(atBottom)
        }}
      >
        {rows.length === 0 ? (
          <p className="px-3 py-3 text-muted-foreground">
            {!selection.workload
              ? "No workloads yet. Deploy the workflow, then pick a workload to follow its pods' logs."
              : stream === "connecting"
                ? "Connecting…"
                : stream === "error"
                  ? "Couldn't read the pod logs. Is the cluster running?"
                  : stream === "ended"
                    ? "No output. The pods are not running — press Deploy to start them."
                    : "Waiting for output…"}
          </p>
        ) : (
          <div className="relative" style={{ height: totalSize }}>
            {virtualizer.getVirtualItems().map((item) => {
              const r = rows[item.index]
              return (
                <div
                  key={r.id}
                  ref={virtualizer.measureElement}
                  data-index={item.index}
                  data-testid="kube-log-line"
                  data-pod={r.pod}
                  className={cn("absolute inset-x-0 top-0 flex gap-3 px-3 hover:bg-muted/60", r.stream === "stderr" && "bg-destructive/5")}
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  <span className="shrink-0 text-muted-foreground/60 select-none">{formatClock(r.t)}</span>
                  <span
                    className={cn("shrink-0 truncate select-none", serviceColor(r.pod, podNames))}
                    style={{ width: `${width}ch` }}
                    title={`${r.pod} / ${r.container}`}
                  >
                    {r.source}
                  </span>
                  <span
                    className={cn(
                      "min-w-0 flex-1 break-words whitespace-pre-wrap",
                      r.stream === "stderr" && "text-destructive",
                      r.stream === "system" && "text-sky-600 italic dark:text-sky-400",
                    )}
                  >
                    {r.text || " "}
                  </span>
                </div>
              )
            })}
          </div>
        )}
      </div>
    </div>
  )
}
