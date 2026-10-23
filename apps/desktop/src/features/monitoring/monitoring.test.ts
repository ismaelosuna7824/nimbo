import { describe, expect, test } from "bun:test"
import type { Workflow } from "@/lib/api/types"
import { alertsByTarget, formatCost, httpCoverage } from "./monitoring"
import { hasMonitoring } from "./monitoring-store"

describe("formatCost", () => {
  test("CPU in cores, sizes in binary units, no disk when there is none", () => {
    expect(formatCost({ cpuMillis: 410, memoryBytes: 1216 * 2 ** 20, diskBytes: 5 * 2 ** 30 })).toBe("0.41 CPU · 1.2 GiB memory · 5 GiB disk")
    expect(formatCost({ cpuMillis: 200, memoryBytes: 448 * 2 ** 20, diskBytes: 0 })).toBe("0.2 CPU · 448 MiB memory")
  })
})

describe("alertsByTarget", () => {
  test("groups by target in first-seen order", () => {
    const a = (name: string, target: string) => ({ name, target, severity: "warning" as const, summary: "" })
    const groups = alertsByTarget([a("Down", "api"), a("OOM", "db"), a("OOM", "api")])
    expect(groups.map((g) => [g.target, g.alerts.map((x) => x.name)])).toEqual([
      ["api", ["Down", "OOM"]],
      ["db", ["OOM"]],
    ])
  })
})

describe("httpCoverage", () => {
  test("counts measured targets and explains the sources' requirements", () => {
    const cov = httpCoverage([
      { target: "shop/Deployment/web", sources: ["traefik", "ingress-nginx"] },
      { target: "shop/Deployment/worker", sources: [] },
    ])
    expect(cov.measured).toBe(1)
    expect(cov.notes).toHaveLength(2)
    expect(cov.notes[0]).toContain("controller.metrics.enabled")
    expect(httpCoverage([{ target: "api", sources: ["traefik"] }]).notes).toEqual([])
  })
})

describe("hasMonitoring", () => {
  test("docker: the generated compose file is listed", () => {
    expect(hasMonitoring({ kind: "docker", compose: { files: ["compose.yaml", "monitoring/compose.yaml"] } } as Workflow)).toBe(true)
    expect(hasMonitoring({ kind: "docker", compose: { files: ["docker/compose.yaml", "docker/monitoring/compose.yaml"] } } as Workflow)).toBe(true)
    expect(hasMonitoring({ kind: "docker", compose: { files: ["compose.yaml"] } } as Workflow)).toBe(false)
  })
  test("kubernetes: the chart (or a wrapper of it) and the generated source", () => {
    const chart = { kind: "helm", path: "charts/kps", chart: { name: "kps", version: "0.1.0", dependencies: [{ name: "kube-prometheus-stack" }] } }
    const generated = { kind: "manifests", path: "monitoring" }
    const kube = (sources: unknown[]) => ({ kind: "kubernetes", kubernetes: { sources } }) as unknown as Workflow
    expect(hasMonitoring(kube([{ kind: "manifests", path: "k8s" }, chart, generated]))).toBe(true)
    expect(hasMonitoring(kube([chart]))).toBe(false)
    expect(hasMonitoring(kube([generated]))).toBe(false)
  })
})
