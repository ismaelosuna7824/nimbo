// Inspector RBAC editors of a Kubernetes workspace (GET/PUT …/kubernetes/access): a Role's or ClusterRole's rules, a
// binding's role and subjects, a ServiceAccount's token, and a workload's identity. What the daemon can't write back
// (Helm templates, Kustomize patches) shows read-only with "Edit in code".

import { useQuery } from "@tanstack/react-query"
import { KeyRoundIcon, PlusIcon, ShieldIcon, ShieldPlusIcon, Trash2Icon, XIcon } from "lucide-react"
import { useCallback, useMemo, useState } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { useSyncedState } from "@/components/use-synced-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { Toggle } from "@/components/ui/toggle"
import { AccessWarnings, LeastPrivilegeOk, ReadOnlySource } from "@/features/access/parts"
import { CommitInput, Field, ListEditor, Section } from "@/features/docker/inspector/fields"
import { api } from "@/lib/api/client"
import type { AccessRelated, KubeAccessEdit, KubeAccessView, KubeGraph, KubeNode, KubeRule, KubeSubject } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { giveOwnRole, showObject } from "../actions"
import {
  blankRule,
  catalogGroups,
  catalogResources,
  groupLabel,
  LEVEL_VERBS,
  roleKey,
  roleOptions,
  ruleProblem,
  ruleSentence,
  serviceAccountNameProblem,
  serviceAccountsIn,
  subjectComplete,
  subjectLabel,
  toggleVerb,
  VERB_GROUPS,
  verbsLevel,
} from "./rbac-model"

type Save = (edit: Omit<KubeAccessEdit, "nodeId">) => Promise<boolean>

/** The RBAC view of an object (refreshed by every saved edit) and how to save an edit of it. */
function useKubeAccess(node: KubeNode) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const query = useQuery({
    queryKey: ["kubernetes-access", workflowId, node.id, updatedAt],
    queryFn: () => api.kubernetesAccess(workflowId, node.id),
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[2] === node.id ? prev : undefined),
    retry: false,
  })
  const save: Save = useCallback(
    async (edit) => {
      await useWorkflowStore.getState().flush()
      try {
        setWorkflow(await api.putKubernetesAccess(workflowId, { nodeId: node.id, ...edit }))
        return true
      } catch (err) {
        toast.error(`Couldn't update ${node.name}`, { description: errorMessage(err) })
        return false
      }
    },
    [workflowId, node.id, node.name, setWorkflow],
  )
  return { query, save }
}

function useRbacCatalog() {
  return useQuery({ queryKey: ["access-catalog", "kubernetes"], queryFn: () => api.accessCatalog("kubernetes"), staleTime: Infinity })
}

/** The inspector of a Role, ClusterRole, RoleBinding, ClusterRoleBinding or ServiceAccount. */
export function KubeAccessPanel({ node }: { node: KubeNode }) {
  const { query, save } = useKubeAccess(node)
  const view = query.data
  return (
    <Section title="Access" icon={<KeyRoundIcon className="size-3.5 text-amber-500" />}>
      <div className="flex flex-col gap-3" data-testid="k8s-rbac-panel" data-kind={node.kind}>
        {query.isPending && !view ? (
          <Loading />
        ) : query.isError && !view ? (
          <ReadOnlySource title="Can't show these permissions visually" reason={errorMessage(query.error)} file={node.file} line={node.line} />
        ) : view ? (
          <>
            <AccessWarnings warnings={view.warnings.filter((w) => !w.path)} />
            {!view.editable && <ReadOnlySource title="Read-only here" reason={view.reason} file={node.file} line={node.line} />}
            {node.kind === "Role" || node.kind === "ClusterRole" ? (
              <RoleRules node={node} view={view} save={save} />
            ) : node.kind === "ServiceAccount" ? (
              <ServiceAccountForm node={node} view={view} save={save} />
            ) : (
              <BindingForm node={node} view={view} save={save} />
            )}
            {view.warnings.length === 0 && <LeastPrivilegeOk />}
          </>
        ) : null}
      </div>
    </Section>
  )
}

function Loading() {
  return (
    <div className="flex flex-col gap-2">
      <Skeleton className="h-16" />
      <Skeleton className="h-16" />
    </div>
  )
}

