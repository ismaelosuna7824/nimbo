import { describe, expect, test } from "bun:test"
import type { KubeField } from "@/lib/api/types"
import {
  budgetPatch,
  dataEntries,
  dataPatch,
  capAddPatch,
  fieldList,
  hardeningItems,
  labelsPatch,
  runAsUserPatch,
  spreadOf,
  spreadPatch,
  strategyOf,
  strategyPatch,
  strategyProblem,
  withPodSecurity,
} from "./workload-extras"

const f = (path: string, value: unknown): KubeField => ({ path, value, editable: true })
const POD = "spec.template.spec"
const C = `${POD}.containers[name=web]`

describe("fields", () => {
  test("list items in index order", () => {
    expect(fieldList([f(`${C}.securityContext.capabilities.drop[1]`, "NET_RAW"), f(`${C}.securityContext.capabilities.drop[0]`, "ALL")], `${C}.securityContext.capabilities.drop`)).toEqual(["ALL", "NET_RAW"])
  })
})

describe("rollout strategy", () => {
  test("reads the defaults and the written limits", () => {
    expect(strategyOf([])).toEqual({ type: "RollingUpdate", maxUnavailable: "", maxSurge: "" })
    expect(strategyOf([f("spec.strategy.type", "Recreate")]).type).toBe("Recreate")
    expect(strategyOf([f("spec.strategy.rollingUpdate.maxUnavailable", 0), f("spec.strategy.rollingUpdate.maxSurge", "25%")])).toEqual({ type: "RollingUpdate", maxUnavailable: "0", maxSurge: "25%" })
  })

  test("Recreate drops rollingUpdate; RollingUpdate writes numbers and percentages", () => {
    expect(strategyPatch({ type: "Recreate", maxUnavailable: "0", maxSurge: "1" })).toEqual({ set: { "spec.strategy.type": "Recreate" }, unset: ["spec.strategy.rollingUpdate"] })
    expect(strategyPatch({ type: "RollingUpdate", maxUnavailable: "0", maxSurge: "25%" })).toEqual({
      set: { "spec.strategy.type": "RollingUpdate", "spec.strategy.rollingUpdate.maxUnavailable": 0, "spec.strategy.rollingUpdate.maxSurge": "25%" },
      unset: [],
    })
    expect(strategyPatch({ type: "RollingUpdate", maxUnavailable: "", maxSurge: "1" }).unset).toEqual(["spec.strategy.rollingUpdate.maxUnavailable"])
  })

  test("both limits 0 is refused", () => {
    expect(strategyProblem({ type: "RollingUpdate", maxUnavailable: "0", maxSurge: "0%" })).toBeDefined()
    expect(strategyProblem({ type: "RollingUpdate", maxUnavailable: "0", maxSurge: "1" })).toBeUndefined()
  })
})

describe("topology spread", () => {
  const at = `${POD}.topologySpreadConstraints`
  const preset = (key: string, labels: Record<string, string> = { app: "web" }) => [
    f(`${at}[0].maxSkew`, 1),
    f(`${at}[0].topologyKey`, key),
    f(`${at}[0].whenUnsatisfiable`, "ScheduleAnyway"),
    ...Object.entries(labels).map(([k, v]) => f(`${at}[0].labelSelector.matchLabels.${k}`, v)),
  ]

  test("recognizes the presets, anything else is custom", () => {
    expect(spreadOf([], POD, { app: "web" })).toBe("off")
    expect(spreadOf(preset("kubernetes.io/hostname"), POD, { app: "web" })).toBe("nodes")
    expect(spreadOf(preset("topology.kubernetes.io/zone"), POD, { app: "web" })).toBe("zones")
    expect(spreadOf(preset("topology.kubernetes.io/zone", { app: "other" }), POD, { app: "web" })).toBe("custom")
    expect(spreadOf([...preset("kubernetes.io/hostname"), f(`${at}[1].maxSkew`, 1)], POD, { app: "web" })).toBe("custom")
  })

  test("writes one constraint counting the workload's pods", () => {
    expect(spreadPatch(POD, "zones", { app: "web" })).toEqual({
      set: { [at]: [{ maxSkew: 1, topologyKey: "topology.kubernetes.io/zone", whenUnsatisfiable: "ScheduleAnyway", labelSelector: { matchLabels: { app: "web" } } }] },
    })
    expect(spreadPatch(POD, "off", { app: "web" })).toEqual({ unset: [at] })
  })
})

