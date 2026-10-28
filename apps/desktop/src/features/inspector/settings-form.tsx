import { ChevronDownIcon, PlusIcon, SearchIcon, Trash2Icon } from "lucide-react"
import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import type { Block, Body, SchemaAttribute, SchemaBlock, SchemaNestedBlock, Value } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { AttrField } from "./attr-field"
import { blocksOf, emptyBody, getAttr, setAttr, setBlocks } from "./body"
import { useInspector } from "./context"
import { objectListFields } from "./object-list"
import { ObjectListField } from "./object-list-field"
import { type SettingsView, useSectionOpen, useSectionState } from "./section-state"
import { classifySetting, type SettingsGroupId } from "./settings-groups"
import { layoutSettings, MANY_SETTINGS, settingName, type SettingItem, type SettingsGroupLayout } from "./settings-layout"

/**
 * A resource's or data source's top-level settings: Required first, then the other attributes and nested blocks
 * grouped by what they configure (settings-groups.ts), set ones first in each group. Guided shows the essentials and
 * keeps the rest behind "More options"; big resources switch between every setting and only the configured ones.
 */
export function SettingsForm({ schema, body, onChange }: { schema: SchemaBlock; body: Body; onChange: (b: Body) => void }) {
  const { mode } = useInspector()
  const pro = mode === "pro"
  const [filter, setFilter] = useState("")
  const [showMore, setShowMore] = useState(false)
  // Settings picked from "Add setting…": shown in the Configured view until the node is left, even while still unset.
  const [revealed, setRevealed] = useState<ReadonlySet<string>>(() => new Set())
  const focusNext = useRef<string | null>(null)
  const root = useRef<HTMLDivElement>(null)
  const storedView = useSectionState((s) => s.view)
  const setView = useSectionState((s) => s.setView)
  const { field, blockEditor } = useBlockEditors({ schema, body, onChange, pathPrefix: "", depth: 0 })
  // Classified once per schema, not on every keystroke.
  const groupOf = useMemo(() => {
    const groups = new Map<string, SettingsGroupId>([
      ...(schema.attributes ?? []).map((a) => [a.name, classifySetting(a.name, a.description)] as const),
      ...(schema.blocks ?? []).map((b) => [b.name, classifySetting(b.name)] as const),
    ])
    return (item: SettingItem) => groups.get(settingName(item)) ?? "advanced"
  }, [schema])

  const items: SettingItem[] = [
    ...editable(schema.attributes ?? [], body, 0).map((attr): SettingItem => ({ kind: "attr", attr, set: isSet(attr, body) })),
    ...(schema.blocks ?? []).map((block): SettingItem => ({ kind: "block", block, set: blocksOf(body, block.name).length > 0 })),
  ]
  const many = items.length > MANY_SETTINGS
  const view = many ? storedView : "all"
  const filtering = filter.trim() !== ""
  const layout = layoutSettings(items, { pro, view, filter, revealed, groupOf })
  const moreCount = layout.more.reduce((n, g) => n + g.items.length, 0)

  useEffect(() => {
    const name = focusNext.current
    if (!name) return
    focusNext.current = null
    const el = root.current?.querySelector(`[data-setting="${CSS.escape(name)}"]`)
    // The value editor, not the field's header buttons (expr, Link to…).
    const target = el?.querySelector<HTMLElement>("input, textarea") ?? el?.querySelector<HTMLElement>("[role=switch], [role=combobox], button")
    target?.scrollIntoView({ block: "center" })
    target?.focus({ preventScroll: true })
  }, [revealed])

  const reveal = (name: string) => {
    focusNext.current = name
    setRevealed(new Set([...revealed, name]))
  }
  const render = (i: SettingItem) => (
    <div key={settingName(i)} data-setting={settingName(i)} className="contents">
      {i.kind === "attr" ? field(i.attr) : blockEditor(i.block)}
    </div>
  )
  const groupList = (groups: SettingsGroupLayout[], defaultOpen: (g: SettingsGroupLayout) => boolean) =>
    groups.map((g) => (
      <SettingsGroup key={g.id} group={g} defaultOpen={defaultOpen(g)} forceOpen={filtering || g.items.some((i) => revealed.has(settingName(i)))}>
        {g.items.map(render)}
      </SettingsGroup>
    ))
  const empty = layout.required.length === 0 && layout.groups.length === 0

  return (
    <div ref={root} className="flex flex-col gap-4">
      {(pro || many) && (
        <div className="flex items-center gap-2">
          {pro && (
            <div className="relative min-w-0 flex-1">
              <SearchIcon className="absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter attributes…" className="h-7 pl-7 text-xs" />
            </div>
          )}
          {many && (
            <ToggleGroup
              type="single"
              size="sm"
              variant="outline"
              className="ml-auto"
              value={view}
              aria-label="Settings to show"
              onValueChange={(v) => v && setView(v as SettingsView)}
              data-testid="settings-view"
            >
              <ToggleGroupItem value="configured" className="h-6 px-2 text-[11px]">
                Configured
              </ToggleGroupItem>
              <ToggleGroupItem value="all" className="h-6 px-2 text-[11px]">
                All
              </ToggleGroupItem>
            </ToggleGroup>
          )}
        </div>
      )}

      {layout.required.length > 0 && (
        <section className="flex flex-col gap-3" data-testid="settings-group-required">
          <h4 className="text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">Required</h4>
          {layout.required.map(render)}
        </section>
      )}
      {/* Guided essentials stay open; elsewhere a group opens by itself when it has something set. */}
      {groupList(layout.groups, (g) => (!pro && view === "all") || g.configured > 0 || layout.groups.length === 1)}

      {empty &&
        (filtering ? (
          <p className="text-xs text-muted-foreground">No setting matches “{filter.trim()}”.</p>
        ) : view === "configured" ? (
          <p className="text-xs text-muted-foreground">Nothing set yet: the provider's defaults apply. Add a setting to change one.</p>
        ) : (
          moreCount > 0 && <p className="text-xs text-muted-foreground">Nothing is required here — the defaults work. Open “More options” to customize.</p>
        ))}

      {!filtering && moreCount > 0 && (
        <>
          <Button variant="ghost" size="sm" className="self-start text-muted-foreground" onClick={() => setShowMore(!showMore)}>
            <ChevronDownIcon className={cn("transition-transform", showMore && "rotate-180")} />
            {showMore ? "Fewer options" : `More options (${moreCount})`}
          </Button>
          {showMore && <div className="flex flex-col gap-4 border-l-2 pl-3">{groupList(layout.more, () => layout.more.length === 1)}</div>}
        </>
      )}

      {layout.addable.length > 0 && !filtering && <AddSetting groups={layout.addable} onPick={reveal} />}
    </div>
  )
}

