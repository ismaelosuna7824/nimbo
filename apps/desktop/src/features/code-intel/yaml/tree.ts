// A tolerant YAML reader that keeps document offsets: block mappings and
// sequences by indentation, plain/quoted/block scalars, single- or
// multi-line flow collections ([…], {…}), anchors, tags, aliases and merge
// keys, and multi-document streams. It never throws: lines it can't place
// are skipped. Schema validation, go to definition and quick fixes walk it.

import { KEY_RE, stripComment, unquote } from "./context"

interface Base {
  from: number
  to: number
  /** 1-based line of the node's start. */
  line: number
}

export interface YamlEntry {
  key: string
  keyFrom: number
  keyTo: number
  line: number
  /** null: the key has no value (`key:` with nothing under it). */
  value: YamlNode | null
}

export interface YamlMap extends Base {
  kind: "map"
  entries: YamlEntry[]
  /** Has a merge key (<<: *alias): some keys come from elsewhere. */
  merges: boolean
}

export interface YamlSeq extends Base {
  kind: "seq"
  items: YamlNode[]
}

export interface YamlScalar extends Base {
  kind: "scalar"
  /** Unquoted text (block scalars: their content). */
  value: string
  quoted: boolean
  /** A literal (|) or folded (>) block. */
  block: boolean
}

export interface YamlAlias extends Base {
  kind: "alias"
  name: string
}

export type YamlNode = YamlMap | YamlSeq | YamlScalar | YamlAlias

export interface YamlDocument {
  index: number
  from: number
  to: number
  root: YamlNode | null
}

interface Line {
  /** Offset of the line start. */
  start: number
  /** 1-based line number. */
  no: number
  raw: string
  /** Column where the content starts (moved past "- " markers while parsing). */
  col: number
  /** Column after the content (comment and trailing blanks stripped). */
  end: number
}

class Reader {
  lines: Line[]
  i = 0

  constructor(lines: Line[]) {
    this.lines = lines
  }

  peek(): Line | undefined {
    return this.lines[this.i]
  }

  content(l: Line) {
    return l.raw.slice(l.col, l.end)
  }

  node(minIndent: number): YamlNode | null {
    const l = this.peek()
    if (!l || l.col < minIndent) return null
    const c = this.content(l)
    if (isDash(c)) return this.seq(l.col)
    if (KEY_RE.test(c)) return this.map(l.col)
    this.i++
    return this.inlineValue(l, l.col, minIndent)
  }

  map(indent: number): YamlMap {
    const first = this.peek()!
    const m: YamlMap = { kind: "map", entries: [], merges: false, from: first.start + first.col, to: first.start + first.end, line: first.no }
    for (let l = this.peek(); l && l.col === indent; l = this.peek()) {
      const c = this.content(l)
      const k = KEY_RE.exec(c)
      if (!k || isDash(c)) break
      this.i++
      const keyFrom = l.start + l.col
      const key = unquote(k[1])
      const entry: YamlEntry = { key, keyFrom, keyTo: keyFrom + k[1].length, line: l.no, value: null }
      if (key === "<<") m.merges = true
      const restCol = l.col + k[0].length + (c.slice(k[0].length).length - c.slice(k[0].length).trimStart().length)
      if (restCol < l.end) entry.value = this.inlineValue(l, restCol, indent + 1)
      else {
        const next = this.peek()
        // "key:" followed by a sequence at the key's own indentation.
        if (next && next.col === indent && isDash(this.content(next))) entry.value = this.seq(indent)
        else entry.value = this.node(indent + 1)
      }
      m.entries.push(entry)
      m.to = Math.max(m.to, entry.value?.to ?? entry.keyTo)
    }
    return m
  }

