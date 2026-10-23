import { describe, expect, test } from "bun:test"
import type { ComposeGraph } from "@/lib/api/types"
import { composeDefinitionAt, type ComposeDefinitionEnv } from "./compose-definition"

const COMPOSE = `services:
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.prod
    env_file: [.env.api]
    depends_on:
      db:
        condition: service_healthy
      cache:
        condition: service_started
    links:
      - "db:database"
    networks:
      - back
    volumes:
      - data:/srv/data
      - ./src:/app/src
    secrets:
      - source: token
    environment:
      DATABASE_URL: postgres://\${POSTGRES_USER}@db/app
  worker:
    build: ./worker
    depends_on:
      - api
    extends:
      service: base
      file: common.yaml
  db:
    image: postgres:16
networks:
  back:
volumes:
  data:
secrets:
  token:
    file: ./secrets/token.txt
`

const FILES = ["compose.yaml", "api/Dockerfile.prod", "worker/Dockerfile", ".env", ".env.api", "common.yaml", "secrets/token.txt"]

const env = (over: Partial<ComposeDefinitionEnv> = {}): ComposeDefinitionEnv => ({
  path: "compose.yaml",
  exists: (p) => FILES.includes(p),
  read: (p) => ({ ".env": "# creds\nPOSTGRES_USER=app\n", "common.yaml": "services:\n  base:\n    image: alpine:3.20\n" })[p],
  ...over,
})

/** The definition of the `nth` occurrence of `word` in the compose file (cursor one character in). */
function at(word: string, nth = 0, e = env()) {
  let i = -1
  for (let n = 0; n <= nth; n++) i = COMPOSE.indexOf(word, i + 1)
  return composeDefinitionAt(COMPOSE, i + 1, e)
}

const lineOf = (needle: string) => COMPOSE.slice(0, COMPOSE.indexOf(needle)).split("\n").length

describe("composeDefinitionAt", () => {
  test("depends_on (mapping keys and list items), links and extends.service go to the service", () => {
    expect(at("db:\n        condition")).toEqual({ file: "compose.yaml", from: COMPOSE.indexOf("  db:\n    image") + 2, line: lineOf("  db:\n    image") })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("- api") + 3, env())).toMatchObject({ file: "compose.yaml", line: 2 })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf('"db:database"') + 2, env())).toMatchObject({ line: lineOf("  db:\n    image") })
    // extends with file: → the service in that file.
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("service: base") + 10, env())).toEqual({ file: "common.yaml", from: 12, line: 2 })
  })

  test("networks, named volumes and secrets go to their top-level declaration; bind mounts don't", () => {
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("- back") + 3, env())).toMatchObject({ line: lineOf("  back:") })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("data:/srv") + 1, env())).toMatchObject({ line: lineOf("  data:\nsecrets") })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("./src:/app") + 2, env())).toBeNull()
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("source: token") + 9, env())).toMatchObject({ line: lineOf("  token:") })
  })

  test("paths open their file: build context/dockerfile, env_file, secret files", () => {
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("./api") + 2, env())).toEqual({ file: "api/Dockerfile.prod", line: 1 })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("./worker") + 2, env())).toEqual({ file: "worker/Dockerfile", line: 1 })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf(".env.api") + 2, env())).toEqual({ file: ".env.api", line: 1 })
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("./secrets/token.txt") + 3, env())).toEqual({ file: "secrets/token.txt", line: 1 })
  })

  test("${VAR} goes to its line in .env", () => {
    expect(composeDefinitionAt(COMPOSE, COMPOSE.indexOf("POSTGRES_USER") + 2, env())).toEqual({ file: ".env", line: 2 })
  })

  test("names declared in another compose file come from the graph", () => {
    const graph: ComposeGraph = { nodes: [{ id: "service:proxy", kind: "service", name: "proxy", file: "compose.override.yaml", line: 3 }], edges: [] } as unknown as ComposeGraph
    const text = "services:\n  web:\n    depends_on: [proxy]\n"
    expect(composeDefinitionAt(text, text.indexOf("proxy") + 1, env({ graph }))).toEqual({ file: "compose.override.yaml", line: 3 })
  })
})
