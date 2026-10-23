import { describe, expect, test } from "bun:test"
import type { ValueContext } from "@/features/code-intel/yaml/context"
import { yamlContextAt } from "@/features/code-intel/yaml/context"
import { parseYaml } from "@/features/code-intel/yaml/tree"
import { validateYaml } from "@/features/code-intel/yaml/validate"
import type { KubernetesModel } from "@/lib/api/types"
import { joinPath, kubeFileKind, relativePath } from "./files"
import { KUSTOMIZATION_SCHEMA, kustomizeDocsUrl, kustomizePatchFiles, kustomizePathValues } from "./kustomize-schema"

const kube: KubernetesModel = {
  sources: [
    { kind: "kustomize", path: "k8s/base", objects: 2 },
    { kind: "helm", path: "chart", valuesFiles: ["env/shared-values.yaml"], objects: 3 },
  ],
  files: ["k8s/base/kustomization.yaml", "k8s/base/app.yaml", "chart/Chart.yaml", "chart/templates/deployment.yaml"],
  graph: { nodes: [], edges: [] },
}

describe("file kinds", () => {
  const kind = (path: string, paths: string[] = []) => kubeFileKind(path, kube, paths)

  test("manifests and kustomizations", () => {
    expect(kind("k8s/base/app.yaml")).toEqual({ kind: "manifest" })
    expect(kind("k8s/overlays/dev/patch.yml")).toEqual({ kind: "manifest" })
    expect(kind("k8s/base/kustomization.yaml")).toEqual({ kind: "kustomization" })
    expect(kind("k8s/overlays/dev/Kustomization")).toEqual({ kind: "kustomization" })
    expect(kind("src/main.go")).toBeNull()
  })

  test("Helm charts: Chart.yaml, values files, templates", () => {
    expect(kind("chart/Chart.yaml")).toEqual({ kind: "chart", chart: "chart" })
    expect(kind("chart/values.yaml")).toEqual({ kind: "values", chart: "chart" })
    expect(kind("chart/values-prod.yaml")).toEqual({ kind: "values", chart: "chart" })
    expect(kind("chart/values.dev.yaml")).toEqual({ kind: "values", chart: "chart" })
    expect(kind("chart/ci/test-values.yaml")).toEqual({ kind: "values", chart: "chart" })
    expect(kind("env/shared-values.yaml")).toEqual({ kind: "values", chart: "chart" })
    expect(kind("chart/templates/deployment.yaml")).toEqual({ kind: "template", chart: "chart" })
    expect(kind("chart/templates/_helpers.tpl")).toEqual({ kind: "template", chart: "chart" })
    expect(kind("chart/templates/NOTES.txt")).toBeNull()
    expect(kind("chart/crds/crd.yaml")).toBeNull()
  })

  test("charts found by their Chart.yaml (subcharts, charts not deployed)", () => {
    const paths = ["chart/charts/redis/Chart.yaml", "other/Chart.yaml"]
    expect(kind("chart/charts/redis/values.yaml", paths)).toEqual({ kind: "values", chart: "chart/charts/redis" })
    expect(kind("other/templates/x.yaml", paths)).toEqual({ kind: "template", chart: "other" })
  })

  test("paths", () => {
    expect(joinPath("k8s/overlays/dev", "../../base")).toBe("k8s/base")
    expect(joinPath("", "../x")).toBeNull()
    expect(relativePath("k8s/overlays/dev", "k8s/base/app.yaml")).toBe("../../base/app.yaml")
    expect(relativePath("k8s", "k8s/app.yaml")).toBe("app.yaml")
  })
})

describe("kustomization", () => {
  test("schema", () => {
    const ok = `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: shop
resources:
  - ../../base
images:
  - name: web
    newTag: "1.2"
replicas:
  - name: web
    count: 3
patches:
  - path: patch.yaml
    target:
      kind: Deployment
configMapGenerator:
  - name: app
    literals: [LOG_LEVEL=debug]
    behavior: merge
labels:
  - pairs: {team: shop}
    includeSelectors: false
`
    expect(validateYaml(parseYaml(ok)[0].root, KUSTOMIZATION_SCHEMA)).toEqual([])
    const bad = "resource:\n  - app.yaml\nimages:\n  - newTag: v2\nconfigMapGenerator:\n  - name: x\n    behavior: upsert\n"
    const msgs = validateYaml(parseYaml(bad)[0].root, KUSTOMIZATION_SCHEMA).map((p) => p.message)
    expect(msgs.some((m) => /Unknown key “resource”.*“resources”/.test(m))).toBe(true)
    expect(msgs.some((m) => /Missing required key “name”/.test(m))).toBe(true)
    expect(msgs.some((m) => /“upsert” is not one of/.test(m))).toBe(true)
    expect(kustomizeDocsUrl([], "images")).toBe("https://kubectl.docs.kubernetes.io/references/kustomize/kustomization/images/")
  })

  const files = ["k8s/base/kustomization.yaml", "k8s/base/app.yaml", "k8s/components/tls/kustomization.yaml", "k8s/overlays/dev/kustomization.yaml", "k8s/overlays/dev/patch.yaml", "k8s/overlays/dev/app.env", "README.md"]
  const values = (text: string) => kustomizePathValues(yamlContextAt(text, text.length) as ValueContext, "k8s/overlays/dev/kustomization.yaml", files).map((o) => o.label)

  test("resources: files and kustomization directories, relative", () => {
    expect(values("resources:\n  - ")).toEqual(["../../base", "../../base/app.yaml", "../../components/tls", "patch.yaml"])
  })

  test("components: directories only; patches: YAML files; generators: any file", () => {
    expect(values("components:\n  - ")).toEqual(["../../base", "../../components/tls"])
    expect(values("patches:\n  - path: ")).toEqual(["../../base/app.yaml", "patch.yaml"])
    expect(values("configMapGenerator:\n  - name: x\n    envs:\n      - ")).toEqual(["../../base/app.yaml", "patch.yaml", "app.env", "../../../README.md"])
    expect(values("namespace: ")).toEqual([])
  })

  test("patch files are partial objects", () => {
    const k = { "k8s/overlays/dev/kustomization.yaml": "patches:\n  - path: patch.yaml\n  - patch: |-\n      - op: add\npatchesStrategicMerge:\n  - ../shared/sm.yaml\n" }
    expect([...kustomizePatchFiles(k)]).toEqual(["k8s/overlays/dev/patch.yaml", "k8s/overlays/shared/sm.yaml"])
  })
})
