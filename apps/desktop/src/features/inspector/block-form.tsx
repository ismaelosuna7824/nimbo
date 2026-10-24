import { ChevronDownIcon, PlusIcon, Trash2Icon } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import type { Block, Body, SchemaAttribute, SchemaBlock, SchemaNestedBlock } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { AttrField } from "./attr-field"
import { blocksOf, emptyBody, getAttr, setAttr, setBlocks } from "./body"
import { objectListFields } from "./object-list"
import { ObjectListField } from "./object-list-field"
import { useInspector } from "./context"

/** Provider bookkeeping of a resource block (not of its nested blocks, e.g. network_details.id). */
const NOISE = new Set(["id", "tags_all"])

/** A value, or blocks written for an attribute-as-blocks (`ingress { … }` of a list(object) attribute). */
function isSet(a: SchemaAttribute, body: Body) {
  return getAttr(body, a.name) !== undefined || (objectListFields(a.type) !== null && blocksOf(body, a.name).length > 0)
}

/** Attributes a user can set: hides computed-only and provider bookkeeping attributes unless they already have a value. */
function editable(attrs: SchemaAttribute[], body: Body, depth: number) {
  return attrs.filter((a) => {
    if (isSet(a, body)) return true
    if (a.computed && !a.optional && !a.required) return false
    if (depth === 0 && NOISE.has(a.name) && !a.required) return false
    return !a.deprecated
  })
}

function rank(a: SchemaAttribute) {
  return a.required ? 0 : a.basic ? 1 : 2
}

function matches(name: string, filter: string) {
  return !filter || name.toLowerCase().includes(filter.toLowerCase())
}

export function BlockForm({
  schema,
  body,
  onChange,
  pathPrefix = "",
  filter = "",
  depth = 0,
}: {
  schema: SchemaBlock
  body: Body
  onChange: (b: Body) => void
  pathPrefix?: string
  filter?: string
  depth?: number
}) {
  const { mode } = useInspector()
  const [showAll, setShowAll] = useState(false)
  const attrs = editable(schema.attributes ?? [], body, depth)
    .filter((a) => matches(a.name, filter))
    .sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
  const nested = (schema.blocks ?? []).filter((b) => matches(b.name, filter))
  const path = (name: string) => (pathPrefix ? `${pathPrefix}.${name}` : name)

  const field = (a: SchemaAttribute) =>
    objectListFields(a.type) ? (
      <ObjectListField key={a.name} attr={a} path={path(a.name)} body={body} onChange={onChange} />
    ) : (
      <AttrField
        key={a.name}
        attr={a}
        path={path(a.name)}
        value={getAttr(body, a.name)}
        onChange={(v) => onChange(setAttr(body, a.name, v))}
      />
    )
  const blockEditor = (nb: SchemaNestedBlock) => (
    <NestedBlocks
      key={nb.name}
      nb={nb}
      items={blocksOf(body, nb.name)}
      onChange={(items) => onChange(setBlocks(body, nb.name, items))}
      pathPrefix={path(nb.name)}
      depth={depth}
    />
  )

  if (mode === "pro") {
    const required = attrs.filter((a) => a.required)
    const optional = attrs.filter((a) => !a.required)
    return (
      <div className="flex flex-col gap-4">
        {required.length > 0 && (
          <FieldSection title="Required" depth={depth}>
            {required.map(field)}
          </FieldSection>
        )}
        {optional.length > 0 && (
          <FieldSection
            title="Optional"
            depth={depth}
            collapsible
            defaultOpen={depth > 0 || required.length === 0}
            forceOpen={!!filter}
          >
            {optional.map(field)}
          </FieldSection>
        )}
        {nested.length > 0 && (
          <FieldSection title="Blocks" depth={depth}>
            {nested.map(blockEditor)}
          </FieldSection>
        )}
      </div>
    )
  }

  const isPrimary = (a: SchemaAttribute) => a.required || a.basic || isSet(a, body)
  const primary = attrs.filter(isPrimary)
  const secondary = attrs.filter((a) => !isPrimary(a))
  const primaryBlocks = nested.filter((b) => (b.minItems ?? 0) > 0 || blocksOf(body, b.name).length > 0)
  const secondaryBlocks = nested.filter((b) => !primaryBlocks.includes(b))
  const more = secondary.length + secondaryBlocks.length

  return (
    <div className="flex flex-col gap-4">
      {primary.map(field)}
      {primaryBlocks.map(blockEditor)}
      {primary.length === 0 && primaryBlocks.length === 0 && depth === 0 && (
        <p className="text-xs text-muted-foreground">Nothing is required here — the defaults work. Open “More options” to customize.</p>
      )}
      {more > 0 && (
        <>
          <Button
            variant="ghost"
            size="sm"
            className="self-start text-muted-foreground"
            onClick={() => setShowAll(!showAll)}
          >
            <ChevronDownIcon className={cn("transition-transform", showAll && "rotate-180")} />
            {showAll ? "Fewer options" : `More options (${more})`}
          </Button>
          {showAll && (
            <div className="flex flex-col gap-4 border-l-2 pl-3">
              {secondary.map(field)}
              {secondaryBlocks.map(blockEditor)}
            </div>
          )}
        </>
      )}
    </div>
  )
}

