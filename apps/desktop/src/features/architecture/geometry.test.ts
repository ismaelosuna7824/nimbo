import { describe, expect, test } from "bun:test"
import { Lanes, routeOrthogonal, wrapParts, type Point, type Rect } from "./geometry"

/** Whether segment a→b passes through the inside of `r`. */
const hits = (a: Point, b: Point, r: Rect) =>
  Math.max(a.x, b.x) > r.x && Math.min(a.x, b.x) < r.x + r.width && Math.max(a.y, b.y) > r.y && Math.min(a.y, b.y) < r.y + r.height

describe("routeOrthogonal", () => {
  test("goes around the cards between the two ends, with right angles only", () => {
    const source = { x: 0, y: 100, width: 260, height: 80 }
    const target = { x: 700, y: 100, width: 260, height: 80 }
    const wall = { x: 350, y: 40, width: 260, height: 200 }
    const route = routeOrthogonal(source, target, [wall])
    expect(route[0]).toEqual({ x: 260, y: 140 })
    expect(route[route.length - 1]).toEqual({ x: 700, y: 140 })
    for (let i = 1; i < route.length; i++) {
      const [a, b] = [route[i - 1], route[i]]
      expect(a.x === b.x || a.y === b.y).toBe(true)
      expect(hits(a, b, wall)).toBe(false)
    }
  })

  test("leaves through the side facing the other end", () => {
    const route = routeOrthogonal({ x: 0, y: 0, width: 100, height: 50 }, { x: 0, y: 300, width: 100, height: 50 }, [])
    expect(route).toEqual([
      { x: 50, y: 50 },
      { x: 50, y: 300 },
    ])
  })

  test("routes beside routes already drawn never share a segment or an end", () => {
    // Two arrows leave one box for two boxes far right, past a wall: alone, both would run along the
    // same corridor and leave through the same point.
    const source = { x: 0, y: 200, width: 260, height: 80 }
    const wall = { x: 400, y: 150, width: 260, height: 300 }
    const targets = [
      { x: 800, y: 0, width: 260, height: 80 },
      { x: 800, y: 60, width: 260, height: 80 },
    ]
    const alone = targets.map((t) => routeOrthogonal(source, t, [wall]))
    const lanes = new Lanes()
    const routes = targets.map((t) => {
      const route = routeOrthogonal(source, t, [wall], lanes)
      lanes.add(route)
      return route
    })
    const segments = (route: Point[]) => route.slice(1).map((b, i) => ({ a: route[i], b }))
    /** Whether two routes run along each other (parallel, overlapping, closer than 5 px) or start at one point. */
    const merge = ([r1, r2]: Point[][]) =>
      (r1[0].x === r2[0].x && r1[0].y === r2[0].y) ||
      segments(r1).some(({ a, b }) =>
        segments(r2).some(({ a: c, b: d }) => {
          const vertical = a.x === b.x
          if (vertical !== (c.x === d.x)) return false
          const [p, q] = vertical ? [a.x, c.x] : [a.y, c.y]
          const [lo1, hi1] = vertical ? [Math.min(a.y, b.y), Math.max(a.y, b.y)] : [Math.min(a.x, b.x), Math.max(a.x, b.x)]
          const [lo2, hi2] = vertical ? [Math.min(c.y, d.y), Math.max(c.y, d.y)] : [Math.min(c.x, d.x), Math.max(c.x, d.x)]
          return Math.abs(p - q) < 5 && lo1 < hi2 && lo2 < hi1
        }),
      )
    expect(merge(alone)).toBe(true)
    expect(merge(routes)).toBe(false)
    for (const route of routes) for (const { a, b } of segments(route)) expect(a.x === b.x || a.y === b.y).toBe(true)
  })
})

describe("wrapParts", () => {
  test("breaks between parts before breaking inside one", () => {
    // Without a canvas, text is measured at 0.56 × font size per character: 11px → ~6.2px.
    const lines = wrapParts(["orders", "postgres 16.3", "db.r6g.large", "SG db: in 5432 from SG app"], "400 11px sans-serif", 240, 2)
    expect(lines).toEqual(["orders · postgres 16.3 · db.r6g.large", "SG db: in 5432 from SG app"])
  })

  test("a part wider than a line breaks between its words and ends in an ellipsis", () => {
    const folded = "+ S3 Bucket Policy, S3 Public Access Block, S3 Bucket Website Configuration"
    const [first, second, ...rest] = wrapParts(["site", "static website", folded], "400 11px sans-serif", 200, 2)
    expect(first).toBe("site · static website")
    expect(second.startsWith("+ S3 Bucket Policy, S3 Public")).toBe(true)
    expect(second.endsWith("…")).toBe(true)
    expect(second).not.toContain(" · ")
    expect(rest).toEqual([])
  })
})
