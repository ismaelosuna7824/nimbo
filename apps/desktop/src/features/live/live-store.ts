import { create } from "zustand"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import { switchView } from "@/features/architecture/store"
import type { EdgeLive, FaultLive, NodeLive } from "./traffic"

/**
 * What the canvas shows of the running load test, keyed by node and edge id. The overlay rewrites it on every
 * sample, keeping the objects of entries that did not change, and each card or edge subscribes to its own entry:
 * a sample re-renders the cards whose numbers changed, not the canvas.
 */
interface LiveState {
  /** Chips and moving edges are shown (a run is on screen and was not dismissed). */
  active: boolean
  nodes: Record<string, NodeLive>
  edges: Record<string, EdgeLive>
  /** The incident of the run on screen (shown even when the traffic is dismissed: a node is really broken). */
  fault: FaultLive | null
}

export const EMPTY_LIVE: LiveState = { active: false, nodes: {}, edges: {}, fault: null }

export const useLiveStore = create<LiveState>(() => EMPTY_LIVE)

export function useNodeLive(id: string): NodeLive | undefined {
  return useLiveStore((s) => (s.active ? s.nodes[id] : undefined))
}

/** An edge's traffic: undefined when it carries none or no run is shown. */
export function useEdgeLive(id: string): EdgeLive | undefined {
  return useLiveStore((s) => (s.active ? s.edges[id] : undefined))
}

/** A run's traffic is on the canvas: edges show measured traffic only (not the running-containers animation). */
export function useTrafficShown(): boolean {
  return useLiveStore((s) => s.active)
}

export function useNodeFault(id: string): FaultLive | undefined {
  return useLiveStore((s) => (s.fault?.nodeId === id ? s.fault : undefined))
}

/** Stops the run; the daemon reverts its fault at once. */
export async function stopRun(runId: string) {
  try {
    await api.stopLoadTest(runId)
  } catch (err) {
    toast.error("Could not stop the test", { description: errorMessage(err) })
  }
}

/** The node the guided failure-test dialog is open on (null: closed). */
export const useFailureTest = create<{ nodeId: string | null }>(() => ({ nodeId: null }))

/** Opens the failure-test dialog on a service or workload, on the Build canvas where its impact shows. */
export function openFailureTest(nodeId: string) {
  switchView("build")
  useFailureTest.setState({ nodeId })
}
