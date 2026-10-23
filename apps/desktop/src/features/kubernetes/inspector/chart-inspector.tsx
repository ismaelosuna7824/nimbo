import { ChevronRightIcon, ChevronsDownUpIcon, ChevronsUpDownIcon, FileSlidersIcon, LayersIcon, RadioIcon, TriangleAlertIcon } from "lucide-react"
import { K8sIcon } from "@/components/k8s-icons/k8s-icon"
import { TechIcon } from "@/components/tech-icons/tech-icon"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { Section } from "@/features/docker/inspector/fields"
import { DiagnosticRow } from "@/features/panels/components/diagnostic-row"
import { revealFiles } from "@/features/panels/terraform/reveal-files"
import type { KubeGraph } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { collapseChart, expandChart, showObject } from "../actions"
import { useChartIcon, useExpandedCharts } from "../canvas/charts"
import { chartSummaries, kindsText, pluralKind, type ChartSummary } from "../canvas/model"

/** The inspector of a vendored chart's release node: chart, values files, live state and its objects by kind. */
export function ChartInspector({ graph, chartId }: { graph: KubeGraph; chartId: string }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const status = useWorkflowStore((s) => s.kube)
  const allDiagnostics = useWorkflowStore((s) => s.workflow!.diagnostics)
  const expanded = useExpandedCharts(workflowId)
  const chart = chartSummaries(graph, status, expanded).find((c) => c.group.id === chartId)
  if (!chart) return null
  const members = new Set(chart.members)
  const diagnostics = (allDiagnostics ?? []).filter((d) => d.nodeId && members.has(d.nodeId))
  const names = new Map(graph.nodes.filter((n) => members.has(n.id)).map((n) => [n.id, n.namespace ? `${n.namespace}/${n.name}` : n.name]))
  return (
    <div className="flex h-full flex-col" data-testid="k8s-chart-inspector" data-chart={chart.group.chart}>
      <Header chart={chart} />
      <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-4">
        <p className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">
          A third-party chart vendored in {chart.group.sourcePath}. Its {chart.members.length} objects are grouped on the canvas;
          configure them through its values. Pick an object below to see it on the canvas.
        </p>
        {chart.live && (
          <Section title="Live" icon={<RadioIcon className="size-3.5 text-sky-500" />}>
            <p className="text-xs" data-testid="k8s-chart-live">
              {chart.live.ready}/{chart.live.total} workloads ready
              {chart.live.problems > 0 ? ` · ${chart.live.problems} failing` : ""}
            </p>
          </Section>
        )}
        <Section title="Values" icon={<FileSlidersIcon className="size-3.5" />}>
          <ul className="flex flex-col gap-1">
            {(chart.group.valuesFiles ?? []).map((f) => (
              <li key={f}>
                <button type="button" className="max-w-full truncate font-mono text-xs text-primary hover:underline" onClick={() => revealFiles("", f)}>
                  {f}
                </button>
              </li>
            ))}
          </ul>
          <p className="text-[11px] text-muted-foreground">
            The chart's options go under <code className="font-mono">{chart.group.chart}:</code> in these files.
          </p>
        </Section>
        <Section title={`Objects (${chart.members.length})`} icon={<LayersIcon className="size-3.5" />}>
          <p className="text-[11px] text-muted-foreground">{kindsText(chart.kinds)}</p>
          <ul className="flex flex-col gap-0.5" data-testid="k8s-chart-objects">
            {chart.kinds.map((k) => (
              <li key={k.kind}>
                <Collapsible>
                  <CollapsibleTrigger className="group/kind flex w-full items-center gap-2 rounded-md px-1.5 py-1 text-left text-xs hover:bg-muted">
                    <ChevronRightIcon className="size-3 shrink-0 text-muted-foreground transition-transform group-data-[state=open]/kind:rotate-90" />
                    <K8sIcon icon={k.kind} size={14} />
                    <span className="flex-1 truncate">{pluralKind(k.kind, k.count)}</span>
                    <span className="text-muted-foreground tabular-nums">{k.count}</span>
                  </CollapsibleTrigger>
                  <CollapsibleContent>
                    <ul className="ml-6 flex flex-col border-l py-0.5 pl-2">
                      {k.ids.map((id) => (
                        <li key={id}>
                          <button
                            type="button"
                            data-testid={`k8s-chart-object-${id}`}
                            className="block max-w-full truncate rounded px-1 py-0.5 text-left font-mono text-[11px] hover:bg-muted hover:text-foreground"
                            title={id}
                            onClick={() => void showObject(id)}
                          >
                            {names.get(id)}
                          </button>
                        </li>
                      ))}
                    </ul>
                  </CollapsibleContent>
                </Collapsible>
              </li>
            ))}
          </ul>
        </Section>
        {diagnostics.length > 0 && (
          <Section title={`Problems (${diagnostics.length})`} icon={<TriangleAlertIcon className="size-3.5 text-amber-500" />}>
            <div className="-mx-3 flex flex-col rounded-lg border">
              {diagnostics.map((d, i) => (
                <DiagnosticRow key={i} diagnostic={d} nodeLabel={names.get(d.nodeId!)} onSelect={() => useWorkflowStore.getState().select(d.nodeId!)} />
              ))}
            </div>
          </Section>
        )}
      </div>
    </div>
  )
}

function Header({ chart }: { chart: ChartSummary }) {
  const g = chart.group
  const icon = useChartIcon(g.chart)
  return (
    <div className="flex items-start gap-3 border-b p-4">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl border bg-background">
        <TechIcon icon={icon} size={26} />
      </span>
      <div className="min-w-0 flex-1">
        <div className="truncate font-heading text-base leading-tight font-medium">{g.chart}</div>
        <div className="truncate font-mono text-[11px] text-muted-foreground" title={g.repository}>
          {g.repository ?? g.sourcePath}
        </div>
        <div className="mt-1.5 flex flex-wrap items-center gap-1.5">
          <Badge variant="secondary" className="gap-1">
            <TechIcon icon="helm" size={12} />
            {g.version ? `chart ${g.version}` : "chart"}
          </Badge>
          {g.appVersion && <Badge variant="outline">app {g.appVersion}</Badge>}
          <Badge variant="outline">release {g.release}</Badge>
          <Badge variant="outline">ns {g.namespace}</Badge>
        </div>
      </div>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button
            variant="outline"
            size="xs"
            className="shrink-0"
            data-testid="k8s-chart-inspector-toggle"
            onClick={() => (chart.expanded ? collapseChart(g.id) : void expandChart(g.id))}
          >
            {chart.expanded ? <ChevronsDownUpIcon /> : <ChevronsUpDownIcon />}
            {chart.expanded ? "Collapse" : "Expand"}
          </Button>
        </TooltipTrigger>
        <TooltipContent>{chart.expanded ? "Collapse into one node on the canvas" : `Show its ${chart.members.length} objects on the canvas`}</TooltipContent>
      </Tooltip>
    </div>
  )
}
