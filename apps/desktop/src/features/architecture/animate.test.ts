import { describe, expect, test } from "bun:test"
import { bestPreset, dirtyTiles, FLASH_TIME, flowAt, flowPlan, frameCount, frameLayout, frameTime, measurePath, pointAt, roundedRoute } from "./animate"
import type { Point } from "./geometry"
import type { DiagramEdge, DiagramItem } from "./layout"

const LIMITS = { scale: 2, maxSide: 4000, maxPixels: 40_000_000 }

describe("frameLayout", () => {
  test("centers the diagram inside the preset's padding, scaled to fit", () => {
    const f = frameLayout({ width: 1000, height: 600 }, "portrait", LIMITS)
    expect([f.width, f.height]).toEqual([1080, 1350])
    expect(f.scale).toBeCloseTo(0.972, 3)
    expect(f.dx).toBe(54) // the 5% padding on the limiting side
    expect(f.dy).toBe(Math.round((1350 - 600 * f.scale) / 2))
    const l = frameLayout({ width: 1000, height: 600 }, "landscape", LIMITS)
    expect([l.width, l.height]).toEqual([1920, 1080])
    expect(l.dy).toBe(54)
    expect(l.dx).toBeGreaterThan(54)
  })

  test("does not blow small diagrams up past 3×", () => {
    const f = frameLayout({ width: 200, height: 100 }, "square", LIMITS)
    expect(f.scale).toBe(3)
    expect(f.dx).toBe((1080 - 600) / 2)
  })

  test("fit keeps the chosen scale within the size limits, with even sides", () => {
    expect(frameLayout({ width: 1000, height: 601 }, "fit", LIMITS)).toEqual({ width: 2000, height: 1202, scale: 2, dx: 0, dy: 0 })
    const capped = frameLayout({ width: 1000, height: 601 }, "fit", { scale: 2, maxSide: 1920, maxPixels: 40_000_000 })
    expect(capped.scale).toBeCloseTo(1.92)
    expect([capped.width, capped.height]).toEqual([1920, 1154])
    const pixels = frameLayout({ width: 2000, height: 2000 }, "fit", { scale: 4, maxSide: 100_000, maxPixels: 4_000_000 })
    expect(pixels.width * pixels.height).toBeLessThanOrEqual(4_000_000 + 2 * 2000 + 4)
    expect(pixels.width % 2).toBe(0)
  })
})

describe("bestPreset", () => {
  test("picks the social frame that shows the diagram largest", () => {
    expect(bestPreset({ width: 1600, height: 500 })).toBe("landscape")
    expect(bestPreset({ width: 800, height: 800 })).toBe("square")
    expect(bestPreset({ width: 700, height: 900 })).toBe("portrait")
  })
})

describe("frame timing", () => {
  test("a loop's frames end one frame before the start comes round again", () => {
    expect(frameCount(6, 30)).toBe(180)
    expect(frameCount(6, 20)).toBe(120)
    expect(frameTime(0, 180, 6)).toBe(0)
    expect(frameTime(90, 180, 6)).toBe(3)
    expect(frameTime(180, 180, 6)).toBe(6)
  })
})

describe("paths", () => {
  test("rounded corners follow polylinePath's curve and stay inside the bend", () => {
    const pts = roundedRoute([
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
    ])
    expect(pts).toContainEqual({ x: 92, y: 0 })
    expect(pts[pts.length - 2]).toEqual({ x: 100, y: 8 })
    expect(pts.every((p) => p.x >= 0 && p.x <= 100 && p.y >= 0 && p.y <= 100)).toBe(true)
    const len = measurePath(pts).length
    expect(len).toBeLessThan(200)
    expect(len).toBeGreaterThan(195)
  })

  test("pointAt walks the polyline and clamps at its ends", () => {
    const m = measurePath([
      { x: 0, y: 0 },
      { x: 100, y: 0 },
      { x: 100, y: 100 },
    ])
    expect(m.length).toBe(200)
    expect(pointAt(m, 50)).toEqual({ x: 50, y: 0 })
    expect(pointAt(m, 150)).toEqual({ x: 100, y: 50 })
    expect(pointAt(m, -5)).toEqual({ x: 0, y: 0 })
    expect(pointAt(m, 500)).toEqual({ x: 100, y: 100 })
  })
})

