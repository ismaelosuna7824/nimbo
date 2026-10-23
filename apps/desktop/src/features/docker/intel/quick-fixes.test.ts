import { describe, expect, test } from "bun:test"
import type { CodeProblem, DockerImage } from "@/lib/api/types"
import type { QuickFix } from "@/features/code-intel/lint"
import { dockerQuickFixes } from "./quick-fixes"

const IMAGES = [
  { id: "postgres", image: "postgres", tag: "16-alpine", label: "PostgreSQL", category: "database", ports: [5432] },
  { id: "node", image: "node", tag: "22-alpine", label: "Node.js", category: "runtime", ports: [3000] },
] as unknown as DockerImage[]

const problem = (code: string, line: number, message = ""): CodeProblem => ({ severity: "warning", code, message, line, column: 1, endLine: line, endColumn: 1000 })

function fixes(kind: "compose" | "dockerfile", text: string, pr: CodeProblem, files: string[] = [], path = kind === "compose" ? "compose.yaml" : "api/Dockerfile") {
  return dockerQuickFixes(kind, { images: IMAGES, files: () => files })(pr, { text, path, files: () => ({}) })
}

/** The text after applying a fix's edits (from the end, so offsets stay valid). */
function apply(text: string, fix: QuickFix): string {
  let out = text
  for (const c of [...(fix.changes ?? [])].sort((a, b) => b.from - a.from)) out = out.slice(0, c.from) + c.insert + out.slice(c.to)
  return out
}

const lineOf = (text: string, needle: string) => text.slice(0, text.indexOf(needle)).split("\n").length

const COMPOSE = `services:
  api:
    build: ./api
    logging:
      driver: json-file
    depends_on:
      - db
      - cache
  db:
    image: postgres
    ports:
      - "5432:5432"
      - target: 5433
        published: "5433"
  cache:
    image: redis:7
`

describe("compose quick fixes", () => {
  test("dk_latest_tag pins the catalog tag or its major version", () => {
    const f = fixes("compose", COMPOSE, problem("dk_latest_tag", lineOf(COMPOSE, "image: postgres")))
    // The catalog's 16-alpine is already a major version: one choice.
    expect(f.map((x) => x.name)).toEqual(["Pin postgres:16-alpine"])
    expect(apply(COMPOSE, f[0])).toContain("    image: postgres:16-alpine\n")
    // A more specific catalog tag also offers its major version (with the same flavor).
    const text = "services:\n  x:\n    image: example/app:latest\n"
    const images = [{ image: "example/app", tag: "3.12.1-slim" }] as unknown as DockerImage[]
    const g = dockerQuickFixes("compose", { images, files: () => [] })(problem("dk_latest_tag", 3), { text, path: "compose.yaml", files: () => ({}) })
    expect(g.map((x) => x.name)).toEqual(["Pin example/app:3.12.1-slim", "Pin example/app:3-slim"])
    expect(apply(text, g[1])).toBe("services:\n  x:\n    image: example/app:3-slim\n")
  })

  test("dk_no_init and dk_no_restart add the key as the service's first key", () => {
    const init = fixes("compose", COMPOSE, problem("dk_no_init", 2))
    expect(apply(COMPOSE, init[0])).toContain("  api:\n    init: true\n    build: ./api\n")
    const restart = fixes("compose", COMPOSE, problem("dk_no_restart", lineOf(COMPOSE, "  db:")))
    expect(restart.map((x) => x.name)).toEqual(["Add restart: unless-stopped", "Add restart: on-failure"])
    expect(apply(COMPOSE, restart[0])).toContain("  db:\n    restart: unless-stopped\n    image: postgres\n")
  })

  test("dk_log_rotation adds options with max-size and max-file under logging", () => {
    const f = fixes("compose", COMPOSE, problem("dk_log_rotation", lineOf(COMPOSE, "logging:")))
    expect(apply(COMPOSE, f[0])).toContain('    logging:\n      driver: json-file\n      options:\n        max-size: "10m"\n        max-file: "3"\n    depends_on:')
  })

  test("dk_db_published binds every published port of the service to 127.0.0.1", () => {
    const f = fixes("compose", COMPOSE, problem("dk_db_published", lineOf(COMPOSE, "    ports:")))
    const out = apply(COMPOSE, f[0])
    expect(out).toContain('      - "127.0.0.1:5432:5432"\n')
    expect(out).toContain('        published: "5433"\n        host_ip: 127.0.0.1\n')
  })

  test("dk_depends_healthy turns the list into conditions, waiting for the database to be healthy", () => {
    const pr = problem("dk_depends_healthy", lineOf(COMPOSE, "      - db"), "api starts as soon as db's container exists, before the PostgreSQL accepts connections")
    const f = fixes("compose", COMPOSE, pr)
    expect(apply(COMPOSE, f[0])).toContain(
      "    depends_on:\n      db:\n        condition: service_healthy\n      cache:\n        condition: service_started\n  db:",
    )
  })

  test("dk_dockerignore creates the context's .dockerignore with the stack's defaults and the heavy folders", () => {
    const pr = problem("dk_dockerignore", 3, "build context api sends api/node_modules (40.0 MB) to the builder on every build: it has no .dockerignore")
    const f = fixes("compose", COMPOSE, pr, ["compose.yaml", "api/Dockerfile", "api/package.json"])
    expect(f[0].name).toBe("Create api/.dockerignore with the stack's defaults")
    const content = f[0].files!["api/.dockerignore"]
    expect(content.split("\n")).toEqual(expect.arrayContaining([".git", ".env*", "node_modules", "api/node_modules", "**/*.log"]))
    // An existing .dockerignore is not overwritten.
    expect(fixes("compose", COMPOSE, pr, ["api/.dockerignore"])).toEqual([])
  })
})

