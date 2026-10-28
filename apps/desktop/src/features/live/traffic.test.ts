import { describe, expect, test } from "bun:test"
import type { LoadSample, LoadTestRun, ScenarioCatalog, LoadTarget } from "@/lib/api/types"
import { failureChoices, rankTargets, targetPath, withPath } from "./failure"
import {
  entryPath,
  faultLive,
  faultText,
  measuredEdges,
  shareRecord,
  targetLive,
  trafficLine,
  trafficTone,
  type FlowEdge,
} from "./traffic"

const sample = (t: number, rps: number, errors = 0, p95 = 20): LoadSample => ({ t, target: rps, rps, errors, inFlight: 1, p50: 10, p95, p99: 30, max: 40 })

const run = (over: Partial<LoadTestRun> = {}): LoadTestRun => ({
  id: "r1",
  workflowId: "w",
  spec: {
    name: "t",
    target: { kind: "http", nodeId: "service:api", label: "GET / · api" },
    profile: { preset: "load", mode: "rps", stages: [{ durationSec: 100, target: 50 }] },
    thresholds: [],
    timeoutMs: 5000,
  },
  status: "running",
  startedAt: "2026-10-27T10:00:00Z",
  timeline: [],
  ...over,
})

describe("trafficTone", () => {
  test("follows the incident verdict's thresholds", () => {
    expect(trafficTone(0)).toBe("ok")
    expect(trafficTone(0.01)).toBe("ok")
    expect(trafficTone(0.02)).toBe("warning")
    expect(trafficTone(0.49)).toBe("warning")
    expect(trafficTone(0.5)).toBe("error")
  })
})

describe("targetLive", () => {
  test("averages the last 3 seconds while running", () => {
    const live = targetLive(run(), [sample(1, 100, 100), sample(2, 40, 0, 15), sample(3, 50, 1, 25), sample(4, 60, 2, 30)])!
    expect(live.rps).toBe(50)
    expect(live.errorRate).toBeCloseTo(3 / 150)
    expect(live.p95Ms).toBe(30)
    expect(live.tone).toBe("warning")
    expect(live.final).toBeUndefined()
  })

  test("uses the summary once the run is over", () => {
    const done = run({
      status: "passed",
      summary: {
        requests: 1000,
        errors: 0,
        errorRate: 0,
        rps: 48.2,
        peakRps: 50,
        bytesIn: 0,
        latency: { min: 1, mean: 5, stddev: 1, p50: 4, p90: 8, p95: 9, p99: 12, max: 20 },
        statusCodes: {},
        errorKinds: {},
        stability: 0.1,
      },
    })
    expect(targetLive(done, [])).toEqual({ role: "target", rps: 48.2, errorRate: 0, p95Ms: 9, tone: "ok", final: true })
  })

  test("nothing before the first sample", () => {
    expect(targetLive(run(), [])).toBeNull()
  })
})

describe("trafficLine", () => {
  test("hides a zero error share", () => {
    expect(trafficLine({ rps: 42.4, errorRate: 0, p95Ms: 18 })).toBe("42 req/s · p95 18ms")
    expect(trafficLine({ rps: 2.25, errorRate: 0.125 })).toBe("2.3 req/s · 12.5% errors")
  })
})

const k8sEdges: FlowEdge[] = [
  { id: "ing", source: "Ingress/shop/web", target: "Service/shop/web", kind: "routes" },
  { id: "sel", source: "Service/shop/web", target: "Deployment/shop/web", kind: "selects" },
  { id: "env", source: "Deployment/shop/web", target: "Service/shop/db", kind: "inferred" },
  { id: "sel-db", source: "Service/shop/db", target: "StatefulSet/shop/db", kind: "selects" },
]

describe("entryPath", () => {
  test("follows Kubernetes routing from the target while it has one way on", () => {
    expect(entryPath("kubernetes", k8sEdges, "Ingress/shop/web")).toEqual(["ing", "sel"])
    expect(entryPath("kubernetes", k8sEdges, "Service/shop/web")).toEqual(["sel"])
  })

  test("stops where requests split", () => {
    const split = [...k8sEdges, { id: "ing2", source: "Ingress/shop/web", target: "Service/shop/admin", kind: "routes" }]
    expect(entryPath("kubernetes", split, "Ingress/shop/web")).toEqual([])
  })

  test("docker targets are reached directly", () => {
    expect(entryPath("docker", [{ id: "d", source: "service:api", target: "service:db", kind: "depends_on" }], "service:api")).toEqual([])
  })
})

