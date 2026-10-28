import { describe, expect, test } from "bun:test"
import type { SchemaAttribute } from "@/lib/api/types"
import { classifySetting } from "./settings-groups"
import { layoutSettings, settingName, type SettingItem, type SettingsLayout } from "./settings-layout"

function attr(name: string, extra: Partial<SchemaAttribute> = {}, set = false): SettingItem {
  return { kind: "attr", attr: { name, type: "string", optional: true, ...extra }, set }
}

// A slice of aws_db_instance: instance_class is required, a few are basic, two are set.
const ITEMS: SettingItem[] = [
  attr("instance_class", { required: true, optional: false }, true),
  attr("allocated_storage", { basic: true }, true),
  attr("engine", { basic: true }),
  attr("kms_key_id"),
  attr("storage_encrypted", { basic: true }),
  attr("port"),
  attr("tags", {}, true),
  attr("apply_immediately"),
  { kind: "block", block: { name: "restore_to_point_in_time", nesting: "list", block: { attributes: [], blocks: [] } }, set: false },
]

const groupOf = (i: SettingItem) => classifySetting(settingName(i))
const names = (l: SettingsLayout["groups"]) => Object.fromEntries(l.map((g) => [g.id, g.items.map(settingName)]))

describe("layoutSettings", () => {
  test("Pro, All: required first, every other setting in its group", () => {
    const l = layoutSettings(ITEMS, { pro: true, view: "all", filter: "", revealed: new Set(), groupOf })
    expect(l.required.map(settingName)).toEqual(["instance_class"])
    expect(names(l.groups)).toEqual({
      general: ["engine"],
      networking: ["port"],
      security: ["storage_encrypted", "kms_key_id"],
      storage: ["allocated_storage"],
      backup: ["restore_to_point_in_time"],
      tags: ["tags"],
      advanced: ["apply_immediately"],
    })
    expect(l.groups.find((g) => g.id === "storage")?.configured).toBe(1)
    expect(l.more).toEqual([])
    expect(l.addable).toEqual([])
  })

  test("Guided, All: essentials grouped, the rest behind More options grouped the same way", () => {
    const l = layoutSettings(ITEMS, { pro: false, view: "all", filter: "", revealed: new Set(), groupOf })
    expect(names(l.groups)).toEqual({ general: ["engine"], security: ["storage_encrypted"], storage: ["allocated_storage"], tags: ["tags"] })
    expect(names(l.more)).toEqual({ networking: ["port"], security: ["kms_key_id"], backup: ["restore_to_point_in_time"], advanced: ["apply_immediately"] })
  })

  test("Configured: required and set settings, the others addable; a revealed one joins its group", () => {
    const l = layoutSettings(ITEMS, { pro: true, view: "configured", filter: "", revealed: new Set(["port"]), groupOf })
    expect(l.required.map(settingName)).toEqual(["instance_class"])
    expect(names(l.groups)).toEqual({ networking: ["port"], storage: ["allocated_storage"], tags: ["tags"] })
    expect(names(l.addable)).toEqual({
      general: ["engine"],
      security: ["storage_encrypted", "kms_key_id"],
      backup: ["restore_to_point_in_time"],
      advanced: ["apply_immediately"],
    })
    expect(l.more).toEqual([])
  })

  test("a filter searches every setting, whatever the view", () => {
    const l = layoutSettings(ITEMS, { pro: true, view: "configured", filter: "KMS", revealed: new Set(), groupOf })
    expect(l.required).toEqual([])
    expect(names(l.groups)).toEqual({ security: ["kms_key_id"] })
    expect(l.addable).toEqual([])
  })

  test("inside a group: set first, attributes before blocks, basic first, then by name", () => {
    const items = [attr("snapshot_identifier"), attr("backup_window", { basic: true }), attr("skip_final_snapshot", {}, true), ITEMS[8]]
    const l = layoutSettings(items, { pro: true, view: "all", filter: "", revealed: new Set(), groupOf })
    expect(names(l.groups)).toEqual({ backup: ["skip_final_snapshot", "backup_window", "snapshot_identifier", "restore_to_point_in_time"] })
  })
})
