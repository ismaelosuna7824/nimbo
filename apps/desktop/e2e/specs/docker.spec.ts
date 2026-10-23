import type { Locator } from "@playwright/test"
import type { WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test, type App, type NimboApi } from "../support/fixtures"

// Docker workspaces without containers: everything here is the compose file, its
// derived graph and the UI around it (CI has no images; nothing is started).

const service = (app: App, name: string): Locator => app.page.locator(`[data-testid="docker-service"][data-name="${name}"]`)

const composeText = (api: NimboApi, id: string) =>
  api.request<WorkflowFileContent>("GET", `/workflows/${id}/files/content?path=compose.yaml`).then((f) => f.content)

/** Drags from one service card's output handle to another's input handle, once both stopped moving. */
async function connectServices(app: App, from: string, to: string) {
  const source = service(app, from).locator(".react-flow__handle.source")
  const target = service(app, to).locator(".react-flow__handle.target")
  let previous = ""
  await expect
    .poll(
      async () => {
        const current = JSON.stringify([await source.boundingBox(), await target.boundingBox()])
        const stable = current === previous && !current.includes("null")
        previous = current
        return stable
      },
      { intervals: [150] },
    )
    .toBe(true)
  const a = (await source.boundingBox())!
  const b = (await target.boundingBox())!
  await app.page.mouse.move(a.x + a.width / 2, a.y + a.height / 2)
  await app.page.mouse.down()
  await app.page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 12 })
  await app.page.mouse.up()
}

test("creates a Docker workflow from the Node + PostgreSQL + Redis template", async ({ app, api, page }) => {
  await app.gotoHome()
  await page.getByRole("button", { name: "New workflow" }).first().click()
  const dialog = page.getByRole("dialog", { name: "New workflow" })
  await dialog.getByRole("radiogroup", { name: "Workflow kind" }).getByRole("radio", { name: /Containers \(Docker\)/ }).click()
  // Only Docker templates are listed once the kind is Docker.
  await expect(dialog.getByText("Empty compose file")).toBeVisible()
  await expect(dialog.getByText("AWS Serverless API")).toHaveCount(0)
  await dialog.getByText("Node.js API + PostgreSQL + Redis").click()
  await dialog.getByRole("button", { name: "Create from template" }).click()
  await expect(dialog).toBeHidden()

  // The canvas: one card per service with its technology's icon, all inside the implicit default network.
  const canvas = page.getByTestId("docker-canvas")
  await expect(canvas.getByTestId("docker-service")).toHaveCount(3)
  const icon = (name: string) => service(app, name).locator("img").first()
  await expect(icon("db")).toHaveAttribute("alt", "PostgreSQL")
  await expect(icon("cache")).toHaveAttribute("alt", "Redis")
  // The API is built from ./api: its icon comes from the stack found in the build context.
  await expect(icon("api")).toHaveAttribute("alt", /Express|Node/)
  const frame = canvas.locator('[data-testid="docker-network"][data-name="default"]')
  await expect(frame).toBeVisible()
  await expect(frame).toContainText("default network")
  // Services sit inside the frame once the first layout is saved (cards move and the view fits meanwhile).
  const outside = async () => {
    const f = await frame.boundingBox()
    const out: string[] = []
    for (const name of ["api", "db", "cache"]) {
      const s = await service(app, name).boundingBox()
      const inside = !!f && !!s && s.x >= f.x && s.y >= f.y && s.x + s.width <= f.x + f.width + 1 && s.y + s.height <= f.y + f.height + 1
      if (!inside) out.push(name)
    }
    return out
  }
  await expect.poll(outside).toEqual([])
  // Named volumes are cards; depends_on edges carry their condition.
  await expect(canvas.locator('[data-testid="docker-volume"][data-name="pgdata"]')).toBeVisible()
  await expect(canvas.getByTestId("docker-edge-label").filter({ hasText: "healthy" })).toHaveCount(2)

  const wf = (await api.listWorkflows()).find((w) => w.name === "Node.js API + PostgreSQL + Redis")
  expect(wf?.kind).toBe("docker")
})

test("creates the Docker reverse proxy from the Edge patterns group", async ({ app, api, page }) => {
  await app.gotoHome()
  await page.getByRole("button", { name: "New workflow" }).first().click()
  const dialog = page.getByRole("dialog", { name: "New workflow" })
  await dialog.getByRole("radiogroup", { name: "Workflow kind" }).getByRole("radio", { name: /Containers \(Docker\)/ }).click()
  await dialog.getByRole("radiogroup", { name: "Template group" }).getByRole("radio", { name: "Edge patterns" }).click()
  // Only the three Docker edge patterns are listed, in pattern order.
  const group = dialog.getByRole("region", { name: "Edge patterns" })
  await expect(group.locator("[data-template]")).toHaveCount(3)
  expect(await group.locator("[data-template]").evaluateAll((els) => els.map((e) => e.getAttribute("data-template")))).toEqual([
    "docker-reverse-proxy",
    "docker-api-gateway",
    "docker-load-balancer",
  ])
  await expect(dialog.getByText("Node.js API + PostgreSQL + Redis")).toHaveCount(0)
  await group.locator('[data-template="docker-reverse-proxy"]').click()
  await dialog.getByRole("button", { name: "Create from template" }).click()
  await expect(dialog).toBeHidden()

  // NGINX in front of the two backends it routes to, each on the canvas.
  const canvas = page.getByTestId("docker-canvas")
  await expect(canvas.getByTestId("docker-service")).toHaveCount(3)
  await expect(service(app, "proxy").locator("img").first()).toHaveAttribute("alt", "NGINX")
  await expect(service(app, "api")).toBeVisible()
  await expect(service(app, "web")).toBeVisible()
  await expect(canvas.locator('[data-testid="docker-network"][data-name="backend"]')).toBeVisible()

  const wf = (await api.listWorkflows()).find((w) => w.name === "Reverse proxy (NGINX)")
  expect(wf?.kind).toBe("docker")
})

