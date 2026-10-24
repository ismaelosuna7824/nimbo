import { describe, expect, test } from "bun:test"
import { LOG_FILES_RE, LOG_SIZE_RE, labelsPatch, loggingPatch, resourcesPatch, rotatesFiles, scrapeLabels, withScope } from "./service-patch"

describe("resourcesPatch", () => {
  test("one field changes, the others stay", () => {
    expect(resourcesPatch({ cpus: "0.5", memory: "128M" }, { pids: 100 })).toEqual({ resources: { cpus: "0.5", memory: "128M", pids: 100 } })
    expect(resourcesPatch({ cpus: "0.5", pids: 100 }, { cpus: "" })).toEqual({ resources: { pids: 100 } })
  })
  test("no limit left removes the limits", () => {
    expect(resourcesPatch({ pids: 100 }, { pids: undefined })).toEqual({ resources: null })
    expect(resourcesPatch(undefined, { memory: "" })).toEqual({ resources: null })
  })
})

describe("loggingPatch", () => {
  test("adds rotation to a bare json-file driver", () => {
    expect(loggingPatch({ driver: "json-file" }, { maxSize: "10m" })).toEqual({ logging: { driver: "json-file", maxSize: "10m" } })
  })
  test("drivers without files drop the rotation options", () => {
    expect(loggingPatch({ driver: "json-file", maxSize: "10m", maxFile: "3" }, { driver: "none" })).toEqual({ logging: { driver: "none" } })
    expect(rotatesFiles("syslog")).toBe(false)
    expect(rotatesFiles(undefined)).toBe(true)
  })
  test("nothing left removes logging", () => {
    expect(loggingPatch({ maxSize: "10m" }, { maxSize: "" })).toEqual({ logging: null })
  })
  test("sizes and counts", () => {
    expect(["10m", "512k", "1g", "100"].every((s) => LOG_SIZE_RE.test(s))).toBe(true)
    expect(["10 MB", "m", "-1m"].some((s) => LOG_SIZE_RE.test(s))).toBe(false)
    expect(LOG_FILES_RE.test("3")).toBe(true)
    expect(LOG_FILES_RE.test("0")).toBe(false)
  })
})

test("withScope", () => {
  expect(withScope({ restart: "always" }, true)).toEqual({ restart: "always", scope: "environment" })
  expect(withScope({ restart: "always" }, false)).toEqual({ restart: "always" })
})

describe("labels", () => {
  test("rows without a key are drafts; none left removes labels", () => {
    expect(labelsPatch([{ key: "team", value: "shop" }, { key: "", value: "x" }])).toEqual({ labels: { team: "shop" } })
    expect(labelsPatch([])).toEqual({ labels: null })
  })
  test("scrape labels use the first container port and keep the others", () => {
    expect(scrapeLabels({ labels: { team: "shop" }, ports: [{ container: "3000", host: "3000" }], expose: ["3000"] })).toEqual({
      team: "shop",
      "prometheus.io/scrape": "true",
      "prometheus.io/port": "3000",
      "prometheus.io/path": "/metrics",
    })
    expect(scrapeLabels({ ports: [], expose: ["8080"] })["prometheus.io/port"]).toBe("8080")
  })
})