/** Related objects with what links them ("Runs as it", "Role reader in shop: get, list configmaps"). */
function RelatedList({ related, empty, testId }: { related: AccessRelated[]; empty: string; testId: string }) {
  if (related.length === 0) return <p className="text-[11px] text-muted-foreground">{empty}</p>
  return (
    <ul className="flex flex-col gap-1" data-testid={testId}>
      {related.map((r) => (
        <li key={r.nodeId}>
          <button
            type="button"
            onClick={() => void showObject(r.nodeId)}
            className="flex w-full min-w-0 flex-col items-start rounded-md border px-2 py-1.5 text-left hover:bg-accent"
          >
            <span className="truncate font-mono text-xs">{r.address}</span>
            <span className="text-[11px] text-muted-foreground">{r.label}</span>
          </button>
        </li>
      ))}
    </ul>
  )
}

// ---------------------------------------------------------------------------
// Roles

/** A Role's rules: "on what" (API group, resources, names) and "can do" (verbs), one card per rule. */
function RoleRules({ node, view, save }: { node: KubeNode; view: KubeAccessView; save: Save }) {
  const catalog = useRbacCatalog()
  const saved = useMemo(() => view.rules ?? [], [view])
  const [rules, setRules] = useSyncedState<KubeRule[]>(saved)
  const commit = (next: KubeRule[]) => {
    setRules(next)
    if (next.every((r) => !ruleProblem(r, node.kind))) void save({ rules: next })
  }
  const bindings = (view.related ?? []).filter((r) => r.address.includes("Binding"))
  if (!view.editable) {
    return (
      <ul className="flex flex-col gap-1 text-xs" data-testid="k8s-rbac-rules">
        {saved.length === 0 && <li className="text-muted-foreground">No permissions.</li>}
        {saved.map((r, i) => (
          <li key={i} className="rounded-md border px-2 py-1.5">
            {ruleSentence(r)}
            <AccessWarnings warnings={view.warnings} path={`rules/${i}`} className="mt-1.5" />
          </li>
        ))}
      </ul>
    )
  }
  return (
    <div className="flex flex-col gap-2.5" data-testid="k8s-rbac-rules">
      {rules.length === 0 && <p className="text-[11px] text-muted-foreground">No permissions yet: add a rule saying what it can do, on what.</p>}
      {rules.map((r, i) => (
        <RuleCard
          key={i}
          index={i}
          rule={r}
          kind={node.kind}
          groups={catalogGroups(catalog.data)}
          resources={(g) => catalogResources(catalog.data, g)}
          warnings={i < saved.length ? view.warnings : []}
          onChange={(next) => commit(rules.map((x, j) => (j === i ? next : x)))}
          onRemove={() => commit(rules.filter((_, j) => j !== i))}
        />
      ))}
      <Button type="button" variant="outline" size="xs" className="self-start" data-testid="k8s-rbac-add-rule" onClick={() => setRules([...rules, blankRule()])}>
        <PlusIcon />
        Add rule
      </Button>
      <Field label="Given to">
        <RelatedList related={bindings} empty="No binding gives this role to anyone yet: drag a workload onto it to run with it." testId="k8s-rbac-role-bindings" />
      </Field>
      {catalog.data && (
        <p className="text-[10.5px] text-muted-foreground">
          Resources and verbs from{" "}
          <a href={catalog.data.source.url} target="_blank" rel="noreferrer" className="underline">
            {catalog.data.source.title}
          </a>{" "}
          ({catalog.data.source.retrieved}).
        </p>
      )}
    </div>
  )
}

const CORE = "core"

