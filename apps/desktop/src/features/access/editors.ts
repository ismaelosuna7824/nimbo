import type { ComponentType } from "react"
import type { AccessEdit, AccessView, WorkflowNode } from "@/lib/api/types"
import { AWSPolicyEditor } from "./aws-policy-editor"
import { AzureRoleEditor } from "./azure-role-editor"
import { GCPIAMEditor } from "./gcp-iam-editor"
import { OCIPolicyEditor } from "./oci-policy-editor"

/** Props of a provider's access editor inside the inspector's Access section. */
export interface AccessEditorProps {
  workflowId: string
  node: WorkflowNode
  view: AccessView
  /** Sends one edit (PUT /workflows/{id}/access) after flushing pending inspector edits; the store gets the result. */
  save: (edit: AccessEdit) => Promise<void>
  /** Selects another node (related links, Link to…). */
  select: (nodeId: string) => void
}

/** Editor per provider (import each provider's editor and add it); a provider without one shows read-only. */
export const ACCESS_EDITORS: Partial<Record<AccessView["provider"], ComponentType<AccessEditorProps>>> = {
  aws: AWSPolicyEditor,
  azure: AzureRoleEditor,
  gcp: GCPIAMEditor,
  oci: OCIPolicyEditor,
}
