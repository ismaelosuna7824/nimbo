import type { Workflow, WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test, type NimboApi } from "../support/fixtures"

// Environments of a Docker workspace (no containers): compose.<env>.yaml overrides plus .env.<env>, created, edited,
// compared and switched from the UI; the base compose file never changes.

const fileText = (api: NimboApi, id: string, path: string) =>
  api
    .request<WorkflowFileContent>("GET", `/workflows/${id}/files/content?path=${encodeURIComponent(path)}`)
    .then((f) => f.content)
    .catch(() => "")

test("Docker environments: create, edit, set a variable, compare, switch and reset an override", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Shop envs", "docker-node-postgres-redis")
  const base = await fileText(api, wf.id, "compose.yaml")
  await page.getByRole("tab", { name: "Environments" }).click()

  // No environments yet: one click makes dev and prod (compose.dev.yaml, compose.prod.yaml).
  await page.getByTestId("env-create-defaults").click()
  await expect(page.getByTestId("env-row-prod")).toBeVisible()
  await expect(page.getByTestId("env-row-dev")).toBeVisible()
  expect(await fileText(api, wf.id, "compose.prod.yaml")).toContain("services:")

  // prod's api: 2 replicas and a memory limit, written to compose.prod.yaml only.
  await page.getByTestId("env-row-prod").getByRole("button").first().click()
  await page.getByTestId("env-edit-tab").click()
  await page.getByTestId("env-setting-replicas").fill("2")
  await page.getByTestId("env-setting-memory").fill("384M")
  await page.getByTestId("env-save").click()
  await expect.poll(() => fileText(api, wf.id, "compose.prod.yaml")).toContain("replicas: 2")
  expect(await fileText(api, wf.id, "compose.prod.yaml")).toContain("memory: 384M")

  // A variable of prod: .env.prod is created with it; .env's values show as inherited.
  await page.getByTestId("env-variables-tab").click()
  await expect(page.getByTestId("env-var-POSTGRES_DB")).toContainText(".env")
  await page.getByTestId("env-var-new-key").fill("LOG_LEVEL")
  await page.getByTestId("env-var-new-value").fill("warn")
  await page.getByTestId("env-var-add").click()
  await expect(page.getByTestId("env-var-LOG_LEVEL")).toHaveAttribute("data-overridden")
  expect(await fileText(api, wf.id, ".env.prod")).toBe("LOG_LEVEL=warn\n")

  // The matrix: prod differs from the base and dev.
  await page.getByTestId("env-compare-tab").click()
  const replicas = page.getByTestId("env-compare-cell-service:api-replicas-prod")
  await expect(replicas).toHaveText("2")
  await expect(replicas).toHaveAttribute("data-differs")
  await expect(page.getByTestId("env-compare-cell-service:api-replicas-dev")).toHaveText("1")

  // Switch on the canvas: the graph and the inspector follow prod and mark what its override sets.
  await page.getByTestId("docker-canvas").getByTestId("environment").click()
  await page.getByTestId("environment-prod").click()
  await expect.poll(async () => (await api.request<Workflow>("GET", `/workflows/${wf.id}`)).compose?.environment).toBe("prod")
  await page.locator('[data-testid="docker-service"][data-name="api"]').click()
  await expect(page.locator("#svc-replicas")).toHaveValue("2")
  await expect(page.getByTestId("override-deploy.replicas")).toContainText("prod override")

  // Reset to base: the override goes from compose.prod.yaml, the base file is untouched.
  await page.getByTestId("override-reset-deploy.replicas").click()
  await expect.poll(() => fileText(api, wf.id, "compose.prod.yaml")).not.toContain("replicas:")
  await expect(page.locator("#svc-replicas")).toHaveValue("1")
  await expect(page.getByTestId("override-deploy.replicas")).toHaveCount(0)
  expect(await fileText(api, wf.id, "compose.yaml")).toBe(base)
})
