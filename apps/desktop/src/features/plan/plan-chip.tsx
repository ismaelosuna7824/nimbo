import { FileDiffIcon, RefreshCwIcon, TriangleAlertIcon, XIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { formatRelative } from "../panels/lib/format"
import { runAction } from "../toolbar/actions"
import { ACTION_META, LEGEND_ACTIONS, type MarkedAction } from "./plan"
import { ActionSymbol } from "./plan-marker"
import { hidePlanFromCanvas, usePlanOverlay, type PlanOverlay } from "./plan-store"

const STALE_TEXT = {
  edited: "The workflow changed after this plan, so Deploy may do something else.",
  deployed: "The sandbox was deployed to after this plan, so Deploy may do something else.",
} as const

const planAgain = () => void runAction("plan", { showLog: false })

/**
 * The Build canvas's plan legend, shown once the user asked to see the latest plan on the canvas (the plan toast or the
 * Deploy tab) until they dismiss it: what the markers mean, how many of each, an outdated warning and the planned
 * changes no card shows (resources of removed nodes).
 */
export function PlanChip() {
  const { shown, run, overlay, loading, failed } = usePlanOverlay()
  const planning = useWorkflowStore((s) => s.runs.some((r) => r.action === "plan" && (r.status === "queued" || r.status === "running")))
  if (!shown) return null

  let body: React.ReactNode
  if (planning) {
    body = (
      <span className="flex h-7 items-center gap-1.5 px-2.5 text-[0.8rem]">
        <Spinner className="size-3.5" /> Planning…
      </span>
    )
  } else if (!run || failed || (!loading && !overlay)) {
    body = (
      <span className="flex h-7 items-center gap-2 pr-1 pl-2.5 text-[0.8rem] text-muted-foreground">
        {!run ? "No plan for this environment yet" : "This plan has no per-resource details"}
        <Button size="xs" variant="outline" onClick={planAgain}>
          {run ? "Plan again" : "Plan"}
        </Button>
      </span>
    )
  } else if (!overlay) {
    body = (
      <span className="flex h-7 items-center gap-1.5 px-2.5 text-[0.8rem]">
        <Spinner className="size-3.5" /> Plan
      </span>
    )
  } else {
    body = <PlanSummaryButton overlay={overlay} />
  }

  return (
    <div className="flex items-center rounded-md border bg-background/80 shadow-xs backdrop-blur" data-testid="plan-chip">
      {body}
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label="Hide plan from canvas" onClick={hidePlanFromCanvas}>
            <XIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent>Hide plan from canvas</TooltipContent>
      </Tooltip>
    </div>
  )
}

function PlanSummaryButton({ overlay }: { overlay: PlanOverlay }) {
  const { counts, stale, run, offCanvas } = overlay
  const actions = LEGEND_ACTIONS.filter((a) => counts[a] > 0)
  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="ghost" size="sm" className="font-normal" aria-label="Plan details">
          <FileDiffIcon />
          <span className="font-medium">Plan</span>
          {stale && <span className="text-amber-600 dark:text-amber-400">outdated</span>}
          {actions.length === 0 ? (
            <span className="text-muted-foreground">no changes</span>
          ) : (
            <span className="flex items-center gap-1 font-mono text-[11px] tabular-nums">
              {actions.map((a) => (
                <span key={a} className={cn("flex items-center gap-0.5", stale && "text-muted-foreground")}>
                  {ACTION_META[a].symbol}
                  {counts[a]}
                </span>
              ))}
            </span>
          )}
        </Button>
      </PopoverTrigger>
      <PopoverContent align="end" className="w-80 gap-3 p-3 text-xs">
        <div className="flex items-baseline justify-between gap-2">
          <span className="font-medium">Latest plan</span>
          <span className="text-muted-foreground">
            {run.environment && <span className="font-mono">{run.environment} · </span>}
            {formatRelative(run.createdAt)}
          </span>
        </div>
        {stale && (
          <div className="flex gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-amber-700 dark:text-amber-300">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            <div className="flex flex-col items-start gap-1.5">
              <span>Outdated. {STALE_TEXT[stale]}</span>
              <Button size="xs" variant="outline" onClick={planAgain}>
                <RefreshCwIcon /> Plan again
              </Button>
            </div>
          </div>
        )}
        {actions.length === 0 ? (
          <p className="text-muted-foreground">No changes: Deploy has nothing to do.</p>
        ) : (
          <ul className="flex flex-col gap-1.5">
            {actions.map((a) => (
              <li key={a} className="flex items-center gap-2">
                <ActionSymbol action={a} stale={stale !== null} />
                <span className="flex-1">{ACTION_META[a].label}</span>
                <span className="text-muted-foreground tabular-nums">{counts[a]}</span>
              </li>
            ))}
          </ul>
        )}
        {offCanvas.length > 0 && (
          <div className="flex flex-col gap-1.5 border-t pt-2">
            <span className="text-muted-foreground">Not on the canvas</span>
            <ul className="flex max-h-40 flex-col gap-1 overflow-y-auto">
              {offCanvas.map((c) => (
                <li key={c.address} className="flex min-w-0 items-center gap-2">
                  <ActionSymbol action={c.action as MarkedAction} stale={stale !== null} />
                  <span className="min-w-0 truncate font-mono text-[11px]" title={c.address}>
                    {c.address}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        )}
        {actions.length > 0 && <p className="text-muted-foreground">Select a card to see its attribute changes in the inspector.</p>}
      </PopoverContent>
    </Popover>
  )
}
