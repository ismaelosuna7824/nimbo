import type { AzureRoleAssignment, WorkflowNode } from "@/lib/api/types"
import { nodeAddress } from "../canvas/catalog"

/** Workloads whose system-assigned managed identity can get roles (backend/internal/catalog/access_azure.go). */
export const AZURE_WORKLOADS: Record<string, true> = {
  azurerm_linux_web_app: true,
  azurerm_windows_web_app: true,
  azurerm_linux_web_app_slot: true,
  azurerm_windows_web_app_slot: true,
  azurerm_linux_function_app: true,
  azurerm_windows_function_app: true,
  azurerm_linux_function_app_slot: true,
  azurerm_windows_function_app_slot: true,
  azurerm_function_app_flex_consumption: true,
  azurerm_container_app: true,
  azurerm_container_app_job: true,
  azurerm_container_group: true,
  azurerm_linux_virtual_machine: true,
  azurerm_windows_virtual_machine: true,
  azurerm_linux_virtual_machine_scale_set: true,
  azurerm_windows_virtual_machine_scale_set: true,
  azurerm_logic_app_standard: true,
  azurerm_logic_app_workflow: true,
  azurerm_api_management: true,
  azurerm_data_factory: true,
  azurerm_automation_account: true,
  azurerm_spring_cloud_app: true,
}

export const AZURE_PRINCIPAL_TYPES = ["User", "Group", "ServicePrincipal"] as const

/** An identity of the workflow the role can go to. */
export interface AzureIdentityOption {
  nodeId: string
  address: string
  /** principal_id expression; empty when the workload has no system-assigned identity yet. */
  expr: string
  /** "Managed identity", "System-assigned identity", "Kubelet identity". */
  kind: string
  /** Why it can't be picked yet. */
  hint?: string
}

/** The `identity` block's type when it is a plain string ("SystemAssigned", "UserAssigned", …). */
function identityType(n: WorkflowNode): string | undefined {
  const block = n.body.blocks.find((b) => b.type === "identity")
  if (!block) return undefined
  const attr = block.body.attrs.find((a) => a.name === "type")
  if (typeof attr?.value.literal === "string") return attr.value.literal
  const m = attr?.value.expr?.trim().match(/^"([^"$]*)"$/)
  return m ? m[1] : ""
}

/**
 * Identities in the graph: user-assigned managed identities, workloads with a system-assigned identity
 * (`identity[0].principal_id`), AKS clusters (kubelet identity). Workloads without one are listed with a hint.
 */
export function azureIdentityOptions(nodes: WorkflowNode[]): AzureIdentityOption[] {
  const out: AzureIdentityOption[] = []
  for (const n of nodes) {
    const address = nodeAddress(n)
    if (n.type === "azurerm_user_assigned_identity" && (n.kind === "resource" || n.kind === "data")) {
      out.push({ nodeId: n.id, address, expr: `${address}.principal_id`, kind: "Managed identity" })
    } else if (n.kind === "resource" && n.type === "azurerm_kubernetes_cluster") {
      if (n.body.blocks.some((b) => b.type === "identity")) {
        out.push({ nodeId: n.id, address, expr: `${address}.kubelet_identity[0].object_id`, kind: "Kubelet identity" })
      }
    } else if (n.kind === "resource" && AZURE_WORKLOADS[n.type]) {
      const t = identityType(n)
      if (t === undefined) {
        out.push({
          nodeId: n.id,
          address,
          expr: "",
          kind: "No identity yet",
          hint: "Drag it onto the resource on the canvas: Nimbo adds a system-assigned identity.",
        })
      } else if (t.includes("SystemAssigned")) {
        out.push({ nodeId: n.id, address, expr: `${address}.identity[0].principal_id`, kind: "System-assigned identity" })
      }
    }
  }
  return out
}

/** Whether two role assignments differ in an editable field. */
export function azureChanged(a: AzureRoleAssignment, b: AzureRoleAssignment): boolean {
  return (
    a.scope.trim() !== b.scope.trim() ||
    (a.roleName ?? "") !== (b.roleName ?? "") ||
    (a.roleId ?? "").trim() !== (b.roleId ?? "").trim() ||
    a.principalId.trim() !== b.principalId.trim() ||
    (a.principalType ?? "") !== (b.principalType ?? "")
  )
}

/** What blocks saving, in beginner words. */
export function azureProblems(ra: AzureRoleAssignment): string[] {
  const out: string[] = []
  if (!ra.principalId.trim()) out.push("Pick who gets the role.")
  if (!(ra.roleName ?? "").trim() && !(ra.roleId ?? "").trim()) out.push("Pick a role.")
  if (!ra.scope.trim()) out.push("Pick what the role applies to.")
  return out
}

/** "Key Vault Secrets User on azurerm_key_vault.main for azurerm_linux_web_app.api" (plain-language summary). */
export function azureSentence(ra: AzureRoleAssignment, who?: string, what?: string): string {
  const role = ra.roleName || (ra.roleId ? "the role definition " + ra.roleId : "a role")
  return `${who || ra.principalId || "Someone"} can do ${role} on ${what || ra.scope || "something"}.`
}
