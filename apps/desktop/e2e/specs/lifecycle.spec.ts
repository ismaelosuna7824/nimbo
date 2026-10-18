import { expect, test } from "../support/fixtures"

test("closes, archives and restores a workflow", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Lifecycle", "aws-static-site")

  // Close from the workflow switcher: back to Home, the workflow is still listed.
  await page.getByRole("button", { name: /^Lifecycle Nimbo workflow/ }).click()
  await page.getByRole("menuitem", { name: "Close workflow" }).click()
  await expect(page.getByRole("heading", { name: "Recent workflows" })).toBeVisible()
  await expect(app.workflowCard("Lifecycle")).toBeVisible()

  // Archive from the card menu.
  await page.getByRole("button", { name: "Actions for Lifecycle" }).click()
  await page.getByRole("menuitem", { name: "Archive" }).click()
  await expect(page.getByText("Archived “Lifecycle”")).toBeVisible()
  await expect(page.getByText("No active workflows")).toBeVisible()
  await expect(app.workflowCard("Lifecycle")).toBeHidden()
  await expect.poll(async () => (await api.getWorkflow(wf.id)).archived).toBe(true)

  // Restore from the Archived section.
  await page.getByRole("button", { name: "Archived (1)" }).click()
  await page.getByRole("button", { name: "Restore" }).click()
  await expect(page.getByText("Restored “Lifecycle”")).toBeVisible()
  await expect(app.workflowCard("Lifecycle")).toBeVisible()
  await expect(page.getByRole("button", { name: /^Archived \(/ })).toBeHidden()
  await expect.poll(async () => (await api.getWorkflow(wf.id)).archived ?? false).toBe(false)
})
