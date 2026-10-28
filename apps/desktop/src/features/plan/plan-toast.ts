import { useEffect } from "react"
import { toast } from "sonner"
import type { PlanSummary } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { showPlanOnCanvas, usePlanOverlayStore } from "./plan-store"

function describe(p: PlanSummary | undefined): string | null {
  if (!p) return null
  const parts = [
    p.add > 0 && `${p.add} to add`,
    p.change > 0 && `${p.change} to change`,
    p.destroy > 0 && `${p.destroy} to destroy`,
  ].filter(Boolean)
  return parts.length ? parts.join(", ") : null
}

/**
 * After a plan this window watched succeed, offers to show it on the canvas (the overlay is opt-in). Quiet when the
 * canvas already shows plans: it switches to the new one by itself.
 */
export function usePlanToast() {
  useEffect(
    () =>
      useWorkflowStore.subscribe((state, prev) => {
        const wf = state.workflow
        if (!wf || wf.id !== prev.workflow?.id || state.runs === prev.runs) return
        const finished = state.runs.find((r) => {
          if (r.action !== "plan" || r.status !== "succeeded") return false
          const before = prev.runs.find((p) => p.id === r.id)?.status
          return before === "queued" || before === "running"
        })
        if (!finished || usePlanOverlayStore.getState().shownFor === wf.id) return
        const changes = describe(finished.plan)
        if (!changes) {
          toast("Plan ready: no changes", { description: "Deploy has nothing to do." })
          return
        }
        toast("Plan ready", {
          description: `${changes}. See which resources change, right on the canvas.`,
          duration: 10_000,
          action: { label: "Show on canvas", onClick: showPlanOnCanvas },
        })
      }),
    [],
  )
}
