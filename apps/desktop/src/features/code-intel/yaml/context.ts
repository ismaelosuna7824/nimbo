// A tolerant, line-based reading of block-style YAML (compose files,
// Kubernetes manifests, Helm values): the key path at the cursor and whether
// it waits for a key or a value. Flow collections ({…}, […]) are not
// entered; block scalars (| and >) are skipped. Good enough for completion
// and hover while the file is being typed and is not valid YAML yet.
// Multi-document streams reset the path at each "---".

/** One step of a path: a mapping key or "[]" for a sequence item. */
export type PathSegment = string

export interface KeyContext {
  kind: "key"
  /** Path of the mapping the key belongs to. */
  path: PathSegment[]
  /** Text typed so far (without quotes). */
  prefix: string
  /** Document offset where the prefix starts. */
  from: number
  /** Column of the key (its indentation). */
  indent: number
}

export interface ValueContext {
  kind: "value"
  /** Path of the value: the mapping's path plus the key, or a sequence's path plus "[]". */
  path: PathSegment[]
  /** The key the value belongs to ("" for a sequence item). */
  key: string
  prefix: string
  from: number
  indent: number
}

export type YamlContext = KeyContext | ValueContext | { kind: "none" }

interface Frame {
  indent: number
  segment: PathSegment
  seq: boolean
  /** A key without an inline value: its content follows on the next lines. */
  open: boolean
}

