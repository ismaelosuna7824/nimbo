import { useEffect, useMemo } from "react"
import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api/client"
import type { Connection, LoadTarget } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { describeExternal, type ExternalTarget } from "@/features/connections/safety"
import { useConnections, useConnectionsStore } from "@/features/connections/store"
import { resolveConnectionTarget } from "@/features/connections/target-draft"
import { composeKey } from "@/features/docker/deploy/compose-model"
import { kubeKey } from "@/features/kubernetes/deploy/kube-model"
import type { SetDraft } from "./configure"
import type { LoadDraft } from "./draft"
import { parseHeaders, targetKey } from "./targets"

export interface WorkflowTarget {
  target: LoadTarget | null
  external: ExternalTarget | null
  /** Saved connection the target uses (source "connection"). */
  connection: Connection | undefined
  suggested: LoadTarget[]
  loading: boolean
}

/** Resolves the workflow tab's draft into a target and whether it leaves the sandbox. */
export function useWorkflowTarget(draft: LoadDraft, setDraft: SetDraft): WorkflowTarget {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const deployedAt = useWorkflowStore((s) => s.deployed?.updatedAt)
  // Docker and Kubernetes workspaces have no deployed state: their targets follow the containers' state and published
  // ports, or the cluster's port-forwards and ingress URLs.
  const containersKey = useWorkflowStore((s) => composeKey(s.compose))
  const clusterKey = useWorkflowStore((s) => kubeKey(s.kube))
  const targets = useQuery({
    queryKey: ["load-targets", workflowId, deployedAt, containersKey, clusterKey],
    queryFn: () => api.loadTargets(workflowId!),
    enabled: !!workflowId,
  })
  const connections = useConnections()
  const checks = useConnectionsStore((s) => s.checks)

  // Preselect a target as soon as something is deployed so the wizard never
  // sits on "nothing selected"; HTTP endpoints first (what most people test).
  const loaded = targets.data
  useEffect(() => {
    if (!loaded?.length || loaded.some((t) => targetKey(t) === draft.suggestedKey)) return
    const first = loaded.find((t) => t.kind === "http") ?? loaded[0]
    setDraft((d) => ({ ...d, suggestedKey: targetKey(first) }))
  }, [loaded, draft.suggestedKey, setDraft])

  const connection = connections.data?.find((c) => c.id === draft.connection.connectionId)
  const target: LoadTarget | null = useMemo(() => {
    if (draft.source === "suggested") return loaded?.find((t) => targetKey(t) === draft.suggestedKey) ?? null
    if (draft.source === "connection") return resolveConnectionTarget(connection, draft.connection)
    if (draft.source === "http") {
      if (!draft.http.url.trim()) return null
      const { headersText, ...http } = draft.http
      return {
        kind: "http",
        label: `${http.method} ${http.url}`,
        http: { ...http, headers: parseHeaders(headersText), body: http.body || undefined },
      }
    }
    if (!draft.aws.operation.trim() || !draft.aws.resource.trim()) return null
    return {
      kind: "aws",
      label: `${draft.aws.operation} ${draft.aws.resource}`,
      aws: { ...draft.aws, payload: draft.aws.payload || undefined, key: draft.aws.key || undefined },
    }
  }, [draft, loaded, connection])

  const external = describeExternal(target, connection, connection ? checks[connection.id] : undefined)
  return { target, external, connection, suggested: loaded ?? [], loading: targets.isPending }
}
