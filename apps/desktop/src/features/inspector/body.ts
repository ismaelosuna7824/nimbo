import type { Attr, Block, Body, CtyType, Value, WorkflowNode } from "@/lib/api/types"
import { nodeAddress } from "../canvas/catalog"

export const emptyBody = (): Body => ({ attrs: [], blocks: [] })

export function getAttr(body: Body, name: string): Value | undefined {
  return body.attrs?.find((a) => a.name === name)?.value
}

/** Returns a copy of body with attribute `name` set (or removed when value is undefined); its comments stay. */
export function setAttr(body: Body, name: string, value: Value | undefined): Body {
  const attrs = body.attrs ?? []
  const idx = attrs.findIndex((a) => a.name === name)
  if (value === undefined) return { ...body, attrs: attrs.filter((a) => a.name !== name) }
  if (idx < 0) return { ...body, attrs: [...attrs, { name, value }] }
  return { ...body, attrs: attrs.map((a, i) => (i === idx ? { ...a, value } : a)) }
}

export function blocksOf(body: Body, type: string): Block[] {
  return (body.blocks ?? []).filter((b) => b.type === type)
}

/** Replaces every block of `type` with `items`, keeping the position of the first existing one. */
export function setBlocks(body: Body, type: string, items: Block[]): Body {
  const blocks = body.blocks ?? []
  const first = blocks.findIndex((b) => b.type === type)
  const rest = blocks.filter((b) => b.type !== type)
  const at = first < 0 ? rest.length : blocks.slice(0, first).filter((b) => b.type !== type).length
  return { ...body, blocks: [...rest.slice(0, at), ...items, ...rest.slice(at)] }
}

/** A value can be edited with a literal widget when it is unset or constant. */
export function hasLiteral(v: Value | undefined) {
  return v === undefined || v.literal !== undefined
}

/** HCL source for a JSON literal (JSON is valid HCL expression syntax). */
export function exprOf(v: Value | undefined): string {
  if (!v) return ""
  if (v.expr !== undefined) return v.expr
  return v.literal === undefined ? "" : JSON.stringify(v.literal, null, 2)
}

/** Index after the HCL string starting at `i` (a quote), across escapes and nested interpolations; -1 if unterminated. */
function skipString(src: string, i: number): number {
  i++
  while (i < src.length) {
    const c = src[i]
    if (c === "\\") {
      i += 2
    } else if (c === '"') {
      return i + 1
    } else if ((c === "$" || c === "%") && src[i + 1] === c && src[i + 2] === "{") {
      i += 3
    } else if ((c === "$" || c === "%") && src[i + 1] === "{") {
      let depth = 1
      i += 2
      while (i < src.length && depth > 0) {
        if (src[i] === '"') {
          i = skipString(src, i)
          if (i < 0) return -1
          continue
        }
        if (src[i] === "{") depth++
        else if (src[i] === "}") depth--
        i++
      }
    } else {
      i++
    }
  }
  return -1
}

/**
 * Splits HCL source at its top-level commas (and newlines when `newlines`), trimming and dropping
 * empty pieces; null when it holds comments or heredocs, or its brackets don't balance.
 */
export function splitTopLevel(src: string, newlines: boolean): string[] | null {
  const out: string[] = []
  let depth = 0
  let start = 0
  let i = 0
  while (i < src.length) {
    const c = src[i]
    if (c === '"') {
      i = skipString(src, i)
      if (i < 0) return null
      continue
    }
    if (c === "#" || (c === "/" && (src[i + 1] === "/" || src[i + 1] === "*")) || (c === "<" && src[i + 1] === "<")) return null
    if (c === "(" || c === "[" || c === "{") depth++
    else if (c === ")" || c === "]" || c === "}") {
      if (--depth < 0) return null
    } else if (depth === 0 && (c === "," || (newlines && c === "\n"))) {
      out.push(src.slice(start, i))
      start = i + 1
    }
    i++
  }
  if (depth !== 0) return null
  out.push(src.slice(start))
  return out.map((s) => s.trim()).filter((s) => s !== "")
}

