import type { LoadSample, LoadTestRun, NodeTraffic, ScenarioId, ScenarioVerdict, WorkflowKind } from "@/lib/api/types"
import { jsonEqual } from "@/lib/json-equal"
import { formatMs, formatPercent } from "@/features/panels/lib/format"
import { SCENARIO_LABEL, VERDICT_LABEL, instantScenario } from "@/features/panels/loadtest/scenario-result"

/**
 * Live traffic on the canvas while a load test runs. Everything shown is measured: the load generator's per-second
 * samples for the node it targets, the workflow's Prometheus for services that export request metrics, and the
 * containers' CPU (already on every card). Edges move only where requests are known to flow; no per-edge split is
 * ever estimated.
 */

/**
 * Error rates up to this are healthy, at or above ERROR_RATE_FAILED the target is failing: the incident verdict's
 * thresholds (scenario.ErrorRateOK, scenario.ErrorRateFailed in the daemon), so the chip and the verdict agree.
 */
export const ERROR_RATE_OK = 0.01
export const ERROR_RATE_FAILED = 0.5
/** Seconds of samples the target's numbers cover: one second alone is noisy. */
export const SAMPLE_WINDOW = 3
/** How long a finished run stays on the canvas with its final numbers. */
export const LINGER_MS = 30_000
/**
 * Below this a measured node shows no chip: health checks and scrapes are not traffic (the monitoring's HTTP alerts
 * ignore the same 0.5 req/s, monitoring.DefaultThresholds.MinRequestsPerSecond in the daemon).
 */
export const MIN_RPS = 0.5

export type TrafficTone = "ok" | "warning" | "error"

export function trafficTone(errorRate: number): TrafficTone {
  if (errorRate >= ERROR_RATE_FAILED) return "error"
  return errorRate > ERROR_RATE_OK ? "warning" : "ok"
}

/** One node's live numbers. */
export interface NodeLive {
  /** "target": the load test sends its requests here (measured by Nimbo's generator); "measured": Prometheus. */
  role: "target" | "measured"
  rps: number
  errorRate: number
  p95Ms?: number
  tone: TrafficTone
  /** The run is over: these are its final numbers. */
  final?: boolean
  /** Where a "measured" node's numbers come from (otel, traefik, ingress-nginx). */
  source?: string
}

/** An edge requests are known to flow along; rps when the receiving node's measured requests all arrive through it. */
export interface EdgeLive {
  rps?: number
}

/** The target's numbers: the last SAMPLE_WINDOW seconds while it runs, the run's summary once it is over. */
export function targetLive(run: LoadTestRun, samples: LoadSample[]): NodeLive | null {
  if (run.status !== "running") {
    const s = run.summary
    if (!s) return null
    return { role: "target", rps: s.rps, errorRate: s.errorRate, p95Ms: s.latency.p95, tone: trafficTone(s.errorRate), final: true }
  }
  const recent = samples.slice(-SAMPLE_WINDOW)
  if (recent.length === 0) return null
  const requests = recent.reduce((a, s) => a + s.rps, 0)
  const errors = recent.reduce((a, s) => a + s.errors, 0)
  const errorRate = requests > 0 ? Math.min(1, errors / requests) : 0
  const p95 = [...recent].reverse().find((s) => s.rps > 0)?.p95
  return { role: "target", rps: requests / recent.length, errorRate, p95Ms: p95, tone: trafficTone(errorRate) }
}

export function measuredLive(n: NodeTraffic, final: boolean): NodeLive {
  return { role: "measured", rps: n.rps, errorRate: n.errorRate, p95Ms: n.p95Ms, tone: trafficTone(n.errorRate), source: n.source, ...(final && { final }) }
}

/** A canvas edge, from the caller to what it calls (Docker) or along the route (Kubernetes). */
export interface FlowEdge {
  id: string
  source: string
  target: string
  kind: string
}

/** Edge kinds requests travel along, per workspace kind (volumes, networks, config… carry none). */
export const TRAFFIC_KINDS: Record<WorkflowKind, Partial<Record<string, true>>> = {
  docker: { depends_on: true, links: true, inferred: true },
  kubernetes: { routes: true, selects: true, inferred: true },
  "": {},
}

/** Kinds that route every request onward (an Ingress to its Service, a Service to the pods it selects). */
const ROUTING_KINDS: Record<WorkflowKind, Partial<Record<string, true>>> = {
  docker: {},
  kubernetes: { routes: true, selects: true },
  "": {},
}

/**
 * Edges the load test's requests certainly follow from its target: routing edges (Kubernetes Ingress → Service →
 * workload) while each step has a single way on. A step with several (an Ingress routing paths to two Services)
 * splits the requests in a way nothing measures, so the path stops there. Docker targets are reached directly.
 */
export function entryPath(kind: WorkflowKind, edges: FlowEdge[], target: string): string[] {
  const routing = ROUTING_KINDS[kind]
  const path: string[] = []
  const seen = new Set([target])
  let at = target
  for (;;) {
    const out = edges.filter((e) => e.source === at && routing[e.kind])
    if (out.length !== 1 || seen.has(out[0].target)) return path
    path.push(out[0].id)
    at = out[0].target
    seen.add(at)
  }
}

/**
 * Traffic edges into a node Prometheus sees receiving requests. The number is the receiver's total, so it is only
 * written on the edge when that edge is the receiver's only incoming traffic edge.
 */
