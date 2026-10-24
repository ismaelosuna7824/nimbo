import { toast } from "sonner"
import { openMonitoringDialog } from "@/features/monitoring/monitoring-store"
import { revealCapacity, revealLoadTest, revealScenario } from "@/features/panels/loadtest/reveal"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { Diagnostic, ReadinessAction } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { envLabel } from "./status"

/** Opens the file:line of a finding in the Code tab (and selects its node when the canvas shows it). */
export function openFinding(d: Diagnostic) {
  const s = useWorkflowStore.getState()
  const nodes = s.workflow?.kubernetes?.graph.nodes ?? s.workflow?.compose?.graph.nodes ?? []
  if (d.nodeId && nodes.some((n) => n.id === d.nodeId)) s.select(d.nodeId)
  if (d.file) revealFiles("", d.file, d.line)
}

/**
 * Runs a readiness action for environment `env`. Load tests and incident scenarios always hit the deployed (active)
 * environment, so verifying another one first switches to it and opens Deploy.
 */
export async function runReadinessAction(action: ReadinessAction | undefined, diagnostics: Diagnostic[] | undefined, env: string) {
  if (!action) return
  const s = useWorkflowStore.getState()
  switch (action.kind) {
    case "open_problem": {
      const d = diagnostics?.find((x) => x.file) ?? diagnostics?.[0]
      if (d) openFinding(d)
      return
    }
    case "open_load_test":
      revealLoadTest(action.runId)
      return
    case "add_monitoring":
      openMonitoringDialog()
      return
    case "plan_capacity":
      revealCapacity(action.runId)
      return
    case "run_load_test":
    case "run_scenario": {
      const active = s.workflow?.activeEnvironment ?? ""
      if (active === env) {
        if (action.kind === "run_scenario" && action.scenario) revealScenario(action.scenario, action.target)
        else revealLoadTest()
        return
      }
      s.update((wf) => ({ ...wf, activeEnvironment: env || undefined }))
      await s.flush()
      s.showBottom("deploy")
      toast.info(`Switched to ${envLabel(env)}`, { description: "Deploy it, then run the test from the Load test tab." })
    }
  }
}
