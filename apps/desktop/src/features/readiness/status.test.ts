import { describe, expect, test } from "bun:test"
import type { EnvironmentReadiness, ReadinessGroup } from "@/lib/api/types"
import { CELL_COLUMNS, defaultEnvironment, scoreLabel, scoreTone, untestedSummary } from "./status"

function group(id: ReadinessGroup["id"], title: string, untested: number): ReadinessGroup {
  return { id, title, weight: 10, score: null, untested, items: [] }
}

function env(name: string, productionLike: boolean, groups: ReadinessGroup[] = []): EnvironmentReadiness {
  return { name, productionLike, status: "ready", score: 100, confidence: 100, summary: "", groups, components: [] }
}

describe("readiness status", () => {
  test("the first production-like environment is shown unless one was picked", () => {
    const envs = [env("", false), env("dev", false), env("prod", true)]
    expect(defaultEnvironment(envs, null, "dev")).toBe("prod")
    expect(defaultEnvironment(envs, "dev", "")).toBe("dev")
    expect(defaultEnvironment(envs, "gone", "")).toBe("prod")
    expect(defaultEnvironment([env("", false), env("dev", false)], null, "dev")).toBe("dev")
    expect(defaultEnvironment([], null, "")).toBeNull()
  })

  test("untested evidence is summarized by category", () => {
    const e = env("prod", true, [group("performance", "Performance and capacity", 2), group("resilience", "Resilience", 3), group("security", "Security", 0)])
    expect(untestedSummary(e)).toBe("Not tested yet: performance and capacity 2, resilience 3")
    expect(untestedSummary(env("prod", true, [group("security", "Security", 0)]))).toBe("Every applicable check has evidence")
  })

  test("scores without evidence read as a dash", () => {
    expect(scoreLabel(null)).toBe("—")
    expect(scoreLabel(0)).toBe("0")
    expect(scoreTone(null)).toBe("text-muted-foreground")
    expect(scoreTone(90)).toContain("emerald")
    expect(scoreTone(89)).toContain("amber")
    expect(scoreTone(69)).toBe("text-destructive")
  })

  test("component columns follow the daemon's cell order", () => {
    expect(CELL_COLUMNS.map((c) => c.id)).toEqual(["rules", "health", "resources", "monitoring", "load", "resilience", "capacity"])
  })
})
