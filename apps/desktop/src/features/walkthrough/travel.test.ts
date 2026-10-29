import { describe, expect, test } from "bun:test"
import { arrivalRoute, bridgeRoute, cameraFor, hopLinks, unionRect } from "./travel"

const box = (x: number, y: number, width = 100, height = 50) => ({ x, y, width, height })

describe("bridgeRoute", () => {
  test("side by side: from the facing right edge to the facing left edge", () => {
    const route = bridgeRoute(box(0, 0), box(300, 100))
    expect(route[0]).toEqual({ x: 100, y: 25 })
    expect(route[route.length - 1]).toEqual({ x: 300, y: 125 })
  })

  test("leftwards and stacked", () => {
    const left = bridgeRoute(box(300, 0), box(0, 0))
    expect(left[0]).toEqual({ x: 300, y: 25 })
    expect(left[left.length - 1]).toEqual({ x: 100, y: 25 })
    const down = bridgeRoute(box(0, 0), box(0, 300))
    expect(down[0]).toEqual({ x: 50, y: 50 })
    expect(down[down.length - 1]).toEqual({ x: 50, y: 300 })
  })

  test("arrivalRoute comes in from the left", () => {
    expect(arrivalRoute(box(200, 0), 100)).toEqual([
      { x: 100, y: 25 },
      { x: 200, y: 25 },
    ])
  })
})

describe("hopLinks", () => {
  const edges = [
    { id: "api>integration", source: "api", target: "integration" },
    { id: "lambda>integration", source: "lambda", target: "integration" },
    { id: "rg>lambda", source: "rg", target: "lambda" },
    { id: "rg>api", source: "rg", target: "api" },
    { id: "table>lambda", source: "table", target: "lambda" },
  ]
  const items = new Set(["api", "lambda", "table", "rg"])

  test("direct edges, either direction", () => {
    expect(hopLinks(edges, ["lambda"], ["table"], (id) => items.has(id))).toEqual({ edges: ["table>lambda"], via: [] })
  })

  test("through a helper nothing else stands for, never through a drawn item", () => {
    expect(hopLinks(edges, ["api"], ["lambda"], (id) => items.has(id))).toEqual({ edges: ["api>integration", "lambda>integration"], via: ["integration"] })
    expect(hopLinks(edges, ["api"], ["table"], (id) => items.has(id))).toEqual({ edges: [], via: [] })
  })
})

describe("camera", () => {
  test("unionRect", () => {
    expect(unionRect([box(0, 0), box(200, 100)])).toEqual({ x: 0, y: 0, width: 300, height: 150 })
    expect(unionRect([])).toBeUndefined()
  })

  test("fits the hop above the caption panel, within the zoom bounds", () => {
    const view = { width: 1000, height: 800 }
    const cam = cameraFor(box(0, 0, 400, 100), view, 200, { padding: 100, maxZoom: 1 })
    expect(cam.zoom).toBe(1)
    // The hop's center (200, 50) sits 100 px above the viewport's center, in the middle of the uncovered part.
    expect(cam).toEqual({ x: 200, y: 150, zoom: 1 })
    const wide = cameraFor(box(0, 0, 4000, 100), view, 200, { padding: 100, minZoom: 0.1 })
    expect(wide.zoom).toBeCloseTo(0.2)
  })

  test("never zooms out below a legible zoom: too large a focus centers on its anchor instead", () => {
    const view = { width: 1000, height: 800 }
    const anchor = box(3000, 0, 200, 100)
    const cam = cameraFor(box(0, 0, 4000, 100), view, 200, { padding: 100, anchor })
    expect(cam.zoom).toBe(0.65)
    expect(cam.x).toBe(3100)
    // Without an anchor it stays on the focus' center.
    expect(cameraFor(box(0, 0, 4000, 100), view, 200, { padding: 100 }).x).toBe(2000)
  })
})
