// Text edits of block YAML that keep the rest of the file as written
// (comments, quoting, order): set a scalar, merge keys into a mapping at its
// own indentation, turn an empty `key:` or `{}` into a block. Each returns
// the changes to apply to the original text (offsets in it).

import { parseYaml, type YamlDocument, type YamlMap, type YamlNode } from "@/features/code-intel/yaml/tree"

export interface TextChange {
  from: number
  to: number
  insert: string
}

/** A YAML value to write: a plain scalar (already formatted), a list of scalars or a nested mapping. */
export type YamlValue = string | string[] | { [key: string]: YamlValue }

/** One step of an object path: a key, or a list index / an item with name = value. */
export type PathStep = string | number | { name: string }

/** "spec.template.spec.containers[0].image" / "containers[name=web]" → steps. */
export function parseFieldPath(path: string): PathStep[] {
  const out: PathStep[] = []
  for (const m of path.matchAll(/([^.[\]]+)|\[(\d+)\]|\[name=([^\]]+)\]/g)) {
    if (m[1] !== undefined) out.push(m[1])
    else if (m[2] !== undefined) out.push(Number(m[2]))
    else out.push({ name: m[3] })
  }
  return out
}

export function lineStartOf(text: string, pos: number): number {
  return text.lastIndexOf("\n", pos - 1) + 1
}

function lineEndOf(text: string, pos: number): number {
  const e = text.indexOf("\n", pos)
  return e < 0 ? text.length : e
}

/** Column of an offset. */
export function columnOf(text: string, pos: number): number {
  return pos - lineStartOf(text, pos)
}

/** 1-based line → offset of its start. */
export function offsetOfLine(text: string, line: number): number {
  let off = 0
  for (let n = 1; n < line; n++) {
    const nl = text.indexOf("\n", off)
    if (nl < 0) return text.length
    off = nl + 1
  }
  return off
}

/** The document of a stream holding 1-based `line` (else the next one, else the first). */
export function documentOfLine(text: string, line: number): YamlDocument | undefined {
  const docs = parseYaml(text)
  const off = offsetOfLine(text, line)
  return docs.find((d) => d.from <= off && off <= Math.max(d.to, d.from)) ?? docs.find((d) => d.from > off) ?? docs[0]
}

/** The node at `steps` under `node`, or undefined. */
export function nodeAtPath(node: YamlNode | null | undefined, steps: readonly PathStep[]): YamlNode | null | undefined {
  let cur: YamlNode | null | undefined = node
  for (const s of steps) {
    if (!cur) return undefined
    if (typeof s === "string") {
      if (cur.kind !== "map") return undefined
      const e = cur.entries.find((x) => x.key === s)
      if (!e) return undefined
      cur = e.value
    } else if (typeof s === "number") {
      if (cur.kind !== "seq") return undefined
      cur = cur.items[s]
    } else {
      if (cur.kind !== "seq") return undefined
      cur = cur.items.find((it) => it.kind === "map" && it.entries.some((e) => e.key === "name" && e.value?.kind === "scalar" && e.value.value === s.name))
    }
  }
  return cur
}

