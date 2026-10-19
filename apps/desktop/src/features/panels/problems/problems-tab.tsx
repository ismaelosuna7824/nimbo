import { useMemo } from "react"
import { ChevronRight, CircleAlert, CircleCheck, Settings2, ShieldCheck, TriangleAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useSettingsView } from "@/features/settings/store"
import { isBestPractice, type Diagnostic } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { nodeLabelById } from "../lib/nodes"

interface Group {
  id: "errors" | "warnings" | "practices"
  label: string
  icon: typeof CircleAlert
  className: string
  hint?: string
  items: Diagnostic[]
}

/** Severity order inside a group: warnings before info. */
const RANK = { error: 0, warning: 1, info: 2 } as const

export function ProblemsTab() {
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const diagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const select = useWorkflowStore((s) => s.select)
  const openSettings = useSettingsView((s) => s.openView)
  const labels = useMemo(() => nodeLabelById(nodes), [nodes])

  const groups = useMemo<Group[]>(() => {
    const all = diagnostics ?? []
    const practices = all.filter(isBestPractice).sort((a, b) => RANK[a.severity] - RANK[b.severity])
    return [
      { id: "errors", label: "Errors", icon: CircleAlert, className: "text-destructive", items: all.filter((d) => d.severity === "error") },
      {
        id: "warnings",
        label: "Warnings",
        icon: TriangleAlert,
        className: "text-amber-600 dark:text-amber-400",
        items: all.filter((d) => d.severity !== "error" && !isBestPractice(d)).sort((a, b) => RANK[a.severity] - RANK[b.severity]),
      },
      {
        id: "practices",
        label: "Best practices",
        icon: ShieldCheck,
        className: "text-sky-600 dark:text-sky-400",
        hint: "Suggestions to make the Terraform production-ready. They never block a deploy.",
        items: practices,
      },
    ]
  }, [diagnostics])

  if (groups.every((g) => g.items.length === 0)) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <CircleCheck />
          </EmptyMedia>
          <EmptyTitle>No problems</EmptyTitle>
          <EmptyDescription>
            Missing required settings, broken references, invalid expressions and best-practice suggestions show up here as you edit.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <ScrollArea className="h-full">
      {groups.map((g) =>
        g.items.length === 0 ? null : (
          <Collapsible key={g.id} defaultOpen asChild>
            <section aria-label={g.label}>
              <div className="sticky top-0 z-10 flex items-center border-b bg-background/95 pr-2 backdrop-blur">
                <CollapsibleTrigger
                  className={cn(
                    "group flex flex-1 items-center gap-1.5 px-3 py-1 text-left text-[11px] font-semibold tracking-wide uppercase outline-none focus-visible:bg-muted",
                    g.className,
                  )}
                >
                  <ChevronRight className="size-3.5 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
                  <g.icon className="size-3.5" />
                  {g.label}
                  <span className="text-muted-foreground tabular-nums">{g.items.length}</span>
                  {g.hint && <span className="ml-2 truncate font-normal tracking-normal text-muted-foreground normal-case">{g.hint}</span>}
                </CollapsibleTrigger>
                {g.id === "practices" && (
                  <Button size="xs" variant="ghost" className="text-muted-foreground" onClick={() => openSettings("practices")}>
                    <Settings2 /> Configure
                  </Button>
                )}
              </div>
              <CollapsibleContent className="divide-y">
                {g.items.map((d, i) => (
                  <DiagnosticRow
                    key={`${d.code}-${d.nodeId ?? ""}-${d.attribute ?? ""}-${i}`}
                    diagnostic={d}
                    nodeLabel={d.nodeId ? labels[d.nodeId] : undefined}
                    onSelect={d.nodeId ? () => select(d.nodeId!) : undefined}
                  />
                ))}
              </CollapsibleContent>
            </section>
          </Collapsible>
        ),
      )}
    </ScrollArea>
  )
}
