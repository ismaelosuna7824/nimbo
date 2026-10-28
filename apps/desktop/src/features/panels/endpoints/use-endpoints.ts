import { useQuery } from "@tanstack/react-query"
import { create } from "zustand"
import { api } from "@/lib/api/client"
import type { HTTPEndpoint, ResourceAccess } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { composeKey } from "@/features/docker/deploy/compose-model"
import { kubeKey } from "@/features/kubernetes/deploy/kube-model"

/**
 * Endpoints of the open workflow. Refetched whenever the deployed state, the
 * sandbox (state, emulator addresses), in a Docker workspace the containers
 * (state, published ports) or in a Kubernetes workspace the cluster (workloads,
 * port-forwards, ingress URLs) change; every caller shares one query.
 */
export function useEndpoints() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const deployedAt = useWorkflowStore((s) => s.deployed?.updatedAt)
  const sandboxKey = useWorkflowStore(
    (s) => `${s.sandbox?.state ?? ""}|${s.sandbox?.emulators.map((e) => `${e.state}@${e.url}`).join(",") ?? ""}`,
  )
  const containersKey = useWorkflowStore((s) => composeKey(s.compose))
  const clusterKey = useWorkflowStore((s) => kubeKey(s.kube))
  return useQuery({
    queryKey: ["endpoints", workflowId, deployedAt, sandboxKey, containersKey, clusterKey],
    queryFn: () => api.endpoints(workflowId!),
    enabled: !!workflowId,
    // The key changes with everything the answer depends on: a new reader (the inspector of each selected node)
    // reuses it instead of fetching it again.
    staleTime: Infinity,
    // Keep showing the previous answer while refetching, but never another workflow's.
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[1] === workflowId ? prev : undefined),
  })
}

/**
 * True when the card has something to use (an address, an endpoint, a snippet
 * or a Try action) and is not supporting plumbing: what gets a canvas plug.
 */
export function isUsable(r: ResourceAccess): boolean {
  if (r.supporting) return false
  return (r.endpoints?.length ?? 0) > 0 || r.actions.length > 0 || r.snippets.length > 0 || r.fields.length > 0
}

/** Identifier for "Try it": the node ID when it maps to one resource, else the resource address. */
export function tryTarget(r: ResourceAccess, all: ResourceAccess[]): string {
  if (!r.nodeId) return r.address
  return all.filter((x) => x.nodeId === r.nodeId).length === 1 ? r.nodeId : r.address
}

export function firstHttpEndpoint(resources: ResourceAccess[] | undefined): HTTPEndpoint | undefined {
  for (const r of resources ?? []) {
    const e = r.endpoints?.[0]
    if (e) return e
  }
  return undefined
}

interface FocusState {
  /** Resource address to scroll to in the Endpoints tab. */
  address: string | null
  /** Bumped on every request so asking twice for the same resource scrolls again. */
  seq: number
  /** Resource whose "Try it" should open (set together with address). */
  tryIt: boolean
  focus: (address: string | null, tryIt?: boolean) => void
}

export const useEndpointsFocus = create<FocusState>((set) => ({
  address: null,
  seq: 0,
  tryIt: false,
  focus: (address, tryIt = false) => set((s) => ({ address, tryIt, seq: s.seq + 1 })),
}))

/** Opens the Endpoints tab, optionally scrolled to one resource (by address). */
export function openEndpoints(address?: string, opts: { tryIt?: boolean } = {}) {
  useWorkflowStore.getState().showBottom("endpoints")
  useEndpointsFocus.getState().focus(address ?? null, opts.tryIt)
}

/** DOM id of a resource card in the Endpoints tab. */
export const cardDomId = (address: string) => `endpoint-${address.replace(/[^\w-]/g, "_")}`
