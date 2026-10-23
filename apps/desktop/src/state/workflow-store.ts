import { create } from "zustand"
import { api, subscribeEvents } from "@/lib/api/client"
import { queryClient } from "@/lib/query-client"
import type {
  ComposeBuild,
  ComposeStats,
  ComposeStatus,
  DeployedState,
  LoadSample,
  LoadTestRun,
  LogLine,
  Run,
  SandboxStatus,
  ServiceUsage,
  ServerEvent,
  KubeStatus,
  Workflow,
  WorkflowSummary,
} from "@/lib/api/types"

export type BottomTab = "terraform" | "variables" | "environments" | "deploy" | "endpoints" | "loadtest" | "problems" | "readiness"
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
  /** Kubernetes: environment the Readiness tab shows first when it opens (set by "Readiness" links, then cleared). */
  readinessEnvironment: string | null
  dialog: AppDialog
  mode: UserMode

  sandbox: SandboxStatus | null
  /** Docker workspaces: containers of the compose project (null while unknown). */
  compose: ComposeStatus | null
  /** Docker and Kubernetes workspaces: the usage stream (host size, interval) without the samples. */
  composeStats: ComposeStatsInfo | null
  /**
   * Docker and Kubernetes workspaces: last USAGE_SAMPLES usage samples, oldest first, per compose service name
   * (docker) or workload node id (kubernetes).
   */
  composeUsage: Record<string, ServiceUsage[]>
  /** Docker workspaces: build metrics of recent build/up runs, newest first. */
  composeBuilds: ComposeBuild[]
  /** Kubernetes workspaces: the local cluster and its workloads, pods, port-forwards, events (null while unknown). */
  kube: KubeStatus | null
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
  /**
   * Apply a local edit and schedule a debounced PUT. `mutate` must not modify
   * its argument: return a new workflow that shares every unchanged part.
   */
  update: (mutate: (current: Workflow) => Workflow) => void
  flush: () => Promise<void>

  select: (nodeId: string | null) => void
  showBottom: (tab: BottomTab) => void
  setBottomOpen: (open: boolean) => void
  /** Opens the Readiness tab on one environment ("" = base). */
  showReadiness: (environment: string) => void
  openDialog: (d: AppDialog) => void
  setMode: (m: UserMode) => void
  /** Uses the app-wide default (Settings) until the user picks a mode on this machine. */
  applyDefaultMode: (m: UserMode) => void

  refreshRuntime: () => Promise<void>
}

let saveTimer: number | undefined
let unsubscribe: (() => void) | undefined
/** Bumped by every open()/close(); an open() that is no longer the latest drops its result. */
let openSeq = 0

/** Streamed log lines waiting to be appended in one store update (bursts of output arrive line by line). */
let pendingLogs: Record<string, LogLine[]> = {}
let logTimer: number | undefined
const LOG_FLUSH_MS = 50

export type ComposeStatsInfo = Omit<ComposeStats, "services">

/** Samples kept per service (about 2 minutes at the daemon's 1 s interval). */
const USAGE_SAMPLES = 120
/** Builds kept (the daemon keeps 20 per workflow). */
const MAX_BUILDS = 20
/** Workflow whose GET compose/stats is in flight (it starts the stream, one call is enough). */
let statsLoading: string | null = null

/** Asked before the user switches away from or closes the open workflow; resolves false to stay. */
let leaveGuard: (() => Promise<boolean>) | null = null

/** Registers the guard for unsaved work outside the workflow itself (e.g. file edits); returns the unregister function. */
export function setLeaveGuard(fn: () => Promise<boolean>): () => void {
  leaveGuard = fn
  return () => {
    if (leaveGuard === fn) leaveGuard = null
  }
}

/** Runs the leave guard; true when there is nothing to lose or the user agreed to discard it. */
export function confirmLeave(): Promise<boolean> {
  return leaveGuard ? leaveGuard() : Promise.resolve(true)
}

