import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

export type Speed = 0.5 | 1 | 2
export const SPEEDS: Speed[] = [0.5, 1, 2]

/** How an item shows on the canvas during a walkthrough. */
export type NodeMark = "current" | "near" | "path"
export type EdgeMark = "current" | "path"

/** Marks keyed by the IDs of the canvas the walkthrough runs on (Build cards or Architecture items). */
export interface Highlight {
  nodes: Record<string, NodeMark>
  edges: Record<string, EdgeMark>
}

interface WalkthroughState {
  /** Workflow the walkthrough is open on (null: closed). */
  workflowId: string | null
  /** When it was opened (ms): the live traffic panel and the walkthrough share the canvas bottom, the newer one shows. */
  startedAt: number
  /** Chosen entry point (its item ID); null until picked when there are several. */
  entry: string | null
  /** Fork item ID → the way taken (an item ID, or SHOW_ALL). */
  choices: Record<string, string>
  /** Index of the step shown; the walk's length shows the summary. */
  step: number
  playing: boolean
  speed: Speed
  /** What the canvas shows (written by the walkthrough layer, read by the canvases). */
  highlight: Highlight | null
}

const CLOSED = { workflowId: null, startedAt: 0, entry: null, choices: {}, step: 0, playing: false, highlight: null } satisfies Partial<WalkthroughState>

export const useWalkthrough = create<WalkthroughState>(() => ({ ...CLOSED, speed: 1 }))

/** Opens the walkthrough on the open workflow, from its first step, playing. */
export function startWalkthrough() {
  const workflowId = useWorkflowStore.getState().workflow?.id
  if (!workflowId) return
  useWalkthrough.setState({ ...CLOSED, workflowId, startedAt: Date.now(), playing: true })
}

export function closeWalkthrough() {
  useWalkthrough.setState(CLOSED)
}

/** Back to the first step (same entry and ways), playing. */
export function replayWalkthrough() {
  useWalkthrough.setState({ step: 0, playing: true })
}

/** True while the walkthrough is open on the open workflow. */
export function useWalkthroughOpen(): boolean {
  const id = useWorkflowStore((s) => s.workflow?.id)
  return useWalkthrough((s) => !!id && s.workflowId === id)
}
