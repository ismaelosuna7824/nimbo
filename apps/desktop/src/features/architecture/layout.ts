import type { ELK, ElkExtendedEdge, ElkNode } from "elkjs/lib/elk.bundled.js"
import {
  ACTOR_H,
  ACTOR_W,
  BORDER_CARD_H,
  BORDER_CARD_W,
  CARD_H,
  CARD_W,
  EMPTY_GROUP_H,
  EMPTY_GROUP_W,
  FONT_FAMILY,
  GROUP_PAD,
  groupHeader,
  Lanes,
  routeOrthogonal,
  textWidth,
  type Point,
  type Rect,
} from "./geometry"
import type { ArchEdge, ArchModel } from "./model"

export type ItemKind = "card" | "group" | "actor"

/** An item placed relative to its parent group. */
export interface LaidItem extends Rect {
  id: string
  kind: ItemKind
  parent?: string
}

/** ELK's placement of a model: items relative to their parents, edge routes in absolute coordinates. */
export interface AutoLayout {
  items: Map<string, LaidItem>
  routes: Map<string, Point[]>
}

export interface DiagramItem extends LaidItem {
  /** Absolute top-left. */
  ax: number
  ay: number
}

export interface DiagramEdge extends ArchEdge {
  /** The orthogonal polyline: ELK's route while it still fits, else one around the cards. */
  route: Point[]
}

/** The diagram as drawn: the automatic layout with groups grown around their content and pinned items placed. */
export interface Diagram {
  /** Parents before children. */
  items: DiagramItem[]
  byId: Map<string, DiagramItem>
  edges: DiagramEdge[]
  /** Absolute bounds of everything but the header and legend. */
  bounds: Rect
}

const SPACING = {
  "elk.layered.spacing.nodeNodeBetweenLayers": "56",
  "elk.spacing.nodeNode": "24",
  "elk.layered.spacing.edgeNodeBetweenLayers": "22",
  "elk.spacing.edgeNode": "16",
  "elk.spacing.edgeEdge": "10",
  "elk.spacing.componentComponent": "36",
  "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
}

// Model order keeps the derived order (edge → compute → data) where edges leave the choice open.
const MODEL_ORDER = "elk.layered.considerModelOrder.strategy"

/**
 * Every container is laid out on its own (flat graphs: elkjs 0.12 crashes on some nested ones with
 * model order). Unconnected children are packed in rows rather than one long column.
 */
const LAYOUT_OPTIONS: Record<string, string> = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.edgeRouting": "ORTHOGONAL",
  "elk.separateConnectedComponents": "true",
  "elk.aspectRatio": "1.6",
  ...SPACING,
  [MODEL_ORDER]: "NODES_AND_EDGES",
}

/** Group label fonts; the label icon, its gaps and the right margin take GROUP_LABEL_EXTRA px. */
export const GROUP_LABEL_FONT = `600 12px ${FONT_FAMILY}`
export const GROUP_SUBLABEL_FONT = `400 11px ${FONT_FAMILY}`
const GROUP_LABEL_EXTRA = 54
/** Room between a border card and the group labels it must not cover. */
const BORDER_GAP = 16
/** Gap between the Internet and the top of the container its gateway is in. */
const ABOVE_GAP = 44

// elkjs is ~1.4 MB: loaded on the first layout (shared with the canvas' chunk).
let elk: Promise<ELK> | undefined

/**
 * Every item of a model with its parent, in model order. Pinned items (a gateway on its VPC's border,
 * the Internet above it) are placed after the automatic layout, not by it.
 */
function modelItems(model: ArchModel) {
  return [
    ...model.groups.map((g) => ({ id: g.id, kind: "group" as const, parent: g.parent, pinned: false })),
    ...model.actors.map((a) => ({ id: a.id, kind: "actor" as const, parent: a.parent, pinned: !!a.above })),
    ...model.cards.map((c) => ({ id: c.id, kind: "card" as const, parent: c.parent, pinned: !!c.onBorder })),
  ]
}

/** What the automatic layout depends on: structure, label sizes and edges (not descriptions). */
export function layoutKey(model: ArchModel) {
  return JSON.stringify([
    modelItems(model).map((i) => [i.id, i.parent ?? "", i.pinned]),
    model.groups.map((g) => [g.label, ...(g.sublabel ?? [])]),
    model.edges.map((e) => e.id),
  ])
}

