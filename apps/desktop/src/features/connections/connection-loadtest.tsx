import { useCallback, useEffect, useMemo, useState } from "react"
import { ArrowLeft, Gauge } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { api } from "@/lib/api/client"
import type { Connection, LoadTestSpec } from "@/lib/api/types"
import { Configure, StepHeader, type SetDraft } from "../panels/loadtest/configure"
import { INITIAL_DRAFT, type LoadDraft } from "../panels/loadtest/draft"
import { History } from "../panels/loadtest/history"
import { RunResults } from "../panels/loadtest/run-results"
import { LoadRunsContext, type LoadRunsSource } from "../panels/loadtest/runs-context"
import { describeExternal } from "./safety"
import { useConnectionsStore } from "./store"
import { ConnectionTargetPicker, type TargetDraftUpdate } from "./target-picker"
import { INITIAL_CONNECTION_TARGET, resolveConnectionTarget } from "./target-draft"

const EMPTY_RUNS: LoadRunsSource["runs"] = []

function newDraft(connectionId: string): LoadDraft {
  return { ...INITIAL_DRAFT, source: "connection", connection: { ...INITIAL_CONNECTION_TARGET, connectionId } }
}

/** Runs owned by a connection, with live samples, for results/history views. */
function useConnectionRuns(connectionId: string): LoadRunsSource {
  const runs = useConnectionsStore((s) => s.runs[connectionId] ?? EMPTY_RUNS)
  const samples = useConnectionsStore((s) => s.samples)
  const loadRuns = useConnectionsStore((s) => s.loadRuns)
  useEffect(() => {
    void loadRuns(connectionId).catch(() => undefined)
  }, [connectionId, loadRuns])
  return useMemo(() => ({ runs, samples }), [runs, samples])
}

/** The "Load test" tab of a connection: the shared wizard, then live results of the started run. */
export function ConnectionLoadTest({
  connection,
  runId,
  setRunId,
}: {
  connection: Connection
  /** Run shown instead of the wizard (just started or opened from History). */
  runId: string | null
  setRunId: (id: string | null) => void
}) {
  const stored = useConnectionsStore((s) => s.drafts[connection.id])
  const saveDraft = useConnectionsStore((s) => s.setDraft)
  const check = useConnectionsStore((s) => s.checks[connection.id])
  const upsertRun = useConnectionsStore((s) => s.upsertRun)
  const source = useConnectionRuns(connection.id)
  const draft = stored ?? newDraft(connection.id)
  const setDraft: SetDraft = useCallback(
    (update) => saveDraft(connection.id, update(useConnectionsStore.getState().drafts[connection.id] ?? newDraft(connection.id))),
    [connection.id, saveDraft],
  )
  const onTargetChange: TargetDraftUpdate = useCallback(
    (update) => setDraft((d) => ({ ...d, connection: update(d.connection) })),
    [setDraft],
  )

  const target = resolveConnectionTarget(connection, draft.connection)
  const external = describeExternal(target, connection, check)

  const start = async (spec: LoadTestSpec) => {
    const run = await api.startConnectionLoadTest(connection.id, spec)
    upsertRun(run)
    return run
  }

  return (
    <LoadRunsContext.Provider value={source}>
      {runId ? (
        <div className="flex h-full min-h-0 flex-col">
          <div className="flex h-9 shrink-0 items-center border-b px-2">
            <Button size="xs" variant="ghost" onClick={() => setRunId(null)}>
              <ArrowLeft /> New test
            </Button>
          </div>
          <div className="min-h-0 flex-1">
            <RunResults key={runId} runId={runId} />
          </div>
        </div>
      ) : (
        <Configure
          draft={draft}
          setDraft={setDraft}
          target={target}
          targetStep={
            <div>
              <StepHeader n={1} title="What do you want to test?" hint={`Pick a suggested request for ${connection.name} or describe your own.`} />
              <ConnectionTargetPicker connection={connection} value={draft.connection} onChange={onTargetChange} />
            </div>
          }
          external={external}
          describeForced={(t) => describeExternal(t, connection, check, true)}
          start={start}
          onStarted={(run) => setRunId(run.id)}
        />
      )}
    </LoadRunsContext.Provider>
  )
}

/** The "History" tab of a connection: its runs, compare two, open one. */
export function ConnectionHistory({ connection, onOpen }: { connection: Connection; onOpen: (runId: string) => void }) {
  const source = useConnectionRuns(connection.id)
  const [compare, setCompare] = useState<string[]>([])
  if (source.runs.length === 0) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <Gauge />
          </EmptyMedia>
          <EmptyTitle>No load tests yet</EmptyTitle>
          <EmptyDescription>Runs you start from this connection’s Load test tab are listed here, so you can compare them.</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return (
    <LoadRunsContext.Provider value={source}>
      <History compare={compare} setCompare={setCompare} onOpen={onOpen} />
    </LoadRunsContext.Provider>
  )
}
