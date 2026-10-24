export interface CodeBadgeInfo {
  kind: "requires-code" | "no-code"
  label: string
  hint: string
}

/**
 * The "Requires code" marker of a challenge (`solvableWithoutCode: false`), on cards and the header; the
 * subtle "No code needed" marker only on the header (`true`). Undeclared challenges show nothing.
 */
export function codeBadge(solvableWithoutCode: boolean | undefined, place: "card" | "header"): CodeBadgeInfo | null {
  if (solvableWithoutCode === false) {
    return {
      kind: "requires-code",
      label: "Requires code",
      hint: "Some steps need editing code or files (a Dockerfile, application code or a Terraform expression) — the canvas and inspectors alone are not enough.",
    }
  }
  if (solvableWithoutCode === true && place === "header") {
    return { kind: "no-code", label: "No code needed", hint: "Every step can be done in the canvas and inspectors." }
  }
  return null
}
