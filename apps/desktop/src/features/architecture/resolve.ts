// Attribute values of a workflow's blocks for the Architecture diagram: variables (active
// environment first, then defaults), locals and the expressions over them (see expr.ts), so
// descriptions, counts and module inputs read as Terraform would compute them where it can.
import type { Attr, Body, NamedBlock, Value, WorkflowEdge, WorkflowNode } from "@/lib/api/types"
import type { DeriveWorkflow } from "./derive"
import { EACH_ATTR } from "./modules"
import { evaluate, hasUnknown, isRecord, liveBranch, parseType, UNKNOWN, withDefaults } from "./expr"

export interface Resolver {
  /** The value of an expression; undefined when unknown (a list with an unknown item is unknown). */
  value(v: Value | undefined): unknown
  /** The value at `path` ("settings.tier": a block or an object attribute, then its keys). */
  attr(n: WorkflowNode, path: string): unknown
  /** Every instance's value of `path` when it reads each.value.<field> of a resolvable for_each. */
  eachAttr(n: WorkflowNode, path: string): unknown[] | undefined
  /** Instances of a block from its count/for_each (undefined when unknown or unset). */
  count(n: WorkflowNode): number | undefined
  /** The known strings of a list at `path` (a list with unknown items still names its known ones). */
  strings(n: WorkflowNode, path: string): string[]
  /** The branch a conditional expression takes (source text), when its condition is known. */
  live(v: Value | undefined): string | undefined
}

/** The attribute at a path as edges name it ("create_vnic_details[0].nsg_ids"). */
export function valueAt(body: Body, path: string): Value | undefined {
  const parts = path.split(".")
  let b: Body | undefined = body
  for (let i = 0; i < parts.length - 1 && b; i++) {
    const m = /^([\w-]+)(?:\[(\d+)\])?$/.exec(parts[i])
    b = m ? b.blocks.filter((x) => x.type === m[1])[Number(m[2] ?? 0)]?.body : undefined
  }
  return b?.attrs.find((a) => a.name === parts[parts.length - 1])?.value
}

/** The attribute at `path` through nested blocks (the first block of a type). */
export function findAttr(body: Body | undefined, path: string): Value | undefined {
  if (!body) return undefined
  const [head, ...rest] = path.split(".")
  if (rest.length === 0) return body.attrs.find((a) => a.name === head)?.value
  const block = body.blocks.find((b) => b.type === head)
  return block ? findAttr(block.body, rest.join(".")) : undefined
}

/** Unknown parts become undefined; a list with an unknown item is unknown as a whole. */
function known(v: unknown): unknown {
  if (v === UNKNOWN) return undefined
  if (Array.isArray(v)) return hasUnknown(v) ? undefined : v
  if (isRecord(v)) return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, known(x)]))
  return v
}

/**
 * A resolver over the workflow's variables and locals. `forEachSource` names the block a for_each
 * iterates over when it is another resource (as many instances as it has).
 */
