import { defineConfig, devices } from "@playwright/test"

// The Docker project deploys to real sandbox containers; it only runs with E2E_DOCKER=1
// and on its own ports so it can run next to the regular suite. E2E_DAEMON_PORT and
// E2E_VITE_PORT move a run to other ports (e.g. several checkouts or agents at once).
const docker = process.env.E2E_DOCKER === "1"
const daemonPort = Number(process.env.E2E_DAEMON_PORT) || (docker ? 7865 : 7864)
const vitePort = Number(process.env.E2E_VITE_PORT) || (docker ? 5193 : 5192)
const appURL = `http://localhost:${vitePort}`

// Read by the global setup, which builds and starts the daemon.
process.env.NIMBO_E2E_DAEMON_ADDR = `127.0.0.1:${daemonPort}`
process.env.NIMBO_E2E_DEV_ORIGINS = `http://127.0.0.1:${vitePort},http://localhost:${vitePort}`

export default defineConfig({
  testDir: "./e2e",
  globalSetup: "./e2e/support/global-setup.ts",
  // Specs share one daemon and reset its data before each test, so they run one at a time.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  timeout: docker ? 15 * 60_000 : 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    ...devices["Desktop Chrome"],
    baseURL: appURL,
    viewport: { width: 1440, height: 900 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "app", testDir: "./e2e/specs" },
    // The desktop app is a WKWebView on macOS: the diagram and its image export also run in WebKit.
    {
      name: "webkit",
      testDir: "./e2e/specs",
      testMatch: /architecture\.spec\.ts$/,
      use: { ...devices["Desktop Safari"], viewport: { width: 1440, height: 900 } },
    },
    ...(docker ? [{ name: "docker", testDir: "./e2e/docker" }] : []),
  ],
  webServer: {
    command: `bunx vite --port ${vitePort} --strictPort`,
    url: appURL,
    reuseExistingServer: false,
    timeout: 60_000,
    env: { VITE_NIMBO_URL: `http://127.0.0.1:${daemonPort}` },
  },
})
