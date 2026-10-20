/** Diagram metrics (CSS pixels at 1×). */
export const CARD_W = 260
export const CARD_H = 80
/** A card drawn on its group's border (an internet gateway on its VPC). */
export const BORDER_CARD_W = 168
export const BORDER_CARD_H = 40
export const ACTOR_W = 112
export const ACTOR_H = 96
export const GROUP_PAD = 20
/** Group header height: the label line plus one line per sublabel line. */
export const groupHeader = (sublabel?: readonly string[]) => 36 + 14 * (sublabel?.length ?? 0)
export const EMPTY_GROUP_W = 260
export const EMPTY_GROUP_H = 120
export const HEADER_H = 64
export const HEADER_GAP = 28
export const LEGEND_W = 236
export const LEGEND_GAP = 28
/** Margin around the whole diagram in exports. */
export const EXPORT_MARGIN = 40

export const FONT_FAMILY = "'Geist Variable', 'Geist', ui-sans-serif, system-ui, sans-serif"

export interface Point {
  x: number
  y: number
}

export interface Rect {
  x: number
  y: number
  width: number
  height: number
}

let measureCtx: CanvasRenderingContext2D | null | undefined
const widthCache = new Map<string, number>()

/** Rendered width of `text` in `font` ("600 13px …"); an estimate when no canvas is available. */
export function textWidth(text: string, font: string) {
  const key = `${font}|${text}`
  const cached = widthCache.get(key)
  if (cached !== undefined) return cached
  if (measureCtx === undefined) measureCtx = typeof document === "undefined" ? null : document.createElement("canvas").getContext("2d")
  let w: number
  if (measureCtx) {
    measureCtx.font = font
    w = measureCtx.measureText(text).width
  } else {
    w = text.length * (Number(/(\d+(?:\.\d+)?)px/.exec(font)?.[1] ?? 12) * 0.56)
  }
  widthCache.set(key, w)
  return w
}

/** Forgets measured widths (after the diagram font finished loading). */
export function resetTextMeasure() {
  widthCache.clear()
}

/** `text` cut with an ellipsis so it fits `max` pixels in `font`. */
export function fitText(text: string, font: string, max: number) {
  if (textWidth(text, font) <= max) return text
  let lo = 0
  let hi = text.length
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2)
    if (textWidth(`${text.slice(0, mid).trimEnd()}…`, font) <= max) lo = mid
    else hi = mid - 1
  }
  return `${text.slice(0, lo).trimEnd()}…`
}

/**
 * `parts` as "a · b · c" over at most `lines` lines of `max` pixels: lines break between parts, inside a
 * part only when it is wider than a line; what still does not fit ends in an ellipsis.
 */
export function wrapParts(parts: readonly string[], font: string, max: number, lines: number): string[] {
  const out: string[] = []
  const joins: string[] = [] // joins[i] separates out[i] from out[i + 1]
  let line = ""
  for (const part of parts) {
    const next = line ? `${line} · ${part}` : part
    if (textWidth(next, font) <= max) {
      line = next
      continue
    }
    if (line) {
      out.push(line)
      joins.push(" · ")
    }
    line = ""
    for (const word of part.split(" ")) {
      const candidate = line ? `${line} ${word}` : word
      if (!line || textWidth(candidate, font) <= max) {
        line = candidate
      } else {
        out.push(line)
        joins.push(" ")
        line = word
      }
    }
  }
  if (line) out.push(line)
  if (out.length > lines) {
    let rest = out[lines - 1]
    for (let i = lines; i < out.length; i++) rest += joins[i - 1] + out[i]
    out.splice(lines - 1, out.length, rest)
  }
  return out.map((l) => fitText(l, font, max))
}

