// The Architecture diagram of a Kubernetes workspace: the rendered objects drawn as the local
// cluster, one group per namespace (with a data tier for databases, caches, queues and their
// volume claims), Ingress/HTTPRoute entries and one card per workload. Services, autoscalers,
// ConfigMaps and Secrets are lines on their workload's card, not cards of their own: requests go
// Ingress → workload and workload → workload through the Services that select them.
import { k8sIcon, k8sIconFor } from "@/components/k8s-icons"
import { techIcon, techIconFor, type TechCategory } from "@/components/tech-icons"
import type { KubeEdge, KubeNode, KubeScaler, KubeSourceKind, KubeStatus, Workflow } from "@/lib/api/types"
import type { ArchCard, ArchGroup, ArchModel } from "./model"
import { cardLines, type CardText, type LegendEntry } from "./styles"

export const KUBE_CLUSTER_GROUP = "group:k8s-cluster"
export const USERS_ACTOR = "actor:users"

export const namespaceGroupId = (ns: string) => `group:namespace:${ns}`
export const dataTierGroupId = (ns: string) => `group:data:${ns}`

/** Kinds that receive outside traffic by host/path. */
const ENTRY_KINDS: Record<string, true> = { Ingress: true, HTTPRoute: true, GRPCRoute: true }
/** Images of these technologies are data stores (drawn in the data tier). */
const DATA_CATEGORIES: Partial<Record<TechCategory, true>> = { database: true, cache: true, messaging: true, search: true, storage: true }
/** Service types reachable from outside the cluster. */
const EXPOSED_TYPES: Record<string, true> = { LoadBalancer: true, NodePort: true }
/** Graph edges that are requests or reads/writes: entry → Service, workload → Service (env URLs), workload → claim. */
const REQUEST_EDGES: Partial<Record<KubeEdge["kind"], true>> = { routes: true, inferred: true, env: true, pvc: true }

const SOURCE_LABEL: Record<KubeSourceKind, string> = { manifests: "Manifests", kustomize: "Kustomize", helm: "Helm" }

type KubeWorkflow = Pick<Workflow, "id" | "name" | "kubernetes">

/**
 * The architecture of a Kubernetes workspace: local cluster → one group per namespace holding its
 * Ingress/HTTPRoute entries and workloads, data stores (StatefulSets and database/cache/queue images)
 * and the claims they mount in the namespace's data tier. Services fold into the workloads they
 * select (a Service selecting nothing stays a card), so routes and env references become
 * Ingress → workload and workload → workload requests. The Users reach every entry and every
 * workload behind a LoadBalancer/NodePort Service. `status` adds the workloads' rollout state.
 */
