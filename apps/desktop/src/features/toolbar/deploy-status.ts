import type { DeployedState, LogLine, Run, RunAction, RunStatus } from "@/lib/api/types"

/** What each run action is called in messages ("Plan failed", "Deploy couldn't start"). */
export const RUN_LABEL: Record<RunAction, string> = {
  validate: "Validation",
  plan: "Plan",
  apply: "Deploy",
  destroy: "Destroy",
  up: "Deploy",
  down: "Destroy",
  build: "Build",
  pull: "Pull",
  restart: "Restart",
  "cluster-stop": "Stop cluster",
  "cluster-start": "Start cluster",
  "cluster-delete": "Delete cluster",
}

/** The Deploy button's label while a run of each action is queued or running. */
export const RUNNING_LABEL: Record<RunAction, string> = {
  validate: "Validating…",
  plan: "Planning…",
  apply: "Deploying…",
  destroy: "Destroying…",
  up: "Deploying…",
  down: "Removing…",
  build: "Building…",
  pull: "Pulling…",
  restart: "Restarting…",
  "cluster-stop": "Stopping cluster…",
  "cluster-start": "Starting cluster…",
  "cluster-delete": "Deleting cluster…",
}

/** Runs that change what is deployed: Deploy and Destroy in every kind of workspace. */
const DEPLOY_ACTIONS: Partial<Record<RunAction, true>> = { apply: true, up: true, destroy: true, down: true }
const isDeploy = (a: RunAction) => a === "apply" || a === "up"

export function isActive(status: RunStatus): boolean {
  return status === "queued" || status === "running"
}

/** The run in progress, if any (runs are newest first). */
export function activeRun(runs: Run[]): Run | undefined {
  return runs.find((r) => isActive(r.status))
}

/**
 * The newest Deploy or Destroy that ended. needs_confirmation runs are skipped: nothing was applied (the confirmation
 * dialog and the Deploy tab tell about them), so what is deployed is still what the run before left.
 */
export function lastDeployRun(runs: Run[]): Run | undefined {
  return runs.find((r) => DEPLOY_ACTIONS[r.action] && !isActive(r.status) && r.status !== "needs_confirmation")
}

/** "just now", "2m ago", "3h ago", "4d ago"; empty when the time is unknown. */
export function shortAgo(iso: string | undefined, now: number): string {
  const t = iso ? Date.parse(iso) : Number.NaN
  if (Number.isNaN(t)) return ""
  const s = Math.max(0, (now - t) / 1000)
  if (s < 60) return "just now"
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ago`
  return `${Math.floor(h / 24)}d ago`
}

export type DeployTone = "ok" | "failed" | "muted"

/**
 * The toolbar's deploy status for the last Deploy/Destroy: green only for a deploy that succeeded, red only for one
 * that failed; a destroy or a canceled run is plain information.
 */
export function deployStatus(run: Run, now: number): { label: string; tone: DeployTone } {
  const ago = shortAgo(run.endedAt ?? run.createdAt, now)
  const deploy = isDeploy(run.action)
  const name = deploy ? "Deploy" : run.action === "down" ? "Remove" : "Destroy"
  if (run.status === "failed") return { label: `${name} failed`, tone: "failed" }
  if (run.status === "canceled") return { label: `${name} canceled`, tone: "muted" }
  const done = deploy ? "Deployed" : run.action === "down" ? "Removed" : "Destroyed"
  return { label: ago ? `${done} ${ago}` : done, tone: deploy ? "ok" : "muted" }
}

/** Runs that went from queued/running in `prev` to ended in `next` (status events as they arrive). */
export function endedRuns(prev: Run[], next: Run[]): Run[] {
  if (prev === next) return []
  return next.filter((r) => {
    if (isActive(r.status)) return false
    const before = prev.find((p) => p.id === r.id)?.status
    return before !== undefined && isActive(before)
  })
}

/** A Deploy that just succeeded (the Deploy button shows it for a moment). */
export function justDeployed(prev: Run[], next: Run[]): Run | undefined {
  return endedRuns(prev, next).find((r) => isDeploy(r.action) && r.status === "succeeded")
}

const MAX_ERROR = 200

/**
 * The line that says why a run failed: the first "Error: …" line of its log (Terraform, docker and kubectl all print
 * one), else the first line on stderr, else the run's own error. Without the "Error:" prefix, cut to one short line.
 */
export function firstErrorLine(log: LogLine[] | undefined, fallback?: string): string {
  const lines = (log ?? []).map((l) => ({ stream: l.stream, text: l.text.trim() })).filter((l) => l.text)
  const error = lines.find((l) => /^error\b/i.test(l.text))
  const text = error ? error.text.replace(/^error\b:?\s*/i, "") : (lines.find((l) => l.stream === "stderr")?.text ?? fallback ?? "")
  return text.length > MAX_ERROR ? `${text.slice(0, MAX_ERROR - 1)}…` : text
}

/** Managed resources in the deployed state (data sources are reads, not things Deploy created). */
export function deployedCount(state: DeployedState | null | undefined): number {
  return state?.resources.filter((r) => r.mode === "managed").length ?? 0
}
