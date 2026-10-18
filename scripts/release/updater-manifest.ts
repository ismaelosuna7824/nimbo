// Writes latest.json for the Tauri v2 updater from the signed release assets:
//
//   bun scripts/release/updater-manifest.ts --dir release --version 1.2.3 \
//     --base-url https://github.com/o/r/releases/download/v1.2.3 --notes changelog.md
//
// Each updater artifact (<asset> + <asset>.sig, produced with createUpdaterArtifacts)
// maps to the updater platform keys below; the macOS universal tarball serves both
// architectures. A missing signature for a present artifact is an error.
import { existsSync, readFileSync, writeFileSync } from "node:fs"
import path from "node:path"
import { parseArgs } from "node:util"

export const UPDATER_ASSETS: Record<string, string[]> = {
  "Nimbo-macOS-universal.app.tar.gz": ["darwin-aarch64", "darwin-x86_64"],
  "Nimbo-Windows-x64-setup.exe": ["windows-x86_64"],
  "Nimbo-Linux-x86_64.AppImage": ["linux-x86_64"],
  "Nimbo-Linux-aarch64.AppImage": ["linux-aarch64"],
}

export interface Manifest {
  version: string
  notes: string
  pub_date: string
  platforms: Record<string, { signature: string; url: string }>
}

/** Builds the manifest from asset name → signature (only assets that exist). */
export function buildManifest(signatures: Record<string, string>, version: string, baseUrl: string, notes: string, pubDate: Date): Manifest {
  const platforms: Manifest["platforms"] = {}
  for (const [asset, keys] of Object.entries(UPDATER_ASSETS)) {
    const signature = signatures[asset]
    if (signature === undefined) continue
    for (const key of keys) platforms[key] = { signature, url: `${baseUrl}/${asset}` }
  }
  return { version, notes, pub_date: pubDate.toISOString(), platforms }
}

if (import.meta.main) {
  const { values } = parseArgs({
    options: { dir: { type: "string" }, version: { type: "string" }, "base-url": { type: "string" }, notes: { type: "string" } },
  })
  const { dir, version, "base-url": baseUrl, notes } = values
  if (!dir || !version || !baseUrl || !notes) {
    console.error("usage: bun scripts/release/updater-manifest.ts --dir <assets> --version X.Y.Z --base-url <url> --notes <file>")
    process.exit(2)
  }

  const signatures: Record<string, string> = {}
  for (const asset of Object.keys(UPDATER_ASSETS)) {
    if (!existsSync(path.join(dir, asset))) continue
    const sig = path.join(dir, `${asset}.sig`)
    if (!existsSync(sig)) throw new Error(`${asset} has no ${asset}.sig; build with createUpdaterArtifacts and the signing key`)
    signatures[asset] = readFileSync(sig, "utf8").trim()
  }
  if (!Object.keys(signatures).length) throw new Error(`no updater artifacts in ${dir}`)

  const manifest = buildManifest(signatures, version, baseUrl, readFileSync(notes, "utf8").trim(), new Date())
  writeFileSync(path.join(dir, "latest.json"), `${JSON.stringify(manifest, null, 2)}\n`)
  console.log(`latest.json: ${version} for ${Object.keys(manifest.platforms).join(", ")}`)
}
