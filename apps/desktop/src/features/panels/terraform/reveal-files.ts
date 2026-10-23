import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

/** A request to show part of the Code tab: filter the tree, optionally open a file. */
export interface FilesRequest {
  workflowId: string
  /** Text put in the tree filter, e.g. "modules/network/". */
  filter: string
  /** File to open; the first file under the filter when it is not in the listing. */
  open?: string
  /** 1-based line to reveal in `open`. */
  line?: number
  /** Distinguishes repeated requests for the same folder. */
  nonce: number
}

export const useFilesRequest = create<{ request: FilesRequest | null }>(() => ({ request: null }))

/**
 * Opens the Code tab of the open workflow filtered to `filter` (a folder such as "modules/network/") and opens `open`,
 * at `line` when given.
 */
export function revealFiles(filter: string, open?: string, line?: number) {
  const store = useWorkflowStore.getState()
  if (!store.workflow) return
  const workflowId = store.workflow.id
  useFilesRequest.setState((s) => ({ request: { workflowId, filter, open, line, nonce: (s.request?.nonce ?? 0) + 1 } }))
  store.showBottom("terraform")
}

/** The pending request for workflowId, if any. */
export function useFilesRequestFor(workflowId: string) {
  return useFilesRequest((s) => (s.request?.workflowId === workflowId ? s.request : null))
}
