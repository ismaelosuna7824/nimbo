import { afterAll, beforeAll, describe, expect, test } from "bun:test"
import type { Completion, CompletionResult } from "@codemirror/autocomplete"
import type { DockerImage } from "@/lib/api/types"
import { composeCompletions, composeHover, splitImage, type ComposeEnv } from "./compose-completion"
import { keysAt, takesNames, valueShape, type JsonSchema } from "@/features/code-intel/yaml/schema"
// The Compose Specification schema the daemon serves (compose-go's schema/compose-spec.json, the version in
// backend/go.mod), so the tests see the real $refs, oneOfs and patternProperties.
import spec from "./testdata/compose-spec.json"

const schema = spec as unknown as JsonSchema

const IMAGES: DockerImage[] = [
  { id: "postgres", image: "postgres", tag: "17-alpine", label: "PostgreSQL", description: "Relational database", category: "database", icon: "postgres", ports: [5432] },
  { id: "redis", image: "redis", tag: "7-alpine", label: "Redis", description: "In-memory store", category: "cache", icon: "redis", ports: [6379] },
]

const env = (names: ComposeEnv["names"] = {}): ComposeEnv => ({ schema, images: IMAGES, names })

function complete(src: string, opts: { explicit?: boolean; names?: ComposeEnv["names"] } = {}): CompletionResult | null {
  const pos = src.indexOf("‸")
  const text = src.slice(0, pos) + src.slice(pos + 1)
  return composeCompletions(text, pos, opts.explicit ?? true, env(opts.names))
}

const labels = (r: CompletionResult | null) => (r?.options ?? []).map((o) => o.label)
const option = (r: CompletionResult | null, label: string): Completion | undefined => r?.options.find((o) => o.label === label)

const BASE = `services:
  api:
    image: node:22
    restart: always
    depends_on:
      - db
  db:
    image: postgres:17
  cache:
    image: redis:7
volumes:
  db-data:
networks:
  back:
`

describe("compose completion: keys from the schema", () => {
  test("top-level keys", () => {
    const r = complete(`‸`)
    expect(labels(r)).toEqual(expect.arrayContaining(["services", "networks", "volumes", "secrets", "configs", "name", "include"]))
  })

  test("the deprecated top-level version is not offered", () => {
    expect(labels(complete(`‸`))).not.toContain("version")
  })

  test("service keys under services.<name>", () => {
    const r = complete(`services:\n  api:\n    ‸`)
    expect(labels(r)).toEqual(expect.arrayContaining(["image", "build", "ports", "environment", "depends_on", "healthcheck", "restart", "volumes"]))
    // Keys of other mappings must not leak in.
    expect(labels(r)).not.toContain("driver_opts")
    expect(labels(r)).not.toContain("services")
  })

  test("keys of nested mappings: healthcheck, build, deploy.resources.limits", () => {
    expect(labels(complete(`services:\n  api:\n    healthcheck:\n      ‸`))).toEqual(expect.arrayContaining(["test", "interval", "retries", "start_period"]))
    expect(labels(complete(`services:\n  api:\n    build:\n      ‸`))).toEqual(expect.arrayContaining(["context", "dockerfile", "target", "args"]))
    expect(labels(complete(`services:\n  api:\n    deploy:\n      resources:\n        limits:\n          ‸`))).toEqual(expect.arrayContaining(["cpus", "memory"]))
  })

  test("keys of a long-syntax port item after '- ' (ports also take short strings)", () => {
    const r = complete(`services:\n  api:\n    ports:\n      - ta‸`)
    expect(labels(r)).toEqual(expect.arrayContaining(["target", "published", "protocol"]))
    expect(option(r, "target")?.apply).toBe("target: ")
  })

  test("a short-syntax port or volume being typed gets no item keys", () => {
    expect(labels(complete(`services:\n  api:\n    ports:\n      - 80‸`))).not.toContain("target")
    expect(labels(complete(`services:\n  api:\n    volumes:\n      - ./data‸`))).not.toContain("target")
  })

  test("long-syntax volume item keys next to the named volumes", () => {
    const r = complete(`services:\n  api:\n    volumes:\n      - t‸\nvolumes:\n  data:\n`)
    expect(labels(r)).toEqual(expect.arrayContaining(["type", "target", "source", "data:"]))
  })

  test("network and volume definitions have their own keys", () => {
    expect(labels(complete(`networks:\n  back:\n    ‸`))).toEqual(expect.arrayContaining(["driver", "internal", "external"]))
    expect(labels(complete(`volumes:\n  data:\n    ‸`))).toEqual(expect.arrayContaining(["driver", "driver_opts", "external"]))
  })

  test("free-name mappings (services:, environment:) offer no schema keys", () => {
    expect(complete(`services:\n  ‸`)).toBeNull()
    expect(complete(`services:\n  api:\n    environment:\n      ‸`)).toBeNull()
  })

  test("the insert text matches the value's shape: mapping, list or inline", () => {
    const r = complete(`services:\n  api:\n    ‸`)
    expect(option(r, "healthcheck")?.apply).toBe("healthcheck:\n      ")
    expect(option(r, "image")?.apply).toBe("image: ")
    // Keys that also take a scalar or a list stay inline (build: ./dir, depends_on: [db]).
    expect(option(r, "build")?.apply).toBe("build: ")
    expect(option(r, "cap_add")?.apply).toBe("cap_add:\n      - ")
  })

  test("the replacement starts at the typed prefix", () => {
    const src = `services:\n  api:\n    heal‸`
    const r = complete(src)
    expect(r?.from).toBe(src.indexOf("heal"))
    expect(labels(r)).toContain("healthcheck")
  })

  test("no popup while typing nothing unless asked", () => {
    expect(complete(`services:\n  api:\n    ‸`, { explicit: false })).toBeNull()
    expect(labels(complete(`services:\n  api:\n    i‸`, { explicit: false }))).toContain("image")
  })
})

