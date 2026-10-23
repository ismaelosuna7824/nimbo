// Helm template intelligence: inside {{ … }} actions, completion of
// .Values paths (from the chart's values.yaml and values.schema.json),
// .Release/.Chart/.Capabilities/.Files/.Template members, named templates
// (define) for include/template, and the template functions; hover on
// functions, built-in objects and .Values paths (with the default value);
// go to definition of .Values paths (values.yaml) and named templates.
// `with`/`range` blocks move the dot like Helm does. Pure functions over text.

import type { Completion, CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import { keysAt, type JsonSchema } from "@/features/code-intel/yaml/schema"
import { parseYaml, type YamlNode } from "@/features/code-intel/yaml/tree"
import { BUILTIN_OBJECTS, TEMPLATE_FUNCTION_BY_NAME, TEMPLATE_FUNCTIONS, TEMPLATE_KEYWORDS, type BuiltinMember } from "./helm-functions"
import { commentAbove, previewNode } from "./helm-schema"

export const FUNCTIONS_DOCS = "https://helm.sh/docs/chart_template_guide/function_list/"
export const OBJECTS_DOCS = "https://helm.sh/docs/chart_template_guide/builtin_objects/"
export const VALUES_DOCS = "https://helm.sh/docs/chart_template_guide/values_files/"
export const NAMED_TEMPLATES_DOCS = "https://helm.sh/docs/chart_template_guide/named_templates/"

const S_VALUES: CompletionSection = { name: "Values", rank: 0 }
const S_OBJECTS: CompletionSection = { name: "Objects", rank: 0 }
const S_TEMPLATES: CompletionSection = { name: "Named templates", rank: 0 }
const S_FUNCTIONS: CompletionSection = { name: "Functions", rank: 1 }
const S_KEYWORDS: CompletionSection = { name: "Actions", rank: 2 }

export interface ChartEnv {
  /** Workflow path of the chart's values.yaml. */
  valuesPath: string
  valuesText?: string
  /** Schema of the values (inferred from values.yaml, merged with values.schema.json). */
  valuesSchema: JsonSchema
  /** Every template of the chart (templates/**) by workflow path, for named templates. */
  templates: Record<string, string>
}

export interface NamedTemplate {
  name: string
  file: string
  /** 1-based line of the define. */
  line: number
  /** Offset of the name in its file. */
  from: number
  /** The {{/* … *\/}} comment right above it. */
  description?: string
}

const DEFINE_RE = /\{\{-?\s*(?:define|block)\s+"([^"]+)"/g

/** Named templates (define/block) of the chart's templates. */
export function namedTemplates(templates: Record<string, string>): NamedTemplate[] {
  const out: NamedTemplate[] = []
  for (const [file, text] of Object.entries(templates)) {
    DEFINE_RE.lastIndex = 0
    for (let m = DEFINE_RE.exec(text); m; m = DEFINE_RE.exec(text)) {
      const before = text.slice(0, m.index)
      const comment = /\{\{-?\s*\/\*([\s\S]*?)\*\/\s*-?\}\}\s*$/.exec(before)?.[1]
      out.push({
        name: m[1],
        file,
        line: before.split("\n").length,
        from: m.index + m[0].length - m[1].length - 1,
        description: comment?.trim() || undefined,
      })
    }
  }
  return out
}

/** The template action ({{ … }}) holding `pos`: the range of its body; null outside actions and in comments. */
export function actionAt(text: string, pos: number): { from: number; to: number } | null {
  const open = pos >= 2 ? text.lastIndexOf("{{", pos - 2) : -1
  if (open < 0) return null
  const close = text.lastIndexOf("}}", pos - 2)
  if (close > open) return null
  let from = open + 2
  if (text[from] === "-" && /\s/.test(text[from + 1] ?? "")) from++
  if (/^\s*\/\*/.test(text.slice(from, pos + 2))) return null
  const end = text.indexOf("}}", pos)
  return { from, to: end < 0 ? text.length : end }
}

/** A field chain ("", "$", "Values", "image") resolved against the dot: the path from the root context; null when unknown. */
type ScopePath = string[] | null

/** The path of an expression's leading field chain (".Values.x", "$.Values.x", ".x" relative to dot). */
function chainPath(expr: string, dot: ScopePath): ScopePath {
  const m = /^(\$?[\w]*)((?:\.[\w]+)*)/.exec(expr.trim())
  if (!m || (!m[1] && !m[2])) return null
  const fields = m[2] ? m[2].slice(1).split(".") : []
  if (m[1] === "$") return fields
  if (m[1] === "") return dot ? [...dot, ...fields] : null
  return null
}

/** What `.` is at `pos`: the root context, a path under it (with/range), or unknown (null). */
export function dotAt(text: string, pos: number): ScopePath {
  const stack: { dot: ScopePath; outer: ScopePath }[] = []
  let dot: ScopePath = []
  const re = /\{\{-?\s*([\s\S]*?)\s*-?\}\}/g
  // Actions before the one holding the cursor (its "{{" excluded: an unclosed one would swallow the text after it).
  const current = actionAt(text, pos)
  const limit = current ? text.lastIndexOf("{{", current.from - 2) : pos
  for (let m = re.exec(text); m && m.index < limit; m = re.exec(text)) {
    const body = m[1]
    if (body.startsWith("/*")) continue
    const word = /^\w+/.exec(body)?.[0]
    const rest = body.slice(word?.length ?? 0).trim()
    switch (word) {
      case "if":
        stack.push({ dot, outer: dot })
        break
      case "with":
        stack.push({ dot: chainPath(rest, dot), outer: dot })
        dot = stack[stack.length - 1].dot
        break
      case "range": {
        const expr = rest.includes(":=") ? rest.slice(rest.indexOf(":=") + 2) : rest
        const p = chainPath(expr, dot)
        stack.push({ dot: p ? [...p, "[]"] : null, outer: dot })
        dot = stack[stack.length - 1].dot
        break
      }
      case "define":
      case "block":
        // Named templates are usually called with the root context (include "x" .).
        stack.push({ dot: [], outer: dot })
        dot = []
        break
      case "else": {
        const top = stack[stack.length - 1]
        if (!top) break
        top.dot = /^with\b/.test(rest) ? chainPath(rest.slice(4), top.outer) : top.outer
        dot = top.dot
        break
      }
      case "end": {
        const top = stack.pop()
        if (top) dot = top.outer
        break
      }
    }
  }
  return dot
}

function memberAt(path: readonly string[]): BuiltinMember[] | null {
  let list: BuiltinMember[] | undefined = BUILTIN_OBJECTS
  for (const seg of path) {
    list = list?.find((b) => b.name === seg)?.members
    if (!list) return null
  }
  return list ?? null
}

/** Field chain before `pos` ("$.Values.image.ta"), with its start. */
function chainBefore(text: string, pos: number): { chain: string; from: number } {
  let from = pos
  while (from > 0 && /[\w.$]/.test(text[from - 1])) from--
  return { chain: text.slice(from, pos), from }
}

/** Whether `pos` is inside a string literal of the action starting at `from`; the string's start when so. */
function stringStart(text: string, from: number, pos: number): number | null {
  let start: number | null = null
  for (let i = from; i < pos; i++) {
    const c = text[i]
    if (start !== null) {
      if (c === "\\") i++
      else if (c === '"') start = null
    } else if (c === '"') start = i + 1
  }
  return start
}

function valueKeyOptions(env: ChartEnv, path: string[], from: number): CompletionResult | null {
  const keys = keysAt(env.valuesSchema, path)
  if (keys.length === 0) return null
  return {
    from,
    options: keys.map<Completion>((k) => ({
      label: k.name,
      type: k.schema.properties || k.schema.type === "object" ? "namespace" : "property",
      section: S_VALUES,
      info: k.description,
    })),
    validFor: /^\w*$/,
  }
}

/** Completions at `pos` of a Helm template; null outside actions or when nothing applies. */
export function templateCompletions(text: string, pos: number, explicit: boolean, env: ChartEnv): CompletionResult | null {
  const action = actionAt(text, pos)
  if (!action) return null
  const str = stringStart(text, action.from, pos)
  if (str !== null) {
    if (!/\b(include|template)\s+$/.test(text.slice(action.from, str - 1))) return null
    return {
      from: str,
      options: namedTemplates(env.templates).map((t) => ({
        label: t.name,
        type: "function",
        section: S_TEMPLATES,
        detail: t.file.slice(t.file.lastIndexOf("/") + 1),
        info: t.description,
      })),
      validFor: /^[\w.-]*$/,
    }
  }
  const { chain, from } = chainBefore(text, pos)
  if (chain.includes(".")) {
    const parts = chain.split(".")
    const last = parts[parts.length - 1]
    const head = parts[0]
    const base = head === "$" ? [] : head === "" ? dotAt(text, pos) : null
    if (!base) return null
    const path = [...base, ...parts.slice(1, -1)]
    const at = pos - last.length
    if (path[0] === "Values") return valueKeyOptions(env, path.slice(1), at)
    const members = memberAt(path)
    if (!members) return null
    return {
      from: at,
      options: members.map((b) => ({ label: b.name, type: b.members || b.name === "Values" ? "namespace" : "property", section: S_OBJECTS, info: b.description })),
      validFor: /^\w*$/,
    }
  }
  if (chain.startsWith("$")) return null
  if (!explicit && chain === "") return null
  const options: Completion[] = [
    ...TEMPLATE_FUNCTIONS.map<Completion>((f) => ({ label: f.name, type: "function", section: S_FUNCTIONS, detail: f.signature, info: f.description })),
    ...TEMPLATE_KEYWORDS.map<Completion>((k) => ({ label: k.name, type: "keyword", section: S_KEYWORDS, info: k.description })),
  ]
  return { from, options, validFor: /^\w*$/ }
}

/** A values entry: its key line and node, following a path ("[]" = first list item). */
export function valuesEntry(valuesText: string, path: readonly string[]): { line: number; node: YamlNode | null; found: number } | null {
  const root = parseYaml(valuesText)[0]?.root ?? null
  let node: YamlNode | null = root
  let line = 0
  let found = 0
  for (const seg of path) {
    if (seg === "[]") {
      if (node?.kind !== "seq" || !node.items[0]) break
      node = node.items[0]
      found++
      continue
    }
    if (node?.kind !== "map") break
    const e = node.entries.find((x) => x.key === seg)
    if (!e) break
    line = e.line
    node = e.value
    found++
  }
  return found ? { line, node, found } : null
}

export interface TemplateHover {
  from: number
  to: number
  title: string
  badges?: string[]
  description?: string
  /** Code shown under the description (a default value). */
  extra?: string
  docsUrl?: string
  docsLabel?: string
}

/** The field chain around `pos` and the index of the segment holding it. */
function chainAround(text: string, pos: number): { parts: string[]; index: number; from: number; to: number; segFrom: number; segTo: number } | null {
  let from = pos
  while (from > 0 && /[\w.$]/.test(text[from - 1])) from--
  let to = pos
  while (to < text.length && /[\w$]/.test(text[to])) to++
  if (to === from) return null
  const chain = text.slice(from, to)
  const parts = chain.split(".")
  let off = from
  for (let i = 0; i < parts.length; i++) {
    const end = off + parts[i].length
    if (pos >= off && pos <= end && (parts[i] || i === parts.length - 1)) return { parts, index: i, from, to, segFrom: off, segTo: end }
    off = end + 1
  }
  return null
}

/** Hover of a template: functions, actions, built-in objects, .Values paths and named templates. */
export function templateHover(text: string, pos: number, env: ChartEnv): TemplateHover | null {
  const action = actionAt(text, pos)
  if (!action) return null
  const str = stringStart(text, action.from, pos)
  if (str !== null) {
    const name = /^[^"]*/.exec(text.slice(str))?.[0] ?? ""
    const t = /\b(include|template)\s+$/.test(text.slice(action.from, str - 1)) ? namedTemplates(env.templates).find((x) => x.name === name) : undefined
    if (!t) return null
    return { from: str, to: str + name.length, title: `define "${t.name}"`, badges: [`${t.file}:${t.line}`], description: t.description, docsUrl: NAMED_TEMPLATES_DOCS, docsLabel: "Named templates ↗" }
  }
  const c = chainAround(text, pos)
  if (!c) return null
  const word = c.parts[c.index]
  if (c.parts.length === 1) {
    const f = TEMPLATE_FUNCTION_BY_NAME[word]
    if (f) return { from: c.segFrom, to: c.segTo, title: f.signature, badges: [f.origin === "sprig" ? "Sprig" : f.origin === "go" ? "Go template" : "Helm"], description: f.description, docsUrl: FUNCTIONS_DOCS, docsLabel: "Template function list ↗" }
    const k = TEMPLATE_KEYWORDS.find((x) => x.name === word)
    if (k) return { from: c.segFrom, to: c.segTo, title: k.name, badges: ["action"], description: k.description }
    return null
  }
  const head = c.parts[0]
  const base = head === "$" ? [] : head === "" ? dotAt(text, pos) : null
  if (!base || c.index === 0) return null
  const path = [...base, ...c.parts.slice(1, c.index + 1)]
  const title = `.${path.filter((p) => p !== "[]").join(".")}`
  if (path[0] === "Values") {
    if (path.length === 1) return { from: c.segFrom, to: c.segTo, title: ".Values", description: BUILTIN_OBJECTS[0].description, docsUrl: VALUES_DOCS, docsLabel: "Values files ↗" }
    const e = env.valuesText ? valuesEntry(env.valuesText, path.slice(1)) : null
    const complete = e && e.found === path.length - 1
    const lines = env.valuesText?.split("\n") ?? []
    return {
      from: c.segFrom,
      to: c.segTo,
      title,
      badges: complete ? [`${env.valuesPath}:${e.line}`] : ["not in values.yaml"],
      description: complete && e.line ? commentAbove(lines, e.line) : undefined,
      extra: complete ? previewNode(env.valuesText!, e.node) : undefined,
    }
  }
  const parent = memberAt(path.slice(0, -1))
  const member = parent?.find((b) => b.name === path[path.length - 1])
  if (!member) return null
  return { from: c.segFrom, to: c.segTo, title, description: member.description, docsUrl: OBJECTS_DOCS, docsLabel: "Built-in objects ↗" }
}

/** Where the name at `pos` of a template is declared: a .Values path in values.yaml, a named template's define. */
export function templateDefinition(text: string, pos: number, env: ChartEnv): { file: string; line: number } | null {
  const action = actionAt(text, pos)
  if (!action) return null
  const str = stringStart(text, action.from, pos)
  if (str !== null) {
    if (!/\b(include|template)\s+$/.test(text.slice(action.from, str - 1))) return null
    const name = /^[^"]*/.exec(text.slice(str))?.[0] ?? ""
    const t = namedTemplates(env.templates).find((x) => x.name === name)
    return t ? { file: t.file, line: t.line } : null
  }
  const c = chainAround(text, pos)
  if (!c || c.parts.length < 2 || c.index === 0) return null
  const head = c.parts[0]
  const base = head === "$" ? [] : head === "" ? dotAt(text, pos) : null
  if (!base) return null
  const path = [...base, ...c.parts.slice(1, c.index + 1)]
  if (path[0] !== "Values" || path.length < 2 || !env.valuesText) return null
  const e = valuesEntry(env.valuesText, path.slice(1))
  return e && e.line ? { file: env.valuesPath, line: e.line } : null
}
