import type { Connection, ConnectionCheck, LoadProfile, LoadTarget } from "@/lib/api/types"
import { connectionAddress, KIND_META } from "./meta"

/** True for hosts that stay on this machine (the sandbox emulators listen on loopback). */
export function isLoopbackHost(host: string): boolean {
  const h = host.toLowerCase().replace(/^\[|\]$/g, "")
  return h === "localhost" || h.endsWith(".localhost") || /^127\.\d+\.\d+\.\d+$/.test(h) || h === "::1"
}

/** An absolute http(s) URL whose host is not loopback; relative or unparsable URLs are not external. */
export function isExternalUrl(url: string): boolean {
  try {
    const u = new URL(url.trim())
    return (u.protocol === "http:" || u.protocol === "https:") && !isLoopbackHost(u.hostname)
  } catch {
    return false
  }
}

export type SideEffect = "writes data" | "invokes function"

/** Side effect a target announces in its label ("… (writes data)", "… (invokes function)"), or inferred for custom targets. */
export function targetSideEffect(t: LoadTarget): SideEffect | null {
  if (/writes data/i.test(t.label)) return "writes data"
  if (/invokes function/i.test(t.label)) return "invokes function"
  if (t.http && !["GET", "HEAD", "OPTIONS"].includes(t.http.method.toUpperCase())) return "writes data"
  if (t.sql && (t.sql.mode === "exec" || /^\s*(insert|update|delete|merge|upsert|create|drop|alter|truncate|replace)\b/i.test(t.sql.query)))
    return "writes data"
  if (t.redis) {
    const cmd = t.redis.command.trim().split(/\s+/)[0]?.toUpperCase() ?? ""
    const reads = ["GET", "MGET", "EXISTS", "TTL", "PTTL", "TYPE", "STRLEN", "HGET", "HGETALL", "HMGET", "LRANGE", "LLEN", "SMEMBERS", "SCARD", "SISMEMBER", "ZRANGE", "ZCARD", "ZSCORE", "XRANGE", "XLEN", "PING", "ECHO", "INFO", "DBSIZE", "SCAN", "KEYS", "TIME"]
    if (cmd && !reads.includes(cmd)) return "writes data"
  }
  if (t.mongo && ["insertOne", "updateOne", "deleteOne"].includes(t.mongo.operation)) return "writes data"
  if (t.aws) {
    if (/^lambda:/i.test(t.aws.operation)) return "invokes function"
    if (!/:(Get|List|Describe|Head|Query|Scan)/.test(t.aws.operation)) return "writes data"
  }
  return null
}

export interface ExternalTarget {
  /** Short "where traffic goes", e.g. "api.example.com" or "AWS account 1234… · eu-west-1". */
  where: string
  details: { label: string; value: string }[]
  sideEffect: SideEffect | null
  aws: boolean
}

/**
 * Describes a target outside the local sandbox (any connection target, or an absolute
 * non-loopback URL), or null when traffic stays local. With force, always describes it
 * (used when the API demands confirmation the UI did not anticipate).
 */
export function describeExternal(
  target: LoadTarget | null,
  conn: Connection | undefined,
  lastCheck: ConnectionCheck | undefined,
  force = false,
): ExternalTarget | null {
  if (!target) return null
  const sideEffect = targetSideEffect(target)
  if (target.connectionId) {
    if (!conn) return { where: "a saved connection", details: [{ label: "Target", value: target.label }], sideEffect, aws: target.kind === "aws" }
    const meta = KIND_META[conn.kind]
    const address = connectionAddress(conn)
    const details = [
      { label: "Connection", value: `${conn.name} (${meta.label})` },
      { label: "Target", value: target.label },
    ]
    if (conn.kind === "aws") {
      const account = lastCheck?.ok ? lastCheck.message.match(/account\s+(\S+)/i)?.[1] : undefined
      details.splice(1, 0, { label: "Account", value: account ?? "unknown — test the connection to see it" }, { label: "Region", value: conn.region || "profile default" }, { label: "Profile", value: conn.profile || "default" })
      return { where: `AWS ${account ? `account ${account}` : `profile ${conn.profile || "default"}`} · ${conn.region || "default region"}`, details, sideEffect, aws: true }
    }
    if (conn.kind === "http" && target.http) {
      const host = hostOf(target.http.url) ?? hostOf(conn.baseUrl ?? "") ?? address
      details.splice(1, 0, { label: "Host", value: host })
      return { where: host, details, sideEffect, aws: false }
    }
    // A stored connection string is never returned, so only discrete fields reveal the server.
    details.splice(1, 0, { label: "Server", value: conn.host ? address : "from the stored connection string" })
    return { where: conn.host ? address : conn.name, details, sideEffect, aws: false }
  }
  const url = target.http?.url ?? ""
  if (isExternalUrl(url) || force) {
    const host = hostOf(url) ?? target.label
    return { where: host, details: [{ label: "Host", value: host }, { label: "Target", value: target.label }], sideEffect, aws: target.kind === "aws" }
  }
  return null
}

function hostOf(url: string): string | undefined {
  try {
    return new URL(url.trim()).host || undefined
  } catch {
    return undefined
  }
}

export interface TrafficEstimate {
  durationSec: number
  peak: number
  /** Total requests in req/s mode (area under the ramped stages); undefined for virtual users. */
  requests?: number
}

export function estimateTraffic(profile: LoadProfile): TrafficEstimate {
  let prev = 0
  let area = 0
  let duration = 0
  for (const s of profile.stages) {
    area += ((prev + s.target) / 2) * s.durationSec
    duration += s.durationSec
    prev = s.target
  }
  const peak = Math.max(0, ...profile.stages.map((s) => s.target))
  return { durationSec: duration, peak, requests: profile.mode === "rps" ? Math.round(area) : undefined }
}
