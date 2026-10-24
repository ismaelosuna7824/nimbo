import { describe, expect, test } from "bun:test"
import type { ServiceFacts } from "@/lib/api/types"
import {
  capDropPatch,
  hardeningItems,
  hardeningScore,
  isRootUser,
  noNewPrivilegesPatch,
  nonRootUserFor,
  normalizeCapability,
  readOnlyPatch,
  secretCandidates,
  secretNameFor,
  withItem,
  withoutItem,
} from "./hardening"

const base: ServiceFacts = { baseImages: [], ports: [], expose: [], environment: [], volumes: [], networks: [], dependsOn: [] }
const SECRET = /(pass(word)?|secret|token|api[_-]?key|private[_-]?key|credential)/i

describe("users", () => {
  test("isRootUser matches the dk_root_user check", () => {
    for (const u of [undefined, "", "root", "0", "root:root", "0:0"]) expect(isRootUser(u)).toBe(true)
    for (const u of ["node", "1000:1000", "nonroot", "10001"]) expect(isRootUser(u)).toBe(false)
  })
  test("nonRootUserFor prefers the image's documented user", () => {
    expect(nonRootUserFor({ nonRootUser: "node" })).toEqual({ user: "node", documented: true })
    expect(nonRootUserFor({})).toEqual({ user: "1000:1000", documented: false })
  })
})

describe("score", () => {
  test("the incident starter scores low, the hardened service full", () => {
    const starter: ServiceFacts = { ...base, privileged: true, volumes: [{ type: "bind", source: "/var/run/docker.sock", target: "/var/run/docker.sock" }] }
    expect(hardeningScore(starter)).toEqual({ passed: 0, total: 7 })
    const hardened: ServiceFacts = {
      ...base,
      user: "node",
      capDrop: ["all"],
      readOnly: true,
      tmpfs: ["/tmp"],
      securityOpt: ["no-new-privileges"],
      init: true,
    }
    expect(hardeningScore(hardened)).toEqual({ passed: 7, total: 7 })
    expect(hardeningItems({ ...hardened, securityOpt: ["no-new-privileges:false"] }).find((i) => i.id === "no-new-privileges")?.ok).toBe(false)
  })
})

describe("patches", () => {
  test("cap_drop keeps other entries and removes the key when empty", () => {
    expect(capDropPatch({ capDrop: ["NET_RAW"] }, true)).toEqual({ capDrop: ["NET_RAW", "ALL"] })
    expect(capDropPatch({ capDrop: ["all"] }, true)).toEqual({ capDrop: ["ALL"] })
    expect(capDropPatch({ capDrop: ["ALL"] }, false)).toEqual({ capDrop: null })
  })
  test("security_opt", () => {
    expect(noNewPrivilegesPatch({ securityOpt: ["seccomp:unconfined"] }, true)).toEqual({ securityOpt: ["seccomp:unconfined", "no-new-privileges:true"] })
    expect(noNewPrivilegesPatch({ securityOpt: ["no-new-privileges"] }, false)).toEqual({ securityOpt: null })
  })
  test("read_only adds /tmp only when there is no tmpfs", () => {
    expect(readOnlyPatch({}, true)).toEqual({ readOnly: true, tmpfs: ["/tmp"] })
    expect(readOnlyPatch({ tmpfs: ["/run"] }, true)).toEqual({ readOnly: true })
    expect(readOnlyPatch({ tmpfs: ["/tmp"] }, false)).toEqual({ readOnly: false })
  })
  test("chips", () => {
    expect(normalizeCapability(" cap_net_bind_service ")).toBe("NET_BIND_SERVICE")
    expect(withItem(["/tmp"], " /run ")).toEqual(["/tmp", "/run"])
    expect(withItem(["/tmp"], "/tmp")).toEqual(["/tmp"])
    expect(withItem(undefined, " ")).toEqual([])
    expect(withoutItem(["/tmp"], "/tmp")).toBeNull()
    expect(withoutItem(["/tmp", "/run"], "/tmp")).toEqual(["/run"])
  })
})

describe("secrets", () => {
  test("secretNameFor", () => {
    expect(secretNameFor("POSTGRES_PASSWORD")).toBe("postgres_password")
    expect(secretNameFor("__API KEY")).toBe("api_key")
    expect(secretNameFor("$$")).toBe("secret")
  })
  test("candidates: secret-looking keys written in compose, flagged when the image reads <KEY>_FILE", () => {
    const svc: ServiceFacts = {
      ...base,
      fileEnv: ["POSTGRES_PASSWORD", "POSTGRES_USER"],
      environment: [
        { key: "POSTGRES_USER", value: "orders" },
        { key: "POSTGRES_PASSWORD", secret: true },
        { key: "API_TOKEN", secret: true, fromFile: ".env" },
        { key: "DB_PASSWORD_FILE", value: "/run/secrets/db" },
        { key: "STRIPE_API_KEY", value: "sk" },
      ],
    }
    expect(secretCandidates(svc, (k) => SECRET.test(k))).toEqual([
      { key: "POSTGRES_PASSWORD", imageReadsFile: true, secret: "postgres_password" },
      { key: "STRIPE_API_KEY", imageReadsFile: false, secret: "stripe_api_key" },
    ])
  })
})
