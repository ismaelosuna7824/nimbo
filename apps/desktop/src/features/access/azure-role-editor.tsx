import { useQuery } from "@tanstack/react-query"
import { CheckIcon, KeyRoundIcon, UserRoundIcon } from "lucide-react"
import { useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api/client"
import type { AzureRoleAssignment } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { ReferencePicker } from "../inspector/reference-picker"
import { ExprInput, FieldLabel, Problems, RolePicker, RoleSource } from "./azure-gcp-parts"
import { referencedNode } from "./azure-gcp-roles"
import { AZURE_PRINCIPAL_TYPES, azureChanged, azureIdentityOptions, azureProblems, azureSentence } from "./azure-role"
import type { AccessEditorProps } from "./editors"
import { AccessWarnings, ReadOnlySource } from "./parts"

const OTHER = "__other__"
const ANY_TYPE = "__any__"

/**
 * An azurerm_role_assignment as "Who" (a managed identity of the graph, or any principal ID), "can do" (a built-in
 * role, searchable, suggested for the scope's resource type first) and "on what" (the scope).
 */
export function AzureRoleEditor({ node, view, save, select }: AccessEditorProps) {
  const saved = view.azure
  const file = useWorkflowStore((s) => node.file ?? s.workflow!.nodeFiles?.[node.id])
  const nodes = useWorkflowStore((s) => s.workflow!.nodes)
  const [draft, setDraft] = useSyncedState(saved)
  const [saving, setSaving] = useState(false)
  const scopeNode = draft ? referencedNode(draft.scope, nodes) : undefined
  const resourceType = scopeNode?.type ?? ""
  const catalog = useQuery({
    queryKey: ["access-catalog", "azure", resourceType],
    queryFn: () => api.accessCatalog("azure", resourceType ? { resourceType } : {}),
    staleTime: Infinity,
  })

  if (!saved || !draft) return null
  if (!saved.editable) {
    return <ReadOnlySource title="Role assignment kept as code" source={assignmentSource(saved)} reason={saved.reason} file={file} />
  }
  const identities = azureIdentityOptions(nodes.filter((n) => n.id !== node.id))
  const picked = identities.find((o) => o.expr && o.expr === draft.principalId.trim())
  const who = referencedNode(draft.principalId, nodes)
  const dirty = azureChanged(saved, draft)
  const problems = azureProblems(draft)
  const set = (patch: Partial<AzureRoleAssignment>) => setDraft({ ...draft, ...patch })
  const submit = async () => {
    setSaving(true)
    try {
      await save({ nodeId: node.id, azure: { ...draft, editable: true } })
    } catch {
      // save() already told the user; the draft stays for another try.
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3" data-testid="azure-role-editor">
      <p className="rounded bg-muted/60 px-2 py-1 text-[11px] break-words" data-testid="azure-role-sentence">
        {azureSentence(draft, who ? `${who.name} (${who.type})` : undefined, scopeNode ? `${scopeNode.name} (${scopeNode.type})` : undefined)}
      </p>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint="a managed identity, user, group or app">Who</FieldLabel>
        <div className="flex flex-wrap items-center gap-1.5">
          <Select
            value={picked ? picked.expr : OTHER}
            onValueChange={(v) => v !== OTHER && set({ principalId: v, principalType: draft.principalType ? "ServicePrincipal" : undefined })}
          >
            <SelectTrigger size="sm" className="h-7 min-w-0 flex-1 text-xs" aria-label="Who" data-testid="azure-principal">
              <SelectValue>
                <span className="flex min-w-0 items-center gap-1.5">
                  <UserRoundIcon className="size-3 shrink-0" />
                  <span className="truncate font-mono">{picked ? picked.address : "Other principal"}</span>
                </span>
              </SelectValue>
            </SelectTrigger>
            <SelectContent className="max-w-96">
              <SelectItem value={OTHER} textValue="Other principal">
                <span className="text-xs">{picked ? "Other principal (type its ID below)" : "Principal ID below"}</span>
              </SelectItem>
              {identities.map((o) => (
                <SelectItem key={o.nodeId} value={o.expr || `none:${o.nodeId}`} disabled={!o.expr} textValue={o.address}>
                  <span className="flex flex-col gap-0.5 py-0.5">
                    <span className="flex items-center gap-1.5 text-xs">
                      <UserRoundIcon className="size-3" />
                      <span className="font-mono">{o.address}</span>
                    </span>
                    <span className="text-[11px] whitespace-normal text-muted-foreground">{o.hint ?? `${o.kind}: ${o.expr}`}</span>
                  </span>
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <ReferencePicker onPick={(expr) => set({ principalId: expr })} />
        </div>
        <ExprInput
          label="Principal ID"
          value={draft.principalId}
          onChange={(principalId) => set({ principalId })}
          placeholder="azurerm_user_assigned_identity.app.principal_id"
          testId="azure-principal-id"
        />
        <div className="flex items-center gap-1.5">
          <span className="text-[11px] text-muted-foreground">Principal type</span>
          <Select value={draft.principalType || ANY_TYPE} onValueChange={(v) => set({ principalType: v === ANY_TYPE ? undefined : v })}>
            <SelectTrigger size="sm" className="h-6 w-auto text-[11px]" aria-label="Principal type" data-testid="azure-principal-type">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={ANY_TYPE}>Not set (Azure looks it up)</SelectItem>
              {AZURE_PRINCIPAL_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t === "ServicePrincipal" ? "ServicePrincipal (apps, managed identities)" : t}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
      </div>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint={resourceType ? `roles that fit ${resourceType} first` : "a built-in role"}>Can do</FieldLabel>
        {draft.roleId ? (
          <>
            <ExprInput
              label="Role definition ID"
              value={draft.roleId}
              onChange={(roleId) => set({ roleId })}
              placeholder="data.azurerm_role_definition.reader.id"
              testId="azure-role-id"
            />
            <Button
              variant="ghost"
              size="xs"
              className="self-start text-[11px] text-muted-foreground"
              onClick={() => set({ roleId: undefined, roleName: "" })}
              data-testid="azure-use-role-name"
            >
              <KeyRoundIcon />
              Pick a built-in role by name instead
            </Button>
          </>
        ) : (
          <RolePicker
            value={draft.roleName ?? ""}
            roles={catalog.data?.roles ?? []}
            loading={catalog.isPending}
            by="name"
            privilegedLabel="Privileged"
            placeholder="Search built-in roles…"
            custom={(text) => text.length > 2}
            onPick={(roleName) => set({ roleName, roleId: undefined })}
            testId="azure-role"
          />
        )}
        <AccessWarnings warnings={view.warnings} path="role" />
      </div>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint="the one resource it needs (scope)">On what</FieldLabel>
        <div className="flex items-center gap-1.5">
          <ExprInput label="Scope" value={draft.scope} onChange={(scope) => set({ scope })} placeholder="azurerm_key_vault.main.id" testId="azure-scope" />
          <ReferencePicker onPick={(expr) => set({ scope: expr })} />
        </div>
        {scopeNode && (
          <button
            type="button"
            className="self-start font-mono text-[11px] text-muted-foreground underline-offset-2 hover:underline"
            onClick={() => select(scopeNode.id)}
            data-testid="azure-scope-node"
          >
            {scopeNode.kind === "data" ? "data." : ""}
            {scopeNode.type}.{scopeNode.name}
          </button>
        )}
        <AccessWarnings warnings={view.warnings} path="scope" />
      </div>

      <Problems problems={dirty ? problems : []} testId="azure-problems" />
      {dirty && (
        <div className="flex items-center gap-1.5">
          <span className="flex-1" />
          <Button variant="ghost" size="xs" onClick={() => setDraft(saved)} disabled={saving} data-testid="azure-discard">
            Discard
          </Button>
          <Button size="xs" onClick={() => void submit()} disabled={saving || problems.length > 0} data-testid="azure-save">
            <CheckIcon />
            Save role assignment
          </Button>
        </div>
      )}
      <RoleSource source={catalog.data?.source} />
    </div>
  )
}

/** The assignment's fields as HCL, for the read-only view. */
function assignmentSource(ra: AzureRoleAssignment): string {
  const lines = [`scope = ${ra.scope}`]
  if (ra.roleName) lines.push(`role_definition_name = "${ra.roleName}"`)
  if (ra.roleId) lines.push(`role_definition_id = ${ra.roleId}`)
  lines.push(`principal_id = ${ra.principalId}`)
  if (ra.principalType) lines.push(`principal_type = "${ra.principalType}"`)
  return lines.join("\n")
}
