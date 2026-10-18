import { createContext } from "react"
import type { LoadSample, LoadTestRun } from "@/lib/api/types"

/**
 * The runs shown by results and history views: a workflow's runs (workflow store)
 * or a connection's runs (connections store), each with live SSE samples per run id.
 */
export interface LoadRunsSource {
  runs: LoadTestRun[]
  samples: Record<string, LoadSample[]>
}

export const LoadRunsContext = createContext<LoadRunsSource>({ runs: [], samples: {} })
