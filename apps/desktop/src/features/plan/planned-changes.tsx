import { useState } from "react"
import { Button } from "@/components/ui/button"
import type { AttributeChange, PlannedChange } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { InspectorSection } from "../inspector/inspector-section"
import { ACTION_META, type MarkedAction } from "./plan"
import { ActionSymbol } from "./plan-marker"
import { useNodePlan } from "./plan-store"

/** Why Terraform deletes or replaces, in the user's words (action_reason values that explain a delete or replace). */
const REASONS: Record<string, string> = {
  replace_because_cannot_update: "an attribute can't change in place",
  replace_because_tainted: "it is marked tainted",
  replace_by_request: "a replacement was requested",
  replace_by_triggers: "a replace_triggered_by reference changed",
  delete_because_no_resource_config: "its node was removed",
  delete_because_no_module: "its module was removed",
  delete_because_count_index: "count no longer includes it",
  delete_because_each_key: "for_each no longer includes it",
  delete_because_wrong_repetition: "count / for_each changed",
  delete_because_no_move_target: "its moved block has no target",
}

/** Attributes listed before "Show all" (a create lists every attribute it sets). */
const FIRST_ATTRS = 6

function formatValue(v: unknown): string {
  if (v === undefined || v === null) return "null"
  return JSON.stringify(v)
}

function Value({ children, muted }: { children: React.ReactNode; muted?: boolean }) {
  const text = typeof children === "string" ? children : undefined
  return (
    <span className={cn("min-w-0 truncate font-mono text-[11px]", muted && "text-muted-foreground italic")} title={text}>
      {children}
    </span>
  )
}

function AttributeRow({ attr, created }: { attr: AttributeChange; created: boolean }) {
  const after = attr.sensitive ? (
    <Value muted>(sensitive)</Value>
  ) : attr.unknown ? (
    <Value muted>known after apply</Value>
  ) : (
    <Value>{formatValue(attr.after)}</Value>
  )
  return (
    <div className="flex flex-col gap-0.5 rounded-md bg-muted/50 px-2 py-1" data-testid="planned-attribute">
      <div className="flex items-center gap-2">
        <span className="font-mono text-[11px] text-muted-foreground">{attr.name}</span>
        {attr.forcesReplacement && <span className="text-[10px] text-destructive">forces replacement</span>}
      </div>
      <div className="flex min-w-0 items-center gap-1.5">
        {!created && (
          <>
            {attr.sensitive ? <Value muted>(sensitive)</Value> : <Value>{formatValue(attr.before)}</Value>}
            <span aria-label="becomes" className="shrink-0 text-muted-foreground">
              →
            </span>
          </>
        )}
        {after}
      </div>
    </div>
  )
}

/** What decides the change first (attributes forcing the replacement), then values the user set, then "known after apply". */
function byRelevance(attrs: AttributeChange[]): AttributeChange[] {
  const rank = (a: AttributeChange) => (a.forcesReplacement ? 0 : a.unknown ? 2 : 1)
  return attrs.toSorted((a, b) => rank(a) - rank(b))
}

function ChangeDetails({ change, showAddress, stale }: { change: PlannedChange; showAddress: boolean; stale: boolean }) {
  const [all, setAll] = useState(false)
  const action = change.action as MarkedAction
  const attrs = byRelevance(change.attributes ?? [])
  const shown = all ? attrs : attrs.slice(0, FIRST_ATTRS)
  const reason = change.reason && REASONS[change.reason]
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex min-w-0 items-center gap-2 text-xs">
        <ActionSymbol action={action} stale={stale} />
        <span className="shrink-0 font-medium">{ACTION_META[action].label}</span>
        {showAddress && (
          <span className="min-w-0 truncate font-mono text-[11px] text-muted-foreground" title={change.address}>
            {change.address}
          </span>
        )}
      </div>
      {reason && <p className="text-xs text-muted-foreground">Because {reason}.</p>}
      {shown.map((a) => (
        <AttributeRow key={a.name} attr={a} created={action === "create"} />
      ))}
      {attrs.length > FIRST_ATTRS && (
        <Button variant="ghost" size="xs" className="self-start" onClick={() => setAll(!all)}>
          {all ? "Show fewer" : `Show all ${attrs.length} attributes`}
        </Button>
      )}
    </div>
  )
}

/** Inspector section: what the plan shown on the canvas does to this node's resources, attribute by attribute. */
export function PlannedChangesSection({ nodeId }: { nodeId: string }) {
  const entry = useNodePlan(nodeId)
  if (!entry) return null
  const { plan, stale } = entry
  const showAddress = plan.changes.length > 1 || plan.changes.some((c) => c.address.startsWith("module."))
  return (
    <InspectorSection id="plan" title={stale ? "Planned changes (outdated)" : "Planned changes"} navLabel="Plan">
      <div className="flex flex-col gap-3" data-testid="planned-changes">
        {stale && <p className="text-xs text-amber-700 dark:text-amber-400">The workflow or the sandbox changed after this plan. Plan again to see what Deploy will do now.</p>}
        {plan.changes.map((c) => (
          <ChangeDetails key={c.address} change={c} showAddress={showAddress} stale={stale} />
        ))}
      </div>
    </InspectorSection>
  )
}
