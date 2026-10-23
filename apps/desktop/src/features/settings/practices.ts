import type { BestPracticeRule, Settings } from "@/lib/api/types"

type RuleRef = Pick<BestPracticeRule, "code" | "defaultOff">

/** Whether a rule runs under the given settings (opt-in rules only when enabled). */
export function ruleEnabled(rule: RuleRef, bp: Settings["bestPractices"]): boolean {
  if (rule.defaultOff) return (bp.enabledRules ?? []).includes(rule.code)
  return !bp.disabledRules.includes(rule.code)
}

/** The settings after switching one rule on or off. */
export function toggleRuleSettings(rule: RuleRef, on: boolean, bp: Settings["bestPractices"]): Settings["bestPractices"] {
  if (rule.defaultOff) {
    const enabled = (bp.enabledRules ?? []).filter((c) => c !== rule.code)
    return { ...bp, enabledRules: on ? [...enabled, rule.code] : enabled }
  }
  const disabled = bp.disabledRules.filter((c) => c !== rule.code)
  return { ...bp, disabledRules: on ? disabled : [...disabled, rule.code] }
}
