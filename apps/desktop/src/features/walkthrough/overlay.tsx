import { ChevronDownIcon, MapIcon, RouteIcon } from "lucide-react"
import { lazy, Suspense, useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkflowStore } from "@/state/workflow-store"
import { closeWalkthrough, startWalkthrough, useWalkthrough, useWalkthroughMode } from "./store"

// The paths, the tour, captions and player load the first time someone starts one.
const WalkthroughLayer = lazy(() => import("./layer"))

/** Frosted canvas chrome (view switch, Tidy up). */
const CHROME = "bg-card/80 shadow-md backdrop-blur-md dark:bg-card/80"

/** The walkthrough inside the Build canvas' React Flow while it is open on the open workflow. */
export function WalkthroughOverlay() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const openOn = useWalkthrough((s) => s.workflowId)
  // Another workflow opened: the walkthrough belonged to the previous one.
  useEffect(() => {
    if (openOn && openOn !== workflowId) closeWalkthrough()
  }, [openOn, workflowId])
  if (!openOn || openOn !== workflowId) return null
  return (
    <Suspense fallback={null}>
      <WalkthroughLayer />
    </Suspense>
  )
}

/** Whether "Follow a request" can start: still checking, nothing handles requests (and why), or something does. */
type EntryState = "checking" | "ok" | { none: string }

/**
 * Build toolbar button: "Guided tour" starts the infrastructure tour; its menu also offers "Follow a request"
 * (disabled, with the reason said for the design, when nothing receives requests).
 */
export function GuidedTourButton() {
  const mode = useWalkthroughMode()
  const [entry, setEntry] = useState<EntryState>("checking")
  const check = (open: boolean) => {
    if (!open) return
    const { workflow, version } = useWorkflowStore.getState()
    if (!workflow) return
    setEntry("checking")
    void import("./entry-check")
      .then((m) => m.checkRequestEntry(workflow, version))
      .then((c) => setEntry(c.ok ? "ok" : { none: c.reason }))
      .catch(() => setEntry("ok"))
  }
  return (
    <div className="flex items-center">
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="outline" size="sm" className={`${CHROME} rounded-r-none`} aria-pressed={mode === "tour"} onClick={() => startWalkthrough("tour")}>
            <MapIcon />
            Guided tour
          </Button>
        </TooltipTrigger>
        <TooltipContent>Tour the infrastructure from its foundations to the apps, one layer at a time</TooltipContent>
      </Tooltip>
      <DropdownMenu onOpenChange={check}>
        <DropdownMenuTrigger asChild>
          <Button variant="outline" size="icon-sm" className={`${CHROME} -ml-px rounded-l-none`} aria-label="More guided tours">
            <ChevronDownIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-60">
          <DropdownMenuItem onSelect={() => startWalkthrough("tour")}>
            <MapIcon />
            Tour the infrastructure
          </DropdownMenuItem>
          {typeof entry === "object" ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <div>
                  <DropdownMenuItem disabled>
                    <RouteIcon />
                    Follow a request
                  </DropdownMenuItem>
                </div>
              </TooltipTrigger>
              <TooltipContent side="left" className="max-w-80">
                {entry.none}
              </TooltipContent>
            </Tooltip>
          ) : (
            <DropdownMenuItem disabled={entry === "checking"} onSelect={() => startWalkthrough("request")}>
              <RouteIcon />
              Follow a request
            </DropdownMenuItem>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}
