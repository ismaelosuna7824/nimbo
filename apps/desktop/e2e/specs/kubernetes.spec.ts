import type { Locator } from "@playwright/test"
import type { Workflow, WorkflowFileContent } from "../../src/lib/api/types"
import { expect, test, type App, type NimboApi } from "../support/fixtures"

// Kubernetes workspaces without a cluster: the manifests / Kustomize / Helm files, their rendered graph and the UI
// around it (CI has no Docker images; nothing is deployed).

const object = (app: App, id: string): Locator => app.page.getByTestId(`k8s-node-${id}`)

const fileText = (api: NimboApi, id: string, path: string) =>
  api.request<WorkflowFileContent>("GET", `/workflows/${id}/files/content?path=${encodeURIComponent(path)}`).then((f) => f.content)

const graph = (api: NimboApi, id: string) => api.request<Workflow>("GET", `/workflows/${id}`).then((w) => w.kubernetes!.graph)

/** Drags from one object card's output handle to another's input handle, once both stopped moving. */
async function connectObjects(app: App, from: string, to: string) {
  const source = object(app, from).locator(".react-flow__handle.source")
  const target = object(app, to).locator(".react-flow__handle.target")
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

/** Types text at the end of a file in the Code tab, then reviews and applies the change. */
async function appendInCode(app: App, path: string, text: string) {
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  const name = path.split("/").pop()!
  await panel.getByRole("treeitem", { name: new RegExp(`^${name.replace(".", "\\.")}`) }).click()
  const editor = panel.getByRole("textbox", { name: `Contents of ${path}` })
  await expect(editor).toContainText("apiVersion")
  await editor.click()
  await app.page.keyboard.press("ControlOrMeta+End")
  await app.page.keyboard.insertText(text)
  await panel.getByRole("button", { name: "Review" }).click()
  const review = app.page.getByRole("dialog", { name: "Review changes" })
  await review.getByRole("button", { name: "Apply changes" }).click()
  await expect(review).toBeHidden()
}

test("creates a Kubernetes workflow from the microservices template", async ({ app, api, page }) => {
  await app.gotoHome()
  await page.getByRole("button", { name: "New workflow" }).first().click()
  const dialog = page.getByRole("dialog", { name: "New workflow" })
  await dialog.getByRole("radiogroup", { name: "Workflow kind" }).getByRole("radio", { name: /Kubernetes/ }).click()
  // Only Kubernetes templates are listed once the kind is Kubernetes.
  await expect(dialog.getByText("Empty manifests")).toBeVisible()
  await expect(dialog.getByText("Node.js API + PostgreSQL + Redis")).toHaveCount(0)
  await dialog.getByText("Microservices with Ingress + autoscaling").click()
  await dialog.getByRole("button", { name: "Create from template" }).click()
  await expect(dialog).toBeHidden()

  // One card per object inside the namespace frame, with the kind's icon and the container image's technology.
  const canvas = page.getByTestId("k8s-canvas")
  await expect(canvas).toBeVisible()
  await expect(object(app, "Namespace/shop")).toContainText("shop")
  for (const id of ["Deployment/shop/frontend", "Deployment/shop/backend", "Deployment/shop/redis", "Service/shop/backend", "Ingress/shop/shop"])
    await expect(object(app, id)).toBeVisible()
  await expect(object(app, "HorizontalPodAutoscaler/shop/backend")).toContainText("1–4")
  await expect(object(app, "Deployment/shop/redis").locator('img[alt="Deployment"]').first()).toBeVisible()
  await expect(object(app, "Deployment/shop/redis").locator('img[alt^="redis:"]').first()).toBeVisible()
  await expect(object(app, "Ingress/shop/shop").locator('img[alt="Ingress"]').first()).toBeVisible()
  // The Ingress routes to both Services on their http port.
  await expect(canvas.getByTestId("k8s-edge-label").filter({ hasText: "http" })).toHaveCount(2)

  const wf = (await api.listWorkflows()).find((w) => w.name === "Microservices with Ingress + autoscaling")
  expect(wf?.kind).toBe("kubernetes")
  expect(wf?.nodeCount).toBeGreaterThanOrEqual(10)
})

test("adds NGINX from the palette and points the frontend Service at it, keeping the file's comments", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Shop", "k8s-microservices")
  await expect(object(app, "Deployment/shop/frontend")).toBeVisible()
  // The Kubernetes palette replaces the cloud tabs.
  await expect(page.getByTestId("k8s-palette")).toBeVisible()
  await expect(page.getByRole("tab", { name: "AWS" })).toHaveCount(0)

  await page.getByTestId("k8s-palette-item-image:nginx").click()
  // The image comes as a Deployment with its Service, in the namespace the app already uses.
  await expect(object(app, "Deployment/shop/nginx")).toBeVisible()
  await expect(object(app, "Service/shop/nginx")).toBeVisible()

  await connectObjects(app, "Service/shop/frontend", "Deployment/shop/nginx")
  const connect = page.getByTestId("k8s-connect-dialog")
  if (await connect.isVisible().catch(() => false)) await connect.getByTestId("k8s-connect-recipe-selects").click()
  await expect(connect).toBeHidden()

  await expect
    .poll(async () => (await graph(api, wf.id)).edges.some((e) => e.kind === "selects" && e.source === "Service/shop/frontend" && e.target === "Deployment/shop/nginx"))
    .toBe(true)
  const text = await fileText(api, wf.id, "k8s/frontend.yaml")
  // The selector now names the NGINX pods; the comment above the Deployment and the rest of the file are untouched.
  expect(text).toMatch(/kind: Service\n(?:.*\n)*? {2}selector:\n {4}app: nginx\n/)
  expect(text).toContain("# The frontend calls the backend (--backend-url) for /echo.")
  expect(text).toContain("--backend-url=http://backend.shop/echo")
})

