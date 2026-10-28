// Traffic animation of the Architecture diagram (GIF/video export): pulses travel along every arrow in its
// direction, in waves that start at the diagram's entry points (users, ingress) and move on through the
// services to the data stores, so the flow reads left → right. Everything here is pure: positions are in
// diagram coordinates and depend only on t mod duration, so the last frame flows into the first.
import type { Point, Rect } from "./geometry"
import type { Diagram } from "./layout"
import type { FlowKind } from "./model"

// ------------------------------------------------------------ frames and sizes

export type SizePreset = "fit" | "square" | "portrait" | "landscape"

/** Social media frame sizes; "fit" sizes the frame to the diagram. */
export const SIZE_PRESETS: Record<Exclude<SizePreset, "fit">, { width: number; height: number }> = {
  square: { width: 1080, height: 1080 },
  portrait: { width: 1080, height: 1350 },
  landscape: { width: 1920, height: 1080 },
}

/** Room between the diagram and the frame edge in the social presets, as a share of the shorter side. */
const PRESET_PADDING = 0.05
/** Small diagrams are not blown up beyond this in a preset frame. */
const MAX_PRESET_SCALE = 3

export interface FrameLayout {
  /** Frame size in pixels (even, as H.264 needs). */
  width: number
  height: number
  /** Pixels per diagram unit: the SVG is rasterized at this scale, never stretched afterwards. */
  scale: number
  /** Where the diagram's top-left corner lands in the frame. */
  dx: number
  dy: number
}

const even = (n: number) => Math.max(2, Math.ceil(n / 2) * 2)

/**
 * The frame for a `content`-sized diagram: a social preset with the diagram centered inside the padding,
 * or the diagram at `scale` (lowered so the frame stays within `maxSide` and `maxPixels`).
 */
export function frameLayout(
  content: { width: number; height: number },
  preset: SizePreset,
  limits: { scale: number; maxSide: number; maxPixels: number },
): FrameLayout {
  const { width: w, height: h } = content
  if (preset === "fit") {
    const scale = Math.min(limits.scale, limits.maxSide / w, limits.maxSide / h, Math.sqrt(limits.maxPixels / (w * h)))
    const width = even(w * scale)
    const height = even(h * scale)
    return { width, height, scale, dx: Math.round((width - w * scale) / 2), dy: Math.round((height - h * scale) / 2) }
  }
  const { width, height } = SIZE_PRESETS[preset]
  const pad = Math.round(Math.min(width, height) * PRESET_PADDING)
  const scale = Math.min((width - 2 * pad) / w, (height - 2 * pad) / h, MAX_PRESET_SCALE)
  return { width, height, scale, dx: Math.round((width - w * scale) / 2), dy: Math.round((height - h * scale) / 2) }
}

/** The social preset that shows a `content`-sized diagram largest (ties go to the smaller frame: square first). */
export function bestPreset(content: { width: number; height: number }): Exclude<SizePreset, "fit"> {
  const presets = ["square", "portrait", "landscape"] as const
  const limits = { scale: 1, maxSide: Infinity, maxPixels: Infinity }
  return presets.reduce((best, p) => (frameLayout(content, p, limits).scale > frameLayout(content, best, limits).scale + 0.01 ? p : best))
}

/** Frames of a `duration`-second loop at `fps`. */
export function frameCount(duration: number, fps: number) {
  return Math.max(1, Math.round(duration * fps))
}

/** Time of frame `i` of `count` in a `duration`-second loop: frame `count` would be frame 0 again. */
export function frameTime(i: number, count: number, duration: number) {
  return (i / count) * duration
}

/**
 * Splits the frame into `tile`-pixel squares and returns the rows of touched squares as rectangles, so a few
 * reads cover all the (often overlapping) areas a frame drew on.
 */
