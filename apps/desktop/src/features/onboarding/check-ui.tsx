import { CheckCircle2Icon, CircleAlertIcon, CircleDashedIcon, ExternalLinkIcon, PlayIcon, TriangleAlertIcon, XCircleIcon } from "lucide-react"
import type { ReactNode } from "react"
import { errorMessage } from "@/components/errors"
import { openExternal } from "@/components/open-external"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { CodeBlock } from "@/features/settings/common"
import type { SetupCheck, SetupCheckStatus, SetupFix } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useSetupAction } from "./store"

export function Lead({ children }: { children: ReactNode }) {
  return <p className="text-base leading-relaxed text-muted-foreground">{children}</p>
}

export function SetupError({ error }: { error: unknown }) {
  return <p className="text-sm text-destructive">Couldn't run the checks: {errorMessage(error)}</p>
}

const STATUS_META: Record<SetupCheckStatus, { label: string; icon: typeof CheckCircle2Icon; text: string; pill: string; border: string }> = {
  ok: {
    label: "OK",
    icon: CheckCircle2Icon,
    text: "text-emerald-600 dark:text-emerald-400",
    pill: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
    border: "border-emerald-500/40",
  },
  warning: {
    label: "Heads-up",
    icon: TriangleAlertIcon,
    text: "text-amber-600 dark:text-amber-400",
    pill: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
    border: "border-amber-500/40",
  },
  missing: {
    label: "Needs action",
    icon: CircleAlertIcon,
    text: "text-sky-600 dark:text-sky-400",
    pill: "bg-sky-500/15 text-sky-700 dark:text-sky-300",
    border: "border-sky-500/40",
  },
  error: {
    label: "Problem",
    icon: XCircleIcon,
    text: "text-destructive",
    pill: "bg-destructive/10 text-destructive",
    border: "border-destructive/40",
  },
}

export function StatusIcon({ status, className }: { status: SetupCheckStatus | undefined; className?: string }) {
  if (!status) return <CircleDashedIcon className={cn("text-muted-foreground", className)} aria-hidden />
  const meta = STATUS_META[status]
  return <meta.icon className={cn(meta.text, className)} aria-hidden />
}

/** Small live status chip; `checking` while the first answer is pending. */
export function StatusPill({ status, checking }: { status: SetupCheckStatus | undefined; checking?: boolean }) {
  if (!status)
    return (
      <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2.5 py-1 text-xs font-medium text-muted-foreground">
        {checking ? <Spinner className="size-3.5" /> : <CircleDashedIcon className="size-3.5" aria-hidden />}
        Checking…
      </span>
    )
  const meta = STATUS_META[status]
  return (
    <span role="status" className={cn("inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium", meta.pill)}>
      <meta.icon className="size-3.5" aria-hidden />
      {meta.label}
    </span>
  )
}

function ActionFixButton({ fix, primary }: { fix: SetupFix; primary: boolean }) {
  const { run, busy } = useSetupAction(fix.action!)
  return (
    <Button size="sm" variant={primary ? "default" : "outline"} disabled={busy} onClick={run}>
      {busy ? <Spinner /> : <PlayIcon />}
      {fix.label}
    </Button>
  )
}

/** Buttons (links, actions) followed by copyable commands, in the daemon's order. */
export function FixList({ fixes, className }: { fixes: SetupFix[] | undefined; className?: string }) {
  if (!fixes?.length) return null
  const buttons = fixes.filter((f) => f.kind !== "command")
  const commands = fixes.filter((f) => f.kind === "command" && f.command)
  return (
    <div className={cn("flex flex-col gap-3", className)}>
      {buttons.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {buttons.map((f, i) =>
            f.kind === "action" && f.action ? (
              <ActionFixButton key={`${f.kind}:${f.label}`} fix={f} primary={i === 0} />
            ) : f.url ? (
              <Button key={`${f.kind}:${f.label}`} size="sm" variant={i === 0 ? "default" : "outline"} onClick={() => void openExternal(f.url!)}>
                <ExternalLinkIcon />
                {f.label}
              </Button>
            ) : null,
          )}
        </div>
      )}
      {commands.map((f) => (
        <div key={`${f.kind}:${f.label}`} className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted-foreground">{f.label}</span>
          <CodeBlock code={f.command!} label="Copy command" />
        </div>
      ))}
    </div>
  )
}

/** One check as a card: title, live status, summary, explanation and fixes (hidden once it's ok). */
export function CheckCard({ check, children, compact }: { check: SetupCheck; children?: ReactNode; compact?: boolean }) {
  const meta = STATUS_META[check.status]
  const ok = check.status === "ok"
  return (
    <div className={cn("flex flex-col gap-3 rounded-xl border bg-card p-4", !ok && meta.border)} data-check={check.id}>
      <div className="flex items-start gap-3">
        <StatusIcon status={check.status} className="mt-0.5 size-5 shrink-0" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h3 className="text-sm font-semibold">{check.title}</h3>
            {!check.required && <span className="text-xs text-muted-foreground">optional</span>}
          </div>
          {check.summary && <p className="mt-0.5 text-sm text-muted-foreground">{check.summary}</p>}
        </div>
        <StatusPill status={check.status} />
      </div>
      {!ok && !compact && check.detail && <p className="text-sm leading-relaxed">{check.detail}</p>}
      {!ok && <FixList fixes={check.fixes} />}
      {children}
    </div>
  )
}

/** The big "done" banner a step shows once its check passes. */
export function SuccessBanner({ title, summary }: { title: string; summary?: string }) {
  return (
    <div
      role="status"
      className="flex items-center gap-4 rounded-2xl border border-emerald-500/40 bg-emerald-500/10 p-5 motion-safe:animate-in motion-safe:fade-in-0 motion-safe:zoom-in-95"
    >
      <span className="flex size-12 shrink-0 items-center justify-center rounded-full bg-emerald-500 text-white">
        <CheckCircle2Icon className="size-7" aria-hidden />
      </span>
      <div className="min-w-0">
        <p className="font-heading text-lg font-semibold text-emerald-700 dark:text-emerald-300">{title}</p>
        {summary && <p className="text-sm text-emerald-800/80 dark:text-emerald-200/80">{summary}</p>}
      </div>
    </div>
  )
}
