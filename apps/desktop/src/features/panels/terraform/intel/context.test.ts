import { describe, expect, test } from "bun:test"
import { contextAt, referenceAt, type CursorContext } from "./context"

/** contextAt with the cursor at "|" (removed from the text). */
function at(src: string): CursorContext {
  const pos = src.indexOf("|")
  return contextAt(src.slice(0, pos) + src.slice(pos + 1), pos)
}

const pathOf = (c: CursorContext) =>
  c.kind === "item" || c.kind === "value" ? c.path.map((b) => [b.type, ...b.labels.map((l) => l.text)].join(" ")) : []

describe("contextAt", () => {
  test("top level", () => {
    const c = at('resource "aws_s3_bucket" "b" {\n}\n\nres|')
    expect(c).toMatchObject({ kind: "item", prefix: "res", path: [] })
  })

  test('resource type after resource "aws_', () => {
    const c = at('resource "aws_|')
    expect(c).toMatchObject({ kind: "label", index: 0, prefix: "aws_", quoted: true, closed: false })
    expect(c.kind === "label" && c.block.type).toBe("resource")
  })

  test("resource type inside a closed label with a body below", () => {
    const c = at('resource "aws_sq|" "q" {\n  name = "x"\n}\n')
    expect(c).toMatchObject({ kind: "label", index: 0, prefix: "aws_sq", closed: true })
  })

  test("bare type without quotes", () => {
    expect(at("data aws_|")).toMatchObject({ kind: "label", index: 0, prefix: "aws_", quoted: false })
  })

  test("argument names in a resource body, while another block follows", () => {
    const src = 'resource "aws_sqs_queue" "q" {\n  name = "jobs"\n  visibility|\n}\n\nresource "aws_sns_topic" "t" {}\n'
    const c = at(src)
    expect(c).toMatchObject({ kind: "item", prefix: "visibility" })
    expect(pathOf(c)).toEqual(["resource aws_sqs_queue q"])
    expect(c.kind === "item" && c.path[0].attrs.map((a) => a.name)).toEqual(["name"])
  })

  test("nested blocks", () => {
    const src = 'resource "aws_s3_bucket" "b" {\n  versioning {\n    ena|\n  }\n}\n'
    expect(pathOf(at(src))).toEqual(["resource aws_s3_bucket b", "versioning"])
  })

  test("unclosed block followed by a top-level block", () => {
    const src = 'resource "aws_s3_bucket" "b" {\n  buck|\n\nresource "aws_sns_topic" "t" {\n}\n'
    expect(pathOf(at(src))).toEqual(["resource aws_s3_bucket b"])
    const after = at('resource "aws_s3_bucket" "b" {\n  bucket = "x"\n\nresource "aws_sns_topic" "t" {\n  na|\n}\n')
    expect(pathOf(after)).toEqual(["resource aws_sns_topic t"])
  })

  test("reference after aws_s3_bucket.main.", () => {
    const c = at('resource "aws_s3_bucket" "main" {}\noutput "o" {\n  value = aws_s3_bucket.main.|\n}\n')
    expect(c).toMatchObject({ kind: "value", chain: ["aws_s3_bucket", "main"], partial: "" })
    expect(c.kind === "value" && c.attr.name).toBe("value")
  })

  test("partial attribute with an index", () => {
    const c = at("output \"o\" {\n  value = aws_instance.web[0].pub|\n}\n")
    expect(c).toMatchObject({ kind: "value", chain: ["aws_instance", "web", "[]"], partial: "pub" })
  })

  test("var., local. and module outputs", () => {
    expect(at("locals {\n  a = var.|\n}\n")).toMatchObject({ kind: "value", chain: ["var"], partial: "" })
    expect(at("locals {\n  a = local.na|\n}\n")).toMatchObject({ kind: "value", chain: ["local"], partial: "na" })
    expect(at("locals {\n  a = module.vpc.|\n}\n")).toMatchObject({ kind: "value", chain: ["module", "vpc"] })
  })

  test("inside string interpolation", () => {
    const c = at('resource "aws_s3_bucket" "b" {\n  bucket = "${var.prefix}-${aws_sqs_queue.q.|}"\n}\n')
    expect(c).toMatchObject({ kind: "value", chain: ["aws_sqs_queue", "q"], string: null })
    const open = at('resource "aws_s3_bucket" "b" {\n  bucket = "${local.|\n}\n')
    expect(open).toMatchObject({ kind: "value", chain: ["local"] })
  })

  test("inside a string literal", () => {
    const c = at('resource "aws_lambda_function" "f" {\n  runtime = "pyt|"\n}\n')
    expect(c).toMatchObject({ kind: "value", string: { prefix: "pyt" } })
    if (c.kind === "value") expect(c.string!.to - c.string!.from).toBe(3)
  })

  test("function calls", () => {
    const c = at('locals {\n  a = join(",", [for s in var.x : upper(s)], |)\n}\n')
    expect(c).toMatchObject({ kind: "value", call: { name: "join", arg: 2 } })
    expect(at("locals {\n  a = upp|\n}\n")).toMatchObject({ kind: "value", chain: [], partial: "upp" })
  })

  test("unclosed call keeps the next attribute separate", () => {
    const src = 'resource "aws_s3_bucket" "b" {\n  bucket = format("%s", |\n  tags = {}\n}\n'
    const c = at(src)
    expect(c).toMatchObject({ kind: "value", call: { name: "format", arg: 1 } })
    if (c.kind === "value") expect(c.path[0].attrs.map((a) => a.name)).toEqual(["bucket", "tags"])
  })

  test("inside a module file", () => {
    const c = at('variable "cidr" {\n  type = string\n}\nresource "aws_vpc" "this" {\n  cidr_block = var.c|\n}\n')
    expect(c).toMatchObject({ kind: "value", chain: ["var"], partial: "c" })
  })

  test("heredocs and comments", () => {
    const src = 'locals {\n  # a comment |\n  p = <<EOT\n${var.x}\nEOT\n  q = 1\n}\n'
    expect(at(src).kind).toBe("none")
    const after = at('locals {\n  p = <<EOT\n${var.x}\nEOT\n  q|\n}\n')
    expect(after).toMatchObject({ kind: "item", prefix: "q" })
  })

  test("variable type", () => {
    expect(at('variable "x" {\n  type = |\n}\n')).toMatchObject({ kind: "value", chain: [], partial: "" })
  })
})

describe("referenceAt", () => {
  test("segments and the one under the cursor", () => {
    const text = "  value = aws_instance.web[0].public_ip\n"
    const r = referenceAt(text, text.indexOf("web") + 1)
    expect(r).toMatchObject({ segs: ["aws_instance", "web", "[]", "public_ip"], index: 1 })
    expect(referenceAt("x = var.name", 10)).toMatchObject({ segs: ["var", "name"], index: 1 })
  })
})
