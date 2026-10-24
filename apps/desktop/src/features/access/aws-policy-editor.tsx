import { useQuery } from "@tanstack/react-query"
import { CheckIcon, CopyIcon, ExternalLinkIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { useState } from "react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useSyncedState } from "@/components/use-synced-state"
import { api } from "@/lib/api/client"
import type { AccessWarning, AWSPolicyDocument, AWSStatement, WorkflowNode } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import {
  blankStatement,
  canDuplicate,
  duplicateStatement,
  encodingLabel,
  sameStatement,
  statementPath,
  statementProblems,
  statementsChanged,
  statementSummary,
  wantsPrincipals,
  awsCatalogKey,
} from "./aws-policy"
import { ActionsEditor, PrincipalsEditor, ResourcesEditor } from "./aws-statement-parts"
import type { AccessEditorProps } from "./editors"
import { AccessWarnings, ReadOnlySource } from "./parts"

/**
 * AWS IAM policy documents of a node, one card each: statements as "Allow / Deny — who — can do — on what", with an
 * action picker from the AWS Service Authorization Reference and resources linked to other nodes. Documents and
 * statements Nimbo can't show this way are kept exactly as written.
 */
export function AWSPolicyEditor({ node, view, save, select }: AccessEditorProps) {
  const file = useWorkflowStore((s) => node.file ?? s.workflow!.nodeFiles?.[node.id])
  const catalog = useQuery({ queryKey: awsCatalogKey(), queryFn: () => api.accessCatalog("aws"), staleTime: Infinity })
  const docs = view.aws ?? []
  return (
    <div className="flex flex-col gap-3" data-testid="aws-policy-editor">
      {docs.map((doc) => (
        <DocumentCard
          key={`${doc.path}-${doc.label}`}
          node={node}
          doc={doc}
          file={file}
          warnings={view.warnings}
          save={(statements) => save({ nodeId: node.id, aws: { ...doc, statements } })}
          select={select}
        />
      ))}
      {catalog.data && (
        <p className="text-[10px] text-muted-foreground">
          Actions and access levels:{" "}
          <a className="underline underline-offset-2" href={catalog.data.source.url} target="_blank" rel="noreferrer">
            {catalog.data.source.title}
          </a>{" "}
          (retrieved {catalog.data.source.retrieved}).
        </p>
      )}
    </div>
  )
}

function DocumentCard({
  node,
  doc,
  file,
  warnings,
  save,
  select,
}: {
  node: WorkflowNode
  doc: AWSPolicyDocument
  file?: string
  warnings: AccessWarning[]
  save: (statements: AWSStatement[]) => Promise<void>
  select: (nodeId: string) => void
}) {
  const [draft, setDraft] = useSyncedState(doc.statements)
  const [saving, setSaving] = useState(false)
  const slug = doc.path || "statements"

  const header = (
    <div className="flex items-center gap-1.5">
      <span className="text-xs font-medium">{doc.label}</span>
      <Badge variant="outline" className="h-4 px-1.5 text-[10px] font-normal text-muted-foreground">
        {encodingLabel(doc.encoding)}
      </Badge>
    </div>
  )

  if (doc.encoding === "reference") {
    return (
      <section className="flex flex-col gap-2" data-testid={`aws-document-${slug}`}>
        {header}
        <p className="text-[11px] text-muted-foreground">{doc.reason}</p>
        <ul className="flex flex-col gap-1">
          {doc.statements.map((st, i) => (
            <li key={i} className="rounded border bg-muted/30 px-2 py-1 font-mono text-[11px]">
              {statementSummary(st)}
            </li>
          ))}
        </ul>
        {doc.linked && (
          <Button variant="outline" size="xs" className="self-start" onClick={() => select(doc.linked!)} data-testid="aws-open-linked">
            <ExternalLinkIcon />
            Edit the policy document
          </Button>
        )}
      </section>
    )
  }
  if (!doc.editable) {
    return (
      <section className="flex flex-col gap-2" data-testid={`aws-document-${slug}`}>
        {header}
        <ReadOnlySource title="Kept as code" source={doc.source} reason={doc.reason} file={file} />
      </section>
    )
  }

  const dirty = statementsChanged(doc.statements, draft)
  const blocked = draft.some((st) => statementProblems(doc, node.type, st).length > 0)
  const update = (i: number, st: AWSStatement) => setDraft((d) => d.map((x, j) => (j === i ? st : x)))
  // Warnings point at the saved list: a draft statement shows those of its unchanged saved self.
  const warningPath = (st: AWSStatement) => {
    const k = doc.statements.findIndex((s) => s.source !== undefined && s.source === st.source && sameStatement(s, st))
    return k < 0 ? undefined : statementPath(doc.path, k)
  }
  const submit = async () => {
    setSaving(true)
    try {
      await save(draft)
    } catch {
      // save() already told the user; the draft stays for another try.
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="flex flex-col gap-2" data-testid={`aws-document-${slug}`}>
      {header}
      {draft.length === 0 && (
        <p className="rounded-md border border-dashed p-2 text-xs text-muted-foreground">No statements: this policy allows nothing yet.</p>
      )}
      {draft.map((st, i) => (
        <StatementCard
          key={i}
          index={i}
          doc={doc}
          nodeType={node.type}
          st={st}
          file={file}
          warnings={warnings}
          warningPath={warningPath(st)}
          onChange={(next) => update(i, next)}
          onDuplicate={() => setDraft((d) => [...d.slice(0, i + 1), duplicateStatement(st), ...d.slice(i + 1)])}
          onDelete={() => setDraft((d) => d.filter((_, j) => j !== i))}
        />
      ))}
      <div className="flex flex-wrap items-center gap-1.5">
        <Button
          variant="outline"
          size="xs"
          onClick={() => setDraft((d) => [...d, { ...blankStatement(), principals: wantsPrincipals(doc, node.type) ? [] : undefined }])}
          data-testid="aws-add-statement"
        >
          <PlusIcon />
          Add statement
        </Button>
        {dirty && (
          <>
            <span className="flex-1" />
            <Button variant="ghost" size="xs" onClick={() => setDraft(doc.statements)} disabled={saving} data-testid="aws-discard">
              Discard
            </Button>
            <Button size="xs" onClick={() => void submit()} disabled={saving || blocked} data-testid="aws-save">
              <CheckIcon />
              Save {doc.label.toLowerCase()}
            </Button>
          </>
        )}
      </div>
    </section>
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

/** A row of a statement: a beginner word ("Who", "Can do", "On what") and its editor. */
function Row({ word, children }: { word: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[3.75rem_1fr] items-start gap-1.5">
      <span className="pt-1 text-[11px] font-medium text-muted-foreground">{word}</span>
      <div className="min-w-0">{children}</div>
    </div>
  )
}

function StatementCard({
  index: i,
  doc,
  nodeType,
  st,
  file,
  warnings,
  warningPath,
  onChange,
  onDuplicate,
  onDelete,
}: {
  index: number
  doc: AWSPolicyDocument
  nodeType: string
  st: AWSStatement
  file?: string
  warnings: AccessWarning[]
  warningPath?: string
  onChange: (st: AWSStatement) => void
  onDuplicate: () => void
  onDelete: () => void
}) {
  const problems = statementProblems(doc, nodeType, st)
  const showWho = wantsPrincipals(doc, nodeType) || (st.principals?.length ?? 0) > 0
  const showOn = doc.path !== "assume_role_policy" || st.resources.length > 0
  const deny = st.effect === "Deny"
  return (
    <div
      className={cn(
        "flex flex-col gap-2 rounded-md border bg-card/40 p-2",
        deny && "border-rose-500/30 bg-rose-500/[0.03]",
      )}
      data-testid={`aws-statement-${i}`}
    >
      <div className="flex items-center gap-1">
        <span className="shrink-0 text-[11px] font-medium text-muted-foreground">Statement {i + 1}</span>
        {st.editable ? (
          <Input
            aria-label="Statement name (Sid)"
            className="h-6 w-32 min-w-0 font-mono text-[11px]"
            placeholder="Name (optional)"
            value={st.sid ?? ""}
            onChange={(e) => onChange({ ...st, sid: e.target.value || undefined })}
            data-testid={`aws-statement-sid-${i}`}
          />
        ) : (
          st.sid && <span className="truncate font-mono text-[11px]">{st.sid}</span>
        )}
        <span className="flex-1" />
        {st.editable && (
          <ToggleGroup
            type="single"
            size="sm"
            variant="outline"
            spacing={0}
            value={deny ? "Deny" : "Allow"}
            onValueChange={(v) => v && onChange({ ...st, effect: v })}
            aria-label="Allow or deny"
            data-testid={`aws-statement-effect-${i}`}
          >
            <ToggleGroupItem value="Allow" className="h-6 px-2 text-[11px] data-[state=on]:text-emerald-700 dark:data-[state=on]:text-emerald-300">
              Allow
            </ToggleGroupItem>
            <ToggleGroupItem value="Deny" className="h-6 px-2 text-[11px] data-[state=on]:text-rose-700 dark:data-[state=on]:text-rose-300">
              Deny
            </ToggleGroupItem>
          </ToggleGroup>
        )}
        <IconAction
          label={canDuplicate(doc, st) ? "Duplicate" : "Conditions can only be copied in code"}
          disabled={!canDuplicate(doc, st)}
          onClick={onDuplicate}
          testId={`aws-statement-duplicate-${i}`}
        >
          <CopyIcon />
        </IconAction>
        <IconAction
          label={st.editable ? "Delete statement" : "Read-only: delete it in code"}
          disabled={!st.editable}
          onClick={onDelete}
          testId={`aws-statement-delete-${i}`}
        >
          <Trash2Icon />
        </IconAction>
      </div>
      {st.editable ? (
        <>
          {showWho && (
            <Row word="Who">
              <PrincipalsEditor index={i} principals={st.principals ?? []} onChange={(principals) => onChange({ ...st, principals })} />
            </Row>
          )}
          <Row word={deny ? "Can't do" : "Can do"}>
            <ActionsEditor index={i} actions={st.actions} onChange={(actions) => onChange({ ...st, actions })} />
          </Row>
          {showOn && (
            <Row word="On what">
              <ResourcesEditor index={i} resources={st.resources} onChange={(resources) => onChange({ ...st, resources })} />
            </Row>
          )}
          {(st.extra?.length ?? 0) > 0 && (
            <Row word="Also">
              <div className="flex flex-col gap-1" data-testid={`aws-statement-extra-${i}`}>
                {st.extra!.map((x) => (
                  <div key={x.key} className="rounded border border-dashed px-1.5 py-1">
                    <div className="text-[10px] text-muted-foreground">{x.key} · kept as code</div>
                    <pre className="max-h-32 overflow-auto font-mono text-[11px] leading-snug whitespace-pre-wrap">{x.source}</pre>
                  </div>
                ))}
              </div>
            </Row>
          )}
          {problems.length > 0 && (
            <ul className="flex flex-col gap-0.5 text-[11px] text-rose-700 dark:text-rose-300">
              {problems.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          )}
        </>
      ) : (
        <ReadOnlySource title="Kept as code" source={st.source} reason={st.reason} file={file} />
      )}
      {warningPath && <AccessWarnings warnings={warnings} path={warningPath} />}
    </div>
  )
}
