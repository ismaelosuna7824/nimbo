// A tolerant HCL scanner for the editor's language features: tokens plus a
// tree of blocks and attributes that survives half-written code (unclosed
// strings, brackets and blocks, headers without a body).

export type TokenKind =
  | "ident"
  | "number"
  | "punct"
  | "newline"
  /** `"` opening a quoted template. */
  | "strOpen"
  /** Literal text of a quoted template or heredoc. */
  | "strText"
  /** `"` closing a quoted template (absent when the string is unterminated). */
  | "strClose"
  /** `<<EOT` / `<<-EOT`. */
  | "heredocOpen"
  | "heredocClose"
  /** `${` or `%{`. */
  | "interpOpen"
  | "interpClose"

export interface Token {
  kind: TokenKind
  from: number
  to: number
  text: string
  /** Open string/heredoc frames around the token (0: plain code). */
  str: number
}

export interface Label {
  text: string
  /** Content range (inside the quotes). */
  from: number
  to: number
  quoted: boolean
  /** Quoted label with its closing quote. */
  closed: boolean
}

export interface Attribute {
  name: string
  nameFrom: number
  nameTo: number
  /** Offset of `=`. */
  eq: number
  valueFrom: number
  valueTo: number
  /** Index of the first value token. */
  tokenFrom: number
  /** Index after the last value token. */
  tokenTo: number
}

export interface Block {
  type: string
  typeFrom: number
  typeTo: number
  labels: Label[]
  /** Offset of `{`; -1 for a header without a body yet. */
  open: number
  /** Offset after `}`, or where the unclosed block ends. */
  close: number
  closed: boolean
  attrs: Attribute[]
  blocks: Block[]
  parent: Block | null
  /** Column of the block type on its line. */
  indent: number
}

export interface Parsed {
  text: string
  tokens: Token[]
  root: Block
  /** Comment ranges [from, to). */
  comments: [number, number][]
}

/** Blocks that only appear at the top level of a module. */
export const TOP_LEVEL_BLOCKS: Record<string, true> = {
  resource: true,
  data: true,
  module: true,
  variable: true,
  output: true,
  locals: true,
  provider: true,
  terraform: true,
  moved: true,
  import: true,
  check: true,
  removed: true,
  ephemeral: true,
}

const isIdentStart = (c: number) => (c >= 65 && c <= 90) || (c >= 97 && c <= 122) || c === 95
const isIdentPart = (c: number) => isIdentStart(c) || (c >= 48 && c <= 57) || c === 45
const isDigit = (c: number) => c >= 48 && c <= 57

const MULTI_PUNCT = ["...", "==", "!=", "<=", ">=", "&&", "||", "=>", "::"]

type Frame = { k: "str" } | { k: "heredoc"; tag: string } | { k: "interp"; braces: number; inQuoted: boolean }

