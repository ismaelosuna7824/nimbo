// The Build canvas of a Docker workspace, as plain data: which compose graph
// nodes become cards, which networks become frames around their services,
// where everything is and which edges are drawn. Kept free of React so it can
// be tested.
//
// Networks are frames: a service sits in the frame of its first network
// (services without `networks:` share the implicit `default` network, drawn
// as a frame too); membership in further networks is an edge. Build stages
// are folded into their service's card. Edges point from the user to what it
// uses (api → db for `api.depends_on: [db]`), the direction people draw and
// read them; the graph stores the opposite (source is used by target).

import type { ComposeEdge, ComposeGraph, ComposeNode, ComposeServiceStatus, ComposeStatus, Position } from "@/lib/api/types"

export const SERVICE_W = 272
export const SERVICE_H = 104
/** Extra height of a card showing its Dockerfile stages. */
export const STAGES_H = 24
export const SMALL_W = 196
export const SMALL_H = 56
export const FRAME_HEADER = 40
export const FRAME_PAD = 20
/** The implicit network of services that declare none. */
export const DEFAULT_NETWORK = "network:default"

export type CardKind = "service" | "volume" | "secret" | "config" | "external" | "frame"

/** What frame wrapping, placement and layout read of a card (the Kubernetes canvas shares them). */
export interface FrameItem {
  id: string
  /** "frame" for frames; anything else is a card. */
  kind: string
  /** The graph node (absent for implicit frames). */
  node?: unknown
  parent?: string
  x: number
  y: number
  w: number
  h: number
  placed: boolean
}

export interface CanvasItem {
  id: string
  kind: CardKind
  /** The graph node (absent for the implicit default network frame). */
  node?: ComposeNode
  /** Frame id the item is drawn in. */
  parent?: string
  /** Absolute rectangle. */
  x: number
  y: number
  w: number
  h: number
  /** Saved by the user (or a previous layout). */
  placed: boolean
  /** Build stages of a service card, in Dockerfile order. */
  stages?: ComposeNode[]
  /** Frames: the network is external (created outside this project). */
  external?: boolean
  status?: ComposeServiceStatus
}

export interface CanvasEdge {
  id: string
  /** The user (drawn as the edge's start). */
  source: string
  /** What it uses. */
  target: string
  kind: ComposeEdge["kind"]
  label?: string
  /** Both ends are running containers. */
  live: boolean
}

export interface DockerCanvas {
  items: CanvasItem[]
  edges: CanvasEdge[]
}

const isFrameNode = (n: ComposeNode) => n.kind === "network" || (n.kind === "external" && n.externalKind === "network")

function cardKind(n: ComposeNode): CardKind | null {
  if (isFrameNode(n)) return "frame"
  switch (n.kind) {
    case "service":
      return "service"
    case "volume":
    case "secret":
    case "config":
    case "external":
      return n.kind
    default:
      return null
  }
}

/** Frame of a service: its first declared network that has a node, else the implicit default network. */
export function frameOf(svc: ComposeNode, frames: Set<string>): string | undefined {
  const nets = svc.service?.networks ?? []
  for (const name of nets) if (frames.has(`network:${name}`)) return `network:${name}`
  return nets.length === 0 ? DEFAULT_NETWORK : undefined
}

const CONDITION_LABEL: Record<string, string> = {
  service_started: "started",
  service_healthy: "healthy",
  service_completed_successfully: "completed",
}

export function edgeLabel(e: ComposeEdge): string | undefined {
  switch (e.kind) {
    case "depends_on":
      return CONDITION_LABEL[e.condition ?? e.label ?? ""] ?? e.condition ?? e.label ?? "started"
    case "inferred":
      return e.envKey ?? e.label
    case "volume":
    case "config":
    case "secret":
      return e.path ? `${e.path}${e.readOnly ? " (ro)" : ""}` : e.label
    case "links":
      return "link"
    case "extends":
      return "extends"
    case "network":
      return "network"
    default:
      return e.label
  }
}

function serviceRunning(s: ComposeServiceStatus | undefined) {
  return s?.state === "running"
}

/**
 * Cards, frames and edges of the canvas. Items without a saved position get
 * `placed: false` and a provisional spot (see placeNew); frames wrap their
 * children.
 */
