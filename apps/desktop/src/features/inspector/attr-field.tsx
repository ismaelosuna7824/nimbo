import { BracesIcon, LockIcon, Undo2Icon, XIcon } from "lucide-react"
import { memo, useState } from "react"
import { ResourceIcon } from "@/components/resource-icon"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { SchemaAttribute, Value } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { nodeLabel, schemaKey } from "../canvas/catalog"
import {
  appendToList,
  ctyKind,
  ctyLabel,
  exprOf,
  listItems,
  listValue,
  mapEntries,
  referencedNodes,
  removeListItem,
  isJsonAttr,
  jsonFieldValue,
  resolveReference,
  stringLiteral,
} from "./body"
import { useInspector } from "./context"
import { MapField } from "./map-field"
import { ReferencePicker } from "./reference-picker"
import { refFit } from "./references"
import { ExpressionInput, TagInput } from "./widgets"

const UNSET = "__unset__"

/** First sentence-ish of a provider description, for inline help. */
function shortHelp(description?: string) {
  if (!description) return ""
  const clean = description.replace(/\s+/g, " ").trim()
  const cut = clean.search(/\.\s/)
  return cut > 0 && cut < 220 ? clean.slice(0, cut + 1) : clean
}

/** Memoized: typing in one field of a form leaves the others' props alone (useBlockEditors hands out stable setters). */
export const AttrField = memo(function AttrField({
  attr,
  value,
  onChange,
  path,
}: {
  attr: SchemaAttribute
  value: Value | undefined
  onChange: (v: Value | undefined) => void
  /** Diagnostic attribute path of this field (defaults to attr.name). */
  path?: string
}) {
  const { mode, nodes, schemas, select, diagnostics } = useInspector()
  const [forceExpr, setForceExpr] = useState(false)
  const [rawRef, setRawRef] = useState(false)
  const kind = ctyKind(attr.type)
  const fieldPath = path ?? attr.name
  const problems = diagnostics.filter(
    (d) => d.attribute === fieldPath || d.attribute?.startsWith(`${fieldPath}.`) || d.attribute?.startsWith(`${fieldPath}[`),
  )
  const constant = value === undefined || value.literal !== undefined
  const exprMode = kind === "complex" || forceExpr || !constant
  const ref = exprMode && !rawRef ? resolveReference(value?.expr, nodes) : null
  // A list built with Link to… (references among strings) shows as chips.
  const items = kind === "string-list" && !constant && !forceExpr && !rawRef ? listItems(value) : null
  // Maps edit entry by entry; values holding expressions are kept as they are.
  const entries = kind === "string-map" && !forceExpr && !rawRef && !ref ? mapEntries(value) : null
  const json = kind === "string" && isJsonAttr(attr.name, attr.description)
  const pro = mode === "pro"
  const help = shortHelp(attr.description)
  const invalid = problems.some((p) => p.severity === "error")

  const setLiteral = (lit: unknown) => onChange(lit === undefined ? undefined : { literal: lit })
  const setExpr = (expr: string) => onChange(expr.trim() ? { expr: expr.trim() } : undefined)

  return (
    <div className="group/field flex flex-col gap-1.5" data-invalid={invalid || undefined}>
      <div className="flex min-h-5 items-center gap-1.5">
        <Tooltip delayDuration={500}>
          <TooltipTrigger asChild>
            <label className="truncate font-mono text-xs font-medium">
              {attr.name}
              {attr.required && <span className="text-destructive"> *</span>}
            </label>
          </TooltipTrigger>
          {attr.description && <TooltipContent className="max-w-80">{attr.description}</TooltipContent>}
        </Tooltip>
        {attr.sensitive && <LockIcon className="size-3 text-muted-foreground" aria-label="Sensitive" />}
        {pro && <span className="font-mono text-[10px] text-muted-foreground/70">{ctyLabel(attr.type)}</span>}
        <div className="ml-auto flex items-center gap-0.5">
          {pro && kind !== "complex" && (
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  type="button"
                  disabled={exprMode && !constant}
                  onClick={() => {
                    setForceExpr(!exprMode)
                    setRawRef(false)
                  }}
                  className={cn(
                    "flex h-5 items-center gap-0.5 rounded px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40",
                    exprMode && "bg-muted text-foreground",
                  )}
                >
                  <BracesIcon className="size-3" />
                  {exprMode ? "expr" : "value"}
                </button>
              </TooltipTrigger>
              <TooltipContent>
                {exprMode && !constant ? "This expression isn't a constant value" : exprMode ? "Switch to a typed value" : "Write an HCL expression"}
              </TooltipContent>
            </Tooltip>
          )}
          {kind !== "bool" && (
            <ReferencePicker
              className="opacity-0 group-hover/field:opacity-100 focus-visible:opacity-100 data-[state=open]:opacity-100"
              target={attr.type}
              base64={attr.name === "user_data" && kind === "string"}
              onPick={(expr, type) => {
                setRawRef(false)
                if (refFit(attr.type, type) === "append") onChange(appendToList(value, expr))
                else setExpr(expr)
              }}
            />
          )}
          {value !== undefined && !attr.required && (
            <button
              type="button"
              onClick={() => {
                onChange(undefined)
                setForceExpr(false)
              }}
              className="flex size-5 items-center justify-center rounded text-muted-foreground opacity-0 group-hover/field:opacity-100 hover:bg-muted hover:text-foreground"
              aria-label={`Clear ${attr.name}`}
            >
              <Undo2Icon className="size-3" />
            </button>
          )}
        </div>
      </div>

      {ref ? (
        <div className="flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => select(ref.node.id)}
            className="inline-flex h-7 min-w-0 items-center gap-1.5 rounded-md border border-primary/30 bg-primary/8 px-2 text-xs hover:bg-primary/15"
          >
            <ResourceIcon name={schemas.get(schemaKey(ref.node.kind, ref.node.type))?.icon} className="size-3.5 shrink-0" />
            <span className="truncate font-medium">{nodeLabel(ref.node, schemas.get(schemaKey(ref.node.kind, ref.node.type)))}</span>
            {ref.attr && <span className="shrink-0 font-mono text-muted-foreground">.{ref.attr}</span>}
          </button>
          {pro && (
            <button
              type="button"
              onClick={() => setRawRef(true)}
              className="text-[11px] text-muted-foreground hover:text-foreground"
            >
              edit
            </button>
          )}
          <button
            type="button"
            onClick={() => onChange(undefined)}
            className="ml-auto flex size-6 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
            aria-label="Unlink"
          >
            <XIcon className="size-3.5" />
          </button>
        </div>
      ) : items ? (
        <ListChips
          items={items}
          invalid={invalid}
          onRemove={(i) => onChange(removeListItem(value, i))}
          onAdd={(text) => onChange(listValue([...items, JSON.stringify(text)]))}
          onEdit={pro ? () => setRawRef(true) : undefined}
        />
      ) : json ? (
        // A JSON document always gets a multi-line editor, set or not, in Guided too: clearing it must not leave a
        // one-line text box that can't take the document back (pasted JSON becomes jsonencode(…)).
        <>
          <ExpressionInput
            value={value?.expr ?? (typeof value?.literal === "string" ? value.literal : "")}
            onCommit={(text) => onChange(jsonFieldValue(text))}
            invalid={invalid}
            rows={6}
            placeholder={'Paste a JSON document, e.g. { "Version": "2012-10-17", "Statement": [ … ] }, or Link to… a policy document'}
          />
          <ReferencedChips expr={value?.expr} />
        </>
      ) : entries ? (
        <MapField entries={entries} onChange={onChange} />
      ) : exprMode ? (
        <>
          <ExpressionInput
            value={exprOf(value)}
            onCommit={(expr) => {
              // Emptied: stay an expression editor, so the user can write the next one instead of a plain text.
              if (!expr.trim()) setForceExpr(true)
              setExpr(expr)
            }}
            invalid={invalid}
            rows={kind === "complex" ? 3 : 1}
          />
          <ReferencedChips expr={value?.expr} />
        </>
      ) : (
        <LiteralWidget attr={attr} literal={value?.literal} onChange={setLiteral} invalid={invalid} />
      )}

      {help && mode === "guided" && <p className="line-clamp-2 text-[11px] leading-snug text-muted-foreground">{help}</p>}
      {problems.map((p, i) => (
        <p key={i} className={cn("text-[11px]", p.severity === "error" ? "text-destructive" : "text-amber-600 dark:text-amber-400")}>
          {p.message}
        </p>
      ))}
    </div>
  )
})

