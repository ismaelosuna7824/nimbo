import { readdir } from "node:fs/promises"
import { expect, test } from "../support/fixtures"

test("exports a workflow to a folder and imports it back", async ({ app, api, page }, testInfo) => {
  const original = await api.createWorkflow("Export source", "aws-queue-worker")
  const dir = testInfo.outputPath("exported")
  const written = await api.exportTo(original.id, dir)
  expect(written.files).toEqual(expect.arrayContaining(["main.tf", "variables.tf"]))
  expect(await readdir(dir)).toContain("main.tf")

  await app.gotoHome()
  await page.getByRole("button", { name: "Import Terraform" }).click()
  const dialog = page.getByRole("dialog", { name: "Import Terraform" })
  const chooser = page.waitForEvent("filechooser")
  await dialog.getByRole("button", { name: "Choose folder…" }).click()
  await (await chooser).setFiles(dir)

  // Review step: one project, renamed before importing.
  await expect(dialog.getByText("Choose which projects to import.")).toBeVisible()
  const name = dialog.getByRole("textbox", { name: /^Workflow name for / })
  await expect(name).toHaveCount(1)
  await name.fill("Imported worker")
  await dialog.getByRole("button", { name: "Import", exact: true }).click()

  await expect(dialog).toBeHidden()
  await expect(page.getByText("Imported “Imported worker”")).toBeVisible()
  await expect(app.workflowName).toHaveValue("Imported worker")
  await expect(app.node("aws_sqs_queue.jobs")).toBeVisible()
  await expect(app.node("aws_lambda_function.worker")).toBeVisible()

  const imported = await api.findWorkflow("Imported worker")
  expect(imported?.id).not.toBe(original.id)
  expect(imported?.nodes.map((n) => n.id).sort()).toEqual(original.nodes.map((n) => n.id).sort())
})
