import { ChevronRightIcon, PlugIcon } from "lucide-react"
import { lazy, memo, Suspense } from "react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { useWorkflowStore } from "@/state/workflow-store"
import { FieldRow } from "../panels/endpoints/bits"
import { useEndpoints } from "../panels/endpoints/use-endpoints"
import { liveHighlights } from "./connect"
import { InspectorSection } from "./inspector-section"

// Snippet highlighting (CodeMirror parsers) loads only for deployed resources.
const CompactResourceCard = lazy(() =>
  import("../panels/endpoints/resource-card").then((m) => ({ default: m.CompactResourceCard })),
)

/**
 * How to use a deployed node: per resource its usage card (identifiers, address, snippet), then the state's other
 * identifiers the card doesn't show, then every attribute behind "All attributes". Memoized by node: typing in the
 * node's form never re-renders it.
 */
export const ConnectSection = memo(function ConnectSection({ nodeId }: { nodeId: string }) {
  const resources = useWorkflowStore((s) => s.deployed?.resources)
  const endpoints = useEndpoints()
  const live = (resources ?? []).filter((r) => r.nodeId === nodeId)
  const usable = (endpoints.data?.resources ?? []).filter((r) => r.nodeId === nodeId)
  if (live.length === 0 && usable.length === 0) return null
  const addresses = [...new Set([...usable.map((r) => r.address), ...live.map((r) => r.address)])]
  return (
    <InspectorSection id="connect" title="Connect" icon={<PlugIcon className="size-3.5 text-primary" />} data-testid="connect-section">
      {addresses.map((address) => {
        const access = usable.find((r) => r.address === address)
        const deployed = live.find((r) => r.address === address)
        const rows = deployed ? liveHighlights(deployed.values, access) : []
        const extra = rows.length > 0 && (
          <div className="flex flex-col gap-1" data-testid="connect-live">
            {rows.map((f) => (
              <FieldRow key={f.label} field={f} compact />
            ))}
          </div>
        )
        return (
          <div key={address} className="flex flex-col gap-2">
            {addresses.length > 1 && <span className="font-mono text-[11px] text-muted-foreground">{address}</span>}
            {access ? (
              <Suspense fallback={<Skeleton className="h-20 w-full" />}>
                <CompactResourceCard resource={access} extra={extra} />
              </Suspense>
            ) : (
              extra
            )}
            {deployed && (
              <Collapsible>
                <CollapsibleTrigger className="group/all flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
                  <ChevronRightIcon className="size-3 transition-transform group-data-[state=open]/all:rotate-90" />
                  All attributes
                </CollapsibleTrigger>
                <CollapsibleContent>
                  <pre className="mt-1 max-h-72 overflow-auto rounded-md bg-muted/50 p-2 font-mono text-[11px]">{JSON.stringify(deployed.values, null, 2)}</pre>
                </CollapsibleContent>
              </Collapsible>
            )}
          </div>
        )
      })}
    </InspectorSection>
  )
})
