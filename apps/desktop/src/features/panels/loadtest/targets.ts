import type { LoadTarget } from "@/lib/api/types"

export const AWS_OPERATIONS = [
  "sqs:SendMessage",
  "s3:PutObject",
  "s3:GetObject",
  "dynamodb:PutItem",
  "dynamodb:GetItem",
  "lambda:Invoke",
  "sns:Publish",
  "kinesis:PutRecord",
  "events:PutEvents",
  "states:StartExecution",
]

export const HTTP_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"]

export const targetKey = (t: LoadTarget) => `${t.connectionId ?? ""}|${t.nodeId ?? ""}|${t.kind}|${t.label}`

/** Parses "Name: value" lines; undefined when there are none. */
export function parseHeaders(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const line of text.split(/\r?\n/)) {
    const i = line.indexOf(":")
    if (i > 0) out[line.slice(0, i).trim()] = line.slice(i + 1).trim()
  }
  return Object.keys(out).length ? out : undefined
}

/** One-line technical description of what a target sends. */
export function targetDetail(t: LoadTarget): string {
  if (t.http) return `${t.http.method} ${t.http.url || "/"}`
  if (t.aws) return `${t.aws.operation} → ${t.aws.resource}`
  if (t.sql) return t.sql.query
  if (t.redis) return t.redis.command
  if (t.mongo) return `${t.mongo.operation} ${t.mongo.database}${t.mongo.collection ? `.${t.mongo.collection}` : ""}`
  return ""
}
