// What the inspector's Link to… picker offers: attributes of other nodes (top level first, nested
// block attributes and list elements behind "More…"), input variables and locals, filtered by the
// type of the field being linked. Mirrored by the no-code audit harness
// (backend/internal/api/nocode_tf_test.go): keep both in sync.
import type { Attr, Body, CtyType, NamedBlock, SchemaBlock, Value } from "@/lib/api/types"
import { ctyKind, getAttr, listItems, objectConstructor, stringLiteral } from "./body"

/** Well-known outputs, listed first when the schema has them. */
export const PREFERRED = [
  "arn",
  "id",
  "name",
  "url",
  "invoke_arn",
  "qualified_arn",
  "endpoint",
  "function_name",
  "bucket",
  "queue_url",
  "execution_arn",
  "api_endpoint",
  "self_link",
  "primary_connection_string",
]

export interface RefOption {
  /** Path after the node address ("arn", "ip_configuration[0].private_ip_address") or the whole expression ("var.x"). */
  path: string
  type: CtyType
}

/** Attributes worth linking to: well-known outputs first, then everything the provider computes. */
export function linkableAttributes(block: SchemaBlock | undefined): RefOption[] {
  if (!block) return []
  const attrs = block.attributes ?? []
  const preferred = PREFERRED.flatMap((p) => attrs.filter((a) => a.name === p))
  const computed = attrs.filter((a) => a.computed && !PREFERRED.includes(a.name)).sort((a, b) => a.name.localeCompare(b.name))
  return [...preferred, ...computed].map((a) => ({ path: a.name, type: a.type }))
}

/** Field types of an object type (["object", {name: type}, optional?]); null for other types. */
export function objectAttrs(t: CtyType): Record<string, CtyType> | null {
  if (!Array.isArray(t) || t[0] !== "object") return null
  const fields = t[1]
  return fields && typeof fields === "object" && !Array.isArray(fields) ? (fields as Record<string, CtyType>) : null
}

/** Element object fields of a list(object) type; null otherwise (sets can't be indexed). */
function listObjectAttrs(t: CtyType): Record<string, CtyType> | null {
  if (!Array.isArray(t) || t[0] !== "list") return null
  return objectAttrs(t[1] as CtyType)
}

const byName = <T extends { name: string }>(a: T, b: T) => a.name.localeCompare(b.name)

/** Elements of a listed list(string|number|bool) attribute the picker offers (`names[0]` … `names[2]`, e.g. zones). */
const LIST_ELEMENTS = 3

/**
 * The second level of the picker ("More…"): computed attributes of nested blocks (`block[0].attr` for
 * list blocks, `block.attr` for single ones, two block levels deep; set and map blocks can't be
 * indexed), the fields of the first element of listed list(object) attributes (`services[0].id`) and
 * the first elements of listed lists of primitives (`names[0]`).
 */
export function nestedReferences(block: SchemaBlock | undefined): RefOption[] {
  if (!block) return []
  const out: RefOption[] = []
  const walk = (b: SchemaBlock, prefix: string, depth: number) => {
    for (const nb of [...(b.blocks ?? [])].sort(byName)) {
      if (nb.nesting !== "list" && nb.nesting !== "single" && nb.nesting !== "group") continue
      const at = `${prefix}${nb.name}${nb.nesting === "list" ? "[0]" : ""}`
      for (const a of [...(nb.block.attributes ?? [])].filter((a) => a.computed).sort(byName)) {
        out.push({ path: `${at}.${a.name}`, type: a.type })
      }
      if (depth < 2) walk(nb.block, `${at}.`, depth + 1)
    }
  }
  walk(block, "", 1)
  for (const top of linkableAttributes(block)) {
    const fields = listObjectAttrs(top.type)
    if (fields) {
      for (const name of Object.keys(fields).sort()) out.push({ path: `${top.path}[0].${name}`, type: fields[name] })
    } else if (Array.isArray(top.type) && top.type[0] === "list" && PRIMITIVES[top.type[1] as string] === true) {
      for (let i = 0; i < LIST_ELEMENTS; i++) out.push({ path: `${top.path}[${i}]`, type: top.type[1] as CtyType })
    }
  }
  return out
}

const PRIMITIVES: Record<string, true> = { string: true, number: true, bool: true }

/**
 * The type a variable's `type` expression declares (the Variables panel suggestions and the usual
 * collection forms); `any` and an absent type are "dynamic"; null for types the picker can't match.
 */
export function parseTypeExpr(src: string | undefined): CtyType | null {
  const s = (src ?? "").replace(/\s+/g, "")
  if (s === "" || s === "any") return "dynamic"
  if (PRIMITIVES[s] === true) return s
  const m = /^(list|set|map)\((.+)\)$/.exec(s)
  if (!m) return null
  const inner = parseTypeExpr(m[2])
  return inner === null ? null : [m[1], inner]
}

