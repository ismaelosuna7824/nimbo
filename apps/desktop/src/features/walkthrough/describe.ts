// What the walkthrough says about each item of a design: its name, friendly type, role in a request,
// where it runs and the supporting pieces around it, from the Architecture model plus what each kind of
// workspace knows (Terraform: catalog schemas and references; Docker: the compose graph; Kubernetes: the
// rendered objects). Pure: the walkthrough layer feeds it the open workflow.
import { baseType } from "@/features/architecture/icons/classify"
import { typeLabel } from "@/features/architecture/derive"
import { PROVIDER_SHORT, type ArchCard, type ArchModel } from "@/features/architecture/model"
import { techIconFor, type TechIconId } from "@/components/tech-icons"
import type { Body, ComposeGraph, KubeEdge, KubeGraph, ResourceSchema, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import type { HopTraits } from "./paths"

/** What an item does with a request (decides the wording of its step). */
export type Role =
  | "users"
  | "internet"
  | "front"
  | "compute"
  | "database"
  | "cache"
  | "storage"
  | "messaging"
  | "identity"
  | "network"
  | "other"

/** A supporting piece shown on a step: an IAM role, a security group rule, a key vault, a volume… */
export interface Chip {
  label: string
  /** Tooltip: the rule, the address. */
  detail?: string
  /** Build canvas node to select. */
  nodeId?: string
}

export interface Where {
  /** absent: in the design, but the active environment does not create it (count = 0). */
  tone: "deployed" | "local" | "export" | "absent"
  text: string
}

export interface ItemInfo extends HopTraits {
  id: string
  /** "api", "orders queue", "Users". */
  name: string
  /** How sentences and choices name it when the name alone is ambiguous ("orders (SQS Queue)"); else `name`. */
  ref?: string
  /** "Azure Container App", "PostgreSQL container", "Kubernetes Ingress". */
  type: string
  role: Role
  /** The catalog's one-line description, when it has one. */
  about?: string
  /** Ports, protocols and routes the item states ("3100→3000/tcp", "external ingress"). */
  facts: string[]
  where?: Where
  also: Chip[]
  /** Build canvas nodes the item stands for (none for actors). */
  nodeIds: string[]
}

export interface Describer {
  info: Map<string, ItemInfo>
  /** How a hop connects, when the design says it ("environment variable DATABASE_URL"). */
  relation: (from: string, to: string) => string | undefined
}

/** Details that tell how requests arrive: ports, protocols, routes, ingress. */
const PORTISH = /(\d{2,5}→\d{2,5}|:\d{2,5}→|\b\d{2,5}\/(tcp|udp)\b|→ [\w.-]+:[\w-]+$|\bhttps?\b|\bgrpc\b|ingress|internet-facing|\bport \d+|\blistener)/i

/** First sentence of a catalog description ("Run code without servers."). */
export function firstSentence(text: string | undefined): string | undefined {
  const t = text?.trim()
  if (!t) return undefined
  const m = /^(.+?[.!?])(\s|$)/.exec(t)
  return m ? m[1] : t
}

const unique = <T>(xs: T[], key: (x: T) => string) => {
  const seen = new Set<string>()
  return xs.filter((x) => {
    const k = key(x)
    if (seen.has(k)) return false
    seen.add(k)
    return true
  })
}

/** Actors and containers: the same in every kind of workspace. */
function baseInfo(model: ArchModel, info: Map<string, ItemInfo>, cardInfo: (id: string) => ItemInfo | undefined) {
  for (const a of model.actors) {
    const role: Role = a.kind === "users" ? "users" : a.kind === "internet" ? "internet" : "other"
    info.set(a.id, { id: a.id, name: a.label, type: a.sublabel ?? (a.kind === "internet" ? "The public internet" : "Outside service"), role, facts: [], also: [], nodeIds: [], async: false, supporting: false })
  }
  for (const g of model.groups) {
    const members = model.cards.filter((c) => c.parent === g.id)
    const first = g.packed && members[0] ? cardInfo(members[0].id) : undefined
    info.set(g.id, {
      id: g.id,
      name: g.label,
      type: g.packed ? `${members.length} of a kind` : g.kind === "subnet" ? "Subnet" : g.kind === "vpc" ? "Private network" : "Group",
      role: first?.role ?? "network",
      facts: [],
      also: [],
      nodeIds: g.packed ? members.map((c) => c.nodeId) : g.nodeId ? [g.nodeId] : [],
      async: first?.async ?? false,
      supporting: false,
      ...(first?.where ? { where: first.where } : {}),
    })
  }
}

// ------------------------------------------------------------------ Terraform

/** Queues, topics, event buses and streams: asynchronous hops. */
const ASYNC_TYPE =
  /^(aws_(sqs_queue|sns_topic|cloudwatch_event_(bus|rule)|kinesis_stream|kinesis_firehose_delivery_stream|msk_cluster|mq_broker|pipes_pipe|scheduler_schedule)|azurerm_(servicebus_(namespace|queue|topic|subscription)|eventhub(_namespace)?|eventgrid_(topic|system_topic|domain)|storage_queue)|google_(pubsub_(topic|subscription|lite_topic)|cloud_tasks_queue|eventarc_trigger)|oci_(streaming_stream(_pool)?|queue_queue|events_rule))$/
/** API front doors, load balancers and CDNs. */
const FRONT_TYPE =
  /(api_?gateway|apigatewayv2|api_management|frontdoor|_cdn_|cloudfront|^aws_(lb|alb|elb)$|_lb$|load_balancer|application_gateway|forwarding_rule|url_map|globalaccelerator|traffic_manager|route53_zone|dns_zone$|_dns_managed_zone)/
const CACHE_TYPE = /(redis|elasticache|memcache|valkey|memorystore)/
/** Configuration and plumbing a service uses but requests never pass through. */
const SUPPORTING_TYPE =
  /(private_dns_zone|_dns_zone_virtual_network_link|launch_template|launch_configuration|instance_template|_iam_|_role$|_policy$|managed_identity|user_assigned_identity|service_account|key_vault|_kms_|secretsmanager|secret_manager|_secret$|_certificate|log_group|log_analytics|application_insights|_monitor_|cloudwatch_(metric_alarm|dashboard)|container_registry|_ecr_|artifact_registry|container_app_environment)/
const SUPPORTING_CATEGORY: Partial<Record<ArchCard["category"], true>> = { identity: true, security: true, monitoring: true, devops: true, management: true }

export function terraformRole(card: Pick<ArchCard, "type" | "category">): Role {
  const t = baseType(card.type)
  if (ASYNC_TYPE.test(t)) return "messaging"
  if (FRONT_TYPE.test(t) && !/private_dns/.test(t)) return "front"
  if (CACHE_TYPE.test(t)) return "cache"
  switch (card.category) {
    case "database":
      return "database"
    case "storage":
      return "storage"
    case "compute":
    case "containers":
    case "serverless":
    case "integration":
    case "analytics":
    case "ai":
      return "compute"
    case "identity":
    case "security":
      return "identity"
    case "networking":
      return "network"
    default:
      return "other"
  }
}

/** Secrets that can hold a connection string. */
const SECRET_TYPE = /^(azurerm_key_vault_secret|aws_secretsmanager_secret(_version)?|google_secret_manager_secret(_version)?|aws_ssm_parameter|oci_vault_secret)$/

export function isSupportingType(card: Pick<ArchCard, "type" | "category" | "existing">): boolean {
  return !!SUPPORTING_CATEGORY[card.category] || SUPPORTING_TYPE.test(baseType(card.type)) || (!!card.existing && card.category === "networking")
}

/** The attribute at `path` ("template[0].container[0].env[1].value") and the body holding it. */
function attrAt(body: Body, path: string): { body: Body; name: string } | undefined {
  const parts = path.split(".")
  let cur = body
  for (let i = 0; i < parts.length - 1; i++) {
    const m = /^([^[]+)(?:\[(\d+)\])?$/.exec(parts[i])
    if (!m) return undefined
    const block = cur.blocks.filter((b) => b.type === m[1])[Number(m[2] ?? 0)]
    if (!block) return undefined
    cur = block.body
  }
  return { body: cur, name: parts[parts.length - 1] }
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

/** "environment variable DATABASE_HOST" for a reference through an env block or a variables map, else the attribute. */
export function referenceText(e: Pick<WorkflowEdge, "source" | "attribute" | "sourceAttr">, referrer: WorkflowNode | undefined): string {
  const attr = e.attribute
  const at = referrer ? attrAt(referrer.body, attr) : undefined
  if (at && at.name === "value") {
    const name = at.body.attrs.find((a) => a.name === "name")?.value.literal
    if (typeof name === "string") return `environment variable ${name}`
  }
  if (at && /^(variables|environment_variables|env|app_settings|environment)$/.test(at.name)) {
    const expr = at.body.attrs.find((a) => a.name === at.name)?.value.expr ?? ""
    const key = new RegExp(`["']?([\\w.-]+)["']?\\s*[=:]\\s*[^\\n,}]*${escapeRe(e.source)}\\b`).exec(expr)?.[1]
    if (key) return `environment variable ${key}`
  }
  if (/event_source_arn$/.test(attr)) return "an event source mapping (it polls for messages)"
  const clean = attr.replace(/\[\d+\]/g, "")
  return e.sourceAttr ? `${clean} → ${e.sourceAttr}` : clean
}

export interface TerraformInput {
  model: ArchModel
  nodes: WorkflowNode[]
  edges: WorkflowEdge[]
  schemaOf: (node: WorkflowNode) => ResourceSchema | undefined
  deployed: Set<string>
  /** Nodes the active environment does not create (count = 0) though the design has them, with its name. */
  absent?: { ids: Set<string>; env: string }
}

export function describeTerraform({ model, nodes, edges, schemaOf, deployed, absent }: TerraformInput): Describer {
  const byId = new Map(nodes.map((n) => [n.id, n]))
  const cards = new Map(model.cards.map((c) => [c.id, c]))
  const cardOfNode = new Map(model.cards.map((c) => [c.nodeId, c.id]))
  const info = new Map<string, ItemInfo>()

  const typeOf = (c: ArchCard) => {
    const node = byId.get(c.nodeId)
    const schema = node && node.kind !== "module" ? schemaOf(node) : undefined
    const label = node?.kind === "module" ? "Module" : schema?.label || typeLabel(c.type)
    const provider = c.provider ? PROVIDER_SHORT[c.provider] : ""
    return { schema, label, type: provider && !label.toLowerCase().startsWith(provider.toLowerCase()) ? `${provider} ${label}` : label }
  }
  const chipOf = (id: string): Chip | undefined => {
    const c = cards.get(id)
    return c && { label: `${typeOf(c).label} ${c.label || c.name}`, nodeId: c.nodeId }
  }

  for (const c of model.cards) {
    const node = byId.get(c.nodeId)
    const { schema, label, type } = typeOf(c)
    const role = terraformRole(c)
    const where: Where | undefined = absent?.ids.has(c.nodeId)
      ? { tone: "absent", text: `Not created in environment "${absent.env}" (count = 0)` }
      : node?.kind === "module" || !schema
        ? undefined
        : node?.kind === "data"
          ? { tone: "local", text: "Existing resource the design only reads" }
          : !schema.flociSupported
            ? { tone: "export", text: "Export only: not emulated locally" }
            : deployed.has(c.nodeId)
              ? { tone: "deployed", text: "Deployed in your local sandbox" }
              : { tone: "local", text: "Runs locally in the sandbox" }
    info.set(c.id, {
      id: c.id,
      name: c.label || c.name,
      // Block names repeat across services ("main", "orders"): sentences and choices name the service too.
      ref: `${c.label || c.name} (${label})`,
      type,
      role,
      about: firstSentence(schema?.description),
      facts: c.details.filter((d) => PORTISH.test(d)).slice(0, 2),
      ...(where ? { where } : {}),
      also: [],
      nodeIds: [c.nodeId],
      async: role === "messaging",
      supporting: isSupportingType(c),
    })
  }

  // Also involved: dashed (control) neighbours, supporting pieces it sends data to, platform cards it
  // references (a Container Apps environment) with their own dashed neighbours, its security group
  // rules and the helpers folded into it.
  const control = (id: string) =>
    model.edges.filter((e) => e.kind === "control" && (e.source === id || e.target === id)).map((e) => (e.source === id ? e.target : e.source))
  const dataNeighbours = (id: string) => new Set(model.edges.filter((e) => e.kind === "data" && (e.source === id || e.target === id)).map((e) => (e.source === id ? e.target : e.source)))
  for (const c of model.cards) {
    const own = info.get(c.id)!
    const near = dataNeighbours(c.id)
    const ids = [
      ...control(c.id),
      ...[...near].filter((id) => info.get(id)?.supporting),
    ]
    for (const e of edges) {
      if (e.kind === "depends_on" || e.target !== c.nodeId) continue
      const from = cardOfNode.get(e.source)
      if (!from || from === c.id || near.has(from) || !info.get(from)?.supporting) continue
      ids.push(from, ...control(from).filter((id) => !near.has(id) && info.get(id)?.supporting))
    }
    const chips: Chip[] = ids.filter((id) => id !== c.id && cards.has(id)).flatMap((id) => chipOf(id) ?? [])
    for (const line of c.security ?? []) {
      const i = line.indexOf(": ")
      const label = (i > 0 ? line.slice(0, i) : line).replace(/^SG /, "Security group ")
      chips.push(i > 0 ? { label, detail: line.slice(i + 2) } : { label })
    }
    if (c.folded) for (const part of c.folded.replace(/^\+\s*/, "").split(", ")) chips.push({ label: part, detail: `Part of ${own.name}` })
    own.also = unique(chips, (x) => x.label)
  }
  baseInfo(model, info, (id) => info.get(id))

  const nodesOf = (id: string) => info.get(id)?.nodeIds ?? []
  // Nodes the model draws (cards, and containers such as resource groups and subnets) never link two others.
  const drawn = new Set([...cardOfNode.keys(), ...model.groups.flatMap((g) => (g.nodeId ? [g.nodeId] : []))])
  const referenceEdges = edges.filter((e) => e.kind !== "depends_on")
  const relation = (from: string, to: string) => {
    const a = new Set(nodesOf(from))
    const b = new Set(nodesOf(to))
    if (!a.size || !b.size) return undefined
    const direct = referenceEdges
      .filter((e) => (a.has(e.source) && b.has(e.target)) || (b.has(e.source) && a.has(e.target)))
      .map((e) => referenceText(e, byId.get(e.target)))
    // Through a helper linked to both ends. A part of one end the other references (a queue in its Service Bus
    // namespace) connects them by that reference; a helper referencing both (an API integration, a target group)
    // is the link itself.
    const through: string[] = []
    for (const e of referenceEdges) {
      const x = a.has(e.source) ? e.target : a.has(e.target) ? e.source : undefined
      if (!x || drawn.has(x)) continue
      const other = referenceEdges.find((f) => (f.source === x && b.has(f.target)) || (f.target === x && b.has(f.source)))
      const helper = byId.get(x)
      if (!other || !helper) continue
      const helperLabel = schemaOf(helper)?.label || typeLabel(helper.type)
      // A connection string kept as a secret: the secret is the link ("the Key Vault Secret database-url").
      if (SECRET_TYPE.test(baseType(helper.type))) {
        const name = helper.body.attrs.find((x) => x.name === "name")?.value.literal
        through.push(`the ${helperLabel} ${typeof name === "string" ? name : helper.name}`)
      } else if (e.source === x && other.target === x) through.push(referenceText(e, byId.get(e.target)))
      else if (other.source === x && e.target === x) through.push(referenceText(other, byId.get(other.target)))
      else through.push(`the ${helperLabel}`)
    }
    // An environment variable says most about how a service finds the other end.
    return [...direct, ...through].find((t) => t.startsWith("environment variable")) ?? direct[0] ?? through[0]
  }
  return { info, relation }
}

// ------------------------------------------------------------------ Docker

/** Web servers and proxies: the front of a compose project. */
const PROXY_TECH: Partial<Record<TechIconId, true>> = { nginx: true, caddy: true, traefik: true, haproxy: true, envoy: true, kong: true, apisix: true, krakend: true, varnish: true, squid: true, apache: true }

function techRole(id: TechIconId, category: string): Role {
  if (PROXY_TECH[id]) return "front"
  if (category === "messaging") return "messaging"
  if (category === "cache") return "cache"
  if (category === "database" || category === "search") return "database"
  if (category === "storage") return "storage"
  return "compute"
}

export function describeDocker(model: ArchModel, graph: ComposeGraph): Describer {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]))
  const info = new Map<string, ItemInfo>()
  for (const c of model.cards) {
    const svc = byId.get(c.nodeId)?.service
    const tech = techIconFor({ image: svc?.image?.ref, baseImages: svc?.baseImages, stack: svc?.stack, kind: "service" })
    const role = techRole(tech.id, tech.category)
    const also: Chip[] = graph.edges.flatMap((e) => {
      if (e.target !== c.nodeId || !(e.kind === "volume" || e.kind === "secret" || e.kind === "config")) return []
      const used = byId.get(e.source)
      return used ? [{ label: `${e.kind} ${used.name}`, ...(e.path ? { detail: `mounted at ${e.path}` } : {}), nodeId: used.id }] : []
    })
    for (const net of (svc?.networks ?? []).slice(1)) also.push({ label: `network ${net}` })
    info.set(c.id, {
      id: c.id,
      name: c.name,
      type: tech.generic ? "Docker container" : `${tech.label} container`,
      role,
      facts: c.details.filter((d) => PORTISH.test(d)).slice(0, 2),
      where: c.existing ? { tone: "export", text: "Not started: its profile is off" } : { tone: "local", text: "Runs locally in Docker" },
      also: unique(also, (x) => x.label),
      nodeIds: [c.nodeId],
      async: role === "messaging",
      supporting: false,
    })
  }
  baseInfo(model, info, (id) => info.get(id))
  const relation = (from: string, to: string) => {
    // The graph points from the used service to its user.
    const links = graph.edges.filter((e) => (e.source === to && e.target === from) || (e.source === from && e.target === to))
    const env = links.find((e) => e.kind === "inferred" && e.envKey)
    if (env) return `environment variable ${env.envKey}`
    if (links.some((e) => e.kind === "links")) return "a compose link"
    const dep = links.find((e) => e.kind === "depends_on")
    if (dep) return `depends_on (starts after it is ${dep.condition === "service_healthy" ? "healthy" : "started"})`
    return undefined
  }
  return { info, relation }
}

