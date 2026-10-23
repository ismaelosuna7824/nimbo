// Completion and hover for YAML files described by a JSON schema: the keys a
// mapping takes (with the right shape inserted: nested mapping, list or
// inline value), enumerated/boolean values and the keys' docs. Dialects add
// their own knowledge through hooks: names declared in the project, catalog
// images, values the schema leaves as free strings.

import type { Completion, CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import { docNode } from "@/features/panels/terraform/intel/format"
import { keyAt, yamlContextAt, type KeyContext, type PathSegment, type ValueContext } from "./context"
import { describeType, enumValues, isObject, keysAt, schemasAt, takesNames, valueShape, type JsonSchema, type KeySpec } from "./schema"

export const S_KEYS: CompletionSection = { name: "Keys", rank: 0 }
export const S_VALUES: CompletionSection = { name: "Values", rank: 0 }

export interface SchemaIntelOptions {
  /** Reference page of a key at a path (hover and completion info link). */
  docsUrl?: (path: readonly PathSegment[], key: string, schema: JsonSchema) => string | undefined
  docsLabel?: string
  /**
   * Completions replacing the schema's keys at a key position (e.g. names declared in the project under
   * depends_on:); null: the schema decides.
   */
  keys?: (ctx: KeyContext, text: string) => CompletionResult | null
  /** Extra values at a value position (project names, catalog images…), listed before the schema's. */
  values?: (ctx: ValueContext, text: string) => Completion[]
  /** Values of keys the schema leaves as free strings, by key name (restart: no | always | …). */
  knownValues?: Record<string, { value: string; description: string }[]>
}

/** Keys already present in the mapping the cursor is in (same indentation, same block). */
export function siblingKeys(text: string, pos: number, indent: number): Set<string> {
  const lines = text.split("\n")
  const lineNo = text.slice(0, pos).split("\n").length - 1
  const out = new Set<string>()
  const keyOf = (l: string) => {
    const i = l.search(/\S/)
    if (i !== indent) return undefined
    return /^([^\s:#'"-][^:#]*?)\s*:(\s|$)/.exec(l.slice(i))?.[1]
  }
  const within = (l: string) => {
    const i = l.search(/\S/)
    if (/^(---|\.\.\.)(\s|$)/.test(l)) return false
    return i < 0 || i > indent || (i === indent && !l.slice(i).startsWith("- "))
  }
  const dashItem = (l: string) => l.search(/\S/) + 2 === indent && /^\s*- /.test(l)
  for (let i = lineNo - 1; i >= 0; i--) {
    const l = lines[i]
    if (dashItem(l)) {
      const k = /^\s*- ([^\s:#'"][^:#]*?)\s*:(\s|$)/.exec(l)?.[1]
      if (k) out.add(k)
      break
    }
    if (!within(l)) break
    const k = keyOf(l)
    if (k) out.add(k)
  }
  for (let i = lineNo + 1; i < lines.length; i++) {
    const l = lines[i]
    if (!within(l)) break
    const k = keyOf(l)
    if (k) out.add(k)
  }
  return out
}

function badges(schema: JsonSchema, spec: KeySpec) {
  return [describeType(schema, spec.schema), ...(spec.required ? ["required"] : []), ...(spec.deprecated ? ["deprecated"] : [])]
}

/** The completion of one key: inserts "key: ", "key:\n  " or "key:\n  - " after the schema's value shape. */
export function keyCompletion(spec: KeySpec, schema: JsonSchema, path: PathSegment[], indent: number, opts: SchemaIntelOptions = {}): Completion {
  const shape = valueShape(schema, spec.schema)
  const pad = " ".repeat(indent + 2)
  const insert = shape === "object" ? `${spec.name}:\n${pad}` : shape === "array" ? `${spec.name}:\n${pad}- ` : `${spec.name}: `
  return {
    label: spec.name,
    type: shape === "scalar" ? "property" : "namespace",
    detail: describeType(schema, spec.schema),
    section: S_KEYS,
    boost: spec.required ? 2 : 0,
    apply: insert,
    info: () =>
      docNode({
        title: spec.name,
        badges: badges(schema, spec),
        description: spec.description,
        docsUrl: opts.docsUrl?.(path, spec.name, schema),
        docsLabel: opts.docsLabel,
      }),
  }
}

/** Completions at `pos` of a YAML file described by `schema`; null when none apply. */
export function schemaCompletions(text: string, pos: number, explicit: boolean, schema: JsonSchema, opts: SchemaIntelOptions = {}): CompletionResult | null {
  const ctx = yamlContextAt(text, pos)
  if (ctx.kind === "none") return null

  if (ctx.kind === "key") {
    if (!explicit && ctx.prefix === "") return null
    const custom = opts.keys?.(ctx, text)
    if (custom) return custom
    if (takesNames(schema, ctx.path) && keysAt(schema, ctx.path).length === 0) return null
    const present = siblingKeys(text, pos, ctx.indent)
    const options = keysAt(schema, ctx.path)
      .filter((k) => !present.has(k.name) && !k.deprecated)
      .map((k) => keyCompletion(k, schema, ctx.path, ctx.indent, opts))
    return options.length ? { from: ctx.from, options, validFor: /^[\w.-]*$/ } : null
  }

  // A value: after "key:" or a sequence item.
  const options: Completion[] = [...(opts.values?.(ctx, text) ?? [])]
  const schemas = schemasAt(schema, ctx.path)
  // "- name" in a list of mappings: the item's keys.
  if (ctx.key === "" && schemas.length > 0 && schemas.every(isObject)) {
    const keys = keysAt(schema, ctx.path)
    if (keys.length && (explicit || ctx.prefix)) {
      return { from: ctx.from, options: keys.map((k) => keyCompletion(k, schema, ctx.path, ctx.indent, opts)), validFor: /^[\w.-]*$/ }
    }
  }
  // Items taking a short string or a mapping (ports, volumes long syntax): a word being typed may be the item's first key.
  if (ctx.key === "" && /^[A-Za-z_]/.test(ctx.prefix) && schemas.some(isObject)) {
    options.push(...keysAt(schema, ctx.path).map((k) => keyCompletion(k, schema, ctx.path, ctx.indent, opts)))
  }
  for (const v of enumValues(schemas)) options.push({ label: v.value, type: "enum", section: S_VALUES, info: v.description })
  const known = opts.knownValues?.[ctx.key]
  if (known && !options.some((o) => o.section === S_VALUES)) {
    for (const v of known) options.push({ label: v.value, type: "enum", section: S_VALUES, info: v.description })
  }
  if (options.length === 0) return null
  if (!explicit && ctx.prefix === "" && ctx.key === "") return null
  return { from: ctx.from, options, validFor: /^[\w.:/@-]*$/ }
}

export interface YamlHover {
  from: number
  to: number
  dom: HTMLElement
}

/** Docs of the key under the cursor from the schema; null elsewhere. */
export function schemaHover(text: string, pos: number, schema: JsonSchema, opts: SchemaIntelOptions = {}): YamlHover | null {
  const k = keyAt(text, pos)
  if (!k) return null
  const spec = keysAt(schema, k.path).find((s) => s.name === k.key)
  if (!spec) return null
  return {
    from: k.from,
    to: k.to,
    dom: docNode({
      title: [...k.path.filter((p) => p !== "[]"), k.key].join("."),
      badges: badges(schema, spec),
      description: spec.description,
      docsUrl: opts.docsUrl?.(k.path, k.key, schema),
      docsLabel: opts.docsLabel,
    }),
  }
}
