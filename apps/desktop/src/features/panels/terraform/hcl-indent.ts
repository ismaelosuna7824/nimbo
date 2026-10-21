import { indentService, indentUnit } from "@codemirror/language"
import type { Extension } from "@codemirror/state"
import { lex } from "./intel/scan"

const OPEN: Record<string, true> = { "{": true, "[": true, "(": true }
const CLOSE: Record<string, true> = { "}": true, "]": true, ")": true }

/**
 * Indentation of the line starting at `lineStart`: two spaces per bracket
 * still open before it, one less when the line starts with a closer. Null
 * inside a string or heredoc (their text keeps its own indentation).
 */
export function hclIndentAt(text: string, lineStart: number, unit = 2): number | null {
  const { tokens } = lex(text)
  let depth = 0
  let last: (typeof tokens)[number] | undefined
  for (const t of tokens) {
    if (t.from >= lineStart) {
      if (t.kind === "newline") continue
      // The line continues a string or heredoc.
      if (t.str > 0 || t.kind === "strText" || t.kind === "strClose" || t.kind === "heredocClose") return null
      if (t.kind === "punct" && CLOSE[t.text]) depth = Math.max(0, depth - 1)
      return depth * unit
    }
    if (t.kind !== "newline") last = t
    if (t.str > 0 || t.kind !== "punct") continue
    if (OPEN[t.text]) depth++
    else if (CLOSE[t.text]) depth = Math.max(0, depth - 1)
  }
  // Nothing on the line yet: inside an unterminated string or heredoc?
  if (last && (last.kind === "strOpen" || last.kind === "strText" || last.kind === "heredocOpen" || (last.str > 0 && last.kind !== "strClose"))) {
    return null
  }
  return depth * unit
}

/** Brace-depth indentation for HCL, so Enter and closing brackets indent like terraform fmt. */
export const hclIndentation: Extension = [
  indentUnit.of("  "),
  indentService.of((ctx, pos) => {
    const line = ctx.lineAt(pos)
    // Text before the line plus what follows the cursor on it (the part that moves to the new line).
    const text = ctx.state.doc.sliceString(0, line.from) + ctx.textAfterPos(pos)
    return hclIndentAt(text, line.from)
  }),
]