/** Width a group needs for its label and sublabel lines. */
function groupLabelWidth(label: string, sublabel: readonly string[] | undefined) {
  return Math.max(GROUP_LABEL_EXTRA + textWidth(label, GROUP_LABEL_FONT), ...(sublabel ?? []).map((l) => GROUP_LABEL_EXTRA + textWidth(l, GROUP_SUBLABEL_FONT)))
}

/** Gap between the cards of a packed group. */
const PACK_GAP = 16

/** Columns and rows of a packed group of n cards: at most three rows. */
function packGrid(n: number) {
  const cols = Math.ceil(n / 3)
  return { cols, rows: Math.ceil(n / cols) }
}

function routeOf(e: ElkExtendedEdge, dx: number, dy: number): Point[] | undefined {
  const section = e.sections?.[0]
  return section && [section.startPoint, ...(section.bendPoints ?? []), section.endPoint].map((p) => ({ x: p.x + dx, y: p.y + dy }))
}

/**
 * What each container's own layout sees of the model's edges: one ELK edge per edge between direct
 * children, one stand-in per pair for edges reaching inside them, and the children that come first.
 * A pair joined by data one way and only by dashed lines the other way is laid out along the data
 * (the load balancer's group before the services it feeds, not after the identities they use);
 * children holding an entry the Users reach from outside the container (a Global group with DNS and
 * the load balancer) come first, between the Users and the rest.
 */
export function containerLinks(model: ArchModel) {
  const parentOf = new Map(modelItems(model).flatMap((i) => (i.pinned ? [] : [[i.id, i.parent ?? ""] as const])))
  const usersActors = new Set(model.actors.filter((a) => a.kind === "users").map((a) => a.id))
  /** The direct child of `container` that `id` is (or is inside), if any. */
  const childOf = (id: string, container: string) => {
    let cur: string | undefined = id
    while (cur !== undefined && parentOf.get(cur) !== container) cur = parentOf.get(cur) || undefined
    return cur
  }
  return (container: string) => {
    const ends = model.edges.map((e) => ({ e, s: childOf(e.source, container), t: childOf(e.target, container) }))
    const first = new Set(ends.flatMap(({ e, s, t }) => (usersActors.has(e.source) && !s && t ? [t] : [])))
    const dataPairs = new Set(ends.flatMap(({ e, s, t }) => (s && t && s !== t && e.kind === "data" ? [`${s}->${t}`] : [])))
    const edges = new Map<string, ElkExtendedEdge>()
    for (const { e, s, t } of ends) {
      if (!s || !t || s === t) continue
      const direct = s === e.source && t === e.target
      if (!direct && !dataPairs.has(`${s}->${t}`) && dataPairs.has(`${t}->${s}`)) continue
      const eid = direct ? e.id : `stand-in:${s}->${t}`
      if (!edges.has(eid)) edges.set(eid, { id: eid, sources: [s], targets: [t] })
    }
    return { first, edges: [...edges.values()] }
  }
}

/**
 * Places every item, bottom-up: each container is laid out on its own with its children (groups as
 * the boxes their own layout made), so every group reads as a compact block. An edge to an item
 * inside a child group stands for it as an edge to that group; ELK routes only edges between direct
 * children, and buildDiagram routes the others around the cards. A packed group is a grid of cards.
 */
