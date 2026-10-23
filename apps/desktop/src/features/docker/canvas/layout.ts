import type { ElkExtendedEdge, ElkNode } from "elkjs/lib/elk.bundled.js"
import { loadElk } from "@/features/canvas/layout"
import type { Position } from "@/lib/api/types"
import { FRAME_HEADER, FRAME_PAD, type FrameItem } from "./model"

// ELK reads spacing per compound node (no inheritance): the frames repeat it, else their services sit 20px apart.
// The model-order constraint stays on the root: inside a frame it makes elkjs throw.
const SPACING = {
  "elk.layered.spacing.nodeNodeBetweenLayers": "128",
  "elk.spacing.nodeNode": "40",
  "elk.spacing.componentComponent": "72",
  "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
}

// Long dependency chains (Ingress → Service → Deployment → Service → …) wrap into rows near a screen's aspect ratio
// instead of one strip too wide to read when fitted. Short chains stay on one row: wrapping them only stacks cards.
const WRAP = { "elk.layered.wrapping.strategy": "MULTI_EDGE", "elk.aspectRatio": "1.8" }
export const WRAP_FROM_LAYERS = 6

const OPTIONS = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
  ...SPACING,
}

/** Cards on the longest edge path (cycles cut), i.e. how many columns a left→right layering needs. */
export function longestChain(edges: { source: string; target: string }[]): number {
  const out = new Map<string, string[]>()
  for (const e of edges) out.set(e.source, [...(out.get(e.source) ?? []), e.target])
  const memo = new Map<string, number>()
  const onPath = new Set<string>()
  const depth = (n: string): number => {
    const known = memo.get(n)
    if (known !== undefined) return known
    if (onPath.has(n)) return 0
    onPath.add(n)
    let best = 1
    for (const t of out.get(n) ?? []) best = Math.max(best, 1 + depth(t))
    onPath.delete(n)
    memo.set(n, best)
    return best
  }
  let max = 0
  for (const n of out.keys()) max = Math.max(max, depth(n))
  return max
}

/**
 * Left→right layered layout of the whole canvas: services nested in their network frames, users before what they use
 * (proxies, then apps, then databases). Returns absolute top-left positions of the cards (frames wrap them).
 */
export async function layoutCanvas(canvas: { items: FrameItem[]; edges: { source: string; target: string; kind: string }[] }): Promise<Map<string, Position>> {
  const kids = new Map<string, FrameItem[]>()
  for (const i of canvas.items) if (i.parent) kids.set(i.parent, [...(kids.get(i.parent) ?? []), i])
  const top = canvas.items.filter((i) => !i.parent && (i.kind !== "frame" || kids.has(i.id) || i.node))
  const build = (i: FrameItem): ElkNode => {
    const list = kids.get(i.id)
    if (i.kind !== "frame" || !list?.length) return { id: i.id, width: i.w, height: i.h }
    return {
      id: i.id,
      layoutOptions: { ...spacing, "elk.padding": `[top=${FRAME_HEADER},left=${FRAME_PAD},bottom=${FRAME_PAD},right=${FRAME_PAD}]` },
      children: list.map(build),
    }
  }
  const ids = new Set(canvas.items.map((i) => i.id))
  const seen = new Set<string>()
  const edges: ElkExtendedEdge[] = []
  for (const e of canvas.edges) {
    // Frames hold their members; an edge to the frame itself would pull it out of line.
    if (!ids.has(e.source) || !ids.has(e.target) || e.kind === "network") continue
    const key = `${e.source}->${e.target}`
    if (seen.has(key) || e.source === e.target) continue
    seen.add(key)
    edges.push({ id: key, sources: [e.source], targets: [e.target] })
  }
  const chain = longestChain(edges.map((e) => ({ source: e.sources[0], target: e.targets[0] })))
  const spacing = chain >= WRAP_FROM_LAYERS ? { ...SPACING, ...WRAP } : SPACING
  const result = await (await loadElk()).layout({ id: "__root", layoutOptions: { ...OPTIONS, ...spacing }, children: top.map(build), edges })
  const out = new Map<string, Position>()
  const walk = (n: ElkNode, ox: number, oy: number) => {
    for (const c of n.children ?? []) {
      const x = ox + (c.x ?? 0)
      const y = oy + (c.y ?? 0)
      if (!c.children?.length) out.set(c.id, { x: Math.round(x), y: Math.round(y) })
      walk(c, x, y)
    }
  }
  walk(result, 0, 0)
  return out
}
