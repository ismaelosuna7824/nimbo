import { test as base, expect, type Locator, type Page } from "@playwright/test"
import type { SetupStatus, Settings, Workflow, WorkflowSummary } from "../../src/lib/api/types"

/** Thin client for the daemon started by the global setup, used to arrange and verify state. */
export class NimboApi {
  readonly baseURL: string
  constructor(baseURL: string) {
    this.baseURL = baseURL
  }

  async request<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(`${this.baseURL}${path}`, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    })
    const text = await res.text()
    if (!res.ok) throw new Error(`${method} ${path}: ${res.status} ${text}`)
    return (text ? JSON.parse(text) : undefined) as T
  }

  listWorkflows = () => this.request<WorkflowSummary[]>("GET", "/workflows")
  getWorkflow = (id: string) => this.request<Workflow>("GET", `/workflows/${id}`)
  createWorkflow = (name: string, templateId?: string) => this.request<Workflow>("POST", "/workflows", { name, templateId })
  addNode = (id: string, type: string, position = { x: 0, y: 0 }) =>
    this.request<{ workflow: Workflow; nodeId: string }>("POST", `/workflows/${id}/nodes`, { kind: "resource", type, position })
  terraform = (id: string) => this.request<{ files: Record<string, string> }>("GET", `/workflows/${id}/terraform`)
  exportTo = (id: string, dir: string) =>
    this.request<{ dir: string; files: string[] }>("POST", `/workflows/${id}/export`, { dir, environments: true })
  settings = () => this.request<Settings>("GET", "/settings")

  async findWorkflow(name: string) {
    const summary = (await this.listWorkflows()).find((w) => w.name === name)
    return summary ? this.getWorkflow(summary.id) : undefined
  }

  /** Back to a fresh install: no workflows, default settings. */
  async reset() {
    for (const w of await this.listWorkflows()) await this.request("DELETE", `/workflows/${w.id}`)
    await this.request("PUT", "/settings", await this.request<Settings>("GET", "/settings/defaults"))
  }
}

const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")

/** Page helpers for the desktop UI running in the browser against the test daemon. */
export class App {
  readonly page: Page
  constructor(page: Page) {
    this.page = page
  }

  /** Opens Home with the first-run wizard already dismissed (and the setup checklist closed if setup is incomplete). */
  async gotoHome() {
    await this.page.addInitScript(() => localStorage.setItem("nimbo.onboarded", "1"))
    const setup = this.page.waitForResponse((r) => r.request().method() === "GET" && new URL(r.url()).pathname === "/api/v1/setup")
    await this.page.goto("/")
    // The app opens the setup checklist (a modal that hides the rest of the page from assistive tech) as soon
    // as GET /setup reports something missing, e.g. on CI where the sandbox images aren't pulled. Settle it first.
    const status = (await (await setup).json()) as SetupStatus
    if (!status.ready) {
      const wizard = this.page.getByRole("dialog", { name: "Set up Nimbo" })
      await wizard.getByRole("button", { name: "I'll do this later" }).click()
      await expect(wizard).toBeHidden()
    }
    await expect(this.page.getByRole("heading", { name: "Nimbo", level: 1 })).toBeVisible()
  }

  workflowCard(name: string) {
    return this.page.getByRole("button", { name: new RegExp(`^${escapeRegExp(name)}\\b`) })
  }

  async openWorkflow(name: string) {
    await this.workflowCard(name).click()
    await expect(this.workflowName).toHaveValue(name)
  }

  /** Arranges a workflow through the API, then opens it from Home. */
  async openNew(api: NimboApi, name: string, templateId?: string) {
    const wf = await api.createWorkflow(name, templateId)
    await this.gotoHome()
    await this.openWorkflow(name)
    return wf
  }

  get workflowName() {
    return this.page.getByRole("textbox", { name: "Workflow name" })
  }

  get saved() {
    return this.page.getByRole("status", { name: "All changes saved" })
  }

  get inspector() {
    return this.page.getByRole("complementary", { name: "Inspector" })
  }

  get canvas() {
    return this.page.getByRole("application")
  }

  /** A resource card on the canvas, by its Terraform address (e.g. aws_sqs_queue.queue). */
  node(address: string): Locator {
    return this.canvas.getByRole("group").filter({ has: this.page.getByText(address, { exact: true }) })
  }

  /** Adds a resource by clicking its palette entry (searching for it first). */
  async addFromPalette(search: string, label: string, type: string) {
    await this.page.getByRole("textbox", { name: "Search resources" }).fill(search)
    await this.page.getByRole("button", { name: new RegExp(`^${escapeRegExp(label)} ${type}\\b`) }).click()
  }

  bottomTab(name: string) {
    return this.page.getByRole("tab", { name, exact: true })
  }

  bottomPanel(name: string) {
    return this.page.getByRole("tabpanel", { name, exact: true })
  }

  setMode(mode: "Guided" | "Pro") {
    return this.page.getByRole("radiogroup", { name: "Experience level" }).getByRole("radio", { name: mode }).click()
  }
}

export const test = base.extend<{ api: NimboApi; app: App }>({
  // Auto: every test starts from a fresh install, before its page loads.
  api: [
    // Playwright reads fixture dependencies from the destructuring pattern, so it must stay (empty).
    // oxlint-disable-next-line no-empty-pattern
    async ({}, provide) => {
      const api = new NimboApi(process.env.NIMBO_E2E_API!)
      await api.reset()
      await provide(api)
    },
    { auto: true },
  ],
  app: async ({ page }, provide) => provide(new App(page)),
})

export { expect }