/** Splits HCL source into tokens; comments are returned separately. */
export function lex(text: string): { tokens: Token[]; comments: [number, number][] } {
  const tokens: Token[] = []
  const comments: [number, number][] = []
  const frames: Frame[] = []
  const n = text.length
  let i = 0
  const strDepth = () => frames.reduce((d, f) => d + (f.k === "interp" ? 0 : 1), 0)
  const push = (kind: TokenKind, from: number, to: number) =>
    tokens.push({ kind, from, to, text: text.slice(from, to), str: strDepth() })

  while (i < n) {
    const top = frames[frames.length - 1]
    if (top && top.k === "str") {
      // Quoted template text up to an interpolation, the closing quote or the end of the line.
      const start = i
      while (i < n) {
        const c = text.charCodeAt(i)
        if (c === 92 /* \ */) {
          i += 2
          continue
        }
        if (c === 34 /* " */ || c === 10) break
        if ((c === 36 || c === 37) && text.charCodeAt(i + 1) === c && text.charCodeAt(i + 2) === 123) {
          // "$${" / "%%{" escape the sequence.
          i += 3
          continue
        }
        if ((c === 36 || c === 37) && text.charCodeAt(i + 1) === 123) break
        i++
      }
      if (i > n) i = n
      if (i > start) push("strText", start, i)
      if (i >= n) break
      const c = text.charCodeAt(i)
      if (c === 34) {
        frames.pop()
        push("strClose", i, i + 1)
        i++
      } else if (c === 10) {
        // Unterminated: the string ends with the line.
        frames.pop()
      } else {
        frames.push({ k: "interp", braces: 0, inQuoted: true })
        push("interpOpen", i, i + 2)
        i += 2
      }
      continue
    }
    if (top && top.k === "heredoc") {
      const lineStart = i === 0 || text.charCodeAt(i - 1) === 10
      if (lineStart) {
        let end = text.indexOf("\n", i)
        if (end < 0) end = n
        if (text.slice(i, end).trim() === top.tag) {
          frames.pop()
          push("heredocClose", i, end)
          i = end
          continue
        }
      }
      const start = i
      let interp = false
      while (i < n) {
        const c = text.charCodeAt(i)
        if ((c === 36 || c === 37) && text.charCodeAt(i + 1) === c && text.charCodeAt(i + 2) === 123) {
          i += 3
          continue
        }
        if ((c === 36 || c === 37) && text.charCodeAt(i + 1) === 123) {
          interp = true
          break
        }
        i++
        if (c === 10) break
      }
      if (i > start) push("strText", start, i)
      if (interp) {
        frames.push({ k: "interp", braces: 0, inQuoted: false })
        push("interpOpen", i, i + 2)
        i += 2
      }
      continue
    }

    const c = text.charCodeAt(i)
    if (c === 10) {
      if (top && top.k === "interp" && top.inQuoted) {
        // An interpolation left open in a quoted string ends with its line.
        frames.pop()
        frames.pop()
      }
      push("newline", i, i + 1)
      i++
      continue
    }
    if (c === 32 || c === 9 || c === 13) {
      i++
      continue
    }
    if (c === 35 /* # */ || (c === 47 && text.charCodeAt(i + 1) === 47)) {
      let end = text.indexOf("\n", i)
      if (end < 0) end = n
      comments.push([i, end])
      i = end
      continue
    }
    if (c === 47 && text.charCodeAt(i + 1) === 42) {
      let end = text.indexOf("*/", i + 2)
      end = end < 0 ? n : end + 2
      comments.push([i, end])
      i = end
      continue
    }
    if (isIdentStart(c)) {
      const start = i
      while (i < n && isIdentPart(text.charCodeAt(i))) i++
      push("ident", start, i)
      continue
    }
    if (isDigit(c)) {
      const start = i
      while (i < n && (isDigit(text.charCodeAt(i)) || text.charCodeAt(i) === 46 || text.charCodeAt(i) === 101)) {
        // Stop before a "." that is not followed by a digit (attribute access after an index).
        if (text.charCodeAt(i) === 46 && !isDigit(text.charCodeAt(i + 1))) break
        i++
      }
      push("number", start, i)
      continue
    }
    if (c === 34) {
      push("strOpen", i, i + 1)
      frames.push({ k: "str" })
      i++
      continue
    }
    if (c === 60 && text.charCodeAt(i + 1) === 60) {
      const m = /^<<(-?)([A-Za-z_][A-Za-z0-9_-]*)[ \t]*\r?(?:\n|$)/.exec(text.slice(i, i + 200))
      if (m) {
        push("heredocOpen", i, i + 2 + m[1].length + m[2].length)
        frames.push({ k: "heredoc", tag: m[2] })
        i += m[0].length
        continue
      }
    }
    if (top && top.k === "interp") {
      if (c === 123) top.braces++
      if (c === 125) {
        if (top.braces === 0) {
          frames.pop()
          push("interpClose", i, i + 1)
          i++
          continue
        }
        top.braces--
      }
    }
    const multi = MULTI_PUNCT.find((p) => text.startsWith(p, i))
    const len = multi ? multi.length : 1
    push("punct", i, i + len)
    i += len
  }
  return { tokens, comments }
}