export function dirtyTiles(rects: Rect[], width: number, height: number, tile = 32): Rect[] {
  const cols = Math.ceil(width / tile)
  const rows = Math.ceil(height / tile)
  const marked = new Uint8Array(cols * rows)
  for (const r of rects) {
    const c0 = Math.max(0, Math.floor(r.x / tile))
    const c1 = Math.min(cols - 1, Math.floor((r.x + r.width) / tile))
    const r0 = Math.max(0, Math.floor(r.y / tile))
    const r1 = Math.min(rows - 1, Math.floor((r.y + r.height) / tile))
    for (let y = r0; y <= r1; y++) marked.fill(1, y * cols + c0, y * cols + c1 + 1)
  }
  const out: Rect[] = []
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < cols; x++) {
      if (!marked[y * cols + x]) continue
      let end = x
      while (end + 1 < cols && marked[y * cols + end + 1]) end++
      const px = x * tile
      const py = y * tile
      out.push({ x: px, y: py, width: Math.min(width, (end + 1) * tile) - px, height: Math.min(height, py + tile) - py })
      x = end
    }
  }
  return out
}

// ------------------------------------------------------------ paths

export interface PathMeasure {
  points: Point[]
  /** Distance from the start to each point. */
  cum: number[]
  length: number
}

/**
 * The points of a route as drawn: corners rounded like `polylinePath` (the same quadratic curve, sampled
 * in `steps` pieces), so pulses follow the line instead of cutting or overshooting its bends.
 */
export function roundedRoute(points: Point[], radius = 8, steps = 4): Point[] {
  if (points.length < 3) return points.slice()
  const out: Point[] = [points[0]]
  for (let i = 1; i < points.length - 1; i++) {
    const prev = points[i - 1]
    const cur = points[i]
    const next = points[i + 1]
    const inLen = Math.hypot(cur.x - prev.x, cur.y - prev.y)
    const outLen = Math.hypot(next.x - cur.x, next.y - cur.y)
    const r = Math.min(radius, inLen / 2, outLen / 2)
    if (r < 0.5) {
      out.push(cur)
      continue
    }
    const a = { x: cur.x - ((cur.x - prev.x) / inLen) * r, y: cur.y - ((cur.y - prev.y) / inLen) * r }
    const b = { x: cur.x + ((next.x - cur.x) / outLen) * r, y: cur.y + ((next.y - cur.y) / outLen) * r }
    out.push(a)
    for (let k = 1; k <= steps; k++) {
      const u = k / steps
      const p = (1 - u) * (1 - u)
      const q = 2 * (1 - u) * u
      out.push({ x: p * a.x + q * cur.x + u * u * b.x, y: p * a.y + q * cur.y + u * u * b.y })
    }
  }
  out.push(points[points.length - 1])
  return out
}

export function measurePath(points: Point[]): PathMeasure {
  const cum = [0]
  for (let i = 1; i < points.length; i++) cum.push(cum[i - 1] + Math.hypot(points[i].x - points[i - 1].x, points[i].y - points[i - 1].y))
  return { points, cum, length: cum[cum.length - 1] ?? 0 }
}

/** The point `dist` along a measured path (clamped to its ends). */
export function pointAt(m: PathMeasure, dist: number): Point {
  const { points, cum } = m
  if (points.length === 1 || dist <= 0) return points[0]
  if (dist >= m.length) return points[points.length - 1]
  let lo = 1
  let hi = cum.length - 1
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (cum[mid] < dist) lo = mid + 1
    else hi = mid
  }
  const a = points[lo - 1]
  const b = points[lo]
  const seg = cum[lo] - cum[lo - 1]
  const u = seg > 0 ? (dist - cum[lo - 1]) / seg : 0
  return { x: a.x + (b.x - a.x) * u, y: a.y + (b.y - a.y) * u }
}

// ------------------------------------------------------------ flow

/** Pulse speed in diagram units per second, faster when the longest path would not fit the loop. */
const SPEED = 260
/** About one wave of requests per this many seconds. */
const WAVE_GAP = 1.5
/** How long a node glows after a pulse arrives. */
export const FLASH_TIME = 0.4
/** Trail behind a pulse: distance between ghosts (in pulse radii) and their size and opacity. */
const TRAIL = [
  { size: 0.72, alpha: 0.5 },
  { size: 0.52, alpha: 0.28 },
  { size: 0.36, alpha: 0.14 },
]
const TRAIL_GAP = 1.9
/** Pulse radius in diagram units per flow kind. */
export const PULSE_RADIUS: Record<FlowKind, number> = { data: 4, control: 3 }
/** Pulses fade in over this distance after leaving their source. */
const FADE_IN = 14

export interface FlowEdge {
  id: string
  kind: FlowKind
  path: PathMeasure
  /** Distance a wave has travelled when it reaches this edge's source. */
  start: number
  /** The card or actor the edge points at (highlighted when a pulse arrives), if any. */
  target?: Rect
}

