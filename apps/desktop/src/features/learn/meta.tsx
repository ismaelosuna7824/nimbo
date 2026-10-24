import { CheckCircle2Icon, CircleDashedIcon, CircleDotIcon } from "lucide-react"
import type { ChallengeProgress, LearnProviderId } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { PROVIDER_META } from "./store"

export function ProviderDot({ provider, className }: { provider: LearnProviderId; className?: string }) {
  return <span aria-hidden className={cn("inline-block size-2 shrink-0 rounded-full", PROVIDER_META[provider].dot, className)} />
}

/** Progress badge of a challenge: completed, in progress or not started. */
export function ProgressBadge({ progress, compact, className }: { progress: ChallengeProgress; compact?: boolean; className?: string }) {
  if (progress.status === "completed") {
    return (
      <span className={cn("inline-flex items-center gap-1 text-xs font-medium text-emerald-600 dark:text-emerald-400", className)}>
        <CheckCircle2Icon className="size-3.5" />
        {!compact && "Completed"}
        {compact && <span className="sr-only">Completed</span>}
      </span>
    )
  }
  if (progress.status === "in_progress") {
    return (
      <span className={cn("inline-flex items-center gap-1 text-xs font-medium text-amber-600 dark:text-amber-400", className)}>
        <CircleDotIcon className="size-3.5" />
        {!compact && (progress.attempts > 0 ? `In progress · ${progress.attempts} ${progress.attempts === 1 ? "check" : "checks"}` : "In progress")}
        {compact && <span className="sr-only">In progress</span>}
      </span>
    )
  }
  return (
    <span className={cn("inline-flex items-center gap-1 text-xs text-muted-foreground", className)}>
      <CircleDashedIcon className="size-3.5" />
      {!compact && "Not started"}
      {compact && <span className="sr-only">Not started</span>}
    </span>
  )
}