export function buildCanvas(graph: ComposeGraph, status?: ComposeStatus | null): DockerCanvas {
  const statusOf = new Map((status?.services ?? []).map((s) => [s.nodeId || `service:${s.name}`, s]))
  const frames = new Set(graph.nodes.filter(isFrameNode).map((n) => n.id))
  const stagesOf = new Map<string, ComposeNode[]>()
  for (const n of graph.nodes) {
    if (n.kind !== "build-stage" || !n.parent) continue
    stagesOf.set(n.parent, [...(stagesOf.get(n.parent) ?? []), n])
  }
  for (const list of stagesOf.values()) list.sort((a, b) => (a.stage?.index ?? 0) - (b.stage?.index ?? 0))

  const leaves: CanvasItem[] = []
  const frameItems = new Map<string, CanvasItem>()
  for (const n of graph.nodes) {
    const kind = cardKind(n)
    if (!kind) continue
    if (kind === "frame") {
      frameItems.set(n.id, { id: n.id, kind, node: n, x: n.position?.x ?? 0, y: n.position?.y ?? 0, w: SMALL_W, h: SMALL_H, placed: !!n.position, external: n.kind === "external" || n.external })
      continue
    }
    const stages = kind === "service" ? (stagesOf.get(n.id) ?? []) : undefined
    const service = kind === "service"
    leaves.push({
      id: n.id,
      kind,
      node: n,
      parent: service ? frameOf(n, frames) : undefined,
      x: n.position?.x ?? 0,
      y: n.position?.y ?? 0,
      w: service ? SERVICE_W : SMALL_W,
      h: service ? SERVICE_H + (stages && stages.length > 1 ? STAGES_H : 0) : SMALL_H,
      placed: !!n.position,
      stages,
      status: service ? statusOf.get(n.id) : undefined,
    })
  }
  if (leaves.some((l) => l.parent === DEFAULT_NETWORK)) {
    frameItems.set(DEFAULT_NETWORK, { id: DEFAULT_NETWORK, kind: "frame", x: 0, y: 0, w: SMALL_W, h: SMALL_H, placed: true })
  }

  const items = [...frameItems.values(), ...leaves]
  const ids = new Set(items.map((i) => i.id))
  const parentOf = new Map(leaves.flatMap((l) => (l.parent ? [[l.id, l.parent] as const] : [])))
  const running = new Set(leaves.filter((l) => serviceRunning(l.status)).map((l) => l.id))
  const edges: CanvasEdge[] = []
  for (const e of graph.edges) {
    if (!ids.has(e.source) || !ids.has(e.target)) continue
    // Membership of the network a service is drawn in is the frame itself.
    if (e.kind === "network" && parentOf.get(e.target) === e.source) continue
    if (e.kind === "build" || e.kind === "copy_from") continue
    edges.push({
      id: e.id,
      source: e.target,
      target: e.source,
      kind: e.kind,
      label: edgeLabel(e),
      live: running.has(e.source) && running.has(e.target),
    })
  }
  wrapFrames(items)
  return { items, edges }
}

/** Grows every frame around its children (frames without children keep their own spot and size). */
export function wrapFrames(items: FrameItem[]) {
  const kids = new Map<string, FrameItem[]>()
  for (const i of items) if (i.parent) kids.set(i.parent, [...(kids.get(i.parent) ?? []), i])
  for (const f of items) {
    if (f.kind !== "frame") continue
    const list = kids.get(f.id)
    if (!list?.length) {
      f.w = SMALL_W
      f.h = SMALL_H
      continue
    }
    const minX = Math.min(...list.map((k) => k.x))
    const minY = Math.min(...list.map((k) => k.y))
    const maxX = Math.max(...list.map((k) => k.x + k.w))
    const maxY = Math.max(...list.map((k) => k.y + k.h))
    f.x = minX - FRAME_PAD
    f.y = minY - FRAME_HEADER
    f.w = maxX - minX + 2 * FRAME_PAD
    f.h = maxY - minY + FRAME_HEADER + FRAME_PAD
    f.placed = list.every((k) => k.placed)
  }
}

/** Gap kept between a newly placed card and its neighbours. */
const PLACE_GAP = 40

/**
 * Cards placed on their own: services, volumes, secrets, configs, external items and network frames without
 * members (a frame with members wraps them instead).
 */
export function looseItems<T extends FrameItem>(items: T[]): T[] {
  const withKids = new Set(items.flatMap((i) => (i.parent ? [i.parent] : [])))
  return items.filter((i) => i.kind !== "frame" || (!!i.node && !withKids.has(i.id)))
}