function RuleCard({
  index,
  rule,
  kind,
  groups,
  resources,
  warnings,
  onChange,
  onRemove,
}: {
  index: number
  rule: KubeRule
  kind: string
  groups: string[]
  resources: (group: string) => { name: string; description?: string }[]
  warnings: KubeAccessView["warnings"]
  onChange: (rule: KubeRule) => void
  onRemove: () => void
}) {
  const problem = ruleProblem(rule, kind)
  const urls = rule.nonResourceURLs ?? []
  const group = rule.apiGroups.length === 1 ? rule.apiGroups[0] : undefined
  const level = verbsLevel(rule.verbs)
  const listId = `k8s-rbac-resources-${index}-list`
  return (
    <div className="flex flex-col gap-2 rounded-lg border p-2.5" data-testid={`k8s-rbac-rule-${index}`}>
      <div className="flex items-start gap-2">
        <p className="min-w-0 flex-1 text-xs font-medium" data-testid={`k8s-rbac-rule-${index}-sentence`}>
          {ruleSentence(rule)}
        </p>
        <Button type="button" variant="ghost" size="icon-xs" aria-label="Remove rule" data-testid={`k8s-rbac-remove-rule-${index}`} onClick={onRemove}>
          <Trash2Icon />
        </Button>
      </div>
      {urls.length > 0 ? (
        <Field label="On the URLs">
          <Chips values={urls} onChange={(nonResourceURLs) => onChange({ ...rule, nonResourceURLs })} placeholder="/healthz" testId={`k8s-rbac-urls-${index}`} />
        </Field>
      ) : (
        <>
          <Field label="API group">
            {group !== undefined || rule.apiGroups.length === 0 ? (
              <Select
                value={group === undefined ? undefined : group === "" ? CORE : group}
                onValueChange={(g) => onChange({ ...rule, apiGroups: [g === CORE ? "" : g], resources: [] })}
              >
                <SelectTrigger size="sm" className="h-8 w-full text-xs" aria-label="API group" data-testid={`k8s-rbac-group-${index}`}>
                  <SelectValue placeholder="Pick a group" />
                </SelectTrigger>
                <SelectContent>
                  {[...new Set([...(group !== undefined && !groups.includes(group) ? [group] : []), ...groups])].map((g) => (
                    <SelectItem key={g} value={g === "" ? CORE : g}>
                      {g === "" ? "core (ConfigMaps, Secrets, Pods, Services…)" : groupLabel(g)}
                    </SelectItem>
                  ))}
                  <SelectItem value="*">every API group (*)</SelectItem>
                </SelectContent>
              </Select>
            ) : (
              <Chips values={rule.apiGroups} onChange={(apiGroups) => onChange({ ...rule, apiGroups })} placeholder="group" testId={`k8s-rbac-groups-${index}`} />
            )}
          </Field>
          <Field label="On what">
            <Chips
              values={rule.resources ?? []}
              onChange={(r) => onChange({ ...rule, resources: r })}
              placeholder="configmaps"
              suggestions={group !== undefined ? resources(group) : []}
              listId={listId}
              testId={`k8s-rbac-resources-${index}`}
            />
          </Field>
          <Field label="Only these names" hint="Empty: every object of those resources in the namespace.">
            <Chips values={rule.resourceNames ?? []} onChange={(resourceNames) => onChange({ ...rule, resourceNames })} placeholder="name" testId={`k8s-rbac-names-${index}`} />
          </Field>
        </>
      )}
      <Field label="Can do" badge={level && <Badge variant="secondary">{level === "read" ? "Read" : level === "write" ? "Write" : "Manage"}</Badge>}>
        <div className="flex flex-col gap-1.5">
          {VERB_GROUPS.map((g) => (
            <div key={g.id} className="flex flex-wrap items-center gap-1">
              <span className="w-14 shrink-0 text-[11px] text-muted-foreground">{g.label}</span>
              {g.verbs.map((v) => (
                <Toggle
                  key={v}
                  size="sm"
                  variant="outline"
                  className={cn("h-6 px-1.5 font-mono text-[11px]", v === "*" && "text-amber-600 dark:text-amber-400")}
                  pressed={rule.verbs.includes(v)}
                  onPressedChange={() => onChange({ ...rule, verbs: toggleVerb(rule.verbs, v) })}
                  data-testid={`k8s-rbac-verb-${index}-${v}`}
                >
                  {v}
                </Toggle>
              ))}
            </div>
          ))}
          <div className="flex flex-wrap items-center gap-1">
            <span className="w-14 shrink-0 text-[11px] text-muted-foreground">Presets</span>
            {(["read", "write", "manage"] as const).map((l) => (
              <Button key={l} type="button" variant="ghost" size="xs" className="h-6 text-[11px]" data-testid={`k8s-rbac-preset-${index}-${l}`} onClick={() => onChange({ ...rule, verbs: [...LEVEL_VERBS[l]] })}>
                {l === "read" ? "Read" : l === "write" ? "Write" : "Manage"}
              </Button>
            ))}
          </div>
        </div>
      </Field>
      {problem && <p className="text-[11px] text-destructive">{problem} Not saved yet.</p>}
      <AccessWarnings warnings={warnings} path={`rules/${index}`} />
    </div>
  )
}

