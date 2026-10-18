import { Cloud, Database, DatabaseZap, Globe, Leaf, type LucideIcon } from "lucide-react"
import type { Connection, ConnectionKind } from "@/lib/api/types"

export interface KindMeta {
  kind: ConnectionKind
  label: string
  description: string
  icon: LucideIcon
  defaultPort?: number
}

export const CONNECTION_KINDS: KindMeta[] = [
  { kind: "http", label: "HTTP API", description: "Any REST/HTTP(S) endpoint by URL, with headers and auth.", icon: Globe },
  { kind: "postgres", label: "PostgreSQL", description: "Run SQL queries against a Postgres database.", icon: Database, defaultPort: 5432 },
  { kind: "mysql", label: "MySQL", description: "Run SQL queries against a MySQL or MariaDB database.", icon: Database, defaultPort: 3306 },
  { kind: "redis", label: "Redis", description: "Send commands (GET, SET, …) to a Redis server.", icon: DatabaseZap, defaultPort: 6379 },
  { kind: "mongodb", label: "MongoDB", description: "Insert, find and update documents in MongoDB.", icon: Leaf, defaultPort: 27017 },
  { kind: "aws", label: "AWS account", description: "SQS, S3, DynamoDB, Lambda… in a real account via an AWS CLI profile.", icon: Cloud },
]

export const KIND_META = Object.fromEntries(CONNECTION_KINDS.map((k) => [k.kind, k])) as Record<ConnectionKind, KindMeta>

export const isDatabase = (k: ConnectionKind) => k === "postgres" || k === "mysql" || k === "redis" || k === "mongodb"

export const AWS_REGIONS = [
  "us-east-1",
  "us-east-2",
  "us-west-1",
  "us-west-2",
  "ca-central-1",
  "sa-east-1",
  "eu-west-1",
  "eu-west-2",
  "eu-west-3",
  "eu-central-1",
  "eu-central-2",
  "eu-north-1",
  "eu-south-1",
  "eu-south-2",
  "me-central-1",
  "af-south-1",
  "ap-south-1",
  "ap-southeast-1",
  "ap-southeast-2",
  "ap-northeast-1",
  "ap-northeast-2",
  "ap-northeast-3",
  "ap-east-1",
]

/** Where a connection points, for cards and dialogs: base URL, host:port/db or region · profile. */
export function connectionAddress(c: Pick<Connection, "kind" | "baseUrl" | "host" | "port" | "database" | "profile" | "region" | "secretsSet" | "uri">): string {
  if (c.kind === "http") return c.baseUrl ?? ""
  if (c.kind === "aws") return `${c.region || "default region"} · profile ${c.profile || "default"}`
  if (c.host) {
    const port = c.port || KIND_META[c.kind].defaultPort
    return `${c.host}${port ? `:${port}` : ""}${c.database ? `/${c.database}` : ""}`
  }
  if (c.uri) return redactUri(c.uri)
  if (c.secretsSet?.includes("uri")) return "Connection string (stored)"
  return ""
}

/** Host part of a connection string without credentials, e.g. "postgres://db.example.com:5432/app". */
export function redactUri(uri: string): string {
  try {
    const u = new URL(uri)
    return `${u.protocol}//${u.host}${u.pathname === "/" ? "" : u.pathname}`
  } catch {
    return uri.replace(/\/\/[^@/]*@/, "//")
  }
}