type Rect = { x: number; y: number; w: number; h: number }

const overlaps = (a: Rect, b: Rect) =>
  a.x < b.x + b.w + PLACE_GAP && b.x < a.x + a.w + PLACE_GAP && a.y < b.y + b.h + PLACE_GAP && b.y < a.y + a.h + PLACE_GAP

/**
 * Spots for items added since the last layout (palette clicks, code edits, agents). A service joins its frame below
 * the members already there; an item another card uses or is used by (a catalog image's data volume) goes to the
 * right of that card's frame; anything else starts a row below the drawing. Nothing lands on another card. Returns
 * the new absolute positions by id.
 */
export function placeNew<T extends FrameItem>(
  items: T[],
  edges: { source: string; target: string }[] = [],
  /** Cards placed first, so the ones that follow them can sit next to them. */
  primary: (i: T) => boolean = (i) => i.kind === "service",
): Map<string, Position> {
  const out = new Map<string, Position>()
  const loose = looseItems(items)
  const placed = loose.filter((i) => i.placed)
  const fresh = loose.filter((i) => !i.placed).sort((a, b) => Number(primary(b)) - Number(primary(a)))
  if (fresh.length === 0) return out
  const byId = new Map(items.map((i) => [i.id, i]))

  // A frame's rectangle from its placed members (frames move with what they hold).
  const frameRect = (id: string): Rect | undefined => {
    const kids = placed.filter((p) => p.parent === id)
    if (!kids.length) return undefined
    const x = Math.min(...kids.map((k) => k.x)) - FRAME_PAD
    const y = Math.min(...kids.map((k) => k.y)) - FRAME_HEADER
    return { x, y, w: Math.max(...kids.map((k) => k.x + k.w)) + FRAME_PAD - x, h: Math.max(...kids.map((k) => k.y + k.h)) + FRAME_PAD - y }
  }
  const neighbour = (i: T) => {
    for (const e of edges) {
      const other = e.source === i.id ? e.target : e.target === i.id ? e.source : undefined
      const item = other ? byId.get(other) : undefined
      if (item?.placed && item.kind !== "frame") return item
    }
    return undefined
  }
  let rowX = placed.length ? Math.min(...placed.map((i) => i.x)) : 0

  for (const i of fresh) {
    const siblings = i.parent ? placed.filter((p) => p.parent === i.parent) : []
    const near = siblings.length ? undefined : neighbour(i)
    const frames = [...new Set(placed.flatMap((p) => (p.parent ? [p.parent] : [])))].flatMap((f) => frameRect(f) ?? [])
    let pos: Position
    if (siblings.length) {
      pos = { x: Math.min(...siblings.map((s) => s.x)), y: Math.max(...siblings.map((s) => s.y + s.h)) + PLACE_GAP }
    } else if (near) {
      const anchor = (near.parent && frameRect(near.parent)) || near
      // Line up with the cards already right of that frame (the volumes column of the layered layout).
      const right = anchor.x + anchor.w
      const column = placed.filter((p) => !p.parent && p.x >= right).map((p) => p.x)
      pos = { x: column.length ? Math.min(...column) : right + 96, y: near.y }
    } else {
      const bottom = Math.max(0, ...placed.filter((p) => !p.parent).map((p) => p.y + p.h), ...frames.map((f) => f.y + f.h))
      pos = { x: rowX, y: bottom + FRAME_HEADER + 96 }
      rowX += i.w + 64
    }
    // Move down past whatever is in the way: placed cards, and for top-level items the frames too.
    const obstacles: Rect[] = i.parent ? placed : [...placed, ...frames]
    for (let guard = 0; guard < 200; guard++) {
      const hit = obstacles.find((o) => overlaps({ ...pos, w: i.w, h: i.h }, o))
      if (!hit) break
      pos = { x: pos.x, y: hit.y + hit.h + PLACE_GAP }
    }
    out.set(i.id, pos)
    i.x = pos.x
    i.y = pos.y
    i.placed = true
    placed.push(i)
  }
  return out
}

/** Absolute item rectangle → React Flow position relative to its frame. */
export function relativePosition(item: FrameItem, byId: Map<string, FrameItem>): Position {
  const parent = item.parent ? byId.get(item.parent) : undefined
  return parent ? { x: item.x - parent.x, y: item.y - parent.y } : { x: item.x, y: item.y }
}
