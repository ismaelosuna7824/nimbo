// Environments of a Docker workspace, the official compose pattern: an override file next to the main compose file
// (compose.<env>.yaml) and/or an env file (.env.<env> or <env>.env) read after .env, like
// `docker compose -f compose.yaml [-f compose.<env>.yaml] --env-file .env --env-file <env file>`.

import type { ComposeEnvironment, ComposeModel, EnvironmentInfo, EnvironmentVariable, ServiceFacts } from "@/lib/api/types"

type Envs = Pick<ComposeModel, "files" | "environment"> & { environments?: ComposeEnvironment[] }

/** The environment a workflow file belongs to: its override file or its env file (undefined for shared files). */
export function environmentOfFile(compose: Envs | undefined, path: string): ComposeEnvironment | undefined {
  return compose?.environments?.find((e) => e.file === path || envFilePath(e) === path)
}

/**
 * Where an environment's variables live: its env file as named (.env.<env> or <env>.env), else the .env.<env> a
 * first variable creates next to its override file.
 */
export function envFilePath(env: Pick<ComposeEnvironment, "name" | "file" | "envFile">): string {
  if (env.envFile) return env.envFile
  const file = env.file ?? ""
  return `${file.slice(0, file.lastIndexOf("/") + 1)}.env.${env.name}`
}

/** The env file of an environment of the Environments tab: the one of its files that isn't a compose file. */
export function infoEnvFile(info: Pick<EnvironmentInfo, "name" | "files">): string {
  const envFile = info.files.find((f) => !/\.ya?ml$/.test(f))
  return envFilePath({ name: info.name, file: info.files[0], envFile })
}

/** The project's .env next to an environment's env file. */
export function baseEnvFile(envFile: string): string {
  return `${envFile.slice(0, envFile.lastIndexOf("/") + 1)}.env`
}

/**
 * Compose files whose ${VAR}s an env file feeds: the base files, plus the override of its environment (its env file)
 * or of every environment (the shared .env).
 */
export function envFileComposeFiles(compose: Envs | undefined, path: string): string[] {
  const files = compose?.files ?? []
  const overrides = (envs: ComposeEnvironment[]) => envs.flatMap((e) => (e.file ? [e.file] : []))
  if (path.slice(path.lastIndexOf("/") + 1) === ".env") return [...files, ...overrides(compose?.environments ?? [])]
  const env = environmentOfFile(compose, path)
  return env ? [...files, ...overrides([env])] : files
}

/**
 * The env file ${VAR}s of a compose file resolve from before .env: for an override, its environment's env file; for
 * a base file, the active environment's env file when it has one.
 */
export function envFileOfCompose(compose: Envs | undefined, path: string): string | undefined {
  const env = compose?.environments?.find((e) => e.file === path)
  if (env) return envFilePath(env)
  const active = compose?.files.includes(path) ? compose.environments?.find((e) => e.name === compose.environment) : undefined
  return active?.envFile
}

/**
 * True when the active environment's override file sets one of the attributes (`image`, `ports`, `deploy.replicas`…);
 * `deploy` matches every `deploy.*` key.
 */
export function overridden(facts: ServiceFacts | undefined, ...keys: string[]): boolean {
  const set = facts?.overrides ?? []
  return keys.some((k) => set.includes(k) || (k === "deploy" && set.some((s) => s.startsWith("deploy."))))
}

/** Value of `key` in a .env text (last assignment wins; quotes and inline comments handled like compose's parser). */
export function dotenvValue(text: string, key: string): string | undefined {
  let out: string | undefined
  for (const line of text.split("\n")) {
    const m = /^\s*(?:export\s+)?([A-Za-z_][\w.-]*)\s*[=:]\s*(.*)$/.exec(line)
    if (!m || m[1] !== key) continue
    const raw = m[2]
    if (raw.startsWith('"')) {
      const end = raw.lastIndexOf('"')
      out = (end > 0 ? raw.slice(1, end) : raw.slice(1)).replace(/\\n/g, "\n").replace(/\\r/g, "\r").replace(/\\(["\\])/g, "$1")
    } else if (raw.startsWith("'")) {
      const end = raw.lastIndexOf("'")
      out = end > 0 ? raw.slice(1, end) : raw.slice(1)
    } else {
      out = raw.replace(/\s+#.*$/, "").trim()
    }
  }
  return out
}

/** Variables shown first: the environment's own, then inherited ones; by name within each. */
export function sortVariables(vars: EnvironmentVariable[]): EnvironmentVariable[] {
  return [...vars].sort((a, b) => Number(b.overridden) - Number(a.overridden) || a.key.localeCompare(b.key))
}

/** Why `key` can't be a variable name (null: it can); `existing` are the environment's own keys. */
export function variableKeyError(key: string, existing: string[]): string | null {
  if (key === "") return "Enter a name."
  if (!/^[A-Za-z_][A-Za-z0-9_.-]*$/.test(key)) return "Letters, digits and _ (not starting with a digit)."
  if (existing.includes(key)) return `${key} is already set here.`
  return null
}
