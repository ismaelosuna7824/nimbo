import { describe, expect, test } from "bun:test"
import type { NamedBlock, WorkflowNode } from "@/lib/api/types"
import { addProblem, addProvider, providerNames, providerRef, providerUsers, regionVariables } from "./providers"

const node = (type: string, provider?: string): WorkflowNode => ({
  id: `${type}.x`,
  kind: "resource",
  type,
  name: "x",
  position: { x: 0, y: 0 },
  positioned: true,
  body: { attrs: provider ? [{ name: "provider", value: { expr: provider } }] : [], blocks: [] },
})
const aws: NamedBlock = { name: "aws", body: { attrs: [{ name: "region", value: { expr: "var.aws_region" } }], blocks: [] } }

describe("provider configurations", () => {
  test("adds an aliased configuration with a region", () => {
    const out = addProvider([aws], "aws", "dr", { expr: "var.dr_region" })
    expect(out[1]).toEqual({
      name: "aws",
      body: { attrs: [{ name: "alias", value: { literal: "dr" } }, { name: "region", value: { expr: "var.dr_region" } }], blocks: [] },
    })
    expect(providerRef(out[1])).toBe("aws.dr")
    expect(addProvider([], "azurerm", "hub", undefined)[0].body.blocks).toEqual([{ type: "features", body: { attrs: [], blocks: [] } }])
  })

  test("refuses duplicates and bad aliases", () => {
    expect(addProblem([aws], "aws", "")).toContain("default configuration")
    expect(addProblem([aws], "aws", "dr")).toBeNull()
    expect(addProblem([aws], "aws", "1dr")).toContain("Aliases")
    expect(addProblem([aws], "constructor", "x")).toBe("Pick a provider.")
  })

  test("users of a configuration", () => {
    const dr = addProvider([], "aws", "dr", undefined)[0]
    const nodes = [node("aws_s3_bucket"), node("aws_backup_vault", "aws.dr"), node("google_storage_bucket")]
    expect(providerUsers(nodes, aws).map((n) => n.type)).toEqual(["aws_s3_bucket"])
    expect(providerUsers(nodes, dr).map((n) => n.type)).toEqual(["aws_backup_vault"])
    expect(providerNames([aws], nodes)).toEqual(["aws", "google"])
  })

  test("region variables", () => {
    expect(
      regionVariables([
        { name: "dr_region", body: { attrs: [{ name: "type", value: { expr: "string" } }], blocks: [] } },
        { name: "count", body: { attrs: [{ name: "type", value: { expr: "number" } }], blocks: [] } },
        { name: "any", body: { attrs: [], blocks: [] } },
      ]),
    ).toEqual(["var.dr_region", "var.any"])
  })
})
