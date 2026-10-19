import { expect, test } from "../support/fixtures"

// The app project runs the daemon without Docker: nothing is deployed, so every answer is design-based.
test("reviews a VPC and checks reachability to a test instance", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Network checks", "aws-vpc-basics")

  // A subnet with a 0.0.0.0/0 route to the internet gateway is public.
  await app.node("aws_subnet.public").getByText("aws_subnet.public", { exact: true }).click()
  const section = app.inspector.getByTestId("network-section")
  await expect(section.getByTestId("subnet-classification").first()).toHaveText("Public")
  await expect(section.getByTestId("network-basis")).toHaveText("Based on the design")

  // A small web server (port 80) goes into that subnet and gets selected.
  await section.getByTestId("add-test-instance").click()
  await expect.poll(async () => (await api.getWorkflow(wf.id)).nodes.filter((n) => n.type === "aws_instance").length).toBe(1)
  const instance = (await api.getWorkflow(wf.id)).nodes.find((n) => n.type === "aws_instance")!
  await expect(app.inspector.getByText(`aws_instance.${instance.name}`, { exact: true })).toBeVisible()
  await expect(app.inspector.getByTestId("network-placement")).toBeVisible()

  // Problems lists network checks apart from errors, warnings and best practices.
  await app.bottomTab("Problems").click()
  const network = app.bottomPanel("Problems").getByRole("region", { name: "Network" })
  await expect(network).toBeVisible()
  await network.getByRole("button", { name: "Check reachability…" }).click()

  const dialog = page.getByRole("dialog", { name: "Check reachability" })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole("combobox", { name: "From", exact: true })).toHaveText(/Internet/)
  await dialog.getByRole("combobox", { name: "To", exact: true }).click()
  await page.getByRole("option", { name: instance.label || `aws_instance.${instance.name}`, exact: true }).click()
  await expect(dialog.getByRole("combobox", { name: "Protocol", exact: true })).toHaveText(/TCP/)

  // HTTP is open to the world in the test instance's security group.
  await dialog.getByRole("spinbutton", { name: "Port" }).fill("80")
  await dialog.getByTestId("reachability-check").click()
  const verdict = dialog.getByTestId("reachability-verdict")
  await expect(verdict).toHaveAttribute("data-verdict", "reachable")
  await expect(verdict).toContainText("✓ Reachable")
  await expect(dialog.getByTestId("reachability-path").getByTestId("reachability-hop").first()).toBeVisible()

  // SSH is not: the security group blocks it.
  await dialog.getByRole("spinbutton", { name: "Port" }).fill("22")
  await dialog.getByTestId("reachability-check").click()
  await expect(verdict).toHaveAttribute("data-verdict", "unreachable")
  await expect(verdict).toContainText("✗ Not reachable")
  await expect(dialog.locator('[data-testid="reachability-hop"][data-verdict="deny"][data-layer="security-group"]').first()).toBeVisible()
  await expect(dialog.getByTestId("reachability-blockers")).toContainText(/security group/i)

  // Nothing deployed: the live test waits for a deploy.
  await expect(dialog.getByTestId("reachability-live")).toBeDisabled()

  await dialog.getByRole("button", { name: "Close" }).click()
  await expect(dialog).toBeHidden()
})
