import { useContext, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api/client"
import type { LoadSample, LoadTestRun } from "@/lib/api/types"
import { mergeSamples } from "./metrics"
import { LoadRunsContext } from "./runs-context"

const EMPTY: LoadSample[] = []

/**
 * A load-test run with its full timeline: the persisted run (refetched when
 * its status changes) overlaid with the latest status and live samples from SSE.
 */
export function useLoadRun(runId: string | null): { run: LoadTestRun | undefined; samples: LoadSample[]; loading: boolean } {
  const source = useContext(LoadRunsContext)
  const listed = source.runs.find((r) => r.id === runId)
  const live = runId ? (source.samples[runId] ?? EMPTY) : EMPTY
  const query = useQuery({
    queryKey: ["loadtest", runId, listed?.status],
    queryFn: () => api.loadTest(runId!),
    enabled: !!runId,
  })
  const fetched = query.data
  const run = useMemo(() => {
    if (!fetched) return listed
    if (!listed) return fetched
    return {
      ...fetched,
      status: listed.status,
      endedAt: listed.endedAt ?? fetched.endedAt,
      summary: listed.summary ?? fetched.summary,
      thresholds: listed.thresholds ?? fetched.thresholds,
      error: listed.error ?? fetched.error,
      // Injection and revert times arrive with status events while the run is live.
      scenario: listed.scenario ?? fetched.scenario,
    }
  }, [fetched, listed])
  const samples = useMemo(() => mergeSamples(fetched?.timeline ?? EMPTY, live), [fetched, live])
  return { run, samples, loading: query.isPending && !!runId }
}
