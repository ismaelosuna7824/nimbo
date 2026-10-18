import { expect, test } from "../support/fixtures"

test("changes the default mode in Settings", async ({ app, api, page }) => {
  await app.gotoHome()
  await page.getByRole("button", { name: "Settings" }).click()

  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible()
  const sections = page.getByRole("navigation", { name: "Settings sections" })
  await expect(page.getByRole("heading", { name: "General", level: 2 })).toBeVisible()

  const mode = page.getByRole("radiogroup", { name: "Mode" })
  await expect(mode.getByRole("radio", { name: /^Guided/ })).toBeChecked()
  await mode.getByRole("radio", { name: /^Pro/ }).click()
  await expect(mode.getByRole("radio", { name: /^Pro/ })).toBeChecked()
  await expect.poll(async () => (await api.settings()).defaultMode).toBe("pro")

  await sections.getByRole("button", { name: "Sandbox & emulators" }).click()
  await expect(page.getByRole("heading", { name: "Sandbox & emulators", level: 2 })).toBeVisible()

  await page.getByRole("button", { name: "Home", exact: true }).click()
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeHidden()
  await expect(page.getByRole("heading", { name: "Nimbo", level: 1 })).toBeVisible()
})
