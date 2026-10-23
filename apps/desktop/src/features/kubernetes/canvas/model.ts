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
//
// Charts vendored from a repository (graph.charts: kube-prometheus-stack,
// Bitnami PostgreSQL…) are one release card while collapsed, their objects'
// edges re-attached to it; expanded, a frame holds all their objects
// (namespaced or not) instead of the namespace frames.

import { loadElk } from "@/features/canvas/layout"
import { layoutCanvas } from "@/features/docker/canvas/layout"
import { FRAME_HEADER, FRAME_PAD, wrapFrames, type FrameItem } from "@/features/docker/canvas/model"
import type { KubeChartGroup, KubeEdge, KubeEdgeKind, KubeGraph, KubeNode, KubePod, KubeState, KubeStatus, KubeWorkloadStatus, Position } from "@/lib/api/types"

export { FRAME_HEADER, FRAME_PAD } from "@/features/docker/canvas/model"

export const WORKLOAD_W = 288
export const WORKLOAD_H = 116
export const SMALL_W = 216
export const SMALL_H = 60
export const CHART_W = 304
export const CHART_H = 124
/** Prefix of the frame of a namespace that has no Namespace object in the files. */
export const NAMESPACE_FRAME = "namespace:"

export type KubeCardKind = "workload" | "small" | "frame" | "chart"

/** Objects of one kind in a chart. */
export interface KindCount {
  kind: string
  count: number
  /** Node ids, in graph order. */
  ids: string[]
}

/** Live state of a chart's workloads on the local cluster. */
export interface ChartLive {
  ready: number
  total: number
  /** Workloads failing (error state or a pod in CrashLoopBackOff, OOMKilled…). */
  problems: number
  state: KubeState
}

/** A vendored chart as the canvas, inspector and Architecture view show it. */
export interface ChartSummary {
  group: KubeChartGroup
  /** Most numerous kinds first. */
  kinds: KindCount[]
  members: string[]
  /** Absent while nothing of the chart runs on the cluster. */
  live?: ChartLive
  expanded: boolean
}

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
  /** The rendered object (absent for implicit namespace frames and charts). */
  node?: KubeNode
  /** Frames: the namespace they hold. */
  namespace?: string
  /** A vendored chart: its collapsed release card, or the frame of its expanded objects. */
  chart?: ChartSummary
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

/** "Deployment" → "Deployments", "Ingress" → "Ingresses", "NetworkPolicy" → "NetworkPolicies". */
export function pluralKind(kind: string, count: number): string {
  if (count === 1) return kind
  if (/[^aeiou]y$/.test(kind)) return `${kind.slice(0, -1)}ies`
  if (/(s|x|ch|sh)$/.test(kind)) return `${kind}es`
  return `${kind}s`
}

/** "12 Deployments · 3 StatefulSets · 40 ConfigMaps" (the first `max` kinds, "+N more" after them). */
export function kindsText(kinds: KindCount[], max = kinds.length): string {
  const shown = kinds.slice(0, max).map((k) => `${k.count} ${pluralKind(k.kind, k.count)}`)
  const rest = kinds.length - shown.length
  return [...shown, rest > 0 ? `+${rest} more kind${rest === 1 ? "" : "s"}` : ""].filter(Boolean).join(" · ")
}

/**
 * Ready/total workloads of a chart and how many fail; undefined while none of them is on the cluster. CronJobs are
 * not counted: they only have pods while a run is due.
 */
export function chartLive(members: KubeNode[], statusOf: Map<string, KubeWorkloadStatus>): ChartLive | undefined {
  const workloads = members.filter((n) => n.category === "workload" && n.kind !== "CronJob")
  const statuses = workloads.map((n) => [n, statusOf.get(n.id)] as const)
  if (!statuses.some(([, s]) => s && s.state !== "absent")) return undefined
  let ready = 0
  let problems = 0
  for (const [n, s] of statuses) {
    if (s?.state === "running" || (n.kind === "Job" && (s?.succeeded ?? 0) > 0)) ready++
    if (s?.state === "error" || s?.pods.some((p) => podDot(p).tone === "error")) problems++
  }
  const state: KubeState = problems > 0 ? "error" : ready === workloads.length ? "running" : "starting"
  return { ready, total: workloads.length, problems, state }
}