/** One group of settings, collapsible; open or closed is remembered per group across resources. */
function SettingsGroup({
  group,
  defaultOpen,
  forceOpen,
  children,
}: {
  group: SettingsGroupLayout
  defaultOpen: boolean
  forceOpen: boolean
  children: React.ReactNode
}) {
  const [userOpen] = useSectionOpen(`tf-group:${group.id}`, defaultOpen)
  const open = userOpen || forceOpen
  return (
    <section className="flex flex-col gap-3" data-testid={`settings-group-${group.id}`}>
      <GroupHeading id={group.id} title={group.title} count={group.items.length} configured={group.configured} open={open} />
      {open && children}
    </section>
  )
}

/** Memoized: typing in a group's field changes its fields, not its heading. */
const GroupHeading = memo(function GroupHeading({
  id,
  title,
  count,
  configured,
  open,
}: {
  id: SettingsGroupId
  title: string
  count: number
  configured: number
  open: boolean
}) {
  const setOpen = useSectionState((s) => s.setOpen)
  return (
    <h4 className="self-start">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(`tf-group:${id}`, !open)}
        className="flex items-center gap-1 rounded text-[11px] font-semibold tracking-wide text-muted-foreground uppercase hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
      >
        <ChevronDownIcon aria-hidden className={cn("size-3.5 transition-transform", !open && "-rotate-90")} />
        {title}
        <span className="ml-1 font-normal tracking-normal normal-case">
          {count}
          {configured > 0 && ` · ${configured} set`}
        </span>
      </button>
    </h4>
  )
})