export async function autoLayout(model: ArchModel): Promise<AutoLayout> {
  const groups = new Map(model.groups.map((g) => [g.id, g]))
  const items = modelItems(model).filter((i) => !i.pinned)
  const kindOf = new Map(items.map((i) => [i.id, i.kind]))
  const children = new Map<string, typeof items>()
  for (const it of items) children.set(it.parent ?? "", [...(children.get(it.parent ?? "") ?? []), it])
  const withBorderCard = new Set(model.cards.flatMap((c) => (c.onBorder && c.parent ? [c.parent] : [])))
  const linksOf = containerLinks(model)

  elk ??= import("elkjs/lib/elk.bundled.js").then(({ default: Elk }) => new Elk())
  const engine = await elk.catch((err: unknown) => {
    elk = undefined // the chunk failed to load: retry on the next layout
    throw err
  })

  const laid = new Map<string, ElkNode>() // container ("" = root) → its layout, children relative to it
  const size = new Map<string, { width: number; height: number }>()
  const layout = async (id: string) => {
    const g = groups.get(id)
    const kids = children.get(id) ?? []
    const header = g ? groupHeader(g.sublabel) : 0
    const labelW = g ? groupLabelWidth(g.label, g.sublabel) : 0
    if (g?.packed) {
      const { cols, rows } = packGrid(kids.length)
      size.set(id, {
        width: Math.ceil(Math.max(labelW, 2 * GROUP_PAD + cols * CARD_W + (cols - 1) * PACK_GAP)),
        height: header + 6 + rows * CARD_H + (rows - 1) * PACK_GAP + GROUP_PAD,
      })
      return
    }
    const { first, edges } = linksOf(id)
    const nodes: ElkNode[] = []
    for (const k of kids) {
      if (k.kind === "group") await layout(k.id)
      nodes.push({
        id: k.id,
        ...(k.kind === "card" ? { width: CARD_W, height: CARD_H } : k.kind === "actor" ? { width: ACTOR_W, height: ACTOR_H } : size.get(k.id)),
        ...(first.has(k.id) ? { layoutOptions: { "elk.layered.layering.layerConstraint": "FIRST" } } : {}),
      })
    }
    const graph: ElkNode = {
      id: id || "__root",
      layoutOptions: { ...LAYOUT_OPTIONS, ...(g ? { "elk.padding": `[top=${header + 6},left=${GROUP_PAD},bottom=${GROUP_PAD},right=${GROUP_PAD}]` } : {}) },
      children: nodes,
      edges,
    }
    const res: ElkNode = await engine.layout(graph).catch(() => {
      // An elkjs fault on some graphs: lay out without the order constraint.
      delete graph.layoutOptions![MODEL_ORDER]
      return engine.layout(graph)
    })
    laid.set(id, res)
    // A border card needs the stretch of top edge past the group's label lines.
    const border = withBorderCard.has(id) ? labelW + BORDER_GAP + BORDER_CARD_W + GROUP_PAD : 0
    size.set(id, {
      width: Math.ceil(Math.max(res.width ?? 0, labelW, border, kids.length ? 0 : EMPTY_GROUP_W)),
      height: Math.ceil(Math.max(res.height ?? 0, kids.length ? 0 : EMPTY_GROUP_H)),
    })
  }
  await layout("")

  const out = new Map<string, LaidItem>()
  for (const [id, res] of laid) {
    for (const c of res.children ?? []) {
      const s = size.get(c.id)
      out.set(c.id, {
        id: c.id,
        kind: kindOf.get(c.id)!,
        parent: id || undefined,
        x: Math.round(c.x ?? 0),
        y: Math.round(c.y ?? 0),
        width: Math.round(s?.width ?? c.width ?? 0),
        height: Math.round(s?.height ?? c.height ?? 0),
      })
    }
  }
  for (const g of model.groups) {
    if (!g.packed || !out.has(g.id)) continue
    const ids = (children.get(g.id) ?? []).map((k) => k.id)
    const { cols } = packGrid(ids.length)
    const top = groupHeader(g.sublabel) + 6
    ids.forEach((id, i) => {
      const x = GROUP_PAD + (i % cols) * (CARD_W + PACK_GAP)
      const y = top + Math.floor(i / cols) * (CARD_H + PACK_GAP)
      out.set(id, { id, kind: kindOf.get(id)!, parent: g.id, x, y, width: CARD_W, height: CARD_H })
    })
  }
  // Routes of real edges, moved from their container to absolute coordinates.
  const absOf = (id: string): Point => {
    const it = out.get(id)
    if (!it) return { x: 0, y: 0 }
    const p = it.parent ? absOf(it.parent) : { x: 0, y: 0 }
    return { x: p.x + it.x, y: p.y + it.y }
  }
  const routes = new Map<string, Point[]>()
  for (const [id, res] of laid) {
    const at = absOf(id)
    for (const e of res.edges ?? []) {
      const route = e.id.startsWith("stand-in:") ? undefined : routeOf(e, at.x, at.y)
      if (route) routes.set(e.id, route)
    }
  }
  return { items: out, routes }
}

const rectOf = (it: DiagramItem): Rect => ({ x: it.ax, y: it.ay, width: it.width, height: it.height })

