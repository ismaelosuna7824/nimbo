import type {
  KubeClusterState,
  KubeEvent,
  KubeGraph,
  KubeLogLine,
  KubeNode,
  KubeProblem,
  KubeState,
  KubeStatus,
  KubeWorkloadStatus,
  LoadResources,
  LogLine,
} from "@/lib/api/types"
import { COMPOSE_STATE, type ChipSummary } from "@/features/docker/deploy/compose-model"

/** Label and status dot of a workload state (same values and colours as compose). */
export const KUBE_STATE: Record<KubeState, { label: string; dot: string }> = COMPOSE_STATE

/** Label and status dot of the workflow's local cluster. */
export const CLUSTER_STATE: Record<KubeClusterState, { label: string; dot: string }> = {
  absent: { label: "No cluster", dot: "bg-muted-foreground/50" },
  starting: { label: "Starting", dot: "bg-amber-500" },
  running: { label: "Running", dot: "bg-emerald-500" },
  stopped: { label: "Stopped", dot: "bg-muted-foreground/50" },
  error: { label: "Error", dot: "bg-destructive" },
}

export const KUBE_PROBLEM_LABEL: Record<string, string> = {
  oom: "Out of memory",
  crashloop: "Crash loop",
  restarts: "Restarting",
  "image-pull": "Image pull failed",
  unschedulable: "Unschedulable",
  probe: "Probe failing",
  exit: "Exited",
  "job-failed": "Job failed",
  config: "Configuration",
}

export function problemLabel(kind: string): string {
  return KUBE_PROBLEM_LABEL[kind] ?? kind
}

/** Workloads with at least one running pod: what plays the role of "deployed" in a Kubernetes workspace. */
export function runningWorkloads(status: KubeStatus | null | undefined): number {
  return status?.workloads.filter((w) => w.pods.some((p) => p.phase === "Running")).length ?? 0
}

export interface KubeChipSummary extends ChipSummary {
  cluster: KubeClusterState
}

/** What the toolbar chip of a Kubernetes workspace says: "Cluster off", "Starting…", "Running 2/3"… */
export function kubeChipSummary(status: KubeStatus | null | undefined): KubeChipSummary {
  const cluster = status?.cluster.state ?? "absent"
  const total = status?.workloads.length ?? 0
  const running = runningWorkloads(status)
  const workloads = status?.state ?? "absent"
  if (cluster !== "running") {
    const label =
      cluster === "absent"
        ? "Cluster off"
        : cluster === "starting"
          ? "Starting…"
          : cluster === "stopped"
            ? "Cluster stopped"
            : "Cluster error"
    return { cluster, state: cluster, label, running, total }
  }
  // The cluster runs: the chip follows its workloads.
  const label =
    total === 0 || workloads === "absent"
      ? "Cluster running"
      : workloads === "starting"
        ? `Rolling out ${running}/${total}`
        : workloads === "running"
          ? `Running ${running}/${total}`
          : workloads === "stopped"
            ? "Workloads stopped"
            : "Workload error"
  const state: KubeState = total === 0 || workloads === "absent" ? "running" : workloads
  return { cluster, state, label, running, total }
}

/**
 * Changes whenever what is reachable changes (cluster, workload states, port-forwards, ingress URLs), not on every
 * status event: endpoints and load targets are refetched on it.
 */
export function kubeKey(status: KubeStatus | null | undefined): string {
  if (!status) return ""
  const workloads = status.workloads.map((w) => `${w.nodeId}:${w.state}`).join(",")
  const forwards = status.forwards.map((f) => `${f.nodeId}:${f.port}>${f.hostPort}:${f.ready ? 1 : 0}`).join(",")
  const ingresses = status.ingresses.map((i) => `${i.nodeId}:${i.address}`).join(",")
  return `${status.cluster.state}|${status.state}|${workloads}|${forwards}|${ingresses}`
}

/** First address reachable from this machine (placeholder of custom HTTP targets). */
export function firstKubeUrl(status: KubeStatus | null | undefined): string | undefined {
  return status?.forwards.find((f) => f.ready && f.url)?.url ?? status?.ingresses[0]?.address
}