/** The type of a variable block (its `type`, dynamic when absent). */
export function variableType(v: NamedBlock): CtyType | null {
  const t = v.body.attrs?.find((a) => a.name === "type")?.value
  return parseTypeExpr(t?.expr ?? (typeof t?.literal === "string" ? t.literal : undefined))
}

/** The type of a local's value when it is a constant; any expression is "dynamic". */
export function localType(value: Value | undefined): CtyType {
  const lit = value?.literal
  if (lit === undefined || lit === null) return "dynamic"
  if (typeof lit === "string" || typeof lit === "number" || typeof lit === "boolean") return typeof lit === "boolean" ? "bool" : typeof lit
  if (Array.isArray(lit) && lit.every((x) => typeof x === "string")) return ["list", "string"]
  if (typeof lit === "object" && !Array.isArray(lit) && Object.values(lit).every((x) => typeof x === "string")) return ["map", "string"]
  return "dynamic"
}

/** Variables (`var.<name>`) and locals (`local.<name>`) the picker lists, in declaration order. */
export function scopeReferences(variables: NamedBlock[] | undefined, locals: Attr[] | undefined): RefOption[] {
  const out: RefOption[] = []
  for (const v of variables ?? []) {
    const t = variableType(v)
    if (t !== null) out.push({ path: `var.${v.name}`, type: t })
  }
  for (const l of locals ?? []) out.push({ path: `local.${l.name}`, type: localType(l.value) })
  return out
}

/** Instances of a `count` node the picker offers (`[0]` … `[2]`, fewer when the count is a smaller number). */
const COUNT_INSTANCES = 3

/** The keys a for_each value iterates when they are written down: a literal set/list/map, toset([…]), or a local/variable holding one. */
function forEachKeys(v: Value | undefined, variables: NamedBlock[], locals: Attr[], depth = 0): string[] | null {
  if (!v || depth > 3) return null
  const lit = v.literal
  if (Array.isArray(lit)) return lit.every((x) => typeof x === "string") ? (lit as string[]) : null
  if (lit && typeof lit === "object") return Object.keys(lit)
  const e = v.expr?.trim()
  if (!e) return null
  const set = /^toset\(([\s\S]+)\)$/.exec(e)
  if (set) return forEachKeys({ expr: set[1] }, variables, locals, depth + 1)
  const local = /^local\.([\w-]+)$/.exec(e)
  if (local) return forEachKeys(locals.find((l) => l.name === local[1])?.value, variables, locals, depth + 1)
  const variable = /^var\.([\w-]+)$/.exec(e)
  if (variable) {
    const def = variables.find((x) => x.name === variable[1])?.body.attrs.find((a) => a.name === "default")?.value
    return forEachKeys(def, variables, locals, depth + 1)
  }
  const obj = objectConstructor(e)
  if (obj) return obj.map((a) => a.name)
  const items = listItems(v)?.map(stringLiteral)
  return items && items.every((s) => s !== undefined) ? (items as string[]) : null
}

/**
 * How Link to… addresses a node's instances: null for a single instance (`addr.attr`), `[0]` … for
 * `count`, `["key"]` for each written-down `for_each` key (none when the keys aren't known).
 */
export function instanceKeys(body: Body, variables: NamedBlock[], locals: Attr[]): string[] | null {
  const count = getAttr(body, "count")
  if (count) {
    const n = typeof count.literal === "number" ? Math.min(count.literal, COUNT_INSTANCES) : COUNT_INSTANCES
    return Array.from({ length: Math.max(n, 0) }, (_, i) => `[${i}]`)
  }
  const forEach = getAttr(body, "for_each")
  if (forEach) return (forEachKeys(forEach, variables, locals) ?? []).map((k) => `[${JSON.stringify(k)}]`)
  return null
}

/**
 * How a reference of type `source` goes into a field of type `target`: as is ("replace"; Terraform
 * converts primitives and lists to sets), as a new element of a list of strings ("append"), or not
 * at all (null: it would need an expression around it).
 */
export function refFit(target: CtyType, source: CtyType): "replace" | "append" | null {
  if (target === "dynamic" || source === "dynamic") return "replace"
  if (JSON.stringify(target) === JSON.stringify(source)) return "replace"
  const tk = ctyKind(target)
  const sk = ctyKind(source)
  if (tk === "string") return PRIMITIVES[sk] === true ? "replace" : null
  if (tk === "number" || tk === "bool") return sk === tk || sk === "string" ? "replace" : null
  if (tk === "string-list") return sk === "string-list" ? "replace" : PRIMITIVES[sk] === true ? "append" : null
  return null
}
