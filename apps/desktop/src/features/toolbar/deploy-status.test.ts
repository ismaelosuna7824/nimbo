import { describe, expect, test } from "bun:test"
import type { DeployedState, LogLine, Run } from "@/lib/api/types"
import {
  activeRun,
  deployedCount,
  deployStatus,
  endedRuns,
  firstErrorLine,
  justDeployed,
  lastDeployRun,
  shortAgo,
} from "./deploy-status"

const NOW = Date.parse("2026-10-29T12:00:00Z")
const ago = (s: number) => new Date(NOW - s * 1000).toISOString()

const run = (id: string, action: Run["action"], status: Run["status"], over: Partial<Run> = {}): Run => ({
  id,
  workflowId: "wf",
  action,
  status,
  createdAt: ago(600),
  exitCode: 0,
  ...over,
})

const line = (stream: LogLine["stream"], text: string): LogLine => ({ t: "", stream, text })

describe("activeRun", () => {
  test("finds the queued or running run", () => {
    expect(activeRun([run("a", "plan", "succeeded"), run("b", "apply", "running")])?.id).toBe("b")
    expect(activeRun([run("a", "apply", "queued")])?.id).toBe("a")
    expect(activeRun([run("a", "apply", "failed")])).toBeUndefined()
  })
})

describe("lastDeployRun", () => {
  test("is the newest ended deploy or destroy, ignoring other actions", () => {
    const runs = [run("p", "plan", "failed"), run("v", "validate", "succeeded"), run("d", "apply", "succeeded"), run("x", "destroy", "succeeded")]
    expect(lastDeployRun(runs)?.id).toBe("d")
  })

  test("skips runs in progress and needs_confirmation (nothing was applied)", () => {
    const runs = [run("r", "apply", "running"), run("n", "apply", "needs_confirmation"), run("f", "apply", "failed")]
    expect(lastDeployRun(runs)?.id).toBe("f")
  })

  test("covers docker and Kubernetes up/down, not their other actions", () => {
    expect(lastDeployRun([run("b", "build", "succeeded"), run("u", "up", "succeeded")])?.id).toBe("u")
    expect(lastDeployRun([run("c", "cluster-stop", "succeeded"), run("d", "down", "failed")])?.id).toBe("d")
  })

  test("is undefined before the first deploy", () => {
    expect(lastDeployRun([run("p", "plan", "succeeded")])).toBeUndefined()
  })
})

describe("shortAgo", () => {
  test("rounds down to the largest unit", () => {
    expect(shortAgo(ago(10), NOW)).toBe("just now")
    expect(shortAgo(ago(150), NOW)).toBe("2m ago")
    expect(shortAgo(ago(3 * 3600 + 59), NOW)).toBe("3h ago")
    expect(shortAgo(ago(50 * 3600), NOW)).toBe("2d ago")
  })

  test("is empty when the time is unknown and never negative", () => {
    expect(shortAgo(undefined, NOW)).toBe("")
    expect(shortAgo("not a date", NOW)).toBe("")
    expect(shortAgo(new Date(NOW + 5000).toISOString(), NOW)).toBe("just now")
  })
})

