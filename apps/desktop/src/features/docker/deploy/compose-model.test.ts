import { describe, expect, test } from "bun:test"
import type { ComposeContainer, ComposeLogLine, ComposeServiceStatus, ComposeState, ComposeStatus } from "@/lib/api/types"
import {
  appendLogRows,
  composeChipSummary,
  composeKey,
  firstPublishedUrl,
  logRowsText,
  logSource,
  remapNote,
  runningServices,
  SERVICE_COLORS,
  serviceColor,
  servicePorts,
  serviceReplicas,
  serviceRestarts,
} from "./compose-model"

const container = (over: Partial<ComposeContainer> = {}): ComposeContainer => ({
  id: "c1",
  name: "nimbo-dk-w1-web-1",
  state: "running",
  exitCode: 0,
  restarts: 0,
  image: "nginx:1.27",
  imageId: "sha256:abc",
  ports: [],
  ...over,
})

const service = (name: string, state: ComposeState, containers: ComposeContainer[] = []): ComposeServiceStatus => ({
  name,
  nodeId: `service:${name}`,
  state,
  containers,
})

const status = (state: ComposeState, services: ComposeServiceStatus[]): ComposeStatus => ({
  workflowId: "w1",
  project: "nimbo-dk-w1",
  state,
  services,
  remaps: [],
  updatedAt: "2026-10-22T10:00:00Z",
})

const logLine = (service: string, text: string, container = `nimbo-dk-w1-${service}-1`): ComposeLogLine => ({
  service,
  container,
  line: { t: "2026-10-22T10:00:00Z", stream: "stdout", text },
})

describe("composeChipSummary", () => {
  test("no status yet reads as containers off", () => {
    expect(composeChipSummary(null)).toEqual({ state: "absent", label: "Containers off", running: 0, total: 0 })
  })

  test("running counts services with a running container", () => {
    const s = status("running", [
      service("api", "running", [container()]),
      service("db", "running", [container({ state: "running" })]),
      service("worker", "absent"),
    ])
    expect(composeChipSummary(s)).toMatchObject({ state: "running", label: "Running 2/3", running: 2, total: 3 })
  })

  test("other states", () => {
    expect(composeChipSummary(status("starting", [])).label).toBe("Starting…")
    expect(composeChipSummary(status("stopped", [])).label).toBe("Stopped")
    expect(composeChipSummary(status("error", [])).label).toBe("Error")
    expect(composeChipSummary(status("absent", [])).label).toBe("Containers off")
  })
})

describe("runningServices", () => {
  test("ignores exited containers and missing status", () => {
    expect(runningServices(null)).toBe(0)
    const s = status("error", [service("api", "error", [container({ state: "exited", exitCode: 1 })]), service("db", "running", [container()])])
    expect(runningServices(s)).toBe(1)
  })
})

describe("servicePorts", () => {
  test("one entry per host port, preferring the binding with a URL, sorted", () => {
    const s = service("web", "running", [
      container({
        ports: [
          { container: 80, protocol: "tcp", hostIp: "::", host: 8080 },
          { container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8080, url: "http://127.0.0.1:8080/" },
          { container: 443, protocol: "tcp", hostIp: "0.0.0.0", host: 4430 },
          { container: 9000, protocol: "tcp", hostIp: "", host: 0 },
        ],
      }),
    ])
    expect(servicePorts(s)).toEqual([
      { container: 443, protocol: "tcp", hostIp: "0.0.0.0", host: 4430 },
      { container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8080, url: "http://127.0.0.1:8080/" },
    ])
  })

  test("firstPublishedUrl skips ports without a URL", () => {
    const s = status("running", [
      service("db", "running", [container({ ports: [{ container: 5432, protocol: "tcp", hostIp: "0.0.0.0", host: 5432 }] })]),
      service("web", "running", [container({ ports: [{ container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8080, url: "http://127.0.0.1:8080/" }] })]),
    ])
    expect(firstPublishedUrl(s)).toBe("http://127.0.0.1:8080/")
    expect(firstPublishedUrl(null)).toBeUndefined()
  })
})

describe("service counters", () => {
  test("replicas and restarts", () => {
    const s = service("web", "running", [container({ restarts: 2 }), container({ id: "c2", state: "restarting", restarts: 3 })])
    expect(serviceReplicas(s)).toEqual({ running: 1, total: 2 })
    expect(serviceRestarts(s)).toBe(5)
  })
})

describe("composeKey", () => {
  test("changes with service state and ports, not with updatedAt", () => {
    const a = status("running", [service("web", "running", [container({ ports: [{ container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8080 }] })])])
    const later = { ...a, updatedAt: "2026-10-22T11:00:00Z" }
    expect(composeKey(later)).toBe(composeKey(a))
    const moved = status("running", [service("web", "running", [container({ ports: [{ container: 80, protocol: "tcp", hostIp: "0.0.0.0", host: 8081 }] })])])
    expect(composeKey(moved)).not.toBe(composeKey(a))
    expect(composeKey(status("stopped", [service("web", "stopped")]))).not.toBe(composeKey(a))
    expect(composeKey(null)).toBe("")
  })
})

describe("remapNote", () => {
  test("names the original and the published port", () => {
    expect(remapNote({ service: "db", containerPort: 5432, protocol: "tcp", hostPort: 5432, port: 55432 })).toBe(
      "db: 5432 was busy → published on 55432",
    )
    expect(remapNote({ service: "dns", containerPort: 53, protocol: "udp", hostPort: 53, port: 1053 })).toBe(
      "dns: 53/udp was busy → published on 1053/udp",
    )
  })
})

describe("serviceColor", () => {
  test("by position among the services, wrapping around the palette", () => {
    const names = ["api", "db", "web"]
    expect(serviceColor("api", names)).toBe(SERVICE_COLORS[0])
    expect(serviceColor("web", names)).toBe(SERVICE_COLORS[2])
    const many = Array.from({ length: SERVICE_COLORS.length + 1 }, (_, i) => `s${i}`)
    expect(serviceColor(`s${SERVICE_COLORS.length}`, many)).toBe(SERVICE_COLORS[0])
  })

  test("unknown services get a stable colour from their name", () => {
    const c = serviceColor("ghost", [])
    expect(SERVICE_COLORS).toContain(c as (typeof SERVICE_COLORS)[number])
    expect(serviceColor("ghost", ["api"])).toBe(c)
  })
})

describe("log rows", () => {
  test("logSource shows the replica number past the first container", () => {
    expect(logSource(logLine("web", "x"))).toBe("web")
    expect(logSource(logLine("web", "x", "nimbo-dk-w1-web-2"))).toBe("web-2")
    expect(logSource(logLine("web", "x", "custom-name"))).toBe("web")
  })

  test("appendLogRows strips ANSI, numbers rows and keeps the newest max", () => {
    let rows = appendLogRows([], [logLine("web", "\x1b[32mready\x1b[0m"), logLine("db", "listening")])
    expect(rows.map((r) => [r.id, r.source, r.text])).toEqual([
      [0, "web", "ready"],
      [1, "db", "listening"],
    ])
    rows = appendLogRows(rows, [logLine("web", "a"), logLine("web", "b")], 3)
    expect(rows.map((r) => [r.id, r.text])).toEqual([
      [1, "listening"],
      [2, "a"],
      [3, "b"],
    ])
    expect(appendLogRows(rows, [])).toBe(rows)
  })

  test("logRowsText aligns the prefixes", () => {
    const rows = appendLogRows([], [logLine("web", "GET /"), logLine("worker", "tick")])
    expect(logRowsText(rows)).toBe("web    | GET /\nworker | tick")
  })
})
