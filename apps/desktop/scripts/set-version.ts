// Sets the app version in tauri.conf.json, package.json and src-tauri/Cargo.toml.
// The release workflow runs it with the tag version before building:
//
//   bun scripts/set-version.ts 1.2.3
import { readFileSync, writeFileSync } from "node:fs"
import path from "node:path"

const version = process.argv[2]?.replace(/^v/, "")
// MSI (WiX) only accepts numeric versions, so releases use plain X.Y.Z.
if (!version || !/^\d+\.\d+\.\d+$/.test(version)) {
  console.error("usage: bun scripts/set-version.ts <X.Y.Z>")
  process.exit(2)
}

const desktop = path.resolve(import.meta.dir, "..")
const files: [string, RegExp][] = [
  ["src-tauri/tauri.conf.json", /^(\s*"version":\s*")[^"]*(")/m],
  ["package.json", /^(\s*"version":\s*")[^"]*(")/m],
  // The first `version =` in Cargo.toml is the one in [package].
  ["src-tauri/Cargo.toml", /^(version\s*=\s*")[^"]*(")/m],
]
for (const [rel, pattern] of files) {
  const file = path.join(desktop, rel)
  const text = readFileSync(file, "utf8")
  if (!pattern.test(text)) throw new Error(`no version field found in ${rel}`)
  writeFileSync(file, text.replace(pattern, `$1${version}$2`))
  console.log(`${rel}: ${version}`)
}
