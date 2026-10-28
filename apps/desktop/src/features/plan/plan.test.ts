import { describe, expect, test } from "bun:test"
import type { PlannedChange, Run } from "@/lib/api/types"
import { countActions, latestPlan, offCanvasChanges, planByNode, planStaleness } from "./plan"

const run = (over: Partial<Run>): Run => ({
  id: "r",
  workflowId: "wf",
  action: "plan",
  status: "succeeded",
  createdAt: "2026-10-27T10:00:00Z",
  exitCode: 0,
  workflowVersion: "2026-10-27T09:59:00.123456789Z",
  ...over,
})

const change = (address: string, action: PlannedChange["action"], nodeId?: string): PlannedChange => ({
  address,
  nodeId,
  mode: "managed",
  type: "aws_s3_bucket",
  name: address,
  action,
})

describe("latestPlan", () => {
  test("is the newest successful plan of the active environment", () => {
    const runs = [
      run({ id: "running", status: "running", createdAt: "2026-10-27T10:05:00Z" }),
      run({ id: "prod", environment: "prod", createdAt: "2026-10-27T10:04:00Z" }),
      run({ id: "failed", status: "failed", createdAt: "2026-10-27T10:03:00Z" }),
      run({ id: "apply", action: "apply", createdAt: "2026-10-27T10:02:00Z" }),
      run({ id: "base", createdAt: "2026-10-27T10:01:00Z" }),
    ]
    expect(latestPlan(runs, undefined)?.id).toBe("base")
    expect(latestPlan(runs, "prod")?.id).toBe("prod")
    expect(latestPlan(runs, "dev")).toBeUndefined()
  })
})

describe("planStaleness", () => {
  const plan = run({})

  test("a plan of the current content is current", () => {
    expect(planStaleness(plan, [plan], "2026-10-27T09:59:00.123456789Z")).toBeNull()
  })

  test("a plan started after a layout-only save (version older than the plan's) is current", () => {
    expect(planStaleness(plan, [plan], "2026-10-27T09:58:00Z")).toBeNull()
  })

  test("a content save after the plan makes it outdated", () => {
    expect(planStaleness(plan, [plan], "2026-10-27T10:00:30Z")).toBe("edited")
  })

  test("a plan without a recorded version is never shown as current", () => {
    expect(planStaleness(run({ workflowVersion: undefined }), [], "2026-10-27T09:00:00Z")).toBe("edited")
  })

  test("an apply or destroy started after the plan makes it outdated, earlier ones don't", () => {
    const before = run({ id: "a0", action: "apply", createdAt: "2026-10-27T09:00:00Z" })
    const validate = run({ id: "v", action: "validate", createdAt: "2026-10-27T10:01:00Z" })
    expect(planStaleness(plan, [validate, plan, before], plan.workflowVersion!)).toBeNull()
    const destroy = run({ id: "d", action: "destroy", status: "failed", createdAt: "2026-10-27T10:01:00.5Z" })
    expect(planStaleness(plan, [destroy, plan], plan.workflowVersion!)).toBe("deployed")
  })
})

describe("planByNode", () => {
  test("folds a node's instances into the most consequential action and leaves quiet ones out", () => {
    const byNode = planByNode([
      change("b[0]", "no-op", "b"),
      change("b[1]", "delete", "b"),
      change("b[2]", "create", "b"),
      change("m.x", "update", "m"),
      change("m.y", "replace", "m"),
      change("keep", "no-op", "keep"),
      change("data", "read", "data"),
      change("gone", "delete"),
    ])
    expect(byNode.get("b")?.action).toBe("delete")
    expect(byNode.get("b")?.changes.map((c) => c.address)).toEqual(["b[1]", "b[2]"])
    expect(byNode.get("m")?.action).toBe("replace")
    expect(byNode.has("keep")).toBe(false)
    expect(byNode.has("data")).toBe(false)
    expect(byNode.size).toBe(2)
  })
})

describe("offCanvasChanges", () => {
  test("lists marked changes without a card: no node, or a node no longer on the canvas", () => {
    const changes = [change("gone", "delete"), change("removed", "delete", "n-removed"), change("a", "update", "n-a"), change("noop", "no-op")]
    expect(offCanvasChanges(changes, new Set(["n-a"])).map((c) => c.address)).toEqual(["gone", "removed"])
  })
})

test("countActions counts marked instances only", () => {
  expect(countActions([change("a", "create"), change("b", "create"), change("c", "replace"), change("d", "no-op")])).toEqual({
    create: 2,
    update: 0,
    replace: 1,
    delete: 0,
  })
})
