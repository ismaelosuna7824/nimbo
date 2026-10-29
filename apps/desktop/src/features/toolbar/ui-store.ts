import { create } from "zustand"

/** Shell-only UI state that is not part of the workflow contract. */
interface UiState {
  inspectorOpen: boolean
  /** The editor's left sidebar (workflow switcher and palette). */
  paletteOpen: boolean
  commandOpen: boolean
  confirmDestroy: boolean
  /** The needs_confirmation apply run whose destructive changes the Deploy confirmation dialog shows. */
  confirmDeployRun: string | null
  /** Runs this window started that have not ended yet (see watchRun). */
  watchedRuns: string[]
  /** Docker workspaces: the "Convert to Kubernetes" dialog (toolbar button, command palette). */
  convertKubernetes: boolean
  setInspectorOpen: (open: boolean) => void
  setPaletteOpen: (open: boolean) => void
  setCommandOpen: (open: boolean) => void
  setConfirmDestroy: (open: boolean) => void
  setConfirmDeployRun: (runId: string | null) => void
  setConvertKubernetes: (open: boolean) => void
  /** Home shown over the open workflow (the rail's Home): the workflow stays open underneath. */
  homeOpen: boolean
  setHomeOpen: (open: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  inspectorOpen: true,
  paletteOpen: true,
  commandOpen: false,
  confirmDestroy: false,
  confirmDeployRun: null,
  watchedRuns: [],
  convertKubernetes: false,
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setConfirmDestroy: (confirmDestroy) => set({ confirmDestroy }),
  setConfirmDeployRun: (confirmDeployRun) => set({ confirmDeployRun }),
  setConvertKubernetes: (convertKubernetes) => set({ convertKubernetes }),
  homeOpen: false,
  setHomeOpen: (homeOpen) => set({ homeOpen }),
}))
