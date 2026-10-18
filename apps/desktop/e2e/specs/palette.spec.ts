import { expect, test } from "../support/fixtures"

test("adds a resource by clicking it in the palette", async ({ app, api }) => {
  const wf = await app.openNew(api, "Palette")
  await expect(app.canvas.getByRole("group")).toHaveCount(0)

  await app.addFromPalette("dynamo", "DynamoDB Table", "aws_dynamodb_table")

  await expect(app.node("aws_dynamodb_table.table")).toBeVisible()
  await expect(app.inspector.getByText("aws_dynamodb_table.table")).toBeVisible()
  await expect.poll(async () => (await api.getWorkflow(wf.id)).nodes.map((n) => n.id)).toContain("aws_dynamodb_table.table")
})
