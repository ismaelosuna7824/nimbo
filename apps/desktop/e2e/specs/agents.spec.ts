import type { Workflow } from "../../src/lib/api/types"
import { expect, test } from "../support/fixtures"

// A workflow an AI agent creates through the MCP server (requests marked X-Nimbo-Client: mcp) must not go unnoticed
// while the user looks at another one: the app offers to open it.
test("offers to open a workflow an AI agent created", async ({ app, api, page }) => {
  await app.openNew(api, "What I had open")
  const res = await fetch(`${api.baseURL}/workflows`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Nimbo-Client": "mcp" },
    body: JSON.stringify({ name: "Made by the agent" }),
  })
  expect(res.status).toBe(201)
  const created = (await res.json()) as Workflow

  const toast = page.getByText("Your AI agent created “Made by the agent”")
  await expect(toast).toBeVisible()
  await expect(app.workflowName).toHaveValue("What I had open")
  await page.getByRole("button", { name: "Open", exact: true }).click()
  await expect(app.workflowName).toHaveValue("Made by the agent")
  expect((await api.findWorkflow("Made by the agent"))?.id).toBe(created.id)
})

test("a workflow created in the app itself raises no agent notification", async ({ app, api, page }) => {
  await app.openNew(api, "Still mine")
  await api.createWorkflow("Created by the app")
  await page.waitForTimeout(1500) // longer than the event takes to arrive
  await expect(page.getByText(/Your AI agent created/)).toHaveCount(0)
  await expect(app.workflowName).toHaveValue("Still mine")
})
