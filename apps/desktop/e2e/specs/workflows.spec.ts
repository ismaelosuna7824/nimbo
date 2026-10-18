import { expect, test } from "../support/fixtures"

test("creates a workflow from a template", async ({ app, api, page }) => {
  await app.gotoHome()
  await expect(page.getByText("No workflows yet")).toBeVisible()

  await page.getByRole("button", { name: "New workflow" }).click()
  const dialog = page.getByRole("dialog", { name: "New workflow" })
  await dialog.getByRole("button", { name: /^Queue worker/ }).click()
  await dialog.getByRole("textbox", { name: "Name" }).fill("Orders worker")
  await dialog.getByRole("button", { name: "Create from template" }).click()

  await expect(dialog).toBeHidden()
  await expect(app.workflowName).toHaveValue("Orders worker")
  await expect(app.node("aws_sqs_queue.jobs")).toBeVisible()

  const wf = await api.findWorkflow("Orders worker")
  expect(wf?.nodes.map((n) => n.id)).toContain("aws_sqs_queue.jobs")
})
