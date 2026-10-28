import { create } from "zustand"
import type { HopVerdict, NetEndpoint, NetProtocol } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** The reachability dialog's question and the canvas highlight of its last answer. */
interface ReachabilityState {
  open: boolean
  source: NetEndpoint
  /** null until a destination is picked. */
  destination: NetEndpoint | null
  protocol: NetProtocol
  port: number
  /** Verdict of each canvas node on the last checked path; null = no highlight. */
  highlight: Record<string, HopVerdict> | null
  /** Bumped by every openReachability() so the dialog drops the previous answer. */
  seq: number
}

export const INTERNET: NetEndpoint = { kind: "internet" }

export const useReachability = create<ReachabilityState>(() => ({
  open: false,
  source: INTERNET,
  destination: null,
  protocol: "tcp",
  port: 80,
  highlight: null,
  seq: 0,
}))

export type ReachabilityPreset = Partial<Pick<ReachabilityState, "source" | "destination" | "protocol" | "port">>

/** Opens the reachability dialog, optionally preset (pending edits are saved first so the analysis sees them). */
export async function openReachability(preset: ReachabilityPreset = {}) {
  await useWorkflowStore.getState().flush()
  useReachability.setState((s) => ({ ...preset, open: true, highlight: null, seq: s.seq + 1 }))
}

export function closeReachability() {
  useReachability.setState({ open: false, highlight: null })
}

// Never carry the dialog (and its canvas highlight) over to another workflow, or past closing this one. Done here, not
// in a dialog effect: the dialog mounts lazily on its first open, and an effect cleanup would also run on that first
// mount under StrictMode and close it straight away.
useWorkflowStore.subscribe((s, prev) => {
  if (s.workflow?.id !== prev.workflow?.id && useReachability.getState().open) closeReachability()
})
