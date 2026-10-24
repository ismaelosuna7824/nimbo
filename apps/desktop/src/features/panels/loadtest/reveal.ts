import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

/** A request to show part of the Load test tab: a run's results, a new test, or the capacity planner. */
export interface LoadTestRequest {
  workflowId: string
  /** Run to show (results), or to size from (capacity); absent opens "New test" / the newest run. */
  runId?: string
  view?: "capacity"
  /** Prefills "New test" with this incident scenario (and target node id). */
  scenario?: { id: string; target?: string }
}

export const useLoadTestRequest = create<{ request: LoadTestRequest | null }>(() => ({ request: null }))

/** Opens the Load test tab of the open workflow on run `runId`'s results, or on a new test without one. */
export function revealLoadTest(runId?: string) {
  const store = useWorkflowStore.getState()
  if (!store.workflow) return
  useLoadTestRequest.setState({ request: { workflowId: store.workflow.id, runId } })
  store.showBottom("loadtest")
}

/** Opens the capacity planner of the open workflow, sized from `runId` (absent: the newest usable run). */
export function revealCapacity(runId?: string) {
  const store = useWorkflowStore.getState()
  if (!store.workflow) return
  useLoadTestRequest.setState({ request: { workflowId: store.workflow.id, runId, view: "capacity" } })
  store.showBottom("loadtest")
}

/** Opens "New test" of the open workflow with incident scenario `id` (on `target`) preselected. */
export function revealScenario(id: string, target?: string) {
  const store = useWorkflowStore.getState()
  if (!store.workflow) return
  useLoadTestRequest.setState({ request: { workflowId: store.workflow.id, scenario: { id, target } } })
  store.showBottom("loadtest")
}
