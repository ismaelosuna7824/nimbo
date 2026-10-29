// Each Build canvas as a TourGraph (see tour.ts): what every card is, what it is built on, where it runs.
// Pure: the tour layer reads the canvas (positions, frames, edges) and the workflow and calls these.
import { archCategoryOf, baseType, providerOfType } from "@/features/architecture/icons/classify"
import { techIconFor } from "@/components/tech-icons"
import { typeLabel, type DeriveWorkflow } from "@/features/architecture/derive"
import { findAttr, makeResolver } from "@/features/architecture/resolve"
import type { ComposeGraph, KubeGraph, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import { firstSentence, terraformRole, type Where } from "./describe"
import type { TourDep, TourGraph, TourKind, TourNode } from "./tour"

/** A card of the canvas: where it is (absolute) and the frame it is drawn in. */
export interface CanvasCard {
  id: string
  x: number
  y: number
  parent?: string
  /** A container drawn around other cards (VPC, network, namespace). */
  frame?: boolean
  /** The canvas' own title of a card no workflow object stands for (an implicit namespace or network frame). */
  label?: string
}

/** A canvas edge as React Flow has it. */
export interface CanvasLink {
  id: string
  source: string
  target: string
}

/** Dependencies from the canvas edges (`dependsOn(edge)` → [dependency, dependent]) and frames (a frame before what it holds). */
function depsFrom(cards: CanvasCard[], links: CanvasLink[], dependsOn: (l: CanvasLink) => [string, string]): TourDep[] {
  const byPair = new Map<string, TourDep>()
  const add = (from: string, to: string, edge?: string) => {
    const key = `${from}>${to}`
    const d = byPair.get(key) ?? { from, to, edges: [] }
    if (edge) d.edges.push(edge)
    byPair.set(key, d)
  }
  for (const l of links) {
    const [from, to] = dependsOn(l)
    add(from, to, l.id)
  }
  for (const c of cards) if (c.parent) add(c.parent, c.id)
  return [...byPair.values()]
}

// ------------------------------------------------------------------ Terraform

/** Utility resources and lookups a design needs but nobody needs explained: names, passwords, glue between two resources. */
const TF_PLUMBING =
  /^(random_|tls_|time_|null_resource$|terraform_data$|local_|archive_)|(_association|_attachment|_access_policy|_authorization_rule|_role_policy|_permission|_virtual_network_link|_policy_document|_client_config|_caller_identity|_partition|_availability_zones?|_subscription|_region)$/

/** What a Terraform resource is for, from its type (most specific first) and its category. */
export function terraformTourKind(type: string): TourKind {
  const t = baseType(type)
  if (/(resource_group|_compartment$|^google_project$|organizations_account)/.test(t)) return "account"
  if (/(virtual_network$|_vpc$|compute_network$|core_vcn$|core_virtual_network$)/.test(t)) return "network"
  if (/subnet(work)?$/.test(t)) return "subnet"
  if (/(dns_zone|dns_record|route53_|dns_managed_zone|dns_record_set)/.test(t)) return "dns"
  if (/(security_group|firewall|network_acl|security_rule|_waf|wafv2|security_policy)/.test(t)) return "security"
  if (/(identity|iam_role|service_account|iam_user|iam_instance_profile)/.test(t)) return "identity"
  if (/(key_vault|secretsmanager|secret_manager|_kms_|_secret$|ssm_parameter|vault_secret)/.test(t)) return "secrets"
  if (/(log_analytics|log_group|_monitor|application_insights|cloudwatch|logging_|_alarm)/.test(t)) return "monitoring"
  if (/(container_app_environment|service_plan|app_service_environment|ecs_cluster|kubernetes_cluster$|eks_cluster$|container_cluster$|containerengine_cluster|container_registry|ecr_repository|artifact_registry)/.test(t)) return "platform"
  const provider = providerOfType(t)
  const role = terraformRole({ type: t, category: provider ? archCategoryOf(provider, t) : "other" })
  switch (role) {
    case "front":
      return "front"
    case "messaging":
      return "messaging"
    case "cache":
      return "cache"
    case "database":
      return "database"
    case "storage":
      return "storage"
    case "compute":
      return "compute"
    case "identity":
      return "identity"
    case "network":
      return "network"
    default:
      return "other"
  }
}

export interface TerraformTourInput {
  nodes: WorkflowNode[]
  links: CanvasLink[]
  cards: CanvasCard[]
  schemaOf: (node: WorkflowNode) => ResourceSchema | undefined
  deployed: Set<string>
  /** Resources the active environment does not create (count = 0), with its name. */
  absent?: { ids: Set<string>; env: string }
}

/** Blocks the active environment creates no instance of (count = 0, an empty for_each), though the canvas draws them. */
export function notCreated(wf: DeriveWorkflow): Set<string> {
  if (!wf.activeEnvironment) return new Set()
  const r = makeResolver(wf, () => undefined)
  return new Set(wf.nodes.filter((n) => (findAttr(n.body, "count") || findAttr(n.body, "for_each")) && r.count(n) === 0).map((n) => n.id))
}

export function terraformTourGraph({ nodes, links, cards, schemaOf, deployed, absent }: TerraformTourInput): TourGraph {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const out: TourNode[] = []
  for (const c of cards) {
    const n = byId.get(c.id)
    if (!n) continue
    const schema = n.kind === "module" ? undefined : schemaOf(n)
    const kind = n.kind === "module" ? "other" : terraformTourKind(n.type)
    const where: Where | undefined = absent?.ids.has(n.id)
      ? { tone: "absent", text: `Not created in environment "${absent.env}"` }
      : n.kind !== "resource" || !schema
        ? undefined
        : !schema.flociSupported
          ? { tone: "export", text: "Export only: not emulated locally" }
          : deployed.has(n.id)
            ? { tone: "deployed", text: "Deployed in your local sandbox" }
            : { tone: "local", text: "Runs locally in the sandbox" }
    out.push({
      id: n.id,
      name: n.label || n.name,
      title: n.kind === "module" ? "Module" : schema?.label || typeLabel(n.type),
      kind,
      ...(firstSentence(schema?.description) ? { about: firstSentence(schema?.description) } : {}),
      ...(where ? { where } : {}),
      ...(TF_PLUMBING.test(n.type) || (n.kind === "data" && kind === "other") ? { plumbing: true } : {}),
      ...(c.frame ? { frame: true } : {}),
      x: c.x,
      y: c.y,
    })
  }
  // A Terraform edge points from the referenced resource to the one referencing it.
  return { nodes: out, deps: depsFrom(cards, links, (l) => [l.source, l.target]) }
}

// ------------------------------------------------------------------ Docker

/** Web servers and proxies: the way into a compose project. */
const PROXY = /^(nginx|caddy|traefik|haproxy|envoy|kong|apisix|krakend|varnish|squid|apache)$/

function techKind(id: string, category: string): TourKind {
  if (PROXY.test(id)) return "front"
  if (category === "database" || category === "search") return "database"
  if (category === "cache") return "cache"
  if (category === "messaging") return "messaging"
  if (category === "storage") return "storage"
  return "compute"
}

const DOCKER_KIND: Record<string, { title: string; kind: TourKind }> = {
  volume: { title: "Volume", kind: "volume" },
  secret: { title: "Secret", kind: "config" },
  config: { title: "Config", kind: "config" },
  network: { title: "Network", kind: "network" },
  external: { title: "External", kind: "other" },
}

export function dockerTourGraph({ graph, links, cards }: { graph: ComposeGraph; links: CanvasLink[]; cards: CanvasCard[] }): TourGraph {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]))
  const out: TourNode[] = cards.map((c) => {
    const n = byId.get(c.id)
    const base = { id: c.id, x: c.x, y: c.y, ...(c.frame ? { frame: true } : {}) }
    if (n?.kind === "service") {
      const svc = n.service
      const tech = techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind: "service" })
      return {
        ...base,
        name: n.name,
        title: tech.generic ? "Container" : `${tech.label} container`,
        kind: techKind(tech.id, tech.category),
        where: n.inactive ? { tone: "export", text: "Not started: its profile is off" } : { tone: "local", text: "Runs locally in Docker" },
      }
    }
    const known = DOCKER_KIND[n?.kind === "external" && n.externalKind === "network" ? "network" : (n?.kind ?? (c.frame ? "network" : "external"))] ?? DOCKER_KIND.external
    return { ...base, name: n?.name ?? c.label ?? c.id.replace(/^network:/, ""), title: known.title, kind: known.kind }
  })
  // Canvas edges point from the service to what it uses.
  return { nodes: out, deps: depsFrom(cards, links, (l) => [l.target, l.source]) }
}

