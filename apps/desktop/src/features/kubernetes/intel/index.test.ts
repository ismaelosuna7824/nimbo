import { describe, expect, test } from "bun:test"
import type { KubernetesModel } from "@/lib/api/types"

// The workflow store reads localStorage at import: a memory stand-in, then the module.
const memory = new Map<string, string>()
globalThis.localStorage ??= {
  getItem: (k: string) => memory.get(k) ?? null,
  setItem: (k: string, v: string) => void memory.set(k, v),
  removeItem: (k: string) => void memory.delete(k),
  clear: () => memory.clear(),
  key: (i: number) => [...memory.keys()][i] ?? null,
  get length() {
    return memory.size
  },
}
const { kubernetesIntel } = await import("./index")

const kube: KubernetesModel = {
  sources: [
    { kind: "manifests", path: "k8s", objects: 1 },
    { kind: "helm", path: "chart", objects: 1 },
  ],
  files: ["k8s/app.yaml", "chart/Chart.yaml", "chart/values.yaml", "chart/templates/deployment.yaml"],
  graph: { nodes: [], edges: [] },
}

const intel = (path: string) =>
  kubernetesIntel({ workflowId: "wf-intel-test", path, buffers: () => ({}), openFile: () => {}, kube: () => kube })

describe("kubernetesIntel", () => {
  test("extensions for manifests, kustomizations and chart files", () => {
    for (const p of ["k8s/app.yaml", "k8s/kustomization.yaml", "chart/Chart.yaml", "chart/values.yaml", "chart/values-prod.yaml", "chart/templates/deployment.yaml", "chart/templates/_helpers.tpl"]) {
      const ext = intel(p)
      expect(ext, p).not.toBeNull()
      expect(ext!.length, p).toBeGreaterThan(0)
    }
  })

  test("null for other files", () => {
    expect(intel("src/app.py")).toBeNull()
    expect(intel("Dockerfile")).toBeNull()
    expect(intel("chart/templates/NOTES.txt")).toBeNull()
  })
})
