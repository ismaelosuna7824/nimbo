import { toast } from "sonner"
import { openMonitoringDialog } from "@/features/monitoring/monitoring-store"
import { revealLoadTest } from "@/features/panels/loadtest/reveal"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { Diagnostic, ReadinessItem } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { envLabel } from "./status"

/** Opens the file:line of a finding in the Code tab (and selects its object when the canvas shows it). */
export function openFinding(d: Diagnostic) {
  const s = useWorkflowStore.getState()
  if (d.nodeId && s.workflow?.kubernetes?.graph.nodes.some((n) => n.id === d.nodeId)) s.select(d.nodeId)
  if (d.file) revealFiles("", d.file, d.line)
}

/**
 * Runs an item's action for environment `env`. A load test always hits the deployed (active) environment, so testing
 * another one first switches to it and opens Deploy.
 */
export async function runReadinessAction(item: ReadinessItem, env: string) {
  const action = item.action
  if (!action) return
  const s = useWorkflowStore.getState()
  switch (action.kind) {
    case "open_problem": {
      const d = item.diagnostics?.find((x) => x.file) ?? item.diagnostics?.[0]
      if (d) openFinding(d)
      return
    }
    case "open_load_test":
      revealLoadTest(action.runId)
      return
    case "add_monitoring":
      openMonitoringDialog()
      return
    case "run_load_test": {
      const active = s.workflow?.activeEnvironment ?? ""
      if (active === env) {
        revealLoadTest()
        return
      }
      s.update((wf) => ({ ...wf, activeEnvironment: env || undefined }))
      await s.flush()
      s.showBottom("deploy")
      toast.info(`Switched to ${envLabel(env)}`, { description: "Deploy it, then run the load test from the Load test tab." })
    }
  }
}
