import { useEffect } from "react"
import { Gauge, Plug } from "lucide-react"
import { toast } from "sonner"
import { useArcadeStore } from "@/components/arcade/arcade-store"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api/client"
import type { Run } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { CopyButton, MethodBadge } from "@/features/panels/endpoints/bits"
import { firstHttpEndpoint, openEndpoints } from "@/features/panels/endpoints/use-endpoints"
import { deployedCount, firstErrorLine, isActive, RUN_LABEL } from "./deploy-status"
import { useUiStore } from "./ui-store"

/**
 * Says how the runs this window started (see watchRun) ended, so a deploy started from the toolbar doesn't need the
 * Deploy tab to be seen: a Deploy that succeeded tells where things live; a run that failed gives its first error
 * and a way to its log; a Terraform apply that needs confirmation opens the Deploy confirmation dialog. Canceled
 * runs end quietly; a plan has its own toast (features/plan/plan-toast).
 */
export function useRunNotices() {
  useEffect(() => {
    useUiStore.setState({ watchedRuns: [] })
    const check = () => {
      const { watchedRuns } = useUiStore.getState()
      if (watchedRuns.length === 0) return
      const ended = useWorkflowStore.getState().runs.filter((r) => watchedRuns.includes(r.id) && !isActive(r.status))
      if (ended.length === 0) return
      useUiStore.setState({ watchedRuns: watchedRuns.filter((id) => !ended.some((r) => r.id === id)) })
      for (const run of ended) notify(run)
    }
    const offRuns = useWorkflowStore.subscribe((s, prev) => {
      // Another workflow opened: its runs are not the ones this window started.
      if (s.workflow?.id !== prev.workflow?.id) useUiStore.setState({ watchedRuns: [] })
      else if (s.runs !== prev.runs) check()
    })
    // A run's end can arrive before the request that started it returns.
    const offWatch = useUiStore.subscribe((s, prev) => {
      if (s.watchedRuns.length > prev.watchedRuns.length) check()
    })
    return () => {
      offRuns()
      offWatch()
    }
  }, [])
}

function notify(run: Run) {
  const wf = useWorkflowStore.getState().workflow
  if (!wf || wf.id !== run.workflowId) return
  if (run.status === "needs_confirmation") {
    if (run.action === "apply") useUiStore.getState().setConfirmDeployRun(run.id)
    return
  }
  if (run.status === "failed") {
    void announceFailure(run)
    return
  }
  if (run.status === "succeeded" && (run.action === "apply" || run.action === "up")) {
    useArcadeStore.getState().celebrate()
    void announceDeploy(run.workflowId, wf.kind ?? "")
  }
}

/** The first error line of a failed run and a way to its whole log. */
async function announceFailure(run: Run) {
  // The stored log is complete; streamed lines can still be waiting to be flushed when the status arrives.
  const stored = await api.run(run.id).then((r) => r.log).catch(() => undefined)
  const log = stored ?? useWorkflowStore.getState().runLogs[run.id]
  if (useWorkflowStore.getState().workflow?.id !== run.workflowId) return
  toast.error(`${RUN_LABEL[run.action]} failed`, {
    description: firstErrorLine(log, run.error) || undefined,
    duration: 12_000,
    action: { label: "View logs", onClick: () => useWorkflowStore.getState().showRun(run.id) },
  })
}

const DEPLOYED_TITLE: Record<string, string> = {
  docker: "Containers are up",
  kubernetes: "Deployed to your cluster",
}

/**
 * A Deploy succeeded: how many resources it left (Terraform) and where they live: the first HTTP address
 * (copyable) with the Endpoints and Load test tabs one click away.
 */
async function announceDeploy(workflowId: string, kind: string) {
  const terraform = kind !== "docker" && kind !== "kubernetes"
  const [info, state] = await Promise.all([
    api.endpoints(workflowId).catch(() => null),
    terraform ? api.state(workflowId).catch(() => null) : null,
  ])
  if (useWorkflowStore.getState().workflow?.id !== workflowId) return
  const n = deployedCount(state)
  const title = DEPLOYED_TITLE[kind] ?? (n > 0 ? `Deployed: ${n} resource${n === 1 ? "" : "s"}` : "Deployed")
  const resources = info?.resources ?? []
  const endpoint = firstHttpEndpoint(resources)
  if (!endpoint) {
    toast.success(title, {
      duration: 8_000,
      ...(resources.length > 0 && {
        description: "See how to use each one: addresses, code snippets and a Try it button.",
        action: { label: "Open endpoints", onClick: () => openEndpoints() },
      }),
    })
    return
  }
  const owner = resources.find((r) => r.endpoints?.includes(endpoint))
  const id = toast.success(title, {
    description: (
      <span className="flex flex-col gap-2">
        <span className="flex min-w-0 items-center gap-1.5 rounded-md bg-muted/60 py-0.5 pr-0.5 pl-1.5">
          <MethodBadge method={endpoint.method} />
          <span className="min-w-0 flex-1 truncate font-mono text-[11px]" title={endpoint.url}>
            {endpoint.url}
          </span>
          <CopyButton value={endpoint.url} label="Copy URL" />
        </span>
        <span className="flex gap-1.5">
          <Button
            size="xs"
            onClick={() => {
              toast.dismiss(id)
              openEndpoints(owner?.address)
            }}
          >
            <Plug /> Open endpoints
          </Button>
          <Button
            size="xs"
            variant="outline"
            onClick={() => {
              toast.dismiss(id)
              useWorkflowStore.getState().showBottom("loadtest")
            }}
          >
            <Gauge /> Load test
          </Button>
        </span>
      </span>
    ),
    duration: 15_000,
  })
}
