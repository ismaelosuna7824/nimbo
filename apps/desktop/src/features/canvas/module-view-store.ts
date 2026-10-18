import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

interface ModuleViewState {
  /** Module node of the open workflow being viewed; null = overlay closed. */
  nodeId: string | null
  /** Nested module call names below nodeId (module.<a>.module.<b>). */
  path: string[]
  setPath: (path: string[]) => void
  close: () => void
}

export const useModuleView = create<ModuleViewState>((set) => ({
  nodeId: null,
  path: [],
  setPath: (path) => set({ path }),
  close: () => set({ nodeId: null, path: [] }),
}))

/** Opens the read-only diagram of a module call (pending edits are saved first so the source is current). */
export async function openModuleView(nodeId: string) {
  await useWorkflowStore.getState().flush()
  useModuleView.setState({ nodeId, path: [] })
}
