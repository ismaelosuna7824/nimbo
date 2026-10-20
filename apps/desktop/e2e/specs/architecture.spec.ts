import { readFile } from "node:fs/promises"
import { expect, test } from "../support/fixtures"

/** Width and height from a PNG's IHDR chunk. */
function pngSize(bytes: Buffer) {
  expect(bytes.subarray(1, 4).toString("ascii")).toBe("PNG")
  return { width: bytes.readUInt32BE(16), height: bytes.readUInt32BE(20) }
}

test("draws the read-only architecture diagram and exports a 2× PNG", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Diagram", "aws-serverless-api")
  const views = page.getByRole("radiogroup", { name: "Canvas view" })
  await expect(views.getByRole("radio", { name: "Build" })).toHaveAttribute("data-state", "on")

  // Switch: the diagram replaces the Build canvas with its own cards, groups and actors.
  await views.getByRole("radio", { name: "Architecture" }).click()
  const diagram = page.getByTestId("architecture-view")
  const lambda = diagram.locator('[data-testid="arch-card"][aria-label="Lambda function"]')
  await expect(lambda).toBeVisible()
  await expect(diagram.locator('[data-testid="arch-card"]')).toHaveCount(5)
  await expect(diagram.locator('[data-testid="arch-actor"][aria-label="Users"]')).toBeVisible()
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="cloud"]')).toHaveAttribute("aria-label", "AWS Cloud")
  await expect(diagram.getByTestId("arch-edge")).toHaveCount(5)
  await expect(diagram.getByTestId("arch-header")).toContainText("Diagram")
  await expect(app.node("aws_lambda_function.api")).toHaveCount(0)

  // Selecting a card opens the resource in the inspector.
  await lambda.click()
  await expect(app.inspector.getByText("aws_lambda_function.api", { exact: true })).toBeVisible()

  // The diagram is a view: dragging a card pans the whole diagram (the card keeps its place in it),
  // double-clicking edits nothing, nothing is saved. The card's place is measured relative to the
  // diagram header in card widths, so panning and zooming (the first fit, double-click zoom) don't count.
  const header = diagram.getByTestId("arch-header")
  const offset = async () => {
    const [c, h] = [(await lambda.boundingBox())!, (await header.boundingBox())!]
    return { x: (c.x - h.x) / c.width, y: (c.y - h.y) / c.width }
  }
  const before = await offset()
  const box = (await lambda.boundingBox())!
  const updatedAt = (await api.getWorkflow(wf.id)).updatedAt
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2 + 40, box.y + box.height / 2 + 120, { steps: 10 })
  await page.mouse.up()
  await lambda.dblclick()
  await expect(page.getByRole("dialog")).toHaveCount(0)
  // Double-click zoom animates; poll until the card settles where it was in the diagram.
  await expect
    .poll(async () => {
      const after = await offset()
      return Math.abs(after.x - before.x) + Math.abs(after.y - before.y)
    })
    .toBeLessThanOrEqual(0.02)
  await expect(diagram.getByRole("button", { name: "Reset layout" })).toHaveCount(0)
  await page.waitForTimeout(1200) // longer than the save debounce
  expect((await api.getWorkflow(wf.id)).updatedAt).toBe(updatedAt)

  // The view is remembered per workflow across reloads.
  await page.reload()
  await expect(page.getByTestId("architecture-view").locator('[data-testid="arch-card"][aria-label="Lambda function"]')).toBeVisible()

  // Export: PNG at 2× of the whole diagram.
  await page.getByRole("button", { name: "Export image…" }).click()
  const dialog = page.getByRole("dialog", { name: "Export architecture diagram" })
  await expect(dialog.getByRole("radio", { name: "2×" })).toHaveAttribute("data-state", "on")
  const size = dialog.getByTestId("export-size")
  await expect(size).toHaveText(/\d+ × \d+ px/)
  const width = Number(await size.getAttribute("data-width"))
  const height = Number(await size.getAttribute("data-height"))
  await dialog.getByRole("radio", { name: "1×" }).click()
  await expect(size).toHaveAttribute("data-width", String(width / 2))
  await dialog.getByRole("radio", { name: "2×" }).click()
  const download = page.waitForEvent("download")
  await dialog.getByRole("button", { name: "Export PNG" }).click()
  const file = await download
  expect(file.suggestedFilename()).toBe("diagram-architecture.png")
  const bytes = await readFile((await file.path())!)
  expect(pngSize(bytes)).toEqual({ width, height })
  expect(width).toBeGreaterThan(1200)
  expect(bytes.length).toBeGreaterThan(50_000)

  // Back to Build with the keyboard shortcut: the canvas is unchanged.
  await page.keyboard.press("ControlOrMeta+Shift+A")
  await expect(views.getByRole("radio", { name: "Build" })).toHaveAttribute("data-state", "on")
  await expect(app.node("aws_lambda_function.api")).toBeVisible()
})
