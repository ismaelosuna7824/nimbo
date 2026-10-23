import { RadarIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkflowStore } from "@/state/workflow-store"
import { MonitoringDialog } from "./monitoring-dialog"
import { hasMonitoring, openMonitoringDialog } from "./monitoring-store"

/** Docker and Kubernetes workspaces: the top bar's Add / Update monitoring button and its dialog. */
export function MonitoringAction() {
  const workflow = useWorkflowStore((s) => s.workflow)
  if (!workflow) return null
  const added = hasMonitoring(workflow)
  return (
    <>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="sm" aria-label={added ? "Update monitoring" : "Add monitoring"} data-testid="add-monitoring" onClick={openMonitoringDialog}>
            <RadarIcon className={added ? "text-emerald-600 dark:text-emerald-400" : undefined} />
            <span className="hidden 2xl:inline">{added ? "Monitoring" : "Add monitoring"}</span>
          </Button>
        </TooltipTrigger>
        <TooltipContent>
          {added
            ? "Monitoring added: open Grafana and Prometheus, or update the alerts and dashboards after changing workloads"
            : "Add monitoring: Prometheus, Grafana, alerts and dashboards for this workflow"}
        </TooltipContent>
      </Tooltip>
      <MonitoringDialog workflow={workflow} />
    </>
  )
}
