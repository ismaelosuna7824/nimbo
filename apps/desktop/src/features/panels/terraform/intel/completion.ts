// Terraform completion: block keywords, resource/data types, arguments and
// nested blocks from the provider schemas, values, references and functions.

import {
  pickedCompletion,
  snippet,
  startCompletion,
  type Completion,
  type CompletionContext,
  type CompletionResult,
  type CompletionSection,
} from "@codemirror/autocomplete"
import type { EditorView } from "@codemirror/view"
import type { Cloud, ResourceSchema, SchemaBlock, TypeInfo } from "@/lib/api/types"
import { bodySpec, iteratorName, schemaItems, type BodyEnv, type ItemSpec } from "./blocks"
import { cachedSchema, loadFunctions, loadSchema, loadTypes, prefetchSchemas } from "./catalog"
import { contextAt, type ItemContext, type LabelContext, type ValueContext } from "./context"
import { docNode, objectKeys, signature, typeName } from "./format"
import type { Block } from "./scan"
import { dirOf, type ModuleSymbols, type SymbolIndex } from "./symbols"

/** Extra data the option renderer reads (badges, strike-through). */
export interface TfCompletion extends Completion {
  local?: boolean
  deprecated?: boolean
}

const S_REQUIRED: CompletionSection = { name: "Required", rank: 0 }
const S_ARGS: CompletionSection = { name: "Arguments", rank: 1 }
const S_BLOCKS: CompletionSection = { name: "Blocks", rank: 2 }
const S_META: CompletionSection = { name: "Meta-arguments", rank: 3 }
const S_REFS: CompletionSection = { name: "References", rank: 0 }
const S_FUNCS: CompletionSection = { name: "Functions", rank: 1 }

const CLOUD_LABEL: Record<Cloud, string> = { aws: "AWS", azure: "Azure", gcp: "Google Cloud", oci: "OCI" }
const WORD = /^[\w-]*$/

export interface CompletionEnv {
  /** Workflow path of the file. */
  path: string
  symbols: SymbolIndex
}

/** Builds the completion source of a file; env resolves the workspace for the current text. */
export function terraformCompletionSource(env: (text: string) => Promise<CompletionEnv>) {
  return async (cx: CompletionContext): Promise<CompletionResult | null> => {
    if (cx.state.readOnly) return null
    const text = cx.state.doc.toString()
    return complete(text, cx.pos, cx.explicit, await env(text))
  }
}

/** Completions at pos in text (null when none apply). */
export async function complete(text: string, pos: number, explicit: boolean, env: CompletionEnv): Promise<CompletionResult | null> {
  const ctx = contextAt(text, pos)
  const module = env.symbols.module(dirOf(env.path))
  const body: BodyEnv = { symbols: env.symbols, module, text }
  switch (ctx.kind) {
    case "item":
      if (!explicit && ctx.prefix === "") return null
      return ctx.path.length === 0 ? topLevel(ctx) : bodyItems(ctx, body)
    case "label":
      return labels(ctx, module, explicit)
    case "value":
      return values(ctx, body, explicit)
    default:
      return null
  }
}

// ---------------------------------------------------------------- top level

const TOP_SNIPPETS: [string, string, string][] = [
  ["resource", 'resource "${}" "${name}" {\n\t${}\n}', "A managed infrastructure object"],
  ["data", 'data "${}" "${name}" {\n\t${}\n}', "Read an existing object"],
  ["module", 'module "${name}" {\n\tsource = "${}"\n}', "Call another module"],
  ["variable", 'variable "${name}" {\n\ttype        = ${string}\n\tdescription = "${}"\n}', "Input variable"],
  ["output", 'output "${name}" {\n\tvalue = ${}\n}', "Output value"],
  ["locals", "locals {\n\t${name} = ${}\n}", "Named local values"],
  ["provider", 'provider "${}" {\n\t${}\n}', "Provider configuration"],
  ["terraform", "terraform {\n\trequired_providers {\n\t\t${}\n\t}\n}", "Terraform settings"],
  ["moved", "moved {\n\tfrom = ${}\n\tto   = ${}\n}", "Record a renamed or moved address"],
  ["import", 'import {\n\tto = ${}\n\tid = "${}"\n}', "Bring an existing object under management"],
  ["check", 'check "${name}" {\n\tassert {\n\t\tcondition     = ${}\n\t\terror_message = "${}"\n\t}\n}', "Continuous validation"],
  ["removed", "removed {\n\tfrom = ${}\n\n\tlifecycle {\n\t\tdestroy = ${false}\n\t}\n}", "Stop managing an object"],
]

