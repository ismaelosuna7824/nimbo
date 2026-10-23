import { create } from "zustand"

/** Shell-only UI state that is not part of the workflow contract. */
interface UiState {
  inspectorOpen: boolean
  commandOpen: boolean
  confirmDestroy: boolean
  /** Docker workspaces: the "Convert to Kubernetes" dialog (toolbar button, command palette). */
  convertKubernetes: boolean
  setInspectorOpen: (open: boolean) => void
  setCommandOpen: (open: boolean) => void
  setConfirmDestroy: (open: boolean) => void
  setConvertKubernetes: (open: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  inspectorOpen: true,
  commandOpen: false,
  confirmDestroy: false,
  convertKubernetes: false,
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setConfirmDestroy: (confirmDestroy) => set({ confirmDestroy }),
  setConvertKubernetes: (convertKubernetes) => set({ convertKubernetes }),
}))
