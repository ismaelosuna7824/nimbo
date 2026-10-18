import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { RunAction } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

const RUN_LABEL: Record<RunAction, string> = {
  validate: "Validation",
  plan: "Plan",
  apply: "Deploy",
  destroy: "Destroy",
}

/**
 * Saves pending edits, starts a Terraform run in the sandbox and shows its log
 * (unless showLog is false, e.g. when started from the load test tab).
 */
export async function runAction(action: RunAction, { showLog = true }: { showLog?: boolean } = {}) {
  const store = useWorkflowStore.getState()
  const wf = store.workflow
  if (!wf) return
  await store.flush()
  try {
    const run = await api.startRun(wf.id, action, useWorkflowStore.getState().workflow?.activeEnvironment)
    const s = useWorkflowStore.getState()
    if (!s.runs.some((r) => r.id === run.id)) useWorkflowStore.setState({ runs: [run, ...s.runs] })
    if (showLog) store.showBottom("deploy")
  } catch (err) {
    toast.error(`${RUN_LABEL[action]} couldn't start`, { description: errorMessage(err) })
  }
}

export async function sandboxAction(action: "start" | "stop" | "reset") {
  const wf = useWorkflowStore.getState().workflow
  if (!wf) return
  try {
    const status = await api.sandboxAction(wf.id, action)
    if (useWorkflowStore.getState().workflow?.id === wf.id) useWorkflowStore.setState({ sandbox: status })
    if (action === "reset") {
      toast.success("Sandbox reset", { description: "Emulator data and Terraform state were wiped." })
      void useWorkflowStore.getState().refreshRuntime()
    }
  } catch (err) {
    toast.error(`Couldn't ${action} the sandbox`, { description: errorMessage(err) })
  }
}

/** True while a Terraform run of the open workflow is queued or running. */
export function useRunBusy() {
  return useWorkflowStore((s) => s.runs.some((r) => r.status === "queued" || r.status === "running"))
}

export async function openWorkflow(id: string) {
  try {
    await useWorkflowStore.getState().open(id)
  } catch (err) {
    toast.error("Couldn't open the workflow", { description: errorMessage(err) })
  }
}

const isMac = typeof navigator !== "undefined" && /Mac/i.test(navigator.platform)
/** Modifier key label for shortcut hints. */
export const MOD_KEY = isMac ? "⌘" : "Ctrl"
