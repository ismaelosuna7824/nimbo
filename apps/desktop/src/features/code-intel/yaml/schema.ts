// Navigation of a JSON schema along YAML key paths: which keys a mapping
// takes, which values a key accepts and their descriptions. Shared by every
// YAML dialect the editor knows (the Compose Specification, Kubernetes
// OpenAPI, Kustomize, Helm values schemas).

import type { PathSegment } from "./context"

export interface JsonSchema {
  $ref?: string
  type?: string | string[]
  title?: string
  description?: string
  deprecated?: boolean
  enum?: unknown[]
  const?: unknown
  default?: unknown
  format?: string
  pattern?: string
  minimum?: number
  maximum?: number
  properties?: Record<string, JsonSchema>
  patternProperties?: Record<string, JsonSchema>
  additionalProperties?: boolean | JsonSchema
  items?: JsonSchema
  oneOf?: JsonSchema[]
  anyOf?: JsonSchema[]
  allOf?: JsonSchema[]
  required?: string[]
  $defs?: Record<string, JsonSchema>
  definitions?: Record<string, JsonSchema>
  /** Kubernetes OpenAPI: a free-form object (CRD fields, annotations of unknown shape). */
  "x-kubernetes-preserve-unknown-fields"?: boolean
  /** Kubernetes OpenAPI: an integer or a string (ports, maxSurge…). */
  "x-kubernetes-int-or-string"?: boolean
}

function resolveRef(root: JsonSchema, s: JsonSchema): JsonSchema {
  let cur = s
  for (let i = 0; cur.$ref && i < 16; i++) {
    const parts = cur.$ref.replace(/^#\//, "").split("/")
    let target: unknown = root
    for (const p of parts) target = (target as Record<string, unknown> | undefined)?.[p.replace(/~1/g, "/").replace(/~0/g, "~")]
    if (!target) return cur
    // Keep the referencing schema's own description (it is usually the more specific one).
    cur = { ...(target as JsonSchema), description: cur.description ?? (target as JsonSchema).description }
  }
  return cur
}

/** Every concrete alternative of a schema: $ref resolved, oneOf/anyOf expanded, allOf merged. */
export function alternatives(root: JsonSchema, s: JsonSchema | undefined): JsonSchema[] {
  if (!s) return []
  const r = resolveRef(root, s)
  const alts = r.oneOf ?? r.anyOf
  if (alts) return alts.flatMap((a) => alternatives(root, { ...a, description: a.description ?? r.description }))
  if (r.allOf) {
    const parts = r.allOf.flatMap((a) => alternatives(root, a))
    const merged: JsonSchema = { ...r, allOf: undefined, properties: { ...r.properties } }
    for (const p of parts) {
      merged.properties = { ...p.properties, ...merged.properties }
      if (p.patternProperties) merged.patternProperties = { ...p.patternProperties, ...merged.patternProperties }
      if (p.required) merged.required = [...new Set([...(merged.required ?? []), ...p.required])]
      merged.type ??= p.type
    }
    return [merged]
  }
  return [r]
}

/** The JSON types a schema admits ("integer" stays distinct from "number"). */
export function types(s: JsonSchema): string[] {
  if (s["x-kubernetes-int-or-string"]) return ["integer", "string"]
  if (Array.isArray(s.type)) return s.type
  if (s.type) return [s.type]
  if (s.properties || s.patternProperties) return ["object"]
  if (s.items) return ["array"]
  return []
}

export function isObject(s: JsonSchema) {
  return types(s).includes("object")
}

export function isArray(s: JsonSchema) {
  return types(s).includes("array")
}

/** Pattern properties that name extension fields (x-…), not keys a mapping takes. */
const EXTENSION = "^x-"

/** The schemas at a key path (several when the schema allows alternatives). */
export function schemasAt(root: JsonSchema, path: readonly PathSegment[]): JsonSchema[] {
  let cur = alternatives(root, root)
  for (const seg of path) {
    const next: JsonSchema[] = []
    for (const s of cur) {
      if (seg === "[]") {
        if (s.items) next.push(...alternatives(root, s.items))
        continue
      }
      const prop = s.properties?.[seg]
      if (prop) {
        next.push(...alternatives(root, prop))
        continue
      }
      let matched = false
      for (const [pattern, ps] of Object.entries(s.patternProperties ?? {})) {
        if (pattern === EXTENSION) continue
        if (new RegExp(pattern).test(seg)) {
          next.push(...alternatives(root, ps))
          matched = true
          break
        }
      }
      if (!matched && typeof s.additionalProperties === "object") next.push(...alternatives(root, s.additionalProperties))
    }
    cur = next
    if (cur.length === 0) break
  }
  return cur
}

export interface KeySpec {
  name: string
  schema: JsonSchema
  description?: string
  deprecated?: boolean
  required?: boolean
}

/** Keys a mapping at `path` takes (first alternative wins on duplicates). */
export function keysAt(root: JsonSchema, path: readonly PathSegment[]): KeySpec[] {
  const out = new Map<string, KeySpec>()
  for (const s of schemasAt(root, path)) {
    if (!isObject(s)) continue
    for (const [name, raw] of Object.entries(s.properties ?? {})) {
      if (out.has(name)) continue
      const alts = alternatives(root, raw)
      const description = raw.description ?? alts.find((a) => a.description)?.description
      out.set(name, {
        name,
        schema: alts.length === 1 ? alts[0] : { oneOf: alts, description },
        description,
        deprecated: raw.deprecated || alts.some((a) => a.deprecated),
        required: s.required?.includes(name),
      })
    }
  }
  return [...out.values()]
}

/** Whether a mapping schema takes free-form names (services, networks, labels, environment…). */
export function takesFreeKeys(s: JsonSchema): boolean {
  return (
    Object.keys(s.patternProperties ?? {}).some((p) => p !== EXTENSION) ||
    typeof s.additionalProperties === "object" ||
    !!s["x-kubernetes-preserve-unknown-fields"]
  )
}

/** Whether the mapping at `path` takes free-form names. */
export function takesNames(root: JsonSchema, path: readonly PathSegment[]): boolean {
  return schemasAt(root, path).some(takesFreeKeys)
}

/** Shape of a value: what to insert after "key:" (a nested mapping, a list or an inline scalar). */
export function valueShape(root: JsonSchema, s: JsonSchema): "object" | "array" | "scalar" {
  const alts = alternatives(root, s)
  // A key that also takes a scalar (build: ./dir, depends_on: [a]) stays inline: the user picks.
  if (alts.some((a) => types(a).some((t) => t === "string" || t === "number" || t === "integer" || t === "boolean")))
    return "scalar"
  if (alts.some(isObject)) return "object"
  if (alts.some(isArray)) return "array"
  return "scalar"
}

/** Enumerated or boolean values of the schemas. */
export function enumValues(schemas: JsonSchema[]): { value: string; description?: string }[] {
  const out = new Map<string, string | undefined>()
  for (const s of schemas) {
    for (const v of s.enum ?? []) out.set(String(v), s.description)
    if (s.const !== undefined) out.set(String(s.const), s.description)
    if (types(s).includes("boolean")) {
      out.set("true", undefined)
      out.set("false", undefined)
    }
  }
  return [...out].map(([value, description]) => ({ value, description }))
}

export function describeType(root: JsonSchema, s: JsonSchema): string {
  const t = new Set<string>()
  for (const a of alternatives(root, s)) for (const x of types(a)) t.add(x === "integer" ? "number" : x)
  return [...t].join(" | ") || "any"
}