/** A scalar as YAML: plain when safe, double-quoted otherwise. */
export function yamlScalar(value: string): string {
  if (value === "" || /^[\s]|[\s]$|^[-?:,[\]{}#&*!|>'"%@`]|: | #|^(true|false|yes|no|on|off|null|~)$/i.test(value) || /^[-+]?(\d|\.\d)/.test(value)) return JSON.stringify(value)
  return value
}

/** Lines of `value` under a key at column `indent` (nested keys +2, list items +2). */
export function renderEntries(value: { [key: string]: YamlValue }, indent: number): string[] {
  const pad = " ".repeat(indent)
  const lines: string[] = []
  for (const [k, v] of Object.entries(value)) {
    if (typeof v === "string") lines.push(`${pad}${k}: ${v}`)
    else if (Array.isArray(v)) {
      lines.push(`${pad}${k}:`)
      for (const item of v) lines.push(`${pad}  - ${item}`)
    } else {
      lines.push(`${pad}${k}:`)
      lines.push(...renderEntries(v, indent + 2))
    }
  }
  return lines
}

/** Replaces a scalar node's text. */
export function setScalar(node: YamlNode, value: string): TextChange {
  return { from: node.from, to: node.to, insert: value }
}

/** The end of the last line a node spans (where lines after it go). */
function endOfNode(text: string, node: YamlNode): number {
  return lineEndOf(text, Math.max(node.from, node.to - 1))
}

/**
 * Merges `value` into the block mapping `map`: missing keys are appended at its indentation, nested mappings merged
 * recursively, existing scalars replaced only when `overwrite` (the fix sets them); lists get missing items.
 */
export function mergeIntoMap(text: string, map: YamlMap, value: { [key: string]: YamlValue }, overwrite: boolean): TextChange[] {
  const changes: TextChange[] = []
  const indent = columnOf(text, map.from)
  const missing: { [key: string]: YamlValue } = {}
  for (const [k, v] of Object.entries(value)) {
    const e = map.entries.find((x) => x.key === k)
    if (!e) {
      missing[k] = v
      continue
    }
    changes.push(...setEntryValue(text, e.keyFrom, indent, e.value, v, overwrite))
  }
  if (Object.keys(missing).length) {
    // Flow mappings ({a: 1}) are rewritten as blocks; block mappings get lines after their last entry.
    if (text[map.from] === "{") changes.push(rewriteFlow(text, map, missing))
    else {
      const at = endOfNode(text, map)
      const insert = `\n${renderEntries(missing, indent).join("\n")}`
      // The last entry's nested additions end at the same place: one insertion keeps them in order.
      const same = changes.find((c) => c.from === at && c.to === at)
      if (same) same.insert += insert
      else changes.push({ from: at, to: at, insert })
    }
  }
  return changes
}

/** Writes `value` as the value of the key at `keyFrom` (column `indent`), whose current value is `cur`. */
function setEntryValue(text: string, keyFrom: number, indent: number, cur: YamlNode | null, value: YamlValue, overwrite: boolean): TextChange[] {
  if (typeof value === "string") {
    if (!cur) return [{ from: lineEndOf(text, keyFrom), to: lineEndOf(text, keyFrom), insert: ` ${value}` }]
    if (cur.kind === "scalar" && !cur.block && (overwrite || cur.value === "") && cur.value !== value.replace(/^"|"$/g, "")) return [setScalar(cur, value)]
    return []
  }
  if (Array.isArray(value)) {
    if (cur?.kind === "seq" && cur.items.length > 0) {
      const have = new Set(cur.items.map((it) => (it.kind === "scalar" ? it.value : "")))
      const add = value.filter((v) => !have.has(v.replace(/^"|"$/g, "")))
      if (!add.length) return []
      if (text[cur.from] === "[") {
        const inner = text.slice(cur.from + 1, Math.max(cur.from + 1, cur.to - 1)).trim()
        return [{ from: cur.from, to: cur.to, insert: `[${[inner, ...add].filter(Boolean).join(", ")}]` }]
      }
      const col = columnOf(text, cur.from)
      return [{ from: endOfNode(text, cur), to: endOfNode(text, cur), insert: add.map((v) => `\n${" ".repeat(col)}- ${v}`).join("") }]
    }
    if (cur && !(cur.kind === "seq" || (cur.kind === "scalar" && /^(|null|~)$/.test(cur.value)))) return []
    return replaceValue(text, keyFrom, cur, value.map((v) => `${" ".repeat(indent + 2)}- ${v}`))
  }
  if (cur?.kind === "map") return mergeIntoMap(text, cur, value, overwrite)
  // `key:` with nothing, `key: null`, `key: []`: the block replaces it.
  if (!cur || (cur.kind === "scalar" && /^(|null|~)$/.test(cur.value)) || (cur.kind === "seq" && cur.items.length === 0)) return replaceValue(text, keyFrom, cur, renderEntries(value, indent + 2))
  return []
}

/** Replaces the value of "key:" (inline or on the lines below) with block lines below the key. */
function replaceValue(text: string, keyFrom: number, cur: YamlNode | null, lines: string[]): TextChange[] {
  const keyLineEnd = lineEndOf(text, keyFrom)
  if (!cur) return [{ from: keyLineEnd, to: keyLineEnd, insert: `\n${lines.join("\n")}` }]
  if (cur.from > keyLineEnd) return [{ from: keyLineEnd, to: endOfNode(text, cur), insert: `\n${lines.join("\n")}` }]
  // Drop the inline value (and the blanks before it): "key: []" → "key:\n  …".
  const colon = text.indexOf(":", keyFrom)
  let from = cur.from
  while (from > colon + 1 && /[ \t]/.test(text[from - 1])) from--
  return [{ from, to: cur.to, insert: `\n${lines.join("\n")}` }]
}

/** A flow mapping ("key: {}", "key: {a: 1}", "- {}") rewritten as a block with its entries plus `extra`. */
function rewriteFlow(text: string, map: YamlMap, extra: { [key: string]: YamlValue }): TextChange {
  const lineStart = lineStartOf(text, map.from)
  const before = text.slice(lineStart, map.from)
  const keyCol = /^[ \t]*(?:-[ \t]+)*/.exec(before)![0].length
  // A list item "- {}": its keys start where the braces were; a key's value: one level below the key.
  const item = !before.slice(keyCol).includes(":")
  const col = item ? map.from - lineStart : keyCol + 2
  const own = map.entries.map((e) => `${" ".repeat(col)}${e.key}: ${e.value ? text.slice(e.value.from, e.value.to) : ""}`.trimEnd())
  const lines = [...own, ...renderEntries(extra, col)]
  if (item) return { from: map.from, to: map.to, insert: lines.map((l, i) => (i === 0 ? l.trimStart() : l)).join("\n") }
  const colon = text.lastIndexOf(":", map.from)
  let from = map.from
  while (from > colon + 1 && /[ \t]/.test(text[from - 1])) from--
  return { from, to: map.to, insert: `\n${lines.join("\n")}` }
}

/**
 * Ensures the mapping at `steps` under `root` exists and merges `value` into it: the deepest existing mapping gets
 * the missing part of the path as nested keys.
 */
export function mergeAtPath(text: string, root: YamlNode | null, steps: readonly string[], value: { [key: string]: YamlValue }, overwrite: boolean): TextChange[] {
  if (root?.kind !== "map") return []
  let cur: YamlMap = root
  for (let i = 0; i < steps.length; i++) {
    let nested: { [key: string]: YamlValue } = value
    for (let j = steps.length - 1; j > i; j--) nested = { [steps[j]]: nested }
    const e = cur.entries.find((x) => x.key === steps[i])
    // A missing key: the rest of the path and the value nest under a new one.
    if (!e) return mergeIntoMap(text, cur, { [steps[i]]: nested }, overwrite)
    if (e.value?.kind !== "map") return setEntryValue(text, e.keyFrom, columnOf(text, e.keyFrom), e.value, nested, overwrite)
    cur = e.value
  }
  return mergeIntoMap(text, cur, value, overwrite)
}

/** Applies changes (non-overlapping) to a text: for tests and previews. */
export function applyChanges(text: string, changes: readonly TextChange[]): string {
  let out = text
  for (const c of [...changes].sort((a, b) => b.from - a.from)) out = out.slice(0, c.from) + c.insert + out.slice(c.to)
  return out
}
