import type { Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/**
 * Mutates a draft copy of the open workflow in place and schedules a save.
 * For the variables, outputs, locals and environments editors: the draft
 * shares the canvas graph (nodes, edges) and the derived, read-only fields
 * with the open workflow, so `mutate` must not touch those.
 */
export function editWorkflow(mutate: (draft: Workflow) => void): void {
  useWorkflowStore.getState().update((current) => {
    const { nodes, edges, clouds, diagnostics, ...rest } = current
    const draft: Workflow = { ...structuredClone(rest), nodes, edges, clouds, diagnostics }
    mutate(draft)
    return draft
  })
}
