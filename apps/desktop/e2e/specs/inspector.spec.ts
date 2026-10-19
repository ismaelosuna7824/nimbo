import { expect, test } from "../support/fixtures"

test("edits a field in Guided mode", async ({ app, api }) => {
  const created = await api.createWorkflow("Inspector guided")
  await api.addNode(created.id, "aws_lambda_function")
  await app.gotoHome()
  await app.openWorkflow("Inspector guided")

  await app.node("aws_lambda_function.function").click()
  await expect(app.inspector.getByText("aws_lambda_function.function")).toBeVisible()
  await expect(app.inspector.getByRole("heading", { name: "Settings" })).toBeVisible()
  // Guided mode shows the essentials only, behind "More options".
  await expect(app.inspector.getByRole("button", { name: /^More options/ })).toBeVisible()
  await expect(app.inspector.getByRole("textbox", { name: "Filter attributes…" })).toBeHidden()

  await app.inspector.getByRole("spinbutton", { name: "e.g. 128" }).fill("512")
  await expect(app.saved).toBeVisible()
  await expect.poll(async () => (await api.terraform(created.id)).files["main.tf"]).toMatch(/memory_size\s*=\s*512/)
})

test("edits a field in Pro mode", async ({ app, api }) => {
  const created = await api.createWorkflow("Inspector pro")
  await api.addNode(created.id, "aws_sqs_queue")
  await app.gotoHome()
  await app.openWorkflow("Inspector pro")
  await app.setMode("Pro")

  await app.node("aws_sqs_queue.queue").click()
  await app.inspector.getByRole("textbox", { name: "Filter attributes…" }).fill("visibility_timeout")
  const field = app.inspector.getByRole("spinbutton", { name: "e.g. 30" })
  await expect(field).toHaveCount(1)
  await field.fill("45")

  await expect(app.saved).toBeVisible()
  await expect
    .poll(async () => (await api.terraform(created.id)).files["main.tf"])
    .toMatch(/visibility_timeout_seconds\s*=\s*45/)
})

test("moves a node to another file in Pro mode", async ({ app, api }) => {
  const created = await api.createWorkflow("Inspector file")
  await api.addNode(created.id, "aws_sqs_queue")
  await app.gotoHome()
  await app.openWorkflow("Inspector file")
  await app.setMode("Pro")

  await app.node("aws_sqs_queue.queue").click()
  const file = app.inspector.getByRole("combobox", { name: "File" })
  await expect(file).toHaveAttribute("placeholder", "main.tf (automatic)")
  await file.fill("queues.tf")
  await file.press("Enter")

  await expect.poll(async () => (await api.terraform(created.id)).files["queues.tf"]).toMatch(/resource "aws_sqs_queue" "queue"/)
  await expect(app.inspector.getByText("Kept in this file on every export.")).toBeVisible()

  await app.inspector.getByRole("button", { name: "Use the recommended file" }).click()
  await expect.poll(async () => (await api.terraform(created.id)).files["main.tf"]).toMatch(/resource "aws_sqs_queue" "queue"/)
})
