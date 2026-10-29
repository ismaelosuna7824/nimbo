import { create } from "zustand"
import { MOD_KEY } from "@/features/toolbar/actions"
import { useWorkflowStore } from "@/state/workflow-store"
import { closeWalkthrough } from "@/features/walkthrough/store"
import type { Scene } from "./use-scene"

/** Shortcut that switches between Build and Architecture. */
export const VIEW_SHORTCUT = [MOD_KEY, "⇧", "A"]

/** The Architecture view publishes its current scene for the image export. */
export function registerArchScene(scene: Scene | null) {
  useArchUi.setState({ scene })
}

export type CanvasView = "build" | "architecture"
/** What the image export renders: the Architecture diagram, or the Build canvas as a PNG. */
export type ExportTarget = "architecture" | "build"

const viewKey = (workflowId: string) => `nimbo.view.${workflowId}`

interface ArchUiState {
  /** View per workflow ID; read from localStorage on first use. */
  views: Record<string, CanvasView>
  exportTarget: ExportTarget | null
  /** The open Architecture view's scene (null while it is not shown or still laid out). */
  scene: Scene | null
  setView: (workflowId: string, view: CanvasView) => void
  openExport: (target: ExportTarget) => void
  closeExport: () => void
}

export const useArchUi = create<ArchUiState>((set, get) => ({
  views: {},
  exportTarget: null,
  scene: null,
  setView(workflowId, view) {
    localStorage.setItem(viewKey(workflowId), view)
    set({ views: { ...get().views, [workflowId]: view } })
  },
  openExport: (exportTarget) => set({ exportTarget }),
  closeExport: () => set({ exportTarget: null }),
}))

/** The open workflow's canvas view (remembered per workflow). */
export function useCanvasView(): CanvasView {
  const id = useWorkflowStore((s) => s.workflow?.id)
  const stored = useArchUi((s) => (id ? s.views[id] : undefined))
  if (!id) return "build"
  return stored ?? (localStorage.getItem(viewKey(id)) === "architecture" ? "architecture" : "build")
}

/** Switches the open workflow between Build and Architecture (or to `view`). Walkthroughs run on Build: leaving it closes one. */
export function switchView(view?: CanvasView) {
  const id = useWorkflowStore.getState().workflow?.id
  if (!id) return
  const ui = useArchUi.getState()
  const current = ui.views[id] ?? (localStorage.getItem(viewKey(id)) === "architecture" ? "architecture" : "build")
  const next = view ?? (current === "build" ? "architecture" : "build")
  if (next === "architecture") closeWalkthrough()
  ui.setView(id, next)
}