test("a ConfigMap mount added in the Code tab appears as an edge on the canvas", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Mounts", "k8s-microservices")
  await expect(object(app, "Deployment/shop/backend")).toBeVisible()
  await appendInCode(
    app,
    "k8s/redis.yaml",
    [
      "",
      "---",
      "# Settings the worker reads from /etc/worker.",
      "apiVersion: v1",
      "kind: ConfigMap",
      "metadata:",
      "  name: worker-config",
      "  namespace: shop",
      "data:",
      "  QUEUE: jobs",
      "---",
      "apiVersion: apps/v1",
      "kind: Deployment",
      "metadata:",
      "  name: worker",
      "  namespace: shop",
      "spec:",
      "  selector:",
      "    matchLabels:",
      "      app: worker",
      "  template:",
      "    metadata:",
      "      labels:",
      "        app: worker",
      "    spec:",
      "      containers:",
      "        - name: worker",
      "          image: busybox:1.37",
      "          volumeMounts:",
      "            - name: config",
      "              mountPath: /etc/worker",
      "      volumes:",
      "        - name: config",
      "          configMap:",
      "            name: worker-config",
      "",
    ].join("\n"),
  )
  await expect(object(app, "ConfigMap/shop/worker-config")).toBeVisible()
  await expect(object(app, "Deployment/shop/worker")).toBeVisible()
  const edge = (await graph(api, wf.id)).edges.find((e) => e.source === "Deployment/shop/worker" && e.target === "ConfigMap/shop/worker-config")
  expect(edge?.kind).toBe("volume")
  await expect(page.getByTestId(`rf__edge-${edge!.id}`)).toBeVisible()
})

