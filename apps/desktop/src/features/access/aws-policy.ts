import type { AccessCatalog, AWSPolicyDocument, AWSPrincipal, AWSStatement } from "@/lib/api/types"
import { exprText, quoteHcl } from "./support"

/** Query key of the pinned AWS reference data (services, or one service's actions). */
export const awsCatalogKey = (service?: string) => ["access-catalog", "aws", service ?? ""] as const

/** Access levels of the AWS Service Authorization Reference, least to most powerful. */
export const AWS_ACCESS_LEVELS = ["List", "Read", "Write", "Permissions management", "Tagging"] as const

/** ARN suffixes offered when linking a resource: the resource itself, everything under it, its streams. */
export const ARN_SUFFIXES = [
  { id: "none", label: "The resource itself", suffix: "" },
  { id: "children", label: "Everything inside it ( /* )", suffix: "/*" },
  { id: "streams", label: "Its streams / versions ( :* )", suffix: ":*" },
  { id: "custom", label: "Custom suffix…", suffix: "" },
] as const

export type ArnSuffixId = (typeof ARN_SUFFIXES)[number]["id"]

/** Principal types the editor offers ("*" is anyone). */
export const PRINCIPAL_TYPES = ["Service", "AWS", "Federated", "*"] as const

/** Literal text inside an HCL template: `${` and `%{` doubled, quotes and backslashes escaped. */
function templateLiteral(text: string): string {
  return text
    .replaceAll("\\", () => "\\\\")
    .replaceAll('"', () => '\\"')
    .replaceAll("${", () => "$${")
    .replaceAll("%{", () => "%%{")
}

/**
 * HCL expression of a linked resource with an ARN suffix: `aws_s3_bucket.b.arn` as is, `"${aws_s3_bucket.b.arn}/*"`
 * with a suffix.
 */
export function resourceExpr(ref: string, suffix: string): string {
  const r = ref.trim()
  if (!suffix) return r
  return `"\${${r}}${templateLiteral(suffix)}"`
}

/** HCL expression of an ARN (or `*`) typed by the user. */
export function arnExpr(text: string): string {
  return quoteHcl(text.trim())
}

const TRAVERSAL = /^(data\.)?[A-Za-z_][\w-]*\.[A-Za-z_][\w-]*(\.[A-Za-z_][\w-]*)+$/
const REF_WITH_SUFFIX = /^"\$\{((?:data\.)?[A-Za-z_][\w-]*\.[A-Za-z_][\w-]*(?:\.[A-Za-z_][\w-]*)+)\}([^"$%\\]*)"$/

/** How a resource expression reads: a linked attribute (+ suffix), a typed ARN, or other code. */
export type ResourceDisplay =
  | { kind: "ref"; ref: string; suffix: string }
  | { kind: "text"; text: string }
  | { kind: "code"; code: string }

export function describeResource(expr: string): ResourceDisplay {
  const t = expr.trim()
  if (TRAVERSAL.test(t)) return { kind: "ref", ref: t, suffix: "" }
  const m = REF_WITH_SUFFIX.exec(t)
  if (m) return { kind: "ref", ref: m[1], suffix: m[2] }
  const text = exprText(t)
  if (text !== t && !text.includes("${")) return { kind: "text", text }
  return { kind: "code", code: t }
}

/** The service prefix of an action ("dynamodb" of "dynamodb:GetItem"). */
export function serviceOf(action: string): string {
  const i = action.indexOf(":")
  return i < 0 ? "" : action.slice(0, i)
}

type CatalogAction = NonNullable<AccessCatalog["actions"]>[number]

/** Actions grouped by access level (reference order), filtered by a case-insensitive search; empty groups dropped. */
export function groupActions(actions: CatalogAction[], search = ""): { level: string; actions: CatalogAction[] }[] {
  const q = search.trim().toLowerCase()
  const shown = q ? actions.filter((a) => a.name.toLowerCase().includes(q)) : actions
  const levels: string[] = [...AWS_ACCESS_LEVELS]
  for (const a of shown) if (!levels.includes(a.level)) levels.push(a.level)
  return levels
    .map((level) => ({ level, actions: shown.filter((a) => a.level === level) }))
    .filter((g) => g.actions.length > 0)
}

/** The action names of some access levels ("All read actions" = List + Read). */
export function actionsAtLevels(actions: CatalogAction[], levels: readonly string[]): string[] {
  return actions.filter((a) => levels.includes(a.level)).map((a) => a.name)
}

/** Adds names to a list of actions, keeping order and skipping duplicates. */
export function addActions(current: string[], names: string[]): string[] {
  const out = [...current]
  for (const n of names) if (!out.includes(n)) out.push(n)
  return out
}

