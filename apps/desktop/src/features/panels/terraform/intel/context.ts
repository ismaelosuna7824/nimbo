// What the cursor is on, for completion, hover and signature help.

import { blockAt, inComment, parseCached, tokenIndexAt, type Attribute, type Block, type Parsed, type Token } from "./scan"

/** Start of an item in a body: block keywords at the top level, arguments and nested blocks inside a block. */
export interface ItemContext {
  kind: "item"
  /** Blocks from the top-level one to the body the cursor is in; empty at the top level. */
  path: Block[]
  from: number
  prefix: string
}

/** A block label: `resource "aws_|"`. */
export interface LabelContext {
  kind: "label"
  block: Block
  /** Which label (0: the type of resource/data, the name of provider/module/variable/output). */
  index: number
  /** Replaced range: the label's content (quotes excluded) or the bare word being typed. */
  from: number
  to: number
  prefix: string
  /** Typed with an opening quote; false for `resource aws_` (completion adds the quotes). */
  quoted: boolean
  /** Whether a closing quote follows the content. */
  closed: boolean
}

/** A function call the cursor is inside: `upper(|)`. */
export interface CallContext {
  name: string
  /** Offset of the name. */
  from: number
  /** 0-based argument index. */
  arg: number
}

/** Inside an attribute's value. */
export interface ValueContext {
  kind: "value"
  path: Block[]
  attr: Attribute
  /** Inside the literal text of a quoted string (not an interpolation). */
  string: { from: number; to: number; prefix: string } | null
  /** Reference segments typed before the one being completed: ["aws_s3_bucket", "main"] for `aws_s3_bucket.main.ar|`; "[]" is an index. */
  chain: string[]
  /** The segment being typed and where it starts. */
  partial: string
  from: number
  call: CallContext | null
}

export type CursorContext = ItemContext | LabelContext | ValueContext | { kind: "none" }

const NONE = { kind: "none" } as const

/** The cursor's context in text at pos. */
export function contextAt(text: string, pos: number): CursorContext {
  const p = parseCached(text)
  if (inComment(p, pos)) return NONE
  const body = blockAt(p.root, pos)

  for (const attr of body.attrs) {
    if (pos > attr.eq && pos <= attr.valueTo) return valueContext(p, body, attr, pos)
  }
  for (const b of body.blocks) {
    const headerEnd = b.open >= 0 ? b.open : b.close
    if (pos > b.typeTo && pos <= headerEnd) return labelContext(p, b, pos)
  }

  let lineStart = pos
  while (lineStart > 0 && text.charCodeAt(lineStart - 1) !== 10) lineStart--
  // An item may follow the block's "{" on the same line.
  if (body.open >= lineStart) lineStart = body.open + 1
  const before = text.slice(lineStart, pos)
  // `resource ` typed, no label yet.
  const bare = /^\s*([A-Za-z_][\w-]*)\s+([A-Za-z_][\w-]*)?$/.exec(before)
  if (bare && body === p.root && (bare[1] === "resource" || bare[1] === "data" || bare[1] === "provider")) {
    const partial = bare[2] ?? ""
    const block: Block = {
      type: bare[1],
      typeFrom: lineStart + before.indexOf(bare[1]),
      typeTo: lineStart + before.indexOf(bare[1]) + bare[1].length,
      labels: [],
      open: -1,
      close: pos,
      closed: false,
      attrs: [],
      blocks: [],
      parent: p.root,
      indent: 0,
    }
    let to = pos
    while (to < text.length && /[\w-]/.test(text[to])) to++
    return { kind: "label", block, index: 0, from: pos - partial.length, to, prefix: partial, quoted: false, closed: false }
  }
  const item = /^\s*([A-Za-z_][\w-]*)?$/.exec(before)
  if (!item) return NONE
  const prefix = item[1] ?? ""
  const path: Block[] = []
  for (let cur: Block | null = body; cur && cur.parent; cur = cur.parent) path.unshift(cur)
  return { kind: "item", path, from: pos - prefix.length, prefix }
}

function labelContext(p: Parsed, b: Block, pos: number): CursorContext {
  for (let index = 0; index < b.labels.length; index++) {
    const l = b.labels[index]
    if (pos >= l.from && pos <= l.to) {
      return {
        kind: "label",
        block: b,
        index,
        from: l.from,
        to: l.to,
        prefix: p.text.slice(l.from, pos),
        quoted: l.quoted,
        closed: l.closed,
      }
    }
  }
  // After the last label, on the header line: a new label.
  const last = b.labels[b.labels.length - 1]
  const after = last ? last.to + (last.quoted && last.closed ? 1 : 0) : b.typeTo
  const between = p.text.slice(after, pos)
  if (/^\s+$/.test(between)) {
    return { kind: "label", block: b, index: b.labels.length, from: pos, to: pos, prefix: "", quoted: false, closed: false }
  }
  return NONE
}

interface Frame {
  k: "(" | "[" | "{" | "str" | "interp" | "heredoc"
  /** "(": the called function. */
  fn?: { name: string; from: number }
  args: number
  /** "[": the reference chain before it (an index). */
  chain?: string[]
  /** "str": where its content starts. */
  from?: number
}

