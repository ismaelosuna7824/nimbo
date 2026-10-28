import type { SchemaAttribute, SchemaNestedBlock } from "@/lib/api/types"
import { SETTINGS_GROUPS, type SettingsGroupId } from "./settings-groups"
import type { SettingsView } from "./section-state"

/** A top-level setting of a resource: an attribute or a nested block, and whether the node sets it. */
export type SettingItem = ({ kind: "attr"; attr: SchemaAttribute } | { kind: "block"; block: SchemaNestedBlock }) & { set: boolean }

export interface SettingsGroupLayout {
  id: SettingsGroupId
  title: string
  items: SettingItem[]
  /** How many of `items` the node sets. */
  configured: number
}

export interface SettingsLayout {
  /** Shown first, ungrouped. */
  required: SettingItem[]
  /** Shown groups, non-empty, in display order. */
  groups: SettingsGroupLayout[]
  /** Guided "More options": the rest, grouped the same way. */
  more: SettingsGroupLayout[]
  /** Configured view: what "Add setting…" offers, grouped. */
  addable: SettingsGroupLayout[]
}

/** Above this many settings, Settings offers the Configured / All switch. */
export const MANY_SETTINGS = 12

export function settingName(item: SettingItem) {
  return item.kind === "attr" ? item.attr.name : item.block.name
}

function isRequired(item: SettingItem) {
  return item.kind === "attr" ? !!item.attr.required : (item.block.minItems ?? 0) > 0
}

/** Guided essentials: required, marked basic by the catalog, or already set. */
function isEssential(item: SettingItem) {
  return item.set || isRequired(item) || (item.kind === "attr" && !!item.attr.basic)
}

// Set first (what the node does), then attributes before blocks, the catalog's basic ones first, then by name.
function order(a: SettingItem, b: SettingItem) {
  const basic = (i: SettingItem) => (i.kind === "attr" && i.attr.basic ? 0 : 1)
  return (
    Number(b.set) - Number(a.set) ||
    Number(a.kind === "block") - Number(b.kind === "block") ||
    basic(a) - basic(b) ||
    settingName(a).localeCompare(settingName(b))
  )
}

function grouped(items: SettingItem[], groupOf: (item: SettingItem) => SettingsGroupId): SettingsGroupLayout[] {
  return SETTINGS_GROUPS.flatMap(({ id, title }) => {
    const mine = items.filter((i) => groupOf(i) === id).sort(order)
    return mine.length ? [{ id, title, items: mine, configured: mine.filter((i) => i.set).length }] : []
  })
}

/**
 * What Settings shows of a resource's top-level `items`, and where:
 * - a filter shows every match, grouped, whatever the view;
 * - Configured: required, set and `revealed` settings (picked from "Add setting…"); the others are addable;
 * - All, Pro: everything; All, Guided: the essentials, the rest behind "More options".
 * Required settings come first; the others go to their group, set ones first.
 */
export function layoutSettings(
  items: SettingItem[],
  opts: { pro: boolean; view: SettingsView; filter: string; revealed: ReadonlySet<string>; groupOf: (item: SettingItem) => SettingsGroupId },
): SettingsLayout {
  const query = opts.filter.trim().toLowerCase()
  const shown = query ? items.filter((i) => settingName(i).toLowerCase().includes(query)) : items
  const visible = (i: SettingItem) =>
    query || (opts.view === "all" && opts.pro) || opts.revealed.has(settingName(i)) || (opts.view === "configured" ? i.set || isRequired(i) : isEssential(i))
  const required = shown.filter(isRequired).sort(order)
  const rest = shown.filter((i) => !isRequired(i))
  const hidden = rest.filter((i) => !visible(i))
  return {
    required,
    groups: grouped(rest.filter(visible), opts.groupOf),
    more: opts.view === "all" ? grouped(hidden, opts.groupOf) : [],
    addable: opts.view === "configured" ? grouped(hidden, opts.groupOf) : [],
  }
}