export function makeResolver(wf: DeriveWorkflow, forEachSource: (n: WorkflowNode) => WorkflowNode | undefined): Resolver {
  const env = wf.environments?.find((e) => e.name === wf.activeEnvironment)
  const variables = new Map<string, NamedBlock>((wf.variables ?? []).map((v) => [v.name, v]))
  const locals = new Map<string, Attr>((wf.locals ?? []).map((l) => [l.name, l]))
  const cache = new Map<string, unknown>()
  const resolving = new Set<string>()
  const counting = new Set<string>()

  /** The raw value (UNKNOWN when unknown) of a value. */
  const raw = (v: Value | undefined): unknown => {
    if (!v) return UNKNOWN
    if (v.literal !== undefined && v.literal !== null) return v.literal
    return v.expr ? evaluate(v.expr, lookup) : v.literal === null ? null : UNKNOWN
  }
  const lookup = (kind: "var" | "local", name: string): unknown => {
    const key = `${kind}.${name}`
    if (cache.has(key)) return cache.get(key)
    if (resolving.has(key)) return UNKNOWN
    resolving.add(key)
    let out: unknown
    if (kind === "local") out = raw(locals.get(name)?.value)
    else {
      const fromEnv = env?.values?.[name]
      const v = variables.get(name)
      const value = fromEnv ?? v?.body.attrs.find((a) => a.name === "default")?.value
      const forEach = fromEnv ? undefined : v?.body.attrs.find((a) => a.name === EACH_ATTR)?.value
      out = forEach && value?.expr ? eachValue(value.expr, raw(forEach)) : raw(value)
      // Optional attributes of an object type take their defaults.
      const type = v?.body.attrs.find((a) => a.name === "type")?.value.expr
      if (type && isRecord(out)) out = withDefaults(out, parseType(type))
    }
    resolving.delete(key)
    cache.set(key, out)
    return out
  }
  /** An input of a module call with for_each: its value when every instance gets the same one. */
  const eachValue = (expr: string, items: unknown): unknown => {
    const entries = Array.isArray(items) ? items.map((x) => [x, x]) : isRecord(items) ? Object.entries(items) : undefined
    if (!entries?.length) return UNKNOWN
    const values = entries.map(([key, value]) => evaluate(expr, lookup, 0, { each: { key, value } }))
    return values.every((x) => !hasUnknown(x) && JSON.stringify(x) === JSON.stringify(values[0])) ? values[0] : UNKNOWN
  }
  const attrRaw = (n: WorkflowNode, path: string): unknown => {
    const parts = path.split(".")
    let body: Body | undefined = n.body
    for (let i = 0; i < parts.length && body; i++) {
      const a = body.attrs.find((x) => x.name === parts[i])
      if (a) {
        // An object attribute ("settings = { tier = … }"): its keys.
        let cur = raw(a.value)
        for (const k of parts.slice(i + 1)) {
          cur = isRecord(cur) && k in cur ? cur[k] : UNKNOWN
        }
        return cur
      }
      body = body.blocks.find((b) => b.type === parts[i])?.body
    }
    return UNKNOWN
  }
  const forEachItems = (n: WorkflowNode): unknown[] | undefined => {
    const v = raw(findAttr(n.body, "for_each"))
    if (Array.isArray(v)) return v
    if (isRecord(v)) return Object.values(v)
    return undefined
  }
  const count = (n: WorkflowNode): number | undefined => {
    if (counting.has(n.id)) return undefined
    counting.add(n.id)
    try {
      const countAttr = findAttr(n.body, "count")
      if (countAttr) {
        const c = raw(countAttr)
        if (typeof c === "number") return c
        return typeof c === "string" && /^\d+$/.test(c) ? Number(c) : undefined
      }
      if (!findAttr(n.body, "for_each")) return undefined
      const items = forEachItems(n)
      if (items) return items.length
      const other = forEachSource(n)
      return other ? count(other) : undefined
    } finally {
      counting.delete(n.id)
    }
  }
  const eachAttr = (n: WorkflowNode, path: string): unknown[] | undefined => {
    const v = findAttr(n.body, path)
    const m = v?.expr ? /^each\.value(?:\.([\w-]+))?$/.exec(v.expr.trim()) : null
    if (!m) return undefined
    const items = forEachItems(n)
    if (!items) return undefined
    return items.map((it) => known(m[1] ? (isRecord(it) ? it[m[1]] : undefined) : it))
  }
  return {
    value: (v) => known(raw(v)),
    attr: (n, path) => known(attrRaw(n, path)),
    eachAttr,
    count,
    strings: (n, path) => {
      const v = attrRaw(n, path)
      return (Array.isArray(v) ? v : [v]).filter((x): x is string => typeof x === "string" && x !== "")
    },
    live: (v) => (v?.expr && v.expr.includes("?") ? liveBranch(v.expr, lookup) : undefined),
  }
}

/** Resource, data source and module call addresses in an expression ("aws_lb.main", "module.vpc"). */
const ADDRESS = /(?<![\w.])((?:data\.)?[a-z][a-z0-9]*_[\w-]*\.[A-Za-z_][\w-]*|module\.[A-Za-z_][\w-]*)/g
const LOCAL_REF = /(?<![\w.])local\.([A-Za-z_][\w-]*)/g

/**
 * References made through locals, which the workflow's edges do not have: a block reading
 * `local.image` whose value names `google_artifact_registry_repository.main` references that
 * repository (through locals naming other locals too). Edges carry the attribute path the way the
 * daemon names it ("template[0].containers[0].image").
 */
export function localEdges(wf: Pick<DeriveWorkflow, "nodes" | "edges" | "locals">): WorkflowEdge[] {
  const locals = new Map<string, Attr>((wf.locals ?? []).map((l) => [l.name, l]))
  if (locals.size === 0) return []
  const ids = new Set(wf.nodes.map((n) => n.id))
  const memo = new Map<string, string[]>()
  /** The addresses a local names, directly or through other locals. */
  const addressesOf = (name: string, seen: Set<string>): string[] => {
    const hit = memo.get(name)
    if (hit) return hit
    const expr = locals.get(name)?.value.expr
    if (!expr || seen.has(name)) return []
    seen.add(name)
    const out = new Set([...expr.matchAll(ADDRESS)].map((m) => m[1]).filter((a) => ids.has(a)))
    for (const m of expr.matchAll(LOCAL_REF)) for (const a of addressesOf(m[1], seen)) out.add(a)
    seen.delete(name)
    const list = [...out]
    memo.set(name, list)
    return list
  }
  const existing = new Set((wf.edges ?? []).map((e) => `${e.source}>${e.target}:${e.attribute}`))
  const out: WorkflowEdge[] = []
  const walk = (n: WorkflowNode, b: Body, prefix: string) => {
    for (const a of b.attrs) {
      const expr = a.value.expr
      if (!expr?.includes("local.")) continue
      const attribute = prefix + a.name
      for (const m of expr.matchAll(LOCAL_REF)) {
        for (const source of addressesOf(m[1], new Set())) {
          const key = `${source}>${n.id}:${attribute}`
          if (source === n.id || existing.has(key)) continue
          existing.add(key)
          out.push({ id: `local:${key}`, source, target: n.id, kind: attribute === "depends_on" ? "depends_on" : "reference", attribute })
        }
      }
    }
    const seen: Record<string, number> = {}
    for (const blk of b.blocks) {
      const i = seen[blk.type] ?? 0
      seen[blk.type] = i + 1
      walk(n, blk.body, `${prefix}${blk.type}[${i}].`)
    }
  }
  for (const n of wf.nodes) walk(n, n.body, "")
  return out
}