export function deriveKubernetesArchitecture(wf: KubeWorkflow, status?: KubeStatus | null): ArchModel {
  const model: ArchModel = { groups: [], cards: [], actors: [], edges: [], providers: [], regions: [] }
  const nodes = wf.kubernetes?.graph.nodes ?? []
  const graphEdges = wf.kubernetes?.graph.edges ?? []
  const workloads = nodes.filter((n) => n.workload)
  const entries = nodes.filter((n) => Object.hasOwn(ENTRY_KINDS, n.kind) || !!n.routes?.length)
  if (workloads.length === 0 && entries.length === 0) return model

  const byId = new Map(nodes.map((n) => [n.id, n]))
  const workloadIds = new Set(workloads.map((w) => w.id))
  // Service → the workloads it selects.
  const selected = new Map<string, string[]>()
  for (const e of graphEdges) {
    if (e.kind !== "selects" || !workloadIds.has(e.target)) continue
    selected.set(e.source, [...(selected.get(e.source) ?? []), e.target])
  }
  const services = nodes.filter((n) => n.service)
  const standaloneServices = services.filter((s) => !selected.get(s.id)?.length)
  const isData = new Map(workloads.map((w) => [w.id, isDataStore(w)]))
  const claimIds = new Set(graphEdges.filter((e) => e.kind === "pvc" && workloadIds.has(e.source) && byId.get(e.target)?.kind === "PersistentVolumeClaim").map((e) => e.target))
  const claims = nodes.filter((n) => claimIds.has(n.id))
  const scalers = new Map(nodes.flatMap((n) => (n.scaler ? [[n.scaler.target, n.scaler] as const] : [])))

  // Namespaces in the order their objects come, data tiers only where data stores live.
  const nsOf = (n: KubeNode) => n.namespace || "default"
  const drawn = [...entries, ...workloads, ...standaloneServices, ...claims]
  const namespaces = [...new Set(drawn.map(nsOf))]
  const dataNamespaces = new Set([...workloads.filter((w) => isData.get(w.id)).map(nsOf), ...claims.map(nsOf)])

  model.groups.push(clusterGroup(wf, status))
  for (const ns of namespaces) {
    model.groups.push({
      id: namespaceGroupId(ns),
      parent: KUBE_CLUSTER_GROUP,
      kind: "k8s-namespace",
      label: ns,
      sublabel: ["namespace"],
      nodeId: byId.has(`Namespace/${ns}`) ? `Namespace/${ns}` : undefined,
    })
    if (dataNamespaces.has(ns)) model.groups.push({ id: dataTierGroupId(ns), parent: namespaceGroupId(ns), kind: "k8s-data", label: "Data", sublabel: ["stateful stores & volumes"] })
  }

  const card = (n: KubeNode, parent: string, category: ArchCard["category"], details: string[]): ArchCard => ({
    id: n.id,
    parent,
    nodeId: n.id,
    type: n.kind,
    kind: "resource",
    category,
    name: n.name,
    details,
  })
  for (const n of entries) model.cards.push(card(n, namespaceGroupId(nsOf(n)), "networking", entryFacts(n, status)))
  for (const s of standaloneServices) model.cards.push(card(s, namespaceGroupId(nsOf(s)), "networking", standaloneServiceFacts(s)))
  for (const w of workloads) {
    const data = isData.get(w.id)
    const facts = workloadFacts(w, byId, graphEdges, services, selected, scalers.get(w.id), status)
    model.cards.push(card(w, data ? dataTierGroupId(nsOf(w)) : namespaceGroupId(nsOf(w)), data ? "database" : "containers", facts))
  }
  for (const c of claims) model.cards.push(card(c, dataTierGroupId(nsOf(c)), "storage", claimFacts(c)))

  // A drawn object stands for itself; a folded Service for the workloads it selects.
  const cardIds = new Set(model.cards.map((c) => c.id))
  const drawnFor = (id: string) => (cardIds.has(id) ? [id] : (selected.get(id) ?? []))

  const exposed = services.filter((s) => Object.hasOwn(EXPOSED_TYPES, s.service!.type))
  const users = [...new Set([...entries.map((n) => n.id), ...exposed.flatMap((s) => drawnFor(s.id))])]
  if (users.length) {
    const sublabel = [entries.length ? "ingress" : "", exposed.length ? "LoadBalancer/NodePort" : ""].filter(Boolean).join(" · ")
    model.actors.push({ id: USERS_ACTOR, kind: "users", label: "Users", sublabel })
    for (const id of users) model.edges.push({ id: `${USERS_ACTOR}->${id}`, source: USERS_ACTOR, target: id, kind: "data" })
  }

  const seen = new Set<string>()
  for (const e of graphEdges) {
    if (!REQUEST_EDGES[e.kind] || !cardIds.has(e.source)) continue
    for (const target of drawnFor(e.target)) {
      const id = `${e.source}->${target}`
      if (target === e.source || seen.has(id)) continue
      seen.add(id)
      model.edges.push({ id, source: e.source, target, kind: "data" })
    }
  }
  return model
}

