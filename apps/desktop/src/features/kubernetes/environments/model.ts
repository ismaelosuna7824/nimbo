import type { EnvCompareGroup, EnvCompareRow, EnvironmentSource, EnvSetting } from "@/lib/api/types"

/** A Kubernetes DNS label: what overlay dirs, values files and namespaces derived from the name accept. */
const NAME_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/

/** Why `name` can't be a new environment name (null: it can). `current` is the environment being renamed. */
export function environmentNameError(name: string, existing: string[], current?: string): string | null {
  if (name === "") return "Enter a name."
  if (!NAME_RE.test(name)) return "Use lowercase letters, digits and dashes (at most 63), starting and ending with a letter or digit."
  if (name === "base") return "“base” is the shared configuration every environment builds on."
  if (name !== current && existing.includes(name)) return `“${name}” already exists.`
  return null
}

/** True when column `col` shows something else than the base (column 0) in this row. */
export function cellDiffers(row: EnvCompareRow, col: number): boolean {
  return col > 0 && (row.values[col] ?? "") !== (row.values[0] ?? "")
}

/** Groups with only the rows that differ between environments (groups left empty are dropped) when `onlyDiffs`. */
export function visibleGroups(groups: EnvCompareGroup[], onlyDiffs: boolean): EnvCompareGroup[] {
  if (!onlyDiffs) return groups
  return groups.flatMap((g) => {
    const rows = g.rows.filter((r) => r.differs)
    return rows.length > 0 ? [{ ...g, rows }] : []
  })
}

/** What the knob's input starts with: the environment's own value, empty when it inherits the base. */
export function initialSettingValue(s: EnvSetting): string {
  return s.overridden ? s.value : ""
}

/**
 * PATCH …/settings values from the edited inputs: only keys whose input changed; an emptied input of a value this
 * environment set drops the override (null, back to the base).
 */
export function settingsPatch(settings: EnvSetting[], draft: Record<string, string>): Record<string, string | null> {
  const values: Record<string, string | null> = {}
  for (const s of settings) {
    if (!s.editable || !(s.key in draft)) continue
    const next = draft[s.key].trim()
    if (next === initialSettingValue(s)) continue
    if (next === "") {
      if (s.overridden) values[s.key] = null
      continue
    }
    values[s.key] = next
  }
  return values
}

/** Client-side check of a knob's input ("" = inherit the base). */
export function settingValueError(type: EnvSetting["type"], value: string, kind: "kubernetes" | "docker" = "kubernetes"): string | null {
  const v = value.trim()
  if (v === "") return null
  // Compose takes cpus as a number of CPUs and memory with b/k/m/g units (512M, 1g, 1GB).
  const docker = kind === "docker"
  switch (type) {
    case "int":
      return /^\d+$/.test(v) ? null : "Whole number"
    case "cpu":
      if (docker) return /^(\d+(\.\d+)?|\.\d+)$/.test(v) ? null : "CPUs like 0.5 or 2"
      return /^(\d+(\.\d+)?|\.\d+)m?$/.test(v) ? null : "CPU like 250m or 0.5"
    case "memory":
      if (docker) return /^\d+(\.\d+)?([bkmgBKMG]|[kmgKMG][bB])?$/.test(v) ? null : "Memory like 512M or 1G"
      return /^\d+(\.\d+)?(Ki|Mi|Gi|Ti|Pi|Ei|k|M|G|T|P|E)?$/.test(v) ? null : "Memory like 256Mi or 1Gi"
    default:
      return null
  }
}

/** Sources that take environments (Kustomize, Helm and compose projects; plain manifests are the same everywhere). */
export function environmentSources(sources: EnvironmentSource[]): EnvironmentSource[] {
  return sources.filter((s) => s.supported)
}

/**
 * Files a new environment adds per source: a Kustomize overlay next to the base (`k8s/base` → `k8s/overlays/<name>`,
 * other base dirs get `<dir>/overlays/<name>`), a Helm values file in the chart dir, a compose override file next to
 * the main compose file (`compose.yaml` → `compose.<name>.yaml`, `docker-compose.yml` → `docker-compose.<name>.yml`).
 */
export function newEnvironmentFiles(sources: EnvironmentSource[], name: string): string[] {
  const env = name || "<name>"
  return environmentSources(sources).flatMap((s) => {
    const path = s.path.replace(/\/+$/, "")
    if (s.kind === "helm") return [join(path, `values-${env}.yaml`)]
    if (s.kind === "kustomize") {
      const parts = path.split("/")
      const dir = parts.at(-1) === "base" ? parts.slice(0, -1).join("/") : path
      return [join(dir, `overlays/${env}/kustomization.yaml`)]
    }
    if (s.kind === "compose") {
      const m = /^(.*\/)?(docker-compose|compose)(?:\.[^./]+)?\.(ya?ml)$/.exec(path)
      return [m ? `${m[1] ?? ""}${m[2]}.${env}.${m[3]}` : `compose.${env}.yaml`]
    }
    return []
  })
}

function join(dir: string, file: string): string {
  return dir === "" || dir === "." ? file : `${dir}/${file}`
}

/** The environments "Create dev and prod" adds: those not there yet. */
export function missingDefaults(existing: string[]): string[] {
  return ["dev", "prod"].filter((n) => !existing.includes(n))
}

/** Files of `from` under a new name (`overlays/prod` → `overlays/<name>`, `chart/values-prod.yaml` → `chart/values-<name>.yaml`). */
export function renamedFiles(files: string[], from: string, name: string): string[] {
  const next = name || "<name>"
  return files.map((f) => {
    const i = f.lastIndexOf(from)
    return i < 0 ? f : f.slice(0, i) + next + f.slice(i + from.length)
  })
}
