import { expect, test } from "../support/fixtures"

// Terraform language intelligence in the Code tab: completion from the catalog's schemas, references,
// diagnostics as you type, hover docs and go to definition.

test("completes resource types, arguments and references, then applies the code", async ({ app, api, page }) => {
  const created = await api.createWorkflow("Intel completion")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Intel completion")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Code")
  const editor = panel.getByRole("textbox", { name: "Contents of main.tf" })
  const popup = page.locator(".cm-tooltip-autocomplete")
  const option = (label: string) => popup.getByRole("option").filter({ has: page.locator(".cm-completionLabel", { hasText: new RegExp(`^${label}$`) }) })
  // CodeMirror ignores Enter for 75 ms after the list changes (so a fast typist doesn't pick by accident).
  const accept = async () => {
    await page.waitForTimeout(150)
    await page.keyboard.press("Enter")
  }

  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.press("Enter")
  await page.keyboard.type('resource "aws_sqs')
  await expect(option("aws_sqs_queue")).toBeVisible()
  await expect(option("aws_sqs_queue")).toContainText("SQS Queue · AWS")
  await expect(option("aws_sqs_queue").getByText("runs locally")).toBeVisible()
  await expect(popup.getByRole("option").first()).toContainText("aws_sqs_queue")

  // Accepting writes the block (name placeholder selected), Tab moves into its body.
  await accept()
  await expect(editor).toContainText('resource "aws_sqs_queue" "queue" {')
  await page.keyboard.press("Tab")
  await page.keyboard.type("visibility")
  await expect(option("visibility_timeout_seconds")).toBeVisible()
  await expect(option("visibility_timeout_seconds")).toContainText("number")
  await accept()
  await page.keyboard.type("30")
  await page.keyboard.press("Escape")
  await expect(editor).toContainText("visibility_timeout_seconds = 30")

  // References: the queue by name, then its exported attributes.
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.press("Enter")
  await page.keyboard.type('output "queue_arn" {')
  await page.keyboard.press("Enter")
  await page.keyboard.type("value = aws_sqs_queue.")
  await expect(option("queue")).toBeVisible()
  await accept()
  await page.keyboard.type(".")
  await expect(popup.getByRole("option").first()).toContainText("arn")
  await accept()
  // The closing brace was inserted with the opening one.
  await page.keyboard.press("Escape")
  await expect(editor).toContainText("value = aws_sqs_queue.queue.arn")

  await panel.getByRole("button", { name: "Apply changes" }).click()
  await expect(panel.getByRole("button", { name: "Apply changes" })).toBeHidden()
  await expect(app.node("aws_sqs_queue.queue")).toBeVisible()
  const wf = await api.getWorkflow(created.id)
  const queue = wf.nodes.find((n) => n.id === "aws_sqs_queue.queue")
  expect(queue?.body.attrs.find((a) => a.name === "visibility_timeout_seconds")?.value.expr).toBe("30")
  expect(wf.outputs.map((o) => o.name)).toContain("queue_arn")
})

test("shows problems as you type, hover docs and goes to definitions", async ({ app, api, page }) => {
  const created = await api.createWorkflow("Intel checks")
  await api.addNode(created.id, "aws_s3_bucket")
  await app.gotoHome()
  await app.openWorkflow("Intel checks")
  await app.setMode("Pro")

  const panel = app.bottomPanel("Code")
  const editor = panel.getByRole("textbox", { name: "Contents of main.tf" })
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.insertText('\nresource "aws_sqs_queue" "jobs" {\n  delay_secs = 5\n  name = var.nope\n}\n')
  await page.keyboard.press("Escape")

  // Engine checks, debounced: unknown argument and undeclared variable as warnings.
  const warning = editor.locator(".cm-lintRange-warning", { hasText: "delay_secs" })
  await expect(warning).toBeVisible()
  await warning.hover()
  await expect(page.locator(".cm-tooltip-lint")).toContainText('Unsupported argument "delay_secs"')
  await expect(editor.locator(".cm-lintRange-warning", { hasText: "var.nope" })).toBeVisible()

  // Syntax errors are errors.
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.insertText('output "broken" {\n  value = \n}\n')
  await expect(editor.locator(".cm-lintRange-error").first()).toBeVisible()
  await page.keyboard.press("ControlOrMeta+z")
  await expect(editor.locator(".cm-lintRange-error")).toHaveCount(0)

  // Hover docs of a resource type.
  await page.mouse.move(0, 0)
  await editor.locator(".cm-line", { hasText: 'resource "aws_sqs_queue" "jobs"' }).getByText('"aws_sqs_queue"').hover()
  const doc = page.locator(".cm-tooltip-hover .cm-tf-doc")
  await expect(doc).toContainText("aws_sqs_queue — SQS Queue")
  await expect(doc).toContainText("runs locally")
  await expect(doc.getByText("Open in the Terraform Registry")).toBeVisible()

  // F12 on local.name_prefix opens locals.tf at its declaration.
  await editor.locator(".cm-line").nth(1).getByText("local", { exact: true }).click()
  await page.keyboard.press("F12")
  await expect(panel.getByRole("tab", { name: "locals.tf" })).toHaveAttribute("aria-selected", "true")
})