/** StatefulSets and workloads running a database, cache, queue or search engine image. */
function isDataStore(n: KubeNode) {
  if (n.kind === "StatefulSet") return true
  return (n.workload?.containers ?? []).some((c) => DATA_CATEGORIES[techIconFor({ image: c.image, kind: "service" }).category])
}

/** "Local cluster", with the k3s version, the cluster name and its state when it is not running. */
function clusterGroup(wf: KubeWorkflow, status: KubeStatus | null | undefined): ArchGroup {
  const cluster = status?.cluster
  const state = !cluster ? "" : cluster.state === "running" ? "" : cluster.state === "absent" ? "not created" : cluster.state
  const facts = [["k3s", cluster?.version ?? ""].filter(Boolean).join(" "), cluster?.name || `nimbo-k8s-${wf.id}`, state]
  return { id: KUBE_CLUSTER_GROUP, kind: "k8s-cluster", label: "Local cluster", sublabel: [facts.filter(Boolean).join(" · ")] }
}

/** Live URLs of the entry when the cluster runs, then its rules: "shop.local/api → api:80 (TLS)". */
function entryFacts(n: KubeNode, status: KubeStatus | null | undefined) {
  const urls = (status?.ingresses ?? []).filter((i) => i.nodeId === n.id).map((i) => i.url)
  const rules = (n.routes ?? []).map((r) => {
    const target = `${r.service}${r.port ? `:${r.port}` : ""}`
    return `${r.host || "*"}${r.path || "/"} → ${target}${r.tls ? " (TLS)" : ""}`
  })
  const classes = [...new Set((n.routes ?? []).map((r) => r.class).filter(Boolean))].map((c) => `class ${c}`)
  return [...new Set([...urls, ...rules, ...classes])]
}

/** "svc web :80→8080", "svc web LoadBalancer :80→8080 :30080", "svc db headless :5432". */
function serviceFact(s: KubeNode) {
  const svc = s.service!
  const ports = svc.ports.map((p) => {
    const target = p.targetPort && p.targetPort !== String(p.port) ? `→${p.targetPort}` : ""
    return `:${p.port}${target}${p.nodePort ? ` (node ${p.nodePort})` : ""}`
  })
  const type = svc.headless ? "headless" : svc.type && svc.type !== "ClusterIP" ? svc.type : ""
  return ["svc", s.name, type, ...ports].filter(Boolean).join(" ")
}

/** A Service that selects no workload: its type, ports and where it points. */
function standaloneServiceFacts(s: KubeNode) {
  const svc = s.service!
  return [
    serviceFact(s).replace(/^svc \S+ ?/, "") || svc.type,
    svc.externalName ? `→ ${svc.externalName}` : "",
    svc.unmatched ? "selects no pods" : "",
  ].filter(Boolean)
}

/** "pvc data 1Gi · ReadWriteOnce · local-path". */
function claimFacts(c: KubeNode) {
  const st = c.storage
  return [st?.size ?? "", (st?.accessModes ?? []).join(", "), st?.storageClass ?? ""].filter(Boolean)
}

/**
 * A workload card's lines, most telling first (cards show two lines; the tooltip has them all):
 * images, replicas or autoscaling, rollout state, the Services selecting it, volume claims, then
 * the ConfigMaps and Secrets it reads.
 */
