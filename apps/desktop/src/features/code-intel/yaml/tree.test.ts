import { describe, expect, test } from "bun:test"
import { get, nodeAt, parseYaml, scalarText, type YamlNode } from "./tree"

/** Plain data of a node, for compact assertions. */
function data(n: YamlNode | null | undefined): unknown {
  if (!n) return null
  if (n.kind === "map") return Object.fromEntries(n.entries.map((e) => [e.key, data(e.value)]))
  if (n.kind === "seq") return n.items.map(data)
  if (n.kind === "alias") return `*${n.name}`
  return n.value
}

describe("parseYaml", () => {
  test("block mappings, sequences (indented or not), flow collections, quotes and comments", () => {
    const text = `# header
x-defaults: &defaults
  restart: unless-stopped
services:
  api:
    <<: *defaults
    image: "node:22"   # pinned
    ports:
    - "3000:3000"
    - 9229
    command: [node, "server.js", {inspect: true}]
    environment:
      A: 'it''s'
      B: plain text
        continued
`
    const [doc] = parseYaml(text)
    expect(data(doc.root)).toEqual({
      "x-defaults": { restart: "unless-stopped" },
      services: {
        api: {
          "<<": "*defaults",
          image: "node:22",
          ports: ["3000:3000", "9229"],
          command: ["node", "server.js", { inspect: "true" }],
          environment: { A: "it's", B: "plain text continued" },
        },
      },
    })
    const api = get(get(doc.root, "services"), "api")
    expect(api?.kind === "map" && api.merges).toBe(true)
    const image = get(api, "image")!
    expect(text.slice(image.from, image.to)).toBe('"node:22"')
  })

  test("multi-document streams, block scalars and multi-line flow collections", () => {
    const text = `apiVersion: v1
kind: ConfigMap
data:
  nginx.conf: |
    server {
      listen 80;
    }
  list: [a,
    b]
---
apiVersion: apps/v1
kind: Deployment
spec:
  template:
    spec:
      containers:
        - name: web
          image: nginx:1.27
          args:
            - --port=80
`
    const docs = parseYaml(text)
    expect(docs.map((d) => scalarText(get(d.root, "kind")))).toEqual(["ConfigMap", "Deployment"])
    expect(data(get(docs[0].root, "data"))).toEqual({ "nginx.conf": "server {\n  listen 80;\n}", list: ["a", "b"] })
    expect(docs[1].from).toBe(text.indexOf("apiVersion: apps/v1"))
    const containers = get(get(get(get(docs[1].root, "spec"), "template"), "spec"), "containers")
    expect(data(containers)).toEqual([{ name: "web", image: "nginx:1.27", args: ["--port=80"] }])
  })

  test("nodeAt finds the key or value under a position with its path", () => {
    const text = "services:\n  api:\n    depends_on:\n      - db\n"
    const [doc] = parseYaml(text)
    const hit = nodeAt(doc.root, text.indexOf("db"))
    expect(hit?.path).toEqual(["services", "api", "depends_on", "[]"])
    expect(hit?.node.kind === "scalar" && hit.node.value).toBe("db")
    const key = nodeAt(doc.root, text.indexOf("depends_on") + 2)
    expect(key?.entry?.key).toBe("depends_on")
  })

  test("never throws on half-typed text", () => {
    for (const t of ["services:\n  api:\n    image: [", "a: {b: ", "- - -", ":", "key: |\n", "a:\n\tb: 1", "---\n---\n"]) {
      expect(() => parseYaml(t)).not.toThrow()
    }
  })
})
