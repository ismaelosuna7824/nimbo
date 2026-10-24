import { describe, expect, test } from "bun:test"
import type { CtyType } from "@/lib/api/types"
import { objectListFields, objectListFromValue, objectListToValue } from "./object-list"

const ingress: CtyType = ["set", ["object", { from_port: "number", protocol: "string", security_groups: ["set", "string"] }]]

describe("object lists", () => {
  test("fields of list/set(object) types", () => {
    expect(objectListFields(ingress)?.map((f) => [f.name, f.type])).toEqual([
      ["from_port", "number"],
      ["protocol", "string"],
      ["security_groups", ["set", "string"]],
    ])
    expect(objectListFields(["list", "string"])).toBeNull()
    expect(objectListFields(["object", { a: "string" }])).toBeNull()
  })

  test("reads literals and object constructors", () => {
    expect(objectListFromValue(undefined)).toEqual([])
    expect(objectListFromValue({ literal: [{ from_port: 80, protocol: "tcp", self: null }] })).toEqual([
      { attrs: [{ name: "from_port", value: { literal: 80 } }, { name: "protocol", value: { literal: "tcp" } }], blocks: [] },
    ])
    const v = objectListFromValue({
      expr: '[\n  {\n    from_port = 443\n    "protocol" = "tcp"\n    security_groups = [aws_security_group.alb.id]\n  },\n  { from_port = 22, protocol = "${var.p}" },\n]',
    })
    expect(v).toEqual([
      {
        attrs: [
          { name: "from_port", value: { literal: 443 } },
          { name: "protocol", value: { literal: "tcp" } },
          { name: "security_groups", value: { expr: "[aws_security_group.alb.id]" } },
        ],
        blocks: [],
      },
      { attrs: [{ name: "from_port", value: { literal: 22 } }, { name: "protocol", value: { expr: '"${var.p}"' } }], blocks: [] },
    ])
  })

  test("other expressions are not representable", () => {
    expect(objectListFromValue({ expr: "[for r in var.rules : { from_port = r.port }]" })).toBeNull()
    expect(objectListFromValue({ expr: "var.rules" })).toBeNull()
    expect(objectListFromValue({ expr: "[{ for k, v in m : k => v }]" })).toBeNull()
    expect(objectListFromValue({ expr: "[merge(a, b)]" })).toBeNull()
    expect(objectListFromValue({ literal: ["x"] })).toBeNull()
  })

  test("writes literals when constant, HCL otherwise, and round-trips", () => {
    const items = [{ attrs: [{ name: "from_port", value: { literal: 80 } }], blocks: [] }]
    expect(objectListToValue(items)).toEqual({ literal: [{ from_port: 80 }] })
    const mixed = [
      {
        attrs: [
          { name: "from_port", value: { literal: 5432 } },
          { name: "security_groups", value: { expr: "[aws_security_group.web.id]" } },
        ],
        blocks: [],
      },
    ]
    const out = objectListToValue(mixed)
    expect(out).toEqual({ expr: "[\n  {\n    from_port = 5432\n    security_groups = [aws_security_group.web.id]\n  },\n]" })
    expect(objectListFromValue(out)).toEqual(mixed)
    expect(objectListToValue([])).toEqual({ literal: [] })
  })
})
