import { useEffect, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { NetworkSummary, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** Resource types that make a workflow worth a network check (a network of its own or firewall rules). */
const NETWORK_TYPES: Record<string, true> = {
  aws_vpc: true,
  aws_subnet: true,
  aws_security_group: true,
  azurerm_virtual_network: true,
  azurerm_subnet: true,
  azurerm_network_security_group: true,
  google_compute_network: true,
  google_compute_subnetwork: true,
  google_compute_firewall: true,
}

export function hasNetworkResources(nodes: WorkflowNode[] | undefined): boolean {
  return (nodes ?? []).some((n) => n.kind === "resource" && NETWORK_TYPES[n.type])
}

/** Edits arrive in bursts (typing, drags): wait for them to settle before re-analysing. */
const REFETCH_DEBOUNCE_MS = 600

/**
 * Network model of the open workflow. Refetched (debounced) when the workflow's
 * content is saved or changed elsewhere (workflow.updated; moved cards don't
 * count) and when the deployed state changes (state.updated); every caller
 * shares one query.
 */
export function useNetwork() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const contentVersion = useWorkflowStore((s) => s.version)
  const deployedAt = useWorkflowStore((s) => s.deployed?.updatedAt)
  const version = `${contentVersion}|${deployedAt ?? ""}`
  const [settled, setSettled] = useState(version)
  useEffect(() => {
    const t = window.setTimeout(() => setSettled(version), REFETCH_DEBOUNCE_MS)
    return () => window.clearTimeout(t)
  }, [version])
  return useQuery({
    queryKey: ["network", workflowId, settled],
    queryFn: () => api.network(workflowId!),
    enabled: !!workflowId,
    // The key changes with everything the model depends on: a new reader (the inspector section of each selected
    // node) reuses the answer instead of fetching it again.
    staleTime: Infinity,
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[1] === workflowId ? prev : undefined),
  })
}

/** Canvas node IDs the network model knows about (what gets an inspector Network section). */
export function networkNodeIds(summary: NetworkSummary | undefined): Set<string> {
  const ids = new Set<string>()
  if (!summary) return ids
  for (const list of [
    summary.networks,
    summary.subnets,
    summary.routeTables,
    summary.gateways,
    summary.securityGroups,
    summary.placements,
  ]) {
    for (const item of list ?? []) if (item.nodeId) ids.add(item.nodeId)
  }
  return ids
}

/**
 * Adds a small test instance (a web server on port 80) and its security group to
 * a subnet, then selects it. Resolves to the new node ID, or undefined on failure.
 */
export async function addTestInstance(subnet: string): Promise<string | undefined> {
  const store = useWorkflowStore.getState()
  await store.flush()
  const wf = store.workflow
  if (!wf) return undefined
  try {
    const res = await api.addTestInstance(wf.id, subnet)
    if (useWorkflowStore.getState().workflow?.id !== wf.id) return undefined
    useWorkflowStore.getState().setWorkflow(res.workflow)
    useWorkflowStore.getState().select(res.nodeId)
    toast.success("Test instance added", {
      description: "Deploy the workflow, then run a live reachability test to see traffic really flow.",
    })
    return res.nodeId
  } catch (err) {
    toast.error("Couldn't add a test instance", { description: errorMessage(err) })
    return undefined
  }
}
