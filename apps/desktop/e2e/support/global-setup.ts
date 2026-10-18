import { execFileSync, spawn, type ChildProcess } from "node:child_process"
import { once } from "node:events"
import { copyFileSync, mkdtempSync, mkdirSync, openSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import path from "node:path"
import { setTimeout as sleep } from "node:timers/promises"

const backendDir = path.resolve(import.meta.dirname, "../../../../backend")

/**
 * Builds the engine from source and starts it on a throwaway home directory.
 * Returns the teardown, which stops the daemon and deletes the directory.
 */
export default async function globalSetup() {
  const addr = process.env.NIMBO_E2E_DAEMON_ADDR!
  const root = mkdtempSync(path.join(process.platform === "win32" ? tmpdir() : "/tmp", "nimbo-e2e-"))
  const home = path.join(root, "home")
  mkdirSync(home)
  const bin = path.join(root, process.platform === "win32" ? "nimbo.exe" : "nimbo")
  const logPath = path.join(root, "daemon.log")

  let daemon: ChildProcess | undefined
  const teardown = async () => {
    if (daemon && daemon.exitCode === null && daemon.signalCode === null) {
      const exited = once(daemon, "exit")
      daemon.kill("SIGTERM")
      const timer = setTimeout(() => daemon!.kill("SIGKILL"), 10_000)
      await exited
      clearTimeout(timer)
    }
    rmSync(root, { recursive: true, force: true })
  }

  try {
    // NIMBO_E2E_BIN: test a prebuilt engine (copied, so the original is never touched) instead of building from source.
    const prebuilt = process.env.NIMBO_E2E_BIN
    if (prebuilt) copyFileSync(prebuilt, bin)
    else execFileSync("go", ["build", "-o", bin, "./cmd/nimbo"], { cwd: backendDir, stdio: "inherit" })
    const log = openSync(logPath, "a")
    daemon = spawn(bin, ["serve", "--addr", addr, "--home", home], {
      env: { ...process.env, NIMBO_DEV_ORIGINS: process.env.NIMBO_E2E_DEV_ORIGINS },
      stdio: ["ignore", log, log],
    })
    const api = `http://${addr}/api/v1`
    await waitForHealth(api, daemon, logPath)
    process.env.NIMBO_E2E_API = api
    process.env.NIMBO_E2E_TMP = root
  } catch (err) {
    await teardown()
    throw err
  }
  return teardown
}

async function waitForHealth(api: string, daemon: ChildProcess, logPath: string) {
  const deadline = Date.now() + 30_000
  while (Date.now() < deadline) {
    if (daemon.exitCode !== null) break
    try {
      const res = await fetch(`${api}/health`)
      if (res.ok) return
    } catch {
      // not listening yet
    }
    await sleep(200)
  }
  throw new Error(`nimbo daemon did not become healthy at ${api}/health:\n${readFileSync(logPath, "utf8")}`)
}
