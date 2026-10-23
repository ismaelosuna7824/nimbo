import { useState } from "react"
import { Boxes, PlayIcon, PowerIcon, ScrollText } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Spinner } from "@/components/ui/spinner"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { runAction, useRunBusy } from "@/features/toolbar/actions"
import { COMPOSE_STATE, composeChipSummary } from "./compose-model"
import { HealthBadge } from "./compose-services"

/** Toolbar chip of a Docker workspace: the compose project's state, its services and Up/Down. */
export function ComposeChip() {
  const status = useWorkflowStore((s) => s.compose)
  const busy = useRunBusy()
  const [starting, setStarting] = useState(false)
  const summary = composeChipSummary(status)
  const meta = COMPOSE_STATE[summary.state]
  // Down also removes stopped containers, so it applies whenever the project has any.
  const exists = summary.state !== "absent"
  const run = async (action: "up" | "down") => {
    setStarting(true)
    try {
      await runAction(action)
    } finally {
      setStarting(false)
    }
  }

  return (
    <Popover>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="gap-2" data-testid="compose-chip" data-state={summary.state}>
          {summary.state === "starting" || busy ? (
            <Spinner className="size-3" />
          ) : (
            <span className={cn("size-2 rounded-full", meta.dot)} />
          )}
          {summary.label}
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80" align="center">
        <div className="flex flex-col gap-3">
          <div>
            <div className="flex items-center gap-2 text-sm font-medium">
              <Boxes className="size-4" />
              Local containers
            </div>
            <p className="mt-1 text-xs text-muted-foreground">
              This workspace runs with docker compose on your machine
              {status?.project ? (
                <>
                  {" "}
                  as project <span className="font-mono">{status.project}</span>
                </>
              ) : null}
              . Deploy starts it.
            </p>
          </div>
          {status?.message && (
            <p
              className={cn(
                "rounded-md bg-muted px-2 py-1.5 text-xs",
                summary.state === "error" && "bg-destructive/10 text-destructive",
              )}
            >
              {status.message}
            </p>
          )}
          {(status?.services.length ?? 0) > 0 && (
            <ul className="flex max-h-56 flex-col gap-1.5 overflow-auto">
              {status!.services.map((s) => (
                <li key={s.name} className="flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                  <span className={cn("size-1.5 shrink-0 rounded-full", COMPOSE_STATE[s.state].dot)} />
                  <span className="truncate font-medium">{s.name}</span>
                  <span className="text-muted-foreground">{COMPOSE_STATE[s.state].label}</span>
                  {s.health && (
                    <span className="ml-auto">
                      <HealthBadge health={s.health} />
                    </span>
                  )}
                </li>
              ))}
            </ul>
          )}
          <div className="flex gap-2">
            <Button size="sm" disabled={busy || starting} onClick={() => void run("up")}>
              {starting ? <Spinner /> : <PlayIcon />}
              Up
            </Button>
            <Button size="sm" variant="outline" disabled={busy || starting || !exists} onClick={() => void run("down")}>
              <PowerIcon />
              Down
            </Button>
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => useWorkflowStore.getState().showBottom("deploy")}
            >
              <ScrollText />
              Details
            </Button>
          </div>
          <p className="-mt-1 text-[11px] text-muted-foreground">Down keeps your volumes (data).</p>
        </div>
      </PopoverContent>
    </Popover>
  )
}
