// YAML formatting that only re-indents: every line keeps its content, its
// comments and its order. Nesting levels become `indent` spaces, sequences
// sit one level under their key, "-" is followed by one space, trailing
// blanks go and the file ends with one newline. Block scalars and multi-line
// flow collections move with their key, keeping their inner layout. The
// result is checked to read back as the same data; otherwise it is refused.

import { KEY_RE, stripComment } from "./context"
import { parseYaml, type YamlNode } from "./tree"

interface Frame {
  /** Column of the content in the original text. */
  orig: number
  /** Column in the formatted text. */
  out: number
  /** A sequence written at its key's column ("key:\n- a"): ends at the next non-dash line of that column. */
  compact?: boolean
}

/** The data of a tree without offsets, to compare a text before and after formatting. */
function shape(n: YamlNode | null): unknown {
  if (!n) return null
  switch (n.kind) {
    case "map":
      return n.entries.map((e) => [e.key, shape(e.value)])
    case "seq":
      return n.items.map(shape)
    case "alias":
      return `*${n.name}`
    case "scalar":
      return n.value
  }
}

function blockShape(text: string) {
  return JSON.stringify(parseYaml(text).map((d) => shape(d.root)))
}

/** Brackets still open at the end of `s` (outside quotes and comments). */
function flowDepth(s: string, depth: number): number {
  let quote = ""
  for (let i = 0; i < s.length; i++) {
    const c = s[i]
    if (quote) {
      if (c === "\\" && quote === '"') i++
      else if (c === quote) quote = ""
      continue
    }
    if (c === '"' || c === "'") quote = c
    else if (c === "[" || c === "{") depth++
    else if (c === "]" || c === "}") depth--
    else if (c === "#" && (i === 0 || /\s/.test(s[i - 1]))) break
  }
  return depth
}

export class YamlFormatError extends Error {}

/** Re-indents `text`; throws YamlFormatError when the result would not read back as the same data. */
export function formatYaml(text: string, indent = 2): string {
  const lines = text.replace(/\r\n/g, "\n").split("\n")
  const out: string[] = []
  let stack: Frame[] = [{ orig: 0, out: 0 }]
  /** A key without an inline value: its children come next. */
  let open: Frame | null = null
  /**
   * Lines copied with a fixed shift (block scalars, multi-line flow collections). A block scalar's shift is set by
   * its first content line, which goes one level under the key (`target`); its lines keep trailing blanks.
   */
  let verbatim: { minCol: number; shift: number | null; target: number; flow: number } | null = null
  const comments: { col: number; text: string }[] = []
  const pad = (n: number) => " ".repeat(Math.max(0, n))

  const flushComments = (col: number) => {
    for (const c of comments) out.push(pad(c.col < 0 ? col : c.col) + c.text)
    comments.length = 0
  }

  for (const raw of lines) {
    const line = raw.replace(/\s+$/, "")
    const col = line.search(/\S/)

    if (verbatim) {
      if (verbatim.flow > 0) {
        out.push(col < 0 ? "" : pad(col + (verbatim.shift ?? 0)) + line.slice(col))
        verbatim.flow = flowDepth(line, verbatim.flow)
        if (verbatim.flow <= 0) verbatim = null
        continue
      }
      if (col < 0 || col >= verbatim.minCol) {
        flushComments(0)
        if (col >= 0) verbatim.shift ??= verbatim.target - col
        out.push(col < 0 ? "" : pad(col + (verbatim.shift ?? 0)) + raw.slice(col))
        continue
      }
      verbatim = null
    }

    if (col < 0) {
      flushComments(0)
      out.push("")
      continue
    }
    const content = line.slice(col)
    if (/^(---|\.\.\.)(\s|$)/.test(line) || line.startsWith("%")) {
      flushComments(0)
      out.push(line)
      stack = [{ orig: 0, out: 0 }]
      open = null
      continue
    }
    if (content.startsWith("#")) {
      // Placed with the line it precedes unless it sits at a known level.
      const at = stack.find((f) => f.orig === col)
      comments.push({ col: at ? at.out : -1, text: content })
      continue
    }

    const dash = /^-(\s|$)/.test(content)
    // Close deeper levels; a compact sequence ends at a non-dash line of its column.
    while (stack.length > 1 && (stack[stack.length - 1].orig > col || (!dash && stack[stack.length - 1].compact && stack[stack.length - 1].orig === col)))
      stack.pop()
    let top = stack[stack.length - 1]
    if (open && (col > open.orig || (dash && col === open.orig))) {
      const child: Frame = { orig: col, out: open.out + indent, compact: col === open.orig }
      stack.push(child)
      top = child
    } else if (top.orig !== col) {
      // An indentation that matches no open level (inconsistent input): nest it under the current level.
      const child: Frame = { orig: col, out: top.out + (col > top.orig ? indent : 0) }
      stack.push(child)
      top = child
    }
    open = null
    flushComments(top.out)

    // Rewrite "- - key: v" markers with one space each; the item's content gets its own level.
    let rest = content
    let outCol: number = top.out
    let origCol: number = col
    let prefix = ""
    /** Original and formatted column of the last "-": a block scalar item ("- |") is indented from it. */
    let dashOrig = col
    let dashOut: number = top.out
    while (/^-(\s|$)/.test(rest)) {
      const after = rest.slice(1)
      const lead = after.length - after.trimStart().length
      prefix += after.trim() ? "- " : "-"
      dashOrig = origCol
      dashOut = outCol
      origCol += 1 + lead
      outCol += 2
      rest = after.trimStart()
      if (rest) stack.push({ orig: origCol, out: outCol })
    }
    out.push(pad(top.out) + prefix + rest)

    if (!rest) {
      // "-" alone: the item follows on the next lines.
      open = { orig: col, out: outCol - indent }
      continue
    }
    const key = KEY_RE.exec(rest)
    const value = key ? stripComment(rest.slice(key[0].length)).trim().replace(/^([&!]\S*\s*)+/, "") : stripComment(rest).trim()
    const lineCol: number = prefix ? origCol : col
    const lineOut: number = prefix ? outCol : top.out
    if (key && value === "") open = { orig: lineCol, out: lineOut }
    else if (/^[|>][-+]?[0-9]?[-+]?$/.test(value)) {
      // A block scalar: with an indentation indicator its lines move exactly like the key; otherwise the first one
      // goes one level under the key (or the item's "-") and the others follow it.
      const fixed = /[0-9]/.test(value)
      const [parentOrig, parentOut] = key || !prefix ? [lineCol, lineOut] : [dashOrig, dashOut]
      verbatim = { minCol: parentOrig + 1, shift: fixed ? parentOut - parentOrig : null, target: parentOut + indent, flow: 0 }
    } else if (value.startsWith("[") || value.startsWith("{")) {
      const depth = flowDepth(value, 0)
      if (depth > 0) verbatim = { minCol: 0, shift: lineOut - lineCol, target: 0, flow: depth }
    }
  }
  flushComments(0)

  while (out.length && out[out.length - 1] === "") out.pop()
  const result = out.join("\n") + "\n"
  if (blockShape(result) !== blockShape(text)) throw new YamlFormatError("Formatting would change the data; the file was left as it is")
  return result
}
