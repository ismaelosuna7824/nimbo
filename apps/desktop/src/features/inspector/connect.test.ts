import { describe, expect, test } from "bun:test"
import type { ResourceAccess } from "@/lib/api/types"
import { liveHighlights } from "./connect"

const ARN = "arn:aws:logs:us-east-1:000000000000:log-group:/app/logs"

function access(fields: ResourceAccess["fields"], urls: string[] = []): ResourceAccess {
  return {
    address: "aws_cloudwatch_log_group.logs",
    type: "aws_cloudwatch_log_group",
    label: "Logs",
    kind: "generic",
    summary: "",
    fields,
    endpoints: urls.map((url) => ({ method: "GET", path: "/", url })),
    snippets: [],
    actions: [],
  }
}

describe("liveHighlights", () => {
  test("without a usage card: identifiers in order, one per value", () => {
    // A log group's id is its name: shown once, as name.
    expect(liveHighlights({ arn: ARN, id: "/app/logs", name: "/app/logs", retention_in_days: 14 })).toEqual([
      { label: "arn", value: ARN },
      { label: "name", value: "/app/logs" },
    ])
  })

  test("leaves out what the usage card shows, by name or by value", () => {
    const card = access([
      { label: "Name", value: "/app/logs" },
      { label: "ARN", value: ARN },
    ])
    expect(liveHighlights({ arn: ARN, id: "/app/logs", name: "/app/logs" }, card)).toEqual([])
  })

  test("keeps identifiers the card doesn't have", () => {
    const card = access([{ label: "Queue URL", value: "http://localhost:4566/000000000000/jobs" }], ["http://localhost:4566/000000000000/jobs"])
    expect(
      liveHighlights({ arn: "arn:aws:sqs:us-east-1:000000000000:jobs", id: "http://localhost:4566/000000000000/jobs", url: "http://localhost:4566/000000000000/jobs", name: "jobs" }, card),
    ).toEqual([
      { label: "arn", value: "arn:aws:sqs:us-east-1:000000000000:jobs" },
      { label: "name", value: "jobs" },
    ])
  })

  test("skips empty and non-scalar values", () => {
    expect(liveHighlights({ arn: "", id: 42, endpoint: { host: "x" }, dns_name: null })).toEqual([{ label: "id", value: "42" }])
  })
})
