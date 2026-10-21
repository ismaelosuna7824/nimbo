import { describe, expect, test } from "bun:test"
import { hclIndentAt } from "./hcl-indent"

/** Indentation of the line that starts at "|" (removed from the text). */
function indentAt(src: string): number | null {
  const at = src.indexOf("|")
  return hclIndentAt(src.slice(0, at) + src.slice(at + 1), at)
}

describe("hclIndentAt", () => {
  test("indents inside blocks by nesting depth", () => {
    expect(indentAt('resource "aws_sqs_queue" "jobs" {\n|')).toBe(2)
    expect(indentAt('resource "a" "b" {\n  lifecycle {\n|')).toBe(4)
  })

  test("a closing bracket lines up with its opener", () => {
    expect(indentAt('resource "a" "b" {\n  tags = {\n    a = 1\n|}')).toBe(2)
    expect(indentAt('locals {\n  x = [\n    1,\n|]')).toBe(2)
  })

  test("brackets inside strings, interpolations and comments don't count", () => {
    expect(indentAt('locals {\n  x = "{ ${var.a} ["\n  # }\n|')).toBe(2)
  })

  test("inside a heredoc the text keeps its own indentation", () => {
    expect(indentAt("locals {\n  doc = <<EOT\n{\n|")).toBeNull()
  })

  test("top level after a closed block", () => {
    expect(indentAt('variable "a" {\n  type = string\n}\n|')).toBe(0)
  })
})
