// Where the walkthrough's dot travels and where the camera looks, in flow coordinates. Pure.
import type { Point, Rect } from "@/features/architecture/geometry"

const center = (r: Rect): Point => ({ x: r.x + r.width / 2, y: r.y + r.height / 2 })

/**
 * A smooth route from `a` to `b` on the Build canvas: leaves `a` on the side facing `b`, enters `b` on the
 * side facing `a` (left/right when they sit side by side, top/bottom when stacked), as a cubic curve
 * sampled in `steps` pieces.
 */
export function bridgeRoute(a: Rect, b: Rect, steps = 24): Point[] {
  const ca = center(a)
  const cb = center(b)
  const dx = cb.x - ca.x
  const dy = cb.y - ca.y
  const across = Math.abs(dx) >= Math.abs(dy)
  const sx = Math.sign(dx) || 1
  const sy = Math.sign(dy) || 1
  const p0 = across ? { x: ca.x + (sx * a.width) / 2, y: ca.y } : { x: ca.x, y: ca.y + (sy * a.height) / 2 }
  const p3 = across ? { x: cb.x - (sx * b.width) / 2, y: cb.y } : { x: cb.x, y: cb.y - (sy * b.height) / 2 }
  const pull = Math.max(30, (across ? Math.abs(p3.x - p0.x) : Math.abs(p3.y - p0.y)) / 2)
  const p1 = across ? { x: p0.x + sx * pull, y: p0.y } : { x: p0.x, y: p0.y + sy * pull }
  const p2 = across ? { x: p3.x - sx * pull, y: p3.y } : { x: p3.x, y: p3.y - sy * pull }
  const out: Point[] = []
  for (let i = 0; i <= steps; i++) {
    const t = i / steps
    const u = 1 - t
    out.push({
      x: u * u * u * p0.x + 3 * u * u * t * p1.x + 3 * u * t * t * p2.x + t * t * t * p3.x,
      y: u * u * u * p0.y + 3 * u * u * t * p1.y + 3 * u * t * t * p2.y + t * t * t * p3.y,
    })
  }
  return out
}

/** A route coming in from the left of `b` (Users reaching the front door, who are not on the Build canvas). */
export function arrivalRoute(b: Rect, distance = 160): Point[] {
  const y = b.y + b.height / 2
  return [
    { x: b.x - distance, y },
    { x: b.x, y },
  ]
}

export interface HopLinks {
  /** Canvas edges between the two ends (directly or through a helper). */
  edges: string[]
  /** Helper nodes in between: an API integration, a Kubernetes Service, a target group. */
  via: string[]
}

/**
 * The Build canvas edges a hop from `from` nodes to `to` nodes travels: the edges joining them in either
 * direction, else those through one node in between that no item of the model stands for (`isItemNode`).
 */
export function hopLinks(edges: { id: string; source: string; target: string }[], from: string[], to: string[], isItemNode: (id: string) => boolean): HopLinks {
  const a = new Set(from)
  const b = new Set(to)
  const direct = edges.filter((e) => (a.has(e.source) && b.has(e.target)) || (b.has(e.source) && a.has(e.target)))
  if (direct.length) return { edges: direct.map((e) => e.id), via: [] }
  const touching = (ends: Set<string>) => {
    const m = new Map<string, string[]>()
    for (const e of edges) {
      const other = ends.has(e.source) ? e.target : ends.has(e.target) ? e.source : undefined
      if (other && !ends.has(other) && !isItemNode(other)) m.set(other, [...(m.get(other) ?? []), e.id])
    }
    return m
  }
  const fromA = touching(a)
  const fromB = touching(b)
  const via = [...fromA.keys()].filter((x) => fromB.has(x))
  return { edges: via.flatMap((x) => [...fromA.get(x)!, ...fromB.get(x)!]), via }
}

/** The smallest rectangle around `rects`. */
export function unionRect(rects: Rect[]): Rect | undefined {
  if (!rects.length) return undefined
  const x = Math.min(...rects.map((r) => r.x))
  const y = Math.min(...rects.map((r) => r.y))
  return { x, y, width: Math.max(...rects.map((r) => r.x + r.width)) - x, height: Math.max(...rects.map((r) => r.y + r.height)) - y }
}

/**
 * The viewport center and zoom that show `focus` in the part of a `view`-sized viewport above the
 * caption panel (`covered` pixels at the bottom), with `padding` pixels around it, never closer than
 * `maxZoom` nor farther than `minZoom`.
 */
export function cameraFor(focus: Rect, view: { width: number; height: number }, covered: number, opts: { padding?: number; minZoom?: number; maxZoom?: number } = {}) {
  const { padding = 80, minZoom = 0.35, maxZoom = 1 } = opts
  const visibleH = Math.max(120, view.height - covered)
  const zoom = Math.min(maxZoom, Math.max(minZoom, Math.min((view.width - 2 * padding) / Math.max(1, focus.width), (visibleH - 2 * padding) / Math.max(1, focus.height))))
  const c = center(focus)
  // The visible part's center sits `covered / 2` pixels above the viewport's: move the camera down by as much.
  return { x: c.x, y: c.y + covered / 2 / zoom, zoom }
}
