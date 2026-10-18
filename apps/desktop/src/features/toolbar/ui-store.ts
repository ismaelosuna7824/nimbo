import { create } from "zustand"

/** Shell-only UI state that is not part of the workflow contract. */
interface UiState {
  inspectorOpen: boolean
  commandOpen: boolean
  confirmDestroy: boolean
  setInspectorOpen: (open: boolean) => void
  setCommandOpen: (open: boolean) => void
  setConfirmDestroy: (open: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  inspectorOpen: true,
  commandOpen: false,
  confirmDestroy: false,
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setConfirmDestroy: (confirmDestroy) => set({ confirmDestroy }),
}))
