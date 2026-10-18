import { expect, test } from "../support/fixtures"

test("Endpoints tab explains what to do before anything is deployed", async ({ app, api }) => {
  await app.openNew(api, "Endpoints empty", "aws-serverless-api")

  await app.bottomTab("Endpoints").click()
  const panel = app.bottomPanel("Endpoints")
  await expect(panel.getByText("Nothing deployed yet")).toBeVisible()
  await expect(panel.getByRole("button", { name: "Deploy" })).toBeEnabled()
  await expect(panel.getByRole("heading", { name: "Connect to your local cloud" })).toBeVisible()
  await expect(panel.getByRole("button", { name: "Start sandbox" })).toBeVisible()
})
