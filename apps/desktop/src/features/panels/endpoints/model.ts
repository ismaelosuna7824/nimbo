import {
  Activity,
  Bell,
  Box,
  Boxes,
  Database,
  Globe,
  HardDrive,
  Inbox,
  Key,
  KeyRound,
  Link2,
  Network,
  Server,
  Settings2,
  Users,
  Workflow,
  Zap,
  type LucideIcon,
} from "lucide-react"
import type { AccessKind, ResourceAccess } from "@/lib/api/types"

export interface KindGroup {
  id: string
  title: string
  /** Plain-language line under the group title. */
  blurb: string
  icon: LucideIcon
  kinds: AccessKind[]
}

/** Display order of the Endpoints tab: what beginners call first comes first. */
export const KIND_GROUPS: KindGroup[] = [
  {
    id: "apis",
    title: "APIs",
    blurb: "Web addresses you can call from your browser, curl, Postman or your app.",
    icon: Globe,
    kinds: ["http-api", "function-url", "web-service"],
  },
  { id: "functions", title: "Functions", blurb: "Code that runs when you call it.", icon: Zap, kinds: ["function"] },
  {
    id: "instances",
    title: "EC2 instances",
    blurb: "Virtual servers in your network: open their web page or a shell inside them.",
    icon: Server,
    kinds: ["instance"],
  },
  {
    id: "queues",
    title: "Queues",
    blurb: "Mailboxes for messages: one part of your app drops them, another picks them up.",
    icon: Inbox,
    kinds: ["queue"],
  },
  {
    id: "topics",
    title: "Topics",
    blurb: "Broadcast a message once and every subscriber receives a copy.",
    icon: Bell,
    kinds: ["topic"],
  },
  { id: "buckets", title: "Storage buckets", blurb: "Folders in the cloud for files of any kind.", icon: HardDrive, kinds: ["bucket"] },
  { id: "tables", title: "Tables", blurb: "Store and look up records by key.", icon: Database, kinds: ["table"] },
  {
    id: "others",
    title: "Other resources",
    blurb: "Everything else that is running in your sandbox.",
    icon: Box,
    kinds: ["stream", "event-bus", "state-machine", "secret", "key", "parameter", "user-pool", "database", "cache", "kubernetes", "generic"],
  },
]

export const KIND_ICON: Record<AccessKind, LucideIcon> = {
  "http-api": Globe,
  "function-url": Link2,
  function: Zap,
  queue: Inbox,
  topic: Bell,
  bucket: HardDrive,
  table: Database,
  stream: Activity,
  "event-bus": Network,
  "state-machine": Workflow,
  secret: KeyRound,
  parameter: Settings2,
  "user-pool": Users,
  database: Database,
  cache: Database,
  "web-service": Server,
  key: Key,
  instance: Server,
  kubernetes: Boxes,
  generic: Box,
}


export interface HeaderRow {
  name: string
  value: string
}

/** Editable HTTP request of a card's "Try it"; endpoint rows prefill it. */
export interface HttpDraft {
  method: string
  url: string
  headers: HeaderRow[]
  body: string
}

/** A request for one route; "ANY" routes start as GET. */
export function httpDraftFor(method: string, url: string, body = ""): HttpDraft {
  const m = method === "ANY" ? "GET" : method.toUpperCase()
  return { method: m, url, headers: [{ name: "Content-Type", value: "application/json" }], body }
}

/**
 * Path of a sandbox URL relative to the deployed API it belongs to ("/items"
 * for ".../$default/items"), so addresses read the way the API declares them.
 */
export function apiPathOf(url: string, resources: ResourceAccess[]): string | undefined {
  for (const r of resources) {
    for (const e of r.endpoints ?? []) {
      if (e.url === url) return e.path
      const brace = e.path.indexOf("{")
      const literal = brace >= 0 ? e.path.slice(0, brace) : e.path
      const at = literal ? e.url.lastIndexOf(literal) : -1
      if (at < 0) continue
      const base = e.url.slice(0, at)
      if (!url.startsWith(base)) continue
      const rest = url.slice(base.length)
      return rest.startsWith("/") ? rest : `/${rest}`
    }
  }
  return undefined
}