describe("measuredEdges", () => {
  const edges: FlowEdge[] = [
    { id: "web-api", source: "service:web", target: "service:api", kind: "depends_on" },
    { id: "api-db", source: "service:api", target: "service:db", kind: "depends_on" },
    { id: "worker-db", source: "service:worker", target: "service:db", kind: "inferred" },
    { id: "api-vol", source: "service:api", target: "volume:data", kind: "volume" },
  ]

  test("moves traffic edges into measured nodes; labels only a node's single way in", () => {
    const got = measuredEdges(
      "docker",
      edges,
      new Map([
        ["service:api", 40],
        ["service:db", 90],
      ]),
    )
    expect(got).toEqual({ "web-api": { rps: 40 }, "api-db": {}, "worker-db": {} })
  })

  test("nothing without measurements", () => {
    expect(measuredEdges("docker", edges, new Map())).toEqual({})
  })
})

describe("shareRecord", () => {
  test("keeps unchanged entries and the record itself", () => {
    const a = { rps: 1 }
    const b = { rps: 2 }
    const prev = { a, b }
    expect(shareRecord(prev, { a: { rps: 1 }, b: { rps: 2 } })).toBe(prev)
    const next = shareRecord(prev, { a: { rps: 1 }, b: { rps: 3 } })
    expect(next).not.toBe(prev)
    expect(next.a).toBe(a)
    expect(next.b).toEqual({ rps: 3 })
  })
})

describe("faultLive", () => {
  const withScenario = (over: Partial<LoadTestRun> = {}) => {
    const r = run(over)
    return { ...r, spec: { ...r.spec, scenario: { id: "dependency_down" as const, target: "service:db", atSec: 25, durationSec: 30 } } }
  }
  const start = 1_000_000

  test("counts down to the injection, then to the revert", () => {
    const pending = faultLive(withScenario(), "docker", start, start + 10_000)!
    expect(pending).toMatchObject({ nodeId: "service:db", phase: "pending", until: start + 25_000, label: "Stopped" })
    expect(faultText(pending, start + 10_000)).toBe("Dependency down in 15 s")

    const active = faultLive(withScenario({ scenario: { id: "dependency_down", target: "service:db", targetName: "db", injectedAtSec: 25, verdict: "", message: "", checks: [] } }), "docker", start, start + 30_000)!
    expect(active).toMatchObject({ phase: "active", until: start + 55_000 })
    expect(faultText(active, start + 30_000)).toBe("Stopped · reverts in 25 s")
  })

  test("reverted, then the verdict", () => {
    const reverted = faultLive(
      withScenario({ scenario: { id: "dependency_down", target: "service:db", injectedAtSec: 25, revertedAtSec: 55, verdict: "", message: "", checks: [] } }),
      "kubernetes",
      start,
      start + 60_000,
    )!
    expect(reverted.phase).toBe("reverted")
    expect(reverted.label).toBe("Scaled to 0")
    const done = faultLive(
      withScenario({ status: "passed", scenario: { id: "dependency_down", target: "service:db", injectedAtSec: 25, revertedAtSec: 55, verdict: "degraded", message: "Recovered in 14 s", checks: [] } }),
      "docker",
      start,
      start + 99_000,
    )!
    expect(done).toMatchObject({ phase: "done", verdict: "degraded" })
    expect(faultText(done, 0)).toBe("Degraded")
  })

  test("no verdict: target already failing, or stopped by the user", () => {
    const ended = (verdict: "" | "inconclusive") =>
      faultLive(
        withScenario({ status: "stopped", scenario: { id: "dependency_down", target: "service:db", injectedAtSec: 25, revertedAtSec: 30, verdict, message: "", checks: [] } }),
        "docker",
        start,
        start,
      )!
    expect(faultText(ended("inconclusive"), 0)).toBe("Not assessed")
    expect(faultText(ended(""), 0)).toBe("Stopped, no verdict")
  })

  test("a run stopped before the fault left the node alone", () => {
    expect(faultLive(withScenario({ status: "stopped" }), "docker", start, start)).toBeNull()
  })

  test("injection errors", () => {
    const failed = faultLive(withScenario({ scenario: { id: "dependency_down", target: "service:db", injectedAtSec: 25, verdict: "", message: "The fault could not be injected: no container", checks: [] } }), "docker", start, start)!
    expect(failed.phase).toBe("failed")
  })

  test("a traffic spike is on the target, timed by the traffic clock", () => {
    const r = run()
    const spike = { ...r, spec: { ...r.spec, scenario: { id: "traffic_spike" as const, target: "", atSec: 20, durationSec: 30, params: { factor: "4" } } } }
    expect(faultLive(spike, "docker", start, start + 25_000)).toMatchObject({ nodeId: "service:api", phase: "active", label: "Traffic ×4" })
    expect(faultLive(spike, "docker", start, start + 60_000)?.phase).toBe("reverted")
  })
})

