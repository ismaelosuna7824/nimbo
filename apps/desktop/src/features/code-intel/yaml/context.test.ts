import { describe, expect, test } from "bun:test"
import { documentAt, keyAt, topLevelNames, yamlContextAt, type YamlContext } from "./context"

/** The context at the "‸" marker of `src`. */
function at(src: string): YamlContext & { text: string; pos: number } {
  const pos = src.indexOf("‸")
  if (pos < 0) throw new Error("no cursor marker")
  const text = src.slice(0, pos) + src.slice(pos + 1)
  return { ...yamlContextAt(text, pos), text, pos }
}

const COMPOSE = `name: shop
services:
  api:
    image: node:22
    environment:
      DATABASE_URL: postgres://db:5432/app
    depends_on:
      db:
        condition: service_healthy
  db:
    image: postgres:17
`

describe("yamlContextAt: mapping keys", () => {
  test("a blank line at the root waits for a top-level key", () => {
    const c = at(`services:\n  api:\n    image: x\n‸`)
    expect(c).toMatchObject({ kind: "key", path: [], prefix: "", indent: 0 })
  })

  test("a key being typed under a service has the service's path and the typed prefix", () => {
    const c = at(`services:\n  api:\n    image: x\n    heal‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"], prefix: "heal", indent: 4 })
    if (c.kind === "key") expect(c.text.slice(c.from, c.pos)).toBe("heal")
  })

  test("dedenting leaves the nested mappings: a key at service level after a deep block", () => {
    const c = at(`${COMPOSE.split("\n  db:")[0]}\n    ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"], indent: 4 })
  })

  test("a new service name at two spaces belongs to services, not to the previous service", () => {
    const c = at(`services:\n  api:\n    image: x\n    ports:\n      - "80:80"\n  ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services"], indent: 2 })
  })

  test("a value after 'key: ' carries the key and the value prefix", () => {
    const c = at(`services:\n  api:\n    restart: unl‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "restart"], key: "restart", prefix: "unl" })
    if (c.kind === "value") expect(c.text.slice(c.from, c.pos)).toBe("unl")
  })

  test("a quoted value's prefix starts after the quote", () => {
    const c = at(`services:\n  api:\n    image: "ngi‸`)
    expect(c).toMatchObject({ kind: "value", key: "image", prefix: "ngi" })
    if (c.kind === "value") expect(c.text[c.from - 1]).toBe('"')
  })

  test("quoted keys are unquoted in the path", () => {
    const c = at(`services:\n  "my-api":\n    ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "my-api"] })
  })

  test("a value of a URL with colons is still a value of its key (not a nested key)", () => {
    const c = at(`services:\n  api:\n    environment:\n      DATABASE_URL: postgres://db:5432/‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "environment", "DATABASE_URL"], prefix: "postgres://db:5432/" })
  })

  test("flow collections are not entered", () => {
    expect(at(`services:\n  api:\n    ports: [‸`).kind).toBe("none")
    expect(at(`services:\n  api:\n    labels: {‸`).kind).toBe("none")
  })
})

describe("yamlContextAt: sequences", () => {
  test("a scalar sequence item is a value at <key>/[]", () => {
    const c = at(`services:\n  api:\n    ports:\n      - 80‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "ports", "[]"], key: "", prefix: "80" })
  })

  test("a key inside a mapping item of a sequence has the item in its path", () => {
    const c = at(`services:\n  api:\n    ports:\n      - target: 80\n        pub‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api", "ports", "[]"], prefix: "pub", indent: 8 })
  })

  test("'- key: value' on the dash line is a value of that key inside the item", () => {
    const c = at(`services:\n  api:\n    volumes:\n      - type: vol‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "volumes", "[]", "type"], key: "type", prefix: "vol" })
  })

  test("a second item starts a new item, not a nested one", () => {
    const c = at(`services:\n  api:\n    depends_on:\n      - db\n      - ‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "depends_on", "[]"], prefix: "" })
  })

  test("sequences indented at their key's column (compact style) still belong to the key", () => {
    const c = at(`services:\n  api:\n    depends_on:\n    - db\n    - ‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "depends_on", "[]"] })
  })

  test("after a compact sequence, a key at the same column is a sibling of the sequence's key", () => {
    const c = at(`services:\n  api:\n    depends_on:\n    - db\n    rest‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"], prefix: "rest" })
  })
})

describe("yamlContextAt: nested services.<name>.depends_on", () => {
  test("a dependency name in long syntax is a key of depends_on", () => {
    const c = at(`services:\n  api:\n    depends_on:\n      ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api", "depends_on"], indent: 6 })
  })

  test("the condition under a dependency has the dependency in its path", () => {
    const c = at(`services:\n  api:\n    depends_on:\n      db:\n        condition: service_‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "api", "depends_on", "db", "condition"], prefix: "service_" })
  })

  test("the second service's depends_on is its own, not the first one's", () => {
    const c = at(`${COMPOSE}    depends_on:\n      - ‸`)
    expect(c).toMatchObject({ kind: "value", path: ["services", "db", "depends_on", "[]"] })
  })
})

