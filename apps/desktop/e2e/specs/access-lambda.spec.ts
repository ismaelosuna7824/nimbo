import type { Locator, Page } from "@playwright/test"
import type { ChallengeResult, Workflow } from "../../src/lib/api/types"
import { type App, expect, type NimboApi, test } from "../support/fixtures"

// Solves the aws-lambda-least-privilege challenge without writing code: a log group from the palette, the
// function↔log group recipe, the role dragged onto the table (Read) and the log group (Write), and the admin "*"
// statement deleted in the role policy's Access section. The static goals must pass; the deploy goal deploys to the
// local AWS emulator, so it is only asserted when the Docker suite runs (E2E_DOCKER=1).

const docker = process.env.E2E_DOCKER === "1"
const NAME = "Lambda least privilege"
const STATIC_GOALS = ["valid", "no-admin-policy", "scoped-to-table", "read-action", "no-service-wildcard", "own-logs-only", "log-retention"]

/** Waits until the cards stop moving (auto-layout and save echoes nudge them). */
async function stable(...cards: Locator[]) {
  let previous = ""
  await expect
    .poll(
      async () => {
        const current = JSON.stringify(await Promise.all(cards.map((c) => c.boundingBox())))
        const ok = current === previous && !current.includes("null")
        previous = current
        return ok
      },
      { intervals: [150] },
    )
    .toBe(true)
}

/** Drags a whole card (not a handle) and drops it onto another card. */
async function dropCard(app: App, page: Page, from: string, to: string) {
  const src = app.node(from)
  const dst = app.node(to)
  await stable(src, dst)
  const a = (await src.boundingBox())!
  const b = (await dst.boundingBox())!
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await page.mouse.down()
  await page.mouse.move(a.x + a.width / 2 + 20, a.y + a.height / 2 + 20, { steps: 4 })
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 16 })
  await page.mouse.up()
}

function policySource(wf: Workflow): string {
  const pol = wf.nodes.find((n) => n.id === "aws_iam_role_policy.reader")
  return pol?.body.attrs.find((a) => a.name === "policy")?.value.expr ?? ""
}

async function challengeState(api: NimboApi, id: string) {
  return api.request<{ last?: ChallengeResult }>("GET", `/workflows/${id}/challenge`)
}