describe("Dockerfile quick fixes", () => {
  test("dk_shell_form converts a simple command to the exec form; shell features are left alone", () => {
    const text = "FROM node:22-alpine\nCMD node server.js --port 3000\n"
    const f = fixes("dockerfile", text, problem("dk_shell_form", 2))
    expect(apply(text, f[0])).toBe('FROM node:22-alpine\nCMD ["node", "server.js", "--port", "3000"]\n')
    expect(fixes("dockerfile", "FROM x\nCMD npm run migrate && npm start\n", problem("dk_shell_form", 2))).toEqual([])
  })

  test("dk_apt_install adds --no-install-recommends and the cleanup in the same RUN", () => {
    const text = "FROM debian:12\nRUN apt-get update && \\\n    apt-get install -y curl\nUSER app\n"
    const f = fixes("dockerfile", text, problem("dk_apt_install", 2))
    expect(apply(text, f[0])).toBe(
      "FROM debian:12\nRUN apt-get update && \\\n    apt-get install --no-install-recommends -y curl \\\n    && rm -rf /var/lib/apt/lists/*\nUSER app\n",
    )
  })

  test("dk_latest_tag on FROM, dk_apk_cache, dk_pip_cache and dk_add", () => {
    const text = "FROM node AS build\nRUN apk add git\nRUN pip install -r requirements.txt\nADD app.js /app/\n"
    expect(apply(text, fixes("dockerfile", text, problem("dk_latest_tag", 1))[0])).toStartWith("FROM node:22-alpine AS build\n")
    expect(apply(text, fixes("dockerfile", text, problem("dk_apk_cache", 2))[0])).toContain("RUN apk add --no-cache git\n")
    expect(apply(text, fixes("dockerfile", text, problem("dk_pip_cache", 3))[0])).toContain("RUN pip install --no-cache-dir -r requirements.txt\n")
    expect(apply(text, fixes("dockerfile", text, problem("dk_add", 4))[0])).toContain("COPY app.js /app/\n")
    expect(fixes("dockerfile", "FROM x\nADD https://example.com/a.tgz /tmp/\n", problem("dk_add", 2))).toEqual([])
  })
})