/** Starts completion again right after a snippet put the cursor in a label or value. */
function snippetThenComplete(template: string) {
  const apply = snippet(template)
  return (view: EditorView, c: Completion, from: number, to: number) => {
    apply(view, c, from, to)
    startCompletion(view)
  }
}

function topLevel(ctx: ItemContext): CompletionResult {
  return {
    from: ctx.from,
    validFor: WORD,
    options: TOP_SNIPPETS.map(([label, template, detail]) => ({
      label,
      type: "keyword",
      detail,
      apply: label === "resource" || label === "data" || label === "provider" ? snippetThenComplete(template) : snippet(template),
    })),
  }
}

// ---------------------------------------------------------------- labels

/** A default name for a new block of a type: aws_sqs_queue -> "queue", unique in the module. */
function defaultName(type: string, module: ModuleSymbols, kind: "resource" | "data"): string {
  const base = type.slice(type.lastIndexOf("_") + 1) || "this"
  const taken = (n: string) => module.resources.has(kind === "data" ? `data.${type}.${n}` : `${type}.${n}`)
  if (!taken(base)) return base
  for (let i = 2; ; i++) if (!taken(`${base}${i}`)) return `${base}${i}`
}

/** Snippet lines for the required arguments and blocks of a schema block. */
function requiredLines(b: SchemaBlock, indent = "\t"): string[] {
  const lines: string[] = []
  for (const a of b.attributes) if (a.required) lines.push(`${indent}${a.name} = ${valueTemplate(a.type, a.enum, indent)}`)
  for (const nb of b.blocks) {
    if ((nb.minItems ?? 0) > 0) {
      const inner = requiredLines(nb.block, indent + "\t")
      lines.push(`${indent}${nb.name} {`, ...(inner.length ? inner : [`${indent}\t\${}`]), `${indent}}`)
    }
  }
  return lines
}

/** The value part of an argument snippet, shaped by its type; indent is the argument line's. */
function valueTemplate(type: unknown, enumValues: string[] | undefined, indent: string): string {
  if (enumValues?.length) return '"${}"'
  const t = typeName(type)
  if (t === "string") return '"${}"'
  if (t.startsWith("list(") || t.startsWith("set(")) return "[${}]"
  if (t.startsWith("map(") || t.startsWith("object(")) return `{\n${indent}\t\${}\n${indent}}`
  return "${}"
}

function typeOption(t: TypeInfo, ctx: LabelContext, module: ModuleSymbols, used: Set<string>): TfCompletion {
  const provider = t.type.slice(0, t.type.indexOf("_"))
  const cloud = t.cloud ? CLOUD_LABEL[t.cloud] : "Utility"
  return {
    label: t.type,
    detail: `${t.label} · ${cloud}`,
    type: t.kind === "data" ? "interface" : "class",
    boost: (used.has(provider) ? 40 : 0) + (t.local ? 2 : 0),
    local: t.local,
    info: () =>
      loadSchema(t.kind, t.type).then((s) =>
        docNode({
          title: `${t.kind === "data" ? "data source" : "resource"} ${t.type}`,
          badges: [cloud, ...(t.local ? ["runs locally"] : [])],
          description: s?.description || t.label,
          docsUrl: s?.docsUrl,
        }),
      ),
    apply: (view, c, from, to) => applyType(view, c, from, to, t, ctx, module),
  }
}

