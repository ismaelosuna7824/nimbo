import { describe, expect, test } from "bun:test"
import type { WorkflowFile } from "@/lib/api/types"
import { nameProblem } from "./file-model"
import {
  changesRequest,
  createFile,
  createFolder,
  deletePath,
  NO_PENDING,
  newFile,
  orderMoves,
  renamePath,
  viewFiles,
} from "./pending-files"

const server: WorkflowFile[] = [
  { ...newFile("main.tf"), generated: true },
  newFile("modules/net/main.tf"),
  newFile("modules/net/README.md"),
  newFile("src/index.js"),
]
const paths = (files: WorkflowFile[]) => files.map((f) => f.path).sort()

describe("pending files", () => {
  test("new files and folders show in the view; a file empties its folder", () => {
    let p = createFolder(NO_PENDING, "templates")
    p = createFolder(p, "scripts")
    p = createFile(p, "templates/user_data.tftpl")
    expect(p.folders).toEqual(["scripts"])
    const { files, status } = viewFiles(server, p, { "templates/user_data.tftpl": { base: "", value: "hi" } })
    expect(paths(files)).toContain("templates/user_data.tftpl")
    expect(status["templates/user_data.tftpl"]).toBe("added")
    expect(files.find((f) => f.path === "templates/user_data.tftpl")?.size).toBe(2)
  })

  test("a path is shown once when the server already has it (right after an apply)", () => {
    const p = createFile(renamePath(NO_PENDING, server, "src/index.js", "src/app.js").pending, "queues.tf")
    const applied = [...server.filter((f) => f.path !== "src/index.js"), newFile("src/app.js"), newFile("queues.tf")]
    const { files, status } = viewFiles(applied, p, {})
    expect(paths(files)).toEqual(paths(applied))
    expect(status).toEqual({})
  })

  test("renaming a folder moves every file; renaming back clears the move", () => {
    const r = renamePath(NO_PENDING, server, "modules/net", "modules/network")
    expect(r.renames).toEqual([
      ["modules/net/main.tf", "modules/network/main.tf"],
      ["modules/net/README.md", "modules/network/README.md"],
    ])
    expect(r.pending.moved).toEqual({
      "modules/network/main.tf": "modules/net/main.tf",
      "modules/network/README.md": "modules/net/README.md",
    })
    const view = viewFiles(server, r.pending, {}).files
    const back = renamePath(r.pending, view, "modules/network", "modules/net")
    expect(back.pending.moved).toEqual({})
  })

  test("deleting a moved file deletes it at its server path; a new file just goes away", () => {
    let p = renamePath(NO_PENDING, server, "src/index.js", "src/app.js").pending
    p = createFile(p, "notes.md")
    let view = viewFiles(server, p, {}).files
    p = deletePath(p, view, "src/app.js").pending
    view = viewFiles(server, p, {}).files
    p = deletePath(p, view, "notes.md").pending
    expect(p).toEqual({ created: {}, moved: {}, deleted: { "src/index.js": true }, folders: [] })
  })

  test("the request carries moves, deletions, new files and edits", () => {
    let p = renamePath(NO_PENDING, server, "main.tf", "app.tf").pending
    p = createFile(p, "queues.tf")
    p = deletePath(p, viewFiles(server, p, {}).files, "src/index.js").pending
    const req = changesRequest(p, {
      "queues.tf": { base: "", value: 'resource "aws_sqs_queue" "q" {}\n' },
      "app.tf": { base: "a", value: "b" },
    })
    expect(req.moves).toEqual([{ from: "main.tf", to: "app.tf" }])
    expect(req.changes).toEqual({ "src/index.js": null, "queues.tf": 'resource "aws_sqs_queue" "q" {}\n', "app.tf": "b" })
  })

  test("moves are ordered; a swap goes through a temporary name", () => {
    expect(
      orderMoves([
        { from: "b", to: "c" },
        { from: "a", to: "b" },
      ]),
    ).toEqual([
      { from: "b", to: "c" },
      { from: "a", to: "b" },
    ])
    expect(
      orderMoves([
        { from: "a", to: "b" },
        { from: "b", to: "a" },
      ]),
    ).toEqual([
      { from: "a", to: "a-moving-1" },
      { from: "b", to: "a" },
      { from: "a-moving-1", to: "b" },
    ])
  })
})

describe("nameProblem", () => {
  test("accepts supported files, nested paths and names without extension", () => {
    for (const ok of ["queues.tf", "prod.tfvars", "templates/user_data.tftpl", "Makefile", "scripts/setup.sh", "a.yml"]) {
      expect(nameProblem(ok, "file")).toBeUndefined()
    }
    expect(nameProblem("modules/net", "folder")).toBeUndefined()
  })

  test("rejects absolute paths, traversal, hidden names and other types", () => {
    expect(nameProblem("", "file")).toBe("Enter a name")
    expect(nameProblem("/etc/passwd", "file")).toContain("relative")
    expect(nameProblem("../x.tf", "file")).toContain("..")
    expect(nameProblem("a//b.tf", "file")).toContain("empty")
    expect(nameProblem(".env", "file")).toContain("Hidden")
    expect(nameProblem("a\\b.tf", "file")).toContain("/")
    expect(nameProblem("image.png", "file")).toContain("Unsupported file type")
    expect(nameProblem("terraform.tfstate", "file")).toContain("state")
  })
})