describe("deployStatus", () => {
  test("a deploy that worked is green, with its time", () => {
    expect(deployStatus(run("a", "apply", "succeeded", { endedAt: ago(120) }), NOW)).toEqual({ label: "Deployed 2m ago", tone: "ok" })
    expect(deployStatus(run("a", "up", "succeeded", { endedAt: ago(5) }), NOW)).toEqual({ label: "Deployed just now", tone: "ok" })
  })

  test("only a failure is red", () => {
    expect(deployStatus(run("a", "apply", "failed"), NOW)).toEqual({ label: "Deploy failed", tone: "failed" })
    expect(deployStatus(run("a", "destroy", "failed"), NOW)).toEqual({ label: "Destroy failed", tone: "failed" })
    expect(deployStatus(run("a", "down", "failed"), NOW)).toEqual({ label: "Remove failed", tone: "failed" })
  })

  test("destroys and canceled runs are plain information", () => {
    expect(deployStatus(run("a", "destroy", "succeeded", { endedAt: ago(3600) }), NOW)).toEqual({ label: "Destroyed 1h ago", tone: "muted" })
    expect(deployStatus(run("a", "down", "succeeded", { endedAt: ago(60) }), NOW)).toEqual({ label: "Removed 1m ago", tone: "muted" })
    expect(deployStatus(run("a", "apply", "canceled"), NOW)).toEqual({ label: "Deploy canceled", tone: "muted" })
  })

  test("falls back to the creation time when the end is unknown", () => {
    expect(deployStatus(run("a", "apply", "succeeded", { createdAt: ago(300) }), NOW).label).toBe("Deployed 5m ago")
  })
})

describe("endedRuns and justDeployed", () => {
  test("report runs that went from queued or running to ended", () => {
    const prev = [run("a", "apply", "running"), run("b", "plan", "queued"), run("c", "apply", "succeeded")]
    const next = [run("a", "apply", "succeeded"), run("b", "plan", "running"), run("c", "apply", "succeeded")]
    expect(endedRuns(prev, next).map((r) => r.id)).toEqual(["a"])
    expect(justDeployed(prev, next)?.id).toBe("a")
  })

  test("ignore runs first seen ended (loaded history) and unchanged lists", () => {
    const next = [run("a", "apply", "succeeded")]
    expect(endedRuns([], next)).toEqual([])
    expect(endedRuns(next, next)).toEqual([])
  })

  test("justDeployed is only for deploys that worked", () => {
    const prev = [run("a", "apply", "running"), run("b", "destroy", "running")]
    expect(justDeployed(prev, [run("a", "apply", "failed"), run("b", "destroy", "succeeded")])).toBeUndefined()
    expect(justDeployed([run("u", "up", "running")], [run("u", "up", "succeeded")])?.id).toBe("u")
  })
})

describe("firstErrorLine", () => {
  test("is the first Error: line of the log, without the prefix", () => {
    const log = [
      line("system", "$ terraform apply"),
      line("stderr", ""),
      line("stderr", "Error: Error creating Service: googleapi: Error 400: invalid"),
      line("stderr", "  with google_cloud_run_v2_service.orders_api,"),
      line("system", "Run failed: terraform apply exited with code 1"),
    ]
    expect(firstErrorLine(log, "terraform apply exited with code 1")).toBe("Error creating Service: googleapi: Error 400: invalid")
  })

  test("matches lowercase error lines of docker and kubectl", () => {
    expect(firstErrorLine([line("stdout", "ok"), line("stderr", "error: no objects passed to apply")])).toBe("no objects passed to apply")
  })

  test("falls back to the first stderr line, then to the run's error", () => {
    expect(firstErrorLine([line("stdout", "building"), line("stderr", "  pull access denied  ")], "x")).toBe("pull access denied")
    expect(firstErrorLine([line("stdout", "building")], "exited with code 1")).toBe("exited with code 1")
    expect(firstErrorLine(undefined)).toBe("")
  })

  test("cuts long lines", () => {
    const text = firstErrorLine([line("stderr", `Error: ${"x".repeat(500)}`)])
    expect(text.length).toBe(200)
    expect(text.endsWith("…")).toBe(true)
  })
})

describe("deployedCount", () => {
  test("counts managed resources, not data sources", () => {
    const res = (mode: "managed" | "data") => ({ address: "a", mode, type: "t", name: "n", values: {} })
    const state: DeployedState = { workflowId: "wf", resources: [res("managed"), res("data"), res("managed")], outputs: {}, updatedAt: "" }
    expect(deployedCount(state)).toBe(2)
    expect(deployedCount(null)).toBe(0)
  })
})
