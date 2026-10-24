import type { GCPIAMBinding, WorkflowNode } from "@/lib/api/types"
import { nodeAddress } from "../canvas/catalog"

/** IAM principal types of a member ("serviceAccount:x@p.iam.gserviceaccount.com"), plus the public ones. */
export const GCP_MEMBER_TYPES = ["serviceAccount", "group", "user", "domain", "allUsers", "allAuthenticatedUsers"] as const
export type GCPMemberType = (typeof GCP_MEMBER_TYPES)[number] | "principal" | "principalSet" | "expression"

export const GCP_MEMBER_LABELS: Record<GCPMemberType, string> = {
  serviceAccount: "Service account",
  group: "Google group",
  user: "User",
  domain: "Domain",
  allUsers: "Anyone (public)",
  allAuthenticatedUsers: "Any Google account (public)",
  principal: "Principal",
  principalSet: "Principal set",
  expression: "Expression",
}

/** Member types without a value after the colon. */
export function isPublicMember(t: GCPMemberType): boolean {
  return t === "allUsers" || t === "allAuthenticatedUsers"
}

/**
 * A member split for editing: `type` and the text after "type:" (an HCL template fragment, so it may hold
 * `${…}` interpolations). Anything that isn't a quoted string (a variable, a function call) is an "expression" whose
 * `value` is the HCL source.
 */
export interface GCPMember {
  type: GCPMemberType
  value: string
}

/** The text of a quoted HCL string (escapes undone, `${…}` kept), or undefined for other expressions. */
function stringTemplate(expr: string): string | undefined {
  const t = expr.trim()
  if (t.length < 2 || !t.startsWith('"') || !t.endsWith('"')) return undefined
  const inner = t.slice(1, -1)
  let out = ""
  for (let i = 0; i < inner.length; i++) {
    const c = inner[i]
    if (c === '"') return undefined // several strings joined some other way
    if (c === "\\" && i + 1 < inner.length) {
      const next = inner[++i]
      out += next === "n" ? "\n" : next === "t" ? "\t" : next
      continue
    }
    out += c
  }
  return out
}

const KNOWN_TYPES: Record<string, true> = {
  serviceAccount: true,
  group: true,
  user: true,
  domain: true,
  principal: true,
  principalSet: true,
}

/** Splits a member HCL expression. */
export function parseMember(expr: string): GCPMember {
  const text = stringTemplate(expr)
  if (text === undefined) return { type: "expression", value: expr.trim() }
  if (text === "allUsers" || text === "allAuthenticatedUsers") return { type: text, value: "" }
  const colon = text.indexOf(":")
  const type = colon > 0 ? text.slice(0, colon) : ""
  if (!KNOWN_TYPES[type]) return { type: "expression", value: expr.trim() }
  return { type: type as GCPMemberType, value: text.slice(colon + 1) }
}

/** HCL expression of a member; `${…}` in the value stay interpolations. */
export function buildMember(m: GCPMember): string {
  if (m.type === "expression") return m.value.trim()
  if (isPublicMember(m.type)) return `"${m.type}"`
  const escaped = m.value.trim().replaceAll("\\", () => "\\\\").replaceAll('"', () => '\\"')
  return `"${m.type}:${escaped}"`
}

/** The member value of a reference picked with Link to… (`${google_service_account.run.email}`). */
export function refValue(expr: string): string {
  return `\${${expr}}`
}

/**
 * Google-managed service agents with the standard `service-PROJECT_NUMBER@<domain>` address
 * (https://cloud.google.com/iam/docs/service-agents).
 */
const SERVICE_AGENTS = [
  { service: "Pub/Sub", domain: "gcp-sa-pubsub.iam.gserviceaccount.com" },
  { service: "Cloud Run", domain: "serverless-robot-prod.iam.gserviceaccount.com" },
  { service: "Cloud KMS", domain: "gcp-sa-cloudkms.iam.gserviceaccount.com" },
  { service: "Secret Manager", domain: "gcp-sa-secretmanager.iam.gserviceaccount.com" },
  { service: "Eventarc", domain: "gcp-sa-eventarc.iam.gserviceaccount.com" },
] as const

/** A service account of the member picker: a google_service_account, or a service agent of a project in the graph. */
export interface ServiceAccountOption {
  key: string
  label: string
  /** The member value (after "serviceAccount:"). */
  value: string
}

/** Service accounts of the workflow (`${google_service_account.x.email}`) and the service agents of its projects. */
export function serviceAccountOptions(nodes: WorkflowNode[]): ServiceAccountOption[] {
  const out: ServiceAccountOption[] = []
  for (const n of nodes) {
    if (n.kind !== "resource" && n.kind !== "data") continue
    const address = nodeAddress(n)
    if (n.type === "google_service_account") out.push({ key: address, label: address, value: refValue(`${address}.email`) })
  }
  for (const n of nodes) {
    if (n.type !== "google_project" || (n.kind !== "resource" && n.kind !== "data")) continue
    const address = nodeAddress(n)
    for (const a of SERVICE_AGENTS) {
      out.push({
        key: `${address}/${a.domain}`,
        label: `${a.service} service agent of ${address}`,
        value: `service-${refValue(`${address}.number`)}@${a.domain}`,
      })
    }
  }
  return out
}

const ROLE_RE = /^(roles\/[A-Za-z0-9_.]+|(projects|organizations)\/[^/]+\/roles\/[A-Za-z0-9_.]+)$/

/** What blocks saving, in beginner words. */
export function gcpProblems(b: GCPIAMBinding): string[] {
  const out: string[] = []
  const members = b.members.filter((m) => m.trim())
  if (members.length === 0) out.push("Add who gets the role.")
  if (b.kind === "member" && members.length > 1) out.push("An IAM member grants one member; use a binding for several.")
  if (new Set(members.map((m) => m.trim())).size !== members.length) out.push("A member is listed twice.")
  if (!ROLE_RE.test(b.role.trim())) out.push("Pick a role (roles/<service>.<role>).")
  if (!(b.resource ?? "").trim()) out.push(`Pick the resource (${b.resourceAttr ?? "resource"}).`)
  for (const m of members) {
    const p = parseMember(m)
    if (p.type !== "expression" && !isPublicMember(p.type) && !p.value.trim()) out.push(`Fill in the ${GCP_MEMBER_LABELS[p.type].toLowerCase()}.`)
  }
  return out
}

/** Whether two bindings differ in an editable field. */
export function gcpChanged(a: GCPIAMBinding, b: GCPIAMBinding): boolean {
  const ma = a.members.map((m) => m.trim())
  const mb = b.members.map((m) => m.trim())
  return (
    a.role.trim() !== b.role.trim() ||
    (a.resource ?? "").trim() !== (b.resource ?? "").trim() ||
    ma.length !== mb.length ||
    ma.some((m, i) => m !== mb[i])
  )
}
