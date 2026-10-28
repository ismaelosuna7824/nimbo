import { describe, expect, test } from "bun:test"
import { appendToList, base64Of, isJsonAttr, jsonFieldValue, listItems, listValue, mapEntries, mapValue, removeListItem, splitTopLevel, stringLiteral } from "./body"

describe("list values", () => {
  test("listItems reads literals and list constructors", () => {
    expect(listItems(undefined)).toEqual([])
    expect(listItems({ literal: ["a", "b"] })).toEqual(['"a"', '"b"'])
    expect(listItems({ expr: '["a", aws_security_group.alb.id]' })).toEqual(['"a"', "aws_security_group.alb.id"])
    expect(listItems({ expr: '[\n  "x",\n  var.y,\n]' })).toEqual(['"x"', "var.y"])
    expect(listItems({ expr: '["${var.a}-b", lookup(m, "k, v")]' })).toEqual(['"${var.a}-b"', 'lookup(m, "k, v")'])
  })

  test("listItems refuses other expressions", () => {
    expect(listItems({ expr: "data.aws_subnets.default.ids" })).toBeNull()
    expect(listItems({ expr: "[for s in aws_subnet.a : s.id]" })).toBeNull()
    expect(listItems({ expr: "[a] + [b]" })).toBeNull()
    expect(listItems({ expr: "concat([a], [b])" })).toBeNull()
    expect(listItems({ expr: "[a, # comment\n b]" })).toBeNull()
    expect(listItems({ literal: "x" })).toBeNull()
  })

  test("appendToList adds a reference to the list", () => {
    expect(appendToList(undefined, "aws_security_group.alb.id")).toEqual({ expr: "[aws_security_group.alb.id]" })
    expect(appendToList({ literal: ["sg-1"] }, "aws_security_group.alb.id")).toEqual({ expr: '["sg-1", aws_security_group.alb.id]' })
    expect(appendToList({ expr: "[a.b.id, c.d.id]" }, "e.f.id")).toEqual({ expr: "[a.b.id, c.d.id, e.f.id]" })
    expect(appendToList({ expr: "[a.b.id]" }, "a.b.id")).toEqual({ expr: "[a.b.id]" })
    // Not a list constructor: the link replaces it.
    expect(appendToList({ expr: "data.x.y.ids" }, "e.f.id")).toEqual({ expr: "[e.f.id]" })
  })

  test("removing elements returns to a literal or unset", () => {
    expect(removeListItem({ expr: '["sg-1", aws_security_group.alb.id]' }, 1)).toEqual({ literal: ["sg-1"] })
    expect(removeListItem({ expr: "[a.b.id]" }, 0)).toBeUndefined()
    expect(removeListItem({ expr: "data.x.y.ids" }, 0)).toEqual({ expr: "data.x.y.ids" })
    expect(listValue(['"a"', '"b"'])).toEqual({ literal: ["a", "b"] })
  })

  test("splitTopLevel and stringLiteral", () => {
    expect(splitTopLevel("a = 1\nb = [1,\n 2], c = { x = 1 }", true)).toEqual(["a = 1", "b = [1,\n 2]", "c = { x = 1 }"])
    expect(splitTopLevel("a, (b", false)).toBeNull()
    expect(splitTopLevel('"unterminated', false)).toBeNull()
    expect(splitTopLevel("<<EOT\nx\nEOT", false)).toBeNull()
    expect(stringLiteral('"a\\"b"')).toBe('a"b')
    expect(stringLiteral('"${x}"')).toBeUndefined()
    expect(stringLiteral("var.x")).toBeUndefined()
  })
})

describe("map values", () => {
  test("mapEntries keeps expression values", () => {
    expect(mapEntries(undefined)).toEqual([])
    expect(mapEntries({ literal: { Env: "prod" } })).toEqual([{ name: "Env", value: { literal: "prod" } }])
    expect(mapEntries({ expr: '{\n  DB_HOST = var.db_host\n  "app.mode" = "web"\n  Name = "${local.p}-x"\n}' })).toEqual([
      { name: "DB_HOST", value: { expr: "var.db_host" } },
      { name: "app.mode", value: { literal: "web" } },
      { name: "Name", value: { expr: '"${local.p}-x"' } },
    ])
    expect(mapEntries({ expr: "merge(local.tags, { a = 1 })" })).toBeNull()
    expect(mapEntries({ expr: "local.common_tags" })).toBeNull()
  })

  test("mapValue writes literals or a constructor, untouched expressions included", () => {
    expect(mapValue([])).toBeUndefined()
    expect(mapValue([{ name: "Env", value: { literal: "prod" } }])).toEqual({ literal: { Env: "prod" } })
    const v = mapValue([
      { name: "DB_HOST", value: { expr: "var.db_host" } },
      { name: "app.mode", value: { literal: "web" } },
      { name: "LoadBalancer", value: { expr: "aws_lb.app.arn_suffix" } },
    ])
    expect(v).toEqual({ expr: '{\n  DB_HOST = var.db_host\n  "app.mode" = "web"\n  LoadBalancer = aws_lb.app.arn_suffix\n}' })
    expect(mapEntries(v)).toEqual([
      { name: "DB_HOST", value: { expr: "var.db_host" } },
      { name: "app.mode", value: { literal: "web" } },
      { name: "LoadBalancer", value: { expr: "aws_lb.app.arn_suffix" } },
    ])
    expect(base64Of("local.user_data")).toBe("base64encode(local.user_data)")
  })
})

describe("JSON document fields", () => {
  test("policy-like string attributes are JSON documents", () => {
    for (const name of ["assume_role_policy", "policy", "redrive_policy", "definition", "container_definitions", "event_pattern", "policy_json"])
      expect(isJsonAttr(name)).toBe(true)
    for (const name of ["name", "policy_arn", "policy_arns", "description"]) expect(isJsonAttr(name)).toBe(false)
    expect(isJsonAttr("settings", "A JSON object of settings.")).toBe(true)
  })

  test("pasted JSON becomes jsonencode, expressions stay, text is a string, empty unsets", () => {
    const doc = '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"sts:AssumeRole"}]}'
    expect(jsonFieldValue(doc)).toEqual({ expr: `jsonencode(${JSON.stringify(JSON.parse(doc), null, 2)})` })
    expect(jsonFieldValue("jsonencode({ Version = \"2012-10-17\" })")).toEqual({ expr: 'jsonencode({ Version = "2012-10-17" })' })
    expect(jsonFieldValue("data.aws_iam_policy_document.assume.json")).toEqual({ expr: "data.aws_iam_policy_document.assume.json" })
    // Half-typed JSON is kept as typed (an expression), so nothing is lost and Problems shows the mistake.
    expect(jsonFieldValue('{"Version": ')).toEqual({ expr: '{"Version":' })
    expect(jsonFieldValue("  ")).toBeUndefined()
    expect(jsonFieldValue("not json")).toEqual({ literal: "not json" })
  })
})
