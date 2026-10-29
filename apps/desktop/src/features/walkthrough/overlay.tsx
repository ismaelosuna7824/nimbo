import { ChevronDownIcon, MapIcon, RouteIcon } from "lucide-react"
import { lazy, Suspense, useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkflowStore } from "@/state/workflow-store"
import type { LayerProps } from "./layer"
import { closeWalkthrough, startWalkthrough, useWalkthrough, useWalkthroughMode, useWalkthroughOpen } from "./store"

// The paths, the tour, captions and player load the first time someone starts one.
const WalkthroughLayer = lazy(() => import("./layer"))

/** Frosted canvas chrome (view switch, Tidy up). */
const CHROME = "bg-card/80 shadow-md backdrop-blur-md dark:bg-card/80"

/** The walkthrough inside a canvas' React Flow while it is open on the open workflow. */
export function WalkthroughOverlay(props: LayerProps) {
  const open = useWalkthroughOpen()
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const openOn = useWalkthrough((s) => s.workflowId)
  // Another workflow opened: the walkthrough belonged to the previous one.
  useEffect(() => {
    if (openOn && openOn !== workflowId) closeWalkthrough()
  }, [openOn, workflowId])
  if (!open) return null
  return (
    <Suspense fallback={null}>
      <WalkthroughLayer {...props} />
    </Suspense>
  )
}

/** Architecture toolbar button: starts (or restarts) "Follow a request". */
export function FollowRequestButton() {
  const mode = useWalkthroughMode()
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="outline" size="sm" className={CHROME} aria-pressed={mode === "request"} onClick={() => startWalkthrough("request")}>
          <RouteIcon />
          Follow a request
        </Button>
      </TooltipTrigger>
      <TooltipContent>See step by step which way a request takes through the design</TooltipContent>
    </Tooltip>
  )
}

/** Whether "Follow a request" can start: still checking, nothing reaches the design, or something does. */
type EntryState = "checking" | "none" | "ok"

/**
 * Build toolbar button: "Guided tour" starts the infrastructure tour; its menu also offers "Follow a request"
 * (disabled, with the reason, when nothing reaches the design).
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
      .then((m) => m.hasRequestEntry(workflow, version))
      .then((ok) => setEntry(ok ? "ok" : "none"))
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
          {entry === "none" ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <div>
                  <DropdownMenuItem disabled>
                    <RouteIcon />
                    Follow a request
                  </DropdownMenuItem>
                </div>
              </TooltipTrigger>
              <TooltipContent side="left">This design has no entry point yet: add a public front door (an API gateway, a load balancer, external ingress or a published port)</TooltipContent>
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
