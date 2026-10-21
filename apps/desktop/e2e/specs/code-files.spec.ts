import type { ModuleView, Workflow, WorkflowFile } from "../../src/lib/api/types"
import { expect, test, type App, type NimboApi } from "../support/fixtures"

/** Opens a workflow with one S3 bucket in Pro mode and returns it with the Code panel. */
async function openPro(app: App, api: NimboApi, name: string) {
  const wf = await api.createWorkflow(name)
  await api.addNode(wf.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow(name)
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await expect(panel.getByRole("tab", { name: "main.tf" })).toHaveAttribute("aria-selected", "true")
  return { wf, panel }
}

test("creates a Terraform file in the tree and applies it to the diagram", async ({ app, api, page }) => {
  const { wf, panel } = await openPro(app, api, "New file")
  const tree = panel.getByRole("tree", { name: "Files" })

  await panel.getByRole("button", { name: "New file" }).click()
  const name = tree.getByRole("textbox", { name: "New file name" })
  // Names are checked as they are committed.
  await name.fill("main.tf")
  await name.press("Enter")
  await expect(tree.getByRole("alert")).toContainText("already exists")
  await name.fill("queues.tf")
  await name.press("Enter")

  await expect(tree.getByRole("treeitem", { name: "queues.tf, new" })).toBeVisible()
  await expect(panel.getByRole("tab", { name: "queues.tf" })).toHaveAttribute("aria-selected", "true")
  const editor = panel.getByRole("textbox", { name: "Contents of queues.tf" })
  await editor.click()
  await page.keyboard.insertText('resource "aws_sqs_queue" "jobs" {\n  name = "jobs"\n}\n')

  await panel.getByRole("button", { name: "Review" }).click()
  const review = page.getByRole("dialog", { name: "Review changes" })
  await expect(review.getByRole("region", { name: "queues.tf, new file" })).toContainText("aws_sqs_queue")
  await review.getByRole("button", { name: "Apply changes" }).click()

  await expect(page.getByText("Applied queues.tf")).toBeVisible()
  await expect(app.node("aws_sqs_queue.jobs")).toBeVisible()
  await expect(tree.getByRole("treeitem", { name: "queues.tf, generated" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Apply changes" })).toBeHidden()

  const stored = await api.getWorkflow(wf.id)
  const queue = stored.nodes.find((n) => n.type === "aws_sqs_queue")!
  expect(stored.nodeFiles?.[queue.id]).toBe("queues.tf")
})

test("New module… creates a module folder whose resources become a module node", async ({ app, api, page }) => {
  const { wf, panel } = await openPro(app, api, "New module")

  await panel.getByRole("button", { name: "New module…" }).click()
  const dialog = page.getByRole("dialog", { name: "New module" })
  await dialog.getByRole("textbox", { name: "Module name" }).fill("queues")
  await dialog.getByRole("button", { name: "Create module" }).click()
  await expect(dialog).toBeHidden()

  const tree = panel.getByRole("tree", { name: "Files" })
  for (const file of ["versions.tf", "variables.tf", "outputs.tf", "README.md"]) {
    await expect(tree.getByRole("treeitem", { name: `${file}, new` })).toBeVisible()
  }
  await expect(panel.getByRole("tab", { name: "main.tf" }).nth(1)).toHaveAttribute("aria-selected", "true")
  const editor = panel.getByRole("textbox", { name: "Contents of modules/queues/main.tf" })
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.insertText('\nresource "aws_sqs_queue" "this" {\n  name = "jobs"\n}\n')

  await panel.getByRole("button", { name: "Apply changes" }).click()
  await expect(page.getByText("Applied 6 file changes")).toBeVisible()
  await expect(app.node("module.queues")).toBeVisible()

  const stored = await api.getWorkflow(wf.id)
  const mod = stored.nodes.find((n) => n.kind === "module")!
  const view = await api.request<ModuleView>("GET", `/workflows/${wf.id}/modules/${encodeURIComponent(mod.id)}`)
  expect(view.dir).toBe("modules/queues")
  expect(view.workflow?.nodes.map((n) => `${n.type}.${n.name}`)).toContain("aws_sqs_queue.this")
  expect((await api.terraform(wf.id)).files["main.tf"]).toContain('source = "./modules/queues"')
  const versions = await api.request<{ content: string }>(
    "GET",
    `/workflows/${wf.id}/files/content?path=${encodeURIComponent("modules/queues/versions.tf")}`,
  )
  expect(versions.content).toMatch(/version = ">= \d/)
})

test("renaming a file keeps its nodes; deleting a folder asks first", async ({ app, api, page }) => {
  const wf = await api.createWorkflow("Rename file")
  await api.addNode(wf.id, "aws_s3_bucket")
  await api.request<Workflow>("PUT", `/workflows/${wf.id}/terraform`, {
    changes: {
      "queues.tf": 'resource "aws_sqs_queue" "jobs" {}\n',
      "templates/user_data.tftpl": "#!/bin/sh\n",
    },
  })
  const before = (await api.getWorkflow(wf.id)).nodes.find((n) => n.type === "aws_sqs_queue")!
  await app.gotoHome()
  await app.openWorkflow("Rename file")
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  const tree = panel.getByRole("tree", { name: "Files" })

  // F2 renames the row under the cursor.
  await tree.getByRole("treeitem", { name: /^queues\.tf/ }).click()
  await page.keyboard.press("F2")
  const rename = tree.getByRole("textbox", { name: "Rename queues.tf" })
  await rename.fill("messaging.tf")
  await rename.press("Enter")
  await expect(tree.getByRole("treeitem", { name: "messaging.tf, generated, moved" })).toBeVisible()

  // The context menu deletes a folder after confirming.
  await tree.getByRole("treeitem", { name: "templates" }).click({ button: "right" })
  await page.getByRole("menuitem", { name: "Delete" }).click()
  const confirm = page.getByRole("alertdialog", { name: "Delete folder templates?" })
  await expect(confirm).toContainText("1 file in templates/")
  await confirm.getByRole("button", { name: "Delete" }).click()
  await expect(tree.getByRole("treeitem", { name: "templates" })).toBeHidden()

  await panel.getByRole("button", { name: "Review" }).click()
  const review = page.getByRole("dialog", { name: "Review changes" })
  await expect(review.getByRole("region", { name: "messaging.tf, renamed" })).toContainText("queues.tf →")
  await expect(review.getByRole("region", { name: "templates/user_data.tftpl, deleted" })).toBeVisible()
  await review.getByRole("button", { name: "Apply changes" }).click()
  await expect(page.getByText("Applied 2 file changes")).toBeVisible()

  const after = await api.getWorkflow(wf.id)
  const queue = after.nodes.find((n) => n.type === "aws_sqs_queue")!
  expect(queue.id).toBe(before.id)
  expect(after.nodeFiles?.[queue.id]).toBe("messaging.tf")
  const { files } = await api.request<{ files: WorkflowFile[] }>("GET", `/workflows/${wf.id}/files`)
  expect(files.map((f) => f.path)).not.toContain("templates/user_data.tftpl")
  expect(files.map((f) => f.path)).not.toContain("queues.tf")
})

test("folders, unsupported names and unapplied changes when leaving", async ({ app, api, page }) => {
  const { wf, panel } = await openPro(app, api, "Folders")
  const tree = panel.getByRole("tree", { name: "Files" })

  await panel.getByRole("button", { name: "New folder" }).click()
  const folder = tree.getByRole("textbox", { name: "New folder name" })
  await folder.fill("templates")
  await folder.press("Enter")
  // An empty folder stays in the tree only: nothing to apply yet.
  await expect(tree.getByRole("treeitem", { name: "templates, new folder" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Apply changes" })).toBeHidden()

  await tree.getByRole("treeitem", { name: "templates, new folder" }).click({ button: "right" })
  await page.getByRole("menuitem", { name: "New file…" }).click()
  const name = tree.getByRole("textbox", { name: "New file name" })
  await name.fill("logo.png")
  await name.press("Enter")
  await expect(tree.getByRole("alert")).toContainText("Unsupported file type")
  await name.fill("user_data.tftpl")
  await name.press("Enter")
  await expect(tree.getByRole("treeitem", { name: "user_data.tftpl, new" })).toBeVisible()
  await panel.getByRole("textbox", { name: "Contents of templates/user_data.tftpl" }).click()
  await page.keyboard.insertText("#!/bin/sh\necho hello\n")

  // Switching away asks before dropping unapplied changes.
  await page.getByRole("button", { name: /^Folders Nimbo workflow/ }).click()
  await page.getByRole("menuitem", { name: "Close workflow" }).click()
  const leave = page.getByRole("alertdialog", { name: "Discard unsaved file changes?" })
  await expect(leave).toContainText("user_data.tftpl")
  await leave.getByRole("button", { name: "Cancel" }).click()
  await expect(app.workflowName).toHaveValue("Folders")

  await panel.getByRole("textbox", { name: "Contents of templates/user_data.tftpl" }).click()
  await page.keyboard.press("ControlOrMeta+s")
  await expect(page.getByText("Applied user_data.tftpl")).toBeVisible()
  const saved = await api.request<{ content: string }>(
    "GET",
    `/workflows/${wf.id}/files/content?path=${encodeURIComponent("templates/user_data.tftpl")}`,
  )
  expect(saved.content).toBe("#!/bin/sh\necho hello\n")
})
