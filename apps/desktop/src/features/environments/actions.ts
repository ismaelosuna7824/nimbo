import { useWorkflowStore } from "@/state/workflow-store"

/** Makes `name` the active environment ("" = base) and saves it: the daemon renders it and deploy/rules/readiness follow. */
export function switchEnvironment(name: string) {
  const next = name === "" ? undefined : name
  useWorkflowStore.getState().update((wf) => ({ ...wf, activeEnvironment: next }))
  void useWorkflowStore.getState().flush()
}
