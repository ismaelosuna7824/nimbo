import { BotIcon, BoxIcon, DownloadCloudIcon, HeartPulseIcon, KeyRoundIcon, LayersIcon, PlayIcon, RocketIcon, SparklesIcon } from "lucide-react"
import type { SetupCheck, SetupCheckStatus, SetupStatus } from "@/lib/api/types"
import type { Platform } from "./platforms"
import type { StepId } from "./store"

export interface StepDef {
  id: StepId
  /** Rail label and page title. */
  label: string
  title: string
  icon: typeof BoxIcon
  /** The user may continue without completing it. */
  optional?: boolean
  /** Setup checks this step is about; the step is done when they are ok. */
  checks?: string[]
  /** Only on these operating systems. */
  platforms?: Platform[]
  /** Its instructions differ per OS (shown for the previewed OS by "Using a different OS?"). */
  os?: true
}

const ALL_STEPS: StepDef[] = [
  { id: "welcome", label: "Welcome", title: "Welcome to Nimbo", icon: SparklesIcon },
  { id: "wsl", label: "Turn on WSL 2", title: "Turn on WSL 2", icon: LayersIcon, checks: ["wsl"], platforms: ["windows"], os: true },
  { id: "docker", label: "Install Docker", title: "Install Docker", icon: BoxIcon, checks: ["docker-cli"], os: true },
  { id: "start", label: "Start Docker", title: "Start Docker", icon: PlayIcon, checks: ["docker-daemon"], os: true },
  {
    id: "permissions",
    label: "Allow Docker access",
    title: "Let your user run Docker",
    icon: KeyRoundIcon,
    checks: ["docker-permissions"],
    os: true,
    platforms: ["linux"],
  },
  { id: "images", label: "Download the local cloud", title: "Download the local cloud", icon: DownloadCloudIcon, checks: ["images", "docker-socket"] },
  { id: "health", label: "Health check", title: "Health check", icon: HeartPulseIcon },
  { id: "agents", label: "Connect your AI agent", title: "Connect your AI agent", icon: BotIcon, optional: true },
  { id: "try", label: "Try it", title: "Try it", icon: RocketIcon },
]

/** The wizard's steps on an OS (WSL 2 only on Windows, Docker group only on Linux). */
export function stepsFor(platform: Platform): StepDef[] {
  return ALL_STEPS.filter((s) => !s.platforms || s.platforms.includes(platform))
}

const SEVERITY: Record<SetupCheckStatus, number> = { ok: 0, warning: 1, missing: 2, error: 3 }

export function findCheck(status: SetupStatus | undefined, id: string): SetupCheck | undefined {
  return status?.checks.find((c) => c.id === id)
}

/** Worst status among the step's checks; undefined when it has none (or none reported yet). */
export function stepStatus(step: StepDef, status: SetupStatus | undefined): SetupCheckStatus | undefined {
  if (!status) return undefined
  const list =
    step.id === "health"
      ? status.ready
        ? status.checks.map((c) => c.status).filter((s) => s === "ok" || s === "warning")
        : status.checks.filter((c) => c.required).map((c) => c.status)
      : (step.checks ?? []).flatMap((id) => findCheck(status, id)?.status ?? [])
  if (list.length === 0) return undefined
  return list.reduce<SetupCheckStatus>((w, s) => (SEVERITY[s] > SEVERITY[w] ? s : w), "ok")
}

/** Whether Next is allowed: informational/optional steps always, check steps once their checks pass. */
export function stepDone(step: StepDef, status: SetupStatus | undefined): boolean {
  if (step.optional || !step.checks) return step.id !== "health" || !!status?.ready
  if (!status) return false
  const s = stepStatus(step, status)
  // Warnings don't block (e.g. low memory); a check this daemon doesn't report has nothing to do.
  return s === undefined || s === "ok" || s === "warning"
}

/** The step a returning user should land on: the first one that isn't done. */
export function firstOpenStep(steps: StepDef[], status: SetupStatus | undefined): StepId {
  return steps.find((s) => !stepDone(s, status))?.id ?? "health"
}

/** The step that helps with a given check (health list "Go to" links). */
export function stepForCheck(steps: StepDef[], checkId: string): StepDef | undefined {
  return steps.find((s) => s.checks?.includes(checkId))
}