/** Inserts a type label; on a new block also its name and a body with the type's required arguments. */
function applyType(view: EditorView, c: Completion, from: number, to: number, t: TypeInfo, ctx: LabelContext, module: ModuleSymbols) {
  const doc = view.state.doc.toString()
  const open = ctx.quoted ? "" : '"'
  const end = ctx.quoted && ctx.closed ? to + 1 : to
  const block = ctx.block
  const fresh = block.open < 0 && block.labels.length <= 1 && ctx.index === 0 && block.type !== "provider"
  if (!fresh) {
    view.dispatch({
      changes: { from, to: end, insert: `${open}${t.type}"` },
      selection: { anchor: from + open.length + t.type.length + 1 },
      annotations: pickedCompletion.of(c),
    })
    return
  }
  // The rest of the header line (an unclosed label may have swallowed it) is replaced.
  let lineEnd = doc.indexOf("\n", end)
  if (lineEnd < 0) lineEnd = doc.length
  const rest = doc.slice(end, lineEnd)
  const replaceTo = /^\s*\{?\s*$/.test(rest) ? lineEnd : end
  const write = (schema: ResourceSchema | null) => {
    const lines = schema ? requiredLines(schema.block) : []
    const name = defaultName(t.type, module, t.kind)
    const template = `${open}${t.type}" "\${${name}}" {\n${lines.length ? lines.join("\n") : "\t${}"}\n}`
    snippet(template)(view, c, from, replaceTo)
  }
  const cached = cachedSchema(t.kind, t.type)
  if (cached !== undefined) {
    write(cached)
    return
  }
  const before = view.state.doc
  void loadSchema(t.kind, t.type)
    .catch(() => null)
    .then((s) => {
      // Only when nothing changed meanwhile.
      if (view.state.doc === before) write(s)
    })
}

async function labels(ctx: LabelContext, module: ModuleSymbols, explicit: boolean): Promise<CompletionResult | null> {
  const b = ctx.block
  if (ctx.index !== 0 || (!explicit && ctx.prefix === "" && !ctx.quoted)) return null
  if (b.type === "resource" || b.type === "data") {
    const idx = await loadTypes().catch(() => null)
    if (!idx) return null
    const list = b.type === "data" ? idx.data : idx.resources
    const used = new Set(module.providers)
    const prefix = ctx.prefix.toLowerCase()
    const options = list.filter((t) => prefix === "" || t.type.includes(prefix)).map((t) => typeOption(t, ctx, module, used))
    // Prefetch the best matches so accepting one has its schema ready.
    if (prefix.length >= 3) {
      prefetchSchemas(
        list
          .filter((t) => t.type.startsWith(prefix))
          .slice(0, 20)
          .map((t) => [t.kind, t.type] as ["resource" | "data", string]),
      )
    }
    return { from: ctx.from, to: ctx.to, options, validFor: WORD }
  }
  if (b.type === "provider") {
    const idx = await loadTypes().catch(() => null)
    if (!idx) return null
    const used = new Set(module.providers)
    return {
      from: ctx.from,
      to: ctx.to,
      validFor: WORD,
      options: idx.providers.map((p) => ({
        label: p,
        type: "namespace",
        boost: used.has(p) ? 10 : 0,
        apply: (view, c, from, to) =>
          view.dispatch({
            changes: { from, to: ctx.quoted && ctx.closed ? to + 1 : to, insert: `${ctx.quoted ? "" : '"'}${p}"` },
            annotations: pickedCompletion.of(c),
          }),
      })),
    }
  }
  return null
}

// ---------------------------------------------------------------- body items

function itemTemplate(it: ItemSpec): string {
  if (it.kind === "block") {
    const labels = (it.labels ?? []).map((l) => ` "\${${l}}"`).join("")
    const inner = it.block ? requiredLines(it.block) : []
    return `${it.name}${labels} {\n${inner.length ? inner.join("\n") : "\t${}"}\n}`
  }
  if (it.typeText !== undefined) {
    if (it.enum?.length) return `${it.name} = ${it.typeText === "string" ? '"${}"' : "${}"}`
    if (it.typeText === "string") return `${it.name} = "\${}"`
    if (it.typeText.startsWith("list") || it.typeText.startsWith("set")) return `${it.name} = [\${}]`
    if (it.typeText.startsWith("map") || it.typeText.startsWith("object")) return `${it.name} = {\n\t\${}\n}`
    return `${it.name} = \${}`
  }
  return `${it.name} = ${valueTemplate(it.type, it.enum, "")}`
}

function itemDetail(it: ItemSpec): string {
  if (it.kind === "block") {
    const n = it.nesting === "single" || it.maxItems === 1 ? "block" : `block · ${it.nesting ?? "list"}`
    return it.required ? `${n} · required` : n
  }
  const t = it.typeText ?? typeName(it.type)
  return [t, it.required ? "required" : "", it.deprecated ? "deprecated" : "", it.sensitive ? "sensitive" : ""].filter(Boolean).join(" · ")
}

function itemOption(it: ItemSpec, spec: { owner: string; docsUrl?: string }): TfCompletion {
  const template = itemTemplate(it)
  const reopen = (it.enum?.length ?? 0) > 0 || it.name === "type" || it.name === "source"
  return {
    label: it.name,
    type: it.kind === "block" ? "type" : "property",
    detail: itemDetail(it),
    section: it.meta ? S_META : it.required ? S_REQUIRED : it.kind === "block" ? S_BLOCKS : S_ARGS,
    boost: it.deprecated ? -30 : 0,
    deprecated: it.deprecated,
    info: () =>
      docNode({
        title: `${spec.owner} › ${it.name}`,
        badges: [itemDetail(it)],
        description: it.description,
        docsUrl: spec.docsUrl,
        extra: it.enum?.length ? `Allowed: ${it.enum.join(", ")}` : undefined,
      }),
    apply: reopen ? snippetThenComplete(template) : snippet(template),
  }
}

async function bodyItems(ctx: ItemContext, env: BodyEnv): Promise<CompletionResult | null> {
  const spec = await bodySpec(ctx.path, env)
  if (!spec) return null
  const b = ctx.path[ctx.path.length - 1]
  const set = new Set(b.attrs.map((a) => a.name))
  const blocks = new Set(b.blocks.map((x) => x.type))
  const options = spec.items
    .filter((it) => !it.computed)
    .filter((it) =>
      it.kind === "attr" ? !set.has(it.name) : !(blocks.has(it.name) && (it.nesting === "single" || it.maxItems === 1)),
    )
    .map((it) => itemOption(it, spec))
  return { from: ctx.from, options, validFor: WORD }
}

// ---------------------------------------------------------------- values

const TYPE_CONSTRAINTS: [string, string][] = [
  ["string", "string"],
  ["number", "number"],
  ["bool", "bool"],
  ["any", "any"],
  ["list", "list(${string})"],
  ["set", "set(${string})"],
  ["map", "map(${string})"],
  ["object", "object({\n\t${name} = ${string}\n})"],
  ["tuple", "tuple([${string}])"],
  ["optional", "optional(${string})"],
]

/** The schema item of the attribute the cursor is in. */
async function attrItem(ctx: ValueContext, env: BodyEnv): Promise<ItemSpec | undefined> {
  const spec = await bodySpec(ctx.path, env).catch(() => null)
  return spec?.items.find((i) => i.name === ctx.attr.name)
}

function stringValues(ctx: ValueContext, it: ItemSpec | undefined, env: BodyEnv): CompletionResult | null {
  const s = ctx.string!
  const top = ctx.path[0]
  if (top?.type === "module" && ctx.attr.name === "source" && ctx.path.length === 1) {
    // Local modules of the workflow, relative to this directory.
    const here = env.module.dir
    const dirs = new Set<string>()
    for (const p of Object.keys(env.symbols.files)) {
      const d = dirOf(p)
      if (p.endsWith(".tf") && d !== here && d.startsWith("modules/")) dirs.add(d)
    }
    const up = here ? here.split("/").map(() => "..").join("/") : "."
    return {
      from: s.from,
      to: s.to,
      validFor: /^[\w./-]*$/,
      options: [...dirs].map((d) => ({ label: `${up}/${d}`, type: "constant", detail: "local module" })),
    }
  }
  if (!it?.enum?.length) return null
  return {
    from: s.from,
    to: s.to,
    validFor: WORD,
    options: it.enum.map((v) => ({ label: v, type: "enum", detail: it.name })),
  }
}

/** Reference roots valid where the cursor is. */
function rootOptions(ctx: ValueContext, env: BodyEnv): Completion[] {
  const top = ctx.path[0]
  const has = (b: Block | undefined, name: string) => !!b?.attrs.some((a) => a.name === name)
  const opts: Completion[] = [
    { label: "var", type: "namespace", detail: "input variables", apply: dotThenComplete("var") },
    { label: "local", type: "namespace", detail: "local values", apply: dotThenComplete("local") },
    { label: "module", type: "namespace", detail: "module outputs", apply: dotThenComplete("module") },
    { label: "data", type: "namespace", detail: "data sources", apply: dotThenComplete("data") },
    { label: "path", type: "namespace", detail: "module, root, cwd", apply: dotThenComplete("path") },
    { label: "terraform", type: "namespace", detail: "workspace", apply: dotThenComplete("terraform") },
  ]
  if (has(top, "for_each") || ctx.path.some((b) => b.type === "dynamic"))
    opts.push({ label: "each", type: "namespace", detail: "key, value", apply: dotThenComplete("each") })
  if (has(top, "count")) opts.push({ label: "count", type: "namespace", detail: "index", apply: dotThenComplete("count") })
  if (ctx.path.some((b) => b.type === "provisioner" || b.type === "connection" || b.type === "postcondition"))
    opts.push({ label: "self", type: "namespace", detail: "this resource", apply: dotThenComplete("self") })
  for (const b of ctx.path)
    if (b.type === "dynamic") {
      const it = iteratorName(b, env.text)
      if (it) opts.push({ label: it, type: "namespace", detail: "dynamic iterator: key, value", apply: dotThenComplete(it) })
    }
  const types = new Set<string>()
  for (const r of env.module.resources.values()) if (r.kind === "resource") types.add(r.type)
  for (const t of types) opts.push({ label: t, type: "class", detail: "resource", apply: dotThenComplete(t) })
  for (const o of opts) o.section = S_REFS
  for (const k of ["true", "false", "null"]) opts.push({ label: k, type: "keyword", section: S_REFS })
  return opts
}

function dotThenComplete(text: string) {
  return (view: EditorView, c: Completion, from: number, to: number) => {
    view.dispatch({ changes: { from, to, insert: `${text}.` }, selection: { anchor: from + text.length + 1 }, annotations: pickedCompletion.of(c) })
    startCompletion(view)
  }
}

async function functionOptions(): Promise<Completion[]> {
  const fns = await loadFunctions().catch(() => null)
  if (!fns) return []
  return [...fns.values()].map((fn) => ({
    label: fn.name,
    type: "function",
    detail: signature(fn).text.slice(fn.name.length),
    section: S_FUNCS,
    info: () => docNode({ title: signature(fn).text, description: fn.description }),
    apply: (view, c, from, to) => {
      snippet(`${fn.name}(\${})`)(view, c, from, to)
    },
  }))
}

/** Attributes of a resource/data schema (computed ones included: they can be read). */
function schemaAttrOptions(block: SchemaBlock, owner: string, docsUrl: string | undefined, withId: boolean): Completion[] {
  const out: Completion[] = schemaItems(block).map((it) => ({
    label: it.name,
    type: it.kind === "block" ? "type" : "property",
    detail: it.kind === "block" ? `block · ${it.nesting}` : typeName(it.type),
    info: () => docNode({ title: `${owner}.${it.name}`, badges: [itemDetail(it)], description: it.description, docsUrl }),
    boost: it.name === "arn" || it.name === "id" || it.name === "name" ? 5 : 0,
  }))
  if (withId && !block.attributes.some((a) => a.name === "id")) out.push({ label: "id", type: "property", detail: "string", boost: 5 })
  return out
}

/** Follows segments through a schema block (nested blocks and object attributes). */
function navigate(block: SchemaBlock, segs: string[]): { block?: SchemaBlock; keys?: Record<string, unknown> } | null {
  let cur: SchemaBlock | undefined = block
  let keys: Record<string, unknown> | null = null
  for (const s of segs) {
    if (s === "[]") continue
    if (cur) {
      const nb: { block: SchemaBlock } | undefined = cur.blocks.find((b) => b.name === s)
      if (nb) {
        cur = nb.block
        continue
      }
      const a = cur.attributes.find((x) => x.name === s)
      keys = a ? objectKeys(a.type) : null
      if (!keys) return null
      cur = undefined
      continue
    }
    const next: unknown = keys?.[s]
    keys = next !== undefined ? objectKeys(next) : null
    if (!keys) return null
  }
  return cur ? { block: cur } : keys ? { keys } : null
}

async function referenceOptions(ctx: ValueContext, env: BodyEnv): Promise<Completion[] | null> {
  const [root, ...rest] = ctx.chain
  const m = env.module
  const top = ctx.path[0]
  switch (root) {
    case "var":
      if (rest.length === 0)
        return [...m.variables.values()].map((v) => ({
          label: v.name,
          type: "variable",
          detail: v.type ?? "any",
          info: () => docNode({ title: `var.${v.name}`, badges: [v.type ?? "any"], description: v.description, extra: v.default && `default = ${v.default}` }),
        }))
      return objectTypeKeys(m.variables.get(rest[0])?.type, rest.slice(1))
    case "local":
      return rest.length === 0
        ? [...m.locals.values()].map((l) => ({ label: l.name, type: "variable", detail: "local", info: () => docNode({ title: `local.${l.name}`, extra: l.value }) }))
        : null
    case "module": {
      if (rest.length === 0)
        return [...m.modules.values()].map((c) => ({ label: c.name, type: "namespace", detail: c.source ?? "module", apply: dotThenComplete(c.name) }))
      const call = m.modules.get(rest[0])
      const callee = call && rest.length === 1 ? env.symbols.callee(m.dir, call) : null
      return callee
        ? [...callee.outputs.values()].map((o) => ({
            label: o.name,
            type: "property",
            detail: "output",
            info: () => docNode({ title: `module.${rest[0]}.${o.name}`, description: o.description, extra: o.value }),
          }))
        : null
    }
    case "data": {
      if (rest.length === 0) {
        const types = new Set([...m.resources.values()].filter((r) => r.kind === "data").map((r) => r.type))
        return [...types].map((t) => ({ label: t, type: "interface", detail: "data source", apply: dotThenComplete(t) }))
      }
      if (rest.length === 1)
        return [...m.resources.values()]
          .filter((r) => r.kind === "data" && r.type === rest[0])
          .map((r) => ({ label: r.name, type: "variable", detail: `data.${r.type}` }))
      return resourceAttrs("data", rest[0], rest.slice(2))
    }
    case "each":
      return rest.length === 0
        ? [
            { label: "key", type: "property", detail: "string" },
            { label: "value", type: "property", detail: "element" },
          ]
        : null
    case "count":
      return rest.length === 0 ? [{ label: "index", type: "property", detail: "number" }] : null
    case "path":
      return rest.length === 0
        ? [
            { label: "module", type: "property", detail: "directory of this module" },
            { label: "root", type: "property", detail: "directory of the root module" },
            { label: "cwd", type: "property", detail: "working directory" },
          ]
        : null
    case "terraform":
      return rest.length === 0 ? [{ label: "workspace", type: "property", detail: "current workspace name" }] : null
    case "self":
      if (!top || (top.type !== "resource" && top.type !== "data")) return null
      return resourceAttrs(top.type, top.labels[0]?.text ?? "", rest)
  }
  for (const b of ctx.path)
    if (b.type === "dynamic" && iteratorName(b, env.text) === root && rest.length === 0)
      return [
        { label: "key", type: "property", detail: "key or index" },
        { label: "value", type: "property", detail: "element" },
      ]
  // TYPE / TYPE.NAME / TYPE.NAME.attr…
  if (rest.length === 0) {
    const names = [...m.resources.values()].filter((r) => r.kind === "resource" && r.type === root)
    if (names.length === 0) return null
    return names.map((r) => ({
      label: r.name,
      type: "variable",
      detail: r.repeat ? `${r.type} · ${r.repeat}` : r.type,
      info: () => docNode({ title: `${r.type}.${r.name}`, description: `Declared in ${r.file || "this file"}:${r.line}` }),
    }))
  }
  if (!m.resources.has(`${root}.${rest[0]}`)) return null
  return resourceAttrs("resource", root, rest.slice(1))
}

async function resourceAttrs(kind: "resource" | "data", type: string, segs: string[]): Promise<Completion[] | null> {
  const schema = await loadSchema(kind, type).catch(() => null)
  if (!schema) return null
  const owner = kind === "data" ? `data.${type}` : type
  const target = segs.length === 0 ? { block: schema.block } : navigate(schema.block, segs)
  if (!target) return null
  if (target.block) return schemaAttrOptions(target.block, owner, schema.docsUrl, segs.filter((s) => s !== "[]").length === 0)
  return Object.entries(target.keys!).map(([k, t]) => ({ label: k, type: "property", detail: typeName(t) }))
}

/** Keys of a variable declared with an object type: `object({ name = string, size = number })`. */
function objectTypeKeys(type: string | undefined, segs: string[]): Completion[] | null {
  if (!type || segs.length > 0) return null
  const m = /^\s*(?:list|set|map)?\(?\s*object\(\{([\s\S]*)\}\)/.exec(type)
  if (!m) return null
  const keys: Completion[] = []
  let depth = 0
  let start = 0
  const body = m[1]
  for (let i = 0; i <= body.length; i++) {
    const c = body[i]
    if (c === "(" || c === "{" || c === "[") depth++
    else if (c === ")" || c === "}" || c === "]") depth--
    if (i === body.length || (depth === 0 && (c === "," || c === "\n"))) {
      const kv = /^\s*([\w-]+)\s*=\s*([\s\S]+?)\s*$/.exec(body.slice(start, i))
      if (kv) keys.push({ label: kv[1], type: "property", detail: kv[2] })
      start = i + 1
    }
  }
  return keys
}

async function values(ctx: ValueContext, env: BodyEnv, explicit: boolean): Promise<CompletionResult | null> {
  const top = ctx.path[0]
  if (ctx.string) {
    return stringValues(ctx, await attrItem(ctx, env), env)
  }
  // Type constraints of variables.
  if (top?.type === "variable" && ctx.path.length === 1 && ctx.attr.name === "type" && ctx.chain.length === 0) {
    return {
      from: ctx.from,
      validFor: WORD,
      options: TYPE_CONSTRAINTS.map(([label, template]) => ({ label, type: "type", apply: snippet(template) })),
    }
  }
  if (ctx.chain.length > 0) {
    const options = await referenceOptions(ctx, env)
    return options ? { from: ctx.from, options, validFor: WORD } : null
  }
  if (!explicit && ctx.partial === "") return null
  const it = await attrItem(ctx, env)
  const options: Completion[] = []
  if (it?.enum?.length && it.typeText !== "string")
    for (const v of it.enum) options.push({ label: v, type: "enum", detail: it.name, boost: 20, section: S_REFS })
  else if (it?.enum?.length)
    for (const v of it.enum) options.push({ label: `"${v}"`, type: "enum", detail: it.name, boost: 20, section: S_REFS })
  if (it && typeName(it.type) === "bool" && !it.enum)
    for (const v of ["true", "false"]) options.push({ label: v, type: "keyword", boost: 20, section: S_REFS })
  options.push(...rootOptions(ctx, env), ...(await functionOptions()))
  return { from: ctx.from, options, validFor: WORD }
}