/** SVG path through `points` with rounded corners. */
export function polylinePath(points: Point[], radius = 8) {
  if (points.length < 2) return ""
  let d = `M ${points[0].x} ${points[0].y}`
  for (let i = 1; i < points.length - 1; i++) {
    const prev = points[i - 1]
    const cur = points[i]
    const next = points[i + 1]
    const inLen = Math.hypot(cur.x - prev.x, cur.y - prev.y)
    const outLen = Math.hypot(next.x - cur.x, next.y - cur.y)
    const r = Math.min(radius, inLen / 2, outLen / 2)
    if (r < 0.5) {
      d += ` L ${cur.x} ${cur.y}`
      continue
    }
    const a = { x: cur.x - ((cur.x - prev.x) / inLen) * r, y: cur.y - ((cur.y - prev.y) / inLen) * r }
    const b = { x: cur.x + ((next.x - cur.x) / outLen) * r, y: cur.y + ((next.y - cur.y) / outLen) * r }
    d += ` L ${a.x} ${a.y} Q ${cur.x} ${cur.y} ${b.x} ${b.y}`
  }
  const last = points[points.length - 1]
  return `${d} L ${last.x} ${last.y}`
}

// ------------------------------------------------------------ orthogonal routing

/** Room kept between a route and the boxes it passes; also the length of the stub leaving a box. */
const CLEARANCE = 12
/** A bend costs as much as this many pixels of length. */
const BEND_COST = 40
/** How far around the two ends the router first looks for a way (then everywhere). */
const SEARCH_MARGIN = 240
/** Gap between parallel routes sharing a corridor, and between routes leaving one side of a box. */
const LANE = 10

/**
 * Routes already drawn. Later routes run beside their segments, not on them, and leave or enter a
 * box through other points of its side, so two arrows never merge into one line.
 */
export class Lanes {
  private readonly segments: { vertical: boolean; pos: number; lo: number; hi: number }[] = []
  private readonly ends: Point[] = []

  add(route: readonly Point[]) {
    for (let i = 1; i < route.length; i++) {
      const a = route[i - 1]
      const b = route[i]
      const vertical = a.x === b.x
      this.segments.push({ vertical, pos: vertical ? a.x : a.y, lo: vertical ? Math.min(a.y, b.y) : Math.min(a.x, b.x), hi: vertical ? Math.max(a.y, b.y) : Math.max(a.x, b.x) })
    }
    if (route.length) this.ends.push(route[0], route[route.length - 1])
  }

  /** Whether a segment would run on (or within `gap` of) one already drawn, end to end included. */
  taken(vertical: boolean, pos: number, lo: number, hi: number, gap = LANE) {
    return this.segments.some((s) => s.vertical === vertical && Math.abs(s.pos - pos) < gap && s.lo < hi + gap && lo < s.hi + gap)
  }

  /** Whether a route already ends at `p` (or right beside it). */
  endsNear(p: Point) {
    return this.ends.some((e) => Math.abs(e.x - p.x) < LANE && Math.abs(e.y - p.y) < LANE)
  }
}

/** Lane steps tried, nearest first, to move off a taken lane: 0, +1, −1, +2, −2, … */
const LANE_STEPS = Array.from({ length: 9 }, (_, i) => (i === 0 ? 0 : Math.ceil(i / 2) * (i % 2 ? 1 : -1)))

/** Right, bottom, left, top. */
const DIRS: readonly Point[] = [
  { x: 1, y: 0 },
  { x: 0, y: 1 },
  { x: -1, y: 0 },
  { x: 0, y: -1 },
]

interface Port {
  /** On the box's side. */
  at: Point
  /** The stub's end, CLEARANCE out from the side. */
  out: Point
  dir: number
  /** Extra cost of leaving through this side: sides facing away from the other end cost more. */
  cost: number
}

/**
 * Where a route may leave `r`: the middle of each side, plus the point of a side lined up with the
 * other end (`along`) so boxes that overlap in projection connect with one straight segment. A point
 * another route already ends at moves along its side, so arrows in and out of a box stay apart.
 */
function portsOf(r: Rect, toward: Point, along: { x?: number; y?: number }, lanes: Lanes | undefined): Port[] {
  const c = { x: r.x + r.width / 2, y: r.y + r.height / 2 }
  const len = Math.hypot(toward.x - c.x, toward.y - c.y) || 1
  const v = { x: (toward.x - c.x) / len, y: (toward.y - c.y) / len }
  const ports: Port[] = []
  DIRS.forEach((d, dir) => {
    const cost = (1 - (d.x * v.x + d.y * v.y)) * 60
    const aligned = d.x === 0 ? along.x : along.y
    const room = (d.x === 0 ? r.width : r.height) / 2 - CLEARANCE
    for (const wanted of aligned === undefined ? [0] : [0, aligned - (d.x === 0 ? c.x : c.y)]) {
      const place = (offset: number) => (d.x === 0 ? { x: c.x + offset, y: c.y + (d.y * r.height) / 2 } : { x: c.x + (d.x * r.width) / 2, y: c.y + offset })
      const free = LANE_STEPS.map((k) => wanted + LANE * k).find((o) => Math.abs(o) <= room && !lanes?.endsNear(place(o)))
      const at = place(free ?? wanted)
      ports.push({ at, out: { x: at.x + d.x * CLEARANCE, y: at.y + d.y * CLEARANCE }, dir, cost })
    }
  })
  return ports
}

