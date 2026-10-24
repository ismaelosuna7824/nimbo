import { describe, expect, test } from "bun:test"
import type { AzureRoleAssignment, Block, WorkflowNode } from "@/lib/api/types"
import { type CatalogRole, filterRoles, referencedNode, suggestedLabel } from "./azure-gcp-roles"
import { azureChanged, azureIdentityOptions, azureProblems, azureSentence } from "./azure-role"

const node = (type: string, name: string, blocks: Block[] = [], kind: WorkflowNode["kind"] = "resource"): WorkflowNode => ({
  id: `${type}.${name}`,
  kind,
  type,
  name,
  position: { x: 0, y: 0 },
  positioned: true,
  body: { attrs: [], blocks },
})

const identity = (type: string): Block => ({ type: "identity", body: { attrs: [{ name: "type", value: { expr: `"${type}"` } }], blocks: [] } })

describe("azureIdentityOptions", () => {
  test("lists managed identities with their principal_id expressions", () => {
    const opts = azureIdentityOptions([
      node("azurerm_user_assigned_identity", "app"),
      node("azurerm_linux_web_app", "api", [identity("SystemAssigned")]),
      node("azurerm_linux_function_app", "fn"),
      node("azurerm_container_app", "worker", [identity("UserAssigned")]),
      node("azurerm_kubernetes_cluster", "aks", [identity("SystemAssigned")]),
      node("azurerm_key_vault", "kv"),
    ])
    expect(opts.map((o) => [o.address, o.expr])).toEqual([
      ["azurerm_user_assigned_identity.app", "azurerm_user_assigned_identity.app.principal_id"],
      ["azurerm_linux_web_app.api", "azurerm_linux_web_app.api.identity[0].principal_id"],
      ["azurerm_linux_function_app.fn", ""],
      ["azurerm_kubernetes_cluster.aks", "azurerm_kubernetes_cluster.aks.kubelet_identity[0].object_id"],
    ])
    expect(opts[2].hint).toContain("system-assigned identity")
  })
})

describe("azure role assignment helpers", () => {
  const ra: AzureRoleAssignment = {
    scope: "azurerm_key_vault.main.id",
    roleName: "Key Vault Secrets User",
    principalId: "azurerm_linux_web_app.api.identity[0].principal_id",
    editable: true,
  }
  test("problems and changes", () => {
    expect(azureProblems(ra)).toEqual([])
    expect(azureProblems({ ...ra, scope: " ", roleName: "", principalId: "" })).toEqual([
      "Pick who gets the role.",
      "Pick a role.",
      "Pick what the role applies to.",
    ])
    expect(azureChanged(ra, { ...ra })).toBe(false)
    expect(azureChanged(ra, { ...ra, roleName: "Key Vault Secrets Officer" })).toBe(true)
    expect(azureChanged(ra, { ...ra, principalType: "ServicePrincipal" })).toBe(true)
  })
  test("sentence", () => {
    expect(azureSentence(ra, "api", "main")).toBe("api can do Key Vault Secrets User on main.")
  })
})

describe("referencedNode", () => {
  const nodes = [node("azurerm_key_vault", "main"), node("azurerm_subscription", "current", [], "data"), node("google_storage_bucket", "b")]
  test("finds the first referenced node", () => {
    expect(referencedNode("azurerm_key_vault.main.id", nodes)?.id).toBe("azurerm_key_vault.main")
    expect(referencedNode("data.azurerm_subscription.current.id", nodes)?.type).toBe("azurerm_subscription")
    expect(referencedNode('"${google_storage_bucket.b.name}/x"', nodes)?.name).toBe("b")
    expect(referencedNode('"/subscriptions/x"', nodes)).toBeUndefined()
  })
})

describe("filterRoles", () => {
  const roles: CatalogRole[] = [
    { id: "1", name: "Key Vault Secrets User", description: "Read secret contents.", suggested: "read" },
    { id: "2", name: "Owner", description: "Grants full access to manage all resources.", privileged: true },
    { id: "3", name: "Storage Blob Data Reader", description: "Read and list Azure Storage containers and blobs." },
    { id: "roles/storage.objectViewer", name: "Storage Object Viewer", description: "View objects." },
  ]
  test("suggested first, then by match quality", () => {
    expect(filterRoles(roles, "").map((r) => r.id)).toEqual(["1", "2", "3", "roles/storage.objectViewer"])
    expect(filterRoles(roles, "read").map((r) => r.id)).toEqual(["1", "3"])
    expect(filterRoles(roles, "owner").map((r) => r.id)).toEqual(["2"])
    expect(filterRoles(roles, "storage.objectviewer").map((r) => r.id)).toEqual(["roles/storage.objectViewer"])
    expect(filterRoles(roles, "blob list").map((r) => r.id)).toEqual(["3"])
    expect(filterRoles(roles, "", 2)).toHaveLength(2)
  })
  test("suggested label", () => {
    expect(suggestedLabel(roles[0])).toBe("Suggested for Read")
    expect(suggestedLabel(roles[1])).toBeUndefined()
  })
})
