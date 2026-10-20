import { expect, test } from "../support/fixtures"

test("adds a resource by clicking it in the palette", async ({ app, api }) => {
  const wf = await app.openNew(api, "Palette")
  await expect(app.canvas.getByRole("group")).toHaveCount(0)

  await app.addFromPalette("dynamo", "DynamoDB Table", "aws_dynamodb_table")

  await expect(app.node("aws_dynamodb_table.table")).toBeVisible()
  await expect(app.inspector.getByText("aws_dynamodb_table.table")).toBeVisible()
  await expect.poll(async () => (await api.getWorkflow(wf.id)).nodes.map((n) => n.id)).toContain("aws_dynamodb_table.table")
})

// Every category list virtualizes against the palette's one scroller; a list
// mounting used to scroll it back to the top.
test("expanding a category keeps the palette scrolled where it was", async ({ app, api, page }) => {
  await app.openNew(api, "Palette scroll")
  const other = page.getByRole("button", { name: "Other", exact: true })
  await other.scrollIntoViewIfNeeded()
  const before = await other.boundingBox()

  await other.click()
  await expect(other).toHaveAttribute("data-state", "open")
  await expect(other.locator("xpath=../..").getByRole("listitem").first()).toBeVisible()

  const after = await other.boundingBox()
  expect(Math.abs(after!.y - before!.y)).toBeLessThanOrEqual(4)
})
