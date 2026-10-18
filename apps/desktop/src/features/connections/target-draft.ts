import type { AWSTarget, Connection, ConnectionKind, LoadTarget, LoadTargetKind, MongoTarget, SQLTarget } from "@/lib/api/types"
import { parseHeaders } from "../panels/loadtest/targets"

/** Form state of "what to hit on this connection": a suggested target or a custom one per kind. */
export interface ConnectionTargetDraft {
  connectionId: string | null
  mode: "suggested" | "custom"
  suggested: LoadTarget | null
  http: { method: string; url: string; headersText: string; body: string }
  sql: SQLTarget & { args: string[] }
  redis: { command: string }
  mongo: MongoTarget
  aws: AWSTarget
}

export const INITIAL_CONNECTION_TARGET: ConnectionTargetDraft = {
  connectionId: null,
  mode: "suggested",
  suggested: null,
  http: { method: "GET", url: "", headersText: "", body: "" },
  sql: { query: "SELECT 1", args: [], mode: "query" },
  redis: { command: "PING" },
  mongo: { database: "", collection: "", operation: "findOne", document: "{}", update: "" },
  aws: { operation: "sqs:SendMessage", resource: "", payload: "", key: "" },
}

export const TARGET_KIND: Record<ConnectionKind, LoadTargetKind> = {
  http: "http",
  postgres: "sql",
  mysql: "sql",
  redis: "redis",
  mongodb: "mongo",
  aws: "aws",
}

const clip = (s: string, n = 60) => (s.length > n ? `${s.slice(0, n - 1)}…` : s)

/** The LoadTarget a draft describes on a connection, or null while incomplete. */
export function resolveConnectionTarget(conn: Connection | undefined, d: ConnectionTargetDraft): LoadTarget | null {
  if (!conn || d.connectionId !== conn.id) return null
  if (d.mode === "suggested") return d.suggested ? { ...d.suggested, connectionId: conn.id } : null
  const base = { connectionId: conn.id }
  switch (TARGET_KIND[conn.kind]) {
    case "http": {
      const url = d.http.url.trim()
      return {
        ...base,
        kind: "http",
        label: `${d.http.method} ${url || "/"}`,
        http: { method: d.http.method, url, headers: parseHeaders(d.http.headersText), body: d.http.body || undefined },
      }
    }
    case "sql": {
      const query = d.sql.query.trim()
      if (!query) return null
      const args = d.sql.args.length ? d.sql.args : undefined
      return { ...base, kind: "sql", label: clip(query.replace(/\s+/g, " ")), sql: { query, args, mode: d.sql.mode } }
    }
    case "redis": {
      const command = d.redis.command.trim()
      if (!command) return null
      return { ...base, kind: "redis", label: clip(command), redis: { command } }
    }
    case "mongo": {
      const m = d.mongo
      if (!m.database.trim() || (m.operation !== "runCommand" && !m.collection?.trim())) return null
      return {
        ...base,
        kind: "mongo",
        label: `${m.operation} ${m.database}${m.operation === "runCommand" ? "" : `.${m.collection}`}`,
        mongo: {
          database: m.database.trim(),
          collection: m.operation === "runCommand" ? undefined : m.collection?.trim(),
          operation: m.operation,
          document: m.document || undefined,
          update: m.operation === "updateOne" ? m.update || undefined : undefined,
        },
      }
    }
    case "aws": {
      const a = d.aws
      if (!a.operation.trim() || !a.resource.trim()) return null
      return {
        ...base,
        kind: "aws",
        label: `${a.operation} ${a.resource}`,
        aws: { operation: a.operation.trim(), resource: a.resource.trim(), payload: a.payload || undefined, key: a.key || undefined },
      }
    }
  }
}
