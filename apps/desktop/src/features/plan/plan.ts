import type { PlanAction, PlannedChange, Run } from "@/lib/api/types"

/** The actions a card is marked with; read and no-op are quiet. */
export type MarkedAction = "create" | "update" | "replace" | "delete"

/** Most consequential first: a card with several instances shows the one the user must not miss. */
export const MARKED_ACTIONS: MarkedAction[] = ["replace", "delete", "update", "create"]

/** Order of counts and legends, as Terraform reports them (add, change, destroy). */
export const LEGEND_ACTIONS: MarkedAction[] = ["create", "update", "replace", "delete"]

export function isMarked(action: PlanAction): action is MarkedAction {
  return (MARKED_ACTIONS as PlanAction[]).includes(action)
}

/** What a plan does to one node: its marked instances (several with count/for_each or in a module) and their verdict. */
export interface NodePlan {
  action: MarkedAction
  changes: PlannedChange[]
}

/** Why a plan no longer describes what Deploy will do. */
export type PlanStaleness = "edited" | "deployed"

/** The newest successful plan of an environment (undefined = the base). `runs` is newest first. */
export function latestPlan(runs: Run[], environment: string | undefined): Run | undefined {
  return runs.find((r) => r.action === "plan" && r.status === "succeeded" && (r.environment ?? "") === (environment ?? ""))
}

/**
 * Whether `plan` still describes what Deploy would do. It is outdated once the workflow's content changed after it (the
 * store's version moves on every save except card moves, so a plan started after a drag stays current) or once the
 * sandbox state moved on: an apply or destroy started after it.
 */
export function planStaleness(plan: Run, runs: Run[], version: string): PlanStaleness | null {
  if (!plan.workflowVersion || Date.parse(plan.workflowVersion) < Date.parse(version)) return "edited"
  const at = Date.parse(plan.createdAt)
  if (runs.some((r) => (r.action === "apply" || r.action === "destroy") && Date.parse(r.createdAt) > at)) return "deployed"
  return null
}

/** The plan's marked changes by node id. */
export function planByNode(changes: PlannedChange[]): Map<string, NodePlan> {
  const byNode = new Map<string, PlannedChange[]>()
  for (const c of changes) {
    if (!c.nodeId || !isMarked(c.action)) continue
    const list = byNode.get(c.nodeId)
    if (list) list.push(c)
    else byNode.set(c.nodeId, [c])
  }
  return new Map(
    [...byNode].map(([id, list]) => [id, { action: MARKED_ACTIONS.find((a) => list.some((c) => c.action === a))!, changes: list }]),
  )
}

/** Marked changes of resources no card on the canvas declares, e.g. the delete of a removed node. */
export function offCanvasChanges(changes: PlannedChange[], nodeIds: ReadonlySet<string>): PlannedChange[] {
  return changes.filter((c) => isMarked(c.action) && (!c.nodeId || !nodeIds.has(c.nodeId)))
}

/** Marked instances per action. */
export function countActions(changes: PlannedChange[]): Record<MarkedAction, number> {
  const counts: Record<MarkedAction, number> = { create: 0, update: 0, replace: 0, delete: 0 }
  for (const c of changes) if (isMarked(c.action)) counts[c.action]++
  return counts
}

export const ACTION_META: Record<MarkedAction, { symbol: string; label: string; past: string; verb: string }> = {
  create: { symbol: "+", label: "Create", past: "created", verb: "created" },
  update: { symbol: "~", label: "Update in place", past: "updated", verb: "updated in place" },
  replace: { symbol: "±", label: "Replace", past: "replaced", verb: "replaced (destroyed and created again)" },
  delete: { symbol: "−", label: "Delete", past: "deleted", verb: "deleted" },
}

/**
 * Tones: replace and delete lose the running object (data, addresses, downtime), the consequence Destroy is red for,
 * so they take the destructive tint; create and update are the expected outcome of an edit and stay in the accent /
 * neutral, never green, which reads as "passed". An outdated plan is muted and dashed in every action so it can't be
 * taken for what Deploy will do now.
 */
export function actionTone(action: MarkedAction, stale = false) {
  if (stale) return "border border-dashed border-muted-foreground/50 bg-muted text-muted-foreground"
  switch (action) {
    case "create":
      return "bg-primary/12 text-primary"
    case "update":
      return "border bg-background text-foreground"
    case "replace":
    case "delete":
      return "bg-destructive/12 text-destructive"
  }
}
