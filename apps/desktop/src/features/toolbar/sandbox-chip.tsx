import { BoxIcon, PlayIcon, RotateCcwIcon, SquareIcon } from "lucide-react"
import { useState } from "react"
import { CLOUD_META } from "@/components/cloud-meta"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import type { SandboxState } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { ComposeChip } from "@/features/docker/deploy/compose-chip"
import { KubeChip } from "@/features/kubernetes/deploy/kube-chip"
import { sandboxAction } from "./actions"

const STATE: Record<SandboxState, { label: string; dot: string }> = {
  absent: { label: "Sandbox off", dot: "bg-muted-foreground/50" },
  starting: { label: "Starting…", dot: "bg-amber-500" },
  running: { label: "Sandbox running", dot: "bg-emerald-500" },
  stopped: { label: "Sandbox stopped", dot: "bg-muted-foreground/50" },
  error: { label: "Sandbox error", dot: "bg-destructive" },
}

/** Runtime chip of the toolbar: the Floci sandbox, the containers of a Docker workspace or a Kubernetes cluster. */
export function SandboxChip() {
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  return kind === "docker" ? <ComposeChip /> : kind === "kubernetes" ? <KubeChip /> : <FlociSandboxChip />
}

function FlociSandboxChip() {
  const sandbox = useWorkflowStore((s) => s.sandbox)
  const [busy, setBusy] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)
  const state = sandbox?.state ?? "absent"
  const meta = STATE[state]
  const run = async (action: "start" | "stop" | "reset") => {
    setBusy(true)
    await sandboxAction(action)
    setBusy(false)
    setConfirmReset(false)
  }
  const running = state === "running" || state === "starting"

  return (
    <Popover onOpenChange={() => setConfirmReset(false)}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="gap-2">
          {state === "starting" || busy ? <Spinner className="size-3" /> : <span className={cn("size-2 rounded-full", meta.dot)} />}
          {meta.label}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80" align="center">
        <div className="flex flex-col gap-3">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <BoxIcon className="size-4" />
              Local sandbox
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              A private set of Floci cloud emulators in Docker, just for this workflow. Deploying starts it automatically.
            </p>
          </div>
          {sandbox?.message && (
            <p className={cn("rounded-md bg-muted px-2 py-1.5 text-xs", state === "error" && "bg-destructive/10 text-destructive")}>
              {sandbox.message}
            </p>
          )}
          {(sandbox?.emulators ?? []).length > 0 && (
            <ul className="flex flex-col gap-1.5">
              {sandbox!.emulators.map((e) => (
                <li key={e.container} className="flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                  <span className={cn("size-1.5 rounded-full", STATE[e.state].dot)} />
                  <span className={cn("font-medium", CLOUD_META[e.cloud].text)}>{CLOUD_META[e.cloud].label}</span>
                  <span className="truncate font-mono text-muted-foreground">{e.url || e.image}</span>
                  {e.version && <span className="ml-auto text-muted-foreground">v{e.version}</span>}
                </li>
              ))}
            </ul>
          )}
          <div className="flex gap-2">
            {running ? (
              <Button size="sm" variant="outline" disabled={busy} onClick={() => void run("stop")}>
                <SquareIcon />
                Stop
              </Button>
            ) : (
              <Button size="sm" disabled={busy} onClick={() => void run("start")}>
                <PlayIcon />
                Start
              </Button>
            )}
            <Button
              size="sm"
              variant={confirmReset ? "destructive" : "ghost"}
              disabled={busy || state === "absent"}
              onClick={() => (confirmReset ? void run("reset") : setConfirmReset(true))}
            >
              <RotateCcwIcon />
              {confirmReset ? "Click again to wipe data" : "Reset"}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
