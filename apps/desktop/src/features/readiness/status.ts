import { CircleCheck, CircleDashed, CircleMinus, CircleX, Info, TriangleAlert, type LucideIcon } from "lucide-react"
import type { EnvironmentReadiness, ReadinessCellId, ReadinessItemStatus, ReadinessStatus } from "@/lib/api/types"

/** Icon, color and screen-reader label of a checklist item (or component cell) status. */
export const ITEM_STATUS: Record<ReadinessItemStatus, { icon: LucideIcon; className: string; label: string }> = {
  pass: { icon: CircleCheck, className: "text-emerald-600 dark:text-emerald-400", label: "Passed" },
  info: { icon: Info, className: "text-sky-600 dark:text-sky-400", label: "Passed with suggestions" },
  warn: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400", label: "Needs work" },
  fail: { icon: CircleX, className: "text-destructive", label: "Failed" },
  todo: { icon: CircleDashed, className: "text-violet-600 dark:text-violet-400", label: "Not tested yet" },
  skip: { icon: CircleMinus, className: "text-muted-foreground/60", label: "Not applicable" },
}

/** Label and colors of an environment verdict (badge and score bar). */
export const ENV_STATUS: Record<ReadinessStatus, { label: string; badge: string; bar: string }> = {
  ready: {
    label: "Ready",
    badge: "bg-emerald-500/10 text-emerald-700 dark:text-emerald-400",
    bar: "*:data-[slot=progress-indicator]:bg-emerald-500",
  },
  needs_work: {
    label: "Needs work",
    badge: "bg-amber-500/10 text-amber-700 dark:text-amber-400",
    bar: "*:data-[slot=progress-indicator]:bg-amber-500",
  },
  blocked: {
    label: "Blocked",
    badge: "bg-destructive/10 text-destructive",
    bar: "*:data-[slot=progress-indicator]:bg-destructive",
  },
}

/** Component table columns, in the order the daemon sends the cells. */
export const CELL_COLUMNS: { id: ReadinessCellId; label: string; hint: string }[] = [
  { id: "rules", label: "Rules", hint: "Best-practice findings on this service or workload" },
  { id: "health", label: "Health", hint: "Healthchecks and restart policies (Docker) or readiness and liveness probes (Kubernetes)" },
  { id: "resources", label: "Resources", hint: "CPU and memory limits (Docker) or requests and limits (Kubernetes)" },
  { id: "monitoring", label: "Monitoring", hint: "Metrics and alerts cover it" },
  { id: "load", label: "Load", hint: "Headroom in the latest load test of this environment" },
  { id: "resilience", label: "Resilience", hint: "Latest verdict of every incident scenario that targeted it" },
  { id: "capacity", label: "Capacity", hint: "Its sizing in the latest capacity plan" },
]

/** Display name of an environment ("" is the base). */
export function envLabel(name: string): string {
  return name === "" ? "Base" : name
}

/**
 * The environment shown first: the one picked before while it still exists, else the first production-like
 * one (what readiness is about), else the active one, else the first.
 */
export function defaultEnvironment(envs: EnvironmentReadiness[], picked: string | null, active: string): string | null {
  if (picked !== null && envs.some((e) => e.name === picked)) return picked
  return (envs.find((e) => e.productionLike) ?? envs.find((e) => e.name === active) ?? envs[0])?.name ?? null
}

/** The item status an environment verdict reads as, for coloring its score like its badge. */
export const ENV_VERDICT: Record<ReadinessStatus, ReadinessItemStatus> = { ready: "pass", needs_work: "warn", blocked: "fail" }

/**
 * Text color of a 0-100 score (null: nothing verified yet), from the verdicts behind it rather than the number: red
 * only when a check failed, amber when one needs work. The number alone overstates it (one warning in a two-check
 * category is 50), and red on something that is not broken reads as an alarm.
 */
export function scoreTone(score: number | null, statuses: readonly ReadinessItemStatus[]): string {
  if (score === null) return "text-muted-foreground"
  if (statuses.includes("fail")) return "text-destructive"
  if (statuses.includes("warn")) return "text-amber-700 dark:text-amber-400"
  return "text-emerald-700 dark:text-emerald-400"
}

/** "83", or an em dash when nothing was verified yet. */
export function scoreLabel(score: number | null): string {
  return score === null ? "—" : String(score)
}

/** Why the confidence is below 100: how many checks are not tested yet, by category title. */
export function untestedSummary(env: EnvironmentReadiness): string {
  const parts = env.groups.filter((g) => g.untested > 0).map((g) => `${g.title.toLowerCase()} ${g.untested}`)
  return parts.length ? `Not tested yet: ${parts.join(", ")}` : "Every applicable check has evidence"
}