describe("hardening", () => {
  const ok = (fields: KubeField[]) => Object.fromEntries(hardeningItems(fields, POD, C).map((i) => [i.id, i.ok]))

  test("reads the pod and container security contexts", () => {
    expect(ok([f(`${C}.securityContext.privileged`, true)])).toEqual({ "non-root": false, seccomp: false, "not-privileged": false, "no-escalation": false, "read-only": false, "drop-all": false })
    expect(
      ok([
        f(`${POD}.securityContext.runAsUser`, 101),
        f(`${POD}.securityContext.seccompProfile.type`, "RuntimeDefault"),
        f(`${C}.securityContext.allowPrivilegeEscalation`, false),
        f(`${C}.securityContext.readOnlyRootFilesystem`, true),
        f(`${C}.securityContext.capabilities.drop[0]`, "ALL"),
      ]),
    ).toEqual({ "non-root": true, seccomp: true, "not-privileged": true, "no-escalation": true, "read-only": true, "drop-all": true })
  })

  test("each switch writes its setting or removes it; privileged can only be turned off", () => {
    const items = Object.fromEntries(hardeningItems([], POD, C).map((i) => [i.id, i]))
    expect(items["non-root"]!.patch(true)).toEqual({ set: { [`${POD}.securityContext.runAsNonRoot`]: true } })
    expect(items.seccomp!.patch(true)).toEqual({ set: { [`${POD}.securityContext.seccompProfile`]: { type: "RuntimeDefault" } } })
    expect(items["drop-all"]!.patch(true)).toEqual({ set: { [`${C}.securityContext.capabilities.drop`]: ["ALL"] } })
    expect(items["read-only"]!.patch(false)).toEqual({ unset: [`${C}.securityContext.readOnlyRootFilesystem`] })
    expect(items["not-privileged"]!.patch(true)).toEqual({ unset: [`${C}.securityContext.privileged`] })
    expect(items["not-privileged"]!.patch(false)).toBeUndefined()
  })

  test("user ID and added capabilities", () => {
    expect(runAsUserPatch(POD, "10001")).toEqual({ set: { [`${POD}.securityContext.runAsUser`]: 10001 } })
    expect(runAsUserPatch(POD, "")).toEqual({ unset: [`${POD}.securityContext.runAsUser`] })
    expect(capAddPatch(C, [])).toEqual({ unset: [`${C}.securityContext.capabilities.add`] })
    expect(capAddPatch(C, ["NET_BIND_SERVICE"])).toEqual({ set: { [`${C}.securityContext.capabilities.add`]: ["NET_BIND_SERVICE"] } })
  })
})

describe("namespace and budget", () => {
  test("Pod Security level as the enforce label", () => {
    expect(withPodSecurity({ team: "a" }, "restricted")).toEqual({ team: "a", "pod-security.kubernetes.io/enforce": "restricted" })
    expect(withPodSecurity({ "pod-security.kubernetes.io/enforce": "baseline" }, "")).toEqual({})
    expect(labelsPatch({})).toEqual({ unset: ["metadata.labels"] })
  })

  test("one budget field at a time", () => {
    expect(budgetPatch("maxUnavailable", "1")).toEqual({ set: { "spec.maxUnavailable": 1 }, unset: ["spec.minAvailable"] })
    expect(budgetPatch("minAvailable", "50%")).toEqual({ set: { "spec.minAvailable": "50%" }, unset: ["spec.maxUnavailable"] })
  })
})

describe("ConfigMap and Secret keys", () => {
  test("reads keys, ConfigMap values only", () => {
    const fields = [f("data.LOG_LEVEL", "info"), f('data["app.conf"]', "x=1"), f("metadata.name", "cfg")]
    expect(dataEntries(fields, "ConfigMap")).toEqual([
      { key: "LOG_LEVEL", value: "info", from: "data" },
      { key: "app.conf", value: "x=1", from: "data" },
    ])
    expect(dataEntries([f("data.TOKEN", "c2VjcmV0"), f("stringData.PASSWORD", "pw")], "Secret")).toEqual([
      { key: "TOKEN", value: "", from: "data" },
      { key: "PASSWORD", value: "", from: "stringData" },
    ])
  })

  test("ConfigMap edits write data, removals unset", () => {
    const before = [
      { key: "LOG_LEVEL", value: "info", from: "data" as const },
      { key: "API_TOKEN", value: "tok", from: "data" as const },
    ]
    expect(dataPatch("ConfigMap", before, [{ key: "LOG_LEVEL", value: "debug", from: "data" }])).toEqual({ set: { "data.LOG_LEVEL": "debug" }, unset: ["data.API_TOKEN"] })
  })

  test("Secret values typed go to stringData, untouched ones stay", () => {
    const before = [
      { key: "TOKEN", value: "", from: "data" as const },
      { key: "USER", value: "", from: "stringData" as const },
    ]
    expect(
      dataPatch("Secret", before, [
        { key: "TOKEN", value: "new", from: "data" },
        { key: "USER", value: "", from: "stringData" },
        { key: "DB_PASSWORD", value: "pw", from: "stringData" },
      ]),
    ).toEqual({ set: { "stringData.TOKEN": "new", "stringData.DB_PASSWORD": "pw" }, unset: ["data.TOKEN"] })
    expect(dataPatch("Secret", before, [])).toEqual({ set: {}, unset: ["data.TOKEN", "stringData.USER"] })
  })
})
