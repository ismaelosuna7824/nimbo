import { create } from "zustand"
import { useUiStore } from "@/features/toolbar/ui-store"
import { useWorkflowStore } from "@/state/workflow-store"

export type Speed = 0.5 | 1 | 2
export const SPEEDS: Speed[] = [0.5, 1, 2]

/** "request": follow a request along the data flow; "tour": tour the infrastructure. Both run on the Build canvas. */
export type WalkthroughMode = "request" | "tour"

/** How an item shows on the canvas during a walkthrough: in focus (glowing on the tour), just shown, on the path. */
export type NodeMark = "current" | "glow" | "near" | "path"
export type EdgeMark = "current" | "path"

/** Marks keyed by Build canvas IDs. */
export interface Highlight {
  nodes: Record<string, NodeMark>
  edges: Record<string, EdgeMark>
}

/** The editor around the canvas: collapsed while a walkthrough runs, given back as it was when it closes. */
interface Layout {
  bottomOpen: boolean
  inspectorOpen: boolean
  paletteOpen: boolean
}

interface WalkthroughState {
  /** Workflow the walkthrough is open on (null: closed). */
  workflowId: string | null
  mode: WalkthroughMode
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
  /** What was open around the canvas before the walkthrough took the room. */
  layout: Layout | null
}

const CLOSED = { workflowId: null, mode: "request", startedAt: 0, entry: null, choices: {}, step: 0, playing: false, highlight: null, layout: null } satisfies Partial<WalkthroughState>

export const useWalkthrough = create<WalkthroughState>(() => ({ ...CLOSED, speed: 1 }))

/**
 * Opens the walkthrough (`mode`) on the open workflow, from its first step, playing. The canvas gets the room: the
 * dock, the palette and the inspector collapse (a walkthrough already open keeps what it saved).
 */
export function startWalkthrough(mode: WalkthroughMode = "request") {
  const workflowId = useWorkflowStore.getState().workflow?.id
  if (!workflowId) return
  const ui = useUiStore.getState()
  const layout = useWalkthrough.getState().layout ?? { bottomOpen: useWorkflowStore.getState().bottomOpen, inspectorOpen: ui.inspectorOpen, paletteOpen: ui.paletteOpen }
  useWorkflowStore.getState().setBottomOpen(false)
  ui.setInspectorOpen(false)
  ui.setPaletteOpen(false)
  useWalkthrough.setState({ ...CLOSED, workflowId, mode, startedAt: Date.now(), playing: true, layout })
}

/** Closes the walkthrough and gives back the dock, palette and inspector as they were. */
export function closeWalkthrough() {
  const { layout } = useWalkthrough.getState()
  useWalkthrough.setState(CLOSED)
  if (!layout) return
  useWorkflowStore.getState().setBottomOpen(layout.bottomOpen)
  const ui = useUiStore.getState()
  ui.setInspectorOpen(layout.inspectorOpen)
  ui.setPaletteOpen(layout.paletteOpen)
}

/** Back to the first step (same entry and ways), playing. */
export function replayWalkthrough() {
  useWalkthrough.setState({ step: 0, playing: true })
}

/** The mode open on the open workflow (null: none). */
export function useWalkthroughMode(): WalkthroughMode | null {
  const id = useWorkflowStore((s) => s.workflow?.id)
  return useWalkthrough((s) => (!!id && s.workflowId === id ? s.mode : null))
}

/** True while a walkthrough (either mode) is open on the open workflow. */
export function useWalkthroughOpen(): boolean {
  return useWalkthroughMode() !== null
}