/** Whether an orthogonal polyline passes through the inside of `r`. */
function crosses(route: Point[], r: Rect) {
  for (let i = 1; i < route.length; i++) {
    const a = route[i - 1]
    const b = route[i]
    if (Math.max(a.x, b.x) > r.x && Math.min(a.x, b.x) < r.x + r.width && Math.max(a.y, b.y) > r.y && Math.min(a.y, b.y) < r.y + r.height) return true
  }
  return false
}

/** Grows groups around their content, places pinned items and resolves edge routes of an automatic layout. */
export function buildDiagram(model: ArchModel, auto: AutoLayout): Diagram {
  const groupsById = new Map(model.groups.map((g) => [g.id, g]))
  const items: DiagramItem[] = []
  const byId = new Map<string, DiagramItem>()
  // Model order puts parents first: groups (parent-first), then actors and cards.
  for (const it of modelItems(model)) {
    const laid: LaidItem | undefined = it.pinned
      ? { id: it.id, kind: it.kind, parent: it.parent, x: 0, y: 0, width: it.kind === "card" ? BORDER_CARD_W : ACTOR_W, height: it.kind === "card" ? BORDER_CARD_H : ACTOR_H }
      : auto.items.get(it.id)
    if (!laid) continue
    const item: DiagramItem = { ...laid, parent: it.parent, ax: 0, ay: 0 }
    items.push(item)
    byId.set(it.id, item)
  }
  const pinned = new Set(modelItems(model).filter((i) => i.pinned).map((i) => i.id))
  // Grow every group (deepest first) around its children, keeping room for the label. Border cards
  // straddle the edge on purpose and do not count.
  const kids = new Map<string, DiagramItem[]>()
  for (const it of items) if (it.parent && !pinned.has(it.id)) kids.set(it.parent, [...(kids.get(it.parent) ?? []), it])
  for (let i = items.length - 1; i >= 0; i--) {
    const g = items[i]
    const list = kids.get(g.id)
    if (g.kind !== "group" || !list?.length) continue
    const top = groupHeader(groupsById.get(g.id)?.sublabel) + 6
    const minX = Math.min(...list.map((k) => k.x))
    const minY = Math.min(...list.map((k) => k.y))
    const dx = Math.max(0, GROUP_PAD - minX)
    const dy = Math.max(0, top - minY)
    if (dx || dy) {
      for (const k of list) {
        k.x += dx
        k.y += dy
      }
      g.x -= dx
      g.y -= dy
      g.width += dx
      g.height += dy
    }
    g.width = Math.max(g.width, ...list.map((k) => k.x + k.width + GROUP_PAD))
    g.height = Math.max(g.height, ...list.map((k) => k.y + k.height + GROUP_PAD))
  }
  const absolute = () => {
    for (const it of items) {
      const p = it.parent ? byId.get(it.parent) : undefined
      it.ax = (p?.ax ?? 0) + it.x
      it.ay = (p?.ay ?? 0) + it.y
    }
  }
  absolute()

  // Border cards sit on their group's top edge, past the labels there, above the subnet they feed;
  // the Internet goes above the outermost container of its gateway.
  if (pinned.size) {
    for (const c of model.cards) {
      const it = byId.get(c.id)
      const g = c.parent ? byId.get(c.parent) : undefined
      if (!it || !g || !pinned.has(c.id)) continue
      const labelEnd = (id: string | undefined) => {
        const group = id ? groupsById.get(id) : undefined
        const item = id ? byId.get(id) : undefined
        return group && item ? item.ax + groupLabelWidth(group.label, group.sublabel) - g.ax : -Infinity
      }
      const min = Math.max(GROUP_PAD, labelEnd(g.id), labelEnd(g.parent)) + BORDER_GAP
      const fed = model.edges.find((e) => e.source === c.id && byId.has(e.target))
      const target = fed ? byId.get(fed.target)! : undefined
      const wanted = target ? target.ax + target.width / 2 - it.width / 2 - g.ax : min
      it.x = Math.round(Math.max(min, Math.min(wanted, g.width - it.width - GROUP_PAD)))
      it.y = -Math.round(it.height / 2)
      it.ax = g.ax + it.x
      it.ay = g.ay + it.y
    }
    for (const a of model.actors) {
      const it = byId.get(a.id)
      const over = a.above ? byId.get(a.above) : undefined
      if (!it || !over || !pinned.has(a.id)) continue
      let outer = over
      while (outer.parent && byId.has(outer.parent)) outer = byId.get(outer.parent)!
      it.x = it.ax = Math.round(over.ax + over.width / 2 - it.width / 2)
      it.y = it.ay = Math.round(Math.min(outer.ay, over.ay) - it.height - ABOVE_GAP)
    }
    absolute()
  }

  // ELK's route stays while both ends (and their sizes) are where it put them and no pinned card now
  // lies across it; other edges (to pinned items, or crossed by one) are routed around the cards.
  const autoAbs = new Map<string, Point>()
  const absOf = (id: string): Point | undefined => {
    const hit = autoAbs.get(id)
    if (hit) return hit
    const it = auto.items.get(id)
    if (!it) return undefined
    const p = it.parent ? absOf(it.parent) : { x: 0, y: 0 }
    if (!p) return undefined
    const abs = { x: p.x + it.x, y: p.y + it.y }
    autoAbs.set(id, abs)
    return abs
  }
  const unmoved = (id: string) => {
    const now = byId.get(id)
    const was = absOf(id)
    const laid = auto.items.get(id)
    return !!now && !!was && !!laid && now.ax === was.x && now.ay === was.y && now.width === laid.width && now.height === laid.height
  }
  const solid = items.filter((i) => i.kind !== "group")
  const shifted = solid.filter((i) => !unmoved(i.id))
  // Group labels are obstacles too: a route crossing "Public subnet · routes: …" hides it. (Kept off
  // the content below, which starts 6 px under the header, once the router's clearance is added.)
  const labels = items.flatMap((i) => {
    const g = i.kind === "group" ? groupsById.get(i.id) : undefined
    return g ? [{ id: i.id, rect: { x: i.ax, y: i.ay, width: Math.min(i.width, groupLabelWidth(g.label, g.sublabel)), height: groupHeader(g.sublabel) - 8 } }] : []
  })
  // ELK's routes stay; the others are routed one by one beside (not on) the routes drawn before them,
  // then once more beside all the others (those drawn after them too), so arrows sharing a corridor
  // or a box side never merge into one line.
  const drawn = model.edges.filter((e) => byId.has(e.source) && byId.has(e.target))
  const kept = new Map<string, Point[]>()
  for (const e of drawn) {
    const route = unmoved(e.source) && unmoved(e.target) ? auto.routes.get(e.id) : undefined
    const s = byId.get(e.source)!
    const t = byId.get(e.target)!
    if (route && !shifted.some((it) => it !== s && it !== t && crosses(route, rectOf(it)))) kept.set(e.id, route)
  }
  const routed = new Map<string, Point[]>()
  const routeBeside = (e: ArchEdge, lanes: Lanes) => {
    const s = byId.get(e.source)!
    const t = byId.get(e.target)!
    const obstacles = [...solid.filter((it) => it !== s && it !== t).map(rectOf), ...labels.filter((l) => l.id !== s.id && l.id !== t.id).map((l) => l.rect)]
    routed.set(e.id, routeOrthogonal(rectOf(s), rectOf(t), obstacles, lanes))
  }
  const free = drawn.filter((e) => !kept.has(e.id))
  const before = new Lanes()
  for (const r of kept.values()) before.add(r)
  for (const e of free) {
    routeBeside(e, before)
    before.add(routed.get(e.id)!)
  }
  for (const e of free) {
    const others = new Lanes()
    for (const [id, r] of [...kept, ...routed]) if (id !== e.id) others.add(r)
    routeBeside(e, others)
  }
  const edges: DiagramEdge[] = drawn.map((e) => ({ ...e, route: kept.get(e.id) ?? routed.get(e.id)! }))

  const roots = items.filter((i) => !i.parent)
  const all = [...roots, ...items.filter((i) => pinned.has(i.id))]
  const minX = Math.min(...all.map((i) => i.ax))
  const minY = Math.min(...all.map((i) => i.ay))
  const maxX = Math.max(...all.map((i) => i.ax + i.width))
  const maxY = Math.max(...all.map((i) => i.ay + i.height))
  const bounds = all.length ? { x: minX, y: minY, width: maxX - minX, height: maxY - minY } : { x: 0, y: 0, width: 0, height: 0 }
  return { items, byId, edges, bounds }
}
