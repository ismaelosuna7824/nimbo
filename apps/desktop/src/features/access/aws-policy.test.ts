import { describe, expect, test } from "bun:test"
import type { AWSPolicyDocument, AWSStatement } from "@/lib/api/types"
import {
  actionsAtLevels,
  addActions,
  arnExpr,
  canDuplicate,
  describeResource,
  duplicateStatement,
  groupActions,
  resourceExpr,
  sameStatement,
  statementPath,
  statementProblems,
  statementsChanged,
  toggleAction,
} from "./aws-policy"

describe("resourceExpr (ARN suffix builder)", () => {
  test("a linked attribute without suffix stays a reference", () => {
    expect(resourceExpr("aws_dynamodb_table.orders.arn", "")).toBe("aws_dynamodb_table.orders.arn")
  })
  test("suffixes become a template", () => {
    expect(resourceExpr("aws_s3_bucket.b.arn", "/*")).toBe('"${aws_s3_bucket.b.arn}/*"')
    expect(resourceExpr("aws_cloudwatch_log_group.reader.arn", ":*")).toBe('"${aws_cloudwatch_log_group.reader.arn}:*"')
    expect(resourceExpr(" aws_dynamodb_table.t.arn ", "/index/*")).toBe('"${aws_dynamodb_table.t.arn}/index/*"')
  })
  test("custom suffixes are escaped", () => {
    expect(resourceExpr("aws_s3_bucket.b.arn", '/a"b/${x}')).toBe('"${aws_s3_bucket.b.arn}/a\\"b/$${x}"')
  })
  test("typed ARNs are quoted literals", () => {
    expect(arnExpr("*")).toBe('"*"')
    expect(arnExpr(" arn:aws:s3:::my-bucket/* ")).toBe('"arn:aws:s3:::my-bucket/*"')
  })
})

describe("describeResource", () => {
  test("round-trips what resourceExpr builds", () => {
    expect(describeResource(resourceExpr("aws_s3_bucket.b.arn", "/*"))).toEqual({ kind: "ref", ref: "aws_s3_bucket.b.arn", suffix: "/*" })
    expect(describeResource("data.aws_caller_identity.me.arn")).toEqual({ kind: "ref", ref: "data.aws_caller_identity.me.arn", suffix: "" })
  })
  test("literals and code", () => {
    expect(describeResource('"*"')).toEqual({ kind: "text", text: "*" })
    expect(describeResource('"arn:aws:sqs:${var.region}:1:q"')).toEqual({ kind: "code", code: '"arn:aws:sqs:${var.region}:1:q"' })
    expect(describeResource("var.arns")).toEqual({ kind: "code", code: "var.arns" })
  })
})

describe("action grouping", () => {
  const actions = [
    { name: "dynamodb:PutItem", level: "Write" },
    { name: "dynamodb:GetItem", level: "Read" },
    { name: "dynamodb:ListTables", level: "List" },
    { name: "dynamodb:TagResource", level: "Tagging" },
    { name: "dynamodb:BatchGetItem", level: "Read" },
    { name: "dynamodb:Future", level: "Brand new" },
  ]
  test("groups in reference order, unknown levels last", () => {
    expect(groupActions(actions).map((g) => [g.level, g.actions.map((a) => a.name)])).toEqual([
      ["List", ["dynamodb:ListTables"]],
      ["Read", ["dynamodb:GetItem", "dynamodb:BatchGetItem"]],
      ["Write", ["dynamodb:PutItem"]],
      ["Tagging", ["dynamodb:TagResource"]],
      ["Brand new", ["dynamodb:Future"]],
    ])
  })
  test("search filters case-insensitively and drops empty groups", () => {
    expect(groupActions(actions, "getitem").map((g) => [g.level, g.actions.length])).toEqual([["Read", 2]])
  })
  test("quick buttons pick whole levels", () => {
    expect(actionsAtLevels(actions, ["List", "Read"])).toEqual(["dynamodb:GetItem", "dynamodb:ListTables", "dynamodb:BatchGetItem"])
  })
  test("adding and toggling keep order without duplicates", () => {
    expect(addActions(["a:X"], ["a:Y", "a:X", "a:Z"])).toEqual(["a:X", "a:Y", "a:Z"])
    expect(toggleAction(["a:X", "a:Y"], "a:X")).toEqual(["a:Y"])
    expect(toggleAction(["a:Y"], "a:X")).toEqual(["a:Y", "a:X"])
  })
})

describe("statements", () => {
  const doc: AWSPolicyDocument = { path: "policy", label: "Permissions", encoding: "jsonencode", editable: true, statements: [] }
  const st: AWSStatement = {
    sid: "Read",
    effect: "Allow",
    actions: ["s3:GetObject"],
    resources: ['"${aws_s3_bucket.b.arn}/*"'],
    extra: [{ key: "Condition", source: "{}" }],
    editable: true,
    source: "{ ... }",
  }
  test("duplicates are new statements", () => {
    const d = duplicateStatement(st)
    expect(d.source).toBeUndefined()
    expect(d.sid).toBe("ReadCopy")
    expect(d.actions).not.toBe(st.actions)
    expect(sameStatement(d, st)).toBe(false)
  })
  test("policy documents can't take copied conditions", () => {
    expect(canDuplicate(doc, st)).toBe(true)
    expect(canDuplicate({ ...doc, encoding: "document" }, st)).toBe(false)
  })
  test("changes are detected", () => {
    expect(statementsChanged([st], [{ ...st }])).toBe(false)
    expect(statementsChanged([st], [{ ...st, effect: "Deny" }])).toBe(true)
    expect(statementsChanged([st], [])).toBe(true)
  })
  test("problems", () => {
    expect(statementProblems(doc, "aws_iam_role_policy", st)).toEqual([])
    expect(statementProblems(doc, "aws_iam_role_policy", { ...st, actions: [], resources: [] })).toHaveLength(2)
    expect(statementProblems({ ...doc, path: "assume_role_policy" }, "aws_iam_role", { ...st, resources: [] })).toEqual(["Say who it applies to."])
    expect(statementProblems(doc, "aws_iam_role_policy", { ...st, sid: "has space" })).toHaveLength(1)
  })
  test("warning paths match the backend", () => {
    expect(statementPath("policy", 2)).toBe("policy/statements/2")
    expect(statementPath("", 0)).toBe("statements/0")
  })
})
