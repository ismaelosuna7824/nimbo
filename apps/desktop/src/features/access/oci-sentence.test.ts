import { describe, expect, test } from "bun:test"
import type { OCIStatement, WorkflowNode } from "@/lib/api/types"
import {
  appendConditionValue,
  blankStatement,
  formatStatement,
  moveItem,
  namedNodes,
  namedValue,
  parseStatement,
  sameStatement,
  splitSubjects,
  statementProblems,
  statementsChanged,
} from "./oci-sentence"

const parse = (text: string) => {
  const r = parseStatement(text)
  if ("error" in r) throw new Error(r.error)
  return r.statement
}

describe("parseStatement / formatStatement", () => {
  test("round trips the documented forms", () => {
    for (const text of [
      "Allow group app-developers to manage instance-family in compartment app-dev",
      "Allow group ${oci_identity_group.dev.name} to read objects in compartment ${oci_identity_compartment.app.name} where target.bucket.name='${oci_objectstorage_bucket.b.name}'",
      "Allow dynamic-group 'Default'/'fn-dg', id ocid1.dynamicgroup.oc1..aaa to use keys in compartment id ocid1.compartment.oc1..bbb",
      "Allow group ops to inspect all-resources in compartment shared:network",
      "Allow any-user to read buckets in tenancy where all {request.principal.type='fnfunc', target.bucket.name='b'}",
      "Allow service objectstorage-us-ashburn-1 to use keys in tenancy",
    ]) {
      expect(formatStatement(parse(text))).toBe(text)
    }
  })

  test("reads the parts", () => {
    expect(parse("allow GROUP a, b to MANAGE object-family IN compartment c WHERE target.bucket.name = 'x'")).toEqual({
      editable: true,
      subjectType: "group",
      subjects: ["a", "b"],
      verb: "manage",
      resourceType: "object-family",
      location: "compartment",
      compartment: "c",
      condition: "target.bucket.name = 'x'",
    })
    expect(parse("Allow group a to read buckets in compartment id ocid1.compartment.oc1..x").compartment).toBe("id ocid1.compartment.oc1..x")
  })

  test("explains what it can't show", () => {
    for (const [text, why] of [
      ["Endorse group A to manage remote-peering-to in any-tenancy", "Cross-tenancy"],
      ["Allow group ops to {INSTANCE_READ} in tenancy", "Permission lists"],
      ["Allow group a to fly buckets in tenancy", "verb"],
      ["Allow users a to read buckets in tenancy", "group"],
      ["Allow group a to read buckets", "in <location>"],
      ["Allow group a to read buckets in tenancy extra", "Unexpected"],
      ["Allow any-user b to read buckets in tenancy", "takes no names"],
      ["Allow group ${oops to read buckets in tenancy", "Unbalanced"],
    ] as const) {
      const r = parseStatement(text)
      expect("error" in r ? r.error : "").toContain(why)
    }
  })

  test("formats incomplete statements with placeholders", () => {
    expect(formatStatement(blankStatement())).toBe("Allow group to read … in compartment …")
    expect(formatStatement({ ...blankStatement(), subjectType: "any-user", subjects: ["ignored"] })).toBe("Allow any-user ignored to read … in compartment …")
  })
})

describe("splitSubjects", () => {
  test("keeps interpolations, quotes and 'id <ocid>' together", () => {
    expect(splitSubjects("a, ${var.g[\"x, y\"]}, 'Default'/'Admins', id ocid1.group.oc1..z")).toEqual([
      'a',
      '${var.g["x, y"]}',
      "'Default'/'Admins'",
      "id ocid1.group.oc1..z",
    ])
    expect(splitSubjects("  ")).toEqual([])
  })
})