  seq(indent: number): YamlSeq {
    const first = this.peek()!
    const s: YamlSeq = { kind: "seq", items: [], from: first.start + first.col, to: first.start + first.end, line: first.no }
    for (let l = this.peek(); l && l.col === indent && isDash(this.content(l)); l = this.peek()) {
      // Move past "-" and the blanks after it: the item starts there, on the same line.
      let col = l.col + 1
      while (col < l.end && (l.raw[col] === " " || l.raw[col] === "\t")) col++
      let item: YamlNode | null
      if (col >= l.end) {
        this.i++
        item = this.node(indent + 1)
        if (!item) item = { kind: "scalar", value: "", quoted: false, block: false, from: l.start + l.col, to: l.start + l.col + 1, line: l.no }
      } else {
        l.col = col
        item = this.node(col)
      }
      if (item) {
        s.items.push(item)
        s.to = Math.max(s.to, item.to)
      }
    }
    return s
  }

  /** The value written after "key:" or "- " at column `col` of line `l` (already consumed). */
  inlineValue(l: Line, col: number, blockIndent: number): YamlNode {
    let text = l.raw.slice(col, l.end)
    let at = col
    // Anchors and tags before the value: &name, !tag.
    for (let m = /^([&!][^\s]*)[ \t]*/.exec(text); m && m[0]; m = /^([&!][^\s]*)[ \t]*/.exec(text)) {
      at += m[0].length
      text = text.slice(m[0].length)
    }
    const from = l.start + at
    if (!text) {
      // "&anchor" alone: the value is the block below.
      return this.node(blockIndent) ?? { kind: "scalar", value: "", quoted: false, block: false, from, to: from, line: l.no }
    }
    if (text.startsWith("*")) return { kind: "alias", name: text.slice(1).trim(), from, to: l.start + l.end, line: l.no }
    if (/^[|>][-+0-9]*$/.test(text)) return this.blockScalar(l, from, blockIndent)
    // "{{ .Values.x }}" (Helm templates) is text, not a flow mapping.
    if (text.startsWith("[") || (text.startsWith("{") && !text.startsWith("{{"))) return this.flow(l, at)
    const quoted = text.startsWith('"') || text.startsWith("'")
    let value = quoted ? unquoteScalar(text) : text
    // Plain multi-line scalars: continuation lines deeper than the block.
    if (!quoted) {
      for (let n = this.peek(); n && n.col >= blockIndent && n.col > 0 && !KEY_RE.test(this.content(n)) && !isDash(this.content(n)); n = this.peek()) {
        value += ` ${this.content(n)}`
        this.i++
      }
    }
    return { kind: "scalar", value, quoted, block: false, from, to: l.start + l.end, line: l.no }
  }

  blockScalar(l: Line, from: number, blockIndent: number): YamlScalar {
    const parts: string[] = []
    let to = l.start + l.end
    let base = -1
    for (let n = this.peek(); n && n.col >= blockIndent; n = this.peek()) {
      if (base < 0) base = n.col
      parts.push(n.raw.slice(Math.min(n.col, base)).trimEnd())
      to = n.start + n.raw.length
      this.i++
    }
    return { kind: "scalar", value: parts.join("\n"), quoted: false, block: true, from, to, line: l.no }
  }

  /** A flow collection starting at column `col` of `l`, possibly spanning the next lines. */
  flow(l: Line, col: number): YamlNode {
    // Gather the source until brackets balance (outside quotes).
    let src = l.raw.slice(col)
    const startOffset = l.start + col
    let depth = 0
    let quote = ""
    let done = false
    const scan = (s: string) => {
      for (let i = 0; i < s.length && !done; i++) {
        const c = s[i]
        if (quote) {
          if (c === "\\" && quote === '"') i++
          else if (c === quote) quote = ""
          continue
        }
        if (c === '"' || c === "'") quote = c
        else if (c === "[" || c === "{") depth++
        else if (c === "]" || c === "}") {
          depth--
          if (depth === 0) done = true
        } else if (c === "#" && (i === 0 || /\s/.test(s[i - 1]))) return
      }
    }
    scan(src)
    while (!done) {
      const n = this.peek()
      if (!n) break
      this.i++
      const extra = n.raw
      src += `\n${extra}`
      scan(extra)
    }
    // The raw text between lines keeps its offsets: parse over the original document slice.
    const p = new FlowParser(src, startOffset, l.no)
    return p.value() ?? { kind: "scalar", value: "", quoted: false, block: false, from: startOffset, to: startOffset, line: l.no }
  }
}