test("a Helm values edit is rendered on the canvas and mapped in the inspector", async ({ app, api, page }) => {
  const wf = await app.openNew(api, "Chart", "k8s-helm-app")
  const deployment = object(app, "Deployment/podinfo/podinfo")
  await expect(deployment).toBeVisible()
  await expect(deployment).toContainText("×1")

  // The inspector writes replicas to values.yaml (the template reads .Values.replicaCount).
  await deployment.click()
  const replicas = page.getByTestId("k8s-inspector-field-spec.replicas")
  await expect(replicas).toHaveAttribute("data-editable", "true")
  await expect(replicas).toContainText("replicaCount")

  // Editing the values file in the Code tab re-renders the chart.
  await app.setMode("Pro")
  const panel = app.bottomPanel("Code")
  await panel.getByRole("treeitem", { name: /^values\.yaml/ }).click()
  const editor = panel.getByRole("textbox", { name: "Contents of chart/values.yaml" })
  await expect(editor).toContainText("replicaCount: 1")
  await editor.getByText("replicaCount: 1").click()
  await page.keyboard.press("End")
  await page.keyboard.press("Backspace")
  await page.keyboard.insertText("3")
  await panel.getByRole("button", { name: "Review" }).click()
  const review = page.getByRole("dialog", { name: "Review changes" })
  await review.getByRole("button", { name: "Apply changes" }).click()
  await expect(review).toBeHidden()

  await expect(deployment).toContainText("×3")
  expect(await fileText(api, wf.id, "chart/values.yaml")).toContain("# Defaults for every environment;")
  // Environments are the chart's values files: prod renders its own replica count.
  // The switch sits on the canvas (the Deploy tab has its own copy).
  await page.getByTestId("k8s-canvas").getByTestId("environment").click()
  await page.getByTestId("environment-prod").click()
  await expect.poll(async () => (await api.request<Workflow>("GET", `/workflows/${wf.id}`)).kubernetes?.environment).toBe("prod")
})

test("the Architecture view of a Kubernetes workflow shows cluster, namespace, entry and workloads", async ({ app, api, page }) => {
  await app.openNew(api, "Arch shop", "k8s-microservices")
  await page.getByRole("radiogroup", { name: "Canvas view" }).getByRole("radio", { name: "Architecture" }).click()
  const diagram = page.getByTestId("architecture-view")
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="k8s-cluster"]')).toBeVisible()
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="k8s-namespace"]')).toHaveCount(1)
  for (const name of ["shop", "frontend", "backend", "redis"]) await expect(diagram.locator(`[data-testid="arch-card"][aria-label="${name}"]`)).toBeVisible()
  // The Ingress is the entry: users reach it.
  await expect(diagram.locator('[data-testid="arch-actor"][aria-label="Users"]')).toBeVisible()
  await expect(diagram.locator('[data-testid="arch-group"][data-group-kind="cloud"]')).toHaveCount(0)
  await expect(page.getByRole("button", { name: /Export image/ })).toBeVisible()
})

test("converts a Docker workflow to Kubernetes", async ({ app, api, page }) => {
  await app.openNew(api, "Compose shop", "docker-node-postgres-redis")
  await page.getByTestId("convert-kubernetes").click()
  const dialog = page.getByTestId("convert-kubernetes-dialog")
  await expect(dialog).toBeVisible()
  await dialog.getByTestId("convert-format-kustomize").click()
  // No load test yet: the sizing table shows compose limits, and prod autoscales the API.
  await expect(dialog.getByTestId("convert-sizing-table")).toContainText("Compose")
  await expect(dialog.getByTestId("convert-sizing-table")).toContainText("HPA 2–5 at 70% CPU")
  await expect(dialog.getByTestId("convert-sizing")).toContainText("No load test yet")
  await dialog.getByTestId("convert-kubernetes-create").click()
  await expect(dialog).toBeHidden()

  // The new workflow opens: a Deployment per stateless service, a StatefulSet for the database.
  await expect(page.getByTestId("k8s-canvas")).toBeVisible()
  await expect(page.getByTestId("k8s-canvas").locator('[data-testid^="k8s-node-StatefulSet/"]').first()).toBeVisible()
  await expect(page.getByTestId("k8s-canvas").locator('[data-testid^="k8s-node-Deployment/"]').first()).toBeVisible()
  const list = await api.listWorkflows()
  expect(list.filter((w) => w.kind === "kubernetes")).toHaveLength(1)
  expect(list.filter((w) => w.kind === "docker")).toHaveLength(1)
})
