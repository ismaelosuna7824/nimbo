import { expect, test } from "bun:test"
import { buildManifest } from "./updater-manifest"

test("maps the universal macOS tarball to both architectures and skips missing assets", () => {
  const manifest = buildManifest(
    { "Nimbo-macOS-universal.app.tar.gz": "SIGMAC", "Nimbo-Windows-x64-setup.exe": "SIGWIN" },
    "1.2.3",
    "https://example.com/v1.2.3",
    "notes",
    new Date("2026-01-02T03:04:05Z"),
  )
  expect(manifest).toEqual({
    version: "1.2.3",
    notes: "notes",
    pub_date: "2026-01-02T03:04:05.000Z",
    platforms: {
      "darwin-aarch64": { signature: "SIGMAC", url: "https://example.com/v1.2.3/Nimbo-macOS-universal.app.tar.gz" },
      "darwin-x86_64": { signature: "SIGMAC", url: "https://example.com/v1.2.3/Nimbo-macOS-universal.app.tar.gz" },
      "windows-x86_64": { signature: "SIGWIN", url: "https://example.com/v1.2.3/Nimbo-Windows-x64-setup.exe" },
    },
  })
})
