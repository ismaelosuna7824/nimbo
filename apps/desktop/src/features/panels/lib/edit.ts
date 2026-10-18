import type { Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** Mutates a draft copy of the open workflow in place and schedules a save. */
export function editWorkflow(mutate: (draft: Workflow) => void): void {
  useWorkflowStore.getState().update((draft) => {
    mutate(draft)
    return draft
  })
}
