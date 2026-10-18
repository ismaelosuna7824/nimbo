import type { Attr, Body, NamedBlock, Value, Workflow } from "@/lib/api/types"

/** How a value is edited: typed literal controls or a raw HCL expression. */
export type ValueKind = "string" | "number" | "bool" | "expr"

export function getAttr(body: Body, name: string): Value | undefined {
  return body.attrs.find((a) => a.name === name)?.value
}

/** Sets (or with `undefined` removes) an attribute on a draft body in place, keeping its comments. */
export function setAttr(body: Body, name: string, value: Value | undefined): void {
  const i = body.attrs.findIndex((a) => a.name === name)
  if (value === undefined) {
    if (i !== -1) body.attrs.splice(i, 1)
  } else if (i === -1) {
    body.attrs.push({ name, value })
  } else {
    body.attrs[i] = { ...body.attrs[i], value }
  }
}

export function hasLiteral(v: Value | undefined): v is Value & { literal: unknown } {
  return v !== undefined && "literal" in v && v.literal !== undefined
}

/** HCL source for a value: its expression, else the literal rendered as HCL-compatible JSON. */
export function valueText(v: Value | undefined): string {
  if (!v) return ""
  if (v.expr !== undefined) return v.expr
  return hasLiteral(v) ? JSON.stringify(v.literal) : ""
}

export function literalString(v: Value | undefined): string | undefined {
  return hasLiteral(v) && typeof v.literal === "string" ? v.literal : undefined
}

export function literalBool(v: Value | undefined): boolean {
  return hasLiteral(v) && v.literal === true
}

/** Edit kind for a variable from its `type` constraint (falling back to its default's literal). */
export function variableKind(variable: NamedBlock): ValueKind {
  const type = valueText(getAttr(variable.body, "type")).replace(/\s+/g, "")
  if (type === "string" || type === "number" || type === "bool") return type
  if (type && type !== "any") return "expr"
  const def = getAttr(variable.body, "default")
  if (!hasLiteral(def)) return def ? "expr" : "string"
  switch (typeof def.literal) {
    case "string":
      return "string"
    case "number":
      return "number"
    case "boolean":
      return "bool"
    default:
      return "expr"
  }
}

/** True when the value can be shown in the typed control for `kind`. */
export function fitsKind(kind: ValueKind, v: Value | undefined): boolean {
  if (kind === "expr") return false
  if (!v) return true
  if (!hasLiteral(v)) return false
  const t = typeof v.literal
  return (kind === "string" && t === "string") || (kind === "number" && t === "number") || (kind === "bool" && t === "boolean")
}

export function uniqueName(base: string, taken: string[]): string {
  if (!taken.includes(base)) return base
  for (let i = 2; ; i++) if (!taken.includes(`${base}_${i}`)) return `${base}_${i}`
}

/** Names of variables without a default that have no value in the environment. */
export function missingRequired(wf: Pick<Workflow, "environments" | "variables">, envName: string | undefined): string[] {
  const env = wf.environments.find((e) => e.name === envName)
  return wf.variables
    .filter((v) => getAttr(v.body, "default") === undefined)
    .filter((v) => !env || env.values[v.name] === undefined)
    .map((v) => v.name)
}

export function emptyBody(attrs: Attr[] = []): Body {
  return { attrs, blocks: [] }
}
