import type { Workflow } from "../../src/lib/api/types"
import { expect, test } from "../support/fixtures"

test("groups best-practice findings in Problems and turns a rule off in Settings", async ({ app, api, page }) => {
  const wf = await api.createWorkflow("Practices")
  const { workflow, nodeId } = await api.addNode(wf.id, "aws_sqs_queue")
  const node = workflow.nodes.find((n) => n.id === nodeId)!
  // A fixed name although the workflow has local.name_prefix.
  const body = { ...node.body, attrs: node.body.attrs.map((a) => (a.name === "name" ? { name: "name", value: { expr: '"jobs"' } } : a)) }
  await api.request<Workflow>("PATCH", `/workflows/${wf.id}/nodes/${nodeId}`, { body })

  await app.gotoHome()
  await app.openWorkflow("Practices")
  await app.bottomTab("Problems").click()
  const panel = app.bottomPanel("Problems")
  const practices = panel.getByRole("region", { name: "Best practices" })
  await expect(practices).toContainText("instead of one derived from local.name_prefix")
  await expect(practices).toContainText('Fix: Set name = "${local.name_prefix}-jobs".')
  await expect(panel.getByRole("region", { name: "Errors" })).toHaveCount(0)

  // Collapsible, and rows link to the node.
  await practices.getByRole("button", { name: /^Best practices/ }).click()
  await expect(practices.getByText("instead of one derived from local.name_prefix")).toBeHidden()
  await practices.getByRole("button", { name: /^Best practices/ }).click()
  await practices.getByRole("button", { name: /instead of one derived from local\.name_prefix/ }).click()
  await expect(app.inspector).toContainText("aws_sqs_queue.queue")

  await practices.getByRole("button", { name: "Configure" }).click()
  await expect(page.getByRole("heading", { name: "Best-practice checks", level: 2 })).toBeVisible()
  await page.getByRole("switch", { name: "Names use local.name_prefix" }).click()
  await expect.poll(async () => (await api.settings()).bestPractices.disabledRules).toEqual(["bp_name_prefix"])
  await page.getByRole("button", { name: "Back to Practices" }).click()
  await expect(panel.getByText("No problems")).toBeVisible()
})
