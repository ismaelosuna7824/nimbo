import { create } from "zustand"
import { api, subscribeEvents } from "@/lib/api/client"
import { queryClient } from "@/lib/query-client"
import type {
  DeployedState,
  LoadSample,
  LoadTestRun,
  LogLine,
  Run,
  SandboxStatus,
  ServerEvent,
  Workflow,
  WorkflowSummary,
} from "@/lib/api/types"

export type BottomTab = "terraform" | "variables" | "deploy" | "endpoints" | "loadtest" | "problems"
export type AppDialog = "import" | "export" | "new-workflow" | null
export type UserMode = "guided" | "pro"

const SAVE_DEBOUNCE_MS = 400

/** Tells the daemon which workflow is open so MCP agents can act on "current". Best effort: editing never depends on it. */
function reportSession(openWorkflowId: string | null) {
  api.setSession(openWorkflowId).catch(() => undefined)
}

interface WorkflowStoreState {
  workflow: Workflow | null
  /** True while local edits are waiting to be persisted. */
  dirty: boolean
  saving: boolean
  saveError: string | null

  selectedNodeId: string | null
  bottomTab: BottomTab
  bottomOpen: boolean
  dialog: AppDialog
  mode: UserMode

  sandbox: SandboxStatus | null
  runs: Run[]
  /** Live log lines per run id (from SSE); complete logs come from api.run(). */
  runLogs: Record<string, LogLine[]>
  deployed: DeployedState | null
  loadTests: LoadTestRun[]
  /** Live samples per load-test run id (from SSE). */
  loadSamples: Record<string, LoadSample[]>

  open: (id: string) => Promise<void>
  close: () => void
  /** Replace the workflow with a server response (no save scheduled). */
  setWorkflow: (wf: Workflow) => void
  /** Apply a local edit and schedule a debounced PUT. */
  update: (mutate: (draft: Workflow) => Workflow) => void
  flush: () => Promise<void>

  select: (nodeId: string | null) => void
  showBottom: (tab: BottomTab) => void
  setBottomOpen: (open: boolean) => void
  openDialog: (d: AppDialog) => void
  setMode: (m: UserMode) => void
  /** Uses the app-wide default (Settings) until the user picks a mode on this machine. */
  applyDefaultMode: (m: UserMode) => void

  refreshRuntime: () => Promise<void>
}

let saveTimer: number | undefined
let unsubscribe: (() => void) | undefined