/** Configured view: search every setting not shown yet; picking one shows it in its group and focuses it. */
function AddSetting({ groups, onPick }: { groups: SettingsGroupLayout[]; onPick: (name: string) => void }) {
  const [open, setOpen] = useState(false)
  const picked = useRef(false)
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button variant="outline" size="sm" className="self-start" data-testid="add-setting">
          <PlusIcon />
          Add setting…
        </Button>
      </PopoverTrigger>
      <PopoverContent
        className="w-72 p-0"
        align="start"
        onCloseAutoFocus={(e) => {
          // The picked setting takes the focus, not this button.
          if (picked.current) e.preventDefault()
          picked.current = false
        }}
      >
        {/* Attribute names: a plain substring match, not fuzzy ("port" shouldn't list engine_lifecycle_support). */}
        <Command filter={(value, search) => (value.includes(search.toLowerCase()) ? 1 : 0)}>
          <CommandInput placeholder="Search settings…" />
          <CommandList className="max-h-72">
            <CommandEmpty>No other setting matches.</CommandEmpty>
            {groups.map((g) => (
              <CommandGroup key={g.id} heading={g.title}>
                {g.items.map((i) => (
                  <CommandItem
                    key={settingName(i)}
                    value={settingName(i)}
                    onSelect={() => {
                      picked.current = true
                      setOpen(false)
                      onPick(settingName(i))
                    }}
                  >
                    <span className="truncate font-mono text-xs">{settingName(i)}</span>
                    {i.kind === "block" && <span className="ml-auto text-[10px] text-muted-foreground">block</span>}
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

/**
 * The editors of a block's attributes and nested blocks. One setter per attribute of the schema, writing into the
 * latest body: typing in a field replaces `body`, and fresh callbacks would re-render every (memoized) field instead
 * of the edited one.
 */
function useBlockEditors({
  schema,
  body,
  onChange,
  pathPrefix,
  depth,
}: {
  schema: SchemaBlock
  body: Body
  onChange: (b: Body) => void
  pathPrefix: string
  depth: number
}) {
  const latest = useRef({ body, onChange })
  useLayoutEffect(() => {
    latest.current = { body, onChange }
  })
  const setters = useMemo(
    () =>
      new Map(
        (schema.attributes ?? []).map((a) => [
          a.name,
          (v: Value | undefined) => latest.current.onChange(setAttr(latest.current.body, a.name, v)),
        ]),
      ),
    [schema],
  )
  const path = (name: string) => (pathPrefix ? `${pathPrefix}.${name}` : name)
  const field = (a: SchemaAttribute) =>
    objectListFields(a.type) ? (
      <ObjectListField key={a.name} attr={a} path={path(a.name)} body={body} onChange={onChange} />
    ) : (
      <AttrField key={a.name} attr={a} path={path(a.name)} value={getAttr(body, a.name)} onChange={setters.get(a.name)!} />
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
  return { field, blockEditor }
}

/** The form of a nested block's body (a resource's top level is SettingsForm, grouped). */
function BlockForm({
  schema,
  body,
  onChange,
  pathPrefix,
  depth,
}: {
  schema: SchemaBlock
  body: Body
  onChange: (b: Body) => void
  pathPrefix: string
  depth: number
}) {
  const { mode } = useInspector()
  const [showAll, setShowAll] = useState(false)
  const { field, blockEditor } = useBlockEditors({ schema, body, onChange, pathPrefix, depth })
  const attrs = editable(schema.attributes ?? [], body, depth).sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name))
  const nested = schema.blocks ?? []

  if (mode === "pro") {
    const required = attrs.filter((a) => a.required)
    const optional = attrs.filter((a) => !a.required)
    return (
      <div className="flex flex-col gap-4">
        {required.length > 0 && (
          <FieldSection title="Required">
            {required.map(field)}
          </FieldSection>
        )}
        {optional.length > 0 && (
          <FieldSection title="Optional" collapsible>
            {optional.map(field)}
          </FieldSection>
        )}
        {nested.length > 0 && (
          <FieldSection title="Blocks">
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

function FieldSection({ title, children, collapsible }: { title: string; children: React.ReactNode; collapsible?: boolean }) {
  const [open, setOpen] = useState(true)
  const heading = (
    <span className="flex items-center gap-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">
      {collapsible && <ChevronDownIcon className={cn("size-3.5 transition-transform", !open && "-rotate-90")} />}
      {title}
    </span>
  )
  if (!collapsible)
    return (
      <section className="flex flex-col gap-2.5">
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