// ------------------------------------------------------------------ Kubernetes

const KUBE_KIND: Record<string, TourKind> = {
  Namespace: "namespace",
  ConfigMap: "config",
  Secret: "config",
  PersistentVolumeClaim: "volume",
  PersistentVolume: "volume",
  StorageClass: "volume",
  Service: "service",
  Ingress: "front",
  HTTPRoute: "front",
  GRPCRoute: "front",
  Gateway: "front",
  ServiceAccount: "identity",
  Role: "identity",
  RoleBinding: "identity",
  ClusterRole: "identity",
  ClusterRoleBinding: "identity",
  NetworkPolicy: "security",
}

export function kubeTourGraph({ graph, links, cards }: { graph: KubeGraph; links: CanvasLink[]; cards: CanvasCard[] }): TourGraph {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]))
  const charts = new Map((graph.charts ?? []).map((c) => [c.id, c]))
  const out: TourNode[] = cards.map((c) => {
    const n = byId.get(c.id)
    const base = { id: c.id, x: c.x, y: c.y, ...(c.frame ? { frame: true } : {}), where: { tone: "local" as const, text: "Runs on your local cluster" } }
    const chart = charts.get(c.id)
    if (chart) return { ...base, name: chart.chart, title: "Helm chart", kind: "compute" as const }
    if (!n) return { ...base, name: c.label ?? c.id.replace(/^namespace:/, ""), title: c.frame ? "Namespace" : "Object", kind: c.frame ? ("namespace" as const) : ("other" as const) }
    const image = n.workload?.containers[0]?.image
    const tech = n.workload ? techIconFor({ image, stack: n.stack, kind: "service" }) : undefined
    const kind = Object.hasOwn(KUBE_KIND, n.kind) ? KUBE_KIND[n.kind] : n.workload ? (tech && !tech.generic ? techKind(tech.id, tech.category) : "compute") : "other"
    return { ...base, name: n.name, title: tech && !tech.generic ? `${n.kind} (${tech.label})` : n.kind, kind }
  })
  // Canvas edges point from the object to what it uses (an Ingress to its Service, a Service to its pods).
  return { nodes: out, deps: depsFrom(cards, links, (l) => [l.target, l.source]) }
}
