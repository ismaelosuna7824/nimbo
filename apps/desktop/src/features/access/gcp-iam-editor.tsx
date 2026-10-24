import { useQuery } from "@tanstack/react-query"
import { CheckIcon, PlusIcon, Trash2Icon, UserRoundIcon } from "lucide-react"
import { useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api/client"
import type { GCPIAMBinding, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { ReferencePicker } from "../inspector/reference-picker"
import { ExprInput, FieldLabel, Problems, RolePicker, RoleSource } from "./azure-gcp-parts"
import { referencedNode } from "./azure-gcp-roles"
import type { AccessEditorProps } from "./editors"
import {
  buildMember,
  GCP_MEMBER_LABELS,
  GCP_MEMBER_TYPES,
  type GCPMember,
  type GCPMemberType,
  gcpChanged,
  gcpProblems,
  isPublicMember,
  parseMember,
  refValue,
  serviceAccountOptions,
} from "./gcp-member"
import { AccessWarnings, ReadOnlySource } from "./parts"

const CUSTOM_ROLE = /^(projects|organizations)\/[^/]+\/roles\/[A-Za-z0-9_.]+$/
const NONE = "__none__"

/**
 * A google_*_iam_member or google_*_iam_binding as "Who" (members: service accounts, groups, users, domains),
 * "can do" (a predefined role, searchable, suggested for the resource first, basic roles flagged) and "on what"
 * (the resource attribute). The condition block is shown read-only and kept as written.
 */
export function GCPIAMEditor({ node, view, save, select }: AccessEditorProps) {
  const saved = view.gcp
  const file = useWorkflowStore((s) => node.file ?? s.workflow!.nodeFiles?.[node.id])
  const nodes = useWorkflowStore((s) => s.workflow!.nodes)
  const [draft, setDraft] = useSyncedState(saved)
  const [saving, setSaving] = useState(false)
  const resourceNode = draft ? referencedNode(draft.resource ?? "", nodes) : undefined
  // Suggestions for the resource's type, or for the IAM type (google_storage_bucket_iam_member).
  const resourceType = resourceNode?.type ?? node.type
  const catalog = useQuery({
    queryKey: ["access-catalog", "gcp", resourceType],
    queryFn: () => api.accessCatalog("gcp", { resourceType }),
    staleTime: Infinity,
  })

  if (!saved || !draft) return null
  if (!saved.editable) {
    return <ReadOnlySource title="IAM grant kept as code" source={bindingSource(saved)} reason={saved.reason} file={file} />
  }
  const dirty = gcpChanged(saved, draft)
  const problems = gcpProblems(draft)
  const binding = draft.kind === "binding"
  const set = (patch: Partial<GCPIAMBinding>) => setDraft({ ...draft, ...patch })
  const setMember = (i: number, m: string) => set({ members: draft.members.map((x, j) => (j === i ? m : x)) })
  const role = catalog.data?.roles?.find((r) => r.id === draft.role)
  const submit = async () => {
    setSaving(true)
    try {
      await save({ nodeId: node.id, gcp: { ...draft, members: draft.members.filter((m) => m.trim()), editable: true } })
    } catch {
      // save() already told the user; the draft stays for another try.
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-3" data-testid="gcp-iam-editor">
      <p className="rounded bg-muted/60 px-2 py-1 text-[11px] break-words" data-testid="gcp-iam-sentence">
        {memberSummary(draft.members)} can do {role ? `${role.name} (${draft.role})` : draft.role || "a role"} on{" "}
        {resourceNode ? `${resourceNode.name} (${resourceNode.type})` : draft.resource || "a resource"}.
      </p>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint={binding ? "every member of this binding" : "one member"}>Who</FieldLabel>
        {draft.members.map((m, i) => {
          const k = saved.members.findIndex((s) => s.trim() === m.trim())
          return (
            <div key={i} className="flex flex-col gap-1">
              <MemberRow
                index={i}
                member={m}
                nodes={nodes}
                removable={binding && draft.members.length > 1}
                onChange={(next) => setMember(i, next)}
                onRemove={() => set({ members: draft.members.filter((_, j) => j !== i) })}
              />
              {k >= 0 && <AccessWarnings warnings={view.warnings} path={`members/${k}`} />}
            </div>
          )
        })}
        {(binding || draft.members.length === 0) && (
          <Button
            variant="outline"
            size="xs"
            className="self-start"
            onClick={() => set({ members: [...draft.members, buildMember({ type: "group", value: "" })] })}
            data-testid="gcp-add-member"
          >
            <PlusIcon />
            Add member
          </Button>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint="a predefined role">Can do</FieldLabel>
        <RolePicker
          value={draft.role}
          roles={catalog.data?.roles ?? []}
          loading={catalog.isPending}
          by="id"
          privilegedLabel="Basic role"
          placeholder="Search roles: objectViewer, secretAccessor…"
          custom={(text) => CUSTOM_ROLE.test(text)}
          onPick={(r) => set({ role: r })}
          testId="gcp-role"
        />
        <AccessWarnings warnings={view.warnings} path="role" />
      </div>

      <div className="flex flex-col gap-1.5">
        <FieldLabel hint={draft.resourceAttr}>On what</FieldLabel>
        <div className="flex items-center gap-1.5">
          <ExprInput
            label={`Resource (${draft.resourceAttr ?? "resource"})`}
            value={draft.resource ?? ""}
            onChange={(resource) => set({ resource })}
            placeholder="google_storage_bucket.reports.name"
            testId="gcp-resource"
          />
          <ReferencePicker onPick={(expr) => set({ resource: expr })} />
        </div>
        {resourceNode && (
          <button
            type="button"
            className="self-start font-mono text-[11px] text-muted-foreground underline-offset-2 hover:underline"
            onClick={() => select(resourceNode.id)}
            data-testid="gcp-resource-node"
          >
            {resourceNode.kind === "data" ? "data." : ""}
            {resourceNode.type}.{resourceNode.name}
          </button>
        )}
        <AccessWarnings warnings={view.warnings} path="scope" />
      </div>

      {saved.condition && (
        <ReadOnlySource title="Condition (kept as written)" source={saved.condition} reason="IAM conditions are edited in code." file={file} />
      )}

      <Problems problems={dirty ? problems : []} testId="gcp-problems" />
      {dirty && (
        <div className="flex items-center gap-1.5">
          <span className="flex-1" />
          <Button variant="ghost" size="xs" onClick={() => setDraft(saved)} disabled={saving} data-testid="gcp-discard">
            Discard
          </Button>
          <Button size="xs" onClick={() => void submit()} disabled={saving || problems.length > 0} data-testid="gcp-save">
            <CheckIcon />
            Save IAM grant
          </Button>
        </div>
      )}
      <RoleSource source={catalog.data?.source} />
    </div>
  )
}

/** One member: its type, and the account / group / user / domain (typed, picked from the graph, or linked). */
function MemberRow({
  index: i,
  member,
  nodes,
  removable,
  onChange,
  onRemove,
}: {
  index: number
  member: string
  nodes: WorkflowNode[]
  removable: boolean
  onChange: (member: string) => void
  onRemove: () => void
}) {
  const m = parseMember(member)
  const update = (next: GCPMember) => onChange(buildMember(next))
  const accounts = serviceAccountOptions(nodes)
  return (
    <div className="flex flex-wrap items-center gap-1.5" data-testid={`gcp-member-${i}`}>
      <Select
        value={m.type}
        onValueChange={(type) => {
          const t = type as GCPMemberType
          if (t === "expression") update({ type: t, value: member })
          else update({ type: t, value: isPublicMember(t) || m.type === "expression" ? "" : m.value })
        }}
      >
        <SelectTrigger size="sm" className="h-7 w-auto text-xs" aria-label="Member type" data-testid={`gcp-member-type-${i}`}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {[...GCP_MEMBER_TYPES, ...(m.type === "principal" || m.type === "principalSet" ? [m.type] : []), "expression" as const].map((t) => (
            <SelectItem key={t} value={t}>
              {GCP_MEMBER_LABELS[t]}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {m.type === "expression" ? (
        <ExprInput label="Member expression" value={m.value} onChange={(value) => update({ type: "expression", value })} placeholder="var.member" testId={`gcp-member-value-${i}`} />
      ) : isPublicMember(m.type) ? (
        <span className="text-[11px] text-amber-700 dark:text-amber-400">Public: no sign-in or a Google account is enough.</span>
      ) : (
        <>
          <MemberValue index={i} value={m.value} placeholder={placeholderOf(m.type)} onChange={(value) => update({ ...m, value })} />
          {m.type === "serviceAccount" && accounts.length > 0 && (
            <Select value={accounts.some((a) => a.value === m.value) ? m.value : NONE} onValueChange={(v) => v !== NONE && update({ ...m, value: v })}>
              <SelectTrigger size="sm" className="h-7 w-auto gap-1 text-xs" aria-label="Pick a service account" data-testid={`gcp-member-account-${i}`}>
                <UserRoundIcon className="size-3" />
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE} disabled>
                  Service accounts and service agents of this workflow
                </SelectItem>
                {accounts.map((a) => (
                  <SelectItem key={a.key} value={a.value}>
                    <span className="text-xs">{a.label}</span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <ReferencePicker onPick={(expr) => update({ ...m, value: refValue(expr) })} />
        </>
      )}
      {removable && (
        <Button variant="ghost" size="icon-xs" aria-label="Remove member" onClick={onRemove} data-testid={`gcp-member-remove-${i}`}>
          <Trash2Icon />
        </Button>
      )}
    </div>
  )
}

/** The text after "type:", committed when left or on Enter. */
function MemberValue({ index, value, placeholder, onChange }: { index: number; value: string; placeholder: string; onChange: (v: string) => void }) {
  const [text, setText] = useSyncedState(value)
  const commit = () => {
    if (text !== value) onChange(text)
  }
  return (
    <Input
      aria-label="Member"
      className="h-7 w-40 min-w-0 flex-1 font-mono text-xs"
      placeholder={placeholder}
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit()
      }}
      data-testid={`gcp-member-value-${index}`}
    />
  )
}

function placeholderOf(t: GCPMemberType): string {
  switch (t) {
    case "serviceAccount":
      return "${google_service_account.app.email}"
    case "group":
      return "analysts@example.com or ${var.group}"
    case "user":
      return "ana@example.com"
    case "domain":
      return "example.com"
    default:
      return "value"
  }
}

/** "group:${var.analysts} and 1 more". */
function memberSummary(members: string[]): string {
  const shown = members.filter((m) => m.trim())
  if (shown.length === 0) return "Nobody"
  const first = parseMember(shown[0])
  const label =
    first.type === "expression" ? first.value : isPublicMember(first.type) ? GCP_MEMBER_LABELS[first.type] : `${first.type}:${first.value}`
  return shown.length === 1 ? label : `${label} and ${shown.length - 1} more`
}

/** The binding's fields as HCL, for the read-only view. */
function bindingSource(b: GCPIAMBinding): string {
  const lines: string[] = []
  if (b.resourceAttr) lines.push(`${b.resourceAttr} = ${b.resource ?? ""}`)
  lines.push(`role = ${b.role.startsWith("roles/") || b.role.includes("/roles/") ? `"${b.role}"` : b.role}`)
  if (b.kind === "member") lines.push(`member = ${b.members[0] ?? ""}`)
  else lines.push(`members = [${b.members.join(", ")}]`)
  if (b.condition) lines.push(b.condition)
  return lines.join("\n")
}
