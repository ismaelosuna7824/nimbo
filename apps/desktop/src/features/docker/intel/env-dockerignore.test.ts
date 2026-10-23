import { describe, expect, test } from "bun:test"
import type { DockerImage } from "@/lib/api/types"
import { insertService } from "@/features/docker/compose-snippet"
import { dockerignoreCompletions, dockerignoreDefaults, stacksIn } from "./dockerignore"
import { dockerfileDefinitionAt } from "./dockerfile"
import { composeVariables, envFileCompletions } from "./env-file"
import { dockerFileKind } from "."

const COMPOSE = `services:
  api:
    image: node:22
    environment:
      DB: postgres://\${POSTGRES_USER}:\${POSTGRES_PASSWORD:?set it}@db/\${POSTGRES_DB:-app}
      PRICE: $$5
  db:
    image: postgres:16
`

const IMAGES = [
  { image: "postgres", tag: "16", label: "PostgreSQL", env: [{ key: "POSTGRES_PASSWORD", value: "", secret: true }, { key: "PGDATA", value: "/var/lib/postgresql/data" }] },
] as unknown as DockerImage[]

const labels = (r: { options: readonly { label: string }[] } | null) => r?.options.map((o) => o.label) ?? []

describe(".env completion", () => {
  test("reads ${VAR}, ${VAR:?msg} and ${VAR:-default}; $$ is an escape", () => {
    expect(composeVariables(COMPOSE, "compose.yaml").map((v) => [v.name, v.required, v.fallback])).toEqual([
      ["POSTGRES_USER", false, undefined],
      ["POSTGRES_PASSWORD", true, undefined],
      ["POSTGRES_DB", false, "app"],
    ])
  })

  test("offers the variables the compose files read that the file doesn't set, then the images' variables", () => {
    const text = "POSTGRES_USER=app\nPOS"
    const r = envFileCompletions(text, text.length, false, { compose: { "compose.yaml": COMPOSE }, images: IMAGES })
    expect(labels(r)).toEqual(["POSTGRES_PASSWORD", "POSTGRES_DB", "PGDATA"])
    expect(r!.from).toBe(text.length - 3)
    const db = r!.options.find((o) => o.label === "POSTGRES_DB")!
    expect(db.apply).toBe("POSTGRES_DB=app")
  })

  test("nothing in a value or a comment", () => {
    const env = { compose: { "compose.yaml": COMPOSE }, images: IMAGES }
    expect(envFileCompletions("A=POS", 5, true, env)).toBeNull()
    expect(envFileCompletions("# POS", 5, true, env)).toBeNull()
  })
})

describe(".dockerignore", () => {
  const files = ["api/package.json", "api/server.js", "worker/requirements.txt", "worker/app.py"]

  test("stacks come from the manifests next to the file", () => {
    expect(stacksIn("api", files)).toEqual(["node"])
    expect(stacksIn("worker", files)).toEqual(["python"])
    expect(stacksIn("", files)).toEqual([])
  })

  test("completion lists the stack's patterns first and skips the ones present", () => {
    const text = "node_modules\nd"
    const r = dockerignoreCompletions(text, text.length, false, ["node"])
    expect(labels(r)).not.toContain("node_modules")
    expect(labels(r).slice(0, 2)).toEqual(["npm-debug.log*", "yarn-error.log"])
    expect(labels(r)).toContain(".git")
  })

  test("defaults for a new file", () => {
    expect(dockerignoreDefaults(["python"], [".venv"])).toBe(".git\n.env*\nDockerfile*\n.dockerignore\n__pycache__\n**/*.pyc\n.venv\nvenv\n.pytest_cache\n.mypy_cache\n*.egg-info\n**/*.log\n")
  })

  test("file kinds of a Docker workspace", () => {
    expect(["compose.yaml", "api/Dockerfile", ".env", "api/.dockerignore", "app.env", "api/Dockerfile.dockerignore", "README.md"].map((p) => dockerFileKind(p))).toEqual([
      "compose",
      "dockerfile",
      "env",
      "dockerignore",
      "env",
      "dockerignore",
      null,
    ])
  })
})

describe("Dockerfile go to definition", () => {
  const text = `ARG NODE=22
FROM node:\${NODE}-alpine AS deps
ENV APP_HOME=/app PORT=3000
WORKDIR $APP_HOME
FROM deps AS build
RUN --mount=type=cache,from=deps,target=/x true
FROM gcr.io/distroless/nodejs22
COPY --from=build /app /app
EXPOSE \${PORT}
`
  const at = (needle: string, nth = 0) => {
    let i = -1
    for (let n = 0; n <= nth; n++) i = text.indexOf(needle, i + 1)
    return dockerfileDefinitionAt(text, i + 1)
  }

  test("stage names in FROM, COPY --from and --mount from= go to FROM … AS", () => {
    const deps = text.indexOf("deps\n")
    expect(at("deps", 1)).toBe(deps)
    expect(at("from=deps") === null).toBe(true)
    expect(dockerfileDefinitionAt(text, text.indexOf("from=deps") + 6)).toBe(deps)
    expect(dockerfileDefinitionAt(text, text.indexOf("--from=build") + 8)).toBe(text.indexOf("build\n"))
  })

  test("$VAR and ${VAR} go to the ARG or ENV declaring them", () => {
    expect(dockerfileDefinitionAt(text, text.indexOf("${NODE}") + 3)).toBe(text.indexOf("NODE=22"))
    expect(dockerfileDefinitionAt(text, text.indexOf("$APP_HOME") + 2)).toBe(text.indexOf("APP_HOME="))
    expect(dockerfileDefinitionAt(text, text.indexOf("${PORT}") + 3)).toBe(text.indexOf("PORT=3000"))
  })
})

describe("insertService", () => {
  const snippet = "  # A service.\n  app:\n    build:\n      context: .\n    restart: unless-stopped\n"

  test("adds the service at the end of services:, renamed when taken, re-indented to the file's style", () => {
    const text = "services:\n    app:\n        image: nginx:1.27\nvolumes:\n    data:\n"
    expect(insertService(text, snippet)).toBe(
      "services:\n    app:\n        image: nginx:1.27\n\n    # A service.\n    app2:\n      build:\n        context: .\n      restart: unless-stopped\nvolumes:\n    data:\n",
    )
  })

  test("creates services: when the file has none", () => {
    expect(insertService("", snippet)).toBe("services:\n  # A service.\n  app:\n    build:\n      context: .\n    restart: unless-stopped\n")
  })
})
