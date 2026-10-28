import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { ACTION_META, actionTone, countActions, LEGEND_ACTIONS, type MarkedAction, type NodePlan } from "./plan"
import { useNodePlan } from "./plan-store"

/** The action symbol in its tone: the card corner marker, the chip's legend and the inspector share it. */
export function ActionSymbol({ action, stale, className }: { action: MarkedAction; stale?: boolean; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        "inline-flex size-4.5 shrink-0 items-center justify-center rounded-full font-mono text-[11px] leading-none font-semibold",
        actionTone(action, stale),
        className,
      )}
    >
      {ACTION_META[action].symbol}
    </span>
  )
}

function describe(plan: NodePlan): string {
  const { changes } = plan
  if (changes.length === 1) return `Will be ${ACTION_META[plan.action].verb}`
  const counts = countActions(changes)
  const parts = LEGEND_ACTIONS.filter((a) => counts[a] > 0).map((a) => `${counts[a]} ${ACTION_META[a].past}`)
  return `${changes.length} resources change: ${parts.join(", ")}`
}

/** Corner marker of a card whose resources the shown plan changes; nothing for unchanged cards or with no plan shown. */
export function PlanMarker({ nodeId }: { nodeId: string }) {
  const entry = useNodePlan(nodeId)
  if (!entry) return null
  const { plan, stale } = entry
  const text = `${stale ? "Outdated plan — " : ""}${describe(plan)}`
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          data-slot="plan-marker"
          data-action={plan.action}
          role="img"
          aria-label={text}
          // Opaque base: the action tints are translucent and would show the card's border and selection ring.
          className="absolute -top-2 -right-2 z-10 rounded-full bg-background shadow-sm ring-2 ring-background"
        >
          <ActionSymbol action={plan.action} stale={stale} className="size-5 text-xs" />
        </span>
      </TooltipTrigger>
      <TooltipContent>{text}</TooltipContent>
    </Tooltip>
  )
}
