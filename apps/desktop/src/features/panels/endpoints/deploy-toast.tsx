import { useEffect } from "react"
import { Gauge, Plug } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"
import { CopyButton, MethodBadge } from "./bits"
import { firstHttpEndpoint, openEndpoints } from "./use-endpoints"

/**
 * After an apply this window watched succeed, tells the user where their
 * stuff lives: the first HTTP address (copyable) and a way to the Endpoints
 * and Load test tabs.
 */
export function useDeployToast() {
  useEffect(
    () =>
      useWorkflowStore.subscribe((state, prev) => {
        const wf = state.workflow
        if (!wf || wf.id !== prev.workflow?.id || state.runs === prev.runs) return
        const finished = state.runs.find((r) => {
          if (r.action !== "apply" || r.status !== "succeeded") return false
          const before = prev.runs.find((p) => p.id === r.id)?.status
          return before === "queued" || before === "running"
        })
        if (finished) void announce(wf.id)
      }),
    [],
  )
}

async function announce(workflowId: string) {
  const info = await api.endpoints(workflowId).catch(() => null)
  if (!info || useWorkflowStore.getState().workflow?.id !== workflowId || info.resources.length === 0) return
  const endpoint = firstHttpEndpoint(info.resources)
  const owner = endpoint && info.resources.find((r) => r.endpoints?.includes(endpoint))
  if (!endpoint) {
    toast.success("Deployed — your resources are ready", {
      description: "See how to use each one: addresses, code snippets and a Try it button.",
      duration: 10_000,
      action: { label: "Open Endpoints", onClick: () => openEndpoints() },
    })
    return
  }
  const id = toast.success("Deployed — your API is live", {
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
            <Plug /> Open Endpoints
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
