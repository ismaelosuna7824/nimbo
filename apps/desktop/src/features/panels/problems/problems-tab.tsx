import { useMemo } from "react"
import { ChevronRight, CircleAlert, CircleCheck, Route, Settings2, ShieldCheck, TriangleAlert } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { useSettingsView } from "@/features/settings/store"
import { TermHint } from "@/features/network/glossary"
import { openReachability } from "@/features/network/store"
import { hasNetworkResources } from "@/features/network/use-network"
import { isBestPractice, isNetworkFinding, type Diagnostic } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { DiagnosticRow } from "../components/diagnostic-row"
import { nodeLabelById } from "../lib/nodes"

interface Group {
  id: "errors" | "warnings" | "practices" | "network"
  label: string
  icon: typeof CircleAlert
  className: string
  hint?: string
  /** Header tooltip. */
  help?: string
  /** Shown even without findings (a workflow with a network gets an explicit "all clear"). */
  always?: boolean
  items: Diagnostic[]
}

/** Severity order inside a group: warnings before info. */
const RANK = { error: 0, warning: 1, info: 2 } as const

export function ProblemsTab() {
  const nodes = useWorkflowStore((s) => s.workflow?.nodes)
  const diagnostics = useWorkflowStore((s) => s.workflow?.diagnostics)
  const docker = useWorkflowStore((s) => s.workflow?.kind === "docker")
  const kubeGraph = useWorkflowStore((s) => (s.workflow?.kind === "kubernetes" ? s.workflow.kubernetes?.graph : undefined))
  const kubernetes = useWorkflowStore((s) => s.workflow?.kind === "kubernetes")
  const select = useWorkflowStore((s) => s.select)
  const openSettings = useSettingsView((s) => s.openView)
  // Kubernetes objects read "Deployment shop/web" (node ids are "Deployment/shop/web").
  const labels = useMemo(
    () =>
      kubeGraph
        ? Object.fromEntries(kubeGraph.nodes.map((n) => [n.id, `${n.kind} ${n.namespace ? `${n.namespace}/` : ""}${n.name}`]))
        : nodeLabelById(nodes),
    [nodes, kubeGraph],
  )
  const withNetwork = useMemo(() => hasNetworkResources(nodes), [nodes])

  const groups = useMemo<Group[]>(() => {
    const all = diagnostics ?? []
    const byRank = (a: Diagnostic, b: Diagnostic) => RANK[a.severity] - RANK[b.severity]
    const practices = all.filter(isBestPractice).sort(byRank)
    const network = all.filter(isNetworkFinding).sort(byRank)
    return [
      {
        id: "errors",
        label: "Errors",
        icon: CircleAlert,
        className: "text-destructive",
        items: all.filter((d) => d.severity === "error" && !isNetworkFinding(d)),
      },
      {
        id: "warnings",
        label: "Warnings",
        icon: TriangleAlert,
        className: "text-amber-600 dark:text-amber-400",
        items: all
          .filter((d) => d.severity !== "error" && !isBestPractice(d) && !isNetworkFinding(d))
          .sort(byRank),
      },
      {
        id: "practices",
        label: "Best practices",
        icon: ShieldCheck,
        className: "text-sky-600 dark:text-sky-400",
        hint: docker
          ? "Suggestions to make the images, compose services and their security production-ready. They never block a run."
          : kubernetes
            ? "Suggestions to make the workloads reliable, secure and reachable. They never block a run."
            : "Suggestions to make the Terraform production-ready. They never block a deploy.",
        items: practices,
      },
      {
        id: "network",
        label: "Network",
        icon: Route,
        className: "text-teal-600 dark:text-teal-400",
        hint: "Who can reach what. Never blocks a deploy.",
        help: "Network checks review your VPCs, subnets, route tables, gateways and security groups: what is exposed to the internet, what can't get out, and what can't talk to each other. They never block a deploy.",
        always: withNetwork,
        items: network,
      },
    ]
  }, [diagnostics, withNetwork, docker, kubernetes])

  if (groups.every((g) => g.items.length === 0 && !g.always)) {
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
    // Block, not Radix's table wrapper: rows keep the panel's width, so long hints truncate instead of being cut off.
    <ScrollArea className="h-full [&_[data-slot=scroll-area-viewport]>div]:block!">
      {groups.map((g) =>
        g.items.length === 0 && !g.always ? null : (
          <Collapsible key={g.id} defaultOpen asChild>
            <section aria-label={g.label}>
              <div className="sticky top-0 z-10 flex items-center border-b bg-background/95 pr-2 backdrop-blur">
                <CollapsibleTrigger
                  className={cn(
                    "group flex min-w-0 flex-1 items-center gap-1.5 px-3 py-1 text-left text-[11px] font-semibold tracking-wide whitespace-nowrap uppercase outline-none focus-visible:bg-muted",
                    g.className,
                  )}
                >
                  <ChevronRight className="size-3.5 shrink-0 text-muted-foreground transition-transform group-data-[state=open]:rotate-90" />
                  <g.icon className="size-3.5 shrink-0" />
                  {g.label}
                  <span className="text-muted-foreground tabular-nums">{g.items.length}</span>
                  {g.hint && <span className="ml-2 min-w-0 truncate font-normal tracking-normal text-muted-foreground normal-case">{g.hint}</span>}
                </CollapsibleTrigger>
                {g.help && <TermHint text={g.help} label={`About ${g.label.toLowerCase()} checks`} />}
                {g.id === "practices" && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="shrink-0 text-muted-foreground"
                    title={kubernetes ? "Configure Kubernetes checks" : "Configure"}
                    data-testid="problems-configure-practices"
                    onClick={() => openSettings("practices")}
                  >
                    <Settings2 />
                    <span className="sr-only @2xl:not-sr-only">{kubernetes ? "Configure Kubernetes checks" : "Configure"}</span>
                  </Button>
                )}
                {g.id === "network" && (
                  <Button
                    size="xs"
                    variant="ghost"
                    className="ml-1 shrink-0 text-muted-foreground"
                    title="Check reachability…"
                    onClick={() => void openReachability()}
                  >
                    <Route />
                    <span className="sr-only @2xl:not-sr-only">Check reachability…</span>
                  </Button>
                )}
              </div>
              <CollapsibleContent className="divide-y">
                {g.items.map((d, i) => (
                  <DiagnosticRow
                    key={`${d.code}-${d.nodeId ?? ""}-${d.attribute ?? ""}-${i}`}
                    diagnostic={d}
                    nodeLabel={d.nodeId ? (labels[d.nodeId] ?? (docker ? d.nodeId.replace(/^service:/, "") : kubernetes ? d.nodeId : undefined)) : undefined}
                    onSelect={d.nodeId ? () => select(d.nodeId!) : undefined}
                  />
                ))}
                {g.items.length === 0 && (
                  <p className="flex items-center gap-2 px-3 py-1.5 text-xs text-muted-foreground">
                    <CircleCheck className="size-3.5 text-emerald-500" />
                    No network issues found. Use “Check reachability…” to test a specific connection.
                  </p>
                )}
              </CollapsibleContent>
            </section>
          </Collapsible>
        ),
      )}
    </ScrollArea>
  )
}
