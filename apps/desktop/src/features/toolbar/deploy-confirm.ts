import type { DestructiveChange } from "@/lib/api/types"

export interface DestructiveGroups {
  /** Destroyed and re-created. */
  replace: DestructiveChange[]
  /** Destroyed for good. */
  delete: DestructiveChange[]
}

/** Splits what a deploy would destroy into replaced and deleted instances, each in address order. */
export function groupDestructive(changes: DestructiveChange[]): DestructiveGroups {
  const sorted = [...changes].sort((a, b) => (a.address < b.address ? -1 : a.address > b.address ? 1 : 0))
  return {
    replace: sorted.filter((c) => c.action === "replace"),
    delete: sorted.filter((c) => c.action === "delete"),
  }
}

/** The confirm button: "Deploy and replace 2", "Deploy and delete 1" or "Deploy and destroy 3" (both kinds). */
export function confirmLabel(groups: DestructiveGroups): string {
  const n = groups.replace.length + groups.delete.length
  const verb = groups.delete.length === 0 ? "replace" : groups.replace.length === 0 ? "delete" : "destroy"
  return `Deploy and ${verb} ${n}`
}

const CAUSE: Record<string, string> = {
  delete_because_no_resource_config: "removed from the design",
  delete_because_no_module: "its module was removed",
  delete_because_count_index: "count no longer has this instance",
  delete_because_each_key: "for_each no longer has this instance",
  delete_because_wrong_repetition: "count/for_each changed how it is repeated",
  delete_because_no_move_target: "its moved block has no target",
  replace_because_tainted: "tainted",
  replace_by_request: "replacement requested",
  replace_by_triggers: "replace_triggered_by changed",
  replace_because_cannot_update: "a setting that can't change in place changed",
}

/**
 * Why an instance is destroyed, in words: replaced ones name the attributes forcing it ("forced by: name, engine"),
 * the rest translate Terraform's action_reason.
 */
export function causeLabel(change: DestructiveChange): string {
  if (change.action === "replace" && change.reasons && change.reasons.length > 0) {
    return `forced by: ${change.reasons.join(", ")}`
  }
  if (change.cause && CAUSE[change.cause]) return CAUSE[change.cause]
  if (change.cause) return change.cause.replaceAll("_", " ")
  return change.action === "replace" ? "must be re-created" : "no longer in the design"
}
