import type { ELK, ElkExtendedEdge, ElkNode } from "elkjs/lib/elk.bundled.js"
import type { Position, WorkflowEdge, WorkflowNode } from "@/lib/api/types"

export const NODE_WIDTH = 248
export const NODE_HEIGHT = 84
export const GROUP_PAD = 24
export const GROUP_HEADER = 44

// elkjs is ~1.4 MB: loaded on the first layout instead of with the app.
let elk: Promise<ELK> | undefined

/** The shared ELK engine (loaded on first use; a failed chunk load is retried next time). */
export function loadElk(): Promise<ELK> {
  elk ??= import("elkjs/lib/elk.bundled.js").then(({ default: Elk }) => new Elk())
  return elk.catch((err: unknown) => {
    elk = undefined
    throw err
  })
}

const ROOT_OPTIONS = {
  "elk.algorithm": "layered",
  "elk.direction": "RIGHT",
  "elk.hierarchyHandling": "INCLUDE_CHILDREN",
  "elk.layered.spacing.nodeNodeBetweenLayers": "90",
  "elk.spacing.nodeNode": "40",
  "elk.spacing.componentComponent": "60",
  "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
  "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
}

/** Valid parent links (parent present, no self-links, cycles broken) as child id → parent id. */
export function parentMap(nodes: WorkflowNode[]) {
  const ids = new Set(nodes.map((n) => n.id))
  const parents = new Map<string, string>()
  for (const n of nodes) if (n.parent && n.parent !== n.id && ids.has(n.parent)) parents.set(n.id, n.parent)
  for (const id of [...parents.keys()]) {
    const seen = new Set([id])
    for (let cur = parents.get(id); cur; cur = parents.get(cur)) {
      if (seen.has(cur)) {
        parents.delete(id)
        break
      }
      seen.add(cur)
    }
  }
  return parents
}

/** Children by parent id for valid parent links. */
export function childrenByParent(nodes: WorkflowNode[]) {
  const parents = parentMap(nodes)
  const children = new Map<string, WorkflowNode[]>()
  for (const n of nodes) {
    const p = parents.get(n.id)
    if (!p) continue
    const list = children.get(p) ?? []
    list.push(n)
    children.set(p, list)
  }
  return children
}

function ancestors(id: string, parentOf: Map<string, string>) {
  const out = new Set<string>()
  let cur = parentOf.get(id)
  while (cur && !out.has(cur)) {
    out.add(cur)
    cur = parentOf.get(cur)
  }
  return out
}

/**
 * Layered left→right layout of `nodes` (parent groups nested) returning
 * absolute top-left positions for every node.
 */
export async function computeLayout(nodes: WorkflowNode[], edges: WorkflowEdge[]): Promise<Map<string, Position>> {
  const children = childrenByParent(nodes)
  const parentOf = parentMap(nodes)

  const build = (n: WorkflowNode): ElkNode => {
    const kids = children.get(n.id)
    if (!kids) return { id: n.id, width: NODE_WIDTH, height: NODE_HEIGHT }
    return {
      id: n.id,
      layoutOptions: { "elk.padding": `[top=${GROUP_HEADER},left=${GROUP_PAD},bottom=${GROUP_PAD},right=${GROUP_PAD}]` },
      children: kids.map(build),
    }
  }

  const ids = new Set(nodes.map((n) => n.id))
  const elkEdges: ElkExtendedEdge[] = []
  const seen = new Set<string>()
  for (const e of edges) {
    if (!ids.has(e.source) || !ids.has(e.target) || e.source === e.target) continue
    if (ancestors(e.source, parentOf).has(e.target) || ancestors(e.target, parentOf).has(e.source)) continue
    const key = `${e.source}->${e.target}`
    if (seen.has(key)) continue
    seen.add(key)
    elkEdges.push({ id: key, sources: [e.source], targets: [e.target] })
  }

  const graph: ElkNode = {
    id: "__root",
    layoutOptions: ROOT_OPTIONS,
    children: nodes.filter((n) => !parentOf.has(n.id)).map(build),
    edges: elkEdges,
  }
  const result = await (await loadElk()).layout(graph)

  const positions = new Map<string, Position>()
  const walk = (node: ElkNode, ox: number, oy: number) => {
    for (const c of node.children ?? []) {
      const x = ox + (c.x ?? 0)
      const y = oy + (c.y ?? 0)
      positions.set(c.id, { x: Math.round(x), y: Math.round(y) })
      walk(c, x, y)
    }
  }
  walk(result, 0, 0)
  return positions
}

/**
 * Positions for nodes that were never placed. When everything is unplaced
 * the whole graph is laid out; otherwise the new nodes are laid out among
 * themselves and placed below the existing drawing.
 */
export async function layoutUnpositioned(nodes: WorkflowNode[], edges: WorkflowEdge[]) {
  const unplaced = nodes.filter((n) => !n.positioned)
  if (unplaced.length === 0) return new Map<string, Position>()
  if (unplaced.length === nodes.length) return computeLayout(nodes, edges)
  const placed = nodes.filter((n) => n.positioned)
  const minX = Math.min(...placed.map((n) => n.position.x))
  const maxY = Math.max(...placed.map((n) => n.position.y + NODE_HEIGHT))
  const positions = await computeLayout(unplaced, edges)
  for (const [id, p] of positions) positions.set(id, { x: p.x + minX, y: p.y + maxY + 120 })
  return positions
}
