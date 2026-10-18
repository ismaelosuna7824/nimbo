import { create } from "zustand"
import { useQuery, type QueryClient } from "@tanstack/react-query"
import { api, subscribeEvents } from "@/lib/api/client"
import type { ConnectionCheck, LoadSample, LoadTestRun } from "@/lib/api/types"
import type { LoadDraft } from "../panels/loadtest/draft"

const CHECKS_KEY = "nimbo.connectionChecks"

export interface StoredCheck extends ConnectionCheck {
  at: string
}

function loadChecks(): Record<string, StoredCheck> {
  try {
    return JSON.parse(localStorage.getItem(CHECKS_KEY) ?? "{}") as Record<string, StoredCheck>
  } catch {
    return {}
  }
}

interface ConnectionsState {
  /** The full-page Connections view replaces home / the editor while open. */
  viewOpen: boolean
  /** Selected connection id, "new" while creating one, null for none. */
  selectedId: string | null
  /** Last "Test connection" result per connection (kept on this machine). */
  checks: Record<string, StoredCheck>
  /** Load-test runs owned by each connection, newest first. */
  runs: Record<string, LoadTestRun[]>
  /** Live samples per run id of connection-owned runs (from SSE). */
  samples: Record<string, LoadSample[]>
  /** Load test form per connection, so switching connections keeps it. */
  drafts: Record<string, LoadDraft>

  openView: (id?: string | "new") => void
  closeView: () => void
  select: (id: string | "new" | null) => void
  recordCheck: (id: string, check: ConnectionCheck) => void
  forget: (id: string) => void
  loadRuns: (id: string) => Promise<void>
  upsertRun: (run: LoadTestRun) => void
  setDraft: (id: string, draft: LoadDraft) => void
}

export const useConnectionsStore = create<ConnectionsState>((set, get) => ({
  viewOpen: false,
  selectedId: null,
  checks: loadChecks(),
  runs: {},
  samples: {},
  drafts: {},

  openView: (id) => set({ viewOpen: true, selectedId: id ?? get().selectedId }),
  closeView: () => set({ viewOpen: false }),
  select: (selectedId) => set({ selectedId }),

  recordCheck(id, check) {
    const checks = { ...get().checks, [id]: { ...check, at: new Date().toISOString() } }
    localStorage.setItem(CHECKS_KEY, JSON.stringify(checks))
    set({ checks })
  },

  forget(id) {
    const { [id]: _check, ...checks } = get().checks
    const { [id]: _runs, ...runs } = get().runs
    const { [id]: _draft, ...drafts } = get().drafts
    localStorage.setItem(CHECKS_KEY, JSON.stringify(checks))
    set({ checks, runs, drafts, selectedId: get().selectedId === id ? null : get().selectedId })
  },

  async loadRuns(id) {
    const list = await api.connectionLoadTests(id)
    // Keep statuses that arrived over SSE while the request was in flight.
    const live = get().runs[id] ?? []
    const merged = list.map((r) => live.find((l) => l.id === r.id && l.status !== "running" && r.status === "running") ?? r)
    for (const l of live) if (!merged.some((r) => r.id === l.id)) merged.push(l)
    set({ runs: { ...get().runs, [id]: merged.sort((a, b) => b.startedAt.localeCompare(a.startedAt)) } })
  },

  upsertRun(run) {
    const id = run.connectionId
    if (!id) return
    const others = (get().runs[id] ?? []).filter((r) => r.id !== run.id)
    set({ runs: { ...get().runs, [id]: [run, ...others].sort((a, b) => b.startedAt.localeCompare(a.startedAt)) } })
  },

  setDraft: (id, draft) => set({ drafts: { ...get().drafts, [id]: draft } }),
}))

/** The saved connections (secrets masked), shared by home, the Connections view and load-test pickers. */
export function useConnections() {
  return useQuery({ queryKey: ["connections"], queryFn: api.listConnections, retry: false })
}

/**
 * Subscribes to every server event (no workflow filter) and routes connection-owned
 * load-test events (published without a workflowId) into the store; keeps the
 * connections list fresh. Returns an unsubscribe function.
 */
export function subscribeConnectionEvents(queryClient: QueryClient): () => void {
  return subscribeEvents(undefined, (e) => {
    const s = useConnectionsStore.getState()
    switch (e.type) {
      case "connections.updated":
        void queryClient.invalidateQueries({ queryKey: ["connections"] })
        break
      case "loadtest.sample":
        if (e.workflowId) break
        useConnectionsStore.setState({
          samples: { ...s.samples, [e.data.runId]: [...(s.samples[e.data.runId] ?? []), e.data.sample] },
        })
        break
      case "loadtest.status":
        if (e.data.connectionId) s.upsertRun(e.data)
        break
    }
  })
}