export function measuredEdges(kind: WorkflowKind, edges: FlowEdge[], receiving: Map<string, number>): Record<string, EdgeLive> {
  const kinds = TRAFFIC_KINDS[kind]
  const into = new Map<string, FlowEdge[]>()
  for (const e of edges) {
    if (!kinds[e.kind] || !receiving.has(e.target)) continue
    into.set(e.target, [...(into.get(e.target) ?? []), e])
  }
  const out: Record<string, EdgeLive> = {}
  for (const [node, list] of into) {
    for (const e of list) out[e.id] = list.length === 1 ? { rps: receiving.get(node) } : {}
  }
  return out
}

/** `next` with every entry equal to `prev`'s kept as `prev`'s object (or `prev` itself when nothing changed). */
export function shareRecord<T>(prev: Record<string, T>, next: Record<string, T>): Record<string, T> {
  let same = Object.keys(prev).length === Object.keys(next).length
  const out: Record<string, T> = {}
  for (const [k, v] of Object.entries(next)) {
    const old = prev[k]
    if (old !== undefined && jsonEqual(old, v)) out[k] = old
    else {
      out[k] = v
      same = false
    }
  }
  return same ? prev : out
}

/** An incident on a node while (and shortly after) its failure test runs. */
export interface FaultLive {
  runId: string
  nodeId: string
  scenario: ScenarioId
  /** What the node is going through now: "Stopped", "+300 ms latency"… */
  label: string
  /**
   * pending: before the fault (after the baseline traffic); active: injected; reverted: put back, recovery watched;
   * failed: could not be injected; done: the run is over (verdict).
   */
  phase: "pending" | "active" | "reverted" | "failed" | "done"
  /** When the current countdown ends (ms since epoch): the injection, then the revert. */
  until?: number
  detail?: string
  verdict?: ScenarioVerdict
  message?: string
}

/** What a fault does to its node, as its chip says it. */
export function faultLabel(id: ScenarioId, kind: WorkflowKind, params: Record<string, string> | undefined): string {
  switch (id) {
    case "kill_replica":
      return "Replica killed"
    case "dependency_down":
      return kind === "kubernetes" ? "Scaled to 0" : "Stopped"
    case "dependency_latency": {
      const delay = Number(params?.delayMs ?? 300)
      const jitter = Number(params?.jitterMs ?? 0)
      const loss = Number(params?.lossPct ?? 0)
      return [`+${delay}${jitter > 0 ? `±${jitter}` : ""} ms latency`, loss > 0 && `${loss}% loss`].filter(Boolean).join(" · ")
    }
    case "cache_flush":
      return "Cache flushed"
    case "traffic_spike":
      return `Traffic ×${Number(params?.factor ?? 3)}`
  }
}

/**
 * The fault state of `run`'s incident at `now`. `loadStart` is when its traffic started (ms; the samples' t = 0):
 * the incident's schedule counts from there. Null when the run has no incident on a node.
 */
export function faultLive(run: LoadTestRun, kind: WorkflowKind, loadStart: number | undefined, now: number): FaultLive | null {
  const spec = run.spec.scenario
  if (!spec) return null
  const nodeId = spec.id === "traffic_spike" ? run.spec.target.nodeId : spec.target
  if (!nodeId) return null
  const result = run.scenario
  const base = { runId: run.id, nodeId, scenario: spec.id, label: faultLabel(spec.id, kind, spec.params), detail: result?.detail }
  if (run.status !== "running") {
    // Stopped before the fault was due: nothing happened to the node.
    if (!result) return null
    return { ...base, phase: "done", verdict: result.verdict || undefined, message: result.message }
  }
  const at = (sec: number) => (loadStart === undefined ? undefined : loadStart + sec * 1000)
  const injectAt = at(spec.atSec)
  const revertAt = at(spec.atSec + spec.durationSec)
  if (spec.id === "traffic_spike") {
    // The spike is part of the traffic schedule: no status event marks it.
    if (loadStart === undefined || now < injectAt!) return { ...base, phase: "pending", until: injectAt }
    return now < revertAt! ? { ...base, phase: "active", until: revertAt } : { ...base, phase: "reverted" }
  }
  if (result?.message && result.revertedAtSec === undefined) return { ...base, phase: "failed", message: result.message }
  if (result?.revertedAtSec !== undefined) return { ...base, phase: "reverted" }
  if (result) return { ...base, phase: "active", until: revertAt }
  return { ...base, phase: "pending", until: injectAt }
}

/** The chip text of a fault at `now` (whole seconds left). */
export function faultText(f: FaultLive, now: number): string {
  const left = f.until !== undefined ? Math.max(0, Math.ceil((f.until - now) / 1000)) : undefined
  const instant = instantScenario(f.scenario)
  switch (f.phase) {
    case "pending":
      return left !== undefined ? `${SCENARIO_LABEL[f.scenario]} in ${left} s` : `${SCENARIO_LABEL[f.scenario]} soon`
    case "active":
      if (left === undefined) return f.label
      return instant ? `${f.label} · watching ${left} s` : `${f.label} · reverts in ${left} s`
    case "reverted":
      return instant ? "Watching recovery" : "Reverted · recovering"
    case "failed":
      return "Fault not injected"
    case "done":
      return f.verdict ? VERDICT_LABEL[f.verdict] : "Stopped, no verdict"
  }
}

/** "42 req/s" (one decimal under 10). */
export function formatRps(rps: number): string {
  return `${rps < 10 ? rps.toFixed(1) : Math.round(rps)} req/s`
}

/** Request rate, p95 and (when any) the error share of a node. */
export function trafficLine(l: Pick<NodeLive, "rps" | "p95Ms" | "errorRate">): string {
  return [formatRps(l.rps), l.p95Ms ? `p95 ${formatMs(l.p95Ms)}` : undefined, l.errorRate > 0 ? `${formatPercent(l.errorRate, 1)} errors` : undefined]
    .filter(Boolean)
    .join(" · ")
}