/** The vendored charts of a graph with their objects by kind and live state. */
export function chartSummaries(graph: KubeGraph, status?: KubeStatus | null, expanded: Iterable<string> = []): ChartSummary[] {
  const open = new Set(expanded)
  const statusOf = new Map((status?.workloads ?? []).map((w) => [w.nodeId, w]))
  const membersOf = new Map<string, KubeNode[]>()
  for (const n of graph.nodes) if (n.chart) membersOf.set(n.chart, [...(membersOf.get(n.chart) ?? []), n])
  return (graph.charts ?? []).flatMap((group) => {
    const members = membersOf.get(group.id) ?? []
    if (members.length === 0) return []
    const byKind = new Map<string, KindCount>()
    for (const n of members) {
      const k = byKind.get(n.kind) ?? { kind: n.kind, count: 0, ids: [] }
      k.count++
      k.ids.push(n.id)
      byKind.set(n.kind, k)
    }
    const kinds = [...byKind.values()].sort((a, b) => b.count - a.count || a.kind.localeCompare(b.kind))
    return [{ group, kinds, members: members.map((n) => n.id), live: chartLive(members, statusOf), expanded: open.has(group.id) }]
  })
}

/**
 * Cards, namespace frames and edges of the canvas. Items without a saved position get `placed: false` (see
 * placeNew); frames wrap their children. Charts listed in `expanded` are frames of their objects; the others are one
 * release card, every edge of their objects attached to it (once per pair of cards).
 */
