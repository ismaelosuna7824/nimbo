import type { WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test } from "../support/fixtures"

// The Code tab of a Docker workspace: files from templates, completion, quick fixes for dk_* checks,
// go to definition and YAML formatting (no containers are started).

test("creates a Dockerfile from a template, completes a stage name and applies it", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Editor files", "docker-node-postgres-redis")
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("button", { name: "New from template…" }).click()
  const dialog = page.getByRole("dialog", { name: "New from template" })
  await dialog.getByRole("option", { name: /^Go Multi-stage/ }).click()
  await dialog.getByRole("textbox", { name: "File" }).fill("worker/Dockerfile")
  await dialog.getByRole("button", { name: "Create file" }).click()
  await expect(dialog).toBeHidden()

  const editor = panel.getByRole("textbox", { name: "Contents of worker/Dockerfile" })
  await expect(editor).toContainText("FROM golang:1.23-alpine AS build")
  await expect(panel.getByRole("treeitem", { name: /^Dockerfile, new/ })).toBeVisible()

  // A stage name after COPY --from=.
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.type("COPY --from=bu")
  const popup = page.locator(".cm-tooltip-autocomplete")
  await expect(popup.getByRole("option").first()).toContainText("build")
  await page.waitForTimeout(150)
  await page.keyboard.press("Enter")
  await page.keyboard.type(" /out/app /app2")
  await expect(editor).toContainText("COPY --from=build /out/app /app2")

  // F12 on the stage name goes to its FROM … AS line (cursor inside "build").
  await editor.locator(".cm-line", { hasText: "COPY --from=build /out/app /app2" }).click()
  await page.keyboard.press("Home")
  for (let i = 0; i < 14; i++) await page.keyboard.press("ArrowRight")
  await page.keyboard.press("F12")
  await expect(editor.locator(".cm-activeLine")).toHaveText("FROM golang:1.23-alpine AS build")

  await panel.getByRole("button", { name: "Apply changes" }).click()
  await expect(panel.getByRole("button", { name: "Apply changes" })).toBeHidden()
  const stored = await api.request<WorkflowFileContent>("GET", `/workflows/${wf.id}/files/content?path=worker/Dockerfile`)
  expect(stored.content).toContain("COPY --from=build /out/app /app2")
})

test("fixes a dk_* problem from the editor and goes to a service from depends_on", async ({ app, api, page }) => {
  await app.openNew(api, "Editor fixes", "docker-node-postgres-redis")
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("treeitem", { name: /^compose\.yaml/ }).click()
  const editor = panel.getByRole("textbox", { name: "Contents of compose.yaml" })
  await expect(editor).toContainText("services:")

  // An untagged image: the dk_latest_tag check offers to pin the catalog's tag. CodeMirror renders the lines in
  // view only: scroll to the redis service at the end of the file first.
  await editor.evaluate((el) => el.closest(".cm-scroller")!.scrollTo(0, 1e6))
  await editor.locator(".cm-line", { hasText: "image: redis:7-alpine" }).click()
  await page.keyboard.press("End")
  // ":7-alpine"
  for (let i = 0; i < 9; i++) await page.keyboard.press("Backspace")
  await expect(editor.locator(".cm-line", { hasText: /^\s+image: redis$/ })).toBeVisible()
  const range = editor.locator("[class*='cm-lintRange']", { hasText: "image: redis" })
  await expect(range).toBeVisible({ timeout: 15_000 })
  await range.hover()
  const tooltip = page.locator(".cm-tooltip-lint")
  await expect(tooltip).toContainText("without a version tag")
  // CodeMirror labels lint actions "Action: <name>.".
  await tooltip.getByRole("button", { name: /^Action: Pin redis:/ }).first().click()
  await expect(editor.locator(".cm-line", { hasText: /^\s+image: redis:\S+$/ })).toBeVisible()
  await expect(editor.locator("[class*='cm-lintRange']", { hasText: "image: redis" })).toHaveCount(0, { timeout: 15_000 })

  // F12 on the db key under depends_on jumps to the db service.
  await page.mouse.move(0, 0)
  await editor.evaluate((el) => el.closest(".cm-scroller")!.scrollTo(0, 0))
  await editor.locator(".cm-line", { hasText: /^\s{6}db:$/ }).click()
  await page.keyboard.press("End")
  await page.keyboard.press("ArrowLeft")
  await page.keyboard.press("F12")
  await expect(editor.locator(".cm-activeLine")).toHaveText("  db:")

  // ⌥⇧F re-indents the YAML and keeps its comments.
  await page.keyboard.press("Alt+Shift+KeyF")
  await expect(editor).toContainText("# Node.js API with PostgreSQL and Redis.")
})

test(".env completes the variables the compose file reads", async ({ app, api, page }) => {
  await app.openNew(api, "Editor env", "docker-node-postgres-redis")
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("treeitem", { name: /^\.env/ }).click()
  const editor = panel.getByRole("textbox", { name: "Contents of .env" })
  await expect(editor).toContainText("POSTGRES_DB=app")
  // Remove POSTGRES_DB, then complete it back.
  await editor.locator(".cm-line", { hasText: "POSTGRES_DB=app" }).click()
  await page.keyboard.press("End")
  await page.keyboard.press("Shift+Home")
  await page.keyboard.press("Backspace")
  await page.keyboard.type("POSTGRES_D")
  const popup = page.locator(".cm-tooltip-autocomplete")
  await expect(popup.getByRole("option").first()).toContainText("POSTGRES_DB")
  await page.waitForTimeout(150)
  await page.keyboard.press("Enter")
  await expect(editor.locator(".cm-line", { hasText: /^POSTGRES_DB=$/ })).toBeVisible()
})

// The active file (compose.yaml, last in the tree) is revealed once; opening or closing another folder later must
// keep the tree where the user scrolled it.
test("opening and closing folders keeps the file tree's scroll position", async ({ app, api, page }) => {
  const wf = await api.createWorkflow("Tree scroll", "docker-node-postgres-redis")
  const changes: Record<string, string> = {}
  for (let s = 1; s <= 8; s++) for (let f = 1; f <= 6; f++) changes[`svc${s}/file${f}.go`] = "package main\n"
  await api.request("PUT", `/workflows/${wf.id}/terraform`, { changes })
  await app.gotoHome()
  await app.openWorkflow("Tree scroll")
  const panel = app.bottomPanel("Code")
  const tree = panel.getByRole("tree", { name: "Files" })
  await expect(panel.getByRole("tab", { name: "compose.yaml" })).toBeVisible()
  await expect.poll(() => tree.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)

  await tree.evaluate((el) => el.scrollTo(0, 0))
  const svc1 = panel.getByRole("treeitem", { name: /^svc1\b/ })
  await expect(svc1).toHaveAttribute("aria-expanded", "true")
  await svc1.click()
  await expect(svc1).toHaveAttribute("aria-expanded", "false")
  await page.waitForTimeout(300)
  expect(await tree.evaluate((el) => el.scrollTop)).toBe(0)

  await svc1.click()
  await expect(svc1).toHaveAttribute("aria-expanded", "true")
  await page.waitForTimeout(300)
  expect(await tree.evaluate((el) => el.scrollTop)).toBe(0)
})
