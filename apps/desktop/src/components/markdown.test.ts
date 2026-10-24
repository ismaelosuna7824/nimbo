import { describe, expect, test } from "bun:test"
import { isValidElement, type ReactElement, type ReactNode } from "react"
import { inline, parseMarkdown } from "./markdown"

describe("parseMarkdown", () => {
  test("splits paragraphs, headings, lists and code blocks", () => {
    const md = [
      "# Title",
      "First line",
      "continues here.",
      "",
      "- one",
      "  wrapped",
      "- two",
      "1. first",
      "2) second",
      "```yaml",
      "a: 1",
      "",
      "b: 2",
      "```",
      "After.",
    ].join("\n")
    expect(parseMarkdown(md)).toEqual([
      { kind: "h", level: 1, text: "Title" },
      { kind: "p", text: "First line continues here." },
      { kind: "ul", items: ["one wrapped", "two"] },
      { kind: "ol", items: ["first", "second"] },
      { kind: "code", lang: "yaml", text: "a: 1\n\nb: 2" },
      { kind: "p", text: "After." },
    ])
  })

  test("an unterminated code fence keeps the rest as code", () => {
    expect(parseMarkdown("```\nx\ny")).toEqual([{ kind: "code", lang: "", text: "x\ny" }])
  })
})

describe("inline", () => {
  const kinds = (nodes: ReactNode[]) =>
    nodes.map((n) => (typeof n === "string" ? n : isValidElement(n) ? String((n as ReactElement).type) : "?"))

  test("code, bold, italic and links become elements", () => {
    expect(kinds(inline("Use `kubectl` with **care**, *really*, see [docs](https://kubernetes.io/docs/).")))
      .toEqual(["Use ", "code", " with ", "strong", ", ", "em", ", see ", "a", "."])
  })

  test("links that are not http(s) render as text, never as anchors", () => {
    const nodes = inline("[click](javascript:alert(1))")
    expect(kinds(nodes)).not.toContain("a")
  })

  test("markup characters inside code are literal", () => {
    const nodes = inline("`**not bold**`")
    expect(kinds(nodes)).toEqual(["code"])
  })

  test("snake_case names are not italic", () => {
    expect(kinds(inline("Fix bp_missing_tags and _this_."))).toEqual(["Fix bp_missing_tags and ", "em", "."])
  })
})
