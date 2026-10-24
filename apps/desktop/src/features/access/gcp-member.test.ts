import { describe, expect, test } from "bun:test"
import type { GCPIAMBinding, WorkflowNode } from "@/lib/api/types"
import { buildMember, gcpChanged, gcpProblems, parseMember, refValue, serviceAccountOptions } from "./gcp-member"

const node = (type: string, name: string, kind: WorkflowNode["kind"] = "resource"): WorkflowNode => ({
  id: `${type}.${name}`,
  kind,
  type,
  name,
  position: { x: 0, y: 0 },
  positioned: true,
  body: { attrs: [], blocks: [] },
})

describe("parseMember / buildMember", () => {
  test("literal, reference and interpolated members round-trip", () => {
    for (const expr of [
      '"user:ana@example.com"',
      '"serviceAccount:${google_service_account.run.email}"',
      '"group:${var.analysts}"',
      '"domain:example.com"',
      '"allUsers"',
      '"allAuthenticatedUsers"',
      "var.member",
      '"principalSet://iam.googleapis.com/projects/1/locations/global/workloadIdentityPools/p/*"',
    ]) {
      expect(buildMember(parseMember(expr))).toBe(expr)
    }
  })

  test("splits type and value", () => {
    expect(parseMember('"serviceAccount:${google_service_account.run.email}"')).toEqual({
      type: "serviceAccount",
      value: "${google_service_account.run.email}",
    })
    expect(parseMember('"allUsers"')).toEqual({ type: "allUsers", value: "" })
    expect(parseMember("local.member")).toEqual({ type: "expression", value: "local.member" })
    expect(parseMember('"weird"')).toEqual({ type: "expression", value: '"weird"' })
  })

  test("builds the member of a linked service account", () => {
    expect(buildMember({ type: "serviceAccount", value: refValue("google_service_account.run.email") })).toBe(
      '"serviceAccount:${google_service_account.run.email}"',
    )
  })

  test("escapes quotes and backslashes of typed values", () => {
    const m = buildMember({ type: "user", value: 'a"b\\c' })
    expect(m).toBe('"user:a\\"b\\\\c"')
    expect(parseMember(m)).toEqual({ type: "user", value: 'a"b\\c' })
  })
})

describe("serviceAccountOptions", () => {
  test("lists service accounts as member values", () => {
    const opts = serviceAccountOptions([node("google_service_account", "run"), node("google_storage_bucket", "b"), node("google_service_account", "ext", "data")])
    expect(opts.map((o) => o.value)).toEqual(["${google_service_account.run.email}", "${data.google_service_account.ext.email}"])
  })
  test("lists the service agents of the projects in the graph", () => {
    const opts = serviceAccountOptions([node("google_project", "current", "data")])
    const pubsub = opts.find((o) => o.label === "Pub/Sub service agent of data.google_project.current")
    expect(pubsub?.value).toBe("service-${data.google_project.current.number}@gcp-sa-pubsub.iam.gserviceaccount.com")
    expect(buildMember({ type: "serviceAccount", value: pubsub!.value })).toBe(
      '"serviceAccount:service-${data.google_project.current.number}@gcp-sa-pubsub.iam.gserviceaccount.com"',
    )
  })
})

describe("gcpProblems / gcpChanged", () => {
  const base: GCPIAMBinding = {
    kind: "member",
    resourceAttr: "bucket",
    resource: "google_storage_bucket.b.name",
    role: "roles/storage.objectViewer",
    members: ['"group:${var.g}"'],
    editable: true,
  }
  test("a valid member has no problems", () => {
    expect(gcpProblems(base)).toEqual([])
  })
  test("flags missing parts, duplicates and bad roles", () => {
    expect(gcpProblems({ ...base, members: [] })).toContain("Add who gets the role.")
    expect(gcpProblems({ ...base, members: ['"user:a@x.com"', '"user:b@x.com"'] })).toContain(
      "An IAM member grants one member; use a binding for several.",
    )
    expect(gcpProblems({ ...base, kind: "binding", members: ['"user:a@x.com"', '"user:a@x.com"'] })).toContain("A member is listed twice.")
    expect(gcpProblems({ ...base, role: "storage.objectViewer" })).toContain("Pick a role (roles/<service>.<role>).")
    expect(gcpProblems({ ...base, role: "projects/p/roles/custom" })).toEqual([])
    expect(gcpProblems({ ...base, members: ['"group:"'] })).toContain("Fill in the google group.")
    expect(gcpProblems({ ...base, resource: "" })).toContain("Pick the resource (bucket).")
  })
  test("detects edits", () => {
    expect(gcpChanged(base, { ...base })).toBe(false)
    expect(gcpChanged(base, { ...base, role: "roles/storage.objectUser" })).toBe(true)
    expect(gcpChanged(base, { ...base, members: ['"group:${var.g}"', '"user:a@x.com"'] })).toBe(true)
  })
})