function resetPendingLogs() {
  clearTimeout(logTimer)
  logTimer = undefined
  pendingLogs = {}
}

export const useWorkflowStore = create<WorkflowStoreState>((set, get) => ({
  workflow: null,
  dirty: false,
  saving: false,
  saveError: null,
  selectedNodeId: null,
  bottomTab: "terraform",
  bottomOpen: true,
  readinessEnvironment: null,
  dialog: null,
  mode: (localStorage.getItem("nimbo.mode") as UserMode | null) ?? "guided",
  sandbox: null,
  compose: null,
  composeStats: null,
  composeUsage: {},
  composeBuilds: [],
  kube: null,
  runs: [],
  runLogs: {},
  deployed: null,
  loadTests: [],
  loadSamples: {},

  async open(id) {
    const openId = get().workflow?.id
    if (openId && openId !== id && !(await confirmLeave())) return
    const seq = ++openSeq
    await get().flush()
    const wf = await api.getWorkflow(id)
    if (seq !== openSeq) return
    unsubscribe?.()
    resetPendingLogs()
    set({
      workflow: wf,
      dirty: false,
      saveError: null,
      selectedNodeId: null,
      sandbox: null,
      compose: null,
      composeStats: null,
      composeUsage: {},
      composeBuilds: [],
      kube: null,
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
    openSeq++
    unsubscribe?.()
    unsubscribe = undefined
    resetPendingLogs()
    set({
      workflow: null,
      selectedNodeId: null,
      sandbox: null,
      compose: null,
      composeStats: null,
      composeUsage: {},
      composeBuilds: [],
      kube: null,
      runs: [],
      runLogs: {},
      deployed: null,
      loadTests: [],
      loadSamples: {},
    })
    reportSession(null)
  },

  setWorkflow(wf) {
    set({ workflow: shareWorkflow(get().workflow, wf), dirty: false })
  },

  update(mutate) {
    const wf = get().workflow
    if (!wf) return
    set({ workflow: mutate(wf), dirty: true })
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
      const current = get().workflow!
      const shared = shareWorkflow(current, saved)
      if (!get().dirty) set({ workflow: shared })
      else
        set({
          workflow: {
            ...current,
            edges: shared.edges,
            diagnostics: shared.diagnostics,
            clouds: shared.clouds,
            compose: shared.compose,
            kubernetes: shared.kubernetes,
            environments: current.kind === "kubernetes" ? shared.environments : current.environments,
          },
        })
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
  showReadiness: (environment) => set({ readinessEnvironment: environment, bottomTab: "readiness", bottomOpen: true }),
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
    const docker = wf.kind === "docker"
    const kubernetes = wf.kind === "kubernetes"
    const terraform = !docker && !kubernetes
    const none = Promise.resolve(null)
    if (docker || kubernetes) void loadComposeStats(wf.id, set, get, kubernetes)
    const [sandbox, compose, runs, deployed, loadTests, builds, kube] = await Promise.allSettled([
      terraform ? api.sandbox(wf.id) : none,
      docker ? api.composeStatus(wf.id) : none,
      api.runs(wf.id),
      terraform ? api.state(wf.id) : none,
      api.loadTests(wf.id),
      docker ? api.composeBuilds(wf.id, MAX_BUILDS) : none,
      kubernetes ? api.kubeStatus(wf.id) : none,
    ])
    if (get().workflow?.id !== wf.id) return
    set({
      sandbox: sandbox.status === "fulfilled" ? sandbox.value : null,
      compose: compose.status === "fulfilled" ? compose.value : null,
      runs: runs.status === "fulfilled" ? runs.value : [],
      deployed: deployed.status === "fulfilled" ? deployed.value : null,
      loadTests: loadTests.status === "fulfilled" ? loadTests.value : [],
      composeBuilds: builds.status === "fulfilled" ? mergeBuilds(get().composeBuilds, builds.value?.builds ?? []) : [],
      kube: kube.status === "fulfilled" ? kube.value : null,
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
                nodeCount:
                  e.data.kind === "docker"
                    ? (e.data.compose?.graph.nodes.filter((n) => n.kind === "service").length ?? 0)
                    : e.data.kind === "kubernetes"
                      ? (e.data.kubernetes?.graph.nodes.length ?? 0)
                      : e.data.nodes.length,
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
      if (!s.dirty && !s.saving) set({ workflow: shareWorkflow(s.workflow, e.data) })
      break
    case "workflow.deleted":
      s.close()
      break
    case "sandbox.status":
      set({ sandbox: e.data })
      break
    case "compose.status": {
      set({ compose: e.data })
      // GET compose/stats starts the `docker stats` stream: ask again once containers run.
      const running = e.data.services.some((svc) => svc.containers.some((c) => c.state === "running"))
      if (running && !s.composeStats?.streaming) void loadComposeStats(e.workflowId, set, get)
      else if (!running && s.composeStats?.streaming) set({ composeStats: { ...s.composeStats, streaming: false } })
      break
    }
    case "kube.status": {
      set({ kube: e.data })
      // GET kubernetes/stats starts the usage stream: ask again once pods run.
      const running = e.data.workloads.some((w) => w.pods.some((p) => p.phase === "Running"))
      if (running && !s.composeStats?.streaming) void loadComposeStats(e.workflowId, set, get, true)
      else if (!running && s.composeStats?.streaming) set({ composeStats: { ...s.composeStats, streaming: false } })
      break
    }
    case "compose.stats":
    case "kube.stats": {
      const usage = { ...s.composeUsage }
      for (const u of e.data.services) {
        const prev = usage[u.service] ?? []
        usage[u.service] = prev.length >= USAGE_SAMPLES ? [...prev.slice(prev.length - USAGE_SAMPLES + 1), u] : [...prev, u]
      }
      set({
        composeUsage: usage,
        ...(s.composeStats && !s.composeStats.streaming && { composeStats: { ...s.composeStats, streaming: true } }),
      })
      break
    }
    case "compose.build":
      set({ composeBuilds: mergeBuilds(s.composeBuilds, [e.data]) })
      break
    case "run.status": {
      const others = s.runs.filter((r) => r.id !== e.data.id)
      set({ runs: [e.data, ...others].sort((a, b) => b.createdAt.localeCompare(a.createdAt)) })
      break
    }
    case "run.log": {
      // Coalesce bursts of output into one update per LOG_FLUSH_MS.
      ;(pendingLogs[e.data.runId] ??= []).push(e.data.line)
      logTimer ??= window.setTimeout(() => {
        const batch = pendingLogs
        resetPendingLogs()
        const runLogs = { ...get().runLogs }
        for (const [runId, lines] of Object.entries(batch)) runLogs[runId] = [...(runLogs[runId] ?? []), ...lines]
        set({ runLogs })
      }, LOG_FLUSH_MS)
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

/**
 * Fetches the live usage of a Docker workspace (`docker stats`) or a Kubernetes one (pod cgroups, keyed by workload
 * node id), starting the daemon's stream, and keeps samples streamed meanwhile.
 */
async function loadComposeStats(
  workflowId: string,
  set: (partial: Partial<WorkflowStoreState>) => void,
  get: () => WorkflowStoreState,
  kubernetes = false,
) {
  if (statsLoading === workflowId) return
  statsLoading = workflowId
  try {
    const { services, ...info } = await (kubernetes ? api.kubeStats(workflowId) : api.composeStats(workflowId))
    if (get().workflow?.id !== workflowId) return
    const prev = get().composeUsage
    const usage: Record<string, ServiceUsage[]> = {}
    for (const svc of services) {
      const history = svc.history.length > 0 ? svc.history : svc.current ? [svc.current] : []
      const last = history.length > 0 ? Date.parse(history[history.length - 1].t) : -Infinity
      const live = (prev[svc.service] ?? []).filter((u) => Date.parse(u.t) > last)
      usage[svc.service] = [...history, ...live].slice(-USAGE_SAMPLES)
    }
    set({ composeStats: info, composeUsage: usage })
  } catch {
    // Docker unavailable: views show no live usage, like an unknown compose status.
  } finally {
    if (statsLoading === workflowId) statsLoading = null
  }
}

/** Builds by run id (`next` wins), newest first, at most MAX_BUILDS. */
function mergeBuilds(prev: ComposeBuild[], next: ComposeBuild[]): ComposeBuild[] {
  const byRun = new Map(prev.map((b) => [b.runId, b]))
  for (const b of next) byRun.set(b.runId, b)
  return [...byRun.values()].sort((a, b) => Date.parse(b.at) - Date.parse(a.at)).slice(0, MAX_BUILDS)
}

/** Structural equality of JSON values (what the API sends). */
function jsonEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true
  if (typeof a !== "object" || typeof b !== "object" || a === null || b === null) return false
  if (Array.isArray(a)) {
    if (!Array.isArray(b) || a.length !== b.length) return false
    for (let i = 0; i < a.length; i++) if (!jsonEqual(a[i], b[i])) return false
    return true
  }
  if (Array.isArray(b)) return false
  const ak = Object.keys(a)
  if (ak.length !== Object.keys(b).length) return false
  for (const k of ak) {
    if (!Object.hasOwn(b, k) || !jsonEqual((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k])) return false
  }
  return true
}

/** `next` with every item equal to the one with the same id in `prev` replaced by the `prev` object. */
function shareById<T extends { id: string }>(prev: T[], next: T[]): T[] {
  const byId = new Map(prev.map((x) => [x.id, x]))
  let reusedAll = prev.length === next.length
  const out = next.map((x, i) => {
    const old = byId.get(x.id)
    if (old && jsonEqual(old, x)) {
      if (prev[i] !== old) reusedAll = false
      return old
    }
    reusedAll = false
    return x
  })
  return reusedAll ? prev : out
}

/**
 * A server copy of the open workflow that keeps the identity of every part
 * that did not change, so views keyed on nodes, edges or diagnostics (canvas
 * cards, inspector) skip re-rendering after saves and their SSE echoes.
 */
function shareWorkflow(prev: Workflow | null, next: Workflow): Workflow {
  if (!prev || prev.id !== next.id) return next
  const out = { ...next, nodes: shareById(prev.nodes, next.nodes), edges: prev.edges && next.edges ? shareById(prev.edges, next.edges) : next.edges }
  for (const key of Object.keys(next) as (keyof Workflow)[]) {
    if (key === "nodes" || key === "edges") continue
    if (jsonEqual(prev[key], next[key])) (out as Record<string, unknown>)[key] = prev[key]
  }
  // Docker and Kubernetes workspaces: unchanged graph nodes and edges keep their identity too (canvas cards skip re-rendering).
  if (out.compose && prev.compose && out.compose !== prev.compose) {
    const graph = {
      nodes: shareById(prev.compose.graph.nodes, out.compose.graph.nodes),
      edges: shareById(prev.compose.graph.edges, out.compose.graph.edges),
    }
    const same = graph.nodes === prev.compose.graph.nodes && graph.edges === prev.compose.graph.edges
    out.compose = { ...out.compose, graph: same ? prev.compose.graph : graph }
  }
  if (out.kubernetes && prev.kubernetes && out.kubernetes !== prev.kubernetes) {
    const next = out.kubernetes.graph
    const graph = {
      ...next,
      nodes: shareById(prev.kubernetes.graph.nodes, next.nodes),
      edges: shareById(prev.kubernetes.graph.edges, next.edges),
      charts: next.charts && shareById(prev.kubernetes.graph.charts ?? [], next.charts),
    }
    const same = graph.nodes === prev.kubernetes.graph.nodes && graph.edges === prev.kubernetes.graph.edges && graph.charts === prev.kubernetes.graph.charts
    out.kubernetes = { ...out.kubernetes, graph: same ? prev.kubernetes.graph : graph }
  }
  return out
}