test("adds PostgreSQL from the Docker palette and makes the API wait until it is healthy", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Proxy stack", "docker-go-nginx")
  await expect(service(app, "api")).toBeVisible()
  // The Docker palette replaces the cloud tabs.
  await expect(page.getByTestId("docker-palette")).toBeVisible()
  await expect(page.getByRole("tab", { name: "AWS" })).toHaveCount(0)

  await page.getByRole("textbox", { name: "Search images" }).fill("postgres")
  await page.locator('[data-testid="docker-palette-item"][data-label="PostgreSQL"]').click()
  await expect(service(app, "postgres")).toBeVisible()
  // The catalog image comes with its data volume.
  await expect.poll(() => composeText(api, wf.id)).toMatch(/\n {2}postgres:\n {4}image: postgres:/)

  await connectServices(app, "api", "postgres")
  const connect = page.getByTestId("compose-connect-dialog")
  await expect(connect).toContainText("How should these connect?")
  await connect.getByRole("button", { name: /^Wait until postgres is healthy/ }).click()
  await expect(connect).toBeHidden()

  // The edge shows the condition on the canvas …
  await expect(page.getByTestId("docker-edge-label").filter({ hasText: "healthy" })).toHaveCount(2)
  // … and the Code tab shows it written into compose.yaml, with the template's comments kept.
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("treeitem", { name: /^compose\.yaml/ }).click()
  const editor = panel.getByRole("textbox", { name: "Contents of compose.yaml" })
  await expect(editor).toContainText("# Go API behind an NGINX reverse proxy.")
  await expect(editor).toContainText("the API is reachable inside the project network as http://api:8080.")
  const text = await composeText(api, wf.id)
  expect(text).toContain("# Go API behind an NGINX reverse proxy. Only the proxy is published;")
  expect(text).toMatch(/\n {2}api:\n(?: {4}.*\n|\n)*? {4}depends_on:\n {6}postgres:\n {8}condition: service_healthy\n/)
  await expect(editor).toContainText("condition: service_healthy")
})

test("a service added in the Code tab appears on the canvas", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Code stack", "docker-go-nginx")
  await expect(page.getByTestId("docker-service")).toHaveCount(2)
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("treeitem", { name: /^compose\.yaml/ }).click()
  const editor = panel.getByRole("textbox", { name: "Contents of compose.yaml" })
  await expect(editor).toContainText("services:")
  await editor.click()
  await page.keyboard.press("ControlOrMeta+End")
  await page.keyboard.insertText("\n  cache:\n    image: redis:7-alpine\n")

  await panel.getByRole("button", { name: "Review" }).click()
  const review = page.getByRole("dialog", { name: "Review changes" })
  await expect(review).toContainText("redis:7-alpine")
  await review.getByRole("button", { name: "Apply changes" }).click()

  await expect(service(app, "cache")).toBeVisible()
  await expect(service(app, "cache").locator("img").first()).toHaveAttribute("alt", "Redis")
  await expect(page.getByTestId("docker-service")).toHaveCount(3)
  await expect.poll(() => composeText(api, wf.id)).toContain("  cache:\n    image: redis:7-alpine")
  const stored = await api.getWorkflow(wf.id)
  expect(stored.compose?.graph.nodes.filter((n) => n.kind === "service").map((n) => n.name).sort()).toEqual(["api", "cache", "proxy"])
})

test("the Architecture view of a Docker workflow shows host, project, network and services", async ({ app, api, page }) => {
  await app.openNew(api, "Arch stack", "docker-node-postgres-redis")
  await page.getByRole("radiogroup", { name: "Canvas view" }).getByRole("radio", { name: "Architecture" }).click()
  const diagram = page.getByTestId("architecture-view")
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="docker-host"]')).toHaveAttribute("aria-label", "Docker host")
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="compose-project"]')).toBeVisible()
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="docker-network"]')).toHaveCount(1)
  for (const name of ["api", "db", "cache"]) await expect(diagram.locator(`[data-testid="arch-card"][aria-label="${name}"]`)).toBeVisible()
  // The API publishes a port: users reach it.
  await expect(diagram.locator('[data-testid="arch-actor"][aria-label="Users"]')).toBeVisible()
  // No cloud groups in a Docker diagram.
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="cloud"]')).toHaveCount(0)
})

test("a Terraform workflow still shows the AWS/Azure/GCP/OCI palette", async ({ app, api, page }) => {
  await api.createWorkflow("Containers", "docker-nginx-static")
  await app.openNew(api, "Cloud")
  const tabs = page.getByRole("tablist").first()
  for (const cloud of ["AWS", "Azure", "GCP", "OCI"]) await expect(tabs.getByRole("tab", { name: cloud })).toBeVisible()
  await expect(page.getByRole("textbox", { name: "Search resources" })).toBeVisible()
  await expect(page.getByTestId("docker-palette")).toHaveCount(0)
  await app.addFromPalette("dynamo", "DynamoDB Table", "aws_dynamodb_table")
  await expect(app.node("aws_dynamodb_table.table")).toBeVisible()
})
