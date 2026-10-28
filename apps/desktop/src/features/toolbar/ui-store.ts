import { create } from "zustand"

/** Shell-only UI state that is not part of the workflow contract. */
interface UiState {
  inspectorOpen: boolean
  commandOpen: boolean
  confirmDestroy: boolean
  /** The needs_confirmation apply run whose destructive changes the Deploy confirmation dialog shows. */
  confirmDeployRun: string | null
  /** The apply run this window started last: if it ends needs_confirmation, the dialog opens on its own. */
  watchedApply: string | null
  /** Docker workspaces: the "Convert to Kubernetes" dialog (toolbar button, command palette). */
  convertKubernetes: boolean
  setInspectorOpen: (open: boolean) => void
  setCommandOpen: (open: boolean) => void
  setConfirmDestroy: (open: boolean) => void
  setConfirmDeployRun: (runId: string | null) => void
  setWatchedApply: (runId: string | null) => void
  setConvertKubernetes: (open: boolean) => void
  /** Home shown over the open workflow (the rail's Home): the workflow stays open underneath. */
  homeOpen: boolean
  setHomeOpen: (open: boolean) => void
}

export const useUiStore = create<UiState>((set) => ({
  inspectorOpen: true,
  commandOpen: false,
  confirmDestroy: false,
  confirmDeployRun: null,
  watchedApply: null,
  convertKubernetes: false,
  setInspectorOpen: (inspectorOpen) => set({ inspectorOpen }),
  setCommandOpen: (commandOpen) => set({ commandOpen }),
  setConfirmDestroy: (confirmDestroy) => set({ confirmDestroy }),
  setConfirmDeployRun: (confirmDeployRun) => set({ confirmDeployRun }),
  setWatchedApply: (watchedApply) => set({ watchedApply }),
  setConvertKubernetes: (convertKubernetes) => set({ convertKubernetes }),
  homeOpen: false,
  setHomeOpen: (homeOpen) => set({ homeOpen }),
}))
