import { useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowDownToLine, Search, X } from "lucide-react"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import type { LogLine, Run } from "@/lib/api/types"
import { api } from "@/lib/api/client"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { formatClock } from "../lib/format"

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;]*[A-Za-z]/g
const EMPTY: LogLine[] = []

/** Full log from the API merged with lines streamed over SSE since. */
function mergeLogs(fetched: LogLine[], live: LogLine[]): LogLine[] {
  if (live.length === 0) return fetched
  const seen = new Set(fetched.map((l) => `${l.t}\u0000${l.stream}\u0000${l.text}`))
  const extra = live.filter((l) => !seen.has(`${l.t}\u0000${l.stream}\u0000${l.text}`))
  return extra.length === 0 ? fetched : [...fetched, ...extra]
}

export function RunLog({ run }: { run: Run }) {
  const live = useWorkflowStore((s) => s.runLogs[run.id] ?? EMPTY)
  const finished = run.status !== "queued" && run.status !== "running"
  const query = useQuery({
    // Refetch once the run finishes to get the complete log.
    queryKey: ["run-log", run.id, finished],
    queryFn: () => api.run(run.id),
  })
  const lines = useMemo(
    () =>
      (finished && query.data ? (query.data.log ?? EMPTY) : mergeLogs(query.data?.log ?? EMPTY, live)).map((l) => ({
        ...l,
        text: l.text.replace(ANSI, ""),
      })),
    [finished, query.data, live],
  )
  const [search, setSearch] = useState("")
  const [follow, setFollow] = useState(true)
  const scroller = useRef<HTMLDivElement>(null)

  const needle = search.trim().toLowerCase()
  const visible = needle ? lines.filter((l) => l.text.toLowerCase().includes(needle)) : lines

  useEffect(() => {
    const el = scroller.current
    if (el && follow && !needle) el.scrollTop = el.scrollHeight
  }, [visible.length, follow, needle])

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex h-8 shrink-0 items-center gap-2 border-b px-2">
        <InputGroup className="h-6 w-56">
          <InputGroupAddon>
            <Search />
          </InputGroupAddon>
          <InputGroupInput
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search log"
            className="text-xs"
            aria-label="Search log"
          />
          {search && (
            <InputGroupAddon align="inline-end">
              <button type="button" aria-label="Clear search" onClick={() => setSearch("")}>
                <X className="size-3" />
              </button>
            </InputGroupAddon>
          )}
        </InputGroup>
        {needle && (
          <span className="text-[11px] text-muted-foreground tabular-nums">
            {visible.length} of {lines.length} lines
          </span>
        )}
        {run.error && <span className="truncate text-xs text-destructive">{run.error}</span>}
        <Button
          size="xs"
          variant={follow ? "secondary" : "ghost"}
          className="ml-auto"
          aria-pressed={follow}
          onClick={() => setFollow((f) => !f)}
        >
          <ArrowDownToLine /> Follow
        </Button>
      </div>
      <div
        ref={scroller}
        className="min-h-0 flex-1 overflow-auto bg-muted/20 py-1 font-mono text-[11.5px] leading-[1.45]"
        onScroll={(e) => {
          const el = e.currentTarget
          const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24
          if (atBottom !== follow && !needle) setFollow(atBottom)
        }}
      >
        {visible.length === 0 ? (
          <p className="px-3 py-2 text-muted-foreground">
            {query.isPending ? "Loading log…" : needle ? "No matching lines." : "Waiting for output…"}
          </p>
        ) : (
          visible.map((l, i) => (
            <div key={i} className="flex gap-3 px-3 hover:bg-muted/60">
              <span className="shrink-0 text-muted-foreground/60 select-none">{formatClock(l.t)}</span>
              <span
                className={cn(
                  "min-w-0 flex-1 break-words whitespace-pre-wrap",
                  l.stream === "stderr" && "text-destructive",
                  l.stream === "system" && "text-sky-600 italic dark:text-sky-400",
                )}
              >
                {needle ? <Highlight text={l.text} needle={needle} /> : l.text || " "}
              </span>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

function Highlight({ text, needle }: { text: string; needle: string }) {
  const parts: ReactNode[] = []
  const lower = text.toLowerCase()
  let from = 0
  for (let i = lower.indexOf(needle); i !== -1; i = lower.indexOf(needle, from)) {
    parts.push(text.slice(from, i), <mark key={i} className="rounded-sm bg-amber-300/70 text-foreground dark:bg-amber-500/50">{text.slice(i, i + needle.length)}</mark>)
    from = i + needle.length
  }
  parts.push(text.slice(from))
  return <>{parts}</>
}