/** Ready and total pods of a workload. */
export function workloadPods(w: KubeWorkloadStatus): { ready: number; total: number } {
  return { ready: w.pods.filter((p) => p.ready).length, total: w.pods.length }
}

export function workloadRestarts(w: KubeWorkloadStatus): number {
  return w.pods.reduce((n, p) => n + p.restarts, 0)
}

/** CPU (cores) and memory (bytes) limits of a workload: the sum of its pods' limits; 0 when a pod has none. */
export function workloadLimits(w: KubeWorkloadStatus): { cpu: number; mem: number } {
  if (w.pods.length === 0) return { cpu: 0, mem: 0 }
  const cpu = w.pods.every((p) => (p.cpuLimit ?? 0) > 0) ? w.pods.reduce((n, p) => n + (p.cpuLimit ?? 0), 0) : 0
  const mem = w.pods.every((p) => (p.memLimit ?? 0) > 0) ? w.pods.reduce((n, p) => n + (p.memLimit ?? 0), 0) : 0
  return { cpu, mem }
}

/** "Deployment/default/web" → "web"; cluster-scoped "ClusterRole/x" → "x". */
export function nodeName(nodeId: string): string {
  return nodeId.slice(nodeId.lastIndexOf("/") + 1)
}

/** "Deployment/default/web" → "Deployment"; "Certificate.cert-manager.io/default/x" → "Certificate". */
export function nodeKind(nodeId: string): string {
  const kind = nodeId.slice(0, nodeId.indexOf("/"))
  const dot = kind.indexOf(".")
  return dot >= 0 ? kind.slice(0, dot) : kind
}

/** Display name per workload node id: its name, or "namespace/name" when two namespaces share a name. */
export function workloadLabels(workloads: readonly Pick<KubeWorkloadStatus, "nodeId" | "name" | "namespace">[]): Record<string, string> {
  const count = new Map<string, number>()
  for (const w of workloads) count.set(w.name, (count.get(w.name) ?? 0) + 1)
  return Object.fromEntries(
    workloads.map((w) => [w.nodeId, (count.get(w.name) ?? 0) > 1 ? `${w.namespace}/${w.name}` : w.name]),
  )
}

/**
 * The workload behind a Service (the one it selects) or an Ingress/HTTPRoute (through the Service it routes to): its
 * image names the technology an endpoint card shows.
 */
export function backingWorkload(nodeId: string, graph: KubeGraph | undefined): KubeNode | undefined {
  if (!graph) return undefined
  const routed = graph.edges.filter((e) => e.source === nodeId && e.kind === "routes").map((e) => e.target)
  const services = routed.length > 0 ? routed : [nodeId]
  const workload = graph.edges.find((e) => services.includes(e.source) && e.kind === "selects")?.target
  return workload ? graph.nodes.find((n) => n.id === workload) : undefined
}

/** Load-test resources with workload node ids replaced by display names (verdicts, peaks and charts read them). */
export function relabelResources(r: LoadResources, labels: Record<string, string>): LoadResources {
  const name = (id: string) => labels[id] ?? (id.includes("/") ? nodeName(id) : id)
  return {
    ...r,
    service: r.service !== undefined ? name(r.service) : r.service,
    services: r.services.map((s) => ({ ...s, service: name(s.service) })),
    verdicts: r.verdicts.map((v) => ({ ...v, service: name(v.service) })),
  }
}

export interface ProblemGroup {
  nodeId: string
  name: string
  severity: "error" | "warning"
  problems: KubeProblem[]
}

