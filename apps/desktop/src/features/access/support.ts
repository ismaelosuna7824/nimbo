import type { AccessLevel, WorkflowNode } from "@/lib/api/types"

/** One grant option of a connect listing (Terraform or Kubernetes recipe with a level). */
export interface AccessOption {
  id: string
  label: string
  description?: string
  level?: AccessLevel
}

const LEVEL_ORDER: AccessLevel[] = ["read", "write", "manage"]

/** Grant options of a recipe listing, in Read, Write, Manage order. */
export function accessOptions<T extends AccessOption>(recipes: T[], isAccess: (r: T) => boolean): T[] {
  return recipes
    .filter(isAccess)
    .sort((a, b) => LEVEL_ORDER.indexOf(a.level ?? "manage") - LEVEL_ORDER.indexOf(b.level ?? "manage"))
}

/** Terraform resource types with an access editor (backend/internal/access providers). */
const ACCESS_RESOURCE_TYPES: Record<string, true> = {
  // AWS policy documents
  aws_iam_role: true,
  aws_iam_policy: true,
  aws_iam_role_policy: true,
  aws_iam_user_policy: true,
  aws_iam_group_policy: true,
  aws_s3_bucket_policy: true,
  aws_sqs_queue_policy: true,
  aws_sns_topic_policy: true,
  // Azure
  azurerm_role_assignment: true,
  // OCI
  oci_identity_policy: true,
}

const ACCESS_DATA_TYPES: Record<string, true> = { aws_iam_policy_document: true }

/** GCP IAM members and bindings of any resource (google_storage_bucket_iam_member, google_project_iam_binding, …). */
const GCP_IAM = /^google_[a-z0-9_]+_iam_(member|binding)$/

/** Whether the inspector shows an access editor for node (GET /workflows/{id}/access answers for it). */
export function accessSupported(node: Pick<WorkflowNode, "kind" | "type">): boolean {
  if (node.kind === "data") return !!ACCESS_DATA_TYPES[node.type]
  if (node.kind !== "resource") return false
  return !!ACCESS_RESOURCE_TYPES[node.type] || GCP_IAM.test(node.type)
}

/** Display text of an HCL expression: quoted literals without quotes, everything else verbatim. */
export function exprText(expr: string): string {
  const t = expr.trim()
  if (t.length >= 2 && t.startsWith('"') && t.endsWith('"') && !t.slice(1, -1).includes('"')) return t.slice(1, -1)
  return t
}

/** HCL expression of a plain string typed by the user (quoted, with `"`, `\` and `${` escaped). */
export function quoteHcl(text: string): string {
  // Function replacers: a string replacement would read "$$" as an escaped "$".
  const escaped = text
    .replaceAll("\\", () => "\\\\")
    .replaceAll('"', () => '\\"')
    .replaceAll("${", () => "$${")
  return `"${escaped}"`
}
