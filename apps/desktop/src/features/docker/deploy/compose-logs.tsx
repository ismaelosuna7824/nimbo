import { useEffect, useMemo, useRef, useState } from "react"
import { useVirtualizer } from "@tanstack/react-virtual"
import { ArrowDownToLine, Copy, Eraser, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { ComposeLogLine } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { copyText, formatClock } from "@/features/panels/lib/format"
import { appendLogRows, logRowsText, serviceColor, type ComposeLogRow } from "./compose-model"

/** Select value of "every service" (Radix Select items can't have an empty value). */
export const ALL_SERVICES = "__all__"
/** Lines docker sends before following (the server caps it at 5000). */
const TAIL = 200
/** Streamed lines are batched so a chatty container doesn't re-render the list per line. */
const FLUSH_MS = 100

export type StreamState = "connecting" | "live" | "ended" | "error"

/**
 * Live `docker compose logs --follow` of one service or all of them. The stream only runs while the viewer is
 * visible (active); hiding it, switching service or unmounting closes it. When the containers stop the daemon sends
 * "end"; the stream reconnects by itself the next time the project's status changes (e.g. after Up).
 */
export function ComposeLogs({
  workflowId,
  services,
  service,
  onServiceChange,
  active,
  statusKey,
}: {
  workflowId: string
  /** Service names in status order (also decides the prefix colours). */
  services: string[]
  service: string
  onServiceChange: (service: string) => void
  active: boolean
  /** composeKey of the project status: a change reconnects an ended stream. */
  statusKey: string
}) {
  const [rows, setRows] = useState<ComposeLogRow[]>([])
  const [stream, setStream] = useState<StreamState>("connecting")
  const [generation, setGeneration] = useState(0)
  const [follow, setFollow] = useState(true)
  const scroller = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!active) return
    // The stream starts with the last TAIL lines again: start from an empty list.
    setRows([])
    setStream("connecting")
    const es = new EventSource(api.composeLogsStreamUrl(workflowId, service === ALL_SERVICES ? undefined : service, TAIL))
    let pending: ComposeLogLine[] = []
    let timer: number | undefined
    const flush = () => {
      window.clearTimeout(timer)
      timer = undefined
      if (pending.length === 0) return
      const batch = pending
      pending = []
      setRows((r) => appendLogRows(r, batch))
    }
    es.onopen = () => setStream("live")
    es.addEventListener("compose.log", (e) => {
      pending.push(JSON.parse((e as MessageEvent<string>).data) as ComposeLogLine)
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
  }, [workflowId, service, active, generation])

  // Containers came back (or changed) after the stream ended: follow them again.
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

  const width = useMemo(() => Math.min(24, rows.reduce((w, r) => Math.max(w, r.source.length), 0)), [rows])

  return (
    <div className="flex h-full min-h-0 flex-col" data-testid="compose-logs">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2">
        <Select value={service} onValueChange={onServiceChange}>
          <SelectTrigger size="sm" className="h-6 min-w-36 text-xs" aria-label="Service">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL_SERVICES}>All services</SelectItem>
            {services.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <StreamBadge state={stream} />
        {ended && (
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
              void copyText(logRowsText(rows)).then(
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
            {stream === "connecting"
              ? "Connecting…"
              : stream === "error"
                ? "Couldn't read the container logs. Is Docker running?"
                : stream === "ended"
                  ? "No output. The containers are not running — press Up to start them."
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
                  data-testid="compose-log-line"
                  data-service={r.service}
                  className={cn(
                    "absolute inset-x-0 top-0 flex gap-3 px-3 hover:bg-muted/60",
                    r.stream === "stderr" && "bg-destructive/5",
                  )}
                  style={{ transform: `translateY(${item.start}px)` }}
                >
                  <span className="shrink-0 text-muted-foreground/60 select-none">{formatClock(r.t)}</span>
                  <span
                    className={cn("shrink-0 truncate select-none", serviceColor(r.service, services))}
                    style={{ width: `${width}ch` }}
                    title={r.source}
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

/** Live / ended / disconnected dot of a log stream. */
export function StreamBadge({ state }: { state: StreamState }) {
  if (state === "connecting") return <Spinner className="size-3.5 text-muted-foreground" />
  return (
    <span className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
      <span
        className={cn(
          "size-1.5 rounded-full",
          state === "live" ? "bg-emerald-500" : state === "error" ? "bg-destructive" : "bg-muted-foreground/50",
        )}
      />
      {state === "live" ? "Live" : state === "error" ? "Disconnected" : "Stream ended"}
    </span>
  )
}