export function buildKubeCanvas(graph: KubeGraph, status?: KubeStatus | null, expanded: Iterable<string> = []): KubeCanvasModel {
  const statusOf = new Map((status?.workloads ?? []).map((w) => [w.nodeId, w]))
  const nodeOf = new Map(graph.nodes.map((n) => [n.id, n]))
  const charts = new Map(chartSummaries(graph, status, expanded).map((c) => [c.group.id, c]))
  const chartOf = (n: KubeNode) => (n.chart ? charts.get(n.chart) : undefined)
  // A chart's Namespace objects are cards of the chart, not frames.
  const namespaceNodes = new Map(graph.nodes.filter((n) => n.kind === "Namespace" && !chartOf(n)).map((n) => [n.name, n]))

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
  for (const c of charts.values()) {
    const at = c.group.position
    if (c.expanded) frames.set(c.group.id, { id: c.group.id, kind: "frame", chart: c, x: 0, y: 0, w: SMALL_W, h: SMALL_H, placed: true })
    else leaves.push({ id: c.group.id, kind: "chart", chart: c, x: at?.x ?? 0, y: at?.y ?? 0, w: CHART_W, h: CHART_H, placed: !!at })
  }
  for (const n of graph.nodes) {
    const chart = chartOf(n)
    if (chart && !chart.expanded) continue
    if (n.kind === "Namespace" && !chart) continue
    const parent = chart ? chart.group.id : n.namespace ? namespaceFrameId(n.namespace, namespaceNodes) : undefined
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
  const live = new Set(graph.nodes.filter((n) => n.category === "workload" && statusOf.get(n.id)?.state === "running").map((n) => n.id))
  for (const e of graph.edges) if (e.kind === "selects" && live.has(e.target)) live.add(e.source)
  for (const e of graph.edges) if (e.kind === "routes" && live.has(e.target)) live.add(e.source)

  // An object of a collapsed chart is drawn as the chart.
  const shown = (id: string) => {
    const n = nodeOf.get(id)
    const chart = n && chartOf(n)
    return chart && !chart.expanded ? chart.group.id : id
  }
  const edges: KubeCanvasEdge[] = []
  const pairs = new Map<string, KubeCanvasEdge>()
  for (const e of graph.edges) {
    const source = shown(e.source)
    const target = shown(e.target)
    if (!ids.has(source) || !ids.has(target) || source === target) continue
    const traffic = !!TRAFFIC[e.kind] && live.has(e.source) && live.has(e.target)
    if (source === e.source && target === e.target) {
      edges.push({ id: e.id, source, target, kind: e.kind, label: edgeLabel(e), live: traffic })
      continue
    }
    const id = `${source}->${target}`
    const same = pairs.get(id)
    if (same) {
      // Several references between the same two cards: one edge, labelled only when they agree.
      if (same.kind !== e.kind || same.label !== edgeLabel(e)) same.label = undefined
      same.live ||= traffic
      continue
    }
    const edge: KubeCanvasEdge = { id, source, target, kind: e.kind, label: edgeLabel(e), live: traffic }
    pairs.set(id, edge)
    edges.push(edge)
  }
  wrapFrames(items)
  return { items, edges }
}

type Rect = { x: number; y: number; w: number; h: number }

/** Space kept around an expanded chart's frame. */
const CHART_GAP = 48

const clear = (a: Rect, obstacles: Rect[]) =>
  !obstacles.some((b) => a.x < b.x + b.w + CHART_GAP && b.x < a.x + a.w + CHART_GAP && a.y < b.y + b.h + CHART_GAP && b.y < a.y + a.h + CHART_GAP)

/**
 * Where the objects of an expanded chart go: `members` are their card rectangles (as saved, or a fresh layout).
 * The chart's frame takes the collapsed card's spot (`anchor`) when it is free, else stays where the cards were saved
 * when that is free (never for a fresh layout), else starts a row below the drawing. `items` is the canvas with the
 * chart expanded. Returns the members' new absolute positions (empty: nothing moves).
 */
export function placeChartFrame(items: FrameItem[], chartId: string, members: Map<string, Rect>, anchor?: Position, fresh = false): Map<string, Position> {
  const out = new Map<string, Position>()
  const rects = [...members.values()]
  if (rects.length === 0) return out
  const minX = Math.min(...rects.map((r) => r.x))
  const minY = Math.min(...rects.map((r) => r.y))
  const frame: Rect = {
    x: minX - FRAME_PAD,
    y: minY - FRAME_HEADER,
    w: Math.max(...rects.map((r) => r.x + r.w)) - minX + 2 * FRAME_PAD,
    h: Math.max(...rects.map((r) => r.y + r.h)) - minY + FRAME_HEADER + FRAME_PAD,
  }
  const withKids = new Set(items.flatMap((i) => (i.parent ? [i.parent] : [])))
  const obstacles = items.filter((i) => !i.parent && i.id !== chartId && (i.kind !== "frame" || withKids.has(i.id) || !!i.node))
  const bottom = obstacles.length ? Math.max(...obstacles.map((o) => o.y + o.h)) + 96 : 0
  const left = obstacles.length ? Math.min(...obstacles.map((o) => o.x)) : 0
  const candidates = [anchor, fresh ? undefined : frame, { x: left, y: bottom }].filter((c): c is Position => !!c)
  const target = candidates.find((c) => clear({ ...frame, x: c.x, y: c.y }, obstacles)) ?? { x: left, y: bottom }
  const dx = Math.round(target.x - frame.x)
  const dy = Math.round(target.y - frame.y)
  if (dx === 0 && dy === 0 && !fresh) return out
  for (const [id, r] of members) out.set(id, { x: Math.round(r.x + dx), y: Math.round(r.y + dy) })
  return out
}

/** Layered layout options of a chart's objects: no model order (it lines unconnected objects up in a staircase). */
const CHART_LAYOUT = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.layered.spacing.nodeNodeBetweenLayers": "96",
  "elk.spacing.nodeNode": "32",
  "elk.spacing.componentComponent": "48",
  "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
  "elk.aspectRatio": "1.6",
}

/** Category order of a chart's objects in its layout: workloads first, cluster plumbing last. */
const CATEGORY_ORDER: Record<string, number> = { workload: 0, network: 1, autoscaling: 2, storage: 3, config: 4, rbac: 5, cluster: 6, custom: 7 }

/**
 * A chart's objects laid out on their own (connected ones layered left → right, the rest packed by category and
 * kind), as rectangles from the origin.
 */
