import { describe, expect, test } from "bun:test"
import { isBestPractice } from "@/lib/api/types"
import { ruleEnabled, toggleRuleSettings } from "./practices"

describe("best-practice rule toggles", () => {
  const bp = { disabledRules: ["dk_no_restart"], enabledRules: ["dk_hardening"] }

  test("default-on rules follow disabledRules, opt-in rules follow enabledRules", () => {
    expect(ruleEnabled({ code: "dk_no_restart" }, bp)).toBe(false)
    expect(ruleEnabled({ code: "dk_latest_tag" }, bp)).toBe(true)
    expect(ruleEnabled({ code: "dk_hardening", defaultOff: true }, bp)).toBe(true)
    expect(ruleEnabled({ code: "dk_no_limits", defaultOff: true }, bp)).toBe(false)
    expect(ruleEnabled({ code: "dk_no_limits", defaultOff: true }, { disabledRules: [] })).toBe(false)
  })

  test("toggling writes the list that matters for the rule", () => {
    expect(toggleRuleSettings({ code: "dk_no_restart" }, true, bp)).toEqual({ disabledRules: [], enabledRules: ["dk_hardening"] })
    expect(toggleRuleSettings({ code: "bp_missing_tags" }, false, bp).disabledRules).toEqual(["dk_no_restart", "bp_missing_tags"])
    expect(toggleRuleSettings({ code: "dk_no_limits", defaultOff: true }, true, bp).enabledRules).toEqual(["dk_hardening", "dk_no_limits"])
    expect(toggleRuleSettings({ code: "dk_hardening", defaultOff: true }, false, bp).enabledRules).toEqual([])
  })
})

describe("isBestPractice", () => {
  test("docker advice is a best practice, docker errors are not", () => {
    expect(isBestPractice({ severity: "info", code: "dk_latest_tag", message: "" })).toBe(true)
    expect(isBestPractice({ severity: "warning", code: "dk_layer_order", message: "" })).toBe(true)
    expect(isBestPractice({ severity: "error", code: "dk_yaml", message: "" })).toBe(false)
    expect(isBestPractice({ severity: "info", code: "bp_name_prefix", message: "" })).toBe(true)
    expect(isBestPractice({ severity: "warning", code: "net_open_ssh", message: "" })).toBe(false)
  })

  test("kubernetes advice is a best practice, kubernetes errors are not", () => {
    expect(isBestPractice({ severity: "warning", code: "k8s_no_requests", message: "" })).toBe(true)
    expect(isBestPractice({ severity: "info", code: "k8s_latest_tag", message: "" })).toBe(true)
    expect(isBestPractice({ severity: "error", code: "k8s_schema", message: "" })).toBe(false)
  })
})
