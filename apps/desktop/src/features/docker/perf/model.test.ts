import { describe, expect, test } from "bun:test"
import type { BuildStep, ComposeBuild, ComposeProblem, LoadResources, ServiceBuildStats, ServiceUsage } from "@/lib/api/types"
import {
  hpaLabel,
  scalingRows,
  scalingTone,
  cpuLimitLabel,
  cpuOfLimit,
  formatBytes,
  formatDelta,
  formatDuration,
  formatPct,
  formatSize,
  latestBuilds,
  memLimitLabel,
  memOfLimit,
  ownSize,
  problemTone,
  resourceRows,
  resourcesTone,
  sparklineCount,
  sparklinePoints,
  stepSummary,
  timeDomain,
  usageLine,
  usageTone,
  verdictTone,
} from "./model"

const usage = (over: Partial<ServiceUsage> = {}): ServiceUsage => ({
  service: "api",
  t: "2026-10-22T10:00:00Z",
  containers: 1,
  cpuPercent: 12.5,
  cpuLimit: 0.25,
  cpuLimited: true,
  memBytes: 118 * 1024 * 1024,
  memLimit: 512 * 1024 * 1024,
  memLimited: true,
  netRxBytes: 0,
  netTxBytes: 0,
  blockReadBytes: 0,
  blockWriteBytes: 0,
  pids: 4,
  ...over,
})

const step = (name: string, cached: boolean, durationMs: number, internal = false): BuildStep => ({
  name,
  instruction: name,
  cached,
  durationMs,
  internal,
})

const svcBuild = (service: string, over: Partial<ServiceBuildStats> = {}): ServiceBuildStats => ({
  service,
  nodeId: `service:${service}`,
  durationMs: 1000,
  cacheHits: 0,
  cacheMisses: 0,
  steps: [],
  sizeBytes: 0,
  baseSizeBytes: 0,
  layers: [],
  findings: [],
  ...over,
})

describe("formatting", () => {
  test("formatBytes uses binary units", () => {
    expect(formatBytes(512)).toBe("512 B")
    expect(formatBytes(1536)).toBe("1.5 KiB")
    expect(formatBytes(118 * 1024 * 1024)).toBe("118 MiB")
    expect(formatBytes(2 * 1024 ** 3)).toBe("2 GiB")
  })

  test("formatSize uses decimal units", () => {
    expect(formatSize(12_300_000)).toBe("12.3 MB")
    expect(formatSize(1_200_000_000)).toBe("1.2 GB")
    expect(formatSize(250_000_000)).toBe("250 MB")
  })

  test("formatDuration", () => {
    expect(formatDuration(850)).toBe("850 ms")
    expect(formatDuration(3200)).toBe("3.2 s")
    expect(formatDuration(42_000)).toBe("42 s")
    expect(formatDuration(125_000)).toBe("2 m 5 s")
    expect(formatDuration(120_000)).toBe("2 m")
  })

  test("formatPct", () => {
    expect(formatPct(23.4)).toBe("23%")
    expect(formatPct(4.56)).toBe("4.6%")
    expect(formatPct(0)).toBe("0%")
  })

  test("limit labels", () => {
    expect(cpuLimitLabel(0.25, true)).toBe("0.25 CPU")
    expect(cpuLimitLabel(2, true)).toBe("2 CPUs")
    expect(cpuLimitLabel(14, false)).toBe("no limit · 14 CPUs")
    expect(memLimitLabel(512 * 1024 * 1024, true)).toBe("512 MiB")
    expect(memLimitLabel(0, false)).toBe("no limit")
  })
})

describe("usage of limits", () => {
  test("cpuOfLimit divides docker's per-core % by the limit", () => {
    expect(cpuOfLimit(usage({ cpuPercent: 24.5, cpuLimit: 0.25 }))).toBeCloseTo(98)
    expect(cpuOfLimit(usage({ cpuPercent: 140, cpuLimit: 14 }))).toBeCloseTo(10)
    expect(cpuOfLimit(usage({ cpuPercent: 30, cpuLimit: 0 }))).toBe(30)
  })

  test("memOfLimit", () => {
    expect(memOfLimit(usage({ memBytes: 256, memLimit: 512 }))).toBe(50)
    expect(memOfLimit(usage({ memLimit: 0 }))).toBe(0)
  })

  test("usageLine", () => {
    expect(usageLine(usage({ cpuPercent: 5.75, cpuLimit: 0.25 }))).toBe("CPU 23% · 118 MiB")
  })
})

