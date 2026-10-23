import { describe, expect, test } from "bun:test"
import { gunzipSync } from "node:zlib"
import { readFileSync } from "node:fs"
import type { CompletionResult } from "@codemirror/autocomplete"
import { yamlContextAt, type KeyContext, type ValueContext } from "@/features/code-intel/yaml/context"
import { keysAt } from "@/features/code-intel/yaml/schema"
import { schemaCompletions } from "@/features/code-intel/yaml/schema-intel"
import { parseYaml } from "@/features/code-intel/yaml/tree"
import { validateYaml } from "@/features/code-intel/yaml/validate"
import type { KubeSchema } from "@/lib/api/types"
import { buildKubeSchemaIndex, docGvk, gvkValues, headerKeys, kindDocsUrl, knownFieldValues } from "./k8s-schema"

// The payload GET /catalog/kubernetes/schema serves: the daemon's embedded reduced swagger of Kubernetes 1.36.
const raw = JSON.parse(
  gunzipSync(readFileSync(`${import.meta.dir}/../../../../../../backend/internal/kube/kuberules/schema/kubernetes-1.36.json.gz`)).toString("utf8"),
) as KubeSchema
const index = buildKubeSchemaIndex(raw)

function problems(text: string, partial = false) {
  return parseYaml(text).flatMap((d) => {
    const { apiVersion, kind } = docGvk(d.root)
    const schema = apiVersion && kind ? index.schemaFor(apiVersion, kind, partial) : null
    return schema ? validateYaml(d.root, schema) : []
  })
}

const DEPLOYMENT = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
  labels: {app: web}
spec:
  replicas: 2
  selector:
    matchLabels:
      app: web
  strategy:
    rollingUpdate:
      maxSurge: 25%
      maxUnavailable: 1
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: web
          image: nginx:1.27
          ports:
            - containerPort: 80
              name: http
          resources:
            requests:
              cpu: 1
              memory: 128Mi
            limits:
              cpu: 0.5
          readinessProbe:
            httpGet:
              path: /
              port: http
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
  ports:
    - port: 80
      targetPort: 8080
`

describe("kubernetes schema index", () => {
  test("indexes every served kind by apiVersion/kind", () => {
    expect(index.version).toBe("1.36.5")
    expect(index.gvk("apps/v1", "Deployment")?.namespaced).toBe(true)
    expect(index.gvk("v1", "Namespace")?.namespaced).toBe(false)
    expect(index.gvk("networking.k8s.io/v1", "Ingress")).toBeDefined()
    expect(index.gvks.some((g) => g.kind === "WatchEvent")).toBe(false)
  })

  test("valid manifests have no problems (int-or-string, quantities, flow mappings, multi-doc)", () => {
    expect(problems(DEPLOYMENT)).toEqual([])
  })

  test("reports unknown fields, wrong types and missing required fields", () => {
    const text = DEPLOYMENT.replace("replicas: 2", "replicas: two").replace("          image: nginx:1.27\n", "          imagee: nginx:1.27\n").replace("        - name: web\n", "        - imagePullPolicy: Always\n")
    const msgs = problems(text).map((p) => p.message)
    expect(msgs.some((m) => /Expected number/.test(m))).toBe(true)
    expect(msgs.some((m) => /Unknown key “imagee”.*did you mean “image”/.test(m))).toBe(true)
    expect(msgs.some((m) => /Missing required key “name”/.test(m))).toBe(true)
  })

  test("partial schemas (Kustomize patches) don't require fields", () => {
    const patch = "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    spec:\n      containers:\n        - image: nginx:1.28\n"
    expect(problems(patch).some((p) => /required key “name”/.test(p.message))).toBe(true)
    expect(problems(patch, true)).toEqual([])
  })

  test("custom resources get no schema", () => {
    expect(index.schemaFor("cert-manager.io/v1", "Certificate")).toBeNull()
    expect(problems("apiVersion: cert-manager.io/v1\nkind: Certificate\nspec:\n  anything: [1, 2]\n")).toEqual([])
  })

  test("keys at a path come from the resolved definitions", () => {
    const schema = index.schemaFor("apps/v1", "Deployment")!
    const names = keysAt(schema, ["spec", "template", "spec", "containers", "[]"]).map((k) => k.name)
    expect(names).toContain("readinessProbe")
    expect(names).toContain("imagePullPolicy")
    const res = schemaCompletions("apiVersion: apps/v1\nkind: Deployment\nspec:\n  repl", 50, false, schema) as CompletionResult
    expect(res.options.map((o) => o.label)).toContain("replicas")
  })

  test("docs links name the kind's reference anchor", () => {
    expect(kindDocsUrl("1.36.5", index.gvk("apps/v1", "Deployment")!)).toBe("https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#deployment-v1-apps")
    expect(kindDocsUrl("1.36.5", index.gvk("networking.k8s.io/v1", "Ingress")!)).toBe(
      "https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.36/#ingress-v1-networking-k8s-io",
    )
    expect(kindDocsUrl("1.36.5", index.gvk("v1", "Service")!)).toMatch(/#service-v1-core$/)
  })
})

describe("apiVersion and kind", () => {
  const valueCtx = (text: string) => yamlContextAt(text, text.length) as ValueContext

  test("kind values narrow to the written apiVersion", () => {
    const text = "apiVersion: apps/v1\nkind: "
    const labels = gvkValues(index, valueCtx(text), parseYaml(text)[0].root).map((o) => o.label)
    expect(labels).toContain("Deployment")
    expect(labels).toContain("StatefulSet")
    expect(labels).not.toContain("Service")
  })

  test("apiVersion values narrow to the versions serving the written kind", () => {
    const text = "kind: HorizontalPodAutoscaler\napiVersion: "
    const labels = gvkValues(index, valueCtx(text), parseYaml(text)[0].root).map((o) => o.label)
    expect(labels).toEqual(["autoscaling/v2", "autoscaling/v1"].filter((v) => labels.includes(v)))
    expect(labels).toContain("autoscaling/v2")
  })

  test("a document without apiVersion/kind offers whole headers", () => {
    const text = "---\nDep"
    const ctx = yamlContextAt(text, text.length) as KeyContext
    const res = headerKeys(index, ctx, null)!
    const dep = res.options.find((o) => o.label === "Deployment")!
    expect(dep.apply).toBe("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: ")
    expect(res.options.map((o) => o.label)).toContain("apiVersion")
  })

  test("headers are not offered once the document has them", () => {
    const text = "apiVersion: v1\nkind: Service\nme"
    const ctx = yamlContextAt(text, text.length) as KeyContext
    expect(headerKeys(index, ctx, parseYaml(text)[0].root)).toBeNull()
  })

  test("well-known field values", () => {
    const at = (text: string, kind?: string) => knownFieldValues(yamlContextAt(text, text.length) as ValueContext, kind).map((o) => o.label)
    expect(at("spec:\n  containers:\n    - imagePullPolicy: ")).toContain("IfNotPresent")
    expect(at("spec:\n  type: ", "Service")).toContain("LoadBalancer")
    expect(at("spec:\n  type: ", "Secret")).toEqual([])
    expect(at("spec:\n  accessModes:\n    - ")).toContain("ReadWriteOnce")
  })
})
