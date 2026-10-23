import { LayersIcon } from "lucide-react"
import { useMemo } from "react"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"

const BASE = "__base"

/**
 * Environment of a Kubernetes workspace: Kustomize overlays (overlays/<env>) and Helm values files
 * (values-<env>.yaml) the daemon found. Switching saves `activeEnvironment`; the daemon renders that
 * environment and the canvas, Architecture view and Deploy follow it.
 */
export function KubeEnvironmentSwitch({ className }: { className?: string }) {
  const environments = useWorkflowStore((s) => s.workflow?.environments)
  const active = useWorkflowStore((s) => s.workflow?.activeEnvironment)
  const sources = useWorkflowStore((s) => s.workflow?.kubernetes?.sources)
  const names = useMemo(() => (environments ?? []).map((e) => e.name), [environments])
  if (names.length === 0) return null

  const choose = (v: string) => {
    const next = v === BASE ? undefined : v
    useWorkflowStore.getState().update((wf) => ({ ...wf, activeEnvironment: next }))
    void useWorkflowStore.getState().flush()
  }

  return (
    <Select value={active && names.includes(active) ? active : BASE} onValueChange={choose}>
      <SelectTrigger
        size="sm"
        className={cn("h-7 min-w-36 gap-1.5 text-xs", className)}
        aria-label="Environment"
        data-testid="kube-environment"
      >
        <LayersIcon className="size-3.5 text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value={BASE} data-testid="kube-environment-base" title="No overlay or values file">
          Base
        </SelectItem>
        {names.map((name) => {
          // What the environment renders: "overlays/dev" for Kustomize, "values-dev.yaml" for Helm, per source.
          const paths = (sources ?? []).flatMap((s) => (s.envPaths?.[name] ? [s.envPaths[name]] : []))
          return (
            <SelectItem key={name} value={name} data-testid={`kube-environment-${name}`} title={paths.join(", ") || undefined}>
              {name}
              {paths.length > 0 && <span className="font-mono text-[11px] text-muted-foreground">{paths.join(", ")}</span>}
            </SelectItem>
          )
        })}
      </SelectContent>
    </Select>
  )
}