describe("compose completion: keys already set are excluded", () => {
  test("keys above and below the cursor in the same mapping", () => {
    const r = complete(`services:\n  api:\n    image: node\n    ‸\n    restart: always\n`)
    expect(labels(r)).not.toContain("image")
    expect(labels(r)).not.toContain("restart")
    expect(labels(r)).toContain("ports")
  })

  test("keys of another service do not count", () => {
    const r = complete(`services:\n  db:\n    image: postgres\n    healthcheck:\n      test: x\n  api:\n    ‸`)
    expect(labels(r)).toContain("image")
    expect(labels(r)).toContain("healthcheck")
  })

  test("keys of a nested mapping do not count for its parent", () => {
    const r = complete(`services:\n  api:\n    build:\n      context: .\n      target: prod\n    ‸`)
    expect(labels(r)).not.toContain("build")
    expect(labels(r)).toContain("image")
  })

  test("in a sequence of mappings only the current item's keys count", () => {
    const r = complete(`services:\n  api:\n    ports:\n      - target: 80\n        published: 8080\n      - target: 443\n        ‸`)
    expect(labels(r)).not.toContain("target")
    expect(labels(r)).toContain("published")
  })
})

describe("compose completion: project names", () => {
  test("depends_on list items offer the other services, never the service itself", () => {
    const r = complete(`${BASE}`.replace("      - db\n", "      - ‸\n"))
    expect(labels(r)).toEqual(expect.arrayContaining(["db", "cache"]))
    expect(labels(r)).not.toContain("api")
  })

  test("depends_on long-syntax keys are service names with a nested mapping insert", () => {
    const src = `services:\n  api:\n    depends_on:\n      ‸\n  db:\n    image: x\n`
    const r = complete(src)
    expect(labels(r)).toEqual(["db"])
    expect(option(r, "db")?.apply).toBe("db:\n        ")
  })

  test("names declared in the project's other compose files are offered too, without duplicates", () => {
    const r = complete(`services:\n  api:\n    depends_on:\n      - ‸\n  db:\n    image: x\n`, { names: { services: ["db", "queue"] } })
    expect(labels(r).sort()).toEqual(["db", "queue"])
  })

  test("service networks offer the declared networks", () => {
    const r = complete(`${BASE}`.replace("    restart: always\n", "    restart: always\n    networks:\n      - ‸\n"))
    expect(labels(r)).toContain("back")
    expect(labels(r)).not.toContain("db")
  })

  test("short volume syntax offers named volumes with the colon", () => {
    const r = complete(`${BASE}`.replace("    restart: always\n", "    restart: always\n    volumes:\n      - ‸\n"))
    expect(labels(r)).toContain("db-data:")
  })

  test("extends.service offers services", () => {
    const r = complete(`services:\n  base:\n    image: x\n  api:\n    extends:\n      service: ‸\n`)
    expect(labels(r)).toContain("base")
  })
})

describe("compose completion: values", () => {
  test("image: offers catalog images as image:tag", () => {
    const r = complete(`services:\n  api:\n    image: ‸`)
    expect(labels(r)).toEqual(expect.arrayContaining(["postgres:17-alpine", "redis:7-alpine"]))
  })

  test("depends_on condition offers the schema's enum", () => {
    const r = complete(`services:\n  api:\n    depends_on:\n      db:\n        condition: ‸`)
    expect(labels(r).sort()).toEqual(["service_completed_successfully", "service_healthy", "service_started"])
  })

  test("restart and pull_policy offer their known values", () => {
    expect(labels(complete(`services:\n  api:\n    restart: ‸`))).toEqual(expect.arrayContaining(["no", "always", "on-failure", "unless-stopped"]))
    expect(labels(complete(`services:\n  api:\n    pull_policy: ‸`))).toEqual(expect.arrayContaining(["missing", "always", "never", "build"]))
  })

  test("boolean keys offer true/false", () => {
    expect(labels(complete(`services:\n  api:\n    read_only: ‸`))).toEqual(expect.arrayContaining(["true", "false"]))
  })

  test("the value range starts after 'key: ' so a partial value is replaced", () => {
    const src = `services:\n  api:\n    restart: unl‸`
    const r = complete(src)
    expect(r?.from).toBe(src.indexOf("unl"))
    expect(r?.validFor instanceof RegExp && r.validFor.test("unless-stopped")).toBe(true)
  })

  test("nothing inside comments or block scalars", () => {
    expect(complete(`services:\n  api:\n    # rest‸`)).toBeNull()
    expect(complete(`services:\n  api:\n    command: |\n      rest‸`)).toBeNull()
  })
})

