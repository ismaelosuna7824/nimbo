import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

/** A request to show part of the Load test tab: a run's results, or a new test. */
export interface LoadTestRequest {
  workflowId: string
  /** Run to show; absent opens "New test". */
  runId?: string
}

export const useLoadTestRequest = create<{ request: LoadTestRequest | null }>(() => ({ request: null }))

/** Opens the Load test tab of the open workflow on run `runId`'s results, or on a new test without one. */
export function revealLoadTest(runId?: string) {
  const store = useWorkflowStore.getState()
  if (!store.workflow) return
  useLoadTestRequest.setState({ request: { workflowId: store.workflow.id, runId } })
  store.showBottom("loadtest")
}