function valueContext(p: Parsed, body: Block, attr: Attribute, pos: number): CursorContext {
  const path: Block[] = []
  for (let cur: Block | null = body; cur && cur.parent; cur = cur.parent) path.unshift(cur)
  const { tokens, text } = p
  const end = Math.min(attr.tokenTo, tokenIndexAt(tokens, pos) + 1)
  const stack: Frame[] = []
  let segs: string[] = []
  let open = false
  let last: Token | null = null
  let prev: Token | null = null

  for (let k = attr.tokenFrom; k < end; k++) {
    const t = tokens[k]
    if (t.from >= pos) break
    prev = last
    last = t
    const top = stack[stack.length - 1]
    switch (t.kind) {
      case "strOpen":
        stack.push({ k: "str", args: 0, from: t.to })
        segs = []
        open = false
        continue
      case "heredocOpen":
        stack.push({ k: "heredoc", args: 0 })
        continue
      case "strClose":
      case "heredocClose":
        if (top && (top.k === "str" || top.k === "heredoc")) stack.pop()
        segs = []
        open = false
        continue
      case "interpOpen":
        stack.push({ k: "interp", args: 0 })
        segs = []
        open = false
        continue
      case "interpClose":
        if (top?.k === "interp") stack.pop()
        segs = []
        open = false
        continue
      case "strText":
        continue
      case "newline":
        if (top?.k === "str") stack.pop()
        segs = []
        open = false
        continue
      case "ident":
      case "number":
        if (open) segs.push(t.text)
        else segs = t.kind === "ident" ? [t.text] : []
        open = false
        continue
    }
    // punct
    switch (t.text) {
      case ".":
        if (segs.length > 0 && !open) open = true
        else segs = []
        continue
      case "(":
        stack.push({
          k: "(",
          args: 0,
          fn: prev?.kind === "ident" && segs.length === 1 && !open ? { name: prev.text, from: prev.from } : undefined,
        })
        segs = []
        open = false
        continue
      case "[":
        stack.push({ k: "[", args: 0, chain: !open && segs.length > 0 ? segs : undefined })
        segs = []
        open = false
        continue
      case "{":
        stack.push({ k: "{", args: 0 })
        segs = []
        open = false
        continue
      case ")":
      case "}":
        if (top && top.k === t.text.replace(")", "(").replace("}", "{")) stack.pop()
        segs = []
        open = false
        continue
      case "]": {
        const f = top?.k === "[" ? stack.pop() : undefined
        segs = f?.chain ? [...f.chain, "[]"] : []
        open = false
        continue
      }
      case ",":
        if (top) top.args++
        segs = []
        open = false
        continue
      default:
        segs = []
        open = false
    }
  }

  const top = stack[stack.length - 1]
  let string: ValueContext["string"] = null
  if (top && (top.k === "str" || top.k === "heredoc") && top.from !== undefined) {
    // Content up to the closing quote (or the line end).
    let to = pos
    while (to < text.length && text[to] !== '"' && text[to] !== "\n") to++
    string = { from: top.from, to, prefix: text.slice(top.from, pos) }
  }

  let partial = ""
  let from = pos
  let chain = segs
  if (last && last.from < pos && pos <= last.to && (last.kind === "ident" || last.kind === "number") && !string) {
    partial = text.slice(last.from, pos)
    from = last.from
    chain = segs.slice(0, -1)
  } else if (!(open && last && last.to === pos)) {
    chain = []
  }
  if (string) chain = []

  let call: CallContext | null = null
  for (let k = stack.length - 1; k >= 0; k--) {
    const f = stack[k]
    if (f.k === "(") {
      if (f.fn) call = { name: f.fn.name, from: f.fn.from, arg: f.args }
      break
    }
    if (f.k === "str" || f.k === "heredoc") break
  }

  return { kind: "value", path, attr, string, chain, partial, from, call }
}

/** The reference under pos: its segments and range (`aws_s3_bucket.main.arn` with the cursor anywhere on it). */
export function referenceAt(text: string, pos: number): { segs: string[]; from: number; to: number; index: number } | null {
  const isWord = (c: string | undefined) => !!c && /[\w-]/.test(c)
  let from = pos
  while (from > 0 && (isWord(text[from - 1]) || text[from - 1] === "." || text[from - 1] === "]")) {
    if (text[from - 1] === "]") {
      // Skip a simple index: [0], ["key"], [each.key].
      const open = text.lastIndexOf("[", from - 1)
      if (open < 0 || /[\n=]/.test(text.slice(open, from))) break
      from = open
      continue
    }
    from--
  }
  let to = pos
  while (to < text.length) {
    if (isWord(text[to])) to++
    else if (text[to] === "." && isWord(text[to + 1])) to++
    else if (text[to] === "[") {
      const close = text.indexOf("]", to)
      if (close < 0 || /[\n=]/.test(text.slice(to, close))) break
      to = close + 1
    } else break
  }
  const raw = text.slice(from, to)
  if (!raw || !/^[A-Za-z_]/.test(raw)) return null
  const segs: string[] = []
  let index = 0
  let offset = from
  for (const part of raw.split(/(\[[^\]]*\]|\.)/)) {
    if (!part || part === ".") {
      offset += part.length
      continue
    }
    if (part.startsWith("[")) segs.push("[]")
    else segs.push(part)
    if (pos >= offset && pos <= offset + part.length) index = segs.length - 1
    offset += part.length
  }
  return { segs, from, to, index }
}
