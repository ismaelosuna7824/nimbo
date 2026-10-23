// Validation of a YAML tree against a JSON schema, in the client and as you
// type: unknown keys, wrong value types, values outside an enum and missing
// required keys, each with the document range to underline. Lenient where
// YAML is dynamic: aliases, merge keys (<<), templated values ({{ … }}) and
// interpolated ones (${…}) are not judged.

import { alternatives, isArray, isObject, takesFreeKeys, types, type JsonSchema } from "./schema"
import type { YamlMap, YamlNode, YamlScalar } from "./tree"

export interface YamlProblem {
  from: number
  to: number
  severity: "error" | "warning"
  message: string
  /** Key path of the offending node ("[]" for sequence items). */
  path: string[]
}

const DYNAMIC = /\$\{|\{\{/

/** The JSON types a plain YAML scalar can be read as (YAML 1.2 core schema; quoted scalars are strings). */
function scalarTypes(n: YamlScalar): Set<string> {
  const out = new Set(["string"])
  if (n.quoted || n.block) return out
  const v = n.value
  if (/^(true|false|True|False|TRUE|FALSE)$/.test(v)) out.add("boolean")
  if (/^[-+]?(0|[1-9][0-9]*)$/.test(v) || /^0x[0-9a-fA-F]+$/.test(v) || /^0o[0-7]+$/.test(v)) out.add("integer").add("number")
  if (/^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$/.test(v) || /^[-+]?\.(inf|Inf|INF)$/.test(v) || /^\.(nan|NaN|NAN)$/.test(v))
    out.add("number")
  if (/^(null|Null|NULL|~)?$/.test(v)) out.add("null")
  return out
}

function nodeTypes(n: YamlNode): Set<string> {
  if (n.kind === "map") return new Set(["object"])
  if (n.kind === "seq") return new Set(["array"])
  if (n.kind === "scalar") return scalarTypes(n)
  return new Set()
}

function accepts(s: JsonSchema, n: YamlNode): boolean {
  const t = types(s)
  if (t.length === 0) return true
  const nt = nodeTypes(n)
  return t.some((x) => nt.has(x) || (x === "number" && nt.has("integer")))
}

function describeNode(n: YamlNode): string {
  if (n.kind === "map") return "a mapping"
  if (n.kind === "seq") return "a list"
  if (n.kind === "scalar") {
    const t = scalarTypes(n)
    if (t.has("boolean")) return "a boolean"
    if (t.has("number")) return "a number"
    if (t.has("null")) return "null"
    return "a string"
  }
  return "a value"
}

function expected(alts: JsonSchema[]): string {
  const t = new Set<string>()
  for (const a of alts) for (const x of types(a)) t.add(x === "integer" ? "number" : x === "object" ? "mapping" : x === "array" ? "list" : x)
  return [...t].join(" or ")
}

function walk(root: JsonSchema, schema: JsonSchema, node: YamlNode, path: string[], out: YamlProblem[]) {
  if (node.kind === "alias") return
  if (node.kind === "scalar" && DYNAMIC.test(node.value)) return
  const alts = alternatives(root, schema)
  if (alts.length === 0) return
  const fitting = alts.filter((a) => accepts(a, node))
  if (fitting.length === 0) {
    out.push({ from: node.from, to: node.to, severity: "error", message: `Expected ${expected(alts)}, found ${describeNode(node)}`, path })
    return
  }
  if (node.kind === "scalar") {
    const enums = fitting.filter((a) => a.enum || a.const !== undefined)
    if (enums.length && enums.length === fitting.length) {
      const allowed = enums.flatMap((a) => (a.enum ?? [a.const]).map(String))
      if (!allowed.includes(node.value)) {
        out.push({ from: node.from, to: node.to, severity: "error", message: `“${node.value}” is not one of: ${allowed.join(", ")}`, path })
      }
    }
    return
  }
  if (node.kind === "seq") {
    const items = fitting.filter(isArray).map((a) => a.items).filter((x): x is JsonSchema => !!x)
    if (items.length === 0) return
    for (const it of node.items) walkAny(root, items, it, [...path, "[]"], out)
    return
  }
  // A mapping: check its keys against the alternatives that take mappings.
  const objects = fitting.filter(isObject)
  if (objects.length === 0) return
  checkMap(root, objects, node, path, out)
}

/** A value validated against several alternative schemas: the first that reports nothing wins. */
function walkAny(root: JsonSchema, schemas: JsonSchema[], node: YamlNode, path: string[], out: YamlProblem[]) {
  if (schemas.length === 1) return walk(root, schemas[0], node, path, out)
  let best: YamlProblem[] | null = null
  for (const s of schemas) {
    const p: YamlProblem[] = []
    walk(root, s, node, path, p)
    if (p.length === 0) return
    if (!best || p.length < best.length) best = p
  }
  if (best) out.push(...best)
}

function checkMap(root: JsonSchema, objects: JsonSchema[], node: YamlMap, path: string[], out: YamlProblem[]) {
  // With several mapping alternatives, judge against the one that knows the most keys of this mapping.
  const keys = node.entries.map((e) => e.key)
  const score = (s: JsonSchema) => keys.filter((k) => s.properties?.[k]).length
  const s = objects.reduce((a, b) => (score(b) > score(a) ? b : a))
  const free = objects.some(takesFreeKeys)
  for (const e of node.entries) {
    if (e.key === "<<") continue
    const prop = objects.map((o) => o.properties?.[e.key]).find(Boolean)
    if (prop) {
      if (e.value) walk(root, prop, e.value, [...path, e.key], out)
      continue
    }
    const pattern = objects
      .flatMap((o) => Object.entries(o.patternProperties ?? {}))
      .find(([p]) => new RegExp(p).test(e.key))?.[1]
    if (pattern) {
      if (e.value) walk(root, pattern, e.value, [...path, e.key], out)
      continue
    }
    const additional = objects.map((o) => o.additionalProperties).find((a): a is JsonSchema => typeof a === "object")
    if (additional) {
      if (e.value) walk(root, additional, e.value, [...path, e.key], out)
      continue
    }
    if (free || objects.some((o) => o.additionalProperties !== false && !o.properties)) continue
    // Only flag unknown keys where the schema lists the keys it takes.
    if (objects.every((o) => o.additionalProperties === false) || objects.some((o) => o.properties)) {
      const known = Object.keys(s.properties ?? {})
      const near = known.find((k) => k.toLowerCase() === e.key.toLowerCase() || editDistance(k, e.key) <= 2)
      out.push({
        from: e.keyFrom,
        to: e.keyTo,
        severity: objects.every((o) => o.additionalProperties === false) ? "error" : "warning",
        message: `Unknown key “${e.key}”${path.length ? ` in ${path.filter((p) => p !== "[]").join(".")}` : ""}${near ? ` — did you mean “${near}”?` : ""}`,
        path: [...path, e.key],
      })
    }
  }
  if (node.merges) return
  for (const r of s.required ?? []) {
    if (keys.includes(r)) continue
    const line = node.entries[0]
    out.push({
      from: line?.keyFrom ?? node.from,
      to: line?.keyTo ?? node.to,
      severity: "error",
      message: `Missing required key “${r}”${path.length ? ` in ${path.filter((p) => p !== "[]").join(".")}` : ""}`,
      path,
    })
  }
}

function editDistance(a: string, b: string): number {
  if (Math.abs(a.length - b.length) > 2) return 3
  const d = Array.from({ length: a.length + 1 }, (_, i) => [i, ...Array<number>(b.length).fill(0)])
  for (let j = 1; j <= b.length; j++) d[0][j] = j
  for (let i = 1; i <= a.length; i++)
    for (let j = 1; j <= b.length; j++) d[i][j] = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1))
  return d[a.length][b.length]
}

/** Problems of one YAML document's tree against `schema` (the root of its $refs). */
export function validateYaml(node: YamlNode | null, schema: JsonSchema): YamlProblem[] {
  if (!node) return []
  const out: YamlProblem[] = []
  walk(schema, schema, node, [], out)
  return out
}
