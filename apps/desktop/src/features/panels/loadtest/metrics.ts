import type { LoadMode, LoadSample, LoadStage, LoadSummary, LoadTestRun, Threshold } from "@/lib/api/types"
import { formatPercent } from "../lib/format"

export const METRIC_META: Record<Threshold["metric"], { label: string; short: string; unit: "ms" | "ratio" | "rps" }> = {
  p50_ms: { label: "Median response time (p50)", short: "p50", unit: "ms" },
  p90_ms: { label: "p90 response time", short: "p90", unit: "ms" },
  p95_ms: { label: "p95 response time", short: "p95", unit: "ms" },
  p99_ms: { label: "p99 response time", short: "p99", unit: "ms" },
  max_ms: { label: "Slowest response", short: "max", unit: "ms" },
  error_rate: { label: "Error rate", short: "errors", unit: "ratio" },
  rps_min: { label: "Average throughput", short: "req/s", unit: "rps" },
}

export const DEFAULT_THRESHOLDS: Threshold[] = [
  { metric: "p95_ms", op: "<", value: 500 },
  { metric: "error_rate", op: "<", value: 0.01 },
]

export const EXPLAIN = {
  speed:
    "Speed: latency percentiles. p95 = 95% of requests were faster than this. Lower is better; p99 shows the slowest outliers.",
  rps: "Throughput: requests per second actually completed. Compare it with the target to see if the system keeps up.",
  errors: "Share of requests that failed (network errors, timeouts or 5xx/4xx responses).",
  stability:
    "Stability: how much the p95 latency varies from second to second (coefficient of variation). Lower means steadier performance.",
  capacity:
    "Capacity: the traffic level where the system started breaking — error rate above 5% or p95 more than 4× its initial value.",
  requests: "Total requests sent so far.",
}

export function modeUnit(mode: LoadMode): string {
  return mode === "rps" ? "req/s" : "users"
}

/** Points (seconds, target) of a stage list; stages ramp linearly from the previous target. */
export function stagePoints(stages: LoadStage[]): [number, number][] {
  const pts: [number, number][] = [[0, 0]]
  let t = 0
  for (const s of stages) {
    t += s.durationSec
    pts.push([t, s.target])
  }
  return pts
}

export function totalDuration(stages: LoadStage[]): number {
  return stages.reduce((sum, s) => sum + s.durationSec, 0)
}

/** Samples from the persisted timeline merged with live SSE samples (by second). */
export function mergeSamples(timeline: LoadSample[], live: LoadSample[]): LoadSample[] {
  if (live.length === 0) return timeline
  const byT = new Map<number, LoadSample>()
  for (const s of timeline) byT.set(s.t, s)
  for (const s of live) byT.set(s.t, s)
  return [...byT.values()].sort((a, b) => a.t - b.t)
}

export interface Kpis {
  requests: number
  errors: number
  errorRate: number
  currentRps: number
  avgRps: number
  peakRps: number
  p50: number
  p95: number
  p99: number
  stability?: number
  breakingPoint?: number
}

function coefficientOfVariation(values: number[]): number | undefined {
  if (values.length < 2) return undefined
  const mean = values.reduce((a, b) => a + b, 0) / values.length
  if (mean === 0) return undefined
  const variance = values.reduce((a, v) => a + (v - mean) ** 2, 0) / values.length
  return Math.sqrt(variance) / mean
}

/**
 * KPIs of a run: from the final summary when available, otherwise estimated
 * from per-second samples with the same definitions the engine uses.
 */
export function computeKpis(samples: LoadSample[], summary?: LoadSummary): Kpis {
  const last = samples[samples.length - 1]
  const requests = samples.reduce((a, s) => a + s.rps, 0)
  const errors = samples.reduce((a, s) => a + s.errors, 0)
  const active = samples.filter((s) => s.rps > 0)
  const firstP95 = active.find((s) => s.p95 > 0)?.p95
  const breaking = firstP95
    ? active.find((s) => s.errors / s.rps > 0.05 || s.p95 > 4 * firstP95)?.target
    : undefined
  const estimated: Kpis = {
    requests: Math.round(requests),
    errors,
    errorRate: requests > 0 ? errors / requests : 0,
    currentRps: last?.rps ?? 0,
    avgRps: samples.length ? requests / samples.length : 0,
    peakRps: samples.reduce((m, s) => Math.max(m, s.rps), 0),
    p50: last?.p50 ?? 0,
    p95: last?.p95 ?? 0,
    p99: last?.p99 ?? 0,
    stability: coefficientOfVariation(active.map((s) => s.p95).filter((v) => v > 0)),
    breakingPoint: breaking,
  }
  if (!summary) return estimated
  return {
    ...estimated,
    requests: summary.requests,
    errors: summary.errors,
    errorRate: summary.errorRate,
    avgRps: summary.rps,
    peakRps: summary.peakRps,
    p50: summary.latency.p50,
    p95: summary.latency.p95,
    p99: summary.latency.p99,
    stability: summary.stability,
    breakingPoint: summary.breakingPoint,
  }
}

export function stabilityLabel(cv: number | undefined): { label: string; tone: "ok" | "warn" | "bad" | "idle" } {
  if (cv === undefined) return { label: "—", tone: "idle" }
  if (cv < 0.15) return { label: "Very stable", tone: "ok" }
  if (cv < 0.35) return { label: "Stable", tone: "ok" }
  if (cv < 0.7) return { label: "Variable", tone: "warn" }
  return { label: "Unstable", tone: "bad" }
}

export function runDurationSec(run: LoadTestRun): number {
  const end = run.endedAt ? Date.parse(run.endedAt) : Date.now()
  return Math.max(0, (end - Date.parse(run.startedAt)) / 1000)
}

export const SERIES_COLORS = {
  target: { light: "oklch(0.65 0 0)", dark: "oklch(0.6 0 0)" },
  rps: { light: "oklch(0.55 0.2 255)", dark: "oklch(0.7 0.16 250)" },
  p50: { light: "oklch(0.6 0.15 155)", dark: "oklch(0.72 0.15 155)" },
  p95: { light: "oklch(0.68 0.17 65)", dark: "oklch(0.78 0.15 70)" },
  p99: { light: "oklch(0.58 0.22 25)", dark: "oklch(0.68 0.2 25)" },
  errors: { light: "oklch(0.58 0.22 25)", dark: "oklch(0.66 0.2 25)" },
  a: { light: "oklch(0.55 0.2 255)", dark: "oklch(0.7 0.16 250)" },
  b: { light: "oklch(0.62 0.2 320)", dark: "oklch(0.72 0.18 320)" },
}

export function formatThreshold(t: Threshold): string {
  const meta = METRIC_META[t.metric]
  const v = meta.unit === "ratio" ? formatPercent(t.value) : meta.unit === "ms" ? `${t.value} ms` : `${t.value} req/s`
  return `${meta.short} ${t.op} ${v}`
}

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 })

/** Short axis label: 1900 → "1.9K". */
export function compactTick(v: number): string {
  return compact.format(v)
}
