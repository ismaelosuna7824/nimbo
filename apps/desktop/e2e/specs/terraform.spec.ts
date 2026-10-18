import { expect, test } from "../support/fixtures"

test("shows the generated Terraform", async ({ app, api }) => {
  const created = await api.createWorkflow("Terraform view")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Terraform view")

  const panel = app.bottomPanel("Terraform")
  await expect(app.bottomTab("Terraform")).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByText("Read-only")).toBeVisible()
  await expect(panel.getByRole("tab", { name: "main.tf" })).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByRole("textbox")).toContainText('resource "aws_s3_bucket" "bucket"')

  await panel.getByRole("tab", { name: "versions.tf" }).click()
  await expect(panel.getByRole("textbox")).toContainText("required_providers")
})

test("applies a Pro-mode code edit back to the diagram", async ({ app, api, page }) => {
  const created = await api.createWorkflow("Terraform edit")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Terraform edit")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Terraform")
  await panel.getByRole("button", { name: "Edit code" }).click()
  await expect(panel.getByText("Editing")).toBeVisible()
  const editor = panel.getByRole("textbox")
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.press("Enter")
  await page.keyboard.type('resource "aws_sns_topic" "alerts" {\nname = "alerts"\n}')
  await expect(panel.getByRole("tab", { name: /main\.tf/ }).getByLabel("modified")).toBeVisible()

  await panel.getByRole("button", { name: "Apply" }).click()
  await expect(page.getByText("Code applied to the diagram")).toBeVisible()
  await expect(app.node("aws_sns_topic.alerts")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Edit code" })).toBeVisible()

  const wf = await api.getWorkflow(created.id)
  expect(wf.nodes.map((n) => n.id)).toEqual(expect.arrayContaining(["aws_s3_bucket.bucket", "aws_sns_topic.alerts"]))
  expect((await api.terraform(created.id)).files["main.tf"]).toMatch(/resource "aws_sns_topic" "alerts"[\s\S]*name\s*=\s*"alerts"/)
})