/** Values as removable chips plus a field adding one (Enter or comma), with optional suggestions. */
function Chips({
  values,
  onChange,
  placeholder,
  suggestions = [],
  listId,
  testId,
}: {
  values: string[]
  onChange: (values: string[]) => void
  placeholder: string
  suggestions?: { name: string; description?: string }[]
  listId?: string
  testId: string
}) {
  const [text, setText] = useState("")
  const add = () => {
    const v = text.trim().replace(/,$/, "")
    setText("")
    if (v && !values.includes(v)) onChange([...values, v])
  }
  return (
    <div className="flex flex-wrap items-center gap-1" data-testid={testId}>
      {values.map((v) => (
        <Badge key={v} variant="outline" className="gap-0.5 pr-0.5 font-mono text-[11px]">
          {v === "" ? '"" (core)' : v}
          <button type="button" aria-label={`Remove ${v}`} className="rounded p-0.5 hover:bg-muted" onClick={() => onChange(values.filter((x) => x !== v))}>
            <XIcon className="size-3" />
          </button>
        </Badge>
      ))}
      <Input
        value={text}
        list={listId}
        placeholder={placeholder}
        aria-label={placeholder}
        data-testid={`${testId}-input`}
        className="h-7 w-32 min-w-0 flex-1 font-mono text-xs"
        onChange={(e) => {
          if (e.target.value.endsWith(",")) {
            setText(e.target.value)
            return add()
          }
          setText(e.target.value)
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            add()
          }
        }}
        onBlur={add}
      />
      {listId && (
        <datalist id={listId}>
          {suggestions.map((s) => (
            <option key={s.name} value={s.name}>
              {s.description}
            </option>
          ))}
        </datalist>
      )}
    </div>
  )
}

// ---------------------------------------------------------------------------
// Bindings

const SUBJECT_KINDS = ["ServiceAccount", "User", "Group"]

