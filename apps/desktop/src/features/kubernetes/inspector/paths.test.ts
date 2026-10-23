import { describe, expect, test } from "bun:test"
import type { KubeField, KubeNode } from "@/lib/api/types"
import { containerPath, envPatch, fieldAccess, hpaMetrics, ingressRules, probeObject, valuesHint, volumesPatch } from "./paths"

const node = (over: Partial<KubeNode> = {}): KubeNode => ({
  id: "Deployment/default/web",
  kind: "Deployment",
  apiVersion: "apps/v1",
  name: "web",
  namespace: "default",
  category: "workload",
  source: 0,
  sourceKind: "manifests",
  sourcePath: "k8s",
  editable: true,
  ...over,
})

const field = (path: string, over: Partial<KubeField> = {}): KubeField => ({ path, value: null, editable: true, ...over })

describe("paths", () => {
  test("container paths follow the pod template of each kind", () => {
    expect(containerPath("Deployment", "web")).toBe("spec.template.spec.containers[name=web]")
    expect(containerPath("CronJob", "job")).toBe("spec.jobTemplate.spec.template.spec.containers[name=job]")
    expect(containerPath("Pod", "x")).toBe("spec.containers[name=x]")
  })
})

describe("fieldAccess", () => {
  const image = "spec.template.spec.containers[name=web].image"

  test("a listed field decides", () => {
    const f = field(image, { editable: false, reason: "computed by a template expression", file: "templates/deploy.yaml", line: 12 })
    expect(fieldAccess([f], image, node({ sourceKind: "helm", editable: false }))).toEqual({ editable: false, reason: f.reason, field: f })
  })

  test("a read-only item makes its list read-only", () => {
    const f = field("spec.template.spec.containers[name=web].env[name=A].value", { editable: false, reason: "range loop" })
    expect(fieldAccess([f], "spec.template.spec.containers[name=web].env", node()).editable).toBe(false)
  })

  test("missing paths follow the object: manifests and Helm write them, generated objects don't", () => {
    expect(fieldAccess([], "spec.replicas", node()).editable).toBe(true)
    expect(fieldAccess([], "spec.replicas", node({ sourceKind: "helm", editable: false })).editable).toBe(true)
    expect(fieldAccess([], "spec.replicas", node({ sourceKind: "kustomize", editable: false })).editable).toBe(false)
  })

  test("Helm values hint", () => {
    expect(valuesHint(field(image, { via: "values", file: "chart/values.yaml", valuesKeys: ["image.repository", "image.tag"] }))).toBe("values.yaml: image.repository, image.tag")
    expect(valuesHint(field(image, { via: "manifest" }))).toBeUndefined()
  })
})

describe("values", () => {
  test("probes", () => {
    expect(probeObject({ type: "http", path: "/healthz", port: "8080", periodSeconds: 5 })).toEqual({ httpGet: { path: "/healthz", port: 8080 }, periodSeconds: 5 })
    expect(probeObject({ type: "tcp", port: "http" })).toEqual({ tcpSocket: { port: "http" } })
    expect(probeObject({ type: "exec", command: "pg_isready" })).toEqual({ exec: { command: ["sh", "-c", "pg_isready"] } })
  })

  test("env edits go variable by variable and keep redacted secrets", () => {
    const c = "spec.template.spec.containers[name=web]"
    const p = envPatch(
      c,
      [{ name: "A", value: "1" }, { name: "PASSWORD", secret: true }, { name: "GONE", value: "x" }],
      [{ name: "A", from: { kind: "ConfigMap", name: "cfg", key: "a" } }, { name: "PASSWORD", secret: true }, { name: "NEW", value: "2" }],
    )
    expect(p.set).toEqual({
      [`${c}.env[name=A].valueFrom`]: { configMapKeyRef: { name: "cfg", key: "a" } },
      [`${c}.env[name=NEW].value`]: "2",
    })
    expect(p.unset).toEqual([`${c}.env[name=GONE]`, `${c}.env[name=A].value`])
  })

  test("volumes keep the ones the inspector can't show", () => {
    const p = volumesPatch(
      "Deployment",
      [{ name: "host", kind: "hostPath", source: "/var/run" }, { name: "cfg", kind: "configMap", source: "a" }],
      [{ name: "host", kind: "hostPath", source: "/var/run" }, { name: "cfg", kind: "configMap", source: "b" }, { name: "tmp", kind: "emptyDir" }],
    )
    expect(p.set).toEqual({
      "spec.template.spec.volumes[name=cfg]": { name: "cfg", configMap: { name: "b" } },
      "spec.template.spec.volumes[name=tmp]": { name: "tmp", emptyDir: {} },
    })
    expect(p.unset).toEqual([])
  })

  test("ingress rules group routes by host", () => {
    const rules = ingressRules(
      [
        { host: "a.local", path: "/", service: "web", port: "80" },
        { host: "a.local", path: "/api", service: "api" },
        { service: "web", port: "http" },
      ],
      () => 8080,
    )
    expect(rules).toEqual([
      {
        host: "a.local",
        http: {
          paths: [
            { path: "/", pathType: "Prefix", backend: { service: { name: "web", port: { number: 80 } } } },
            { path: "/api", pathType: "Prefix", backend: { service: { name: "api", port: { number: 8080 } } } },
          ],
        },
      },
      { http: { paths: [{ path: "/", pathType: "Prefix", backend: { service: { name: "web", port: { name: "http" } } } }] } },
    ])
  })

  test("hpa metrics", () => {
    expect(hpaMetrics({ cpu: 70 })).toEqual([{ type: "Resource", resource: { name: "cpu", target: { type: "Utilization", averageUtilization: 70 } } }])
    expect(hpaMetrics({})).toEqual([])
  })
})
