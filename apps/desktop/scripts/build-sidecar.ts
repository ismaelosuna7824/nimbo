// Builds the Go daemon as a Tauri sidecar: src-tauri/binaries/nimbo-<rust target triple>[.exe].
import { mkdirSync } from "node:fs"
import path from "node:path"

const desktop = path.resolve(import.meta.dir, "..")
const backend = path.resolve(desktop, "../../backend")

const rustc = Bun.spawnSync(["rustc", "-vV"], { stdout: "pipe", stderr: "inherit" })
if (rustc.exitCode !== 0) throw new Error("rustc -vV failed; install Rust to build the desktop app")
const triple = /^host: (\S+)$/m.exec(rustc.stdout.toString())?.[1]
if (!triple) throw new Error("could not read the host target triple from rustc -vV")

const outDir = path.join(desktop, "src-tauri", "binaries")
mkdirSync(outDir, { recursive: true })
const out = path.join(outDir, `nimbo-${triple}${triple.includes("windows") ? ".exe" : ""}`)

// NIMBO_VERSION (set by setup.sh from `git describe`) is stamped into `nimbo version`.
const version = process.env.NIMBO_VERSION
const ldflags = version ? ["-ldflags", `-X main.version=${version}`] : []
console.log(`building nimbo${version ? ` ${version}` : ""} -> ${path.relative(desktop, out)}`)
const go = Bun.spawnSync(["go", "build", "-trimpath", ...ldflags, "-o", out, "./cmd/nimbo"], {
  cwd: backend,
  stdout: "inherit",
  stderr: "inherit",
})
if (go.exitCode !== 0) process.exit(go.exitCode ?? 1)
