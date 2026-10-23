import { describe, expect, test } from "bun:test"
import type { ImportProject } from "@/lib/api/types"
import { initialSelection, selectAll, toggleProject } from "./import-selection"

const project = (path: string, extra: Partial<ImportProject> = {}): ImportProject => ({
  path,
  name: path,
  resources: 0,
  dataSources: 0,
  modules: 0,
  providers: [],
  clouds: [],
  tfvarsFiles: [],
  hasBackend: false,
  diagnostics: [],
  layout: "single",
  kind: "docker",
  ...extra,
})

const projects = [
  project("docker"),
  project("dockerfiles:services", { members: ["services/api", "services/web"] }),
  project("services/api"),
  project("services/web"),
  project("modules/net", { kind: undefined, diagnostics: [{ severity: "info", code: "unused_module", message: "unused" }] }),
]

describe("import selection", () => {
  test("a Dockerfile group starts selected instead of its folders", () => {
    expect(initialSelection(projects)).toEqual({
      docker: true,
      "dockerfiles:services": true,
      "services/api": false,
      "services/web": false,
      "modules/net": false,
    })
  })

  test("a group and its folders exclude each other", () => {
    const start = initialSelection(projects)
    const member = toggleProject(projects, start, "services/api", true)
    expect(member["dockerfiles:services"]).toBe(false)
    expect(member["services/api"]).toBe(true)
    const group = toggleProject(projects, { ...member, "services/web": true }, "dockerfiles:services", true)
    expect(group).toMatchObject({ "dockerfiles:services": true, "services/api": false, "services/web": false })
    expect(toggleProject(projects, group, "docker", false).docker).toBe(false)
  })

  test("select all takes the group, not its folders", () => {
    expect(selectAll(projects, true)).toEqual({
      docker: true,
      "dockerfiles:services": true,
      "services/api": false,
      "services/web": false,
      "modules/net": true,
    })
    expect(Object.values(selectAll(projects, false)).some(Boolean)).toBe(false)
  })
})