/** An element of the stub document below: it records what was appended. */
interface StubEl {
  textContent?: string
  href?: string
  children: (StubEl | string)[]
}

/** Hover DOM built while the stub document is installed. */
function stubOf(dom: HTMLElement): StubEl {
  const el: unknown = dom
  return el as StubEl
}

/** The text of every node of a docNode tree. */
function textOf(el: StubEl | string): string {
  if (typeof el === "string") return el
  return (el.textContent ?? "") + el.children.map(textOf).join("")
}

const linkOf = (dom: HTMLElement) => stubOf(dom).children.find((c): c is StubEl => typeof c !== "string" && !!c.href)?.href

describe("compose hover", () => {
  let installed = false
  beforeAll(() => {
    // Hover builds its tooltip with document.createElement; bun has no DOM, so a recording stub stands in.
    const make = (): StubEl & Record<string, unknown> => {
      const el: StubEl & Record<string, unknown> = { children: [] }
      el.append = (...c: (StubEl | string)[]) => el.children.push(...c)
      el.addEventListener = () => {}
      return el
    }
    const doc: unknown = { createElement: make }
    if (typeof globalThis.document === "undefined") {
      globalThis.document = doc as Document
      installed = true
    }
  })
  afterAll(() => {
    if (installed) Reflect.deleteProperty(globalThis, "document")
  })

  test("a key's docs: dotted path title, the type and a link to the section of the Compose reference", () => {
    const text = `services:\n  api:\n    healthcheck:\n      interval: 5s\n`
    const h = composeHover(text, text.indexOf("interval") + 1, env())
    expect(h).not.toBeNull()
    expect(text.slice(h!.from, h!.to)).toBe("interval")
    const [title] = stubOf(h!.dom).children
    expect(textOf(title)).toBe("services.api.healthcheck.interval")
    expect(linkOf(h!.dom)).toBe("https://docs.docker.com/reference/compose-file/services/#healthcheck")
  })

  test("unknown keys have no hover", () => {
    const text = `services:\n  api:\n    imagex: x\n`
    expect(composeHover(text, text.indexOf("imagex") + 1, env())).toBeNull()
  })

  test("the image reference hovers its catalog entry and notes a tag other than the catalog's", () => {
    const text = `services:\n  db:\n    image: postgres:16\n`
    const h = composeHover(text, text.indexOf("postgres") + 2, env())
    expect(h && text.slice(h.from, h.to)).toBe("postgres:16")
    expect(textOf(stubOf(h!.dom))).toContain("catalog tag 17-alpine")
    expect(linkOf(h!.dom)).toBe("https://hub.docker.com/_/postgres")
  })

  test("an image outside the catalog has no hover", () => {
    const text = `services:\n  db:\n    image: myorg/thing:1\n`
    expect(composeHover(text, text.indexOf("thing"), env())).toBeNull()
  })
})

describe("compose schema navigation", () => {
  test("services takes free names; a service mapping does not", () => {
    expect(takesNames(schema, ["services"])).toBe(true)
    expect(takesNames(schema, ["services", "api"])).toBe(false)
  })

  test("a key's shape follows its value type", () => {
    const svc = new Map(keysAt(schema, ["services", "api"]).map((k) => [k.name, k]))
    expect(valueShape(schema, svc.get("healthcheck")!.schema)).toBe("object")
    expect(valueShape(schema, svc.get("image")!.schema)).toBe("scalar")
  })
})

describe("splitImage", () => {
  test("repository and tag", () => {
    expect(splitImage("nginx:1.27-alpine")).toEqual(["nginx", "1.27-alpine"])
    expect(splitImage("nginx")).toEqual(["nginx", undefined])
  })

  test("a registry port is not a tag", () => {
    expect(splitImage("localhost:5000/app")).toEqual(["localhost:5000/app", undefined])
    expect(splitImage("localhost:5000/app:v2")).toEqual(["localhost:5000/app", "v2"])
  })

  test("a digest is dropped", () => {
    expect(splitImage("postgres:17@sha256:abc")).toEqual(["postgres", "17"])
  })
})
