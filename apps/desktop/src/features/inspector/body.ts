import type { Block, Body, CtyType, Value, WorkflowNode } from "@/lib/api/types"
import { nodeAddress } from "../canvas/catalog"

export const emptyBody = (): Body => ({ attrs: [], blocks: [] })

export function getAttr(body: Body, name: string): Value | undefined {
  return body.attrs?.find((a) => a.name === name)?.value
}

/** Returns a copy of body with attribute `name` set (or removed when value is undefined). */
export function setAttr(body: Body, name: string, value: Value | undefined): Body {
  const attrs = body.attrs ?? []
  const idx = attrs.findIndex((a) => a.name === name)
  if (value === undefined) return { ...body, attrs: attrs.filter((a) => a.name !== name) }
  if (idx < 0) return { ...body, attrs: [...attrs, { name, value }] }
  return { ...body, attrs: attrs.map((a, i) => (i === idx ? { name, value } : a)) }
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