describe("formatDelta", () => {
  test("bigger and slower are worse", () => {
    expect(formatDelta(12_300_000, "bytes")).toMatchObject({ text: "+12.3 MB", tone: "worse" })
    expect(formatDelta(-1200, "ms")).toMatchObject({ text: "\u22121.2 s", tone: "better" })
    expect(formatDelta(2, "count")).toMatchObject({ text: "+2", tone: "worse" })
  })

  test("higherIsBetter flips the tone (cache hits)", () => {
    expect(formatDelta(2, "count", true)).toMatchObject({ text: "+2", tone: "better" })
    expect(formatDelta(-3, "count", true)).toMatchObject({ text: "\u22123", tone: "worse" })
  })

  test("negligible changes are neutral", () => {
    expect(formatDelta(0, "count")).toMatchObject({ text: "±0", tone: "same" })
    expect(formatDelta(20, "ms")).toMatchObject({ tone: "same" })
    expect(formatDelta(-300, "bytes")).toMatchObject({ tone: "same" })
  })
})

describe("sparkline", () => {
  test("count follows the stats interval", () => {
    expect(sparklineCount(1000)).toBe(60)
    expect(sparklineCount(2000)).toBe(30)
    expect(sparklineCount(undefined)).toBe(60)
  })

  test("needs two samples", () => {
    expect(sparklinePoints([usage()], 60, 16)).toBe("")
  })

  test("normalises to % of limit, clamps and right-aligns", () => {
    const pts = sparklinePoints(
      [usage({ cpuPercent: 0 }), usage({ cpuPercent: 25 }), usage({ cpuPercent: 50 })],
      100,
      12,
      3,
    )
    // 0% → bottom (y = 11), 100% → top (y = 1), 200% clamped to 100%.
    expect(pts).toBe("0.0,11.0 50.0,1.0 100.0,1.0")
    const partial = sparklinePoints([usage({ cpuPercent: 0 }), usage({ cpuPercent: 0 })], 100, 12, 5).split(" ")
    expect(partial[0]).toBe("75.0,11.0")
    expect(partial[1]).toBe("100.0,11.0")
  })
})

describe("builds", () => {
  test("stepSummary counts Dockerfile steps (FROM excluded) and lists the slowest uncached", () => {
    const s = stepSummary(
      [
        step("FROM node:22", false, 20_000),
        step("npm ci", false, 40_000),
        step("COPY", true, 10),
        step("build", false, 9000),
        step("[internal] load", false, 99_000, true),
        step("tiny", false, 100),
      ],
      2,
    )
    expect(s.total).toBe(4)
    expect(s.cached).toBe(1)
    expect(s.executed).toBe(3)
    expect(s.slow.map((x) => x.name)).toEqual(["npm ci", "FROM node:22"])
  })

  test("ownSize is unknown without a base size", () => {
    expect(ownSize({ sizeBytes: 300, baseSizeBytes: 200 })).toBe(100)
    expect(ownSize({ sizeBytes: 300, baseSizeBytes: 0 })).toBeUndefined()
    expect(ownSize({ sizeBytes: 100, baseSizeBytes: 200 })).toBe(0)
  })

  test("latestBuilds keeps the newest stats of each service", () => {
    const newer: ComposeBuild = { runId: "r2", action: "build", at: "2", durationMs: 1, succeeded: true, services: [svcBuild("api", { durationMs: 2 })] }
    const older: ComposeBuild = {
      runId: "r1",
      action: "up",
      at: "1",
      durationMs: 1,
      succeeded: true,
      services: [svcBuild("api", { durationMs: 1 }), svcBuild("web")],
    }
    const out = latestBuilds([newer, older])
    expect(out.map((x) => [x.stats.service, x.build.runId])).toEqual([
      ["api", "r2"],
      ["web", "r1"],
    ])
  })
})

