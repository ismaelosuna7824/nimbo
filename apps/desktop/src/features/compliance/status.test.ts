import { describe, expect, test } from "bun:test"
import type { ComplianceControl, ComplianceStatus } from "@/lib/api/types"
import { complianceMarks } from "./highlight"
import { sections } from "./status"

function control(id: string, section: string, status: ComplianceStatus, resources: [string, boolean][] = []): ComplianceControl {
  return {
    id,
    title: id,
    section,
    guidance: "",
    status,
    resources: resources.map(([nodeId, failing]) => ({ nodeId, address: nodeId, type: "aws_s3_bucket", failing })),
    findings: [],
  }
}

describe("compliance sections", () => {
  test("keep the benchmark's section order and put violations first", () => {
    const got = sections([
      control("1.1", "Storage", "met"),
      control("2.1", "Network", "manual"),
      control("1.2", "Storage", "not_applicable"),
      control("1.3", "Storage", "violated"),
      control("1.4", "Storage", "manual"),
    ])
    expect(got.map((s) => s.section)).toEqual(["Storage", "Network"])
    expect(got[0].controls.map((c) => c.id)).toEqual(["1.3", "1.4", "1.1", "1.2"])
  })
})

describe("compliance canvas marks", () => {
  const controls = [
    control("1.1", "Storage", "violated", [["a", true], ["b", false]]),
    control("1.2", "Storage", "violated", [["c", true]]),
    control("1.3", "Storage", "met", [["d", false]]),
  ]
  test("without a control: every failing resource of a violated control", () => {
    expect(complianceMarks(controls, null)).toEqual({ a: "failing", c: "failing" })
  })
  test("one control: its resources, failing and passing", () => {
    expect(complianceMarks(controls, controls[0])).toEqual({ a: "failing", b: "passing" })
    expect(complianceMarks(controls, controls[2])).toEqual({ d: "passing" })
  })
})