function isDash(c: string) {
  return c === "-" || c.startsWith("- ") || c.startsWith("-\t")
}

function unquoteScalar(text: string): string {
  const q = text[0]
  const end = text.lastIndexOf(q)
  const body = end > 0 ? text.slice(1, end) : text.slice(1)
  if (q === "'") return body.replace(/''/g, "'")
  return body.replace(/\\(["\\/nt])/g, (_, c: string) => (c === "n" ? "\n" : c === "t" ? "\t" : c))
}

/** A small parser of flow collections and their scalars. */
class FlowParser {
  s: string
  base: number
  line: number
  i = 0

  constructor(s: string, base: number, line: number) {
    this.s = s
    this.base = base
    this.line = line
  }

  ws() {
    for (;;) {
      while (this.i < this.s.length && /\s/.test(this.s[this.i])) this.i++
      if (this.s[this.i] === "#") {
        while (this.i < this.s.length && this.s[this.i] !== "\n") this.i++
        continue
      }
      return
    }
  }

  lineAt(i: number) {
    let n = this.line
    for (let k = 0; k < i; k++) if (this.s[k] === "\n") n++
    return n
  }

  value(): YamlNode | null {
    this.ws()
    const c = this.s[this.i]
    if (c === undefined) return null
    const start = this.i
    if (c === "[") {
      this.i++
      const seq: YamlSeq = { kind: "seq", items: [], from: this.base + start, to: this.base + start, line: this.lineAt(start) }
      for (;;) {
        this.ws()
        if (this.s[this.i] === "]") {
          this.i++
          break
        }
        if (this.i >= this.s.length) break
        const v = this.value()
        if (v) seq.items.push(v)
        this.ws()
        if (this.s[this.i] === ",") this.i++
        else if (this.s[this.i] !== "]") {
          if (!v) this.i++
        }
      }
      seq.to = this.base + this.i
      return seq
    }
    if (c === "{") {
      this.i++
      const map: YamlMap = { kind: "map", entries: [], merges: false, from: this.base + start, to: this.base + start, line: this.lineAt(start) }
      for (;;) {
        this.ws()
        if (this.s[this.i] === "}") {
          this.i++
          break
        }
        if (this.i >= this.s.length) break
        const keyStart = this.i
        const k = this.scalar(true)
        this.ws()
        let value: YamlNode | null = null
        if (this.s[this.i] === ":") {
          this.i++
          this.ws()
          if (this.s[this.i] !== "," && this.s[this.i] !== "}") value = this.value()
        }
        if (k.kind === "scalar" && (k.value !== "" || k.quoted)) {
          map.entries.push({ key: k.value, keyFrom: this.base + keyStart, keyTo: k.to, line: this.lineAt(keyStart), value })
          if (k.value === "<<") map.merges = true
        } else if (this.s[this.i] !== "," && this.s[this.i] !== "}") this.i++
        this.ws()
        if (this.s[this.i] === ",") this.i++
      }
      map.to = this.base + this.i
      return map
    }
    return this.scalar(false)
  }

  /** A quoted or plain scalar inside a flow collection; `key`: stop at ":" too. */
  scalar(key: boolean): YamlNode {
    const start = this.i
    const c = this.s[this.i]
    if (c === '"' || c === "'") {
      this.i++
      while (this.i < this.s.length && this.s[this.i] !== c) {
        if (c === '"' && this.s[this.i] === "\\") this.i++
        else if (c === "'" && this.s[this.i] === "'" && this.s[this.i + 1] === "'") this.i++
        this.i++
      }
      this.i++
      const raw = this.s.slice(start, this.i)
      return { kind: "scalar", value: unquoteScalar(raw), quoted: true, block: false, from: this.base + start, to: this.base + this.i, line: this.lineAt(start) }
    }
    if (c === "*") {
      while (this.i < this.s.length && !/[\s,\]}]/.test(this.s[this.i])) this.i++
      return { kind: "alias", name: this.s.slice(start + 1, this.i), from: this.base + start, to: this.base + this.i, line: this.lineAt(start) }
    }
    while (this.i < this.s.length) {
      const ch = this.s[this.i]
      if (ch === "," || ch === "]" || ch === "}" || ch === "\n") break
      // ": " (or ":" before a delimiter) ends a key; "http://x" keeps its colon.
      if (key && ch === ":" && (this.i + 1 >= this.s.length || /[\s,\]}]/.test(this.s[this.i + 1]))) break
      if (ch === "#" && /\s/.test(this.s[this.i - 1] ?? "")) break
      this.i++
    }
    const raw = this.s.slice(start, this.i)
    const value = raw.trim()
    const to = start + raw.trimEnd().length
    return { kind: "scalar", value, quoted: false, block: false, from: this.base + start, to: this.base + to, line: this.lineAt(start) }
  }
}