describe("yamlContextAt: block scalars", () => {
  test("lines inside a | block are not YAML", () => {
    expect(at(`services:\n  api:\n    command: |\n      echo a: b\n      run‸`).kind).toBe("none")
    expect(at(`configs:\n  app:\n    content: >-\n      key: ‸`).kind).toBe("none")
  })

  test("a 'key:' line inside a block scalar does not open a mapping", () => {
    const c = at(`services:\n  api:\n    command: |\n      fake:\n        nested: 1\n    rest‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"], prefix: "rest" })
  })

  test("the block ends at a dedented line", () => {
    const c = at(`x-script: |\n  line one\n  line: two\nservices:\n  ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services"] })
  })

  test("the cursor right after the block indicator is not a completion spot", () => {
    expect(at(`services:\n  api:\n    command: |‸`).kind).toBe("none")
  })
})

describe("yamlContextAt: comments", () => {
  test("the cursor in a comment has no context", () => {
    expect(at(`services:\n  api:\n    # restart: al‸`).kind).toBe("none")
    expect(at(`services:\n  api:\n    image: x # pick a tag‸`).kind).toBe("none")
  })

  test("commented-out keys above do not change the path", () => {
    const c = at(`services:\n  api:\n    # build:\n    #   context: .\n    ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"] })
  })

  test("a # inside a quoted value is not a comment", () => {
    const c = at(`services:\n  api:\n    environment:\n      COLOR: "#ff0000"\n      ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api", "environment"] })
  })

  test("a # inside an unquoted URL fragment (no space before) is not a comment", () => {
    const c = at(`services:\n  api:\n    environment:\n      URL: http://x/#a‸`)
    expect(c).toMatchObject({ kind: "value", key: "URL", prefix: "http://x/#a" })
  })
})

describe("yamlContextAt: anchors, aliases and documents", () => {
  test("an extension field with an anchor does not leak into services", () => {
    const c = at(`x-common: &common\n  restart: always\n  logging:\n    driver: json-file\nservices:\n  api:\n    <<: *common\n    ‸`)
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"], indent: 4 })
  })

  test("a service declared with an anchor keeps its name in the path", () => {
    const c = at(`services:\n  api: &api\n    image: x\n    ‸`)
    // "api: &api" has an inline value (the anchor), which must not close the mapping below it.
    expect(c).toMatchObject({ kind: "key", path: ["services", "api"] })
  })

  test("a document separator resets the path", () => {
    const c = at(`services:\n  api:\n    image: x\n---\n‸`)
    expect(c).toMatchObject({ kind: "key", path: [] })
  })
})

describe("topLevelNames", () => {
  test("lists the names of each section with their 1-based lines", () => {
    const names = topLevelNames(`${COMPOSE}volumes:\n  db-data:\nnetworks:\n  back:\n    driver: bridge\n`)
    expect(names.services).toEqual([
      { name: "api", line: 3 },
      { name: "db", line: 10 },
    ])
    expect(names.volumes).toEqual([{ name: "db-data", line: 13 }])
    expect(names.networks).toEqual([{ name: "back", line: 15 }])
  })

  test("nested keys, comments and block scalar content are not names", () => {
    const names = topLevelNames(`services:\n  # old:\n  api:\n    command: |\n      fake: 1\n  "worker":\n    image: x\n`)
    expect(names.services.map((n) => n.name)).toEqual(["api", "worker"])
  })
})

describe("keyAt", () => {
  test("the key under the cursor with its path and range", () => {
    const text = `services:\n  api:\n    healthcheck:\n      interval: 5s\n`
    const pos = text.indexOf("interval") + 3
    const k = keyAt(text, pos)
    expect(k).toMatchObject({ path: ["services", "api", "healthcheck"], key: "interval" })
    expect(text.slice(k!.from, k!.to)).toBe("interval")
  })

  test("a key on a dash line is found after the dash", () => {
    const text = `services:\n  api:\n    ports:\n      - target: 80\n`
    const k = keyAt(text, text.indexOf("target") + 1)
    expect(k).toMatchObject({ path: ["services", "api", "ports", "[]"], key: "target" })
  })

  test("the cursor on the value is not on the key", () => {
    const text = `services:\n  api:\n    restart: always\n`
    expect(keyAt(text, text.indexOf("always") + 2)).toBeNull()
  })
})

describe("documentAt", () => {
  test("finds the document of a multi-document stream and its index", () => {
    const text = "---\na: 1\n---\nb: 2\n---\nc: 3\n"
    expect(documentAt(text, text.indexOf("a"))).toEqual({ from: 4, to: 9, index: 0 })
    expect(documentAt(text, text.indexOf("b"))).toMatchObject({ index: 1, from: text.indexOf("b") })
    expect(documentAt(text, text.indexOf("c"))).toMatchObject({ index: 2, to: text.length })
  })

  test("a single document is the whole text", () => {
    expect(documentAt("a: 1\n", 2)).toEqual({ from: 0, to: 5, index: 0 })
  })
})
