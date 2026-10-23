import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import type { Workflow } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/**
 * Runs an environment change on the daemon (it writes overlay dirs / values files) after saving pending edits, and
 * puts the saved workflow into the store so the canvas, rules and readiness follow. False on failure (toast shown).
 */
export async function changeEnvironments(
  run: (workflowId: string) => Promise<Workflow>,
  messages: { success?: string; failure: string },
): Promise<boolean> {
  const store = useWorkflowStore.getState()
  const wf = store.workflow
  if (!wf) return false
  try {
    await store.flush()
    useWorkflowStore.getState().setWorkflow(await run(wf.id))
    if (messages.success) toast.success(messages.success)
    return true
  } catch (err) {
    toast.error(messages.failure, { description: errorMessage(err) })
    return false
  }
}
