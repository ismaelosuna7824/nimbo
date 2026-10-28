import { BadgeCheck, CircleCheck, CircleDashed, CircleMinus, CircleSlash, CircleX, type LucideIcon } from "lucide-react"
import type { ComplianceControl, ComplianceStatus } from "@/lib/api/types"

/**
 * Tone per status (docs/UX_GUIDELINES.md rule 2): green only where Nimbo verified the design, red for violations (a
 * finding on any resource the control applies to: that is how an auditor reads it), violet for what only the user can
 * attest (dashed while open), muted for controls without a verdict (their reason is in a tooltip).
 */
export const STATUS_META: Record<ComplianceStatus, { label: string; icon: LucideIcon; className: string; chip: string; hint: string }> = {
  violated: {
    label: "Violated",
    icon: CircleX,
    className: "text-destructive",
    chip: "bg-destructive/12 text-destructive",
    hint: "Findings of its rules on resources the control applies to.",
  },
  met: {
    label: "Met",
    icon: CircleCheck,
    className: "text-emerald-600 dark:text-emerald-400",
    chip: "bg-emerald-500/12 text-emerald-600 dark:text-emerald-400",
    hint: "No findings of its rules on the design.",
  },
  manual: {
    label: "Manual",
    icon: CircleDashed,
    className: "text-violet-600 dark:text-violet-400",
    chip: "border border-dashed border-violet-500/60 text-violet-600 dark:text-violet-400",
    hint: "A design can't show it (account settings, MFA, processes): mark it as done once it is.",
  },
  attested: {
    label: "Attested",
    icon: BadgeCheck,
    className: "text-violet-600 dark:text-violet-400",
    chip: "bg-violet-500/12 text-violet-600 dark:text-violet-400",
    hint: "Marked as done by you, with a note for the report.",
  },
  not_evaluated: {
    label: "Not evaluated",
    icon: CircleSlash,
    className: "text-muted-foreground",
    chip: "bg-muted text-muted-foreground",
    hint: "One of its rules is turned off in Settings → Best-practice checks.",
  },
  not_applicable: {
    label: "Not applicable",
    icon: CircleMinus,
    className: "text-muted-foreground",
    chip: "bg-muted text-muted-foreground",
    hint: "Nothing in this design is what the control is about.",
  },
}

/** Display order: what needs attention first. */
export const STATUS_ORDER: ComplianceStatus[] = ["violated", "manual", "met", "attested", "not_evaluated", "not_applicable"]

/** Controls grouped by section (benchmark order), each section with its violated controls first. */
export function sections(controls: ComplianceControl[]): { section: string; controls: ComplianceControl[] }[] {
  const out: { section: string; controls: ComplianceControl[] }[] = []
  for (const c of controls) {
    let s = out.find((x) => x.section === c.section)
    if (!s) out.push((s = { section: c.section, controls: [] }))
    s.controls.push(c)
  }
  for (const s of out) s.controls.sort((a, b) => STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status))
  return out
}
