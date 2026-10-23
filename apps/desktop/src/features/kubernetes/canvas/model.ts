// The Build canvas of a Kubernetes workspace, as plain data: which rendered
// objects become cards, which namespaces become frames around their objects,
// the live state of each workload and which edges are drawn. Kept free of
// React so it can be tested.
//
// Namespaces are frames (like Docker networks): every namespaced object sits
// in the frame of its namespace; cluster-scoped objects (ClusterRole,
// StorageClass…) sit outside. A Namespace object in the files is the frame
// itself. Edges keep the graph's direction: the source refers to the target
// (Service → workload it selects, workload → ConfigMap it mounts), the same
// direction a drag between two cards connects.

import { wrapFrames, type FrameItem } from "@/features/docker/canvas/model"
import type { KubeEdge, KubeEdgeKind, KubeGraph, KubeNode, KubePod, KubeStatus, KubeWorkloadStatus } from "@/lib/api/types"

export { FRAME_HEADER, FRAME_PAD } from "@/features/docker/canvas/model"

export const WORKLOAD_W = 288
export const WORKLOAD_H = 116
export const SMALL_W = 216
export const SMALL_H = 60
/** Prefix of the frame of a namespace that has no Namespace object in the files. */
export const NAMESPACE_FRAME = "namespace:"

export type KubeCardKind = "workload" | "small" | "frame"

/** One pod of a workload as a coloured dot. */
export interface PodDot {
  name: string
  tone: "ok" | "pending" | "error" | "done"
  /** "web-7d9f: Running · ready · 2 restarts". */
  label: string
  /** Why it isn't fine: CrashLoopBackOff, OOMKilled, ImagePullBackOff… */
  reason?: string
}

export interface KubeCanvasItem extends FrameItem {
  kind: KubeCardKind
  /** The rendered object (absent for implicit namespace frames). */
  node?: KubeNode
  /** Frames: the namespace they hold. */
  namespace?: string
  /** Workloads: live state from the local cluster. */
  status?: KubeWorkloadStatus
  pods?: PodDot[]
  /** Workloads scaled by a HorizontalPodAutoscaler. */
  hpa?: { id: string; min: number; max: number }
  /** Small cards: one line of facts ("ClusterIP · 80→8080"). */
  facts?: string
  /** A Service whose selector matches no pod template. */
  unmatched?: boolean
}

export interface KubeCanvasEdge {
  id: string
  /** The object that refers (drawn as the edge's start). */
  source: string
  /** What it refers to. */
  target: string
  kind: KubeEdgeKind
  label?: string
  /** Traffic flows: both ends serve running pods. */
  live: boolean
}

export interface KubeCanvasModel {
  items: KubeCanvasItem[]
  edges: KubeCanvasEdge[]
}

/** Frame id of a namespace: its Namespace object when the files have one, else an implicit frame. */
export function namespaceFrameId(ns: string, namespaceNodes: Map<string, KubeNode>): string {
  return namespaceNodes.get(ns)?.id ?? `${NAMESPACE_FRAME}${ns}`
}

const ERROR_REASON = /backoff|oom|err|fail|invalid|unschedulable|evicted|deadline/i

/** Colour and tooltip of one pod. */
export function podDot(p: KubePod): PodDot {
  const oom = p.containers.some((c) => c.oomKilled)
  const reason = p.reason ?? (oom ? "OOMKilled" : p.containers.find((c) => c.reason && ERROR_REASON.test(c.reason))?.reason)
  let tone: PodDot["tone"]
  if (p.phase === "Failed" || (reason && ERROR_REASON.test(reason))) tone = "error"
  else if (p.phase === "Succeeded") tone = "done"
  else if (p.phase === "Running" && p.ready) tone = "ok"
  else tone = "pending"
  const parts = [p.phase, p.ready ? "ready" : undefined, reason, p.restarts > 0 ? `${p.restarts} restart${p.restarts === 1 ? "" : "s"}` : undefined]
  return { name: p.name, tone, reason, label: `${p.name}: ${parts.filter(Boolean).join(" · ")}` }
}

/** "3/3", "1/2" (ready of desired) for a workload card; Jobs count completions. */
export function replicasText(node: KubeNode, status?: KubeWorkloadStatus): string | undefined {
  if (node.kind === "CronJob") return node.workload?.schedule
  if (node.kind === "Job") return status ? `${status.succeeded ?? 0} done` : undefined
  if (status && status.state !== "absent") return `${status.ready}/${status.desired}`
  if (node.kind === "DaemonSet" || node.kind === "Pod") return undefined
  return `×${node.workload?.replicas ?? 1}`
}

function servicePorts(n: KubeNode): string {
  return (n.service?.ports ?? [])
    .slice(0, 3)
    .map((p) => {
      const target = p.targetPort && p.targetPort !== String(p.port) ? `→${p.targetPort}` : ""
      const proto = p.protocol && p.protocol !== "TCP" ? `/${p.protocol.toLowerCase()}` : ""
      return `${p.port}${target}${proto}`
    })
    .join(", ")
}