/** A binding: which role ("Grants") to whom ("Who"). */
function BindingForm({ node, view, save }: { node: KubeNode; view: KubeAccessView; save: Save }) {
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph)
  const options = roleOptions(graph, node)
  const current = view.roleRef
  if (current && !options.some((o) => roleKey(o) === roleKey(current))) options.unshift({ kind: current.kind as "Role" | "ClusterRole", name: current.name })
  const role = current ? options.find((o) => roleKey(o) === roleKey(current)) : undefined
  const subjects = view.subjects ?? []
  return (
    <div className="flex flex-col gap-3">
      <Field label="Grants">
        {view.editable ? (
          <Select
            value={current ? roleKey(current) : undefined}
            onValueChange={(v) => {
              const o = options.find((x) => roleKey(x) === v)
              if (o) void save({ roleRef: { kind: o.kind, name: o.name } })
            }}
          >
            <SelectTrigger size="sm" className="h-8 w-full text-xs" aria-label="Role" data-testid="k8s-rbac-roleref">
              <SelectValue placeholder="Pick a role" />
            </SelectTrigger>
            <SelectContent>
              {options.map((o) => (
                <SelectItem key={roleKey(o)} value={roleKey(o)}>
                  <span className={cn(o.broad && "text-amber-600 dark:text-amber-400")}>
                    {o.kind} {o.name}
                  </span>
                  {o.hint && <span className="text-muted-foreground"> · {o.hint}</span>}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <span className="font-mono text-xs">{current ? `${current.kind} ${current.name}` : "—"}</span>
        )}
        {role?.nodeId && (
          <button type="button" className="self-start text-[11px] text-primary hover:underline" onClick={() => void showObject(role.nodeId!)}>
            Open {role.kind} {role.name}
          </button>
        )}
        {node.kind === "RoleBinding" && current?.kind === "ClusterRole" && (
          <p className="text-[11px] text-muted-foreground">A RoleBinding applies the ClusterRole's rules in {node.namespace} only.</p>
        )}
      </Field>
      <AccessWarnings warnings={view.warnings} path="roleRef" />
      <Field label="Who">
        {view.editable ? (
          <div data-testid="k8s-rbac-subjects">
            <ListEditor<KubeSubject>
              items={subjects}
              empty="Nobody: add a ServiceAccount, user or group."
              addLabel="Add subject"
              blank={() => ({ kind: "ServiceAccount", name: "", namespace: node.namespace })}
              complete={(s) => subjectComplete(s, node.kind)}
              onChange={(next) => void save({ subjects: next })}
              render={(s, update, i) => <SubjectRow subject={s} update={update} index={i} graph={graph} bindingNamespace={node.namespace} />}
            />
          </div>
        ) : (
          <ul className="flex flex-col gap-0.5 font-mono text-xs" data-testid="k8s-rbac-subjects">
            {subjects.map((s, i) => (
              <li key={i}>{subjectLabel(s)}</li>
            ))}
          </ul>
        )}
      </Field>
      <AccessWarnings warnings={view.warnings} path="subjects" />
    </div>
  )
}

function SubjectRow({
  subject: s,
  update,
  index,
  graph,
  bindingNamespace,
}: {
  subject: KubeSubject
  update: (s: KubeSubject) => void
  index: number
  graph?: KubeGraph
  bindingNamespace?: string
}) {
  const listId = `k8s-rbac-subject-${index}-sas`
  return (
    <>
      <Select value={s.kind} onValueChange={(kind) => update({ ...s, kind, namespace: kind === "ServiceAccount" ? (s.namespace ?? bindingNamespace) : undefined })}>
        <SelectTrigger size="sm" className="h-8 w-32 shrink-0 text-xs" aria-label="Subject kind" data-testid={`k8s-rbac-subject-${index}-kind`}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {SUBJECT_KINDS.map((k) => (
            <SelectItem key={k} value={k}>
              {k}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <CommitInput aria-label="Subject name" className="font-mono" placeholder="name" list={s.kind === "ServiceAccount" ? listId : undefined} value={s.name} onCommit={(name) => update({ ...s, name: name.trim() })} data-testid={`k8s-rbac-subject-${index}-name`} />
      {s.kind === "ServiceAccount" && (
        <>
          <CommitInput aria-label="Subject namespace" width="basis-24" className="font-mono" placeholder="namespace" value={s.namespace ?? ""} onCommit={(ns) => update({ ...s, namespace: ns.trim() || undefined })} />
          <datalist id={listId}>
            {serviceAccountsIn(graph, s.namespace ?? bindingNamespace).map((n) => (
              <option key={n} value={n} />
            ))}
          </datalist>
        </>
      )}
    </>
  )
}

// ---------------------------------------------------------------------------
// ServiceAccounts and workloads

/** A ServiceAccount: its token, who runs as it, what it is given. */
function ServiceAccountForm({ node, view, save }: { node: KubeNode; view: KubeAccessView; save: Save }) {
  const related = view.related ?? []
  const users = related.filter((r) => r.label === "Runs as it")
  const grants = related.filter((r) => r.label !== "Runs as it")
  return (
    <div className="flex flex-col gap-3">
      <TokenSwitch value={view.automountToken} editable={view.editable} onChange={(v) => void save({ automountToken: v })} hint="Pods running as it get an API token unless they say otherwise." />
      <Field label="Who runs as it">
        <RelatedList related={users} empty="No workload of this workflow runs as it." testId="k8s-rbac-sa-users" />
      </Field>
      <Field label="What it can do">
        <RelatedList related={grants} empty="Nothing: it can't call the Kubernetes API." testId="k8s-rbac-sa-grants" />
      </Field>
      <OwnRoleButton node={node} />
    </div>
  )
}

function TokenSwitch({ value, editable, onChange, hint }: { value?: boolean; editable: boolean; onChange: (v: boolean) => void; hint: string }) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex flex-col gap-0.5">
        <label htmlFor="k8s-rbac-automount" className="text-xs font-medium">
          Mount API token
        </label>
        <p className="text-[11px] text-muted-foreground">
          {value === undefined ? `${hint} Not set: Kubernetes mounts it.` : hint}
        </p>
      </div>
      <Switch id="k8s-rbac-automount" data-testid="k8s-rbac-automount" checked={value !== false} disabled={!editable} onCheckedChange={(v) => onChange(v)} />
    </div>
  )
}

function OwnRoleButton({ node }: { node: KubeNode }) {
  return (
    <Button type="button" variant="outline" size="xs" className="self-start" data-testid="k8s-rbac-own-role" onClick={() => void giveOwnRole(node)}>
      <ShieldPlusIcon />
      Give it its own Role
    </Button>
  )
}

const NEW_SA = "__new__"

/** A workload's "Identity": the ServiceAccount its pods run as, its API token and what it can do. */
export function WorkloadIdentity({ node }: { node: KubeNode }) {
  const { query, save } = useKubeAccess(node)
  const graph = useWorkflowStore((s) => s.workflow!.kubernetes?.graph)
  const [creating, setCreating] = useState(false)
  const view = query.data
  const accounts = serviceAccountsIn(graph, node.namespace)
  return (
    <Section title="Identity" icon={<ShieldIcon className="size-3.5 text-amber-500" />}>
      <div className="flex flex-col gap-3" data-testid="k8s-identity">
        {query.isPending && !view ? (
          <Loading />
        ) : query.isError && !view ? (
          <ReadOnlySource title="Can't show its identity" reason={errorMessage(query.error)} file={node.file} line={node.line} />
        ) : view ? (
          <>
            <AccessWarnings warnings={view.warnings} />
            {!view.editable && <ReadOnlySource title="Read-only here" reason={view.reason} file={node.file} line={node.line} />}
            <Field label="Runs as" hint="The ServiceAccount whose permissions its pods get.">
              {view.editable ? (
                creating ? (
                  <NewServiceAccount
                    initial={node.name}
                    onCancel={() => setCreating(false)}
                    onCreate={async (name) => {
                      if (await save({ serviceAccount: name })) setCreating(false)
                    }}
                  />
                ) : (
                  <Select value={view.serviceAccount ?? "default"} onValueChange={(v) => (v === NEW_SA ? setCreating(true) : void save({ serviceAccount: v }))}>
                    <SelectTrigger size="sm" className="h-8 w-full font-mono text-xs" aria-label="ServiceAccount" data-testid="k8s-identity-sa">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="default">default (the namespace's)</SelectItem>
                      {[...new Set([...accounts, ...(view.serviceAccount && view.serviceAccount !== "default" ? [view.serviceAccount] : [])])]
                        .filter((n) => n !== "default")
                        .map((n) => (
                          <SelectItem key={n} value={n}>
                            {n}
                          </SelectItem>
                        ))}
                      <SelectItem value={NEW_SA} data-testid="k8s-identity-new-sa">
                        New ServiceAccount…
                      </SelectItem>
                    </SelectContent>
                  </Select>
                )
              ) : (
                <span className="font-mono text-xs">{view.serviceAccount}</span>
              )}
            </Field>
            <TokenSwitch value={view.automountToken} editable={view.editable} onChange={(v) => void save({ automountToken: v })} hint="Turn it off unless its containers call the Kubernetes API." />
            <Field label="What it can do">
              <RelatedList related={(view.related ?? []).filter((r) => !r.address.startsWith("ServiceAccount "))} empty="Nothing: it can't call the Kubernetes API." testId="k8s-identity-grants" />
            </Field>
            <p className="text-[11px] text-muted-foreground">Drag this card onto a ConfigMap, Secret or other object to grant Read, Write or Manage on it only.</p>
            {view.editable && <OwnRoleButton node={node} />}
          </>
        ) : null}
      </div>
    </Section>
  )
}

function NewServiceAccount({ initial, onCreate, onCancel }: { initial: string; onCreate: (name: string) => void; onCancel: () => void }) {
  const [name, setName] = useState(initial)
  const problem = serviceAccountNameProblem(name)
  return (
    <form
      className="flex flex-col gap-1"
      onSubmit={(e) => {
        e.preventDefault()
        if (!problem) onCreate(name)
      }}
    >
      <div className="flex items-center gap-1.5">
        <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} aria-label="New ServiceAccount name" aria-invalid={!!problem || undefined} className="h-8 font-mono text-xs" data-testid="k8s-identity-new-sa-name" />
        <Button type="submit" size="xs" disabled={!!problem} data-testid="k8s-identity-new-sa-create">
          Create
        </Button>
        <Button type="button" variant="ghost" size="xs" onClick={onCancel}>
          Cancel
        </Button>
      </div>
      {problem && <p className="text-[11px] text-destructive">{problem}</p>}
    </form>
  )
}