function ReferencedChips({ expr }: { expr?: string }) {
  const { nodes, schemas, select } = useInspector()
  const refs = referencedNodes(expr, nodes)
  if (!refs.length) return null
  return (
    <div className="flex flex-wrap items-center gap-1 text-[11px] text-muted-foreground">
      Uses
      {refs.map((n) => (
        <button
          key={n.id}
          type="button"
          onClick={() => select(n.id)}
          className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 hover:bg-muted hover:text-foreground"
        >
          <ResourceIcon name={schemas.get(schemaKey(n.kind, n.type))?.icon} className="size-3" />
          {nodeLabel(n, schemas.get(schemaKey(n.kind, n.type)))}
        </button>
      ))}
    </div>
  )
}

/** A list holding references (Link to…) and texts: one chip per element, removable, plus a text input. */
function ListChips({
  items,
  invalid,
  onRemove,
  onAdd,
  onEdit,
}: {
  items: string[]
  invalid: boolean
  onRemove: (index: number) => void
  onAdd: (text: string) => void
  onEdit?: () => void
}) {
  const { nodes, schemas, select } = useInspector()
  const [draft, setDraft] = useState("")
  const commit = () => {
    const text = draft.trim()
    if (text) onAdd(text)
    setDraft("")
  }
  return (
    <div
      className={cn("flex min-h-8 flex-wrap items-center gap-1 rounded-md border px-1.5 py-1", invalid && "border-destructive")}
      data-testid="list-chips"
    >
      {items.map((src, i) => {
        const text = stringLiteral(src)
        const ref = text === undefined ? resolveReference(src, nodes) : null
        const schema = ref ? schemas.get(schemaKey(ref.node.kind, ref.node.type)) : undefined
        return (
          <span
            key={`${i}-${src}`}
            className={cn(
              "inline-flex h-6 max-w-full min-w-0 items-center gap-1 rounded border px-1.5 text-xs",
              ref ? "border-primary/30 bg-primary/8" : "bg-muted/50",
            )}
          >
            {ref ? (
              <button type="button" onClick={() => select(ref.node.id)} className="inline-flex min-w-0 items-center gap-1 hover:underline">
                <ResourceIcon name={schema?.icon} className="size-3 shrink-0" />
                <span className="truncate font-medium">{nodeLabel(ref.node, schema)}</span>
                {ref.attr && <span className="shrink-0 font-mono text-muted-foreground">.{ref.attr}</span>}
              </button>
            ) : (
              <span className={cn("truncate", text === undefined && "font-mono")}>{text ?? src}</span>
            )}
            <button
              type="button"
              onClick={() => onRemove(i)}
              className="flex size-4 shrink-0 items-center justify-center rounded text-muted-foreground hover:bg-muted hover:text-foreground"
              aria-label={`Remove ${text ?? src}`}
            >
              <XIcon className="size-3" />
            </button>
          </span>
        )
      })}
      <input
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault()
            commit()
          }
        }}
        placeholder="Add…"
        className="h-6 min-w-16 flex-1 bg-transparent px-1 text-xs outline-none placeholder:text-muted-foreground"
      />
      {onEdit && (
        <button type="button" onClick={onEdit} className="h-6 px-1 text-[11px] text-muted-foreground hover:text-foreground">
          edit
        </button>
      )}
    </div>
  )
}

