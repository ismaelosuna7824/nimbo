import { useEffect, useEffectEvent } from "react"
import { useWorkflowStore } from "@/state/workflow-store"

/**
 * Shows the run the toolbar's deploy status or a run toast asked for (`showRun`) in a Deploy tab, once: `select`
 * picks it and switches to its log.
 */
export function useRequestedRun(select: (runId: string) => void) {
  const requested = useWorkflowStore((s) => s.deployRun)
  const onRequest = useEffectEvent(select)
  useEffect(() => {
    if (!requested) return
    onRequest(requested)
    useWorkflowStore.setState({ deployRun: null })
  }, [requested])
}