test("solves the least-privilege Lambda challenge with the access editors", async ({ app, api, page }) => {
  test.setTimeout(docker ? 15 * 60_000 : 5 * 60_000)

  const wf = await api.request<Workflow>("POST", "/learn/challenges/aws-lambda-least-privilege/start", { name: NAME })
  await app.gotoHome()
  await app.openWorkflow(NAME)
  await expect(app.node("aws_iam_role.reader")).toBeVisible()
  await expect(app.saved).toBeVisible()

  // 1. A CloudWatch log group from the palette, connected to the function.
  await app.addFromPalette("log group", "CloudWatch Log Group", "aws_cloudwatch_log_group")
  await expect(app.node("aws_cloudwatch_log_group.logs")).toBeVisible()
  await expect(app.saved).toBeVisible()
  // The new card lands next to the others, possibly under the canvas header: show every card before dragging.
  await page.locator(".react-flow__controls-fitview").click()
  await app.connect("aws_lambda_function.reader", "aws_cloudwatch_log_group.logs")
  const dialog = page.getByRole("dialog", { name: "How should these connect?" })
  await expect(dialog).toBeVisible({ timeout: 15_000 })
  await dialog.getByRole("button", { name: /^Send the function's logs to this log group/ }).click({ timeout: 15_000 })
  await expect(dialog).toBeHidden()
  await expect
    .poll(async () => {
      const g = (await api.getWorkflow(wf.id)).nodes.find((n) => n.id === "aws_cloudwatch_log_group.logs")
      return g?.body.attrs.find((a) => a.name === "name")?.value.expr
    })
    .toBe('"/aws/lambda/${aws_lambda_function.reader.function_name}"')

  // 2. The role dropped onto the table: Read. The card goes back where it was.
  const roleBefore = (await api.getWorkflow(wf.id)).nodes.find((n) => n.id === "aws_iam_role.reader")!.position
  await dropCard(app, page, "aws_iam_role.reader", "aws_dynamodb_table.orders")
  await expect(dialog.getByTestId("access-level-picker")).toBeVisible({ timeout: 15_000 })
  await expect(dialog.getByTestId("access-level-read")).toContainText("dynamodb:GetItem")
  await dialog.getByTestId("access-level-read").click()
  await expect(dialog).toBeHidden()
  await expect.poll(async () => policySource(await api.getWorkflow(wf.id))).toContain("dynamodb:GetItem")
  expect((await api.getWorkflow(wf.id)).nodes.find((n) => n.id === "aws_iam_role.reader")!.position).toEqual(roleBefore)

  // 3. The role dropped onto the log group: Write (create streams and put events in that group only).
  await dropCard(app, page, "aws_iam_role.reader", "aws_cloudwatch_log_group.logs")
  await expect(dialog.getByTestId("access-level-write")).toContainText("logs:PutLogEvents", { timeout: 15_000 })
  await dialog.getByTestId("access-level-write").click()
  await expect(dialog).toBeHidden()
  await expect.poll(async () => policySource(await api.getWorkflow(wf.id))).toContain('"${aws_cloudwatch_log_group.logs.arn}:*"')

  // 4. Delete the admin statement in the role policy's Access section.
  await app.node("aws_iam_role_policy.reader").click()
  const access = app.inspector.getByTestId("access-section")
  // The starter's statement: every action ("*") on every resource ("*").
  await expect(access.getByTestId("aws-statement-0")).toContainText("every action of every service")
  await expect(access.getByTestId("access-warnings").first()).toContainText("administrator access")
  await access.getByTestId("aws-statement-delete-0").click()
  await access.getByTestId("aws-save").click()
  await expect.poll(async () => policySource(await api.getWorkflow(wf.id))).not.toContain('Action   = "*"')
  await expect(access.getByTestId("aws-statement-0")).toContainText("dynamodb:GetItem")
  await expect(access.getByText("Least privilege: no wildcards or broad grants.")).toBeVisible()
  await expect(app.saved).toBeVisible()

  // 5. Check the challenge.
  await app.bottomTab("Challenge").click()
  const tab = page.getByTestId("challenge-tab")
  await tab.getByTestId("challenge-check").click()
  const goals = tab.getByTestId("challenge-goals")
  const goal = (label: string) => goals.locator("li[data-status]").filter({ hasText: label })

  if (docker) {
    await expect
      .poll(async () => (await challengeState(api, wf.id)).last?.status, { timeout: 14 * 60_000, intervals: [2000] })
      .toBe("passed")
    await expect(goal("The function deploys to the local AWS emulator")).toHaveAttribute("data-status", "passed")
  } else {
    // Without Docker only the static goals are graded; stop the check before (or while) it deploys.
    await expect
      .poll(
        async () => {
          const last = (await challengeState(api, wf.id)).last
          return STATIC_GOALS.every((id) => {
            const s = last?.goals.find((g) => g.id === id)?.status
            return s === "passed" || s === "failed" || s === "error"
          })
        },
        { timeout: 120_000, intervals: [1000] },
      )
      .toBe(true)
    if ((await challengeState(api, wf.id)).last?.status === "running") {
      await tab.getByRole("button", { name: "Stop" }).click()
    }
    // Let the stopped check settle (a deploy it started finishes or rolls back) before the sandbox is removed below.
    await expect
      .poll(async () => (await challengeState(api, wf.id)).last?.status, { timeout: 120_000, intervals: [1000] })
      .not.toBe("running")
  }
  const last = (await challengeState(api, wf.id)).last!
  for (const id of STATIC_GOALS) {
    const g = last.goals.find((x) => x.id === id)
    expect(g?.status, `${id}: ${g?.detail}`).toBe("passed")
  }
  await expect(goal("No policy grants every action on every resource")).toHaveAttribute("data-status", "passed")
  await expect(goal("The function's log group has a retention period")).toHaveAttribute("data-status", "passed")
  // Deleting the workflow removes its emulator sandbox (containers, network) so nothing outlives the test.
  await api.request("DELETE", `/workflows/${wf.id}`)
})