function LiteralWidget({
  attr,
  literal,
  onChange,
  invalid,
}: {
  attr: SchemaAttribute
  literal: unknown
  onChange: (lit: unknown) => void
  invalid: boolean
}) {
  const kind = ctyKind(attr.type)
  const placeholder = attr.default !== undefined ? `e.g. ${typeof attr.default === "string" ? attr.default : JSON.stringify(attr.default)}` : undefined

  switch (kind) {
    case "bool":
      return (
        <div className="flex h-7 items-center gap-2">
          <Switch checked={literal === true} onCheckedChange={(c) => onChange(c)} aria-label={attr.name} />
          <span className="text-xs text-muted-foreground">
            {literal === undefined ? "Not set (provider default)" : literal ? "Enabled" : "Disabled"}
          </span>
        </div>
      )
    case "number":
      return (
        <Input
          type="number"
          className="h-8"
          aria-invalid={invalid || undefined}
          value={typeof literal === "number" ? literal : ""}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
        />
      )
    case "string-list":
      return (
        <TagInput
          value={Array.isArray(literal) ? literal.map(String) : []}
          onChange={(list) => onChange(list.length ? list : undefined)}
          invalid={invalid}
        />
      )
    default:
      if (attr.enum?.length) {
        return (
          <Select
            value={typeof literal === "string" ? literal : UNSET}
            onValueChange={(v) => onChange(v === UNSET ? undefined : v)}
          >
            <SelectTrigger className="h-8 w-full" aria-invalid={invalid || undefined}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {!attr.required && <SelectItem value={UNSET}>Not set</SelectItem>}
              {attr.enum.map((o) => (
                <SelectItem key={o} value={o}>
                  {o}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        )
      }
      return (
        <Input
          type={attr.sensitive ? "password" : "text"}
          className="h-8"
          aria-invalid={invalid || undefined}
          value={typeof literal === "string" ? literal : literal === undefined ? "" : String(literal)}
          placeholder={placeholder}
          onChange={(e) => onChange(e.target.value === "" ? undefined : e.target.value)}
        />
      )
  }
}
