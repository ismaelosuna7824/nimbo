import { useQuery } from "@tanstack/react-query"
import { ArrowDownIcon, ArrowUpIcon, CheckIcon, ChevronsUpDownIcon, PlusIcon, Trash2Icon, UsersIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { AccessCatalog, OCIStatement, WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { ReferencePicker } from "../inspector/reference-picker"
import type { AccessEditorProps } from "./editors"
import {
  appendConditionValue,
  blankStatement,
  formatStatement,
  moveItem,
  namedNodes,
  namedValue,
  OCI_SUBJECT_TYPES,
  OCI_VERBS,
  sameStatement,
  splitSubjects,
  statementProblems,
  statementsChanged,
  takesNoNames,
} from "./oci-sentence"
import { AccessWarnings, ReadOnlySource } from "./parts"

const SUBJECT_LABELS: Record<string, string> = {
  group: "group",
  "dynamic-group": "dynamic group",
  "any-user": "any user",
  "any-group": "any group",
  service: "service",
}

const EMPTY: OCIStatement[] = []
const NONE = "__none__"

/**
 * OCI policy statements as sentences: "Allow [group ▾] to [verb ▾] [resource-type] in [compartment ▾] where …".
 * Statements Nimbo can't show this way (Endorse/Admit/Define, permission lists, expressions) are kept as written.
 */
export function OCIPolicyEditor({ node, view, save }: AccessEditorProps) {
  const pol = view.oci
  const file = useWorkflowStore((s) => node.file ?? s.workflow!.nodeFiles?.[node.id])
  const nodes = useWorkflowStore((s) => s.workflow!.nodes)
  const catalog = useQuery({ queryKey: ["access-catalog", "oci"], queryFn: () => api.accessCatalog("oci"), staleTime: Infinity })
  const saved = pol?.statements ?? EMPTY
  const [draft, setDraft] = useSyncedState(saved)
  const [saving, setSaving] = useState(false)

  if (!pol) return null
  if (!pol.editable) {
    return <ReadOnlySource title="Statements kept as code" source={pol.statements[0]?.source} reason={pol.reason} file={file} />
  }
  const dirty = statementsChanged(saved, draft)
  const blocked = draft.some((st) => statementProblems(st).length > 0)
  const update = (i: number, st: OCIStatement) => setDraft((d) => d.map((x, j) => (j === i ? st : x)))
  // Warnings point at the saved list: find each draft statement's saved position (unchanged ones only).
  const warningPath = (st: OCIStatement) => {
    const k = saved.findIndex((s) => s.source !== undefined && s.source === st.source && sameStatement(s, st))
    return k < 0 ? undefined : `statements/${k}`
  }
  const submit = async () => {
    setSaving(true)
    try {
      await save({ nodeId: node.id, oci: { statements: draft, editable: true } })
    } catch {
      // save() already told the user; the draft stays for another try.
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="flex flex-col gap-2" data-testid="oci-policy-editor">
      <p className="text-[11px] text-muted-foreground">
        Each statement is a sentence: who, can do what, on what, where. Verbs go from least to most access: inspect, read, use,
        manage.
      </p>
      {draft.length === 0 && (
        <p className="rounded-md border border-dashed p-2 text-xs text-muted-foreground">No statements: this policy grants nothing yet.</p>
      )}
      {draft.map((st, i) => {
        const path = warningPath(st)
        return (
          <div key={i} className="flex flex-col gap-2 rounded-md border bg-card/40 p-2" data-testid={`oci-statement-${i}`}>
            <div className="flex items-center gap-1">
              <span className="text-[11px] font-medium text-muted-foreground">Statement {i + 1}</span>
              <span className="flex-1" />
              <IconAction label="Move up" disabled={i === 0} onClick={() => setDraft((d) => moveItem(d, i, i - 1))} testId={`oci-up-${i}`}>
                <ArrowUpIcon />
              </IconAction>
              <IconAction
                label="Move down"
                disabled={i === draft.length - 1}
                onClick={() => setDraft((d) => moveItem(d, i, i + 1))}
                testId={`oci-down-${i}`}
              >
                <ArrowDownIcon />
              </IconAction>
              <IconAction
                label={st.editable ? "Remove" : "Read-only: delete it in code"}
                disabled={!st.editable}
                onClick={() => setDraft((d) => d.filter((_, j) => j !== i))}
                testId={`oci-remove-${i}`}
              >
                <Trash2Icon />
              </IconAction>
            </div>
            {st.editable ? (
              <SentenceBuilder index={i} st={st} nodes={nodes} catalog={catalog.data} onChange={(next) => update(i, next)} />
            ) : (
              <ReadOnlySource title="Kept as written" source={st.source} reason={st.reason} file={file} />
            )}
            {path && <AccessWarnings warnings={view.warnings} path={path} />}
          </div>
        )
      })}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button variant="outline" size="xs" onClick={() => setDraft((d) => [...d, blankStatement()])} data-testid="oci-add-statement">
          <PlusIcon />
          Add statement
        </Button>
        {dirty && (
          <>
            <span className="flex-1" />
            <Button variant="ghost" size="xs" onClick={() => setDraft(saved)} disabled={saving} data-testid="oci-discard">
              Discard
            </Button>
            <Button size="xs" onClick={() => void submit()} disabled={saving || blocked} data-testid="oci-save">
              <CheckIcon />
              Save statements
            </Button>
          </>
        )}
      </div>
      {catalog.data && (
        <p className="text-[10px] text-muted-foreground">
          Verbs and resource-types:{" "}
          <a className="underline underline-offset-2" href={catalog.data.source.url} target="_blank" rel="noreferrer">
            {catalog.data.source.title}
          </a>{" "}
          (retrieved {catalog.data.source.retrieved}).
        </p>
      )}
    </div>
  )
}

function IconAction({
  label,
  disabled,
  onClick,
  testId,
  children,
}: {
  label: string
  disabled?: boolean
  onClick: () => void
  testId: string
  children: React.ReactNode
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span>
          <Button variant="ghost" size="icon-xs" aria-label={label} disabled={disabled} onClick={onClick} data-testid={testId}>
            {children}
          </Button>
        </span>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  )
}

/** A keyword of the sentence ("Allow", "to", "in", "where"). */
function Word({ children }: { children: React.ReactNode }) {
  return <span className="text-xs font-medium text-muted-foreground">{children}</span>
}

function SentenceBuilder({
  index: i,
  st,
  nodes,
  catalog,
  onChange,
}: {
  index: number
  st: OCIStatement
  nodes: WorkflowNode[]
  catalog?: AccessCatalog
  onChange: (st: OCIStatement) => void
}) {
  const [wantWhere, setWantWhere] = useState(false)
  const showWhere = wantWhere || !!st.condition
  const problems = statementProblems(st)
  const named = namedNodes(nodes, st.subjectType ?? "group")
  const compartments = namedNodes(nodes, "compartment")
  const addSubject = (value: string) => {
    const cur = st.subjects ?? []
    if (!cur.includes(value)) onChange({ ...st, subjects: [...cur, value] })
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <Word>Allow</Word>
        <Select
          value={st.subjectType ?? "group"}
          onValueChange={(subjectType) => onChange({ ...st, subjectType, subjects: takesNoNames(subjectType) ? [] : st.subjects })}
        >
          <SelectTrigger size="sm" className="h-7 w-auto text-xs" aria-label="Who" data-testid={`oci-subject-type-${i}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {OCI_SUBJECT_TYPES.map((t) => (
              <SelectItem key={t} value={t}>
                {SUBJECT_LABELS[t]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {!takesNoNames(st.subjectType) && (
          <>
            <SubjectsInput
              index={i}
              subjects={st.subjects ?? []}
              placeholder={st.subjectType === "service" ? "objectstorage-us-ashburn-1" : "name, 'Domain'/'Name' or id ocid1…"}
              onChange={(subjects) => onChange({ ...st, subjects })}
            />
            {named.length > 0 && (
              <Select value={NONE} onValueChange={(v) => v !== NONE && addSubject(v)}>
                <SelectTrigger size="sm" className="h-7 w-auto gap-1 text-xs" aria-label={`Pick a ${SUBJECT_LABELS[st.subjectType ?? "group"]}`} data-testid={`oci-subject-pick-${i}`}>
                  <UsersIcon className="size-3" />
                  <span>{SUBJECT_LABELS[st.subjectType ?? "group"]} ▾</span>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE} disabled>
                    Adds its name to the sentence
                  </SelectItem>
                  {named.map((n) => (
                    <SelectItem key={n.nodeId} value={namedValue(n)}>
                      <span className="font-mono text-xs">{n.name ?? n.reference}</span>
                      <span className="ml-1.5 font-mono text-[10px] text-muted-foreground">{n.address}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <ReferencePicker onPick={(expr) => addSubject(`\${${expr}}`)} />
          </>
        )}
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <Word>to</Word>
        <VerbSelect index={i} verb={st.verb ?? "read"} catalog={catalog} onChange={(verb) => onChange({ ...st, verb })} />
        <ResourceTypePicker index={i} value={st.resourceType ?? ""} catalog={catalog} onChange={(resourceType) => onChange({ ...st, resourceType })} />
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        <Word>in</Word>
        <Select value={st.location ?? "compartment"} onValueChange={(location) => onChange({ ...st, location })}>
          <SelectTrigger size="sm" className="h-7 w-auto text-xs" aria-label="Where" data-testid={`oci-location-${i}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="compartment">compartment</SelectItem>
            <SelectItem value="tenancy">tenancy (every compartment)</SelectItem>
          </SelectContent>
        </Select>
        {st.location !== "tenancy" && (
          <>
            <Input
              aria-label="Compartment"
              className="h-7 w-40 min-w-0 flex-1 font-mono text-xs"
              placeholder="name, a:b path or id ocid1…"
              value={st.compartment ?? ""}
              onChange={(e) => onChange({ ...st, compartment: e.target.value })}
              data-testid={`oci-compartment-${i}`}
            />
            {compartments.length > 0 && (
              <Select value={NONE} onValueChange={(v) => v !== NONE && onChange({ ...st, compartment: v })}>
                <SelectTrigger size="sm" className="h-7 w-auto text-xs" aria-label="Pick a compartment" data-testid={`oci-compartment-pick-${i}`}>
                  <span>compartment ▾</span>
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={NONE} disabled>
                    Uses its name
                  </SelectItem>
                  {compartments.map((c) => (
                    <SelectItem key={c.nodeId} value={namedValue(c)}>
                      <span className="font-mono text-xs">{c.name ?? c.reference}</span>
                      <span className="ml-1.5 font-mono text-[10px] text-muted-foreground">{c.address}</span>
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            <ReferencePicker onPick={(expr) => onChange({ ...st, compartment: `\${${expr}}` })} />
          </>
        )}
        {!showWhere && (
          <Button variant="ghost" size="xs" className="h-6 text-[11px] text-muted-foreground" onClick={() => setWantWhere(true)} data-testid={`oci-add-where-${i}`}>
            <PlusIcon />
            where…
          </Button>
        )}
      </div>

      {showWhere && (
        <div className="flex items-center gap-1.5">
          <Word>where</Word>
          <Input
            aria-label="Condition"
            className="h-7 min-w-0 flex-1 font-mono text-xs"
            placeholder="target.bucket.name='invoices'"
            value={st.condition ?? ""}
            onChange={(e) => onChange({ ...st, condition: e.target.value })}
            data-testid={`oci-where-${i}`}
          />
          <ReferencePicker onPick={(expr) => onChange({ ...st, condition: appendConditionValue(st.condition, expr) })} />
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label="Remove the condition"
            onClick={() => {
              setWantWhere(false)
              onChange({ ...st, condition: undefined })
            }}
          >
            <XIcon />
          </Button>
        </div>
      )}

      <p className="rounded bg-muted/60 px-2 py-1 font-mono text-[11px] break-words" data-testid={`oci-preview-${i}`}>
        {formatStatement(st)}
      </p>
      {problems.length > 0 && (
        <ul className="flex flex-col gap-0.5 text-[11px] text-amber-700 dark:text-amber-400" data-testid={`oci-problems-${i}`}>
          {problems.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Comma-separated subjects, split when the field is left. */
function SubjectsInput({
  index,
  subjects,
  placeholder,
  onChange,
}: {
  index: number
  subjects: string[]
  placeholder: string
  onChange: (subjects: string[]) => void
}) {
  const joined = subjects.join(", ")
  const [text, setText] = useSyncedState(joined)
  const commit = () => {
    if (text !== joined) onChange(splitSubjects(text))
  }
  return (
    <Input
      aria-label="Names"
      className="h-7 w-40 min-w-0 flex-1 font-mono text-xs"
      placeholder={placeholder}
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit()
      }}
      data-testid={`oci-subjects-${index}`}
    />
  )
}

function VerbSelect({ index, verb, catalog, onChange }: { index: number; verb: string; catalog?: AccessCatalog; onChange: (verb: string) => void }) {
  const described = new Map((catalog?.verbs ?? []).map((v) => [v.name, v.description ?? ""]))
  return (
    <Select value={verb} onValueChange={onChange}>
      <SelectTrigger size="sm" className="h-7 w-auto text-xs" aria-label="Can do" data-testid={`oci-verb-${index}`}>
        <SelectValue />
      </SelectTrigger>
      <SelectContent className="max-w-80">
        {OCI_VERBS.map((v) => {
          const [who, what] = (described.get(v) ?? "").split(/:\s(.*)/s)
          return (
            <SelectItem key={v} value={v} textValue={v}>
              <span className="flex flex-col gap-0.5 py-0.5">
                <span className="font-mono text-xs font-medium">{v}</span>
                {who && (
                  <span className="line-clamp-2 text-[11px] whitespace-normal text-muted-foreground">
                    {who}
                    {what ? ` — ${what}` : ""}
                  </span>
                )}
              </span>
            </SelectItem>
          )
        })}
      </SelectContent>
    </Select>
  )
}

/** Resource-types of the pinned policy reference, grouped by service; any other name can be typed. */
function ResourceTypePicker({
  index,
  value,
  catalog,
  onChange,
}: {
  index: number
  value: string
  catalog?: AccessCatalog
  onChange: (value: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")
  const groups = new Map<string, { name: string; description?: string }[]>()
  for (const rt of catalog?.resourceTypes ?? []) {
    const g = rt.group ?? "Other"
    groups.set(g, [...(groups.get(g) ?? []), rt])
  }
  const typed = search.trim()
  const known = (catalog?.resourceTypes ?? []).some((rt) => rt.name === typed)
  const pick = (v: string) => {
    onChange(v)
    setOpen(false)
    setSearch("")
  }
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className={cn("h-7 min-w-36 justify-between font-mono text-xs", !value && "text-muted-foreground")}
          aria-label="On what (resource-type)"
          data-testid={`oci-resource-${index}`}
        >
          {value || "resource-type…"}
          <ChevronsUpDownIcon className="size-3 opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-80 p-0" align="start">
        <Command>
          <CommandInput placeholder="object-family, buckets, keys…" value={search} onValueChange={setSearch} />
          <CommandList className="max-h-80">
            <CommandEmpty>{catalog ? "No resource-type matches." : "Loading the policy reference…"}</CommandEmpty>
            {typed && !known && /^[a-z0-9-]+$/.test(typed) && (
              <CommandGroup heading="Not in the reference">
                <CommandItem value={`custom ${typed}`} onSelect={() => pick(typed)}>
                  Use <span className="font-mono">{typed}</span>
                </CommandItem>
              </CommandGroup>
            )}
            {[...groups].map(([group, types]) => (
              <CommandGroup key={group} heading={group}>
                {types.map((rt) => (
                  <CommandItem key={rt.name} value={`${rt.name} ${group}`} onSelect={() => pick(rt.name)}>
                    <CheckIcon className={cn("size-3", rt.name === value ? "opacity-100" : "opacity-0")} />
                    <span className="font-mono text-xs">{rt.name}</span>
                    {rt.description?.startsWith("Aggregate") && <span className="ml-auto text-[10px] text-muted-foreground">family</span>}
                  </CommandItem>
                ))}
              </CommandGroup>
            ))}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