const inflate = (r: Rect, by: number): Rect => ({ x: r.x - by, y: r.y - by, width: r.width + 2 * by, height: r.height + 2 * by })
const overlaps = (a: Rect, b: Rect) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height
const contains = (a: Rect, b: Rect) => a.x <= b.x && a.y <= b.y && a.x + a.width >= b.x + b.width && a.y + a.height >= b.y + b.height

/** Middle of the stretch two ranges share (leaving room for rounded corners), if they share one. */
function sharedMiddle(a0: number, a1: number, b0: number, b1: number) {
  const lo = Math.max(a0, b0) + CLEARANCE
  const hi = Math.min(a1, b1) - CLEARANCE
  return hi >= lo ? Math.round((lo + hi) / 2) : undefined
}

/**
 * An orthogonal polyline from `source` to `target` that keeps CLEARANCE away from every obstacle
 * (and from the two ends themselves, past the stubs leaving them): a shortest path with bend costs
 * over the grid of obstacle sides and port lines, its middle segments then centered in their
 * corridors, beside the routes already drawn (`lanes`) rather than on them.
 */
export function routeOrthogonal(source: Rect, target: Rect, obstacles: readonly Rect[], lanes?: Lanes): Point[] {
  const sc = { x: source.x + source.width / 2, y: source.y + source.height / 2 }
  const tc = { x: target.x + target.width / 2, y: target.y + target.height / 2 }
  const along = {
    x: sharedMiddle(source.x, source.x + source.width, target.x, target.x + target.width),
    y: sharedMiddle(source.y, source.y + source.height, target.y, target.y + target.height),
  }
  const starts = portsOf(source, tc, along, lanes)
  const ends = portsOf(target, sc, along, lanes)
  const walls = obstacles.map((o) => inflate(o, CLEARANCE))
  // The ends are walls too (their stubs reach just outside), unless one contains the other.
  if (!contains(source, target) && !contains(target, source)) walls.push(inflate(source, CLEARANCE - 1), inflate(target, CLEARANCE - 1))
  const near = inflate(
    {
      x: Math.min(source.x, target.x),
      y: Math.min(source.y, target.y),
      width: Math.max(source.x + source.width, target.x + target.width) - Math.min(source.x, target.x),
      height: Math.max(source.y + source.height, target.y + target.height) - Math.min(source.y, target.y),
    },
    SEARCH_MARGIN,
  )
  const route =
    shortestRoute(
      starts,
      ends,
      walls.filter((w) => overlaps(w, near)),
    ) ?? shortestRoute(starts, ends, walls)
  if (route) return centerSegments(route, walls, lanes)
  // Boxed in on every side: a plain step between the facing sides.
  const s = starts.reduce((a, b) => (b.cost < a.cost ? b : a))
  const t = ends.reduce((a, b) => (b.cost < a.cost ? b : a))
  const horizontal = s.dir % 2 === 0
  const mid = horizontal ? (s.out.x + t.out.x) / 2 : (s.out.y + t.out.y) / 2
  return simplify([s.at, s.out, horizontal ? { x: mid, y: s.out.y } : { x: s.out.x, y: mid }, horizontal ? { x: mid, y: t.out.y } : { x: t.out.x, y: mid }, t.out, t.at])
}

