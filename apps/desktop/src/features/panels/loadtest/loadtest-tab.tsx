import { useMemo, useState, type ReactNode } from "react"
import { Gauge, ShieldAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { api } from "@/lib/api/client"
import type { LoadTestSpec } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { describeExternal } from "@/features/connections/safety"
import { useConnections, useConnectionsStore } from "@/features/connections/store"
import { StatusBadge } from "../components/status-badge"
import { Configure } from "./configure"
import { INITIAL_DRAFT, type LoadDraft } from "./draft"
import { History } from "./history"
import { RunResults } from "./run-results"
import { LoadRunsContext } from "./runs-context"
import { useWorkflowTarget } from "./use-workflow-target"
import { WorkflowTargetStep } from "./workflow-target"

type View = "configure" | "results" | "history"

/** Header note: where the traffic of what is on screen goes. */
export function TrafficScope({ external }: { external: string | null }) {
  return external ? (
    <Badge
      variant="outline"
      className="ml-auto h-5 max-w-[50%] gap-1 border-amber-500/40 text-[11px] text-amber-700 dark:text-amber-400"
      title={`Traffic goes to ${external}, outside your local sandbox.`}
    >
      <ShieldAlert className="size-3" /> <span className="truncate">External target · {external}</span>
    </Badge>
  ) : (
    <span className="ml-auto hidden text-[11px] text-muted-foreground md:inline">Traffic goes to your local sandbox only.</span>
  )
}

export function LoadTestTab() {
  const workflow = useWorkflowStore((s) => s.workflow)
  const loadTests = useWorkflowStore((s) => s.loadTests)
  const loadSamples = useWorkflowStore((s) => s.loadSamples)
  const pro = useWorkflowStore((s) => s.mode === "pro")
  const [view, setView] = useState<View>("configure")
  const [draft, setDraft] = useState<LoadDraft>(INITIAL_DRAFT)
  const [runId, setRunId] = useState<string | null>(null)
  const [compare, setCompare] = useState<string[]>([])
  const resolved = useWorkflowTarget(draft, setDraft)
  const connections = useConnections()
  const source = useMemo(() => ({ runs: loadTests, samples: loadSamples }), [loadTests, loadSamples])

  if (!workflow) return null

  const shownRunId = runId ?? loadTests[0]?.id ?? null
  const shownRun = loadTests.find((r) => r.id === shownRunId)
  const shownConn = connections.data?.find((c) => c.id === shownRun?.spec.target.connectionId)
  const scope =
    view === "results"
      ? shownRun?.external
        ? (describeExternal(shownRun.spec.target, shownConn, undefined, true)?.where ?? "an external service")
        : null
      : (resolved.external?.where ?? null)

  const start = async (spec: LoadTestSpec) => {
    const run = await api.startLoadTest(workflow.id, spec)
    const s = useWorkflowStore.getState()
    if (!s.loadTests.some((r) => r.id === run.id)) useWorkflowStore.setState({ loadTests: [run, ...s.loadTests] })
    return run
  }

  const views: { id: View; label: string; extra?: ReactNode }[] = [
    { id: "configure", label: "New test" },
    {
      id: "results",
      label: "Results",
      extra: shownRun?.status === "running" ? <StatusBadge status="running" label="Live" className="h-4" /> : undefined,
    },
    { id: "history", label: "History & compare", extra: <span className="text-[10px] opacity-70">{loadTests.length}</span> },
  ]

  return (
    <LoadRunsContext.Provider value={source}>
      <div className="flex h-full min-h-0 flex-col">
        <div className="flex h-8 shrink-0 items-center gap-0.5 border-b px-1.5" role="tablist">
          {views.map((v) => (
            <button
              key={v.id}
              type="button"
              role="tab"
              aria-selected={view === v.id}
              onClick={() => setView(v.id)}
              className={cn(
                "flex h-6 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50",
                view === v.id && "bg-muted font-medium text-foreground",
              )}
            >
              {v.label}
              {v.extra}
            </button>
          ))}
          <TrafficScope external={scope} />
        </div>
        <div className="min-h-0 flex-1">
          {view === "configure" && (
            <Configure
              draft={draft}
              setDraft={setDraft}
              target={resolved.target}
              targetStep={<WorkflowTargetStep draft={draft} setDraft={setDraft} resolved={resolved} pro={pro} />}
              external={resolved.external}
              describeForced={(t) =>
                describeExternal(t, resolved.connection, resolved.connection ? useConnectionsStore.getState().checks[resolved.connection.id] : undefined, true)
              }
              start={start}
              onStarted={(run) => {
                setRunId(run.id)
                setView("results")
              }}
            />
          )}
          {view === "results" &&
            (shownRunId ? (
              <RunResults key={shownRunId} runId={shownRunId} />
            ) : (
              <Empty className="h-full">
                <EmptyHeader>
                  <EmptyMedia variant="icon">
                    <Gauge />
                  </EmptyMedia>
                  <EmptyTitle>No results yet</EmptyTitle>
                  <EmptyDescription>
                    A load test measures speed (response times), stability (how steady they stay) and capacity (when
                    things start failing) under the traffic you choose.
                  </EmptyDescription>
                </EmptyHeader>
              </Empty>
            ))}
          {view === "history" && (
            <History
              compare={compare}
              setCompare={setCompare}
              onOpen={(id) => {
                setRunId(id)
                setView("results")
              }}
            />
          )}
        </div>
      </div>
    </LoadRunsContext.Provider>
  )
}
