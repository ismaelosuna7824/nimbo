import { describe, expect, test } from "bun:test"
import { evaluate, liveBranch, parseType, UNKNOWN, withDefaults } from "./expr"

const VARS: Record<string, unknown> = {
  "var.create": true,
  "var.name": "shop",
  "var.subnets": ["a", "b"],
  "var.tags": {},
  "var.types": { sites: "Microsoft.Web/sites@2024-04-01" },
  "var.nothing": null,
  "local.prefix": "dev",
}
const lookup = (kind: "var" | "local", name: string) => (`${kind}.${name}` in VARS ? VARS[`${kind}.${name}`] : UNKNOWN)
const ev = (src: string) => evaluate(src, lookup)

describe("evaluate", () => {
  test("literals, references, interpolation and operators", () => {
    expect(ev('"${local.prefix}-${var.name}"')).toBe("dev-shop")
    expect(ev("var.create ? 1 : 0")).toBe(1)
    expect(ev("!var.create || length(var.subnets) > 1")).toBe(true)
    expect(ev("var.nothing != null ? 1 : 0")).toBe(0)
    expect(ev('var.types.sites')).toBe("Microsoft.Web/sites@2024-04-01")
    expect(ev('var.types["sites"]')).toBe("Microsoft.Web/sites@2024-04-01")
    expect(ev("2 * 3 + 1")).toBe(7)
  })

  test("collections, for expressions and functions", () => {
    expect(ev('{ for s in var.subnets : s => upper(s) if s != "b" }')).toEqual({ a: "A" })
    expect(ev("[for s in var.subnets : s]")).toEqual(["a", "b"])
    expect(ev('merge(var.tags, { env = "dev" })')).toEqual({ env: "dev" })
    expect(ev('lookup({ a = 1 }, "b", 2)')).toBe(2)
    expect(ev("toset(concat(var.subnets, [\"a\"]))")).toEqual(["a", "b"])
    expect(ev('try(aws_instance.x.id, "none")')).toBe("none")
    expect(ev('format("%s-%d", var.name, 3)')).toBe("shop-3")
  })

  test("what only Terraform knows is unknown, and spreads", () => {
    expect(ev("aws_instance.web.id")).toBe(UNKNOWN)
    expect(ev("var.missing ? 1 : 0")).toBe(UNKNOWN)
    expect(ev("length(var.missing)")).toBe(UNKNOWN)
    // An unknown condition with equal branches is still known.
    expect(ev("var.missing ? 1 : 1")).toBe(1)
    expect(ev("var.missing && false")).toBe(false)
    expect(ev("<<EOT\nx\nEOT")).toBe(UNKNOWN)
  })
})

describe("conditionals and types", () => {
  test("the branch a conditional takes", () => {
    expect(liveBranch("var.create ? aws_nat_gateway.a.id : aws_internet_gateway.b.id", lookup)).toBe("aws_nat_gateway.a.id")
    expect(liveBranch('!var.create ? "x" : (var.nothing == null ? oci_core_route_table.b.id : "y")', lookup)).toBe("oci_core_route_table.b.id")
    expect(liveBranch("var.missing ? a.b : c.d", lookup)).toBeUndefined()
    expect(liveBranch('"a ? b : c"', lookup)).toBeUndefined()
  })

  test("optional attributes of object types take their defaults", () => {
    const type = parseType('object({ sites = optional(string, "Microsoft.Web/sites@2024-04-01"), nested = optional(object({ on = optional(bool, true) }), {}), plain = optional(string) })')
    expect(withDefaults({}, type)).toEqual({ sites: "Microsoft.Web/sites@2024-04-01", nested: { on: true } })
    expect(withDefaults({ sites: "x" }, type)).toEqual({ sites: "x", nested: { on: true } })
  })
})
