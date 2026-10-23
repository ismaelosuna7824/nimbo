import type {
  BuildStep,
  ComposeBuild,
  ComposeProblem,
  HPAPolicy,
  LoadResources,
  LoadSample,
  LoadVerdict,
  ScalingKind,
  ServiceBuildStats,
  ServiceUsage,
  WorkloadScaling,
} from "@/lib/api/types"

const MINUS = "\u2212"

function trimmed(n: number): string {
  return n.toFixed(1).replace(/\.0$/, "")
}

function scaled(n: number, base: number, units: string[]): string {
  const abs = Math.abs(n)
  if (!Number.isFinite(n)) return "—"
  if (abs < base) return `${Math.round(n)} B`
  let v = n
  let i = -1
  while (Math.abs(v) >= base && i < units.length - 1) {
    v /= base
    i++
  }
  return `${Math.abs(v) < 100 ? trimmed(v) : Math.round(v)} ${units[i]}`
}

/** Memory and I/O counters, binary units like `docker stats`: "512 B", "118 MiB", "1.5 GiB". */
export function formatBytes(n: number): string {
  return scaled(n, 1024, ["KiB", "MiB", "GiB", "TiB"])
}

/** Image and layer sizes, decimal units like `docker images`: "12.3 MB", "1.2 GB". */
export function formatSize(n: number): string {
  return scaled(n, 1000, ["kB", "MB", "GB", "TB"])
}

/** "850 ms", "3.2 s", "42 s", "2 m 5 s". */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms)) return "—"
  const abs = Math.abs(ms)
  if (abs < 1000) return `${Math.round(ms)} ms`
  const s = ms / 1000
  if (abs < 60_000) return `${Math.abs(s) < 10 ? trimmed(s) : Math.round(s)} s`
  const total = Math.round(abs / 1000)
  const m = Math.floor(total / 60)
  const rem = total % 60
  return `${ms < 0 ? "-" : ""}${m} m${rem ? ` ${rem} s` : ""}`
}

/** CPU use as % of the service's limit (cores; host CPUs when unlimited): 100 = the limit is saturated. */
export function cpuOfLimit(u: Pick<ServiceUsage, "cpuPercent" | "cpuLimit">): number {
  return u.cpuLimit > 0 ? u.cpuPercent / u.cpuLimit : u.cpuPercent
}

/** Memory use as % of the service's limit (host memory when unlimited); 0 when the limit is unknown. */
export function memOfLimit(u: Pick<ServiceUsage, "memBytes" | "memLimit">): number {
  return u.memLimit > 0 ? (u.memBytes / u.memLimit) * 100 : 0
}

/** "23%", "4.5%", "0%". */
export function formatPct(p: number): string {
  if (!Number.isFinite(p)) return "—"
  return `${p > 0 && p < 10 ? trimmed(p) : Math.round(p)}%`
}

/** "0.25 CPU", "2 CPUs"; unlimited: "no limit · 14 CPUs". */
export function cpuLimitLabel(limit: number, limited: boolean): string {
  const n = Number.isInteger(limit) ? String(limit) : String(Number(limit.toFixed(2)))
  const cpus = `${n} ${limit > 1 ? "CPUs" : "CPU"}`
  return limited ? cpus : `no limit · ${cpus}`
}

/** "512 MiB"; unlimited: "no limit · 7.7 GiB" (host memory). */
export function memLimitLabel(limit: number, limited: boolean): string {
  if (!limited) return limit > 0 ? `no limit · ${formatBytes(limit)}` : "no limit"
  return formatBytes(limit)
}

/** "CPU 23% · 118 MiB" for a card. */
export function usageLine(u: ServiceUsage): string {
  return `CPU ${formatPct(cpuOfLimit(u))} · ${formatBytes(u.memBytes)}`
}

export type DeltaTone = "worse" | "better" | "same"

export const DELTA_CLASS: Record<DeltaTone, string> = {
  worse: "text-destructive",
  better: "text-emerald-600 dark:text-emerald-400",
  same: "text-muted-foreground",
}

/**
 * A change between two builds: "+12.3 MB", "−1.2 s", "+2 cached". Bigger, slower and more cache misses are worse;
 * `higherIsBetter` flips it (cache hits).
 */
export function formatDelta(
  value: number,
  unit: "bytes" | "ms" | "count",
  higherIsBetter = false,
): { text: string; tone: DeltaTone; className: string } {
  const tiny = unit === "bytes" ? Math.abs(value) < 1000 : unit === "ms" ? Math.abs(value) < 50 : value === 0
  if (tiny || !Number.isFinite(value)) return { text: "±0", tone: "same", className: DELTA_CLASS.same }
  const abs = Math.abs(value)
  const body = unit === "bytes" ? formatSize(abs) : unit === "ms" ? formatDuration(abs) : String(abs)
  const tone: DeltaTone = value > 0 !== higherIsBetter ? "worse" : "better"
  return { text: `${value > 0 ? "+" : MINUS}${body}`, tone, className: DELTA_CLASS[tone] }
}

