import { useMemo } from "react"
import { CircleCheck } from "lucide-react"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { Severity } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { SEVERITY_META } from "../lib/severity"
import { nodeLabelById } from "../lib/nodes"

const ORDER: Severity[] = ["error", "warning", "info"]

export function ProblemsTab() {
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const diagnostics = useWorkflowStore((s) => s.workflow?.diagnostics) ?? []
  const select = useWorkflowStore((s) => s.select)
  const labels = useMemo(() => nodeLabelById(nodes), [nodes])

  if (diagnostics.length === 0) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <CircleCheck />
          </EmptyMedia>
          <EmptyTitle>No problems</EmptyTitle>
          <EmptyDescription>
            Missing required settings, broken references and invalid expressions show up here as you edit.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <ScrollArea className="h-full">
      {ORDER.map((severity) => {
        const items = diagnostics.filter((d) => d.severity === severity)
        if (items.length === 0) return null
        const meta = SEVERITY_META[severity]
        return (
          <section key={severity}>
            <h3
              className={cn(
                "sticky top-0 z-10 flex items-center gap-1.5 border-b bg-background/95 px-3 py-1 text-[11px] font-semibold tracking-wide uppercase backdrop-blur",
                meta.className,
              )}
            >
              <meta.icon className="size-3.5" />
              {meta.label}
              <span className="text-muted-foreground tabular-nums">{items.length}</span>
            </h3>
            <div className="divide-y">
              {items.map((d, i) => (
                <DiagnosticRow
                  key={`${d.code}-${d.nodeId ?? ""}-${d.attribute ?? ""}-${i}`}
                  diagnostic={d}
                  nodeLabel={d.nodeId ? labels[d.nodeId] : undefined}
                  onSelect={d.nodeId ? () => select(d.nodeId!) : undefined}
                />
              ))}
            </div>
          </section>
        )
      })}
    </ScrollArea>
  )
}
