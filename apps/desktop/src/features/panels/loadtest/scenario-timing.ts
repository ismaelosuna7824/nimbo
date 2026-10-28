import type { LoadProfile, ScenarioOption, ScenarioSpec } from "@/lib/api/types"
import { totalDuration } from "./metrics"

/** Seconds of normal traffic before the fault (the verdict's baseline) and after it (recovery). */
export const BASELINE_SEC = 10
export const RECOVERY_SEC = 15
/** The daemon's scenario.MinAtSec. */
export const MIN_AT_SEC = 5

/** What is wrong with the scenario's timing for this traffic shape, or null when it can run. */
export function scenarioProblem(spec: ScenarioSpec | null, profile: LoadProfile | null): string | null {
  if (!spec || !profile) return null
  const total = totalDuration(profile.stages)
  if (spec.atSec < MIN_AT_SEC) return `Start the incident at ${MIN_AT_SEC} s or later, so there is normal traffic to compare with.`
  if (spec.durationSec < 1) return "The incident needs a duration."
  if (spec.atSec + spec.durationSec > total)
    return `The incident ends at ${spec.atSec + spec.durationSec} s but the test lasts ${total} s: start it earlier or pick a longer traffic shape.`
  return null
}

/** Default timing: after a baseline, leaving time to watch the recovery. */
export function defaultTiming(opt: ScenarioOption, total: number) {
  const atSec = Math.max(MIN_AT_SEC, Math.min(BASELINE_SEC, Math.floor(total / 3)), Math.round(total * 0.25))
  const durationSec = Math.max(1, Math.min(opt.defaultDurationSec, total - atSec - RECOVERY_SEC))
  return { atSec, durationSec }
}

/** The scenario with its default parameters and timing for the traffic shape (on `target`, else its first target). */
export function initialSpec(opt: ScenarioOption, profile: LoadProfile | null, target?: string): ScenarioSpec {
  const params: Record<string, string> = {}
  for (const p of opt.params ?? []) params[p.name] = String(p.default)
  const timing = profile ? defaultTiming(opt, totalDuration(profile.stages)) : { atSec: 0, durationSec: 0 }
  const chosen = opt.targets.find((t) => t.id === target) ?? opt.targets[0]
  return { id: opt.id, target: opt.needsTarget ? (chosen?.id ?? "") : "", params, ...timing }
}
