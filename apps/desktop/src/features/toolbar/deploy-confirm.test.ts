import { describe, expect, test } from "bun:test"
import type { DestructiveChange } from "@/lib/api/types"
import { causeLabel, confirmLabel, groupDestructive } from "./deploy-confirm"

const change = (address: string, action: DestructiveChange["action"], over: Partial<DestructiveChange> = {}): DestructiveChange => ({
  address,
  action,
  ...over,
})

describe("groupDestructive", () => {
  test("splits replaced and deleted instances, each in address order", () => {
    const groups = groupDestructive([
      change("aws_sqs_queue.b", "delete"),
      change("aws_db_instance.main", "replace"),
      change("aws_sqs_queue.a", "delete"),
      change("aws_ami.base", "replace"),
    ])
    expect(groups.replace.map((c) => c.address)).toEqual(["aws_ami.base", "aws_db_instance.main"])
    expect(groups.delete.map((c) => c.address)).toEqual(["aws_sqs_queue.a", "aws_sqs_queue.b"])
  })

  test("does not reorder the input", () => {
    const input = [change("b", "delete"), change("a", "delete")]
    groupDestructive(input)
    expect(input.map((c) => c.address)).toEqual(["b", "a"])
  })
})

describe("confirmLabel", () => {
  test("names the kind of destruction and counts every instance", () => {
    expect(confirmLabel(groupDestructive([change("a", "replace"), change("b", "replace")]))).toBe("Deploy and replace 2")
    expect(confirmLabel(groupDestructive([change("a", "delete")]))).toBe("Deploy and delete 1")
    expect(confirmLabel(groupDestructive([change("a", "replace"), change("b", "delete"), change("c", "delete")]))).toBe(
      "Deploy and destroy 3",
    )
  })
})

describe("causeLabel", () => {
  test("replacements name the attributes forcing them", () => {
    expect(causeLabel(change("a", "replace", { cause: "replace_because_cannot_update", reasons: ["engine", "name"] }))).toBe(
      "forced by: engine, name",
    )
  })

  test("translates Terraform's action reasons", () => {
    expect(causeLabel(change("a", "delete", { cause: "delete_because_no_resource_config" }))).toBe("removed from the design")
    expect(causeLabel(change("a", "delete", { cause: "delete_because_no_module" }))).toBe("its module was removed")
    expect(causeLabel(change("a", "delete", { cause: "delete_because_count_index" }))).toBe("count no longer has this instance")
    expect(causeLabel(change("a", "delete", { cause: "delete_because_each_key" }))).toBe("for_each no longer has this instance")
    expect(causeLabel(change("a", "replace", { cause: "replace_because_tainted" }))).toBe("tainted")
    expect(causeLabel(change("a", "replace", { cause: "replace_by_request" }))).toBe("replacement requested")
  })

  test("falls back to readable words", () => {
    expect(causeLabel(change("a", "delete", { cause: "delete_because_something_new" }))).toBe("delete because something new")
    expect(causeLabel(change("a", "replace"))).toBe("must be re-created")
    expect(causeLabel(change("a", "delete"))).toBe("no longer in the design")
  })
})
