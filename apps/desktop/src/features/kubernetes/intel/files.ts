// Which Kubernetes file the editor is showing: a manifest, a kustomization,
// a Helm chart's Chart.yaml, values file or template. Charts are the helm
// sources' directories plus any directory holding a Chart.yaml (vendored
// subcharts, charts not deployed yet).

import type { KubernetesModel } from "@/lib/api/types"

export type KubeFileKind = "manifest" | "kustomization" | "chart" | "values" | "template"

export interface KubeFileInfo {
  kind: KubeFileKind
  /** Chart directory of chart, values and template files ("" = the workflow root). */
  chart?: string
}

/** "a/b/c.yaml" → "a/b"; "c.yaml" → "". */
export function dirname(path: string): string {
  const i = path.lastIndexOf("/")
  return i < 0 ? "" : path.slice(0, i)
}

export function basename(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1)
}

/** Joins a relative path to a directory, resolving "." and ".."; null when it leaves the workflow. */
export function joinPath(dir: string, rel: string): string | null {
  const parts = dir ? dir.split("/") : []
  for (const seg of rel.replace(/\\/g, "/").split("/")) {
    if (seg === "" || seg === ".") continue
    if (seg === "..") {
      if (parts.length === 0) return null
      parts.pop()
    } else parts.push(seg)
  }
  return parts.join("/")
}

/** `path` relative to directory `dir` ("../x" when outside it). */
export function relativePath(dir: string, path: string): string {
  const from = dir ? dir.split("/") : []
  const to = path.split("/")
  let i = 0
  while (i < from.length && i < to.length - 1 && from[i] === to[i]) i++
  return [...Array<string>(from.length - i).fill(".."), ...to.slice(i)].join("/")
}

function cleanDir(p: string): string {
  const j = joinPath("", p)
  return j ?? ""
}

const YAML = /\.ya?ml$/i
const KUSTOMIZATION = /^(kustomization\.ya?ml|Kustomization)$/
const VALUES = /^values([.-][\w.-]+)?\.ya?ml$/i

/** Directories holding a chart: helm sources and every directory with a Chart.yaml. */
export function chartDirs(kube: KubernetesModel | undefined, paths: Iterable<string>): string[] {
  const out = new Set<string>()
  for (const s of kube?.sources ?? []) if (s.kind === "helm") out.add(cleanDir(s.path))
  for (const p of paths) if (basename(p) === "Chart.yaml") out.add(dirname(p))
  return [...out]
}

/** The innermost chart directory holding `path`, or undefined. */
export function chartOf(path: string, charts: readonly string[]): string | undefined {
  let best: string | undefined
  for (const c of charts) {
    if (c !== "" && !path.startsWith(`${c}/`)) continue
    if (best === undefined || c.length > best.length) best = c
  }
  return best
}

/** Path of `rel` inside chart directory `chart`. */
export function inChart(chart: string, rel: string): string {
  return chart ? `${chart}/${rel}` : rel
}

/** The chart a values file outside every chart belongs to (a helm source's valuesFiles/environments). */
function sourceChartOfValues(path: string, kube: KubernetesModel | undefined): string | undefined {
  for (const s of kube?.sources ?? []) {
    if (s.kind !== "helm") continue
    const files = [...(s.valuesFiles ?? []), ...Object.values(s.environments ?? {}), ...Object.values(s.envPaths ?? {})]
    if (files.some((f) => cleanDir(f) === path)) return cleanDir(s.path)
  }
  return undefined
}

/** What the editor offers for `path` in a Kubernetes workflow; null for files it doesn't handle. */
export function kubeFileKind(path: string, kube: KubernetesModel | undefined, paths: Iterable<string> = []): KubeFileInfo | null {
  const name = basename(path)
  const known = new Set(paths)
  for (const f of kube?.files ?? []) known.add(f)
  known.add(path)
  const charts = chartDirs(kube, known)
  const chart = chartOf(path, charts)
  if (chart !== undefined) {
    const rel = chart ? path.slice(chart.length + 1) : path
    if (rel === "Chart.yaml") return { kind: "chart", chart }
    if (rel.startsWith("templates/")) return /\.(ya?ml|tpl)$/i.test(rel) ? { kind: "template", chart } : null
    if (!YAML.test(rel)) return null
    if (VALUES.test(name) || sourceChartOfValues(path, kube) === chart) return { kind: "values", chart }
    // ci/*-values.yaml (chart-testing) and other values-like files.
    if (/(^|[-_.])values\.ya?ml$/i.test(name)) return { kind: "values", chart }
    return null
  }
  if (KUSTOMIZATION.test(name)) return { kind: "kustomization" }
  const valuesOf = sourceChartOfValues(path, kube)
  if (valuesOf !== undefined) return { kind: "values", chart: valuesOf }
  if (YAML.test(name)) return { kind: "manifest" }
  return null
}