export async function layoutMembers(members: KubeCanvasItem[], edges: KubeCanvasEdge[]): Promise<Map<string, Rect>> {
  const ids = new Set(members.map((m) => m.id))
  const rank = (m: KubeCanvasItem) => CATEGORY_ORDER[m.node?.category ?? ""] ?? 8
  const sorted = [...members].sort((a, b) => rank(a) - rank(b) || (a.node?.kind ?? "").localeCompare(b.node?.kind ?? "") || a.id.localeCompare(b.id))
  const seen = new Set<string>()
  const elkEdges = edges.flatMap((e) => {
    const key = `${e.source}->${e.target}`
    if (!ids.has(e.source) || !ids.has(e.target) || e.source === e.target || seen.has(key)) return []
    seen.add(key)
    return [{ id: key, sources: [e.source], targets: [e.target] }]
  })
  const result = await (await loadElk()).layout({
    id: "__chart",
    layoutOptions: CHART_LAYOUT,
    children: sorted.map((m) => ({ id: m.id, width: m.w, height: m.h })),
    edges: elkEdges,
  })
  const byId = new Map(members.map((m) => [m.id, m]))
  return new Map(
    (result.children ?? []).map((c) => [c.id, { x: Math.round(c.x ?? 0), y: Math.round(c.y ?? 0), w: byId.get(c.id)!.w, h: byId.get(c.id)!.h }]),
  )
}

/**
 * Positions that make an expanded chart tidy: its objects laid out (layoutMembers) when none has a position yet,
 * then the frame moved to a free spot (see placeChartFrame). Empty when nothing moves, or when only some objects
 * lack a position (placeNew puts those next to their siblings).
 */
export async function layoutChart(canvas: KubeCanvasModel, chartId: string, anchor?: Position): Promise<Map<string, Position>> {
  const members = canvas.items.filter((i) => i.parent === chartId)
  const fresh = members.every((m) => !m.placed)
  if (!fresh && members.some((m) => !m.placed)) return new Map()
  const rects = fresh ? await layoutMembers(members, canvas.edges) : new Map<string, Rect>(members.map((m) => [m.id, m]))
  return placeChartFrame(canvas.items, chartId, rects, anchor, fresh)
}

/**
 * Left→right layered layout of the whole canvas (Tidy up, first layout). An expanded chart is laid out on its own
 * (layoutMembers) and placed as one block, like its collapsed card, so its many objects don't stretch the user's
 * drawing. Returns absolute positions of cards, collapsed charts and expanded charts' frames (their cards' spot).
 */
export async function layoutKubeCanvas(graph: KubeGraph, status: KubeStatus | null | undefined, expanded: Iterable<string>): Promise<Map<string, Position>> {
  const open = buildKubeCanvas(graph, status, expanded)
  const blocks = new Map<string, { rects: Map<string, Rect>; x: number; y: number; w: number; h: number }>()
  for (const f of open.items) {
    if (f.kind !== "frame" || !f.chart) continue
    const rects = await layoutMembers(
      open.items.filter((i) => i.parent === f.id),
      open.edges,
    )
    const all = [...rects.values()]
    const x = Math.min(...all.map((r) => r.x))
    const y = Math.min(...all.map((r) => r.y))
    const w = Math.max(...all.map((r) => r.x + r.w)) - x + 2 * FRAME_PAD
    const h = Math.max(...all.map((r) => r.y + r.h)) - y + FRAME_HEADER + FRAME_PAD
    blocks.set(f.id, { rects, x, y, w, h })
  }
  // Every chart as a card (expanded ones as big as their frame); then their objects into their block.
  const closed = buildKubeCanvas(graph, status)
  const items = closed.items.map((i) => {
    const b = blocks.get(i.id)
    return b ? { ...i, w: b.w, h: b.h } : i
  })
  const out = await layoutCanvas({ items, edges: closed.edges })
  for (const [id, b] of blocks) {
    const at = out.get(id)
    if (!at) continue
    for (const [member, r] of b.rects) out.set(member, { x: at.x + FRAME_PAD + r.x - b.x, y: at.y + FRAME_HEADER + r.y - b.y })
  }
  return out
}

/**
 * Adds to `positions` (new absolute positions of cards) the spot of every expanded chart frame some of whose objects
 * move: the chart's card goes there when it is collapsed again.
 */
export function withChartSpots(canvas: KubeCanvasModel, positions: Map<string, Position>): Map<string, Position> {
  for (const f of canvas.items) {
    if (f.kind !== "frame" || !f.chart) continue
    const kids = canvas.items.filter((i) => i.parent === f.id)
    if (!kids.some((k) => positions.has(k.id))) continue
    const at = kids.map((k) => positions.get(k.id) ?? k)
    positions.set(f.id, { x: Math.min(...at.map((p) => p.x)) - FRAME_PAD, y: Math.min(...at.map((p) => p.y)) - FRAME_HEADER })
  }
  return positions
}
