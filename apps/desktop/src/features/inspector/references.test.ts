import { describe, expect, test } from "bun:test"
import type { SchemaBlock } from "@/lib/api/types"
import { instanceKeys, linkableAttributes, localType, nestedReferences, parseTypeExpr, refFit, scopeReferences } from "./references"

const firewall: SchemaBlock = {
  attributes: [
    { name: "name", type: "string", required: true },
    { name: "id", type: "string", computed: true, optional: true },
    { name: "threat_intel_mode", type: "string", computed: true, optional: true },
  ],
  blocks: [
    {
      name: "ip_configuration",
      nesting: "list",
      block: {
        attributes: [
          { name: "name", type: "string", required: true },
          { name: "private_ip_address", type: "string", computed: true },
        ],
        blocks: [],
      },
    },
    {
      name: "virtual_hub",
      nesting: "single",
      block: {
        attributes: [{ name: "public_ip_addresses", type: ["list", "string"], computed: true }],
        blocks: [
          { name: "deep", nesting: "list", block: { attributes: [{ name: "x", type: "string", computed: true }], blocks: [{ name: "deeper", nesting: "list", block: { attributes: [{ name: "y", type: "string", computed: true }], blocks: [] } }] } },
        ],
      },
    },
    { name: "rule", nesting: "set", block: { attributes: [{ name: "id", type: "string", computed: true }], blocks: [] } },
  ],
}

const services: SchemaBlock = {
  attributes: [
    { name: "id", type: "string", computed: true },
    { name: "names", type: ["list", "string"], computed: true },
    { name: "zone_ids", type: ["set", "string"], computed: true },
    { name: "services", type: ["list", ["object", { id: "string", cidr_block: "string", name: "string" }]], computed: true },
    { name: "tags", type: ["set", ["object", { key: "string" }]], computed: true },
  ],
  blocks: [],
}

describe("Link to… options", () => {
  test("top-level: preferred outputs first, then computed attributes", () => {
    expect(linkableAttributes(firewall).map((o) => o.path)).toEqual(["id", "name", "threat_intel_mode"])
  })

  test("nested: block attributes and list(object) elements", () => {
    expect(nestedReferences(firewall).map((o) => o.path)).toEqual([
      "ip_configuration[0].private_ip_address",
      "virtual_hub.public_ip_addresses",
      "virtual_hub.deep[0].x",
    ])
    expect(nestedReferences(services)).toEqual([
      { path: "names[0]", type: "string" },
      { path: "names[1]", type: "string" },
      { path: "names[2]", type: "string" },
      { path: "services[0].cidr_block", type: "string" },
      { path: "services[0].id", type: "string" },
      { path: "services[0].name", type: "string" },
    ])
  })

  test("variables and locals with their types", () => {
    const opts = scopeReferences(
      [
        { name: "location", body: { attrs: [{ name: "type", value: { expr: "string" } }], blocks: [] } },
        { name: "zones", body: { attrs: [{ name: "type", value: { expr: "list(string)" } }], blocks: [] } },
        { name: "anything", body: { attrs: [], blocks: [] } },
        { name: "settings", body: { attrs: [{ name: "type", value: { expr: "object({ a = string })" } }], blocks: [] } },
      ],
      [
        { name: "prefix", value: { expr: '"app"', literal: "app" } },
        { name: "subnet_ids", value: { expr: "sort(data.aws_subnets.default.ids)" } },
      ],
    )
    expect(opts).toEqual([
      { path: "var.location", type: "string" },
      { path: "var.zones", type: ["list", "string"] },
      { path: "var.anything", type: "dynamic" },
      { path: "local.prefix", type: "string" },
      { path: "local.subnet_ids", type: "dynamic" },
    ])
    expect(parseTypeExpr("map( string )")).toEqual(["map", "string"])
    expect(parseTypeExpr("constructor")).toBeNull()
    expect(localType({ literal: { a: "b" } })).toEqual(["map", "string"])
  })

  test("type fit: replace, append into string lists, or nothing", () => {
    expect(refFit("string", "number")).toBe("replace")
    expect(refFit(["list", "string"], "string")).toBe("append")
    expect(refFit(["set", "string"], ["list", "string"])).toBe("replace")
    expect(refFit(["list", "string"], "dynamic")).toBe("replace")
    expect(refFit("string", ["list", "string"])).toBeNull()
    expect(refFit("bool", "number")).toBeNull()
    expect(refFit(["map", "string"], "string")).toBeNull()
  })

  test("instances of count and for_each nodes", () => {
    const body = (attrs: Record<string, { expr?: string; literal?: unknown }>) => ({ attrs: Object.entries(attrs).map(([name, value]) => ({ name, value })), blocks: [] })
    const locals = [{ name: "tiers", value: { expr: "{\n  public = { cidr = local.a }\n  private = { cidr = local.b }\n}" } }]
    const variables = [{ name: "zones", body: body({ default: { literal: ["a", "b"] } }) }]
    expect(instanceKeys(body({}), variables, locals)).toBeNull()
    expect(instanceKeys(body({ count: { literal: 2 } }), variables, locals)).toEqual(["[0]", "[1]"])
    expect(instanceKeys(body({ count: { expr: "length(local.azs)" } }), variables, locals)).toEqual(["[0]", "[1]", "[2]"])
    expect(instanceKeys(body({ for_each: { expr: "local.tiers" } }), variables, locals)).toEqual(['["public"]', '["private"]'])
    expect(instanceKeys(body({ for_each: { expr: "toset(var.zones)" } }), variables, locals)).toEqual(['["a"]', '["b"]'])
    expect(instanceKeys(body({ for_each: { expr: 'toset(["x", "y"])' } }), variables, locals)).toEqual(['["x"]', '["y"]'])
    expect(instanceKeys(body({ for_each: { expr: "data.x.y.names" } }), variables, locals)).toEqual([])
  })
})
