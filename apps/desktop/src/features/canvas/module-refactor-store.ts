import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

/** Which module refactoring dialog is open. */
interface ModuleRefactorState {
  /** Nodes to extract into a new module; null = extract dialog closed. */
  extract: string[] | null
  /** Module call to inline; null = inline dialog closed. */
  inline: string | null
}

export const useModuleRefactor = create<ModuleRefactorState>(() => ({ extract: null, inline: null }))

/** Opens "Extract to module" for these node IDs (pending edits are saved first). */
export async function openExtractModule(nodeIds: string[]) {
  if (nodeIds.length === 0) return
  await useWorkflowStore.getState().flush()
  useModuleRefactor.setState({ extract: nodeIds })
}

/** Opens the "Inline module" confirmation for a module call node (pending edits are saved first). */
export async function openInlineModule(nodeId: string) {
  await useWorkflowStore.getState().flush()
  useModuleRefactor.setState({ inline: nodeId })
}
