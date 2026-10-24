// Pure logic of service form fields that write several compose keys at once: resource limits, logging, and the
// "only in this environment" scope.

import type { ComposeLogging, ComposeResources, ServiceFacts, ServicePatch } from "@/lib/api/types"

/** Patch of one limit field (cpus, memory, pids), keeping the others; null (no limits) once none is left. */
export function resourcesPatch(cur: ComposeResources | undefined, change: Partial<ComposeResources>): ServicePatch {
  const next = { ...cur, ...change }
  const out: ComposeResources = {}
  if (next.cpus) out.cpus = next.cpus
  if (next.memory) out.memory = next.memory
  if (next.pids) out.pids = next.pids
  return { resources: Object.keys(out).length ? out : null }
}

/** Logging drivers the form offers ("" = Docker's default, json-file). */
export const LOG_DRIVERS = [
  { value: "", label: "Default (json-file)" },
  { value: "json-file", label: "json-file" },
  { value: "local", label: "local" },
  { value: "none", label: "none (no logs)" },
]

/** Drivers that keep log files on disk, rotated with max-size and max-file. */
export function rotatesFiles(driver?: string): boolean {
  return !driver || driver === "json-file" || driver === "local"
}

/** A json-file/local max-size: a number with an optional k, m or g unit ("10m"). */
export const LOG_SIZE_RE = /^\d+[kmg]?$/i
/** A max-file count ("3"). */
export const LOG_FILES_RE = /^[1-9]\d*$/

/** Patch of one logging field, keeping the others; null removes `logging:` once nothing is left. */
export function loggingPatch(cur: ComposeLogging | undefined, change: Partial<ComposeLogging>): ServicePatch {
  const next = { ...cur, ...change }
  const out: ComposeLogging = {}
  if (next.driver) out.driver = next.driver
  if (rotatesFiles(next.driver)) {
    if (next.maxSize) out.maxSize = next.maxSize
    if (next.maxFile) out.maxFile = next.maxFile
  }
  return { logging: Object.keys(out).length ? out : null }
}

/** The rotation the form suggests: json-file with 10 MB files, 3 of them. */
export const ROTATION: ComposeLogging = { driver: "json-file", maxSize: "10m", maxFile: "3" }

/** A label row of the Labels list. */
export interface LabelRow {
  key: string
  value: string
}

/** Patch of the Labels list: rows without a key are drafts, not written. */
export function labelsPatch(rows: LabelRow[]): ServicePatch {
  const labels = Object.fromEntries(rows.filter((r) => r.key).map((r) => [r.key, r.value]))
  return { labels: Object.keys(labels).length ? labels : null }
}

/**
 * Labels that tell a Prometheus scraper (Nimbo's Add monitoring) where the service's metrics are: its first container
 * port (published or exposed), path /metrics. Other labels stay.
 */
export function scrapeLabels(svc: Pick<ServiceFacts, "labels" | "ports" | "expose">): Record<string, string> {
  const port = svc.ports[0]?.container ?? svc.expose[0] ?? "80"
  return { ...svc.labels, "prometheus.io/scrape": "true", "prometheus.io/port": port.split("-")[0], "prometheus.io/path": "/metrics" }
}

/** Sends a patch to the active environment's override file only when `scoped`. */
export function withScope(patch: ServicePatch, scoped: boolean): ServicePatch {
  return scoped ? { ...patch, scope: "environment" } : patch
}