function workloadFacts(
  n: KubeNode,
  byId: Map<string, KubeNode>,
  edges: KubeEdge[],
  services: KubeNode[],
  selected: Map<string, string[]>,
  scaler: KubeScaler | undefined,
  status: KubeStatus | null | undefined,
) {
  const w = n.workload!
  const live = status?.workloads.find((s) => s.nodeId === n.id)
  const used = (kind: "ConfigMap" | "Secret", label: string) =>
    edges
      .filter((e) => (e.kind === "env" || e.kind === "envFrom" || e.kind === "volume") && e.source === n.id && byId.get(e.target)?.kind === kind)
      .map((e) => `${label} ${byId.get(e.target)!.name}`)
  return [
    ...new Set([
      w.containers.map((c) => c.image).join(", "),
      scalingFact(n, scaler),
      liveFact(live),
      ...services.filter((s) => selected.get(s.id)?.includes(n.id)).map(serviceFact),
      ...(w.volumeClaimTemplates ?? []).map((t) => ["pvc", t.name, t.size ?? "", t.storageClass ?? ""].filter(Boolean).join(" ")),
      ...(w.volumes ?? []).filter((v) => v.kind === "persistentVolumeClaim").map((v) => `pvc ${v.source ?? v.name}`),
      ...used("ConfigMap", "cm"),
      ...used("Secret", "secret"),
    ]),
  ].filter(Boolean)
}

/** "HPA 2–10 · cpu 70%", "×3", "schedule 0 3 * * *", "every node". */
function scalingFact(n: KubeNode, scaler: KubeScaler | undefined) {
  if (scaler) {
    const targets = [scaler.cpu !== undefined ? `cpu ${scaler.cpu}%` : "", scaler.memory !== undefined ? `mem ${scaler.memory}%` : ""].filter(Boolean)
    return [`HPA ${scaler.minReplicas}–${scaler.maxReplicas}`, ...targets].join(" · ")
  }
  if (n.workload?.schedule) return `schedule ${n.workload.schedule}`
  if (n.kind === "DaemonSet") return "every node"
  const replicas = n.workload?.replicas
  return replicas !== undefined && replicas !== 1 ? `×${replicas}` : ""
}

/** "2/3 ready", "starting · 0/1 ready · ImagePullBackOff", "not running". */
function liveFact(live: KubeStatus["workloads"][number] | undefined) {
  if (!live) return ""
  if (live.state === "absent") return "not running"
  const reason = live.pods.find((p) => p.reason)?.reason ?? ""
  return [live.state === "running" ? "" : live.state, `${live.ready}/${live.desired} ready`, reason].filter(Boolean).join(" · ")
}

/**
 * Icon and text of a card: a workload is the technology its first container runs (the object's
 * kind icon when the image is unknown); entries, Services and claims are their kind.
 */
export function kubeCardScene(card: ArchCard, node: KubeNode | undefined): { icon: string; text: CardText } {
  const container = node?.workload?.containers[0]
  const tech = node?.workload ? techIconFor({ image: container?.image, stack: node.stack, kind: "service" }) : undefined
  const kind = k8sIconFor(node?.kind ?? card.type)
  return {
    icon: tech && !tech.generic ? tech.src : kind.src,
    text: {
      title: card.name,
      lines: cardLines([card.type, ...card.details]),
      tooltip: [`${card.name} (${card.type})`, card.details.join("\n"), card.nodeId].join("\n"),
    },
  }
}

/** Label icon of a container: Kubernetes on the cluster, the namespace and volume icons inside. */
export function kubeGroupIconOf(g: ArchGroup): string | undefined {
  if (g.kind === "k8s-cluster") return techIcon("kubernetes").src
  if (g.kind === "k8s-namespace") return k8sIcon("ns").src
  if (g.kind === "k8s-data") return k8sIcon("pvc").src
  return undefined
}

/** "Kubernetes · Kustomize · prod": the source kinds and the environment the graph was rendered for. */
export function kubeSubtitle(wf: Pick<Workflow, "kubernetes">) {
  const kinds = [...new Set((wf.kubernetes?.sources ?? []).map((s) => SOURCE_LABEL[s.kind]))]
  return ["Kubernetes", ...kinds, wf.kubernetes?.environment ?? ""].filter(Boolean).join(" · ")
}

export function kubeLegend(model: ArchModel): LegendEntry[] {
  const out: LegendEntry[] = [{ kind: "data", label: "Requests, routes & volumes" }]
  if (model.edges.some((e) => e.kind === "control")) out.push({ kind: "control", label: "Control" })
  return out
}
