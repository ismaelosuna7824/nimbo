// Which vendored charts are expanded on the Build canvas, per workflow. Charts start collapsed (one release node);
// the expanded ones are remembered in localStorage by KubeChartGroup id, which names the workflow's source and the
// vendored subchart ("chart:charts/monitoring/charts/kube-prometheus-stack").
import { useQuery } from "@tanstack/react-query"
import { create } from "zustand"
import { techIcon, techIconFor, type TechIconInfo } from "@/components/tech-icons"
import { api } from "@/lib/api/client"
import type { KubeCatalogItem } from "@/lib/api/types"

const storageKey = (workflowId: string) => `nimbo.k8s.expandedCharts.${workflowId}`
const NONE: readonly string[] = []

/** What localStorage held for a workflow, read once (stable references for selectors). */
const stored = new Map<string, readonly string[]>()

function load(workflowId: string): readonly string[] {
  let ids = stored.get(workflowId)
  if (ids) return ids
  try {
    const raw = JSON.parse(localStorage.getItem(storageKey(workflowId)) ?? "[]") as unknown
    ids = Array.isArray(raw) ? raw.filter((v): v is string => typeof v === "string") : NONE
  } catch {
    ids = NONE
  }
  stored.set(workflowId, ids)
  return ids
}

/** Expanded chart ids per workflow id changed in this session (else localStorage's). */
const useChartUi = create<Record<string, readonly string[]>>(() => ({}))

/** The expanded chart ids of a workflow. */
export function expandedCharts(workflowId: string): readonly string[] {
  return useChartUi.getState()[workflowId] ?? load(workflowId)
}

/** The expanded chart ids of a workflow, re-rendering when they change. */
export function useExpandedCharts(workflowId: string): readonly string[] {
  return useChartUi((s) => s[workflowId]) ?? load(workflowId)
}

/** Expands or collapses a chart of a workflow and remembers it. */
export function setChartExpanded(workflowId: string, chartId: string, open: boolean) {
  const current = expandedCharts(workflowId)
  if (current.includes(chartId) === open) return
  const next = open ? [...current, chartId] : current.filter((id) => id !== chartId)
  try {
    if (next.length) localStorage.setItem(storageKey(workflowId), JSON.stringify(next))
    else localStorage.removeItem(storageKey(workflowId))
  } catch {
    // Storage unavailable: the choice lasts for this session only.
  }
  useChartUi.setState({ [workflowId]: next })
}

/** Icon of a vendored chart: its palette entry's, else the technology its name says, else Helm. */
export function chartIcon(chart: string, catalog: readonly KubeCatalogItem[] | undefined): TechIconInfo {
  const entry = catalog?.find((i) => i.chart?.name === chart)
  if (entry) return techIcon(entry.icon)
  const tech = techIconFor({ image: chart })
  return tech.generic ? techIcon("helm") : tech
}

/** The Kubernetes palette (cached for the session): what chart icons come from. */
export function useKubeCatalog() {
  return useQuery({ queryKey: ["kubernetes-catalog"], queryFn: api.kubernetesCatalog, staleTime: Infinity }).data
}

export function useChartIcon(chart: string): TechIconInfo {
  return chartIcon(chart, useKubeCatalog())
}