function FieldSection({
  title,
  children,
  depth,
  collapsible,
  defaultOpen = true,
  forceOpen = false,
}: {
  title: string
  children: React.ReactNode
  depth: number
  collapsible?: boolean
  defaultOpen?: boolean
  /** Keeps the section expanded regardless of the user's toggle (e.g. while filtering). */
  forceOpen?: boolean
}) {
  const [userOpen, setOpen] = useState(defaultOpen)
  const open = userOpen || forceOpen
  const heading = (
    <span className="flex items-center gap-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
      {collapsible && <ChevronDownIcon className={cn("size-3.5 transition-transform", !open && "-rotate-90")} />}
      {title}
    </span>
  )
  if (!collapsible)
    return (
      <section className={cn("flex flex-col gap-3", depth > 0 && "gap-2.5")}>
        {heading}
        {children}
      </section>
    )
  return (
    <Collapsible open={open} onOpenChange={setOpen} className="flex flex-col gap-3">
      <CollapsibleTrigger className="self-start">{heading}</CollapsibleTrigger>
      <CollapsibleContent className="flex flex-col gap-3">{children}</CollapsibleContent>
    </Collapsible>
  )
}

function NestedBlocks({
  nb,
  items,
  onChange,
  pathPrefix,
  depth,
}: {
  nb: SchemaNestedBlock
  items: Block[]
  onChange: (items: Block[]) => void
  pathPrefix: string
  depth: number
}) {
  const max = nb.nesting === "single" || nb.nesting === "group" ? 1 : nb.maxItems || Infinity
  const min = nb.minItems ?? 0
  const add = () =>
    onChange([
      ...items,
      { type: nb.name, labels: nb.nesting === "map" ? [`key${items.length + 1}`] : undefined, body: emptyBody() },
    ])
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-dashed p-2.5">
      <div className="flex items-center gap-2">
        <span className="min-w-0 truncate font-mono text-xs font-medium">{nb.name}</span>
        {min > 0 && <span className="text-[10px] text-destructive">required</span>}
        <span className="text-[10px] text-muted-foreground">
          {nb.nesting === "single" || nb.nesting === "group" ? "block" : `${items.length}${max !== Infinity ? ` / ${max}` : ""}`}
        </span>
        <Button variant="ghost" size="xs" className="ml-auto" disabled={items.length >= max} onClick={add}>
          <PlusIcon />
          Add
        </Button>
      </div>
      {items.map((item, i) => (
        <div key={i} className="flex flex-col gap-3 rounded-md border bg-muted/30 p-2.5">
          <div className="flex items-center gap-2">
            {nb.nesting === "map" ? (
              <Input
                className="h-6 w-40 font-mono text-xs"
                value={item.labels?.[0] ?? ""}
                onChange={(e) => onChange(items.map((b, j) => (j === i ? { ...b, labels: [e.target.value] } : b)))}
                aria-label="Key"
              />
            ) : (
              <span className="text-[11px] text-muted-foreground">
                {nb.name} {max > 1 ? `#${i + 1}` : ""}
              </span>
            )}
            <Button
              variant="ghost"
              size="icon-xs"
              className="ml-auto"
              disabled={items.length <= min}
              onClick={() => onChange(items.filter((_, j) => j !== i))}
              aria-label={`Remove ${nb.name}`}
            >
              <Trash2Icon />
            </Button>
          </div>
          <BlockForm
            schema={nb.block}
            body={item.body}
            onChange={(b) => onChange(items.map((x, j) => (j === i ? { ...x, body: b } : x)))}
            pathPrefix={`${pathPrefix}[${i}]`}
            depth={depth + 1}
          />
        </div>
      ))}
    </div>
  )
}