const lineIndent = (text: string, pos: number) => {
  let s = pos
  while (s > 0 && text.charCodeAt(s - 1) !== 10) s--
  return pos - s
}

const lineEnd = (text: string, pos: number) => {
  const e = text.indexOf("\n", pos)
  return e < 0 ? text.length : e
}

function newBlock(type: string, from: number, to: number, parent: Block | null, indent: number): Block {
  return { type, typeFrom: from, typeTo: to, labels: [], open: -1, close: -1, closed: false, attrs: [], blocks: [], parent, indent }
}

/** Parses source into a block tree, tolerating incomplete code. */
export function parse(text: string): Parsed {
  const { tokens, comments } = lex(text)
  const root = newBlock("", 0, 0, null, -1)
  root.open = -1
  let i = 0
  const tok = (k: number) => tokens[k] as Token | undefined
  const isPunct = (t: Token | undefined, p: string) => !!t && t.kind === "punct" && t.text === p

  const skipLine = () => {
    let depth = 0
    while (i < tokens.length) {
      const t = tokens[i]
      if (t.kind === "newline" && t.str === 0 && depth <= 0) return
      if (t.kind === "punct" && t.str === 0) {
        if (t.text === "(" || t.text === "[" || t.text === "{") depth++
        else if (t.text === ")" || t.text === "]" || t.text === "}") {
          if (depth === 0 && t.text === "}") return
          depth--
        }
      }
      i++
    }
  }

  /** Starts a new body item that ends an unclosed expression: `}`, `name =`, `name {`, `name "label"`. */
  const startsItem = (k: number) => {
    const t = tok(k)
    if (!t) return false
    if (isPunct(t, "}")) return true
    if (t.kind !== "ident") return false
    const nx = tok(k + 1)
    return !!nx && (isPunct(nx, "=") || isPunct(nx, "{") || nx.kind === "strOpen")
  }

  const expression = (indent: number) => {
    let depth = 0
    while (i < tokens.length) {
      const t = tokens[i]
      if (t.kind === "newline" && t.str === 0) {
        if (depth <= 0) return
        // Inside an unclosed bracket, a less or equally indented item ends the value.
        const nx = tok(i + 1)
        if (nx && nx.kind !== "newline" && lineIndent(text, nx.from) <= indent && startsItem(i + 1)) return
      }
      if (t.kind === "punct" && t.str === 0) {
        if (t.text === "(" || t.text === "[" || t.text === "{") depth++
        else if (t.text === ")" || t.text === "]" || t.text === "}") {
          if (depth === 0) return
          depth--
        }
      }
      i++
    }
  }

  const body = (block: Block) => {
    while (i < tokens.length) {
      const t = tokens[i]
      if (t.kind === "newline") {
        i++
        continue
      }
      if (isPunct(t, "}")) {
        if (block === root) {
          i++
          continue
        }
        block.close = t.to
        block.closed = true
        i++
        return
      }
      if (t.kind === "ident") {
        const indent = lineIndent(text, t.from)
        // A top-level keyword at column 0 ends blocks left open above it.
        if (block !== root && indent === 0 && TOP_LEVEL_BLOCKS[t.text] === true) {
          const nx = tok(i + 1)
          if (nx && (nx.kind === "strOpen" || nx.kind === "ident" || isPunct(nx, "{"))) {
            let s = t.from
            while (s > 0 && text.charCodeAt(s - 1) !== 10) s--
            block.close = Math.max(block.open + 1, s - 1)
            return
          }
        }
        const nx = tok(i + 1)
        if (isPunct(nx, "=")) {
          const eq = nx!
          i += 2
          const tokenFrom = i
          expression(indent)
          block.attrs.push({
            name: t.text,
            nameFrom: t.from,
            nameTo: t.to,
            eq: eq.from,
            valueFrom: eq.to,
            // Up to what ends it (the line break, or a "}" on the same line).
            valueTo: i < tokens.length ? tokens[i].from : text.length,
            tokenFrom,
            tokenTo: i,
          })
          continue
        }
        if (nx && (nx.kind === "strOpen" || nx.kind === "ident" || isPunct(nx, "{"))) {
          const b = newBlock(t.text, t.from, t.to, block, indent)
          block.blocks.push(b)
          i++
          // Labels: quoted or bare identifiers, up to "{" on the same line.
          while (i < tokens.length) {
            const lt = tokens[i]
            if (lt.kind === "strOpen") {
              const from = lt.to
              let to = from
              let closed = false
              let label = ""
              i++
              while (i < tokens.length && tokens[i].str > 0) {
                const st = tokens[i]
                if (st.kind === "strText") {
                  label += st.text
                  to = st.to
                } else if (st.kind === "interpOpen" || st.kind === "interpClose") {
                  to = st.to
                }
                i++
              }
              if (i < tokens.length && tokens[i].kind === "strClose") {
                closed = true
                to = tokens[i].from
                i++
              } else if (to === from) {
                to = Math.max(from, Math.min(lineEnd(text, from), i < tokens.length ? tokens[i].from : text.length))
              }
              b.labels.push({ text: label, from, to, quoted: true, closed })
              continue
            }
            if (lt.kind === "ident") {
              b.labels.push({ text: lt.text, from: lt.from, to: lt.to, quoted: false, closed: true })
              i++
              continue
            }
            break
          }
          if (isPunct(tok(i), "{")) {
            b.open = tokens[i].from
            i++
            body(b)
            if (!b.closed && b.close < 0) b.close = text.length
          } else {
            // A header without its body yet ends with its line.
            b.close = lineEnd(text, b.typeFrom)
            skipLine()
          }
          continue
        }
      }
      skipLine()
    }
    if (block !== root && block.close < 0) block.close = text.length
  }

  body(root)
  root.close = text.length
  root.closed = true
  return { text, tokens, root, comments }
}