export const useWorkflowStore = create<WorkflowStoreState>((set, get) => ({
  workflow: null,
  dirty: false,
  saving: false,
  saveError: null,
  selectedNodeId: null,
  bottomTab: "terraform",
  bottomOpen: true,
  dialog: null,
  mode: (localStorage.getItem("nimbo.mode") as UserMode | null) ?? "guided",
  sandbox: null,
  runs: [],
  runLogs: {},
  deployed: null,
  loadTests: [],
  loadSamples: {},

  async open(id) {
    await get().flush()
    unsubscribe?.()
    const wf = await api.getWorkflow(id)
    set({
      workflow: wf,
      dirty: false,
      saveError: null,
      selectedNodeId: null,
      sandbox: null,
      runs: [],
      runLogs: {},
      deployed: null,
      loadTests: [],
      loadSamples: {},
    })
    localStorage.setItem("nimbo.lastWorkflow", id)
    reportSession(id)
    unsubscribe = subscribeEvents(id, (e) => applyEvent(e, set, get))
    await get().refreshRuntime()
  },

  close() {
    unsubscribe?.()
    unsubscribe = undefined
    set({ workflow: null, selectedNodeId: null, sandbox: null, runs: [], deployed: null, loadTests: [] })
    reportSession(null)
  },

  setWorkflow(wf) {
    set({ workflow: wf, dirty: false })
  },

  update(mutate) {
    const wf = get().workflow
    if (!wf) return
    set({ workflow: mutate(structuredClone(wf)), dirty: true })
    clearTimeout(saveTimer)
    saveTimer = window.setTimeout(() => void get().flush(), SAVE_DEBOUNCE_MS)
  },

  async flush() {
    clearTimeout(saveTimer)
    const { workflow, dirty } = get()
    if (!workflow || !dirty) return
    set({ saving: true, dirty: false })
    try {
      const saved = await api.saveWorkflow(workflow)
      // Keep edits made while the request was in flight.
      if (!get().dirty) set({ workflow: saved })
      else set({ workflow: { ...get().workflow!, edges: saved.edges, diagnostics: saved.diagnostics, clouds: saved.clouds } })
      set({ saveError: null })
    } catch (err) {
      set({ saveError: err instanceof Error ? err.message : String(err), dirty: true })
    } finally {
      set({ saving: false })
    }
  },

  select: (nodeId) => set({ selectedNodeId: nodeId }),
  showBottom: (tab) => set({ bottomTab: tab, bottomOpen: true }),
  setBottomOpen: (open) => set({ bottomOpen: open }),
  openDialog: (d) => set({ dialog: d }),
  setMode(m) {
    localStorage.setItem("nimbo.mode", m)
    set({ mode: m })
  },
  applyDefaultMode(m) {
    if (!localStorage.getItem("nimbo.mode")) set({ mode: m })
  },

  async refreshRuntime() {
    const wf = get().workflow
    if (!wf) return
    const [sandbox, runs, deployed, loadTests] = await Promise.allSettled([
      api.sandbox(wf.id),
      api.runs(wf.id),
      api.state(wf.id),
      api.loadTests(wf.id),
    ])
    if (get().workflow?.id !== wf.id) return
    set({
      sandbox: sandbox.status === "fulfilled" ? sandbox.value : null,
      runs: runs.status === "fulfilled" ? runs.value : [],
      deployed: deployed.status === "fulfilled" ? deployed.value : null,
      loadTests: loadTests.status === "fulfilled" ? loadTests.value : [],
    })
  },
}))

function applyEvent(
  e: ServerEvent,
  set: (partial: Partial<WorkflowStoreState>) => void,
  get: () => WorkflowStoreState,
) {
  const s = get()
  if (!s.workflow || e.workflowId !== s.workflow.id) return
  switch (e.type) {
    case "workflow.updated":
      // Keep list views (switcher recents, palette) in step with name/icon edits.
      queryClient.setQueryData<WorkflowSummary[]>(["workflows"], (list) =>
        list?.map((w) =>
          w.id === e.data.id
            ? {
                ...w,
                name: e.data.name,
                description: e.data.description,
                icon: e.data.icon,
                archived: e.data.archived,
                updatedAt: e.data.updatedAt,
                nodeCount: e.data.nodes.length,
                clouds: e.data.clouds,
              }
            : w,
        ),
      )
      // Archived elsewhere (another window, an agent): keep pending edits, then go home.
      if (e.data.archived) {
        void s.flush().finally(() => {
          if (get().workflow?.id === e.data.id) get().close()
        })
        break
      }
      // Ignore echoes of our own saves while local edits are pending.
      if (!s.dirty && !s.saving) set({ workflow: e.data })
      break
    case "workflow.deleted":
      s.close()
      break
    case "sandbox.status":
      set({ sandbox: e.data })
      break
    case "run.status": {
      const others = s.runs.filter((r) => r.id !== e.data.id)
      set({ runs: [e.data, ...others].sort((a, b) => b.createdAt.localeCompare(a.createdAt)) })
      break
    }
    case "run.log": {
      const prev = s.runLogs[e.data.runId] ?? []
      set({ runLogs: { ...s.runLogs, [e.data.runId]: [...prev, e.data.line] } })
      break
    }
    case "state.updated":
      set({ deployed: e.data })
      break
    case "loadtest.sample": {
      const prev = s.loadSamples[e.data.runId] ?? []
      set({ loadSamples: { ...s.loadSamples, [e.data.runId]: [...prev, e.data.sample] } })
      break
    }
    case "loadtest.status": {
      const others = s.loadTests.filter((r) => r.id !== e.data.id)
      set({ loadTests: [e.data, ...others].sort((a, b) => b.startedAt.localeCompare(a.startedAt)) })
      break
    }
  }
}
