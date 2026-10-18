import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/spinner"
import type { LoadTestStatus, RunStatus, SandboxState } from "@/lib/api/types"
import { cn } from "@/lib/utils"

type Tone = "ok" | "bad" | "busy" | "idle" | "warn"

const TONE_CLASS: Record<Tone, string> = {
  ok: "border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
  bad: "border-destructive/30 bg-destructive/10 text-destructive",
  busy: "border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-400",
  warn: "border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-400",
  idle: "border-border bg-muted text-muted-foreground",
}

const STATUS: Record<RunStatus | SandboxState | LoadTestStatus, { tone: Tone; label: string }> = {
  queued: { tone: "busy", label: "Queued" },
  running: { tone: "busy", label: "Running" },
  succeeded: { tone: "ok", label: "Succeeded" },
  failed: { tone: "bad", label: "Failed" },
  canceled: { tone: "idle", label: "Canceled" },
  absent: { tone: "idle", label: "Not created" },
  starting: { tone: "busy", label: "Starting" },
  stopped: { tone: "idle", label: "Stopped" },
  error: { tone: "bad", label: "Error" },
  passed: { tone: "ok", label: "Passed" },
}

export function StatusBadge({
  status,
  label,
  className,
}: {
  status: RunStatus | SandboxState | LoadTestStatus
  /** Overrides the default label (e.g. "Running" vs "Live"). */
  label?: string
  className?: string
}) {
  const s = STATUS[status]
  return (
    <Badge variant="outline" className={cn("h-5 gap-1 px-1.5 text-[11px]", TONE_CLASS[s.tone], className)}>
      {s.tone === "busy" && <Spinner className="size-3" />}
      {label ?? s.label}
    </Badge>
  )
}
