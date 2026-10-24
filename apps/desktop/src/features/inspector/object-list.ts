// Attributes typed list(object)/set(object) (aws_security_group ingress, azurerm security_rule, …) are
// edited as cards, one per object, written either as blocks (attributes-as-blocks syntax, what the
// starters use and what a new card adds) or back into the attribute's value when it holds a list of
// object constructors. Mirrored by the no-code audit harness (backend/internal/api/nocode_tf_test.go).
import type { Body, CtyType, SchemaAttribute, Value } from "@/lib/api/types"
import { bracketed, emptyBody, objectConstructor, splitTopLevel } from "./body"
import { objectAttrs } from "./references"

/** The object fields of a list(object)/set(object) type; null for other types. */
export function objectListFields(t: CtyType): SchemaAttribute[] | null {
  if (!Array.isArray(t) || (t[0] !== "list" && t[0] !== "set")) return null
  const fields = objectAttrs(t[1] as CtyType)
  if (!fields) return null
  return Object.keys(fields)
    .sort()
    .map((name) => ({ name, type: fields[name], optional: true }))
}

/** The fields of one `{ k = v, … }` object constructor (nulls dropped); null for any other expression. */
function objectItem(src: string): Body | null {
  const entries = objectConstructor(src)
  return entries ? { ...emptyBody(), attrs: entries.filter((a) => a.value.literal !== null) } : null
}

/**
 * The objects of a list(object) value as card bodies: from a literal array of objects or a
 * `[{ … }, …]` constructor; null when the value is anything else (a `for`, a function, a reference),
 * which stays an expression.
 */
export function objectListFromValue(v: Value | undefined): Body[] | null {
  if (!v) return []
  if (v.expr === undefined) {
    if (!Array.isArray(v.literal)) return null
    const out: Body[] = []
    for (const o of v.literal) {
      if (!o || typeof o !== "object" || Array.isArray(o)) return null
      const body = emptyBody()
      for (const [name, literal] of Object.entries(o)) if (literal !== null) body.attrs.push({ name, value: { literal } })
      out.push(body)
    }
    return out
  }
  const inner = bracketed(v.expr, "[")
  if (inner === null) return null
  const items = splitTopLevel(inner, false)
  if (!items) return null
  const out: Body[] = []
  for (const it of items) {
    const body = objectItem(it)
    if (!body) return null
    out.push(body)
  }
  return out
}

/** Card bodies back to a list(object) value: a literal when every field is constant, else HCL. */
export function objectListToValue(items: Body[]): Value {
  if (items.every((b) => b.attrs.every((a) => a.value.literal !== undefined))) {
    return { literal: items.map((b) => Object.fromEntries(b.attrs.map((a) => [a.name, a.value.literal]))) }
  }
  const objects = items.map((b) => `{\n${b.attrs.map((a) => `    ${a.name} = ${a.value.expr ?? JSON.stringify(a.value.literal)}`).join("\n")}\n  }`)
  return { expr: `[\n  ${objects.join(",\n  ")},\n]` }
}
