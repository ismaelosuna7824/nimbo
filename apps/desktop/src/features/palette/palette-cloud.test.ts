import { describe, expect, test } from "bun:test"
import { paletteCloud } from "./palette-cloud"

describe("paletteCloud", () => {
  test("opens on the workflow's first cloud", () => {
    expect(paletteCloud(null, ["gcp"])).toBe("gcp")
    expect(paletteCloud(undefined, ["azure", "aws"])).toBe("azure")
  })

  test("keeps the cloud the user picked for the workflow", () => {
    expect(paletteCloud("aws", ["gcp"])).toBe("aws")
  })

  test("ignores an unknown stored value and defaults to AWS without clouds", () => {
    expect(paletteCloud("ibm", ["oci"])).toBe("oci")
    expect(paletteCloud(null, [])).toBe("aws")
    expect(paletteCloud(null, undefined)).toBe("aws")
  })
})