const item = (id: string, ax: number, kind: DiagramItem["kind"] = "card"): DiagramItem => ({ id, kind, x: ax, y: 0, ax, ay: 0, width: 100, height: 80 })
const edge = (source: string, target: string, route: Point[], kind: DiagramEdge["kind"] = "data"): DiagramEdge => ({ id: `${source}->${target}`, source, target, kind, route })
const diagramOf = (items: DiagramItem[], edges: DiagramEdge[]) => ({ byId: new Map(items.map((i) => [i.id, i])), edges })

describe("flow", () => {
  // users (0..100) → api (300..400) → db (600..700), routes 200 px each.
  const d = diagramOf(
    [item("users", 0, "actor"), item("api", 300), item("db", 600)],
    [
      edge("api", "db", [
        { x: 400, y: 40 },
        { x: 600, y: 40 },
      ]),
      edge("users", "api", [
        { x: 100, y: 40 },
        { x: 300, y: 40 },
      ]),
    ],
  )

  test("edges start where the wave from the entry point reaches their source", () => {
    const plan = flowPlan(d, 6)
    expect(plan.edges.find((e) => e.id === "users->api")!.start).toBe(0)
    expect(plan.edges.find((e) => e.id === "api->db")!.start).toBe(200)
    expect(plan.speed).toBe(260)
    expect(plan.waves).toBe(4)
  })

  test("a pulse moves along its arrow and the loop is seamless", () => {
    const plan = flowPlan(d, 6)
    const at = (t: number) => flowAt(plan, t).pulses.map((p) => ({ x: Math.round(p.x * 1000), y: Math.round(p.y * 1000), a: Math.round(p.alpha * 1000) }))
    expect(at(6)).toEqual(at(0))
    expect(at(6 + 1.234)).toEqual(at(1.234))
    // Wave 0 at t = 0.5 s has travelled 130 px: on the first arrow, heading right.
    const lead = flowAt(plan, 0.5).pulses.filter((p) => p.alpha === 1)
    expect(lead.map((p) => Math.round(p.x))).toEqual([230])
    const later = flowAt(plan, 0.6).pulses.filter((p) => p.alpha === 1)
    expect(later.map((p) => Math.round(p.x))).toEqual([256])
  })

  test("a node lights up when a pulse arrives and fades within FLASH_TIME", () => {
    const plan = flowPlan(d, 6)
    const arrive = 200 / plan.speed
    expect(flowAt(plan, arrive + 0.01).flashes.map((f) => f.rect.x)).toContain(300)
    expect(flowAt(plan, arrive + FLASH_TIME + 0.01).flashes.map((f) => f.rect.x)).not.toContain(300)
  })

  test("speeds up so a wave crosses a long diagram within the loop", () => {
    const long = diagramOf(
      [item("a", 0), item("b", 3000)],
      [
        edge("a", "b", [
          { x: 100, y: 0 },
          { x: 3000, y: 0 },
        ]),
      ],
    )
    const plan = flowPlan(long, 4)
    expect(2900 / plan.speed).toBeLessThanOrEqual(4 - FLASH_TIME + 1e-9)
  })

  test("a cycle nothing enters starts at its leftmost item", () => {
    const cyc = diagramOf(
      [item("a", 500), item("b", 0)],
      [
        edge("a", "b", [
          { x: 500, y: 0 },
          { x: 100, y: 0 },
        ]),
        edge("b", "a", [
          { x: 100, y: 80 },
          { x: 500, y: 80 },
        ]),
      ],
    )
    const plan = flowPlan(cyc, 6)
    expect(plan.edges.find((e) => e.id === "b->a")!.start).toBe(0)
    expect(plan.edges.find((e) => e.id === "a->b")!.start).toBe(400)
  })
})

describe("dirtyTiles", () => {
  test("covers every drawn area with one rectangle per run of touched tiles", () => {
    const tiles = dirtyTiles(
      [
        { x: 5, y: 5, width: 10, height: 10 },
        { x: 40, y: 10, width: 10, height: 10 },
        { x: 90, y: 90, width: 30, height: 30 },
      ],
      100,
      100,
    )
    expect(tiles).toEqual([
      { x: 0, y: 0, width: 64, height: 32 },
      { x: 64, y: 64, width: 36, height: 32 },
      { x: 64, y: 96, width: 36, height: 4 },
    ])
  })
})
