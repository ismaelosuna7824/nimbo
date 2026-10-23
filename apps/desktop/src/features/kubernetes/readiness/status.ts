import { CircleCheck, CircleDashed, CircleMinus, CircleX, Info, TriangleAlert, type LucideIcon } from "lucide-react"
import type { EnvironmentReadiness, ReadinessItemStatus, ReadinessStatus } from "@/lib/api/types"

/** Icon, color and screen-reader label of a checklist item status. */
export const ITEM_STATUS: Record<ReadinessItemStatus, { icon: LucideIcon; className: string; label: string }> = {
  pass: { icon: CircleCheck, className: "text-emerald-600 dark:text-emerald-400", label: "Passed" },
  info: { icon: Info, className: "text-sky-600 dark:text-sky-400", label: "Passed with suggestions" },
  warn: { icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400", label: "Needs work" },
  fail: { icon: CircleX, className: "text-destructive", label: "Blocking" },
  todo: { icon: CircleDashed, className: "text-violet-600 dark:text-violet-400", label: "Not verified yet" },
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

/** Display name of an environment ("" is the base of a workflow without environments). */
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
