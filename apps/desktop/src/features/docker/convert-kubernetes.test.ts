import { describe, expect, test } from "bun:test"
import { DNS_LABEL, suggestKubeName } from "./convert-kubernetes"

describe("suggestKubeName", () => {
  test("turns workflow names into DNS labels like the server", () => {
    expect(suggestKubeName("Node.js API + PostgreSQL + Redis")).toBe("node-js-api-postgresql-redis")
    expect(suggestKubeName("  Shop_Backend  ")).toBe("shop-backend")
    expect(suggestKubeName("3 tier app")).toBe("s-3-tier-app")
    expect(suggestKubeName("¡¡!!")).toBe("app")
  })

  test("stays a valid label at the length limit", () => {
    const s = suggestKubeName(`${"a".repeat(62)} b`)
    expect(s.length).toBeLessThanOrEqual(63)
    expect(DNS_LABEL.test(s)).toBe(true)
  })
})

describe("DNS_LABEL", () => {
  test("accepts names the converter accepts", () => {
    expect(DNS_LABEL.test("shop")).toBe(true)
    expect(DNS_LABEL.test("shop-1")).toBe(true)
    expect(DNS_LABEL.test("Shop")).toBe(false)
    expect(DNS_LABEL.test("1shop")).toBe(false)
    expect(DNS_LABEL.test("shop-")).toBe(false)
  })
})