/** The inner source of `src` when it is one bracketed constructor (`open … close`), else null. */
export function bracketed(src: string, open: "[" | "{"): string | null {
  const s = src.trim()
  if (!s.startsWith(open) || !s.endsWith(open === "[" ? "]" : "}")) return null
  return s.slice(1, -1)
}

/** The string a quoted HCL literal stands for (no interpolation), else undefined. */
export function stringLiteral(src: string): string | undefined {
  if (!src.startsWith('"') || /[$%]\{/.test(src)) return undefined
  try {
    const v: unknown = JSON.parse(src)
    return typeof v === "string" ? v : undefined
  } catch {
    return undefined
  }
}

/**
 * The HCL source of each element of a list value (a literal array or a `[a, b]` constructor); null
 * when the value is another expression (a reference to a whole list, a function, a `for`).
 */
export function listItems(v: Value | undefined): string[] | null {
  if (!v) return []
  if (v.expr === undefined) return Array.isArray(v.literal) ? v.literal.map((x) => JSON.stringify(x)) : null
  const inner = bracketed(v.expr, "[")
  if (inner === null) return null
  const items = splitTopLevel(inner, false)
  return items && !/^for\s/.test(items[0] ?? "") ? items : null
}

/** A list of elements as a value: a literal when every element is a quoted string, unset when empty. */
export function listValue(items: string[]): Value | undefined {
  if (items.length === 0) return undefined
  const strings = items.map(stringLiteral)
  if (strings.every((s) => s !== undefined)) return { literal: strings }
  return { expr: `[${items.join(", ")}]` }
}

/** Link to… on a list field: `ref` appended to the list (a new `[ref]` when the value isn't a list constructor). */
export function appendToList(v: Value | undefined, ref: string): Value | undefined {
  const items = listItems(v) ?? []
  if (v && items.includes(ref)) return v
  return listValue([...items, ref])
}

/** The list without its element `index` (unchanged when the value isn't a list constructor). */
export function removeListItem(v: Value | undefined, index: number): Value | undefined {
  const items = listItems(v)
  return items ? listValue(items.filter((_, i) => i !== index)) : v
}

/** A value from its HCL source: a literal when it is JSON (constant), else the expression. */
export function sourceValue(src: string): Value {
  if (!/[$%]\{/.test(src)) {
    try {
      return { literal: JSON.parse(src) as unknown }
    } catch {
      // not a constant
    }
  }
  return { expr: src }
}

const ITEM_RE = /^("(?:[^"\\]|\\.)*"|[A-Za-z_][\w-]*)\s*[=:]\s*([\s\S]+)$/

/** The items of one `{ k = v, … }` object constructor in order; null for any other expression. */
export function objectConstructor(src: string): Attr[] | null {
  const inner = bracketed(src, "{")
  if (inner === null) return null
  const pieces = splitTopLevel(inner, true)
  if (!pieces || /^for\s/.test(pieces[0] ?? "")) return null
  const out: Attr[] = []
  for (const p of pieces) {
    const m = ITEM_RE.exec(p)
    if (!m || m[2].startsWith("=")) return null
    out.push({ name: m[1].startsWith('"') ? (JSON.parse(m[1]) as string) : m[1], value: sourceValue(m[2].trim()) })
  }
  return out
}

/**
 * The entries of a map value (a literal object or a `{ k = v }` constructor), each value a literal or
 * the expression it holds; null when the value is another expression (merge(), a reference).
 */
export function mapEntries(v: Value | undefined): Attr[] | null {
  if (!v) return []
  if (v.expr !== undefined) return objectConstructor(v.expr)
  const lit = v.literal
  if (!lit || typeof lit !== "object" || Array.isArray(lit)) return null
  return Object.entries(lit).map(([name, literal]) => ({ name, value: { literal } }))
}

/** Map entries as a value: a literal when every value is constant, a `{ k = v }` constructor otherwise, unset when empty. */
export function mapValue(entries: Attr[]): Value | undefined {
  if (entries.length === 0) return undefined
  if (entries.every((e) => e.value.literal !== undefined)) {
    return { literal: Object.fromEntries(entries.map((e) => [e.name, e.value.literal])) }
  }
  const key = (k: string) => (/^[A-Za-z_][\w-]*$/.test(k) ? k : JSON.stringify(k))
  return { expr: `{\n${entries.map((e) => `  ${key(e.name)} = ${e.value.expr ?? JSON.stringify(e.value.literal)}`).join("\n")}\n}` }
}

/** Link to… with "Base64-encode" (user data). */
export const base64Of = (expr: string) => `base64encode(${expr})`

/**
 * String attributes that hold a JSON document: IAM and resource policies, Step Functions definitions, ECS container
 * definitions, EventBridge patterns… The provider schema types them as plain strings; the name (or the description)
 * is what tells them apart.
 */
export function isJsonAttr(name: string, description?: string): boolean {
  return (
    /(^|_)policy$|_policy_|(^|_)policy_document$|_json$|^json_|(^|_)definition$|^container_definitions$|^event_pattern$|^template_body$/.test(name) ||
    /\bJSON\b/.test(description ?? "")
  )
}

/** Expressions a JSON field keeps as they are (functions, references, heredocs), rather than as a text. */
const JSON_FIELD_EXPR = /^(jsonencode|file|templatefile|data\.|local\.|var\.|module\.|[a-z0-9]+_[a-z0-9_]+\.[\w-]+|<<)/

/**
 * The value of a JSON field from what was typed or pasted: a JSON document becomes `jsonencode(…)` (JSON is valid
 * HCL object syntax, so the document stays readable and Terraform checks it); `jsonencode(…)`, `file(…)`, data
 * sources and other expressions stay expressions; anything else is a plain string. Empty unsets it.
 */
export function jsonFieldValue(text: string): Value | undefined {
  const src = text.trim()
  if (!src) return undefined
  try {
    const doc: unknown = JSON.parse(src)
    if (doc !== null && typeof doc === "object") return { expr: `jsonencode(${JSON.stringify(doc, null, 2)})` }
  } catch {
    // Not JSON: an expression or a plain string.
  }
  // Half-typed JSON stays an expression, so the text is kept and Problems points at the mistake.
  if (JSON_FIELD_EXPR.test(src) || src.startsWith("{") || src.startsWith("[")) return { expr: src }
  return { literal: src }
}

export type CtyKind = "string" | "number" | "bool" | "string-list" | "string-map" | "complex"

export function ctyKind(t: CtyType): CtyKind {
  if (t === "string" || t === "number" || t === "bool") return t
  if (Array.isArray(t) && t.length === 2 && t[1] === "string") {
    if (t[0] === "list" || t[0] === "set") return "string-list"
    if (t[0] === "map") return "string-map"
  }
  return "complex"
}

export function ctyLabel(t: CtyType): string {
  if (typeof t === "string") return t
  const [kind, inner] = t
  if (kind === "object") return "object"
  if (kind === "tuple") return "tuple"
  return `${kind}(${ctyLabel(inner as CtyType)})`
}

export interface ResolvedRef {
  node: WorkflowNode
  /** Remainder after the address, e.g. "arn" or "outputs[0]". */
  attr: string
}

/** Resolves an expression that is exactly a traversal of another node (e.g. "aws_sqs_queue.q.arn"). */
export function resolveReference(expr: string | undefined, nodes: WorkflowNode[]): ResolvedRef | null {
  const e = expr?.trim()
  if (!e || !/^[\w.[\]"*-]+$/.test(e)) return null
  let best: ResolvedRef | null = null
  let bestLen = 0
  for (const n of nodes) {
    const addr = nodeAddress(n)
    if (e !== addr && !e.startsWith(`${addr}.`) && !e.startsWith(`${addr}[`)) continue
    if (addr.length <= bestLen) continue
    best = { node: n, attr: e.slice(addr.length).replace(/^\./, "") }
    bestLen = addr.length
  }
  return best
}

/** Nodes referenced anywhere inside an expression. */
export function referencedNodes(expr: string | undefined, nodes: WorkflowNode[]): WorkflowNode[] {
  if (!expr) return []
  return nodes.filter((n) => {
    const addr = nodeAddress(n).replace(/[.*+?^${}()|[\]\\]/g, "\\$&")
    return new RegExp(`(^|[^\\w.])${addr}(?![\\w-])`).test(expr)
  })
}
