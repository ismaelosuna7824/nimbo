import type {
  ComposeLogLine,
  ComposePort,
  ComposeServiceStatus,
  ComposeState,
  ComposeStatus,
  LogLine,
  PortRemap,
} from "@/lib/api/types"

/** Label and status dot of a compose project or service state. */
export const COMPOSE_STATE: Record<ComposeState, { label: string; dot: string }> = {
  absent: { label: "Not created", dot: "bg-muted-foreground/50" },
  starting: { label: "Starting", dot: "bg-amber-500" },
  running: { label: "Running", dot: "bg-emerald-500" },
  stopped: { label: "Stopped", dot: "bg-muted-foreground/50" },
  error: { label: "Error", dot: "bg-destructive" },
}

export interface ChipSummary {
  state: ComposeState
  label: string
  /** Services with at least one running container. */
  running: number
  /** Every service of the compose files (absent ones too). */
  total: number
}

/** What the toolbar chip of a Docker workspace says: "Containers off", "Starting…", "Running 2/3"… */
export function composeChipSummary(status: ComposeStatus | null): ChipSummary {
  const total = status?.services.length ?? 0
  const running = status ? runningServices(status) : 0
  const state = status?.state ?? "absent"
  const label =
    state === "absent"
      ? "Containers off"
      : state === "starting"
        ? "Starting…"
        : state === "running"
          ? `Running ${running}/${total}`
          : state === "stopped"
            ? "Stopped"
            : "Error"
  return { state, label, running, total }
}

/** Services with at least one running container: what plays the role of "deployed" in a Docker workspace. */
export function runningServices(status: ComposeStatus | null | undefined): number {
  return status?.services.filter((s) => s.containers.some((c) => c.state === "running")).length ?? 0
}

/**
 * Changes whenever what is reachable changes (project state, a service's state or its published ports), not on every
 * status event: endpoints and load targets are refetched on it.
 */
export function composeKey(status: ComposeStatus | null | undefined): string {
  if (!status) return ""
  const services = status.services.map(
    (s) => `${s.name}:${s.state}:${servicePorts(s).map((p) => `${p.host}/${p.protocol}`).join("+")}`,
  )
  return `${status.state}|${services.join(",")}`
}

/** Published ports of a service, one per host port (Docker lists 0.0.0.0 and :: bindings separately). */
export function servicePorts(s: ComposeServiceStatus): ComposePort[] {
  const byKey = new Map<string, ComposePort>()
  for (const c of s.containers) {
    for (const p of c.ports) {
      if (!p.host) continue
      const key = `${p.host}/${p.protocol}`
      const seen = byKey.get(key)
      // Prefer the binding the daemon gave a URL (reachable from this machine).
      if (!seen || (!seen.url && p.url)) byKey.set(key, p)
    }
  }
  return [...byKey.values()].sort((a, b) => a.host - b.host)
}

/** First published address reachable from this machine (placeholder of custom HTTP targets). */
export function firstPublishedUrl(status: ComposeStatus | null | undefined): string | undefined {
  for (const s of status?.services ?? []) {
    const url = servicePorts(s).find((p) => p.url)?.url
    if (url) return url
  }
  return undefined
}

/** Running and total containers (replicas) of a service. */
export function serviceReplicas(s: ComposeServiceStatus): { running: number; total: number } {
  return { running: s.containers.filter((c) => c.state === "running").length, total: s.containers.length }
}

export function serviceRestarts(s: ComposeServiceStatus): number {
  return s.containers.reduce((n, c) => n + c.restarts, 0)
}

/** "db: 5432 was busy → published on 55432". */
export function remapNote(r: PortRemap): string {
  const proto = r.protocol === "udp" ? "/udp" : ""
  return `${r.service}: ${r.hostPort}${proto} was busy → published on ${r.port}${proto}`
}

// ---------------------------------------------------------------------------
// Container logs

/** Text colours of service prefixes, assigned in service order so neighbours differ. */
export const SERVICE_COLORS = [
  "text-sky-600 dark:text-sky-400",
  "text-violet-600 dark:text-violet-400",
  "text-emerald-600 dark:text-emerald-400",
  "text-amber-600 dark:text-amber-400",
  "text-pink-600 dark:text-pink-400",
  "text-teal-600 dark:text-teal-400",
  "text-orange-600 dark:text-orange-400",
  "text-indigo-600 dark:text-indigo-400",
  "text-lime-700 dark:text-lime-400",
  "text-cyan-600 dark:text-cyan-400",
] as const

/**
 * Colour of a service's log prefix: by its position among the project's services (stable while the compose file
 * keeps them), or by a hash of its name for services the status does not list (yet).
 */
export function serviceColor(service: string, services: readonly string[]): string {
  const i = services.indexOf(service)
  if (i >= 0) return SERVICE_COLORS[i % SERVICE_COLORS.length]
  let h = 0
  for (const ch of service) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return SERVICE_COLORS[h % SERVICE_COLORS.length]
}

// eslint-disable-next-line no-control-regex
const ANSI = /\x1b\[[0-9;]*[A-Za-z]/g

export interface ComposeLogRow {
  /** Monotonic id (React key; rows are only appended or cleared). */
  id: number
  /** "web", or "web-2" for the second replica. */
  source: string
  service: string
  t: string
  stream: LogLine["stream"]
  text: string
}

/** Prefix of a log line: the service, plus the replica number when it is not the first container. */
export function logSource(l: ComposeLogLine): string {
  const replica = /-(\d+)$/.exec(l.container)?.[1]
  return replica && replica !== "1" ? `${l.service}-${replica}` : l.service
}

/** Most lines kept in the viewer; older ones are dropped (the full log stays in Docker). */
export const MAX_LOG_ROWS = 5000

/** Appends streamed lines (ANSI colours stripped) and drops the oldest beyond max. */
export function appendLogRows(
  prev: ComposeLogRow[],
  lines: readonly ComposeLogLine[],
  max = MAX_LOG_ROWS,
): ComposeLogRow[] {
  if (lines.length === 0) return prev
  let id = prev.length ? prev[prev.length - 1].id + 1 : 0
  const added = lines.map((l) => ({
    id: id++,
    source: logSource(l),
    service: l.service,
    t: l.line.t,
    stream: l.line.stream,
    text: l.line.text.replace(ANSI, ""),
  }))
  const out = prev.concat(added)
  return out.length > max ? out.slice(out.length - max) : out
}

/** Plain-text copy of log rows, like `docker compose logs` prints them. */
export function logRowsText(rows: readonly ComposeLogRow[]): string {
  const width = rows.reduce((w, r) => Math.max(w, r.source.length), 0)
  return rows.map((r) => `${r.source.padEnd(width)} | ${r.text}`).join("\n")
}