const catalog: ScenarioCatalog = {
  kind: "docker",
  scenarios: [
    { id: "kill_replica", title: "Kill a replica", description: "", needsTarget: true, targets: [{ id: "service:api", name: "api", detail: "" }], defaultDurationSec: 30 },
    { id: "dependency_down", title: "Dependency down", description: "", needsTarget: true, targets: [{ id: "service:db", name: "db", detail: "" }, { id: "service:cache", name: "cache", detail: "" }], defaultDurationSec: 20 },
    { id: "traffic_spike", title: "Traffic spike", description: "", needsTarget: false, targets: [], defaultDurationSec: 30 },
  ],
  unavailable: [{ id: "cache_flush", title: "Flush the cache", reason: "No Redis, Valkey or Memcached service." }],
}
const targets: LoadTarget[] = [{ kind: "http", nodeId: "service:api", label: "GET / · api" }]

describe("failureChoices", () => {
  test("what acts on the node first, the rest disabled with why", () => {
    const got = failureChoices(catalog, "service:db", "db", targets)
    expect(got.map((c) => [c.id, !!c.option])).toEqual([
      ["dependency_down", true],
      ["kill_replica", false],
      ["traffic_spike", false],
      ["cache_flush", false],
    ])
    expect(got[0].option!.targets).toEqual([{ id: "service:db", name: "db", detail: "" }])
    expect(got[1].unavailable).toBe("Not for db: it acts on api.")
    expect(got[3].unavailable).toBe("No Redis, Valkey or Memcached service.")
  })

  test("a spike needs traffic to the node", () => {
    expect(failureChoices(catalog, "service:api", "api", targets).filter((c) => c.option).map((c) => c.id)).toEqual(["kill_replica", "traffic_spike"])
  })
})

describe("rankTargets", () => {
  const edges = [
    { source: "service:web", target: "service:api" },
    { source: "service:api", target: "service:db" },
  ]
  const all: LoadTarget[] = [
    { kind: "http", nodeId: "service:db", label: "db" },
    { kind: "http", nodeId: "service:web", label: "web" },
    { kind: "http", nodeId: "service:api", label: "api" },
  ]
  test("callers first for a dependency fault", () => {
    expect(rankTargets(all, edges, "service:db", "dependency_down").map((t) => t.label)).toEqual(["api", "web", "db"])
  })
  test("the node itself first for a crash", () => {
    expect(rankTargets(all, edges, "service:api", "kill_replica").map((t) => t.label)).toEqual(["api", "web", "db"])
  })
})

describe("withPath", () => {
  const api: LoadTarget = { kind: "http", nodeId: "service:api", label: "GET / · api:3000", http: { method: "GET", url: "http://127.0.0.1:3100/" } }
  test("another path of the same service, still its node", () => {
    expect(withPath(api, "notes")).toEqual({ kind: "http", nodeId: "service:api", label: "GET /notes · api:3000", http: { method: "GET", url: "http://127.0.0.1:3100/notes" } })
    expect(withPath(api, "/")).toBe(api)
    expect(withPath(api, "//notes").http!.url).toBe("http://127.0.0.1:3100/notes")
    expect(targetPath(withPath(api, "/notes?limit=5"))).toBe("/notes?limit=5")
  })
})
