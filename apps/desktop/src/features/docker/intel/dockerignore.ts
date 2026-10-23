// .dockerignore files: completion of the patterns worth excluding from a
// build context, picked by the stack found next to it (package.json → Node,
// requirements.txt → Python, go.mod → Go, …) plus the ones every context
// should drop (.git, .env*, the Dockerfile itself). Also the stack defaults
// the dk_dockerignore quick fix writes.

import type { CompletionResult, CompletionSection } from "@codemirror/autocomplete"
import { baseName, dirName } from "@/features/panels/terraform/file-model"

export const DOCKERIGNORE_NAME = /(^|\/)(\.dockerignore|[^/]+\.dockerignore)$/

export type Stack = "node" | "python" | "go" | "java" | "dotnet" | "rust" | "php" | "ruby"

const S_STACK: CompletionSection = { name: "For this stack", rank: 0 }
const S_COMMON: CompletionSection = { name: "Common", rank: 1 }

const COMMON: [string, string][] = [
  [".git", "Repository history: large and never needed in the image"],
  [".gitignore", "Git configuration"],
  [".env*", "Local environment files with credentials"],
  ["Dockerfile*", "The Dockerfile itself (changing it would bust the COPY . cache)"],
  [".dockerignore", "This file"],
  ["compose*.y*ml", "Compose files"],
  ["docker-compose*.y*ml", "Compose files"],
  ["**/*.log", "Log files"],
  [".vscode", "Editor settings"],
  [".idea", "Editor settings"],
  [".DS_Store", "macOS folder metadata"],
  ["README.md", "Documentation"],
  ["coverage", "Test coverage reports"],
  ["tmp", "Temporary files"],
]

const STACK: Record<Stack, [string, string][]> = {
  node: [
    ["node_modules", "Host-installed dependencies (wrong platform, huge)"],
    ["npm-debug.log*", "npm logs"],
    ["yarn-error.log", "Yarn logs"],
    ["dist", "Build output (rebuilt in the image)"],
    ["build", "Build output"],
    [".next", "Next.js build cache"],
    [".turbo", "Turborepo cache"],
  ],
  python: [
    ["__pycache__", "Bytecode caches"],
    ["**/*.pyc", "Bytecode"],
    [".venv", "Host virtualenv (wrong platform)"],
    ["venv", "Host virtualenv"],
    [".pytest_cache", "pytest cache"],
    [".mypy_cache", "mypy cache"],
    ["*.egg-info", "Packaging metadata"],
  ],
  go: [
    ["bin", "Host-built binaries"],
    ["vendor", "Vendored modules (go mod download runs in the image)"],
    ["**/*_test.go", "Tests (not needed to build the binary)"],
  ],
  java: [
    ["target", "Maven build output"],
    ["build", "Gradle build output"],
    [".gradle", "Gradle cache"],
    ["*.iml", "IntelliJ module files"],
  ],
  dotnet: [
    ["bin", "Build output"],
    ["obj", "Intermediate build output"],
    [".vs", "Visual Studio settings"],
    ["*.user", "User settings"],
  ],
  rust: [["target", "Cargo build output (huge)"]],
  php: [
    ["vendor", "Composer dependencies (installed in the image)"],
    ["node_modules", "Frontend dependencies"],
    ["storage/logs", "Laravel logs"],
  ],
  ruby: [
    ["vendor/bundle", "Host-installed gems"],
    [".bundle", "Bundler settings"],
    ["log", "Logs"],
    ["tmp", "Temporary files"],
  ],
}

const MARKERS: [RegExp, Stack][] = [
  [/^package\.json$/, "node"],
  [/^(requirements.*\.txt|pyproject\.toml|Pipfile|setup\.py)$/, "python"],
  [/^go\.mod$/, "go"],
  [/^(pom\.xml|build\.gradle(\.kts)?|settings\.gradle(\.kts)?)$/, "java"],
  [/\.(csproj|fsproj|sln)$/, "dotnet"],
  [/^Cargo\.toml$/, "rust"],
  [/^composer\.json$/, "php"],
  [/^Gemfile$/, "ruby"],
]

/** Stacks of a folder of the workflow, from the manifests directly inside it. */
export function stacksIn(dir: string, files: readonly string[]): Stack[] {
  const out = new Set<Stack>()
  for (const f of files) {
    if (dirName(f) !== dir) continue
    const b = baseName(f)
    for (const [re, s] of MARKERS) if (re.test(b)) out.add(s)
  }
  return [...out]
}

/** The patterns a new .dockerignore for these stacks starts with. */
export function dockerignoreDefaults(stacks: readonly Stack[], extra: readonly string[] = []): string {
  const lines = [".git", ".env*", "Dockerfile*", ".dockerignore", ...stacks.flatMap((s) => STACK[s].map(([p]) => p)), ...extra, "**/*.log"]
  return `${[...new Set(lines)].join("\n")}\n`
}

/** Completions at `pos` of a .dockerignore (a pattern at the start of a line). */
export function dockerignoreCompletions(text: string, pos: number, explicit: boolean, stacks: readonly Stack[]): CompletionResult | null {
  const lineStart = text.lastIndexOf("\n", pos - 1) + 1
  const before = text.slice(lineStart, pos)
  if (before.startsWith("#") || /\s/.test(before)) return null
  const prefix = before.replace(/^!/, "")
  if (!explicit && !prefix) return null
  const present = new Set(text.split("\n").map((l) => l.trim().replace(/^!/, "")))
  const seen = new Set<string>()
  const options = [
    ...stacks.flatMap((s) => STACK[s].map(([p, info]) => ({ label: p, info, section: S_STACK, detail: s, boost: 1 }))),
    ...COMMON.map(([p, info]) => ({ label: p, info, section: S_COMMON })),
  ]
    .filter((o) => !present.has(o.label) && !seen.has(o.label) && seen.add(o.label))
    .map((o) => ({ ...o, type: "text" }))
  return options.length ? { from: pos - prefix.length, options, validFor: /^[\w.*/-]*$/ } : null
}