/** How close a usage % of limit is to saturation: ≥ 90 error, ≥ 70 warning. */
export function usageTone(pct: number): "ok" | "warning" | "error" {
  return pct >= 90 ? "error" : pct >= 70 ? "warning" : "ok"
}

export const PROBLEM_LABEL: Record<ComposeProblem["kind"], string> = {
  oom: "Out of memory",
  restarts: "Restarting",
  exit: "Exited",
  unhealthy: "Unhealthy",
}

/** Samples a sparkline of the last ~60 s shows at the given stats interval. */
export function sparklineCount(intervalMs: number | undefined, windowMs = 60_000): number {
  return Math.max(2, Math.round(windowMs / (intervalMs && intervalMs > 0 ? intervalMs : 1000)))
}

/**
 * SVG polyline points of CPU (% of limit, clamped to 0..100) over the last `count` samples, right-aligned so a
 * short history grows from the right edge like a live chart. Empty when there are fewer than 2 samples.
 */
export function sparklinePoints(history: ServiceUsage[], width: number, height: number, count = 60): string {
  const recent = history.slice(-count)
  if (recent.length < 2) return ""
  const step = width / (count - 1)
  const offset = width - (recent.length - 1) * step
  return recent
    .map((u, i) => {
      const v = Math.min(100, Math.max(0, cpuOfLimit(u)))
      const x = offset + i * step
      const y = height - 1 - (v / 100) * (height - 2)
      return `${x.toFixed(1)},${y.toFixed(1)}`
    })
    .join(" ")
}

export interface StepSummary {
  total: number
  cached: number
  executed: number
  /** Executed (uncached) user steps, slowest first. */
  slow: BuildStep[]
}

/**
 * Cached vs executed Dockerfile steps (BuildKit internal steps and FROM
 * excluded, like the daemon's cache counts) and the slowest uncached steps
 * (a FROM's time is the base image pull, so it can be one of them).
 */
export function stepSummary(steps: BuildStep[], top = 3): StepSummary {
  const own = steps.filter((s) => !s.internal)
  const counted = own.filter((s) => !/^FROM\s/i.test(s.instruction))
  const cached = counted.filter((s) => s.cached).length
  const slow = own
    .filter((s) => !s.cached && s.durationMs > 0)
    .sort((a, b) => b.durationMs - a.durationMs)
    .slice(0, top)
  return { total: counted.length, cached, executed: counted.length - cached, slow }
}

/** Size the service's own layers add on top of its base image; undefined when the base size is unknown. */
export function ownSize(s: Pick<ServiceBuildStats, "sizeBytes" | "baseSizeBytes">): number | undefined {
  return s.baseSizeBytes > 0 ? Math.max(0, s.sizeBytes - s.baseSizeBytes) : undefined
}

/** The newest build stats of each service (builds come newest first), in first-seen order. */
export function latestBuilds(builds: ComposeBuild[]): { build: ComposeBuild; stats: ServiceBuildStats }[] {
  const seen = new Set<string>()
  const out: { build: ComposeBuild; stats: ServiceBuildStats }[] = []
  for (const build of builds) {
    for (const stats of build.services) {
      if (seen.has(stats.service)) continue
      seen.add(stats.service)
      out.push({ build, stats })
    }
  }
  return out
}

export type Tone = "ok" | "warning" | "error"

export const TONE_CLASS: Record<Tone, string> = {
  ok: "text-emerald-600 dark:text-emerald-400",
  warning: "text-amber-600 dark:text-amber-400",
  error: "text-destructive",
}

export const TONE_SURFACE: Record<Tone, string> = {
  ok: "border-emerald-500/30 bg-emerald-500/8",
  warning: "border-amber-500/40 bg-amber-500/10",
  error: "border-destructive/40 bg-destructive/10",
}

/** A load-test verdict: crashes (OOM, restarts) are errors, saturated limits are warnings, the rest is fine. */
export function verdictTone(v: Pick<LoadVerdict, "kind" | "bottleneck">): Tone {
  if (v.kind === "oom" || v.kind === "restarted") return "error"
  if (v.bottleneck || v.kind !== "ok") return "warning"
  return "ok"
}