function shortestRoute(starts: Port[], ends: Port[], walls: Rect[]): Point[] | undefined {
  const xSet = new Set<number>()
  const ySet = new Set<number>()
  for (const w of walls) {
    xSet.add(w.x).add(w.x + w.width)
    ySet.add(w.y).add(w.y + w.height)
  }
  for (const p of [...starts, ...ends]) {
    xSet.add(p.out.x)
    ySet.add(p.out.y)
  }
  // A frame around everything so a route can always go around.
  const xs0 = [...xSet]
  const ys0 = [...ySet]
  xSet.add(Math.min(...xs0) - 2 * CLEARANCE).add(Math.max(...xs0) + 2 * CLEARANCE)
  ySet.add(Math.min(...ys0) - 2 * CLEARANCE).add(Math.max(...ys0) + 2 * CLEARANCE)
  const xs = [...xSet].sort((a, b) => a - b)
  const ys = [...ySet].sort((a, b) => a - b)
  const xi = new Map(xs.map((v, i) => [v, i]))
  const yi = new Map(ys.map((v, i) => [v, i]))
  const nx = xs.length
  const n = nx * ys.length
  // Grid points strictly inside a wall, and grid segments crossing one.
  const blocked = new Uint8Array(n)
  const hBlocked = new Uint8Array(n) // (i, j) → (i + 1, j)
  const vBlocked = new Uint8Array(n) // (i, j) → (i, j + 1)
  for (const w of walls) {
    const i0 = xi.get(w.x)!
    const i1 = xi.get(w.x + w.width)!
    const j0 = yi.get(w.y)!
    const j1 = yi.get(w.y + w.height)!
    for (let j = j0; j <= j1; j++) {
      const insideY = j > j0 && j < j1
      for (let i = i0; i <= i1; i++) {
        const insideX = i > i0 && i < i1
        const k = j * nx + i
        if (insideX && insideY) blocked[k] = 1
        if (insideY && i < i1) hBlocked[k] = 1
        if (insideX && j < j1) vBlocked[k] = 1
      }
    }
  }

  // Dijkstra over (grid point, heading) states.
  const dist = new Float64Array(n * 4).fill(Infinity)
  const prev = new Int32Array(n * 4).fill(-1)
  const heap = new MinHeap()
  const startOf = new Map<number, Port>()
  for (const s of starts) {
    const k = yi.get(s.out.y)! * nx + xi.get(s.out.x)!
    if (blocked[k]) continue
    const state = k * 4 + s.dir
    const cost = s.cost + CLEARANCE
    if (cost < dist[state]) {
      dist[state] = cost
      startOf.set(state, s)
      heap.push(cost, state)
    }
  }
  const endsAt = new Map<number, Port[]>()
  for (const e of ends) {
    const k = yi.get(e.out.y)! * nx + xi.get(e.out.x)!
    if (!blocked[k]) endsAt.set(k, [...(endsAt.get(k) ?? []), e])
  }
  let best = Infinity
  let bestState = -1
  let bestEnd: Port | undefined
  while (heap.size) {
    const [cost, state] = heap.pop()
    if (cost > dist[state]) continue
    if (cost >= best) break
    const k = state >> 2
    const dir = state & 3
    for (const e of endsAt.get(k) ?? []) {
      // The last segment runs into the target against its side's outward direction.
      const inward = (e.dir + 2) % 4
      if (dir === e.dir) continue
      const total = cost + (dir === inward ? 0 : BEND_COST) + e.cost + CLEARANCE
      if (total < best) {
        best = total
        bestState = state
        bestEnd = e
      }
    }
    const i = k % nx
    const j = (k - i) / nx
    for (let d = 0; d < 4; d++) {
      if (d === (dir + 2) % 4) continue
      const ni = i + DIRS[d].x
      const nj = j + DIRS[d].y
      if (ni < 0 || nj < 0 || ni >= nx || nj >= ys.length) continue
      const nk = nj * nx + ni
      if (blocked[nk]) continue
      if (d === 0 ? hBlocked[k] : d === 2 ? hBlocked[nk] : d === 1 ? vBlocked[k] : vBlocked[nk]) continue
      const next = cost + Math.abs(xs[ni] - xs[i]) + Math.abs(ys[nj] - ys[j]) + (d === dir ? 0 : BEND_COST)
      const ns = nk * 4 + d
      if (next < dist[ns]) {
        dist[ns] = next
        prev[ns] = state
        heap.push(next, ns)
      }
    }
  }
  if (bestState < 0 || !bestEnd) return undefined
  const points: Point[] = []
  let state = bestState
  for (; prev[state] >= 0; state = prev[state]) points.push({ x: xs[(state >> 2) % nx], y: ys[Math.floor((state >> 2) / nx)] })
  points.push({ x: xs[(state >> 2) % nx], y: ys[Math.floor((state >> 2) / nx)] })
  points.reverse()
  return simplify([startOf.get(state)!.at, ...points, bestEnd.out, bestEnd.at])
}