describe("tones", () => {
  test("verdictTone", () => {
    expect(verdictTone({ kind: "ok", bottleneck: false })).toBe("ok")
    expect(verdictTone({ kind: "cpu-bound", bottleneck: true })).toBe("warning")
    expect(verdictTone({ kind: "single-core", bottleneck: false })).toBe("warning")
    expect(verdictTone({ kind: "oom", bottleneck: true })).toBe("error")
    expect(verdictTone({ kind: "restarted", bottleneck: false })).toBe("error")
  })

  test("resourcesTone is the worst verdict", () => {
    const r = (kinds: LoadResources["verdicts"][number]["kind"][]): LoadResources => ({
      services: [],
      verdict: "",
      verdicts: kinds.map((kind) => ({ service: "s", kind, bottleneck: kind !== "ok", message: "" })),
    })
    expect(resourcesTone(r([]))).toBe("ok")
    expect(resourcesTone(r(["ok", "memory-bound"]))).toBe("warning")
    expect(resourcesTone(r(["cpu-bound", "oom"]))).toBe("error")
  })

  test("an autoscaling problem in the headline warns", () => {
    const scaled = (kind: "late" | "scaled" | "no_hpa"): LoadResources => ({
      services: [],
      verdict: "web: headline",
      verdicts: [{ service: "web", kind: "ok", bottleneck: false, message: "" }],
      scaling: { verdict: "web: headline", workloads: [{ nodeId: "Deployment/a/web", workload: "web", kind, message: "web: headline", timeline: [], peakReplicas: 2 }] },
    })
    expect(resourcesTone(scaled("late"))).toBe("warning")
    expect(resourcesTone(scaled("scaled"))).toBe("ok")
    expect(resourcesTone(scaled("no_hpa"))).toBe("ok")
    expect(scalingTone("maxed")).toBe("warning")
    expect(scalingTone("not_needed")).toBe("ok")
    expect(scalingTone("too_short")).toBe("info")
  })

  test("usageTone", () => {
    expect(usageTone(50)).toBe("ok")
    expect(usageTone(70)).toBe("warning")
    expect(usageTone(98)).toBe("error")
  })

  test("problemTone", () => {
    const p = (severity: ComposeProblem["severity"]): ComposeProblem => ({
      service: "api",
      nodeId: "service:api",
      container: "c",
      kind: severity === "error" ? "oom" : "restarts",
      severity,
      message: "",
    })
    expect(problemTone([])).toBeUndefined()
    expect(problemTone([p("warning")])).toBe("warning")
    expect(problemTone([p("warning"), p("error")])).toBe("error")
  })
})

describe("resource chart", () => {
  const resources: LoadResources = {
    service: "api",
    verdict: "",
    verdicts: [],
    services: [
      {
        service: "api",
        nodeId: "service:api",
        cpuLimit: 0.5,
        cpuLimited: true,
        memLimit: 0,
        memLimited: false,
        timeline: [
          { t: 0, cpuPercent: 5, memBytes: 1024 * 1024, containers: 1 },
          { t: 1, cpuPercent: 50, memBytes: 2 * 1024 * 1024, containers: 1 },
        ],
        peakCpuPercent: 50,
        peakCpuOfLimit: 1,
        peakMemBytes: 2 * 1024 * 1024,
        peakMemOfLimit: 0,
      },
      {
        service: "db",
        nodeId: "service:db",
        cpuLimit: 2,
        cpuLimited: false,
        memLimit: 0,
        memLimited: false,
        timeline: [{ t: 2, cpuPercent: 20, memBytes: 0, containers: 1 }],
        peakCpuPercent: 20,
        peakCpuOfLimit: 0.1,
        peakMemBytes: 0,
        peakMemOfLimit: 0,
      },
    ],
  }

  test("resourceRows merges timelines by t", () => {
    const { rows, series } = resourceRows(resources)
    expect(series).toEqual([
      { service: "api", cpuKey: "cpu0", memKey: "mem0", target: true },
      { service: "db", cpuKey: "cpu1", memKey: "mem1", target: false },
    ])
    expect(rows).toEqual([
      { t: 0, cpu0: 10, mem0: 1 },
      { t: 1, cpu0: 100, mem0: 2 },
      { t: 2, cpu1: 10, mem1: 0 },
    ])
  })

  test("timeDomain spans samples and resources", () => {
    expect(timeDomain([{ t: 1 }, { t: 5 }], resources)).toEqual([0, 5])
    expect(timeDomain([{ t: 1 }, { t: 5 }])).toEqual([1, 5])
    expect(timeDomain([])).toBeUndefined()
  })
})

describe("scaling", () => {
  test("scalingRows merges replicas, HPA CPU and p95 by second", () => {
    const rows = scalingRows(
      {
        timeline: [
          { t: 0, desired: 2, ready: 2 },
          { t: 1, desired: 2, ready: 2, cpuUtilization: 90 },
          { t: 2, desired: 4, ready: 2, cpuUtilization: 90 },
        ],
      },
      [
        { t: 1, p95: 12 },
        { t: 2, p95: 0 },
        { t: 3, p95: 40 },
      ],
    )
    expect(rows).toEqual([
      { t: 0, desired: 2, ready: 2 },
      { t: 1, desired: 2, ready: 2, cpu: 90, p95: 12 },
      { t: 2, desired: 4, ready: 2, cpu: 90 },
      { t: 3, p95: 40 },
    ])
  })

  test("hpaLabel", () => {
    expect(hpaLabel({ name: "web", min: 2, max: 5, targetCpu: 70 })).toBe("HPA 2–5 pods · 70% CPU")
    expect(hpaLabel({ name: "web", min: 1, max: 3 })).toBe("HPA 1–3 pods")
  })
})