// ------------------------------------------------------------------ Kubernetes

const ENTRY_KIND: Record<string, true> = { Ingress: true, HTTPRoute: true, GRPCRoute: true, Gateway: true }
const SIDE_EDGES: Partial<Record<KubeEdge["kind"], true>> = { envFrom: true, volume: true, serviceaccount: true, scales: true, netpol: true, binds: true, disrupts: true, selects: true }

export function describeKubernetes(model: ArchModel, graph: KubeGraph): Describer {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]))
  const info = new Map<string, ItemInfo>()
  for (const c of model.cards) {
    const node = byId.get(c.nodeId)
    const image = node?.workload?.containers[0]?.image
    const tech = node?.workload ? techIconFor({ image, stack: node.stack, kind: "service" }) : undefined
    const role: Role = Object.hasOwn(ENTRY_KIND, c.type)
      ? "front"
      : c.type === "PersistentVolumeClaim"
        ? "storage"
        : tech && !tech.generic
          ? techRole(tech.id, tech.category)
          : c.category === "database"
            ? "database"
            : c.category === "networking"
              ? "front"
              : "compute"
    const also: Chip[] = graph.edges.flatMap((e) => {
      if (!SIDE_EDGES[e.kind] || (e.source !== c.nodeId && e.target !== c.nodeId)) return []
      const other = byId.get(e.source === c.nodeId ? e.target : e.source)
      return other && other.kind !== c.type ? [{ label: `${other.kind} ${other.name}`, nodeId: other.id }] : []
    })
    info.set(c.id, {
      id: c.id,
      name: c.name,
      type: `Kubernetes ${c.type}${tech && !tech.generic ? ` (${tech.label})` : ""}`,
      role,
      facts: c.details.filter((d) => PORTISH.test(d)).slice(0, 2),
      where: { tone: "local", text: "Runs on your local cluster" },
      also: unique(also, (x) => x.label),
      nodeIds: [c.nodeId],
      async: role === "messaging",
      supporting: false,
    })
  }
  baseInfo(model, info, (id) => info.get(id))
  const relation = (from: string, to: string) => {
    const direct = graph.edges.find((e) => e.source === from && e.target === to && (e.kind === "env" || e.kind === "inferred" || e.kind === "routes"))
    if (direct?.envKey) return `environment variable ${direct.envKey}`
    if (direct?.kind === "routes") return direct.label ? `route ${direct.label}` : "a route"
    // Through the Service selecting the workload.
    for (const e of graph.edges) {
      if (e.source !== from || !(e.kind === "routes" || e.kind === "env" || e.kind === "inferred")) continue
      const svc = byId.get(e.target)
      if (!svc?.service || !graph.edges.some((s) => s.kind === "selects" && s.source === svc.id && s.target === to)) continue
      const how = e.envKey ? `environment variable ${e.envKey} → ` : e.kind === "routes" ? `route${e.label ? ` ${e.label}` : ""} → ` : ""
      return `${how}Service ${svc.name}`
    }
    return undefined
  }
  return { info, relation }
}