/** Toggles one action. */
export function toggleAction(current: string[], name: string): string[] {
  return current.includes(name) ? current.filter((a) => a !== name) : [...current, name]
}

/** A new statement: allow, nothing yet. */
export function blankStatement(): AWSStatement {
  return { effect: "Allow", actions: [], resources: [], editable: true }
}

/** A copy of a statement as a new one (no source: the backend writes it fresh). */
export function duplicateStatement(st: AWSStatement): AWSStatement {
  const copy: AWSStatement = {
    ...st,
    actions: [...st.actions],
    resources: [...st.resources],
    principals: st.principals?.map((p) => ({ ...p, values: [...p.values] })),
    extra: st.extra?.map((x) => ({ ...x })),
    source: undefined,
    editable: true,
  }
  if (copy.sid) copy.sid = `${copy.sid}Copy`
  return copy
}

/** Whether a statement can be duplicated (policy documents can't take conditions from the editor). */
export function canDuplicate(doc: AWSPolicyDocument, st: AWSStatement): boolean {
  return st.editable && !(doc.encoding === "document" && (st.extra?.length ?? 0) > 0)
}

function samePrincipals(a: AWSPrincipal[] | undefined, b: AWSPrincipal[] | undefined): boolean {
  const x = a ?? []
  const y = b ?? []
  return (
    x.length === y.length &&
    x.every((p, i) => p.type === y[i].type && p.values.length === y[i].values.length && p.values.every((v, k) => v.trim() === y[i].values[k].trim()))
  )
}

const sameList = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v.trim() === b[i].trim())

export function sameStatement(a: AWSStatement, b: AWSStatement): boolean {
  return (
    (a.sid ?? "") === (b.sid ?? "") &&
    a.effect === b.effect &&
    sameList(a.actions, b.actions) &&
    sameList(a.resources, b.resources) &&
    samePrincipals(a.principals, b.principals) &&
    sameList(
      (a.extra ?? []).map((x) => `${x.key}=${x.source}`),
      (b.extra ?? []).map((x) => `${x.key}=${x.source}`),
    ) &&
    (a.source ?? "") === (b.source ?? "")
  )
}

export function statementsChanged(saved: AWSStatement[], draft: AWSStatement[]): boolean {
  return saved.length !== draft.length || saved.some((s, i) => !sameStatement(s, draft[i]))
}

/** Warning path of a statement (backend access.AWSStatementPath). */
export function statementPath(docPath: string, index: number): string {
  return docPath ? `${docPath}/statements/${index}` : `statements/${index}`
}

/** Whether statements of this document say who they apply to (trust and resource policies). */
export function wantsPrincipals(doc: AWSPolicyDocument, nodeType: string): boolean {
  return doc.path === "assume_role_policy" || /^aws_(s3_bucket|sqs_queue|sns_topic)_policy$/.test(nodeType)
}

/** What keeps a statement from being saved (beginner wording). */
export function statementProblems(doc: AWSPolicyDocument, nodeType: string, st: AWSStatement): string[] {
  if (!st.editable) return []
  const out: string[] = []
  if (st.actions.length === 0) out.push("Pick at least one action it can do.")
  if (st.actions.some((a) => !a.trim() || /[\s"]/.test(a))) out.push("Actions can't contain spaces or quotes.")
  if (st.resources.length === 0 && doc.path !== "assume_role_policy") out.push("Pick what it applies to (a resource or an ARN).")
  if (wantsPrincipals(doc, nodeType) && (st.principals?.length ?? 0) === 0) out.push("Say who it applies to.")
  if (st.principals?.some((p) => p.values.length === 0)) out.push("Every 'who' needs at least one value.")
  if (st.sid && !/^[A-Za-z0-9]+$/.test(st.sid)) out.push("The name (Sid) may only use letters and digits.")
  return out
}

/** One-line summary of a statement: "Allow s3:GetObject on aws_s3_bucket.b.arn/*". */
export function statementSummary(st: AWSStatement): string {
  const on = st.resources.map((r) => {
    const d = describeResource(r)
    return d.kind === "ref" ? d.ref + d.suffix : d.kind === "text" ? d.text : d.code
  })
  const acts = st.actions.length > 3 ? `${st.actions.slice(0, 3).join(", ")} +${st.actions.length - 3}` : st.actions.join(", ")
  return `${st.effect} ${acts || "nothing"}${on.length ? ` on ${on.join(", ")}` : ""}`
}

/** Display label of a document encoding. */
export function encodingLabel(encoding: AWSPolicyDocument["encoding"]): string {
  switch (encoding) {
    case "jsonencode":
      return "jsonencode"
    case "json":
      return "JSON text"
    case "document":
      return "policy document"
    case "reference":
      return "from a policy document"
    default:
      return "code"
  }
}
