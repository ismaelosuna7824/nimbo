import { toast } from "sonner"
import { create } from "zustand"
import type { LoadTestRun } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { revealLoadTest } from "@/features/panels/loadtest/reveal"
import { SCENARIO_LABEL } from "@/features/panels/loadtest/scenario-result"
import { LINGER_MS } from "./traffic"

/**
 * What the canvas needs to know about load-test runs beyond their status, kept as runs stream in (not while
 * rendering): when each run's traffic started, and which runs ended moments ago (the canvas keeps their final numbers
 * for LINGER_MS).
 */
interface RunClock {
  /**
   * When the run's traffic started (ms; the samples' t = 0), estimated as a sample's arrival minus its t: network
   * delay only makes a sample arrive later, so the earliest estimate is kept.
   */
  loadStart: Record<string, number>
  /** Runs that ended under LINGER_MS ago. */
  lingering: Record<string, true>
}

export const useRunClock = create<RunClock>(() => ({ loadStart: {}, lingering: {} }))

useWorkflowStore.subscribe((s, prev) => {
  if (s.loadSamples !== prev.loadSamples) {
    const now = Date.now()
    const starts = useRunClock.getState().loadStart
    let next: Record<string, number> | undefined
    for (const [runId, samples] of Object.entries(s.loadSamples)) {
      const last = samples[samples.length - 1]
      if (!last || samples === prev.loadSamples[runId]) continue
      const at = now - last.t * 1000
      if (starts[runId] === undefined || at < starts[runId]) (next ??= { ...starts })[runId] = at
    }
    if (next) useRunClock.setState({ loadStart: next })
  }
  if (s.loadTests !== prev.loadTests) {
    for (const r of s.loadTests) {
      if (r.status === "running" || prev.loadTests.find((p) => p.id === r.id)?.status !== "running") continue
      useRunClock.setState((c) => ({ lingering: { ...c.lingering, [r.id]: true } }))
      window.setTimeout(() => {
        useRunClock.setState((c) => {
          const lingering = { ...c.lingering }
          delete lingering[r.id]
          return { lingering }
        })
      }, LINGER_MS)
      if (r.spec.scenario) verdictToast(r)
    }
  }
})

/** How a failure test went, with a link to its full results (the run just ended). */
function verdictToast(r: LoadTestRun) {
  const scenario = r.spec.scenario!
  const title = `${SCENARIO_LABEL[scenario.id]}${r.scenario?.targetName ? ` on ${r.scenario.targetName}` : ""}`
  const opts = { description: r.scenario?.message || undefined, action: { label: "View results", onClick: () => revealLoadTest(r.id) } }
  const v = r.scenario?.verdict
  if (!r.scenario) toast.info(`${title}: stopped before the fault`, { ...opts, description: "Nothing was broken." })
  else if (v === "survived") toast.success(`Survived: ${title}`, opts)
  else if (v === "degraded") toast.warning(`Degraded: ${title}`, opts)
  else if (v === "failed") toast.error(`Failed: ${title}`, opts)
  else if (v === "inconclusive") toast.info(`Not assessed: ${title}`, opts)
  else toast.info(`${title}: stopped before the end, no verdict`, opts)
}
