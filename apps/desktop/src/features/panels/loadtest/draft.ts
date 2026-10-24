import type { AWSTarget, HTTPTarget, LoadProfile, ScenarioSpec, Threshold } from "@/lib/api/types"
import { INITIAL_CONNECTION_TARGET, type ConnectionTargetDraft } from "@/features/connections/target-draft"
import { DEFAULT_THRESHOLDS } from "./metrics"

/** Load test form state, kept by the tab so switching views does not lose it. */
export interface LoadDraft {
  /** Workflow tab only: where the target comes from ("connection" = a saved connection). */
  source: "suggested" | "http" | "aws" | "connection"
  suggestedKey: string | null
  http: HTTPTarget & { headersText: string }
  aws: AWSTarget
  connection: ConnectionTargetDraft
  profile: LoadProfile | null
  thresholds: Threshold[]
  timeoutMs: number
  maxWorkers?: number
  /** Workflow tab only: the incident to inject. atSec 0 = not timed yet (the picker applies defaults). */
  scenario: ScenarioSpec | null
}

export const INITIAL_DRAFT: LoadDraft = {
  source: "suggested",
  suggestedKey: null,
  http: { method: "GET", url: "", headersText: "", body: "" },
  aws: { operation: "sqs:SendMessage", resource: "", payload: "" },
  connection: INITIAL_CONNECTION_TARGET,
  profile: null,
  thresholds: DEFAULT_THRESHOLDS,
  timeoutMs: 5000,
  scenario: null,
}
