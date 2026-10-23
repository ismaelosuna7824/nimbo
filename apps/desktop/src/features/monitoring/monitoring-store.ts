import { create } from "zustand"
import type { Workflow } from "@/lib/api/types"

/** The "Add monitoring" dialog of docker and Kubernetes workspaces (top bar, command palette, readiness report). */
interface MonitoringDialogState {
  open: boolean
  setOpen: (open: boolean) => void
}

export const useMonitoringDialog = create<MonitoringDialogState>((set) => ({
  open: false,
  setOpen: (open) => set({ open }),
}))

/** Opens the Add monitoring dialog for the open workflow. */
export function openMonitoringDialog() {
  useMonitoringDialog.getState().setOpen(true)
}

const PROM_STACK = "kube-prometheus-stack"

/**
 * Whether Add monitoring already ran on the workflow: docker lists monitoring/compose.yaml among its compose files;
 * Kubernetes has the kube-prometheus-stack helm source and the generated monitoring/ manifests source.
 */
export function hasMonitoring(wf: Workflow | null | undefined): boolean {
  if (wf?.kind === "docker") return (wf.compose?.files ?? []).some((f) => f === "monitoring/compose.yaml" || f.endsWith("/monitoring/compose.yaml"))
  if (wf?.kind !== "kubernetes") return false
  const sources = wf.kubernetes?.sources ?? []
  const chart = sources.some(
    (s) => s.kind === "helm" && (s.chart?.name === PROM_STACK || (s.chart?.dependencies ?? []).some((d) => d.name === PROM_STACK)),
  )
  const generated = sources.some((s) => s.kind === "manifests" && s.path.replace(/\/+$/, "") === "monitoring")
  return chart && generated
}