/** Worst tone of every verdict; an autoscaling problem that became the headline (late, maxed, no metrics) warns too. */
export function resourcesTone(r: LoadResources): Tone {
  let tone: Tone = "ok"
  for (const v of r.verdicts) {
    const t = verdictTone(v)
    if (t === "error") return "error"
    if (t === "warning") tone = "warning"
  }
  const s = r.scaling
  if (s && r.verdict === s.verdict && s.workloads.some((w) => w.message === s.verdict && scalingTone(w.kind) === "warning")) {
    tone = "warning"
  }
  return tone
}

export const SCALING_LABEL: Record<ScalingKind, string> = {
  scaled: "Scaled in time",
  late: "Scaled too late",
  maxed: "Hit max replicas",
  no_hpa: "No autoscaler",
  no_metrics: "No metrics",
  not_needed: "No scaling needed",
  too_short: "Run too short",
}

/** Autoscaling verdict tone: problems warn, a missing HPA or a short run is information. */
export function scalingTone(kind: ScalingKind): Tone | "info" {
  switch (kind) {
    case "scaled":
    case "not_needed":
      return "ok"
    case "late":
    case "maxed":
    case "no_metrics":
      return "warning"
    default:
      return "info"
  }
}

/** "HPA 2–5 pods · 70% CPU". */
export function hpaLabel(h: HPAPolicy): string {
  return `HPA ${h.min}–${h.max} pods${h.targetCpu ? ` · ${h.targetCpu}% CPU` : ""}`
}

export interface ScalingRow {
  t: number
  desired?: number
  ready?: number
  /** HPA CPU utilization (% of requests). */
  cpu?: number
  p95?: number
}

/**
 * One row per second with a workload's desired/ready replicas and HPA CPU reading, and the run's p95, for the
 * scaling chart (the replicas and latency share the seconds axis).
 */
export function scalingRows(w: Pick<WorkloadScaling, "timeline">, samples: Pick<LoadSample, "t" | "p95">[]): ScalingRow[] {
  const byT = new Map<number, ScalingRow>()
  const row = (t: number) => {
    let r = byT.get(t)
    if (!r) byT.set(t, (r = { t }))
    return r
  }
  for (const p of w.timeline) {
    const r = row(p.t)
    r.desired = p.desired
    r.ready = p.ready
    if (p.cpuUtilization !== undefined) r.cpu = p.cpuUtilization
  }
  // p95 is 0 in seconds without a successful request: no point rather than a dip to zero.
  for (const s of samples) if (s.p95 > 0) row(s.t).p95 = s.p95
  return [...byT.values()].sort((a, b) => a.t - b.t)
}

/** Worst severity of a service's problems, or undefined without problems. */
export function problemTone(problems: ComposeProblem[]): "error" | "warning" | undefined {
  if (problems.length === 0) return undefined
  return problems.some((p) => p.severity === "error") ? "error" : "warning"
}

export interface ResourceSeries {
  service: string
  /** Chart keys: cpu<i> (% of limit) and mem<i> (MiB). */
  cpuKey: string
  memKey: string
  target: boolean
}

const MIB = 1024 * 1024

/** One row per timeline second with every service's CPU (% of limit) and memory (MiB), for a recharts chart. */
export function resourceRows(r: LoadResources): { rows: Record<string, number>[]; series: ResourceSeries[] } {
  const byT = new Map<number, Record<string, number>>()
  const series = r.services.map((s, i) => {
    const cpuKey = `cpu${i}`
    const memKey = `mem${i}`
    for (const p of s.timeline) {
      let row = byT.get(p.t)
      if (!row) byT.set(p.t, (row = { t: p.t }))
      // One decimal: what the chart tooltip shows.
      row[cpuKey] = Math.round(cpuOfLimit({ cpuPercent: p.cpuPercent, cpuLimit: s.cpuLimit }) * 10) / 10
      row[memKey] = Math.round((p.memBytes / MIB) * 10) / 10
    }
    return { service: s.service, cpuKey, memKey, target: !!s.target || s.service === r.service }
  })
  const rows = [...byT.values()].sort((a, b) => a.t - b.t)
  return { rows, series }
}

/** Shared x-axis domain (seconds) of the load-test charts and the resource chart. */
export function timeDomain(samples: Pick<LoadSample, "t">[], resources?: LoadResources): [number, number] | undefined {
  let min = Infinity
  let max = -Infinity
  for (const s of samples) {
    min = Math.min(min, s.t)
    max = Math.max(max, s.t)
  }
  for (const svc of resources?.services ?? []) {
    for (const p of svc.timeline) {
      min = Math.min(min, p.t)
      max = Math.max(max, p.t)
    }
  }
  return min <= max ? [min, max] : undefined
}