export interface FlowPlan {
  edges: FlowEdge[]
  duration: number
  /** Diagram units per second. */
  speed: number
  waves: number
}

/**
 * Where every edge starts in the flow: the shortest route length from an entry point (an item nothing
 * points at; the leftmost item of a cycle nothing enters). Pulses run along every edge in each of
 * `waves` evenly spaced waves per loop, fast enough that a wave crosses the whole diagram within it.
 */
export function flowPlan(diagram: Pick<Diagram, "edges" | "byId">, duration: number): FlowPlan {
  const edges = diagram.edges
    .map((e) => ({ e, path: measurePath(roundedRoute(e.route)) }))
    .filter(({ path }) => path.length > 0)
  const dist = new Map<string, number>()
  const incoming = new Set(edges.map(({ e }) => e.target))
  for (const { e } of edges) if (!incoming.has(e.source)) dist.set(e.source, 0)
  const relax = () => {
    for (let changed = true, round = 0; changed && round <= edges.length; round++) {
      changed = false
      for (const { e, path } of edges) {
        const d = dist.get(e.source)
        if (d === undefined) continue
        const next = d + path.length
        if (next < (dist.get(e.target) ?? Infinity)) {
          dist.set(e.target, next)
          changed = true
        }
      }
    }
  }
  relax()
  for (;;) {
    const left = edges
      .filter(({ e }) => !dist.has(e.source))
      .map(({ e }) => e.source)
      .sort((a, b) => (diagram.byId.get(a)?.ax ?? 0) - (diagram.byId.get(b)?.ax ?? 0))
    if (!left.length) break
    dist.set(left[0], 0)
    relax()
  }

  const flow: FlowEdge[] = edges.map(({ e, path }) => {
    const t = diagram.byId.get(e.target)
    return {
      id: e.id,
      kind: e.kind,
      path,
      start: dist.get(e.source) ?? 0,
      target: t && t.kind !== "group" ? { x: t.ax, y: t.ay, width: t.width, height: t.height } : undefined,
    }
  })
  const reach = Math.max(0, ...flow.map((f) => f.start + f.path.length))
  const speed = Math.max(SPEED, reach / Math.max(0.1, duration - FLASH_TIME))
  return { edges: flow, duration, speed, waves: Math.max(1, Math.round(duration / WAVE_GAP)) }
}

export interface Pulse {
  x: number
  y: number
  /** Radius in diagram units. */
  radius: number
  alpha: number
}

export interface Flash {
  rect: Rect
  /** 1 when the pulse just arrived, fading to 0. */
  strength: number
}

const mod = (a: number, n: number) => ((a % n) + n) % n

/** Pulses (trails included, faint to bright) and node highlights at time `t` of the loop. */
export function flowAt(plan: FlowPlan, t: number): { pulses: Pulse[]; flashes: Flash[] } {
  const pulses: Pulse[] = []
  const flashes = new Map<Rect, number>()
  for (let w = 0; w < plan.waves; w++) {
    const age = mod(t - (w * plan.duration) / plan.waves, plan.duration)
    const travelled = age * plan.speed
    for (const e of plan.edges) {
      const s = travelled - e.start
      const L = e.path.length
      const r = PULSE_RADIUS[e.kind]
      const fade = Math.min(1, s / FADE_IN)
      if (s >= 0 && s <= L) {
        for (let k = TRAIL.length - 1; k >= 0; k--) {
          const at = s - (k + 1) * TRAIL_GAP * r
          if (at < 0) continue
          const p = pointAt(e.path, at)
          pulses.push({ x: p.x, y: p.y, radius: r * TRAIL[k].size, alpha: TRAIL[k].alpha * fade })
        }
        const p = pointAt(e.path, s)
        pulses.push({ x: p.x, y: p.y, radius: r, alpha: fade })
      }
      if (e.target && e.kind === "data") {
        const since = age - (e.start + L) / plan.speed
        if (since >= 0 && since < FLASH_TIME) flashes.set(e.target, Math.max(flashes.get(e.target) ?? 0, 1 - since / FLASH_TIME))
      }
    }
  }
  return { pulses, flashes: [...flashes].map(([rect, strength]) => ({ rect, strength })) }
}
