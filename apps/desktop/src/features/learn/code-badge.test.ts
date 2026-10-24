import { describe, expect, test } from "bun:test"
import { codeBadge } from "./code-badge"

describe("codeBadge", () => {
  test("requires code on cards and the header", () => {
    for (const place of ["card", "header"] as const) {
      const b = codeBadge(false, place)
      expect(b?.kind).toBe("requires-code")
      expect(b?.label).toBe("Requires code")
      expect(b?.hint).toContain("editing code or files")
    }
  })

  test("no code needed only on the header", () => {
    expect(codeBadge(true, "card")).toBeNull()
    expect(codeBadge(true, "header")?.kind).toBe("no-code")
  })

  test("undeclared shows nothing", () => {
    expect(codeBadge(undefined, "card")).toBeNull()
    expect(codeBadge(undefined, "header")).toBeNull()
  })
})
