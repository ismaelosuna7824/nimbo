import { expect, test } from "../support/fixtures"

test("extracts selected nodes into a module, opens its files and inlines it back", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Modules", "aws-queue-worker")
  await expect(app.node("aws_sqs_queue.jobs")).toBeVisible()

  // Shift-click selects both queues; the toolbar offers the extraction.
  await app.node("aws_sqs_queue.jobs").click()
  await app.node("aws_sqs_queue.dlq").click({ modifiers: ["Shift"] })
  await page.getByRole("button", { name: "Extract 2 to module…" }).click()

  const dialog = page.getByRole("dialog", { name: "Extract to module" })
  await dialog.getByLabel("Module name").fill("queue")
  await expect(dialog.getByText("jobs_arn", { exact: true })).toBeVisible()
  await expect(dialog.getByText(/Adds 2 moved blocks/)).toBeVisible()
  await dialog.getByRole("button", { name: "Extract" }).click()
  await expect(dialog).toBeHidden()
  await expect(app.node("module.queue")).toBeVisible()
  await expect(app.node("aws_sqs_queue.jobs")).toBeHidden()

  const stored = await api.getWorkflow(wf.id)
  expect(stored.other.filter((b) => b.type === "moved")).toHaveLength(2)

  // The read-only module view opens the module's folder in the Code tab.
  await app.node("module.queue").dblclick()
  const overlay = page.getByRole("dialog", { name: "Inside module.queue" })
  await overlay.getByRole("button", { name: "Open files" }).click()
  const code = app.bottomPanel("Code")
  await expect(code.getByRole("textbox", { name: "Contents of modules/queue/main.tf" })).toContainText(
    'resource "aws_sqs_queue" "jobs"',
  )
  await expect(code.getByRole("textbox", { name: "Filter files" })).toHaveValue("modules/queue/")

  // Inline it back from the module view.
  await overlay.getByRole("button", { name: "Inline module" }).click()
  await page.getByRole("alertdialog").getByRole("button", { name: "Inline module" }).click()
  await expect(overlay).toBeHidden()
  await expect(app.node("aws_sqs_queue.jobs")).toBeVisible()
  await expect(app.node("module.queue")).toBeHidden()
  const back = await api.getWorkflow(wf.id)
  expect(back.nodes.some((n) => n.kind === "module")).toBe(false)
})

test("explains why a selection cannot be extracted", async ({ app, api, page }) => {
  await app.openNew(api, "Modules reject", "aws-queue-worker")
  await app.node("aws_sqs_queue.jobs").click({ button: "right" })
  await page.getByRole("menuitem", { name: "Extract to module…" }).click()
  const dialog = page.getByRole("dialog", { name: "Extract to module" })
  await dialog.getByLabel("Module name").fill("Bad Name")
  await expect(dialog.getByText(/Use snake_case/)).toBeVisible()
  await expect(dialog.getByRole("button", { name: "Extract" })).toBeDisabled()
})