/** One line of facts of a small card. */
export function smallFacts(n: KubeNode): string {
  switch (n.kind) {
    case "Service": {
      const svc = n.service
      if (!svc) return "Service"
      if (svc.type === "ExternalName") return `ExternalName · ${svc.externalName ?? ""}`
      return [svc.headless ? "headless" : svc.type, servicePorts(n)].filter(Boolean).join(" · ")
    }
    case "Ingress":
    case "HTTPRoute": {
      const routes = n.routes ?? []
      if (routes.length === 0) return "no rules"
      const first = routes[0]
      const text = `${first.host ?? "*"}${first.path ?? "/"}`
      return routes.length > 1 ? `${text} +${routes.length - 1}` : text
    }
    case "ConfigMap":
    case "Secret": {
      const keys = n.data?.length ?? 0
      return `${keys} key${keys === 1 ? "" : "s"}`
    }
    case "PersistentVolumeClaim":
    case "PersistentVolume":
      return [n.storage?.size, n.storage?.storageClass].filter(Boolean).join(" · ") || "volume claim"
    case "StorageClass":
      return [n.storage?.provisioner, n.storage?.default ? "default" : undefined].filter(Boolean).join(" · ") || "storage class"
    case "HorizontalPodAutoscaler": {
      const s = n.scaler
      if (!s) return "autoscaler"
      return [`${s.minReplicas}–${s.maxReplicas}`, s.cpu ? `cpu ${s.cpu}%` : undefined, s.memory ? `mem ${s.memory}%` : undefined].filter(Boolean).join(" · ")
    }
    case "PodDisruptionBudget": {
      const d = n.disruption
      if (d?.minAvailable) return `minAvailable ${d.minAvailable}`
      if (d?.maxUnavailable) return `maxUnavailable ${d.maxUnavailable}`
      return "disruption budget"
    }
    default:
      return n.custom ? `${n.kind} · ${n.apiVersion}` : n.kind
  }
}

export function edgeLabel(e: KubeEdge): string | undefined {
  switch (e.kind) {
    case "inferred":
    case "env":
      return e.envKey ?? e.label
    default:
      return e.label
  }
}

/** Edges traffic flows along (animated while their pods run). */
const TRAFFIC: Partial<Record<KubeEdgeKind, true>> = { selects: true, routes: true, inferred: true, governs: true }

/**
 * Cards, namespace frames and edges of the canvas. Items without a saved position get `placed: false` (see
 * placeNew); frames wrap their children.
 */
export function buildKubeCanvas(graph: KubeGraph, status?: KubeStatus | null): KubeCanvasModel {
  const statusOf = new Map((status?.workloads ?? []).map((w) => [w.nodeId, w]))
  const nodeOf = new Map(graph.nodes.map((n) => [n.id, n]))
  const namespaceNodes = new Map(graph.nodes.filter((n) => n.kind === "Namespace").map((n) => [n.name, n]))

  const hpaOf = new Map<string, NonNullable<KubeCanvasItem["hpa"]>>()
  for (const n of graph.nodes) {
    if (n.scaler?.target) hpaOf.set(n.scaler.target, { id: n.id, min: n.scaler.minReplicas, max: n.scaler.maxReplicas })
  }
  for (const e of graph.edges) {
    const s = e.kind === "scales" ? nodeOf.get(e.source)?.scaler : undefined
    if (s && !hpaOf.has(e.target)) hpaOf.set(e.target, { id: e.source, min: s.minReplicas, max: s.maxReplicas })
  }

  const frames = new Map<string, KubeCanvasItem>()
  for (const n of namespaceNodes.values()) {
    frames.set(n.id, { id: n.id, kind: "frame", node: n, namespace: n.name, x: n.position?.x ?? 0, y: n.position?.y ?? 0, w: SMALL_W, h: SMALL_H, placed: !!n.position })
  }
  const leaves: KubeCanvasItem[] = []
  for (const n of graph.nodes) {
    if (n.kind === "Namespace") continue
    const parent = n.namespace ? namespaceFrameId(n.namespace, namespaceNodes) : undefined
    if (parent && !frames.has(parent)) {
      frames.set(parent, { id: parent, kind: "frame", namespace: n.namespace, x: 0, y: 0, w: SMALL_W, h: SMALL_H, placed: true })
    }
    const workload = n.category === "workload"
    const st = workload ? statusOf.get(n.id) : undefined
    leaves.push({
      id: n.id,
      kind: workload ? "workload" : "small",
      node: n,
      parent,
      x: n.position?.x ?? 0,
      y: n.position?.y ?? 0,
      w: workload ? WORKLOAD_W : SMALL_W,
      h: workload ? WORKLOAD_H : SMALL_H,
      placed: !!n.position,
      status: st,
      pods: st ? st.pods.map(podDot) : undefined,
      hpa: workload ? hpaOf.get(n.id) : undefined,
      facts: workload ? undefined : smallFacts(n),
      unmatched: n.kind === "Service" && !!n.service?.unmatched,
    })
  }

  const items = [...frames.values(), ...leaves]
  const ids = new Set(items.map((i) => i.id))

  // Live: running workloads, the Services selecting them, the routes to those Services.
  const live = new Set(leaves.filter((l) => l.status?.state === "running").map((l) => l.id))
  for (const e of graph.edges) if (e.kind === "selects" && live.has(e.target)) live.add(e.source)
  for (const e of graph.edges) if (e.kind === "routes" && live.has(e.target)) live.add(e.source)

  const edges: KubeCanvasEdge[] = []
  for (const e of graph.edges) {
    if (!ids.has(e.source) || !ids.has(e.target) || e.source === e.target) continue
    edges.push({
      id: e.id,
      source: e.source,
      target: e.target,
      kind: e.kind,
      label: edgeLabel(e),
      live: !!TRAFFIC[e.kind] && live.has(e.source) && live.has(e.target),
    })
  }
  wrapFrames(items)
  return { items, edges }
}
