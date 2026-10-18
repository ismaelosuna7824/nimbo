// Builds the Go daemon as a Tauri sidecar: src-tauri/binaries/nimbo-<rust target triple>[.exe].
//
//   bun scripts/build-sidecar.ts                                  # host target
//   bun scripts/build-sidecar.ts --target x86_64-pc-windows-msvc  # cross-compile (CGO-free)
//   bun scripts/build-sidecar.ts --target universal-apple-darwin  # arm64 + x86_64 merged with lipo
//
// NIMBO_VERSION (set by setup.sh from `git describe`, or by the release workflow
// from the tag) is stamped into `nimbo version`.
import { mkdirSync } from "node:fs"
import path from "node:path"
import { parseArgs } from "node:util"

const desktop = path.resolve(import.meta.dir, "..")
const backend = path.resolve(desktop, "../../backend")
const outDir = path.join(desktop, "src-tauri", "binaries")

const { values } = parseArgs({ options: { target: { type: "string" } } })
const triple = values.target ?? hostTriple()
const version = process.env.NIMBO_VERSION
mkdirSync(outDir, { recursive: true })

if (triple === "universal-apple-darwin") {
  // `tauri build --target universal-apple-darwin` compiles each architecture separately
  // (tauri-build wants the per-arch sidecars) and bundles the lipo-merged one.
  const slices = ["aarch64-apple-darwin", "x86_64-apple-darwin"].map((t) => build(t, sidecarPath(t)))
  const out = sidecarPath(triple)
  run(["lipo", "-create", "-output", out, ...slices], desktop)
  console.log(`merged -> ${path.relative(desktop, out)}`)
} else {
  build(triple, sidecarPath(triple))
}

function build(target: string, out: string): string {
  const ldflags = version ? ["-ldflags", `-X main.version=${version}`] : []
  console.log(`building nimbo${version ? ` ${version}` : ""} for ${target} -> ${path.relative(desktop, out)}`)
  run(["go", "build", "-trimpath", ...ldflags, "-o", out, "./cmd/nimbo"], backend, {
    CGO_ENABLED: "0",
    GOOS: goos(target),
    GOARCH: goarch(target),
  })
  return out
}

function sidecarPath(target: string): string {
  return path.join(outDir, `nimbo-${target}${target.includes("windows") ? ".exe" : ""}`)
}

function goos(target: string): string {
  if (target.includes("apple-darwin")) return "darwin"
  if (target.includes("windows")) return "windows"
  if (target.includes("linux")) return "linux"
  throw new Error(`unsupported target triple: ${target}`)
}

function goarch(target: string): string {
  const arch = target.split("-")[0]
  if (arch === "aarch64") return "arm64"
  if (arch === "x86_64") return "amd64"
  throw new Error(`unsupported target architecture: ${target}`)
}

function hostTriple(): string {
  const rustc = Bun.spawnSync(["rustc", "-vV"], { stdout: "pipe", stderr: "inherit" })
  if (rustc.exitCode !== 0) throw new Error("rustc -vV failed; install Rust to build the desktop app")
  const host = /^host: (\S+)$/m.exec(rustc.stdout.toString())?.[1]
  if (!host) throw new Error("could not read the host target triple from rustc -vV")
  return host
}

function run(cmd: string[], cwd: string, env: Record<string, string> = {}): void {
  const proc = Bun.spawnSync(cmd, { cwd, env: { ...process.env, ...env }, stdout: "inherit", stderr: "inherit" })
  if (proc.exitCode !== 0) process.exit(proc.exitCode ?? 1)
}
