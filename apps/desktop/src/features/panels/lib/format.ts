export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—"
  if (ms < 1000) return `${Math.round(ms)}ms`
  const s = ms / 1000
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)}s`
  const m = Math.floor(s / 60)
  const rem = Math.round(s % 60)
  if (m < 60) return rem ? `${m}m ${rem}s` : `${m}m`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}

export function durationBetween(start?: string, end?: string, now = Date.now()): number | undefined {
  if (!start) return undefined
  const a = Date.parse(start)
  const b = end ? Date.parse(end) : now
  return Number.isNaN(a) || Number.isNaN(b) ? undefined : b - a
}

const rtf = new Intl.RelativeTimeFormat("en", { numeric: "auto" })

export function formatRelative(iso: string): string {
  const t = Date.parse(iso)
  if (Number.isNaN(t)) return ""
  const diff = (t - Date.now()) / 1000
  const abs = Math.abs(diff)
  if (abs < 45) return "just now"
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute")
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour")
  return rtf.format(Math.round(diff / 86400), "day")
}

export function formatClock(iso: string): string {
  const d = new Date(iso)
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleTimeString("en", { hour12: false })
}

export function formatMs(ms: number | undefined): string {
  if (ms === undefined || !Number.isFinite(ms)) return "—"
  if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`
  return `${ms < 10 ? ms.toFixed(1) : Math.round(ms)}ms`
}

export function formatNumber(n: number | undefined, digits = 0): string {
  if (n === undefined || !Number.isFinite(n)) return "—"
  return n.toLocaleString("en", { maximumFractionDigits: digits, minimumFractionDigits: 0 })
}

export function formatPercent(ratio: number | undefined, digits = 2): string {
  if (ratio === undefined || !Number.isFinite(ratio)) return "—"
  return `${(ratio * 100).toFixed(digits)}%`
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

/** Renders any JSON value as compact single-line text. */
export function displayJSON(v: unknown): string {
  if (v === null || v === undefined) return "null"
  if (typeof v === "string") return v
  return JSON.stringify(v)
}

export async function copyText(text: string): Promise<void> {
  await navigator.clipboard.writeText(text)
}
