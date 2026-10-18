// Computes the next release from Conventional Commits since the last vX.Y.Z tag
// and writes the grouped changelog. Used by .github/workflows/release.yml:
//
//   bun scripts/release/next-version.ts --repo owner/name --notes changelog.md >> "$GITHUB_OUTPUT"
//
// Prints version=, tag=, previous_tag=, release=true|false, tagged=true|false.
// feat → minor, fix/perf → patch, `type!:` or a BREAKING CHANGE footer → major.
// The first release (no tags yet) is 0.1.0. If HEAD already carries a vX.Y.Z tag
// (a re-run), that version is reused so the release can be finished.
import { writeFileSync } from "node:fs"
import { parseArgs } from "node:util"

export type Level = "major" | "minor" | "patch"

export interface Commit {
  sha: string
  type: string
  scope: string | null
  description: string
  breaking: boolean
}

const HEADER = /^(?<type>[a-z]+)(?:\((?<scope>[^)]*)\))?(?<bang>!)?: (?<description>.+)$/i
const BREAKING_FOOTER = /^BREAKING[ -]CHANGE: /m
const SEMVER_TAG = /^v(\d+)\.(\d+)\.(\d+)$/
export const FIRST_VERSION = "0.1.0"

/** Parses a Conventional Commit; null for anything else (merges, free-form messages). */
export function parseCommit(sha: string, subject: string, body: string): Commit | null {
  const m = HEADER.exec(subject.trim())
  if (!m?.groups) return null
  return {
    sha,
    type: m.groups.type.toLowerCase(),
    scope: m.groups.scope?.trim() || null,
    description: m.groups.description.trim(),
    breaking: !!m.groups.bang || BREAKING_FOOTER.test(body),
  }
}

/** The highest bump the commits ask for, or null when none of them is releasable. */
export function releaseLevel(commits: Commit[]): Level | null {
  if (commits.some((c) => c.breaking)) return "major"
  if (commits.some((c) => c.type === "feat")) return "minor"
  if (commits.some((c) => c.type === "fix" || c.type === "perf")) return "patch"
  return null
}

export function bump(version: string, level: Level): string {
  const [major, minor, patch] = version.split(".").map(Number)
  if (level === "major") return `${major + 1}.0.0`
  if (level === "minor") return `${major}.${minor + 1}.0`
  return `${major}.${minor}.${patch + 1}`
}

const SECTIONS: [string, (c: Commit) => boolean][] = [
  ["Breaking changes", (c) => c.breaking],
  ["Features", (c) => !c.breaking && c.type === "feat"],
  ["Bug fixes", (c) => !c.breaking && c.type === "fix"],
  ["Performance", (c) => !c.breaking && c.type === "perf"],
]

/** Markdown changelog grouped by kind; non-user-facing types (docs, ci, chore, test, …) are left out. */
export function changelog(commits: Commit[], repo: string, previousTag: string | null, tag: string): string {
  const out: string[] = []
  for (const [title, match] of SECTIONS) {
    const items = commits.filter(match)
    if (!items.length) continue
    out.push(`### ${title}`, "")
    for (const c of items) {
      const scope = c.scope ? `**${c.scope}:** ` : ""
      out.push(`- ${scope}${c.description} ([${c.sha.slice(0, 7)}](https://github.com/${repo}/commit/${c.sha}))`)
    }
    out.push("")
  }
  if (previousTag) out.push(`**Full changelog:** https://github.com/${repo}/compare/${previousTag}...${tag}`)
  return out.join("\n").trim()
}

function git(...args: string[]): string | null {
  const proc = Bun.spawnSync(["git", ...args], { stdout: "pipe", stderr: "pipe" })
  return proc.exitCode === 0 ? proc.stdout.toString().trim() : null
}

function commitsSince(previousTag: string | null, head: string): Commit[] {
  const range = previousTag ? `${previousTag}..${head}` : head
  const log = git("log", "--format=%H%x1f%s%x1f%b%x1e", range) ?? ""
  return log
    .split("\x1e")
    .map((entry) => entry.trim().split("\x1f"))
    .filter((fields) => fields.length >= 2)
    .map(([sha, subject, body = ""]) => parseCommit(sha, subject, body))
    .filter((c): c is Commit => c !== null)
}

if (import.meta.main) {
  const { values } = parseArgs({ options: { repo: { type: "string" }, notes: { type: "string" } } })
  if (!values.repo || !values.notes) {
    console.error("usage: bun scripts/release/next-version.ts --repo owner/name --notes <file>")
    process.exit(2)
  }

  const headTag = (git("tag", "--points-at", "HEAD") ?? "").split("\n").find((t) => SEMVER_TAG.test(t)) ?? null
  // The newest vX.Y.Z tag before this release.
  const previousTag = git("describe", "--tags", "--abbrev=0", "--match", "v[0-9]*.[0-9]*.[0-9]*", headTag ? "HEAD^" : "HEAD")
  const commits = commitsSince(previousTag, "HEAD")
  const level = releaseLevel(commits)
  const previousVersion = previousTag?.slice(1) ?? null

  let version: string
  if (headTag) version = headTag.slice(1)
  else if (!level) version = previousVersion ?? FIRST_VERSION
  else version = previousVersion ? bump(previousVersion, level) : FIRST_VERSION
  const release = !!headTag || level !== null
  const tag = `v${version}`

  writeFileSync(values.notes, `${changelog(commits, values.repo, previousTag, tag)}\n`)
  console.log(`version=${version}`)
  console.log(`tag=${tag}`)
  console.log(`previous_tag=${previousTag ?? ""}`)
  console.log(`release=${release}`)
  console.log(`tagged=${!!headTag}`)
}
