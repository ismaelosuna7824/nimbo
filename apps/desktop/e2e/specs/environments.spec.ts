import { expect, test } from "../support/fixtures"

test("creates environments and switches the active one", async ({ app, api }) => {
  const wf = await app.openNew(api, "Environments", "aws-serverless-api")

  await app.bottomTab("Variables & Environments").click()
  const panel = app.bottomPanel("Variables & Environments")
  // project, environment, aws_region, lambda_memory_mb, log_retention_days
  await expect(panel.getByRole("tab", { name: "Variables 5" })).toBeVisible()
  await panel.getByRole("tab", { name: "Environments 0" }).click()
  await expect(panel.getByText("No environments yet")).toBeVisible()

  await panel.getByRole("button", { name: "Create environment" }).click()
  const list = panel.getByRole("listbox", { name: "Environments" })
  await expect(list.getByRole("option", { name: "dev" })).toHaveAttribute("aria-selected", "true")
  await expect(panel.getByRole("textbox", { name: "Environment name" })).toHaveValue("dev")
  await expect(panel.getByText("Active", { exact: true })).toBeVisible()

  await panel.getByRole("button", { name: "New environment" }).click()
  await expect(list.getByRole("option", { name: "staging" })).toHaveAttribute("aria-selected", "true")
  await panel.getByRole("button", { name: "Set active" }).click()
  await expect(panel.getByText("Active", { exact: true })).toBeVisible()
  await expect(panel.getByRole("tab", { name: "Environments 2" })).toBeVisible()

  await expect(app.saved).toBeVisible()
  await expect
    .poll(async () => {
      const saved = await api.getWorkflow(wf.id)
      return { names: saved.environments.map((e) => e.name), active: saved.activeEnvironment }
    })
    .toEqual({ names: ["dev", "staging"], active: "staging" })
})
