import { expect, test } from "../support/fixtures"

test("follows a request through the Build canvas step by step", async ({ app, api, page }) => {
  await app.openNew(api, "Walk", "aws-serverless-api")
  await page.getByRole("button", { name: "Follow a request" }).click()
  const panel = page.getByTestId("walkthrough-panel")
  const counter = panel.getByTestId("walkthrough-step-counter")
  const caption = panel.getByTestId("walkthrough-caption")
  // It plays on its own; pause to step by hand.
  await panel.getByRole("button", { name: "Pause (space)" }).click()
  await expect(counter).toHaveText("1/4")
  await expect(caption).toContainText("the request starts here")

  const card = (id: string) => page.locator(`.react-flow__node[data-id="${id}"]`)
  // The caption takes the bottom of the canvas: the minimap steps aside meanwhile.
  await expect(page.locator(".react-flow__minimap")).toHaveCount(0)

  // A selected (focused) card stays put: the arrows only drive the walkthrough. Measured against another card in
  // card widths, so the camera following the hop (pan and zoom) does not count.
  const offset = async () => {
    const [a, b] = [(await card("aws_apigatewayv2_api.http").boundingBox())!, (await card("aws_lambda_function.api").boundingBox())!]
    return { x: Math.round(((a.x - b.x) / a.width) * 100), y: Math.round(((a.y - b.y) / a.width) * 100) }
  }
  await card("aws_apigatewayv2_api.http").click()
  await expect(card("aws_apigatewayv2_api.http")).toHaveClass(/selected/)
  const before = await offset()
  await page.keyboard.press("ArrowRight")
  await page.keyboard.press("ArrowUp")
  await expect(counter).toHaveText("2/4")
  await expect.poll(offset).toEqual(before)
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("http")
  await expect(caption).toContainText("It is the front door")
  await expect(card("aws_apigatewayv2_api.http")).toHaveClass(/ring-primary/)
  // The request travels as a dot into the front door.
  await expect(page.getByTestId("walkthrough-dot")).toHaveCount(1)
  // Off the path: faded.
  await expect(card("aws_iam_role.lambda")).toHaveClass(/opacity-25/)

  // The keyboard steps too; the Lambda shows the role it runs as.
  await page.keyboard.press("ArrowRight")
  await expect(counter).toHaveText("3/4")
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("api")
  await expect(panel.getByTestId("walkthrough-via")).toContainText("the API Integration")
  await expect(panel.getByTestId("walkthrough-also")).toContainText("IAM Role lambda")
  await expect(card("aws_lambda_function.api")).toHaveClass(/ring-primary/)
  await expect(card("aws_apigatewayv2_api.http")).not.toHaveClass(/ring-primary/)
  // The hop's edges are emphasized without their raw attribute paths: the caption says how they connect.
  await expect(page.locator(".react-flow__edgelabel-renderer").getByText(/integration_uri/)).toHaveCount(0)

  await page.keyboard.press("ArrowRight")
  await expect(counter).toHaveText("4/4")
  await expect(panel.getByTestId("walkthrough-via")).toContainText("environment variable TABLE_NAME")
  await page.keyboard.press("ArrowLeft")
  await expect(counter).toHaveText("3/4")
  await page.keyboard.press("ArrowRight")
  await page.keyboard.press("ArrowRight")
  await expect(panel.getByTestId("walkthrough-summary")).toContainText("The request passed through 3 components.")

  // Esc closes it and the canvas is as it was.
  await page.keyboard.press("Escape")
  await expect(panel).toHaveCount(0)
  await expect(card("aws_iam_role.lambda")).not.toHaveClass(/opacity-25/)
  await expect(page.locator(".react-flow__minimap")).toHaveCount(1)
})

test("in the Architecture view: picks an entry, marks the asynchronous hop and continues to the consumer", async ({ app, api, page }) => {
  await app.openNew(api, "Queue walk", "docker-rabbitmq-worker")
  await page.getByRole("radiogroup", { name: "Canvas view" }).getByRole("radio", { name: "Architecture" }).click()
  const diagram = page.getByTestId("architecture-view")
  await expect(diagram.locator('[data-testid="arch-card"][aria-label="producer"]')).toBeVisible()

  await diagram.getByRole("button", { name: "Follow a request" }).click()
  const panel = page.getByTestId("walkthrough-panel")
  // Users reach two services: choose where the request comes in.
  await panel.getByTestId("walkthrough-entries").getByRole("button", { name: "producer" }).click()
  await panel.getByRole("button", { name: "Pause (space)" }).click()
  await panel.getByRole("button", { name: "Next step (→)" }).click()
  await panel.getByRole("button", { name: "Next step (→)" }).click()
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("broker")
  await expect(panel.getByTestId("walkthrough-async")).toContainText("the request returns here; the message continues later")
  await expect(page.locator('.react-flow__node[data-id="service:broker"]')).toHaveClass(/ring-primary/)

  await panel.getByRole("button", { name: "Next step (→)" }).click()
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("worker")
  await expect(panel.getByTestId("walkthrough-caption")).toContainText("Later, it picks the message up from broker")
  await panel.getByRole("button", { name: "Next step (→)" }).click()
  await expect(panel.getByTestId("walkthrough-summary")).toContainText("The request passed through 3 components; 1 continued asynchronously.")
  await panel.getByRole("button", { name: "Close (Esc)" }).click()
  await expect(panel).toHaveCount(0)
})

test("from the command palette on Kubernetes: a fork asks which way, Show all plays both", async ({ app, api, page }) => {
  // With reduced motion the highlight jumps from hop to hop; no dot travels.
  await page.emulateMedia({ reducedMotion: "reduce" })
  await app.openNew(api, "Kube walk", "k8s-microservices")
  await expect(page.getByTestId("k8s-node-Deployment/shop/frontend")).toBeVisible()
  await page.keyboard.press("ControlOrMeta+k")
  const palette = page.getByRole("dialog", { name: "Command palette" })
  await palette.getByRole("combobox").fill("follow a request")
  await palette.getByRole("option", { name: "Follow a request" }).click()

  const panel = page.getByTestId("walkthrough-panel")
  await panel.getByRole("button", { name: "Pause (space)" }).click()
  await panel.getByRole("button", { name: "Next step (→)" }).click()
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("shop")
  // The Ingress routes to two workloads: the walk waits for a way.
  await expect(panel.getByTestId("walkthrough-step-counter")).toHaveText("2/2+")
  await expect(panel.getByRole("button", { name: "Next step (→)" })).toBeDisabled()
  await panel.getByTestId("walkthrough-choices").getByRole("button", { name: "Show all" }).click()
  await expect(panel.getByTestId("walkthrough-name")).toHaveText("backend")
  await expect(page.getByTestId("walkthrough-dot")).toHaveCount(0)
  await expect(panel.getByTestId("walkthrough-via")).toContainText("Service backend")
  await expect(page.locator('.react-flow__node[data-id="Deployment/shop/backend"]')).toHaveClass(/ring-primary/)
  await expect(panel.getByTestId("walkthrough-step-counter")).toHaveText("3/6")
})
