import { CircleCheck, Info, Layers, TriangleAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { LoadScaling } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { hpaLabel, SCALING_LABEL, scalingTone, TONE_CLASS } from "@/features/docker/perf/model"

const ICON = { ok: CircleCheck, warning: TriangleAlert, error: TriangleAlert, info: Info }
const TEXT = { ...TONE_CLASS, info: "text-sky-600 dark:text-sky-400" }
const BADGE = {
  ok: "border-emerald-500/30 text-emerald-700 dark:text-emerald-400",
  warning: "border-amber-500/40 text-amber-700 dark:text-amber-400",
  error: "border-destructive/40 text-destructive",
  info: "border-sky-500/30 text-sky-700 dark:text-sky-400",
}

/** The autoscaling verdict of every workload in a Kubernetes run's scope (the target and each workload with an HPA). */
export function ScalingVerdicts({ scaling }: { scaling: LoadScaling }) {
  return (
    <div className="min-w-0 rounded-lg border p-2" data-testid="loadtest-scaling">
      <div className="mb-1 flex items-center gap-1 text-[11px] font-medium text-muted-foreground">
        <Layers className="size-3" /> Autoscaling
        {scaling.note && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Info className="size-3 cursor-help" />
            </TooltipTrigger>
            <TooltipContent className="max-w-72">{scaling.note}</TooltipContent>
          </Tooltip>
        )}
      </div>
      <ul className="flex flex-col gap-1.5">
        {scaling.workloads.map((w) => {
          const tone = scalingTone(w.kind)
          const Icon = ICON[tone]
          return (
            <li key={w.nodeId} data-workload={w.workload} data-kind={w.kind} className="flex gap-1.5 text-xs">
              <Icon className={cn("mt-0.5 size-3.5 shrink-0", TEXT[tone])} />
              <div className="min-w-0">
                <div className="flex flex-wrap items-center gap-1">
                  <span className="font-medium">{w.workload}</span>
                  <Badge variant="outline" className={cn("h-4 px-1 text-[10px]", BADGE[tone])}>
                    {SCALING_LABEL[w.kind]}
                  </Badge>
                  {w.hpa && <span className="text-[10px] text-muted-foreground tabular-nums">{hpaLabel(w.hpa)}</span>}
                </div>
                <p className="mt-0.5 text-pretty">{w.message}</p>
              </div>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
