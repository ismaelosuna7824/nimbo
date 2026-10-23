import { useEffect } from "react"
import { Gauge, Plug } from "lucide-react"
import { toast } from "sonner"
import { useArcadeStore } from "@/components/arcade/arcade-store"
import { Button } from "@/components/ui/button"
import { api } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"
import { CopyButton, MethodBadge } from "./bits"
import { firstHttpEndpoint, openEndpoints } from "./use-endpoints"

/**
 * After an apply (compose up in a Docker workspace) this window watched
 * succeed, tells the user where their stuff lives: the first HTTP address
 * (copyable) and a way to the Endpoints and Load test tabs. In Arcade the
 * mascot celebrates too.
 */
export function useDeployToast() {
  useEffect(
    () =>
      useWorkflowStore.subscribe((state, prev) => {
        const wf = state.workflow
        if (!wf || wf.id !== prev.workflow?.id || state.runs === prev.runs) return
        const finished = state.runs.find((r) => {
          if ((r.action !== "apply" && r.action !== "up") || r.status !== "succeeded") return false
          const before = prev.runs.find((p) => p.id === r.id)?.status
          return before === "queued" || before === "running"
        })
        if (!finished) return
        useArcadeStore.getState().celebrate()
        void announce(wf.id, wf.kind ?? "")
      }),
    [],
  )
}

const READY_TITLE: Record<string, { many: string; one: string }> = {
  docker: { many: "Containers are up — your services are ready", one: "Containers are up — your service is live" },
  kubernetes: { many: "Deployed to your cluster — your workloads are ready", one: "Deployed to your cluster — your service is live" },
}

async function announce(workflowId: string, kind: string) {
  const title = READY_TITLE[kind] ?? { many: "Deployed — your resources are ready", one: "Deployed — your API is live" }
  const info = await api.endpoints(workflowId).catch(() => null)
  if (!info || useWorkflowStore.getState().workflow?.id !== workflowId || info.resources.length === 0) return
  const endpoint = firstHttpEndpoint(info.resources)
  const owner = endpoint && info.resources.find((r) => r.endpoints?.includes(endpoint))
  if (!endpoint) {
    toast.success(title.many, {
      description: "See how to use each one: addresses, code snippets and a Try it button.",
      duration: 10_000,
      action: { label: "Open Endpoints", onClick: () => openEndpoints() },
    })
    return
  }
  const id = toast.success(title.one, {
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
