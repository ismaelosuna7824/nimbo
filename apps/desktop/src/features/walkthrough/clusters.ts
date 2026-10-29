// Managed Kubernetes clusters (EKS, AKS, GKE, OKE) in a Terraform design. Requests rarely reach a cluster without a
// load balancer or an Ingress in front of it, but operators do: kubectl talks to the control plane's API endpoint,
// which schedules the work on the node pools. When the endpoint is public, the walkthrough offers that path
// ("Operator (kubectl) → control plane → node group"). Pure.
import type { DeriveWorkflow } from "@/features/architecture/derive"
import { baseType } from "@/features/architecture/icons/classify"
import type { ArchEdge, ArchModel } from "@/features/architecture/model"
import { makeResolver } from "@/features/architecture/resolve"
import type { WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import type { Entry } from "./paths"

/** The operator running kubectl: an actor the walkthrough adds for clusters with a public API endpoint. */
export const OPERATOR_ACTOR = "actor:operator"

export const CLUSTER_TYPE = /^(aws_eks_cluster|azurerm_kubernetes_cluster|google_container_cluster|oci_containerengine_cluster)$/
export const NODE_POOL_TYPE = /^(aws_eks_node_group|aws_eks_fargate_profile|azurerm_kubernetes_cluster_node_pool|google_container_node_pool|oci_containerengine_node_pool)$/

interface ClusterAccess {
  /** The cluster's card. */
  cluster: string
  /** kubectl can reach its API endpoint from the internet. */
  publicEndpoint: boolean
  /** Cards of its node pools, in the model's order. */
  pools: string[]
}

/** A literal attribute of a block (`attr(node, "vpc_config.endpoint_public_access")`), undefined when not literal. */
type AttrOf = (node: WorkflowNode, path: string) => unknown

/** Whether a cluster's API endpoint is reachable from the internet, from each provider's setting and its default. */
function publicEndpoint(node: WorkflowNode, attr: AttrOf): boolean {
  switch (baseType(node.type)) {
    case "aws_eks_cluster":
      return attr(node, "vpc_config.endpoint_public_access") !== false
    case "azurerm_kubernetes_cluster":
      return attr(node, "private_cluster_enabled") !== true
    case "google_container_cluster":
      return attr(node, "private_cluster_config.enable_private_endpoint") !== true
    case "oci_containerengine_cluster":
      return attr(node, "endpoint_config.is_public_ip_enabled") === true
    default:
      return false
  }
}

/** The clusters of a model with their endpoint and node pools (pools reference their cluster). */
function clusterAccess(model: ArchModel, nodes: WorkflowNode[], edges: WorkflowEdge[], attr: AttrOf): ClusterAccess[] {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const cardOf = new Map(model.cards.map((c) => [c.nodeId, c.id]))
  return model.cards.flatMap((c) => {
    const node = byId.get(c.nodeId)
    if (!node || !CLUSTER_TYPE.test(baseType(node.type))) return []
    const pools = model.cards
      .filter((p) => NODE_POOL_TYPE.test(baseType(p.type)) && edges.some((e) => e.source === c.nodeId && e.target === p.nodeId && e.kind !== "depends_on"))
      .map((p) => p.id)
    return [{ cluster: cardOf.get(c.nodeId)!, publicEndpoint: publicEndpoint(node, attr), pools }]
  })
}

/** `model` with the operator reaching every cluster with a public endpoint, and each cluster its node pools. */
function withOperator(model: ArchModel, clusters: ClusterAccess[]): ArchModel {
  const open = clusters.filter((c) => c.publicEndpoint)
  if (!open.length) return model
  const edges: ArchEdge[] = [
    ...open.map((c) => ({ id: `flow:${OPERATOR_ACTOR}->${c.cluster}`, source: OPERATOR_ACTOR, target: c.cluster, kind: "data" as const })),
    ...open.flatMap((c) => c.pools.map((p) => ({ id: `flow:${c.cluster}->${p}`, source: c.cluster, target: p, kind: "data" as const }))),
  ]
  return {
    ...model,
    actors: [...model.actors, { id: OPERATOR_ACTOR, kind: "service", label: "Operator", sublabel: "kubectl" }],
    edges: [...model.edges, ...edges.filter((e) => !model.edges.some((x) => x.id === e.id))],
  }
}

/** `model` (derived from `wf`) with the operator's way into each cluster whose API endpoint is public. */
export function withClusterOperators(model: ArchModel, wf: DeriveWorkflow): ArchModel {
  const r = makeResolver(wf, () => undefined)
  return withOperator(model, clusterAccess(model, wf.nodes, wf.edges ?? [], (n, path) => r.attr(n, path)))
}

/** The operator's ways in: one per cluster with a public API endpoint (see withOperator). */
export function operatorEntries(model: ArchModel): Entry[] {
  return model.edges.filter((e) => e.source === OPERATOR_ACTOR).map((e) => ({ actor: OPERATOR_ACTOR, target: e.target }))
}

/**
 * How a subnet's traffic reaches the internet: through a NAT gateway its route table names (and the internet gateway
 * of the NAT's own subnet), or straight through an internet gateway. Workflow node IDs, nearest first.
 */
export function egressOf(subnet: string, nodes: WorkflowNode[], edges: WorkflowEdge[]): string[] {
  const typeOf = (id: string) => baseType(nodes.find((n) => n.id === id)?.type ?? "")
  const gatewaysOf = (s: string): { nat?: string; igw?: string } => {
    const assocs = edges.filter((e) => e.source === s && /route_table_association$/.test(typeOf(e.target))).map((e) => e.target)
    const tables = edges.filter((e) => assocs.includes(e.target) && /route_table$/.test(typeOf(e.source))).map((e) => e.source)
    const into = edges.filter((e) => tables.includes(e.target) && e.kind !== "depends_on")
    return {
      nat: into.find((e) => /nat_gateway$/.test(typeOf(e.source)))?.source,
      igw: into.find((e) => /internet_gateway$/.test(typeOf(e.source)))?.source,
    }
  }
  const { nat, igw } = gatewaysOf(subnet)
  if (igw) return [igw]
  if (!nat) return []
  const natSubnet = edges.find((e) => e.target === nat && /(^|\.)subnet_id$/.test(e.attribute) && /subnet$/.test(typeOf(e.source)))?.source
  const out = natSubnet ? gatewaysOf(natSubnet).igw : undefined
  return out ? [nat, out] : [nat]
}
