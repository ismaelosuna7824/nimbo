import { describe, expect, test } from "bun:test"
import { bump, changelog, parseCommit, releaseLevel, type Commit } from "./next-version"

const commit = (subject: string, body = "", sha = "a".repeat(40)) => parseCommit(sha, subject, body)

describe("parseCommit", () => {
  test("reads type, scope and description", () => {
    expect(commit("feat(desktop): add updater")).toMatchObject({ type: "feat", scope: "desktop", description: "add updater", breaking: false })
    expect(commit("fix: crash on start")).toMatchObject({ type: "fix", scope: null })
  })

  test("detects breaking changes from ! and the footer", () => {
    expect(commit("feat(api)!: drop v0 routes")?.breaking).toBe(true)
    expect(commit("refactor: rename store", "Some text\n\nBREAKING CHANGE: settings moved")?.breaking).toBe(true)
  })

  test("ignores non-conventional messages", () => {
    expect(commit("Merge pull request #3 from x/y")).toBeNull()
    expect(commit("update stuff")).toBeNull()
  })
})

describe("releaseLevel", () => {
  const level = (...subjects: string[]) => releaseLevel(subjects.map((s) => commit(s)).filter((c): c is Commit => c !== null))

  test("picks the highest bump", () => {
    expect(level("fix: a", "feat: b")).toBe("minor")
    expect(level("perf: a", "docs: b")).toBe("patch")
    expect(level("fix: a", "feat!: b")).toBe("major")
  })

  test("docs, ci, chore and test commits are not releasable", () => {
    expect(level("docs: a", "ci: b", "chore: c", "test: d")).toBeNull()
    expect(level()).toBeNull()
  })
})

test("bump", () => {
  expect(bump("0.1.0", "patch")).toBe("0.1.1")
  expect(bump("0.1.3", "minor")).toBe("0.2.0")
  expect(bump("0.4.2", "major")).toBe("1.0.0")
})

test("changelog groups user-facing commits and links them", () => {
  const commits = [
    commit("feat(desktop): in-app updates", "", "1".repeat(40)),
    commit("fix: wrong port", "", "2".repeat(40)),
    commit("ci: pin runner", "", "3".repeat(40)),
    commit("feat!: new data format", "", "4".repeat(40)),
  ].filter((c): c is Commit => c !== null)
  const md = changelog(commits, "o/r", "v0.1.0", "v1.0.0")
  expect(md).toContain("### Breaking changes\n\n- new data format ([4444444](https://github.com/o/r/commit/" + "4".repeat(40) + "))")
  expect(md).toContain("### Features\n\n- **desktop:** in-app updates")
  expect(md).toContain("### Bug fixes\n\n- wrong port")
  expect(md).not.toContain("pin runner")
  expect(md).toContain("https://github.com/o/r/compare/v0.1.0...v1.0.0")
})
