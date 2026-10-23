import { describe, expect, test } from "bun:test"
import { DNS_LABEL, autoscalingError, describeLoadTest, formatDescription, sizingNote, suggestKubeName } from "./convert-kubernetes"

describe("formatDescription", () => {
  test("lists the docker environments the output carries", () => {
    expect(formatDescription("kustomize", [])).toContain("overlays/dev and overlays/prod")
    expect(formatDescription("kustomize", ["dev", "prod", "qa"])).toContain("overlays/dev, overlays/prod and overlays/qa")
    expect(formatDescription("helm", ["prod"])).toContain("values-prod.yaml;")
    expect(formatDescription("manifests", ["dev", "prod"])).toContain("dev and prod environments are left out")
  })
})

describe("sizingNote", () => {
  test("summarizes the measurement, flags censored ones, falls back to the provenance", () => {
    const measured = { cpuP90: 0.205, cpuPeak: 0.2209, memPeak: 51747226 }
    expect(sizingNote({ detail: "from load test …", measured })).toBe("measured p90 0.2 · peak 0.22 CPU · 50Mi")
    expect(sizingNote({ detail: "", measured, censored: "cpu" })).toBe("measured p90 0.2 · peak 0.22 CPU · 50Mi · hit its Docker limit")
    expect(sizingNote({ detail: "cpu limit from compose" })).toBe("cpu limit from compose")
  })
})

describe("autoscalingError", () => {
  test("accepts the bounds the converter accepts", () => {
    expect(autoscalingError(2, 10)).toBe("")
    expect(autoscalingError(100, 95)).toBe("")
    expect(autoscalingError(5, 70)).toBe("")
  })

  test("rejects values outside them, fractions and empty fields", () => {
    expect(autoscalingError(1, 70)).toContain("Max replicas")
    expect(autoscalingError(101, 70)).toContain("Max replicas")
    expect(autoscalingError(2.5, 70)).toContain("Max replicas")
    expect(autoscalingError(Number(""), 70)).toContain("Max replicas")
    expect(autoscalingError(5, 9)).toContain("Target CPU")
    expect(autoscalingError(5, 96)).toContain("Target CPU")
  })
})

describe("describeLoadTest", () => {
  test("date, peak rps and duration", () => {
    const startedAt = new Date(2026, 9, 12, 14, 2).toISOString()
    expect(describeLoadTest({ startedAt, peakRps: 179.6, seconds: 45 })).toBe("12 Oct 14:02 · 180 rps peak · 45 s")
    expect(describeLoadTest({ startedAt, peakRps: 3, seconds: 0 })).toBe("12 Oct 14:02 · 3 rps peak")
  })
})

describe("suggestKubeName", () => {
  test("turns workflow names into DNS labels like the server", () => {
    expect(suggestKubeName("Node.js API + PostgreSQL + Redis")).toBe("node-js-api-postgresql-redis")
    expect(suggestKubeName("  Shop_Backend  ")).toBe("shop-backend")
    expect(suggestKubeName("3 tier app")).toBe("s-3-tier-app")
    expect(suggestKubeName("¡¡!!")).toBe("app")
  })

  test("stays a valid label at the length limit", () => {
    const s = suggestKubeName(`${"a".repeat(62)} b`)
    expect(s.length).toBeLessThanOrEqual(63)
    expect(DNS_LABEL.test(s)).toBe(true)
  })
})

describe("DNS_LABEL", () => {
  test("accepts names the converter accepts", () => {
    expect(DNS_LABEL.test("shop")).toBe(true)
    expect(DNS_LABEL.test("shop-1")).toBe(true)
    expect(DNS_LABEL.test("Shop")).toBe(false)
    expect(DNS_LABEL.test("1shop")).toBe(false)
    expect(DNS_LABEL.test("shop-")).toBe(false)
  })
})
