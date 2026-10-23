import { describe, expect, test } from "bun:test"
import { longestChain, WRAP_FROM_LAYERS } from "./layout"

const chain = (...ids: string[]) => ids.slice(1).map((t, i) => ({ source: ids[i], target: t }))

describe("longestChain", () => {
  test("an Ingress → Service → Deployment → … chain wraps, a three-service compose stack does not", () => {
    const k8s = chain("ing", "svc-fe", "fe", "svc-be", "be", "svc-redis", "redis")
    expect(longestChain(k8s)).toBe(7)
    expect(longestChain(k8s)).toBeGreaterThanOrEqual(WRAP_FROM_LAYERS)
    const compose = [...chain("proxy", "api", "db"), { source: "api", target: "cache" }]
    expect(longestChain(compose)).toBe(3)
    expect(longestChain(compose)).toBeLessThan(WRAP_FROM_LAYERS)
  })

  test("counts the longest branch, not the edge count", () => {
    const edges = [...chain("a", "b", "c", "d"), { source: "a", target: "x" }, { source: "x", target: "d" }]
    expect(longestChain(edges)).toBe(4)
  })

  test("terminates on cycles", () => {
    expect(longestChain([...chain("a", "b", "c"), { source: "c", target: "a" }])).toBe(3)
    expect(longestChain([])).toBe(0)
  })
})
