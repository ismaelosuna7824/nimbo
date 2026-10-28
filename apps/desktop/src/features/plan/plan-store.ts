import { useQuery } from "@tanstack/react-query"
import { useEffect, useMemo } from "react"
import { create } from "zustand"
import { api } from "@/lib/api/client"
import type { PlannedChange, Run } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { switchView } from "../architecture/store"
import {
  countActions,
  latestPlan,
  offCanvasChanges,
  planByNode,
  planStaleness,
  type MarkedAction,
  type NodePlan,
  type PlanStaleness,
} from "./plan"

/** The latest plan of the open workflow's active environment, mapped onto its canvas. */
export interface PlanOverlay {
  run: Run
  stale: PlanStaleness | null
  byNode: Map<string, NodePlan>
  offCanvas: PlannedChange[]
  counts: Record<MarkedAction, number>
}

interface PlanOverlayState {
  /** Workflow whose canvas shows its latest plan: opt-in, until dismissed. */
  shownFor: string | null
  /** The plan the canvas shows, published by usePlanOverlay while shown (cards and the inspector read it). */
  overlay: PlanOverlay | null
}

export const usePlanOverlayStore = create<PlanOverlayState>(() => ({ shownFor: null, overlay: null }))

/** Shows the latest plan on the Build canvas of the open workflow. */
export function showPlanOnCanvas() {
  const id = useWorkflowStore.getState().workflow?.id
  if (!id) return
  usePlanOverlayStore.setState({ shownFor: id })
  switchView("build")
}

export function hidePlanFromCanvas() {
  usePlanOverlayStore.setState({ shownFor: null, overlay: null })
}

/** The node's planned change while the plan is shown. */
export function useNodePlan(nodeId: string): { plan: NodePlan; stale: boolean } | undefined {
  const plan = usePlanOverlayStore((s) => s.overlay?.byNode.get(nodeId))
  const stale = usePlanOverlayStore((s) => s.overlay?.stale != null)
  return plan && { plan, stale }
}

/** Run details never change once the run ended. */
const FOREVER = Infinity

/**
 * Keeps the store's overlay in step with the latest plan while it is shown and returns what the canvas chip needs:
 * whether it is shown, the plan run (undefined: none for this environment), and its overlay once its details loaded.
 */
export function usePlanOverlay() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const shownFor = usePlanOverlayStore((s) => s.shownFor)
  const shown = shownFor !== null && shownFor === workflowId
  const environment = useWorkflowStore((s) => s.workflow?.activeEnvironment)
  // Joined so that edits which keep the set of cards don't recompute the overlay.
  const nodeIds = useWorkflowStore((s) => s.workflow?.nodes.map((n) => n.id).join("\n") ?? "")
  const version = useWorkflowStore((s) => s.version)
  const runs = useWorkflowStore((s) => s.runs)
  const run = latestPlan(runs, environment)
  const details = useQuery({
    queryKey: ["run", run?.id],
    queryFn: () => api.run(run!.id),
    enabled: shown && run !== undefined,
    staleTime: FOREVER,
  })
  const changes = details.data?.id === run?.id ? details.data?.changes : undefined
  // Kept apart so cards keep their NodePlan objects (and skip re-rendering) while only staleness or the runs change.
  const byNode = useMemo(() => planByNode(changes ?? []), [changes])
  const stale = run ? planStaleness(run, runs, version) : null
  const overlay = useMemo<PlanOverlay | null>(() => {
    if (!run || !changes) return null
    return {
      run,
      stale,
      byNode,
      offCanvas: offCanvasChanges(changes, new Set(nodeIds.split("\n"))),
      counts: countActions(changes),
    }
  }, [run, changes, stale, byNode, nodeIds])

  useEffect(() => {
    usePlanOverlayStore.setState({ overlay: shown ? overlay : null })
  }, [shown, overlay])
  // Another workflow opened: its canvas starts without a plan.
  useEffect(() => {
    if (usePlanOverlayStore.getState().shownFor !== workflowId) hidePlanFromCanvas()
  }, [workflowId])

  return { shown, run, overlay, loading: details.isPending && details.fetchStatus !== "idle", failed: details.isError }
}
