import { execFileSync } from "node:child_process"
import { expect, test } from "../support/fixtures"

const NAME = "Sandbox journey"

/** Every container, network and volume of a workflow's sandbox carries its id in the name. */
function leftovers(workflowId: string) {
  const list = (args: string[], format: string) =>
    execFileSync("docker", [...args, "--filter", `name=${workflowId}`, "--format", format], { encoding: "utf8" })
      .split("\n")
      .filter(Boolean)
  return [...list(["ps", "-a"], "{{.Names}}"), ...list(["network", "ls"], "{{.Name}}"), ...list(["volume", "ls"], "{{.Name}}")]
}

test.beforeAll(() => {
  // Fail fast with a clear message rather than timing out on a deploy.
  execFileSync("docker", ["info", "--format", "{{.ServerVersion}}"], { stdio: "pipe" })
})

let workflowId: string | undefined

test.afterEach(async ({ api }) => {
  // Safety net when the journey fails midway: deleting the workflow removes its sandbox.
  if (workflowId && (await api.listWorkflows()).some((w) => w.id === workflowId)) {
    await api.request("DELETE", `/workflows/${workflowId}`)
  }
  if (workflowId) expect(leftovers(workflowId)).toEqual([])
})

test("deploys to the sandbox, tries the API, load-tests it and cleans up", async ({ app, api, page }) => {
  const wf = await app.openNew(api, NAME, "aws-serverless-api")
  workflowId = wf.id

  // Deploy and wait for the Endpoints tab to list the HTTP API (first runs may pull images).
  await page.getByRole("button", { name: "Deploy", exact: true }).click()
  await app.bottomTab("Endpoints").click()
  const endpoints = app.bottomPanel("Endpoints")
  const apis = endpoints.getByRole("region", { name: "APIs" })
  await expect(apis.getByRole("heading", { name: "items-api" })).toBeVisible({ timeout: 10 * 60_000 })
  expect(leftovers(wf.id).length).toBeGreaterThan(0)

  // Try it: create an item, then read it back.
  await apis.getByRole("button", { name: "Try", exact: true }).click()
  await apis.getByRole("combobox", { name: "Method" }).click()
  await page.getByRole("option", { name: "POST" }).click()
  await apis.getByRole("textbox", { name: "Request body" }).fill('{"name":"from-e2e"}')
  await apis.getByRole("button", { name: "Send" }).click()
  await expect(apis.getByText("201 Created")).toBeVisible({ timeout: 60_000 })

  await apis.getByRole("combobox", { name: "Method" }).click()
  await page.getByRole("option", { name: "GET" }).click()
  await apis.getByRole("button", { name: "Send" }).click()
  await expect(apis.getByText("200 OK")).toBeVisible({ timeout: 60_000 })
  await expect(apis.getByText(/"name":\s*"from-e2e"/)).toBeVisible()

  // Smoke load test against the API (10 s at 5 req/s).
  await page.getByRole("main").getByRole("button", { name: "Load test", exact: true }).click()
  const load = app.bottomPanel("Load test")
  await expect(load.getByRole("radiogroup", { name: "Targets" }).getByRole("radio", { checked: true })).toHaveAccessibleName(
    /^GET \/ http:\/\/127\.0\.0\.1:\d+\/execute-api\//,
  )
  await load.getByRole("button", { name: "Next" }).click()
  await expect(load.getByRole("button", { name: /^Smoke test/ })).toHaveAttribute("aria-pressed", "true")
  await load.getByRole("button", { name: "Next" }).click()
  await load.getByRole("button", { name: "Start test" }).click()
  await expect(load.getByRole("tab", { name: "History & compare 1" })).toBeVisible()
  await expect(load.getByRole("row", { name: /^errors < 1\.00% .* passed$/ })).toBeVisible({ timeout: 2 * 60_000 })
  await expect(load.getByRole("row", { name: /^p95 < 500 ms .* passed$/ })).toBeVisible()
  await expect(load.getByText("Passed", { exact: true })).toBeVisible()

  // Destroy what was deployed.
  // Destroy sits in the Deploy menu, apart from Deploy itself.
  await page.getByRole("button", { name: "More deploy actions" }).click()
  await page.getByRole("menuitem", { name: /^Destroy everything in the sandbox/ }).click()
  const confirm = page.getByRole("alertdialog", { name: `Destroy everything in “${NAME}”?` })
  await confirm.getByRole("button", { name: "Destroy" }).click()
  await app.bottomTab("Endpoints").click()
  await expect(endpoints.getByText("Nothing deployed yet")).toBeVisible({ timeout: 5 * 60_000 })

  // Delete the workflow from Home: its sandbox containers, network and volumes go with it.
  await page.getByRole("button", { name: new RegExp(`^${NAME} Nimbo workflow`) }).click()
  await page.getByRole("menuitem", { name: "Close workflow" }).click()
  await page.getByRole("button", { name: `Actions for ${NAME}` }).click()
  await page.getByRole("menuitem", { name: "Delete…" }).click()
  await page.getByRole("alertdialog", { name: `Delete “${NAME}”?` }).getByRole("button", { name: "Delete" }).click()
  await expect(page.getByText("No workflows yet")).toBeVisible()
  await expect.poll(() => leftovers(wf.id), { timeout: 60_000 }).toEqual([])
})