/** Every document of a YAML stream as a tree with offsets. */
export function parseYaml(text: string): YamlDocument[] {
  const docs: YamlDocument[] = []
  let lines: Line[] = []
  let docFrom = 0
  let sawContent = false
  const flush = (to: number) => {
    if (sawContent) {
      const r = new Reader(lines)
      let root: YamlNode | null = null
      while (r.peek()) {
        const minIndent = r.peek()!.col
        const n = r.node(minIndent)
        if (!root && n) root = n
        else if (!n) r.i++
      }
      docs.push({ index: docs.length, from: docFrom, to, root })
    }
    lines = []
    sawContent = false
  }
  let offset = 0
  const raw = text.split("\n")
  raw.forEach((l, i) => {
    const start = offset
    offset += l.length + 1
    if (/^(---|\.\.\.)([ \t]|$)/.test(l)) {
      flush(start)
      docFrom = Math.min(offset, text.length)
      // "--- value" / "--- |" on the separator line: rare in config files; the rest of the line is ignored.
      return
    }
    if (/^%/.test(l) && !sawContent) return
    const stripped = stripComment(l).replace(/\s+$/, "")
    const col = stripped.search(/\S/)
    if (col < 0) return
    sawContent = true
    lines.push({ start, no: i + 1, raw: l, col, end: stripped.length })
  })
  flush(text.length)
  return docs
}

/** The deepest node whose range holds `pos`, with its key path ("[]" for sequence items). */
export function nodeAt(root: YamlNode | null, pos: number): { node: YamlNode; path: string[]; entry?: YamlEntry } | null {
  if (!root || pos < root.from || pos > root.to) return null
  let cur: { node: YamlNode; path: string[]; entry?: YamlEntry } = { node: root, path: [] }
  for (;;) {
    const n = cur.node
    let next: typeof cur | null = null
    if (n.kind === "map") {
      for (const e of n.entries) {
        if (pos >= e.keyFrom && pos <= e.keyTo) return { node: n, path: cur.path, entry: e }
        if (e.value && pos >= e.value.from && pos <= e.value.to) {
          next = { node: e.value, path: [...cur.path, e.key], entry: e }
          break
        }
      }
    } else if (n.kind === "seq") {
      for (const it of n.items) {
        if (pos >= it.from && pos <= it.to) {
          next = { node: it, path: [...cur.path, "[]"] }
          break
        }
      }
    }
    if (!next) return cur
    cur = next
  }
}

/** The value of a mapping's key (first match), or undefined. */
export function get(node: YamlNode | null | undefined, key: string): YamlNode | null | undefined {
  if (!node || node.kind !== "map") return undefined
  return node.entries.find((e) => e.key === key)?.value
}

/** The text of a scalar node, or undefined. */
export function scalarText(node: YamlNode | null | undefined): string | undefined {
  return node?.kind === "scalar" ? node.value : undefined
}
