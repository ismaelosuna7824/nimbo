import { CircleCheck, CircleMinus, CircleX, Flame, Info, TriangleAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ScenarioId, ScenarioResult, ScenarioVerdict } from "@/lib/api/types"
import { cn } from "@/lib/utils"

/** Short names of the scenarios (badges, chart markers). */
export const SCENARIO_LABEL: Record<ScenarioId, string> = {
  kill_replica: "Kill a replica",
  dependency_down: "Dependency down",
  dependency_latency: "Slow dependency",
  cache_flush: "Cache flush",
  traffic_spike: "Traffic spike",
}

/** What the injection marker on the charts says. */
export const INJECT_MARK: Record<ScenarioId, string> = {
  kill_replica: "kill",
  dependency_down: "down",
  dependency_latency: "slow",
  cache_flush: "flush",
  traffic_spike: "spike",
}

/** Kill and flush act once: the window after them is only observed. */
export const instantScenario = (id: ScenarioId) => id === "kill_replica" || id === "cache_flush"

export const VERDICT_LABEL: Record<ScenarioVerdict, string> = { survived: "Survived", degraded: "Degraded", failed: "Failed", inconclusive: "Not assessed" }
const VERDICT_BADGE: Record<ScenarioVerdict, string> = {
  survived: "border-emerald-500/30 text-emerald-700 dark:text-emerald-400",
  degraded: "border-amber-500/40 text-amber-700 dark:text-amber-400",
  failed: "border-destructive/40 text-destructive",
  inconclusive: "text-muted-foreground",
}
const VERDICT_ICON = { survived: CircleCheck, degraded: TriangleAlert, failed: CircleX, inconclusive: CircleMinus }
const VERDICT_TEXT = {
  survived: "text-emerald-600 dark:text-emerald-400",
  degraded: "text-amber-600 dark:text-amber-400",
  failed: "text-destructive",
  inconclusive: "text-muted-foreground",
}

/** The verdict as a small badge (results header, history rows). */
export function ScenarioBadge({ scenario, className }: { scenario: ScenarioResult; className?: string }) {
  const v = scenario.verdict
  return (
    <Badge
      variant="outline"
      className={cn("h-4 gap-0.5 px-1 text-[10px]", v ? VERDICT_BADGE[v] : "text-muted-foreground", className)}
      title={scenario.message || SCENARIO_LABEL[scenario.id]}
      data-testid="loadtest-scenario-badge"
      data-verdict={v || "pending"}
    >
      <Flame className="size-2.5" />
      {SCENARIO_LABEL[scenario.id]}
      {v && ` · ${VERDICT_LABEL[v]}`}
    </Badge>
  )
}

/** The scenario's verdict, its one-line message (numbers and fix) and the checks behind it. */
export function ScenarioCard({ scenario, running }: { scenario: ScenarioResult; running: boolean }) {
  const v = scenario.verdict
  const Icon = v ? VERDICT_ICON[v] : Flame
  const times = [
    `injected at ${scenario.injectedAtSec}s`,
    scenario.revertedAtSec !== undefined && (instantScenario(scenario.id) ? `window ended at ${scenario.revertedAtSec}s` : `reverted at ${scenario.revertedAtSec}s`),
    scenario.recoveredAtSec !== undefined && `recovered at ${scenario.recoveredAtSec}s`,
  ].filter(Boolean)
  return (
    <div className="min-w-0 rounded-lg border p-2" data-testid="loadtest-scenario" data-verdict={v || "pending"}>
      <div className="mb-1 flex flex-wrap items-center gap-1 text-[11px] font-medium text-muted-foreground">
        <Flame className="size-3" /> Incident: {SCENARIO_LABEL[scenario.id]}
        {scenario.targetName && <span className="font-mono">· {scenario.targetName}</span>}
        <span className="font-normal tabular-nums">· {times.join(", ")}</span>
        {scenario.detail && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Info className="size-3 cursor-help" aria-label="What was done" />
            </TooltipTrigger>
            <TooltipContent className="max-w-72">{scenario.detail}</TooltipContent>
          </Tooltip>
        )}
      </div>
      <div className="flex gap-1.5 text-xs">
        {v ? <Icon className={cn("mt-0.5 size-3.5 shrink-0", VERDICT_TEXT[v])} /> : running ? <Spinner className="mt-0.5 size-3.5" /> : <Icon className="mt-0.5 size-3.5 shrink-0" />}
        <p className="min-w-0 text-pretty">
          {v ? scenario.message : scenario.message || (running ? "Fault injected; the verdict comes when the run ends." : "No verdict.")}
        </p>
      </div>
      {scenario.checks.length > 0 && (
        <ul className="mt-1.5 grid grid-cols-1 gap-x-3 gap-y-1 border-t pt-1.5 @3xl:grid-cols-2">
          {scenario.checks.map((c) => (
            <li key={c.name} className="flex gap-1.5 text-[11px]" data-check={c.name} data-passed={c.passed}>
              {c.passed ? (
                <CircleCheck className="mt-px size-3 shrink-0 text-emerald-600 dark:text-emerald-400" aria-label="passed" />
              ) : (
                <CircleX className="mt-px size-3 shrink-0 text-destructive" aria-label="failed" />
              )}
              <span className="min-w-0">
                <span className="font-medium">{c.name}</span> <span className="text-muted-foreground">{c.detail}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}
