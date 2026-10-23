import type { Workflow, WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test, type NimboApi } from "../support/fixtures"

// The Environments tab of a Kubernetes workspace (no cluster): duplicate an overlay, edit it without YAML, compare,
// switch, rename and delete.

const fileText = (api: NimboApi, id: string, path: string) =>
  api.request<WorkflowFileContent>("GET", `/workflows/${id}/files/content?path=${encodeURIComponent(path)}`).then((f) => f.content)

const names = (api: NimboApi, id: string) =>
  api.request<Workflow>("GET", `/workflows/${id}`).then((w) => w.environments.map((e) => e.name))

test("Kustomize environments: duplicate, edit, compare, switch, rename and delete", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Hello envs", "k8s-kustomize-app")
  await page.getByRole("tab", { name: "Environments" }).click()
  await expect(page.getByTestId("env-row-prod")).toBeVisible()

  // Duplicate prod as staging: the overlay is copied with its names rewritten.
  await page.getByTestId("env-action-prod").click()
  await page.getByTestId("env-duplicate-prod").click()
  await page.getByTestId("env-new-name").fill("staging")
  await page.getByTestId("env-new-submit").click()
  await expect(page.getByTestId("env-row-staging")).toBeVisible()
  expect(await fileText(api, wf.id, "overlays/staging/kustomization.yaml")).toContain("namespace: hello-staging")

  // Edit staging's replicas and image tag: written to its overlay only.
  await page.getByTestId("env-row-staging").click()
  await page.getByTestId("env-edit-tab").click()
  await page.getByTestId("env-setting-replicas").fill("3")
  await page.getByTestId("env-setting-image:nginx").fill("1.29-alpine")
  await page.getByTestId("env-save").click()
  await expect.poll(() => fileText(api, wf.id, "overlays/staging/kustomization.yaml")).toContain("newTag: 1.29-alpine")
  expect(await fileText(api, wf.id, "overlays/staging/kustomization.yaml")).toContain("count: 3")
  expect(await fileText(api, wf.id, "overlays/prod/kustomization.yaml")).toContain("count: 2")

  // The matrix shows staging next to the others, highlighted where it differs from the base.
  await page.getByTestId("env-compare-tab").click()
  const replicas = page.getByTestId("env-compare-cell-Deployment/web-replicas-staging")
  await expect(replicas).toHaveText("3")
  await expect(replicas).toHaveAttribute("data-differs")
  await expect(page.getByTestId("env-compare-cell-Deployment/web-replicas-base")).toHaveText("1")

  // Switch, then rename the active environment: the active one follows.
  await page.getByTestId("env-action-staging").click()
  await page.getByTestId("env-switch-staging").click()
  await expect.poll(async () => (await api.request<Workflow>("GET", `/workflows/${wf.id}`)).kubernetes?.environment).toBe("staging")
  await expect(page.getByTestId("env-row-staging")).toContainText("active")
  await page.getByTestId("env-action-staging").click()
  await page.getByTestId("env-rename-staging").click()
  await page.getByTestId("env-rename-input").fill("qa")
  await page.getByTestId("env-rename-submit").click()
  await expect(page.getByTestId("env-row-qa")).toBeVisible()
  await expect.poll(async () => (await api.request<Workflow>("GET", `/workflows/${wf.id}`)).kubernetes?.environment).toBe("qa")

  // Delete it: its overlay goes and the base is active again.
  await page.getByTestId("env-action-qa").click()
  await page.getByTestId("env-delete-qa").click()
  await page.getByTestId("env-delete-confirm").click()
  await expect(page.getByTestId("env-row-qa")).toHaveCount(0)
  await expect.poll(() => names(api, wf.id)).toEqual(["dev", "prod"])
  expect((await api.request<Workflow>("GET", `/workflows/${wf.id}`)).kubernetes?.environment ?? "").toBe("")
})
