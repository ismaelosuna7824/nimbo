import { RouteIcon } from "lucide-react"
import { lazy, Suspense, useEffect } from "react"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useWorkflowStore } from "@/state/workflow-store"
import type { LayerProps } from "./layer"
import { closeWalkthrough, startWalkthrough, useWalkthrough, useWalkthroughOpen } from "./store"

// The paths, captions and player load the first time someone follows a request.
const WalkthroughLayer = lazy(() => import("./layer"))

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

/** Canvas toolbar button (frosted canvas chrome): starts, or restarts, the walkthrough. */
export function FollowRequestButton() {
  const open = useWalkthroughOpen()
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="outline" size="sm" className="bg-card/80 shadow-md backdrop-blur-md dark:bg-card/80" aria-pressed={open} onClick={startWalkthrough}>
          <RouteIcon />
          Follow a request
        </Button>
      </TooltipTrigger>
      <TooltipContent>See step by step which way a request takes through the design</TooltipContent>
    </Tooltip>
  )
}
