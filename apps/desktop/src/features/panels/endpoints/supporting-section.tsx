import { Cable, ChevronRight } from "lucide-react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import type { AccessField, ResourceAccess } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CopyButton } from "./bits"
import { cardDomId } from "./use-endpoints"

export const SUPPORTING_SECTION_ID = "endpoints-supporting"

/** The identifier worth copying from a supporting resource: its ARN, else its ID, else the first field. */
function mainIdentifier(r: ResourceAccess): AccessField | undefined {
  return (
    r.fields.find((f) => f.label === "ARN") ??
    r.fields.find((f) => f.label === "ID") ??
    r.fields.find((f) => f.label === "URL") ??
    r.fields[0]
  )
}

/**
 * Plumbing Terraform created around the resources people use (roles,
 * permissions, triggers, routing): one compact row each, collapsed by default.
 */
export function SupportingSection({
  resources,
  open,
  onOpenChange,
  highlight,
}: {
  resources: ResourceAccess[]
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Address of the row to highlight. */
  highlight: string | null
}) {
  return (
    <Collapsible
      id={SUPPORTING_SECTION_ID}
      open={open}
      onOpenChange={onOpenChange}
      className="scroll-mt-2 rounded-lg border"
    >
      <CollapsibleTrigger className="group/sup flex w-full items-baseline gap-2 px-3 py-2 text-left">
        <ChevronRight className="size-3.5 self-center text-muted-foreground transition-transform group-data-[state=open]/sup:rotate-90" />
        <Cable className="size-3.5 self-center text-muted-foreground" />
        <span className="text-sm font-semibold">Supporting resources ({resources.length})</span>
        <span className="min-w-0 truncate text-xs text-muted-foreground">
          — roles, permissions and wiring Terraform created for you
        </span>
      </CollapsibleTrigger>
      <CollapsibleContent className="border-t">
        <ul className="divide-y">
          {resources.map((r) => {
            const id = mainIdentifier(r)
            return (
              <li
                key={r.address}
                id={cardDomId(r.address)}
                className={cn(
                  "grid scroll-mt-2 grid-cols-[minmax(0,14rem)_minmax(0,14rem)_minmax(0,1fr)_auto] items-center gap-3 px-3 py-1 text-xs",
                  highlight === r.address && "bg-primary/10 ring-2 ring-primary ring-inset",
                )}
              >
                <span className="truncate font-medium" title={r.summary || r.label}>
                  {r.label}
                </span>
                <span className="truncate font-mono text-[11px] text-muted-foreground" title={r.address}>
                  {r.type}
                </span>
                <span className="truncate font-mono text-[11px] text-muted-foreground select-all" title={id?.value}>
                  {id?.value}
                </span>
                {id ? <CopyButton value={id.value} label={`Copy ${id.label}`} what={`${id.label} copied`} /> : <span />}
              </li>
            )
          })}
        </ul>
      </CollapsibleContent>
    </Collapsible>
  )
}
