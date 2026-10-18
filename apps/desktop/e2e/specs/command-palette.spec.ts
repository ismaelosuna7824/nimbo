import { expect, test } from "../support/fixtures"

test("opens the command palette with the shortcut and runs commands", async ({ app, api, page }) => {
  await app.openNew(api, "Commands", "aws-static-site")
  const palette = page.getByRole("dialog", { name: "Command palette" })
  const search = palette.getByRole("combobox")

  // Keyboard only: shortcut, filter, Enter runs the highlighted command.
  await page.keyboard.press("ControlOrMeta+k")
  await expect(palette).toBeVisible()
  await search.fill("toggle mode")
  await expect(palette.getByRole("option", { name: "Switch to Pro mode" })).toHaveAttribute("aria-selected", "true")
  await page.keyboard.press("Enter")
  await expect(palette).toBeHidden()
  await expect(page.getByRole("radiogroup", { name: "Experience level" }).getByRole("radio", { name: "Pro" })).toBeChecked()

  await page.keyboard.press("ControlOrMeta+k")
  await search.fill("open panel endpoints")
  await palette.getByRole("option", { name: /^Endpoints — URLs & snippets/ }).click()
  await expect(palette).toBeHidden()
  await expect(app.bottomTab("Endpoints")).toHaveAttribute("aria-selected", "true")

  await page.keyboard.press("ControlOrMeta+k")
  await search.fill("close workflow")
  await palette.getByRole("option", { name: /^Close workflow/ }).click()
  await expect(page.getByRole("heading", { name: "Recent workflows" })).toBeVisible()
})
