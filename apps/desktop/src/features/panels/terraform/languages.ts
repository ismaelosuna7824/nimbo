import { HighlightStyle, StreamLanguage, type StreamParser } from "@codemirror/language"
import type { Extension } from "@codemirror/state"
import { tags as t } from "@lezer/highlight"
import type { FileLanguage } from "@/lib/api/types"
import { hclLanguage } from "./hcl-language"

/** Headings, emphasis, code, links, lists and quotes: enough to read a README. */
const markdownParser: StreamParser<{ fence: boolean }> = {
  name: "markdown",
  startState: () => ({ fence: false }),
  token(stream, state) {
    if (stream.sol() && stream.match(/^\s*(```|~~~)/)) {
      state.fence = !state.fence
      stream.skipToEnd()
      return "meta"
    }
    if (state.fence) {
      stream.skipToEnd()
      return "string"
    }
    if (stream.sol()) {
      if (stream.match(/^#{1,6}\s.*/)) return "header"
      if (stream.match(/^\s*>.*/)) return "quote"
      if (stream.match(/^\s*([-*+]|\d+\.)\s/)) return "keyword"
      if (stream.match(/^\s*([-*_]\s*){3,}$/)) return "meta"
    }
    if (stream.match(/^`[^`]+`/)) return "string"
    if (stream.match(/^\*\*[^*]+\*\*/) || stream.match(/^__[^_]+__/)) return "strong"
    if (stream.match(/^\*[^*\s][^*]*\*/) || stream.match(/^_[^_\s][^_]*_/)) return "em"
    if (stream.match(/^!?\[[^\]]*\]\([^)]*\)/)) return "link"
    if (stream.match(/^https?:\/\/\S+/)) return "link"
    stream.next()
    while (!stream.eol() && !/[`*_[!h]/.test(stream.peek()!)) stream.next()
    return null
  },
}

const loaded: Partial<Record<FileLanguage, Extension>> = { hcl: hclLanguage, text: [] }

/** The language extension for a file; modes load on first use, each in its own chunk. */
export async function loadLanguage(lang: FileLanguage): Promise<Extension> {
  const cached = loaded[lang]
  if (cached) return cached
  let parser: StreamParser<unknown>
  // Dynamic on purpose: a mode is only downloaded when a file in that language is opened.
  switch (lang) {
    case "javascript":
      parser = (await import("@codemirror/legacy-modes/mode/javascript")).javascript
      break
    case "typescript":
      parser = (await import("@codemirror/legacy-modes/mode/javascript")).typescript
      break
    case "json":
      parser = (await import("@codemirror/legacy-modes/mode/javascript")).json
      break
    case "python":
      parser = (await import("@codemirror/legacy-modes/mode/python")).python
      break
    case "go":
      parser = (await import("@codemirror/legacy-modes/mode/go")).go
      break
    case "yaml":
      parser = (await import("@codemirror/legacy-modes/mode/yaml")).yaml
      break
    case "shell":
      parser = (await import("@codemirror/legacy-modes/mode/shell")).shell
      break
    case "markdown":
      parser = markdownParser as StreamParser<unknown>
      break
    default:
      return []
  }
  return (loaded[lang] = StreamLanguage.define(parser))
}

/** Light syntax colors; the HCL entries come first and set the look of Terraform code. */
export const codeHighlightLight = HighlightStyle.define([
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
  { tag: t.definition(t.variableName), color: "#1d4ed8" },
  { tag: t.standard(t.variableName), color: "#b45309" },
  { tag: t.special(t.string), color: "#0e7490" },
  { tag: [t.meta, t.modifier], color: "#c2410c" },
  { tag: t.tagName, color: "#be123c" },
  { tag: t.attributeName, color: "#b45309" },
  { tag: t.heading, color: "#be123c", fontWeight: "700" },
  { tag: t.emphasis, fontStyle: "italic" },
  { tag: t.strong, fontWeight: "700" },
  { tag: t.link, color: "#1d4ed8", textDecoration: "underline" },
  { tag: t.quote, color: "#2f7d32", fontStyle: "italic" },
  { tag: t.invalid, color: "#dc2626" },
])

/** Dark syntax colors (One Dark-like), same tag set as the light style. */
export const codeHighlightDark = HighlightStyle.define([
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
  { tag: t.definition(t.variableName), color: "#61afef" },
  { tag: t.standard(t.variableName), color: "#e5c07b" },
  { tag: t.special(t.string), color: "#56b6c2" },
  { tag: [t.meta, t.modifier], color: "#d19a66" },
  { tag: t.tagName, color: "#e06c75" },
  { tag: t.attributeName, color: "#d19a66" },
  { tag: t.heading, color: "#e06c75", fontWeight: "700" },
  { tag: t.emphasis, fontStyle: "italic" },
  { tag: t.strong, fontWeight: "700" },
  { tag: t.link, color: "#61afef", textDecoration: "underline" },
  { tag: t.quote, color: "#98c379", fontStyle: "italic" },
  { tag: t.invalid, color: "#f87171" },
])
