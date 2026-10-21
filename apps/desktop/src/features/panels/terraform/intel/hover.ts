// Hover docs: resource/data types, arguments and blocks, functions and
// references (variables, locals, modules, resources and their attributes).

import type { SchemaBlock } from "@/lib/api/types"
import { bodySpec, type BodyEnv, type ItemSpec } from "./blocks"
import { loadFunctions, loadSchema, typeIndex } from "./catalog"
import type { CompletionEnv } from "./completion"
import { referenceAt } from "./context"
import { docNode, signature, typeName } from "./format"
import { resolveReference } from "./resolve"
import { blockAt, blockPath, inComment, parseCached } from "./scan"
import { dirOf } from "./symbols"

export interface Hover {
  from: number
  to: number
  dom: HTMLElement
}

const KEYWORDS: Record<string, string> = {
  resource: "A managed infrastructure object: Terraform creates, updates and destroys it.",
  data: "A data source: reads an existing object without managing it.",
  module: "Calls another module (a directory of Terraform files) with input variables.",
  variable: "An input variable of this module.",
  output: "A value this module exposes to its caller or the CLI.",
  locals: "Named values computed inside this module.",
  provider: "Configuration of a provider (region, credentials, aliases).",
  terraform: "Settings: required Terraform and provider versions, backend.",
  moved: "Records that an object moved to a new address, so Terraform doesn't recreate it.",
  import: "Brings an existing cloud object under Terraform management.",
  check: "Assertions checked on every plan and apply, without blocking them.",
  removed: "Stops managing an object, optionally without destroying it.",
}

const BUILTINS: Record<string, string> = {
  "each.key": "Key (or set element) of the current for_each instance.",
  "each.value": "Value of the current for_each instance.",
  "count.index": "Index (from 0) of the current count instance.",
  "path.module": "Filesystem path of the module where the expression is.",
  "path.root": "Filesystem path of the root module.",
  "path.cwd": "Working directory Terraform runs in.",
  "terraform.workspace": "Name of the current workspace.",
}

function itemDoc(it: ItemSpec, owner: string, docsUrl?: string): HTMLElement {
  const kind = it.kind === "block" ? `block${it.nesting ? ` · ${it.nesting}` : ""}` : (it.typeText ?? typeName(it.type))
  const flags = [kind, it.required ? "required" : it.computed ? "read-only" : "optional"]
  if (it.deprecated) flags.push("deprecated")
  if (it.sensitive) flags.push("sensitive")
  return docNode({
    title: `${owner} › ${it.name}`,
    badges: flags,
    description: it.description,
    docsUrl,
    extra: it.enum?.length ? `Allowed: ${it.enum.join(", ")}` : undefined,
  })
}

/** Finds an attribute or block along segs in a schema block. */
function schemaMember(block: SchemaBlock, segs: string[]): ItemSpec | null {
  let cur: SchemaBlock | undefined = block
  let found: ItemSpec | null = null
  for (const s of segs) {
    if (s === "[]") continue
    if (!cur) return found
    const nb: { name: string; nesting: string; block: SchemaBlock } | undefined = cur.blocks.find((b) => b.name === s)
    if (nb) {
      found = { name: s, kind: "block", nesting: nb.nesting }
      cur = nb.block
      continue
    }
    const a = cur.attributes.find((x) => x.name === s)
    if (!a) return s === "id" ? { name: "id", kind: "attr", typeText: "string", computed: true, description: "ID of the object." } : null
    found = { name: s, kind: "attr", type: a.type, required: a.required, computed: a.computed && !a.optional, description: a.description, sensitive: a.sensitive, deprecated: a.deprecated }
    cur = undefined
  }
  return found
}