/** Drops repeated points and the middle of straight runs. */
function simplify(points: Point[]): Point[] {
  const out: Point[] = []
  for (const p of points) {
    const last = out[out.length - 1]
    if (last && last.x === p.x && last.y === p.y) continue
    const before = out[out.length - 2]
    if (before && last && ((before.x === last.x && last.x === p.x) || (before.y === last.y && last.y === p.y))) out[out.length - 1] = p
    else out.push(p)
  }
  return out
}

/**
 * Moves each middle segment of a route to the middle of the free corridor it runs through, so routes
 * keep off the boxes they pass instead of hugging them, then into a lane of the corridor no route
 * already drawn uses. A segment stays between the far ends of its two neighbors, so they only
 * stretch or shrink.
 */
function centerSegments(points: Point[], walls: Rect[], lanes: Lanes | undefined): Point[] {
  const out = points.map((p) => ({ ...p }))
  for (let s = 1; s < out.length - 2; s++) {
    const a = out[s]
    const b = out[s + 1]
    const vertical = a.x === b.x
    const pos = vertical ? a.x : a.y
    const lo = vertical ? Math.min(a.y, b.y) : Math.min(a.x, b.x)
    const hi = vertical ? Math.max(a.y, b.y) : Math.max(a.x, b.x)
    let min = -Infinity
    let max = Infinity
    for (const w of walls) {
      const [w0, w1, c0, c1] = vertical ? [w.y, w.y + w.height, w.x, w.x + w.width] : [w.x, w.x + w.width, w.y, w.y + w.height]
      if (w1 <= lo || w0 >= hi) continue
      if (c1 <= pos) min = Math.max(min, c1)
      else if (c0 >= pos) max = Math.min(max, c0)
    }
    for (const v of vertical ? [out[s - 1].x, out[s + 2].x] : [out[s - 1].y, out[s + 2].y]) {
      if (v < pos) min = Math.max(min, v)
      else max = Math.min(max, v)
    }
    const centered = Number.isFinite(min) && Number.isFinite(max) && max - min <= 2 * SEARCH_MARGIN ? Math.round((min + max) / 2) : pos
    // Beside the other routes, a lane apart; in a corridor too narrow for that, half a lane.
    const lane = (gap: number) => [...LANE_STEPS.map((k) => centered + k * gap), pos].find((p) => p >= min && p <= max && !lanes?.taken(vertical, p, lo, hi, gap))
    const next = (lanes && (lane(LANE) ?? lane(LANE / 2))) ?? centered
    if (vertical) a.x = b.x = next
    else a.y = b.y = next
  }
  return simplify(out)
}

/** A binary min-heap of (cost, state) pairs. */
class MinHeap {
  private costs: number[] = []
  private states: number[] = []
  get size() {
    return this.costs.length
  }
  push(cost: number, state: number) {
    const c = this.costs
    const s = this.states
    let i = c.length
    c.push(cost)
    s.push(state)
    while (i > 0) {
      const parent = (i - 1) >> 1
      if (c[parent] <= cost) break
      c[i] = c[parent]
      s[i] = s[parent]
      i = parent
    }
    c[i] = cost
    s[i] = state
  }
  pop(): [number, number] {
    const c = this.costs
    const s = this.states
    const top: [number, number] = [c[0], s[0]]
    const lastC = c.pop()!
    const lastS = s.pop()!
    if (c.length) {
      let i = 0
      for (;;) {
        const l = 2 * i + 1
        if (l >= c.length) break
        const r = l + 1
        const m = r < c.length && c[r] < c[l] ? r : l
        if (c[m] >= lastC) break
        c[i] = c[m]
        s[i] = s[m]
        i = m
      }
      c[i] = lastC
      s[i] = lastS
    }
    return top
  }
}