export const KEY_RE = /^("(?:[^"\\]|\\.)*"|'(?:[^']|'')*'|[^\s#'"{[][^:#]*?)[ \t]*:(?=[ \t]|$)/

export function unquote(s: string) {
  const t = s.trim()
  if ((t.startsWith('"') && t.endsWith('"')) || (t.startsWith("'") && t.endsWith("'"))) return t.slice(1, -1)
  return t
}

export function stripComment(s: string) {
  // A # starts a comment at the line start or after whitespace, outside quotes.
  let quote = ""
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (quote) {
      if (c === quote) quote = ""
      continue
    }
    if (c === '"' || c === "'") quote = c
    else if (c === "#" && (i === 0 || /\s/.test(s[i - 1]))) return s.slice(0, i)
  }
  return s
}

/** Splits leading "- " markers: their columns and the rest of the line. */
export function dashes(line: string, start: number): { cols: number[]; rest: number } {
  const cols: number[] = []
  let i = start
  for (;;) {
    if (line[i] === "-" && (i + 1 === line.length || line[i + 1] === " " || line[i + 1] === "\t")) {
      cols.push(i)
      i++
      while (line[i] === " " || line[i] === "\t") i++
      continue
    }
    return { cols, rest: i }
  }
}

class Scanner {
  stack: Frame[] = []
  /** Lines with a deeper indentation than this belong to a block scalar. */
  blockScalar = -1

  path() {
    return this.stack.map((f) => f.segment)
  }

  popForKey(indent: number) {
    while (this.stack.length && this.stack[this.stack.length - 1].indent >= indent) this.stack.pop()
  }

  popForDash(col: number) {
    for (;;) {
      const top = this.stack[this.stack.length - 1]
      if (!top) return
      if (top.indent > col || (top.indent === col && (top.seq || !top.open))) this.stack.pop()
      else return
    }
  }

  /** Feeds a complete line (not the cursor line). */
  line(raw: string) {
    const text = stripComment(raw).replace(/\s+$/, "")
    const indent = text.search(/\S/)
    if (indent < 0) return
    if (this.blockScalar >= 0) {
      if (indent > this.blockScalar) return
      this.blockScalar = -1
    }
    if (text.startsWith("---", indent) || text.startsWith("...", indent)) {
      this.stack = []
      return
    }
    const { cols, rest } = dashes(text, indent)
    for (const c of cols) {
      this.popForDash(c)
      this.stack.push({ indent: c, segment: "[]", seq: true, open: true })
    }
    const body = text.slice(rest)
    if (!body) return
    const m = KEY_RE.exec(body)
    if (!m) return
    if (cols.length === 0) this.popForKey(rest)
    const value = body.slice(m[0].length).trim()
    const key = unquote(m[1])
    if (!value) this.stack.push({ indent: rest, segment: key, seq: false, open: true })
    else if (/^[|>][-+0-9]*$/.test(value)) {
      this.stack.push({ indent: rest, segment: key, seq: false, open: false })
      this.blockScalar = rest
    } else this.stack.push({ indent: rest, segment: key, seq: false, open: false })
  }
}

/** Where the cursor at `pos` is in the YAML document `text`. */
export function yamlContextAt(text: string, pos: number): YamlContext {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const sc = new Scanner()
  if (lineStart > 0) for (const l of text.slice(0, lineStart - 1).split("\n")) sc.line(l)
  const line = text.slice(lineStart, pos)
  if (stripComment(line) !== line) return { kind: "none" }
  const indent = line.search(/\S/)
  if (sc.blockScalar >= 0 && (indent < 0 || indent > sc.blockScalar)) {
    if (indent < 0 && line.length <= sc.blockScalar) sc.blockScalar = -1
    else return { kind: "none" }
  }
  if (indent < 0) {
    // Blank so far: a key at this column.
    sc.popForKey(line.length)
    return { kind: "key", path: sc.path(), prefix: "", from: pos, indent: line.length }
  }
  const { cols, rest } = dashes(line, indent)
  for (const c of cols) {
    sc.popForDash(c)
    sc.stack.push({ indent: c, segment: "[]", seq: true, open: true })
  }
  const body = line.slice(rest)
  const m = KEY_RE.exec(body)
  if (m) {
    if (cols.length === 0) sc.popForKey(rest)
    const key = unquote(m[1])
    const after = body.slice(m[0].length)
    const lead = after.length - after.trimStart().length
    let value = after.slice(lead)
    let from = lineStart + rest + m[0].length + lead
    if (value.startsWith('"') || value.startsWith("'")) {
      value = value.slice(1)
      from++
    }
    if (/^[|>[{]/.test(value)) return { kind: "none" }
    return { kind: "value", path: [...sc.path(), key], key, prefix: value, from, indent: rest }
  }
  // A key being typed or a sequence item's scalar.
  let prefix = body
  let from = lineStart + rest
  if (prefix.startsWith('"') || prefix.startsWith("'")) {
    prefix = prefix.slice(1)
    from++
  }
  if (cols.length === 0) sc.popForKey(rest)
  if (cols.length > 0) {
    // "- foo": either a scalar item or the first key of a mapping item; the schema decides.
    return { kind: "value", path: sc.path(), key: "", prefix, from, indent: rest }
  }
  if (/[\s:]/.test(prefix.trim()) && !/^[\w.$/-]*$/.test(prefix)) return { kind: "none" }
  return { kind: "key", path: sc.path(), prefix, from, indent: rest }
}

/** Keys declared directly under each top-level section (services, networks, volumes, …). */
export function topLevelNames(text: string): Record<string, { name: string; line: number }[]> {
  const out: Record<string, { name: string; line: number }[]> = {}
  let section: string | null = null
  let childIndent = -1
  let blockScalar = -1
  const lines = text.split("\n")
  lines.forEach((raw, i) => {
    const t = stripComment(raw).replace(/\s+$/, "")
    const indent = t.search(/\S/)
    if (indent < 0) return
    if (blockScalar >= 0) {
      if (indent > blockScalar) return
      blockScalar = -1
    }
    const m = KEY_RE.exec(t.slice(indent))
    if (indent === 0) {
      section = m ? unquote(m[1]) : null
      childIndent = -1
      return
    }
    if (!section || !m) return
    if (childIndent < 0) childIndent = indent
    if (indent === childIndent) (out[section] ??= []).push({ name: unquote(m[1]), line: i + 1 })
    const value = t.slice(indent + m[0].length).trim()
    if (/^[|>][-+0-9]*$/.test(value)) blockScalar = indent
  })
  return out
}

/** The key (with its document range) under the cursor, for hover: the key's path and name. */
export function keyAt(text: string, pos: number): { path: PathSegment[]; key: string; from: number; to: number } | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const lineEnd = (() => {
    const e = text.indexOf("\n", pos)
    return e < 0 ? text.length : e
  })()
  const line = text.slice(lineStart, lineEnd)
  const indent = line.search(/\S/)
  if (indent < 0) return null
  const { rest } = dashes(line, indent)
  const m = KEY_RE.exec(line.slice(rest))
  if (!m) return null
  const from = lineStart + rest
  const to = from + m[1].length
  if (pos < from || pos > to) return null
  const ctx = yamlContextAt(text, from + m[0].length)
  if (ctx.kind !== "value") return null
  return { path: ctx.path.slice(0, -1), key: ctx.key, from, to }
}

/** The document of a multi-document stream ("---" separators) that holds `pos`: its range and index. */
export function documentAt(text: string, pos: number): { from: number; to: number; index: number } {
  let from = 0
  let index = 0
  const re = /^(---|\.\.\.)[ \t]*(#.*)?$/gm
  for (let m = re.exec(text); m; m = re.exec(text)) {
    const end = m.index + m[0].length
    if (m.index >= pos && m.index > from) return { from, to: m.index, index }
    if (m[1] === "---" && m.index > 0 && text.slice(from, m.index).trim() !== "") index++
    from = Math.min(end + 1, text.length)
  }
  return { from, to: text.length, index }
}
