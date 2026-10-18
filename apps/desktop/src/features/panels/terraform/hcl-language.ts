import { HighlightStyle, StreamLanguage, type StreamParser, type StringStream } from "@codemirror/language"
import { tags as t } from "@lezer/highlight"

/** One open string context: a quoted string or a heredoc body. */
type Frame = { kind: "string" } | { kind: "heredoc"; tag: string; indented: boolean } | { kind: "interp"; depth: number }

interface HclState {
  stack: Frame[]
  blockComment: boolean
  /** True until the first token of the current line was read. */
  lineStart: boolean
}

const BLOCK_KEYWORDS = keywordTable([
  "resource",
  "data",
  "module",
  "variable",
  "output",
  "locals",
  "provider",
  "terraform",
  "moved",
  "import",
  "check",
  "removed",
  "dynamic",
  "content",
  "lifecycle",
  "provisioner",
  "connection",
  "backend",
  "required_providers",
  "validation",
])
const CONTROL_KEYWORDS = keywordTable(["for", "in", "if", "else", "endfor", "endif", "for_each", "count", "depends_on"])
const ATOMS = keywordTable(["true", "false", "null"])
const ROOT_REFS = keywordTable(["var", "local", "module", "data", "each", "count", "path", "terraform", "self"])

function keywordTable(words: string[]): Record<string, true> {
  return Object.fromEntries(words.map((w) => [w, true]))
}

/** Consumes template text until an interpolation, the closing quote or end of line. */
function templateText(stream: StringStream, state: HclState, quoted: boolean): string {
  while (!stream.eol()) {
    if (stream.match(/^(\$\$|%%)\{/)) continue
    if (stream.match(/^[$%]\{/, false)) break
    const ch = stream.next()
    if (quoted && ch === "\\") {
      stream.next()
      continue
    }
    if (quoted && ch === '"') {
      state.stack.pop()
      return "string"
    }
  }
  return "string"
}

function tokenTemplate(stream: StringStream, state: HclState, frame: Frame): string | null {
  if (frame.kind === "heredoc" && state.lineStart) {
    state.lineStart = false
    const line = stream.string.slice(stream.pos)
    if ((frame.indented ? line.trim() : line.trimEnd()) === frame.tag) {
      stream.skipToEnd()
      state.stack.pop()
      return "keyword"
    }
  }
  state.lineStart = false
  if (stream.match(/^[$%]\{~?/)) {
    state.stack.push({ kind: "interp", depth: 0 })
    return "processingInstruction"
  }
  return templateText(stream, state, frame.kind === "string")
}

function tokenCode(stream: StringStream, state: HclState): string | null {
  const firstOnLine = state.lineStart
  state.lineStart = false
  const frame = state.stack[state.stack.length - 1]

  if (stream.match("#") || stream.match("//")) {
    stream.skipToEnd()
    return "comment"
  }
  if (stream.match("/*")) {
    state.blockComment = true
    return tokenBlockComment(stream, state)
  }
  const heredoc = stream.match(/^<<(-?)([A-Za-z_][\w-]*)/) as RegExpMatchArray | null
  if (heredoc) {
    state.stack.push({ kind: "heredoc", tag: heredoc[2], indented: heredoc[1] === "-" })
    return "keyword"
  }
  if (stream.peek() === '"') {
    stream.next()
    state.stack.push({ kind: "string" })
    return templateText(stream, state, true)
  }
  if (frame?.kind === "interp") {
    if (stream.peek() === "{") {
      frame.depth++
      stream.next()
      return "bracket"
    }
    if (stream.match(/^~?\}/)) {
      if (frame.depth === 0) {
        state.stack.pop()
        return "processingInstruction"
      }
      frame.depth--
      return "bracket"
    }
  }
  if (stream.match(/^\d+(\.\d+)?([eE][+-]?\d+)?/)) return "number"
  const word = stream.match(/^[A-Za-z_][\w-]*/) as RegExpMatchArray | null
  if (word) {
    const w = word[0]
    if (ATOMS[w]) return "atom"
    if (stream.match(/^\s*\(/, false)) return "variableName.function"
    if (stream.match(/^\s*=(?!=)/, false) || (frame?.kind !== "interp" && stream.match(/^\s*:(?!:)/, false))) {
      return CONTROL_KEYWORDS[w] ? "keyword" : "propertyName"
    }
    if (firstOnLine && BLOCK_KEYWORDS[w] && frame === undefined) return "keyword"
    if (CONTROL_KEYWORDS[w]) return "keyword"
    if (firstOnLine && frame === undefined && stream.match(/^\s*(\{|")/, false)) return "typeName"
    if (ROOT_REFS[w] && stream.peek() === ".") return "variableName.special"
    return "variableName"
  }
  if (stream.match(/^(==|!=|<=|>=|&&|\|\||=>|\.\.\.|[-+*/%<>!?:=])/)) return "operator"
  if (stream.match(/^[[\](){}]/)) return "bracket"
  if (stream.match(/^[.,]/)) return "punctuation"
  stream.next()
  return null
}

function tokenBlockComment(stream: StringStream, state: HclState): string {
  while (!stream.eol()) {
    if (stream.match("*/")) {
      state.blockComment = false
      break
    }
    stream.next()
  }
  return "comment"
}

const hclParser: StreamParser<HclState> = {
  name: "hcl",
  startState: () => ({ stack: [], blockComment: false, lineStart: true }),
  copyState: (s) => ({ stack: s.stack.map((f) => ({ ...f })), blockComment: s.blockComment, lineStart: s.lineStart }),
  token(stream, state) {
    if (stream.sol()) state.lineStart = true
    if (state.blockComment) {
      state.lineStart = false
      return tokenBlockComment(stream, state)
    }
    const frame = state.stack[state.stack.length - 1]
    if (frame && frame.kind !== "interp") return tokenTemplate(stream, state, frame)
    if (stream.eatSpace()) return null
    return tokenCode(stream, state)
  },
  blankLine(state) {
    state.lineStart = true
  },
  languageData: { commentTokens: { line: "#", block: { open: "/*", close: "*/" } } },
}

export const hclLanguage = StreamLanguage.define(hclParser)

export const hclHighlightLight = HighlightStyle.define([
  { tag: t.comment, color: "#6a737d", fontStyle: "italic" },
  { tag: t.keyword, color: "#a626a4", fontWeight: "600" },
  { tag: t.typeName, color: "#a626a4" },
  { tag: t.string, color: "#2f7d32" },
  { tag: t.processingInstruction, color: "#c2410c", fontWeight: "600" },
  { tag: t.number, color: "#b45309" },
  { tag: t.atom, color: "#0e7490" },
  { tag: t.propertyName, color: "#1d4ed8" },
  { tag: t.function(t.variableName), color: "#7c3aed" },
  { tag: t.special(t.variableName), color: "#be123c" },
  { tag: t.operator, color: "#525252" },
])

export const hclHighlightDark = HighlightStyle.define([
  { tag: t.comment, color: "#7f848e", fontStyle: "italic" },
  { tag: t.keyword, color: "#c678dd", fontWeight: "600" },
  { tag: t.typeName, color: "#c678dd" },
  { tag: t.string, color: "#98c379" },
  { tag: t.processingInstruction, color: "#f59e0b", fontWeight: "600" },
  { tag: t.number, color: "#d19a66" },
  { tag: t.atom, color: "#56b6c2" },
  { tag: t.propertyName, color: "#61afef" },
  { tag: t.function(t.variableName), color: "#c4a7ff" },
  { tag: t.special(t.variableName), color: "#e06c75" },
  { tag: t.operator, color: "#a3a3a3" },
])