const cache = new Map<string, Parsed>()

/** parse with a small cache (completion, hover and signature help parse the same text). */
export function parseCached(text: string): Parsed {
  const hit = cache.get(text)
  if (hit) return hit
  const p = parse(text)
  cache.set(text, p)
  if (cache.size > 8) cache.delete(cache.keys().next().value!)
  return p
}

/** Index of the first token ending after pos (tokens.length when none). */
export function tokenIndexAt(tokens: Token[], pos: number): number {
  let lo = 0
  let hi = tokens.length
  while (lo < hi) {
    const mid = (lo + hi) >> 1
    if (tokens[mid].to < pos) lo = mid + 1
    else hi = mid
  }
  return lo
}

/** The body containing pos: the innermost block whose braces enclose it (the root otherwise). */
export function blockAt(root: Block, pos: number): Block {
  for (const b of root.blocks) {
    if (b.open >= 0 && pos > b.open && (b.closed ? pos < b.close : pos <= b.close)) return blockAt(b, pos)
  }
  return root
}

/** Blocks from the top-level one down to b (root excluded). */
export function blockPath(b: Block): Block[] {
  const out: Block[] = []
  for (let cur: Block | null = b; cur && cur.parent; cur = cur.parent) out.unshift(cur)
  return out
}

export function inComment(p: Parsed, pos: number): boolean {
  return p.comments.some(([f, t]) => pos > f && pos <= t)
}

/** 1-based line of an offset. */
export function lineOf(text: string, pos: number): number {
  let n = 1
  for (let i = text.indexOf("\n"); i >= 0 && i < pos; i = text.indexOf("\n", i + 1)) n++
  return n
}
