/** A Kubernetes name: a DNS-1123 label starting with a letter (what the converter accepts). */
export const DNS_LABEL = /^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$/

/** A workflow name as a DNS label: the server's default application name, shown as the placeholder. */
export function suggestKubeName(name: string): string {
  const s = name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 63)
    .replace(/-+$/, "")
  if (!s) return "app"
  return /^[0-9]/.test(s) ? `s-${s}`.slice(0, 63).replace(/-+$/, "") : s
}

/**
 * What a format writes. Without docker environments: the fixed dev + prod pair. With them: one overlay / values file
 * per environment (its override's differences), production-like ones hardened.
 */
export function formatDescription(format: "manifests" | "kustomize" | "helm", environments: string[]): string {
  if (environments.length === 0) {
    if (format === "manifests") return "Plain YAML in k8s/: one file per service, applied as is (no prod environment)."
    if (format === "kustomize") return "k8s/base plus overlays/dev and overlays/prod (2+ replicas, PodDisruptionBudgets, autoscalers)."
    return "chart/ with values.yaml, values-dev.yaml and values-prod.yaml."
  }
  const list = (paths: string[]) => (paths.length === 1 ? paths[0] : `${paths.slice(0, -1).join(", ")} and ${paths.at(-1)}`)
  if (format === "manifests") return `Plain YAML in k8s/ from the base compose files: the ${list(environments)} environments are left out.`
  const hardened = "production-like ones (prod, staging…) get 2+ replicas, PodDisruptionBudgets and autoscalers"
  if (format === "kustomize") return `k8s/base plus one overlay per environment: ${list(environments.map((e) => `overlays/${e}`))}, each with its override's differences; ${hardened}.`
  return `chart/ with values.yaml plus one values file per environment: ${list(environments.map((e) => `values-${e}.yaml`))}; ${hardened}.`
}

/** HPA bounds the converter accepts. */
export const MAX_REPLICAS = { min: 2, max: 100 }
export const TARGET_CPU = { min: 10, max: 95 }

/** Why the autoscaling inputs can't be used ("" when they can). */
export function autoscalingError(maxReplicas: number, targetCpu: number): string {
  if (!Number.isInteger(maxReplicas) || maxReplicas < MAX_REPLICAS.min || maxReplicas > MAX_REPLICAS.max)
    return `Max replicas: ${MAX_REPLICAS.min}–${MAX_REPLICAS.max}.`
  if (!Number.isInteger(targetCpu) || targetCpu < TARGET_CPU.min || targetCpu > TARGET_CPU.max)
    return `Target CPU: ${TARGET_CPU.min}–${TARGET_CPU.max}%.`
  return ""
}

/** "12 Oct 14:02 · 180 rps peak · 45 s" for a load test run. */
export function describeLoadTest(run: { startedAt: string; peakRps: number; seconds: number }): string {
  const at = new Date(run.startedAt)
  const when = `${at.getDate()} ${at.toLocaleString("en", { month: "short" })} ${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")}`
  const parts = [when, `${Math.round(run.peakRps)} rps peak`]
  if (run.seconds > 0) parts.push(`${Math.round(run.seconds)} s`)
  return parts.join(" · ")
}

/** One short line under a service in the sizing table: what the load test measured, else where the numbers come from. */
export function sizingNote(s: { detail: string; measured?: { cpuP90: number; cpuPeak: number; memPeak: number }; censored?: string }): string {
  if (!s.measured) return s.detail
  const { cpuP90, cpuPeak, memPeak } = s.measured
  const note = `measured p90 ${Number(cpuP90.toFixed(2))} · peak ${Number(cpuPeak.toFixed(2))} CPU · ${Math.ceil(memPeak / 2 ** 20)}Mi`
  return s.censored ? `${note} · hit its Docker limit` : note
}
