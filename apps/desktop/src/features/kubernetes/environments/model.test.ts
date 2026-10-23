import { describe, expect, test } from "bun:test"
import type { EnvCompareGroup, EnvironmentSource, EnvSetting } from "@/lib/api/types"
import {
  cellDiffers,
  environmentNameError,
  missingDefaults,
  newEnvironmentFiles,
  settingsPatch,
  settingValueError,
  renamedFiles,
  visibleGroups,
} from "./model"

describe("environment names", () => {
  test("accepts DNS labels", () => {
    expect(environmentNameError("dev", [])).toBeNull()
    expect(environmentNameError("eu-west-1", [])).toBeNull()
    expect(environmentNameError("a".repeat(63), [])).toBeNull()
  })
  test("rejects empty, invalid, reserved and taken names", () => {
    expect(environmentNameError("", [])).not.toBeNull()
    expect(environmentNameError("Prod", [])).not.toBeNull()
    expect(environmentNameError("-dev", [])).not.toBeNull()
    expect(environmentNameError("dev-", [])).not.toBeNull()
    expect(environmentNameError("dev_1", [])).not.toBeNull()
    expect(environmentNameError("a".repeat(64), [])).not.toBeNull()
    expect(environmentNameError("base", [])).toContain("base")
    expect(environmentNameError("prod", ["dev", "prod"])).toContain("already exists")
  })
  test("a rename may keep its own name", () => {
    expect(environmentNameError("prod", ["dev", "prod"], "prod")).toBeNull()
  })
})

const groups: EnvCompareGroup[] = [
  {
    id: "deployment/web",
    label: "web",
    kind: "Deployment",
    rows: [
      { key: "replicas", label: "Replicas", values: ["1", "1", "3"], differs: true },
      { key: "image", label: "nginx image", values: ["nginx:1", "nginx:1", "nginx:1"], differs: false },
    ],
  },
  {
    id: "configmap/cfg",
    label: "cfg",
    kind: "ConfigMap",
    rows: [{ key: "LOG", label: "LOG", values: ["info", "info", "info"], differs: false }],
  },
]

describe("compare matrix", () => {
  test("a cell differs when it is not the base", () => {
    const row = groups[0].rows[0]
    expect(cellDiffers(row, 0)).toBe(false)
    expect(cellDiffers(row, 1)).toBe(false)
    expect(cellDiffers(row, 2)).toBe(true)
    expect(cellDiffers({ key: "x", label: "x", values: ["", "2"], differs: true }, 1)).toBe(true)
    expect(cellDiffers({ key: "x", label: "x", values: ["1"], differs: true }, 1)).toBe(true)
  })
  test("only differences keeps differing rows and drops empty groups", () => {
    const shown = visibleGroups(groups, true)
    expect(shown).toHaveLength(1)
    expect(shown[0].rows.map((r) => r.key)).toEqual(["replicas"])
    expect(visibleGroups(groups, false)).toBe(groups)
  })
})

const setting = (s: Partial<EnvSetting> & { key: string }): EnvSetting => ({
  label: s.key,
  group: "Scaling",
  type: "int",
  value: "",
  overridden: false,
  editable: true,
  ...s,
})

describe("settings patch", () => {
  const settings = [
    setting({ key: "replicas", value: "3", base: "1", overridden: true }),
    setting({ key: "cpu", type: "cpu", value: "100m", base: "100m" }),
    setting({ key: "memory", type: "memory", value: "256Mi", base: "128Mi", overridden: true }),
    setting({ key: "locked", value: "2", base: "2", editable: false }),
  ]
  test("sends only changed keys", () => {
    expect(settingsPatch(settings, {})).toEqual({})
    expect(settingsPatch(settings, { replicas: "3", cpu: "" })).toEqual({})
    expect(settingsPatch(settings, { replicas: " 5 ", cpu: "250m" })).toEqual({ replicas: "5", cpu: "250m" })
  })
  test("emptying an override drops it", () => {
    expect(settingsPatch(settings, { memory: "" })).toEqual({ memory: null })
  })
  test("ignores knobs that can't be edited", () => {
    expect(settingsPatch(settings, { locked: "4" })).toEqual({})
  })
  test("checks values by type", () => {
    expect(settingValueError("int", "3")).toBeNull()
    expect(settingValueError("int", "3.5")).not.toBeNull()
    expect(settingValueError("cpu", "250m")).toBeNull()
    expect(settingValueError("cpu", "0.5")).toBeNull()
    expect(settingValueError("cpu", "lots")).not.toBeNull()
    expect(settingValueError("memory", "1Gi")).toBeNull()
    expect(settingValueError("memory", "1GB")).not.toBeNull()
    expect(settingValueError("string", "anything")).toBeNull()
    expect(settingValueError("int", "")).toBeNull()
  })
})

describe("new environments", () => {
  const sources: EnvironmentSource[] = [
    { label: "app", kind: "kustomize", path: "k8s/base", supported: true },
    { label: "api", kind: "kustomize", path: "deploy", supported: true },
    { label: "chart", kind: "helm", path: "chart", supported: true },
    { label: "raw", kind: "manifests", path: "manifests", supported: false },
  ]
  test("lists the files created per source", () => {
    expect(newEnvironmentFiles(sources, "staging")).toEqual([
      "k8s/overlays/staging/kustomization.yaml",
      "deploy/overlays/staging/kustomization.yaml",
      "chart/values-staging.yaml",
    ])
    expect(newEnvironmentFiles([{ label: "root", kind: "kustomize", path: "base", supported: true }], "dev")).toEqual([
      "overlays/dev/kustomization.yaml",
    ])
  })
  test("defaults skip existing environments", () => {
    expect(missingDefaults([])).toEqual(["dev", "prod"])
    expect(missingDefaults(["prod"])).toEqual(["dev"])
  })
  test("copies keep the layout of the source environment", () => {
    expect(renamedFiles(["overlays/prod", "chart/values-prod.yaml"], "prod", "staging")).toEqual([
      "overlays/staging",
      "chart/values-staging.yaml",
    ])
    expect(renamedFiles(["prod/overlays/prod"], "prod", "qa")).toEqual(["prod/overlays/qa"])
  })
})
