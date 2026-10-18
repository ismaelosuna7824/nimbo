import path from "node:path"
import type { Workflow, WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test } from "../support/fixtures"

const fixtureRepo = path.resolve(import.meta.dirname, "../../../../backend/internal/api/testdata/repo")

test("shows the generated Terraform in the file viewer", async ({ app, api }) => {
  const created = await api.createWorkflow("Terraform view")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Terraform view")

  const panel = app.bottomPanel("Code")
  await expect(app.bottomTab("Code")).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByText("Read-only")).toBeVisible()
  await expect(panel.getByRole("tab", { name: "main.tf" })).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByRole("textbox", { name: "Contents of main.tf" })).toContainText('resource "aws_s3_bucket" "bucket"')

  await panel.getByRole("treeitem", { name: /^versions\.tf/ }).click()
  await expect(panel.getByRole("tab", { name: "versions.tf" })).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByRole("textbox", { name: "Contents of versions.tf" })).toContainText("required_providers")
})

test("applies a Pro-mode code edit back to the diagram", async ({ app, api, page }) => {
  const created = await api.createWorkflow("Terraform edit")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Terraform edit")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Code")
  await expect(panel.getByText("Editable")).toBeVisible()
  const editor = panel.getByRole("textbox", { name: "Contents of main.tf" })
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.press("Enter")
  await page.keyboard.type('resource "aws_sns_topic" "alerts" {\nname = "alerts"\n}')
  await expect(panel.getByRole("tab", { name: "main.tf" }).getByLabel("modified")).toBeVisible()

  await panel.getByRole("button", { name: "Apply changes" }).click()
  await expect(page.getByText("Code applied to the diagram")).toBeVisible()
  await expect(app.node("aws_sns_topic.alerts")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Apply changes" })).toBeHidden()

  const wf = await api.getWorkflow(created.id)
  expect(wf.nodes.map((n) => n.id)).toEqual(expect.arrayContaining(["aws_s3_bucket.bucket", "aws_sns_topic.alerts"]))
  expect((await api.terraform(created.id)).files["main.tf"]).toMatch(/resource "aws_sns_topic" "alerts"[\s\S]*name\s*=\s*"alerts"/)
})

test("shows syntax errors at their line and keeps the diagram", async ({ app, api, page }) => {
  const created = await api.createWorkflow("Terraform broken")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Terraform broken")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Code")
  await panel.getByRole("textbox", { name: "Contents of main.tf" }).click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.type('\nresource "aws_sns_topic" "broken" {\nname = \n}')
  await page.keyboard.press("ControlOrMeta+s")

  const problems = panel.getByRole("alert")
  await expect(problems).toContainText("Not applied")
  await expect(problems.getByText(/^main\.tf:\d+$/)).toBeVisible()
  await expect(panel.getByLabel(/^error: Invalid expression/)).toBeVisible()
  expect((await api.getWorkflow(created.id)).nodes.map((n) => n.id)).toEqual(["aws_s3_bucket.bucket"])

  await panel.getByRole("button", { name: "Discard" }).click()
  await expect(panel.getByRole("tab", { name: "main.tf" }).getByLabel("modified")).toBeHidden()
})

test("finds, edits and saves a Lambda source next to modules and tfvars", async ({ app, api, page }) => {
  const [wf] = (
    await api.request<{ workflows: Workflow[] }>("POST", "/import", { dir: fixtureRepo, projects: ["envs/dev"] })
  ).workflows
  await app.gotoHome()
  await app.openWorkflow("dev")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Code")
  const tree = panel.getByRole("tree", { name: "Files" })
  for (const name of ["modules", "network", "README.md", "src", "terraform.tfvars", ".terraform.lock.hcl"]) {
    await expect(tree.getByRole("treeitem", { name: new RegExp(`^${name.replace(/\./g, "\\.")}`) })).toBeVisible()
  }

  // ⌘P picks a file by fuzzy name.
  await page.keyboard.press("ControlOrMeta+p")
  const picker = page.getByRole("dialog", { name: "Go to file" })
  await picker.getByPlaceholder("Go to file…").fill("handler")
  await page.keyboard.press("Enter")
  await expect(picker).toBeHidden()
  await expect(panel.getByRole("tab", { name: "handler.py" })).toHaveAttribute("aria-selected", "true")
  const editor = panel.getByRole("textbox", { name: "Contents of src/handler.py" })
  await expect(editor).toContainText("def handle")

  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.type("# edited in Nimbo\n")
  const tab = panel.getByRole("tab", { name: "handler.py" })
  await expect(tab.getByLabel("modified")).toBeVisible()

  // ⌘W asks before closing a tab with unsaved changes.
  await page.keyboard.press("ControlOrMeta+w")
  const confirm = page.getByRole("alertdialog", { name: "Discard changes to handler.py?" })
  await confirm.getByRole("button", { name: "Cancel" }).click()
  await expect(tab).toBeVisible()
  await expect(app.workflowName).toHaveValue("dev")

  await editor.click()
  await page.keyboard.press("ControlOrMeta+s")
  await expect(page.getByText("Saved handler.py")).toBeVisible()
  await expect(tab.getByLabel("modified")).toBeHidden()
  const saved = await api.request<WorkflowFileContent>(
    "GET",
    `/workflows/${wf.id}/files/content?path=${encodeURIComponent("src/handler.py")}`,
  )
  expect(saved.content).toContain("# edited in Nimbo")
})