/** Hover content at pos, or null. */
export async function hoverAt(text: string, pos: number, env: CompletionEnv): Promise<Hover | null> {
  const p = parseCached(text)
  if (inComment(p, pos)) return null
  const body = blockAt(p.root, pos)
  const module = env.symbols.module(dirOf(env.path))
  const benv: BodyEnv = { symbols: env.symbols, module, text }

  for (const b of body.blocks) {
    if (pos >= b.typeFrom && pos <= b.typeTo) {
      if (body === p.root) {
        const d = KEYWORDS[b.type]
        return d ? { from: b.typeFrom, to: b.typeTo, dom: docNode({ title: b.type, description: d }) } : null
      }
      const spec = await bodySpec(blockPath(body), benv)
      const it = spec?.items.find((i) => i.name === b.type)
      return it && spec ? { from: b.typeFrom, to: b.typeTo, dom: itemDoc(it, spec.owner, spec.docsUrl) } : null
    }
    const l0 = b.labels[0]
    if (body === p.root && l0 && pos >= l0.from && pos <= l0.to) {
      if (b.type === "resource" || b.type === "data") {
        const kind = b.type
        const schema = await loadSchema(kind, l0.text).catch(() => null)
        const info = typeIndex()?.byKey.get(`${kind}/${l0.text}`)
        if (!schema) return null
        const badges = [kind === "data" ? "data source" : "resource", schema.cloud ? schema.cloud.toUpperCase() : "utility"]
        if (info?.local) badges.push("runs locally")
        return {
          from: l0.from,
          to: l0.to,
          dom: docNode({ title: `${l0.text} — ${schema.label}`, badges, description: schema.description, docsUrl: schema.docsUrl }),
        }
      }
      if (b.type === "provider") {
        const schema = await loadSchema("provider", l0.text).catch(() => null)
        return schema
          ? { from: l0.from, to: l0.to, dom: docNode({ title: `provider ${l0.text}`, badges: [schema.provider], description: schema.description, docsUrl: schema.docsUrl }) }
          : null
      }
    }
  }
  for (const a of body.attrs) {
    if (pos >= a.nameFrom && pos <= a.nameTo && body !== p.root) {
      const spec = await bodySpec(blockPath(body), benv)
      const it = spec?.items.find((i) => i.name === a.name)
      return it && spec ? { from: a.nameFrom, to: a.nameTo, dom: itemDoc(it, spec.owner, spec.docsUrl) } : null
    }
  }

  const ref = referenceAt(text, pos)
  if (!ref) return null
  const after = text.slice(ref.to).match(/^\s*\(/)
  if (after && ref.segs.length === 1) {
    const fn = (await loadFunctions().catch(() => null))?.get(ref.segs[0])
    return fn ? { from: ref.from, to: ref.to, dom: docNode({ title: signature(fn).text, description: fn.description }) } : null
  }
  const key = ref.segs.slice(0, 2).join(".")
  if (BUILTINS[key]) return { from: ref.from, to: ref.to, dom: docNode({ title: key, description: BUILTINS[key] }) }

  const r = resolveReference(ref.segs, ref.index, module, env.symbols)
  if (!r) return null
  const where = (file: string, line: number) => (file ? `${file}:${line}` : "downloaded module")
  switch (r.kind) {
    case "variable": {
      const v = r.decl
      return {
        from: ref.from,
        to: ref.to,
        dom: docNode({
          title: `var.${v.name}`,
          badges: [v.type ?? "any", v.default === undefined ? "required" : "optional", ...(v.sensitive ? ["sensitive"] : [])],
          description: v.description ?? `Declared in ${where(v.file, v.line)}`,
          extra: v.default !== undefined ? `default = ${v.default}` : undefined,
        }),
      }
    }
    case "local":
      return { from: ref.from, to: ref.to, dom: docNode({ title: `local.${r.decl.name}`, description: `Declared in ${where(r.decl.file, r.decl.line)}`, extra: r.decl.value }) }
    case "module":
      return {
        from: ref.from,
        to: ref.to,
        dom: docNode({
          title: `module.${r.decl.name}`,
          badges: [r.decl.source ?? "module", ...(r.decl.version ? [r.decl.version] : [])],
          description: `Declared in ${where(r.decl.file, r.decl.line)}`,
        }),
      }
    case "output":
      return {
        from: ref.from,
        to: ref.to,
        dom: docNode({ title: `module.${r.call.name}.${r.decl.name}`, badges: ["output"], description: r.decl.description, extra: r.decl.value }),
      }
    case "resource": {
      const d = r.decl
      const schema = await loadSchema(d.kind, d.type).catch(() => null)
      const addr = d.kind === "data" ? `data.${d.type}.${d.name}` : `${d.type}.${d.name}`
      if (r.attr.length === 0 || !schema) {
        return {
          from: ref.from,
          to: ref.to,
          dom: docNode({
            title: schema ? `${addr} — ${schema.label}` : addr,
            badges: [d.kind === "data" ? "data source" : "resource", ...(d.repeat ? [d.repeat] : [])],
            description: `${schema?.description ? `${schema.description} ` : ""}Declared in ${where(d.file, d.line)}.`,
            docsUrl: schema?.docsUrl,
          }),
        }
      }
      const it = schemaMember(schema.block, r.attr)
      return it ? { from: ref.from, to: ref.to, dom: itemDoc(it, addr, schema.docsUrl) } : null
    }
  }
}
