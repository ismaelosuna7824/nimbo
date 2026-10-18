import { CircleCheck, CircleDashed, CircleX } from "lucide-react"
import type { ConnectionCheck } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { formatMs, formatRelative } from "../panels/lib/format"
import type { StoredCheck } from "./store"

/** Compact last "Test connection" result: ok · latency · message, or the failure. */
export function CheckStatus({ check, className }: { check: StoredCheck | undefined; className?: string }) {
  if (!check) {
    return (
      <span className={cn("flex items-center gap-1 text-xs text-muted-foreground", className)}>
        <CircleDashed className="size-3.5 shrink-0" /> Not tested yet
      </span>
    )
  }
  return (
    <span
      className={cn("flex min-w-0 items-center gap-1 text-xs", check.ok ? "text-emerald-700 dark:text-emerald-400" : "text-destructive", className)}
      title={check.detail ?? check.message}
    >
      {check.ok ? <CircleCheck className="size-3.5 shrink-0" /> : <CircleX className="size-3.5 shrink-0" />}
      <span className="truncate">
        {check.ok ? `${check.message} · ${formatMs(check.latencyMs)}` : check.message || "Failed"}
      </span>
      <span className="shrink-0 text-muted-foreground">· {formatRelative(check.at)}</span>
    </span>
  )
}

/** Full result of a check, shown inline under the Test button. */
export function CheckResult({ check }: { check: ConnectionCheck }) {
  return (
    <div
      role="status"
      className={cn(
        "flex gap-2 rounded-lg border p-2.5 text-xs",
        check.ok ? "border-emerald-500/40 bg-emerald-500/5" : "border-destructive/40 bg-destructive/5",
      )}
    >
      {check.ok ? (
        <CircleCheck className="mt-0.5 size-4 shrink-0 text-emerald-600 dark:text-emerald-400" />
      ) : (
        <CircleX className="mt-0.5 size-4 shrink-0 text-destructive" />
      )}
      <div className="min-w-0">
        <p className="font-medium">
          {check.ok ? "Connected" : "Connection failed"} · {formatMs(check.latencyMs)}
        </p>
        {check.message && <p className="text-muted-foreground">{check.message}</p>}
        {check.detail && <pre className="mt-1 font-mono text-[11px] break-all whitespace-pre-wrap text-destructive">{check.detail}</pre>}
      </div>
    </div>
  )
}
