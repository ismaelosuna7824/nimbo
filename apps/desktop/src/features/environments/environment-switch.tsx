import { LayersIcon } from "lucide-react"
import { useMemo } from "react"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { switchEnvironment } from "./actions"
import { environmentFiles, environmentNames } from "./files"

const BASE = "__base"

/**
 * Environment of a docker or Kubernetes workspace: files on top of the base (Kustomize overlays, Helm values files,
 * compose overrides, env files). Switching saves `activeEnvironment`; the daemon renders that environment and the
 * canvas, Architecture view and Deploy follow it.
 */
export function EnvironmentSwitch({ className }: { className?: string }) {
  const workflow = useWorkflowStore((s) => s.workflow)
  const active = workflow?.activeEnvironment
  const names = useMemo(() => (workflow ? environmentNames(workflow) : []), [workflow])
  if (!workflow || names.length === 0) return null

  return (
    <Select value={active && names.includes(active) ? active : BASE} onValueChange={(v) => switchEnvironment(v === BASE ? "" : v)}>
      <SelectTrigger
        size="sm"
        className={cn("h-7 min-w-36 gap-1.5 text-xs", className)}
        aria-label="Environment"
        data-testid="environment"
      >
        <LayersIcon className="size-3.5 text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={BASE} data-testid="environment-base" title="Only the base files">
          Base
        </SelectItem>
        {names.map((name) => {
          const paths = environmentFiles(workflow, name)
          return (
            <SelectItem key={name} value={name} data-testid={`environment-${name}`} title={paths.join(", ") || undefined}>
              {name}
              {/* Paths in the list only: the trigger shows the name. */}
              {paths.length > 0 && <span className="font-mono text-[11px] text-muted-foreground in-data-[slot=select-trigger]:hidden">{paths.join(", ")}</span>}
            </SelectItem>
          )
        })}
      </SelectContent>
    </Select>
  )
}
