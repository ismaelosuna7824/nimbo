import type { LoadProfile, LoadTarget, ScenarioCatalog, ScenarioId, ScenarioOption, WorkflowKind } from "@/lib/api/types"
import { initialSpec, scenarioProblem } from "@/features/panels/loadtest/scenario-timing"

/** A failure the guided dialog lists for a node: runnable, or disabled with why. */
export interface FailureChoice {
  id: ScenarioId
  title: string
  option?: ScenarioOption
  unavailable?: string
}

/**
 * The incident catalog seen from one node: the scenarios that act on it, the traffic spike when the load test can
 * target it, and every other scenario disabled with the catalog's reason (or what it acts on instead).
 */
export function failureChoices(catalog: ScenarioCatalog, nodeId: string, nodeName: string, targets: LoadTarget[]): FailureChoice[] {
  const out: FailureChoice[] = []
  for (const o of catalog.scenarios) {
    if (o.id === "traffic_spike") {
      out.push(
        targets.some((t) => t.nodeId === nodeId)
          ? { id: o.id, title: o.title, option: o }
          : { id: o.id, title: o.title, unavailable: `The load test can't send traffic to ${nodeName} directly.` },
      )
      continue
    }
    if (o.targets.some((t) => t.id === nodeId)) out.push({ id: o.id, title: o.title, option: { ...o, targets: o.targets.filter((t) => t.id === nodeId) } })
    else out.push({ id: o.id, title: o.title, unavailable: `Not for ${nodeName}: it acts on ${o.targets.map((t) => t.name).join(", ")}.` })
  }
  for (const u of catalog.unavailable) out.push({ id: u.id, title: u.title, unavailable: u.reason })
  // Runnable first, keeping the catalog's order.
  return [...out.filter((c) => c.option), ...out.filter((c) => !c.option)]
}

/**
 * Load targets ordered by how directly their requests reach `faulted`: the node itself first for a crash or a
 * spike, its callers first for a dependency fault (traffic to the broken dependency alone shows nothing about the
 * app using it). `edges` go from caller to callee (or along routes); unreachable targets come last.
 */
export function rankTargets(targets: LoadTarget[], edges: { source: string; target: string }[], faulted: string, scenario: ScenarioId): LoadTarget[] {
  const dist = (from: string | undefined) => {
    if (!from) return Infinity
    const seen = new Map([[from, 0]])
    const queue = [from]
    while (queue.length) {
      const at = queue.shift()!
      if (at === faulted) return seen.get(at)!
      for (const e of edges) {
        if (e.source === at && !seen.has(e.target)) {
          seen.set(e.target, seen.get(at)! + 1)
          queue.push(e.target)
        }
      }
    }
    return Infinity
  }
  const onNode = scenario === "kill_replica" || scenario === "traffic_spike"
  const score = (t: LoadTarget) => {
    const d = dist(t.nodeId)
    // A dependency fault wants traffic through the node, not to it.
    const rank = !onNode && d === 0 ? 1000 : d
    return rank + (t.kind === "http" ? 0 : 0.5)
  }
  return targets
    .map((t, i) => ({ t, i, s: score(t) }))
    .sort((a, b) => a.s - b.s || a.i - b.i)
    .map((x) => x.t)
}

/** A traffic shape can host the incident with its default timing (baseline before, recovery after). */
export function presetFits(option: ScenarioOption, profile: LoadProfile): boolean {
  return scenarioProblem(initialSpec(option, profile), profile) === null
}

/** What Nimbo does to the node, in plain words, per workspace kind. */
export function faultAction(id: ScenarioId, kind: WorkflowKind, name: string): string {
  const k8s = kind === "kubernetes"
  switch (id) {
    case "kill_replica":
      return k8s
        ? `Nimbo force-deletes one pod of ${name}, as if its node lost it. Kubernetes starts a replacement; other replicas keep serving.`
        : `Nimbo kills the main process of one ${name} container (SIGKILL), like a crash. Docker's restart policy brings it back; other replicas keep serving.`
    case "dependency_down":
      return k8s
        ? `Nimbo scales ${name} to 0 replicas, then back to its replicas when the time is up.`
        : `Nimbo pauses the ${name} container (docker pause): it stops answering but keeps its data, and is resumed when the time is up.`
    case "dependency_latency":
      return `Nimbo adds network delay to ${k8s ? `the pods of ${name}` : name} with tc netem from a helper that shares its network, and removes it when the time is up.`
    case "cache_flush":
      return `Nimbo empties ${name} once (FLUSHALL on Redis/Valkey/KeyDB, flush_all on Memcached). What was cached is gone; it is a cache, so the app must cope.`
    case "traffic_spike":
      return "Nimbo multiplies the test's traffic by the factor for the window, then goes back to the normal rate."
  }
}

/** What to expect while it happens. */
export function faultExpect(id: ScenarioId, kind: WorkflowKind, name: string): string {
  switch (id) {
    case "kill_replica":
      return `Requests on the crashed replica fail for a moment; a resilient setup recovers within seconds.`
    case "dependency_down":
      return `Requests that need ${name} fail or wait for their timeout. A resilient app fails fast and recovers as soon as ${name} is back.`
    case "dependency_latency":
      return `Requests that use ${name} slow down by about the delay; watch for timeouts and retries piling up.`
    case "cache_flush":
      return "Cache misses: latency and database load jump until the cache warms up again."
    case "traffic_spike":
      return kind === "kubernetes" ? "Errors and latency under the surge, and whether autoscaling keeps up." : "Errors and latency under the surge, and how fast they settle afterwards."
  }
}

/** The path of an HTTP target's URL ("/" for "http://127.0.0.1:3100"). */
export function targetPath(t: LoadTarget): string {
  if (!t.http) return ""
  const u = new URL(t.http.url)
  return u.pathname + u.search
}

/**
 * `t` sent to another path of the same service: a dependency fault only shows on requests that use the dependency,
 * and the suggested target is the service's root. Keeps the node, so the canvas still marks it as the target.
 */
export function withPath(t: LoadTarget, path: string): LoadTarget {
  if (!t.http) return t
  const p = `/${path.trim().replace(/^\/+/, "")}`
  if (p === targetPath(t)) return t
  const at = t.label.indexOf(" · ")
  return { ...t, label: `${t.http.method} ${p}${at >= 0 ? t.label.slice(at) : ""}`, http: { ...t.http, url: new URL(t.http.url).origin + p } }
}