/** Problems grouped per workload, groups with errors first, then by name; errors first inside a group. */
export function groupProblems(problems: readonly KubeProblem[]): ProblemGroup[] {
  const groups = new Map<string, ProblemGroup>()
  for (const p of problems) {
    let g = groups.get(p.nodeId)
    if (!g) groups.set(p.nodeId, (g = { nodeId: p.nodeId, name: nodeName(p.nodeId), severity: "warning", problems: [] }))
    g.problems.push(p)
    if (p.severity === "error") g.severity = "error"
  }
  const rank = (s: "error" | "warning") => (s === "error" ? 0 : 1)
  return [...groups.values()]
    .map((g) => ({ ...g, problems: [...g.problems].sort((a, b) => rank(a.severity) - rank(b.severity)) }))
    .sort((a, b) => rank(a.severity) - rank(b.severity) || a.name.localeCompare(b.name))
}

/** Worst severity of a list of problems, or undefined without problems. */
export function kubeProblemTone(problems: readonly KubeProblem[]): "error" | "warning" | undefined {
  if (problems.length === 0) return undefined
  return problems.some((p) => p.severity === "error") ? "error" : "warning"
}

/**
 * Events about a workload: the workload itself, its pods, and the ReplicaSets/Jobs it owns (named "<workload>-…").
 * Without a workload every event.
 */
export function workloadEvents(events: readonly KubeEvent[], w: KubeWorkloadStatus | undefined): KubeEvent[] {
  if (!w) return [...events]
  const pods = new Set(w.pods.map((p) => p.name))
  return events.filter(
    (e) =>
      e.nodeId === w.nodeId ||
      ((e.namespace ?? w.namespace) === w.namespace && (pods.has(e.name) || e.name.startsWith(`${w.name}-`))),
  )
}

/** "45s", "12m", "3h", "5d" (kubectl-style age). */
export function formatAge(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—"
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 48) return `${h}h`
  return `${Math.floor(h / 24)}d`
}

/** Age of a pod since it started; "—" when unknown. */
export function podAge(startedAt: string | undefined, now = Date.now()): string {
  if (!startedAt) return "—"
  const t = Date.parse(startedAt)
  return Number.isNaN(t) ? "—" : formatAge(now - t)
}

/** Shell-quoted path for a copyable command. */
function shellQuote(s: string): string {
  return /^[\w@%+=:,./~-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`
}

/** `kubectl --kubeconfig <path> get pods -A`: Nimbo's kubeconfig never touches ~/.kube/config. */
export function kubectlSnippet(kubeconfig: string, args = "get pods -A"): string {
  return `kubectl --kubeconfig ${shellQuote(kubeconfig)} ${args}`
}

// ---------------------------------------------------------------------------
// Pod logs

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;]*[A-Za-z]/g

export interface KubeLogRow {
  /** Monotonic id (React key; rows are only appended or cleared). */
  id: number
  /** "web-7d9c-x2x4z" or "web-7d9c-x2x4z/sidecar" when the pod has more than one container. */
  source: string
  pod: string
  container: string
  t: string
  stream: LogLine["stream"]
  text: string
}

/** Most lines kept in the viewer; older ones are dropped (the full log stays in the cluster). */
export const MAX_KUBE_LOG_ROWS = 5000

/**
 * Appends streamed lines (ANSI colours stripped) and drops the oldest beyond max. `multi` lists the pods with more
 * than one container (their prefix names the container).
 */
export function appendKubeLogRows(
  prev: KubeLogRow[],
  lines: readonly KubeLogLine[],
  multi: ReadonlySet<string> = new Set(),
  max = MAX_KUBE_LOG_ROWS,
): KubeLogRow[] {
  if (lines.length === 0) return prev
  let id = prev.length ? prev[prev.length - 1].id + 1 : 0
  const added = lines.map((l) => ({
    id: id++,
    source: multi.has(l.pod) ? `${l.pod}/${l.container}` : l.pod,
    pod: l.pod,
    container: l.container,
    t: l.line.t,
    stream: l.line.stream,
    text: l.line.text.replace(ANSI, ""),
  }))
  const out = prev.concat(added)
  return out.length > max ? out.slice(out.length - max) : out
}

/** Plain-text copy of log rows, like `kubectl logs --prefix` prints them. */
export function kubeLogRowsText(rows: readonly KubeLogRow[]): string {
  return rows.map((r) => `[${r.source}] ${r.text}`).join("\n")
}