describe("statementProblems", () => {
  const ok: OCIStatement = { ...blankStatement(), subjects: ["dev"], resourceType: "buckets", compartment: "app" }
  test("accepts a complete statement", () => {
    expect(statementProblems(ok)).toEqual([])
    expect(statementProblems({ ...ok, location: "tenancy", compartment: "" })).toEqual([])
    expect(statementProblems({ editable: false, source: "local.x" })).toEqual([])
  })
  test("lists what is missing", () => {
    expect(statementProblems(blankStatement())).toEqual(["Choose the group.", "Choose what it applies to (a resource-type).", "Choose the compartment."])
    expect(statementProblems({ ...ok, resourceType: "buckets objects" })[0]).toContain("one word")
  })
  test("rejects names that would not read back", () => {
    expect(statementProblems({ ...ok, subjects: ["dev to x"] })).not.toEqual([])
    expect(statementProblems({ ...ok, compartment: "app where" })).not.toEqual([])
  })
})

describe("change tracking", () => {
  const a = parse("Allow group a to read buckets in tenancy")
  test("sameStatement ignores whitespace, not meaning", () => {
    expect(sameStatement(a, { ...a, subjects: [" a "] })).toBe(true)
    expect(sameStatement(a, { ...a, verb: "use" })).toBe(false)
    expect(sameStatement({ editable: false, source: "x" }, { editable: false, source: "x" })).toBe(true)
  })
  test("statementsChanged sees edits, additions and moves", () => {
    const saved = [
      { ...a, source: '"s1"' },
      { editable: false, source: "local.x" },
    ]
    expect(statementsChanged(saved, saved.slice())).toBe(false)
    expect(statementsChanged(saved, [{ ...saved[0], verb: "manage" }, saved[1]])).toBe(true)
    expect(statementsChanged(saved, moveItem(saved, 0, 1))).toBe(true)
    expect(statementsChanged(saved, [...saved, blankStatement()])).toBe(true)
  })
  test("moveItem clamps", () => {
    expect(moveItem([1, 2, 3], 0, 2)).toEqual([2, 3, 1])
    expect(moveItem([1, 2, 3], 2, -5)).toEqual([3, 1, 2])
    expect(moveItem([1, 2, 3], 7, 0)).toEqual([1, 2, 3])
  })
})

describe("namedNodes", () => {
  const node = (type: string, name: string, attr?: { literal?: unknown; expr?: string }): WorkflowNode =>
    ({
      id: `${type}.${name}`,
      kind: "resource",
      type,
      name,
      position: { x: 0, y: 0 },
      positioned: true,
      body: { attrs: attr ? [{ name: "name", value: attr }] : [], blocks: [] },
    }) as WorkflowNode
  const nodes = [
    node("oci_identity_group", "dev", { literal: "app-developers", expr: '"app-developers"' }),
    node("oci_identity_group", "ops", { expr: '"${local.prefix}-ops"' }),
    node("oci_identity_compartment", "app", { literal: "app-dev" }),
  ]
  test("lists the workflow's groups and compartments with their names", () => {
    const groups = namedNodes(nodes, "group")
    expect(groups.map((g) => [g.address, g.name, g.reference])).toEqual([
      ["oci_identity_group.dev", "app-developers", "${oci_identity_group.dev.name}"],
      ["oci_identity_group.ops", undefined, "${oci_identity_group.ops.name}"],
    ])
    expect(namedNodes(nodes, "compartment").map((c) => c.name)).toEqual(["app-dev"])
    expect(namedNodes(nodes, "service")).toEqual([])
  })
  test("namedValue prefers the constant name, falls back to the reference", () => {
    const [dev, ops] = namedNodes(nodes, "group")
    expect(namedValue(dev)).toBe("app-developers")
    expect(namedValue(dev, "reference")).toBe("${oci_identity_group.dev.name}")
    expect(namedValue(ops)).toBe("${oci_identity_group.ops.name}")
  })
})

describe("appendConditionValue", () => {
  test("a picked reference becomes a quoted value after what was typed", () => {
    expect(appendConditionValue("target.key.id =", "oci_kms_key.cmk.id")).toBe("target.key.id = '${oci_kms_key.cmk.id}'")
    expect(appendConditionValue("target.key.id = ", "oci_kms_key.cmk.id")).toBe("target.key.id = '${oci_kms_key.cmk.id}'")
    expect(appendConditionValue(undefined, "oci_kms_key.cmk.id")).toBe("'${oci_kms_key.cmk.id}'")
  })
})
