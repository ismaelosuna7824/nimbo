import type { MonitoringAlert, MonitoringHTTP, MonitoringHTTPSource, MonitoringSummary } from "@/lib/api/types"

/** How each request-metrics source is shown, with what it needs. */
export const HTTP_SOURCES: Record<MonitoringHTTPSource, { label: string; hint: string; note?: string }> = {
  traefik: {
    label: "Traefik ingress",
    hint: "Requests routed by Traefik (k3s and the local cluster, which also serves class nginx): measured without changing the app.",
  },
  "ingress-nginx": {
    label: "ingress-nginx",
    hint: "Class nginx Ingresses on clusters that run ingress-nginx.",
    note: "ingress-nginx alerts need the controller's metrics scraped: controller.metrics.enabled and controller.metrics.serviceMonitor.enabled in its chart.",
  },
  otel: {
    label: "OpenTelemetry",
    hint: "The app's own HTTP metrics (http.server.request.duration), from its scraped metrics endpoint.",
    note: "OpenTelemetry alerts fire only if the app exports the HTTP semantic-convention metrics (http_server_request_duration_seconds) on its metrics endpoint.",
  },
}

/** How many targets have request metrics, and the requirements of the sources in use. */
export function httpCoverage(http: MonitoringHTTP[]): { measured: number; notes: string[] } {
  const used = new Set(http.flatMap((h) => h.sources))
  const notes = [...used].map((s) => HTTP_SOURCES[s].note).filter((n): n is string => !!n)
  if (http.some((h) => h.sources.length === 0))
    notes.push("Targets without request metrics get no HTTP alert: route them through an Ingress or expose OpenTelemetry HTTP metrics on a scraped port.")
  return { measured: http.filter((h) => h.sources.length > 0).length, notes }
}

/** Binary size with one decimal: 1.5 GiB, 448 MiB. */
export function formatSize(bytes: number): string {
  const gib = bytes / 2 ** 30
  if (gib >= 1) return `${Number.isInteger(gib) ? gib : gib.toFixed(1)} GiB`
  return `${Math.round(bytes / 2 ** 20)} MiB`
}

/** "0.4 CPU · 1.2 GiB memory · 5 GiB disk" (what the stack requests or reserves). */
export function formatCost(cost: MonitoringSummary["cost"]): string {
  const cpu = Math.round(cost.cpuMillis / 10) / 100
  return [`${cpu} CPU`, `${formatSize(cost.memoryBytes)} memory`, cost.diskBytes > 0 ? `${formatSize(cost.diskBytes)} disk` : ""].filter(Boolean).join(" · ")
}

/** Alerts grouped by what they watch (workload or service), in first-seen order. */
export function alertsByTarget(alerts: MonitoringAlert[]): { target: string; alerts: MonitoringAlert[] }[] {
  const groups = new Map<string, MonitoringAlert[]>()
  for (const a of alerts) groups.set(a.target, [...(groups.get(a.target) ?? []), a])
  return [...groups].map(([target, list]) => ({ target, alerts: list }))
}
