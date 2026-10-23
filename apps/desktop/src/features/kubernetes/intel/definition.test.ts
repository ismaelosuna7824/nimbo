import { describe, expect, test } from "bun:test"
import type { KubeNode } from "@/lib/api/types"
import { manifestDefinition, type ManifestDefinitionEnv } from "./definition"

const APP = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  selector:
    matchLabels: {app: web}
  template:
    metadata:
      labels:
        tier: frontend
        app: web
    spec:
      serviceAccountName: web
      containers:
        - name: web
          image: nginx:1.27
          envFrom:
            - configMapRef:
                name: web-config
            - secretRef:
                name: web-secret
          env:
            - name: TOKEN
              valueFrom:
                secretKeyRef:
                  name: web-secret
                  key: token
          volumeMounts:
            - name: data
              mountPath: /data
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: web-data
        - name: conf
          configMap:
            name: web-config
---
apiVersion: v1
kind: Service
metadata:
  name: web
spec:
  selector:
    app: web
    tier: frontend
  ports:
    - port: 80
`

const CONFIG = `apiVersion: v1
kind: ConfigMap
metadata:
  name: web-config
data:
  LOG_LEVEL: info
---
apiVersion: v1
kind: Secret
metadata:
  name: web-secret
stringData:
  token: abc
`

const node = (kind: string, name: string, file: string, line: number, extra: Partial<KubeNode> = {}): KubeNode => ({
  id: `${kind}/default/${name}`,
  kind,
  apiVersion: "v1",
  name,
  namespace: "default",
  category: "config",
  source: 0,
  sourceKind: "manifests",
  sourcePath: "k8s",
  file,
  line,
  editable: true,
  ...extra,
})

const env = (nodes: KubeNode[] = []): ManifestDefinitionEnv => ({ path: "k8s/app.yaml", nodes, files: { "k8s/app.yaml": APP, "k8s/config.yaml": CONFIG } })

const posOf = (text: string, needle: string, nth = 0, offset = 1) => {
  let i = -1
  for (let n = 0; n <= nth; n++) i = text.indexOf(needle, i + 1)
  return i + offset
}

describe("manifest go to definition", () => {
  test("Service selector → the workload's pod template label", () => {
    const pos = posOf(APP, "tier: frontend", 1)
    expect(manifestDefinition(APP, pos, env())).toEqual({ file: "k8s/app.yaml", line: 11 })
    // On the value too, and through the graph when the workload is rendered.
    const graph = [node("Deployment", "web", "k8s/app.yaml", 1, { workload: { containers: [], podLabels: { app: "web", tier: "frontend" } } })]
    expect(manifestDefinition(APP, posOf(APP, "app: web", 2, 6), env(graph))).toMatchObject({ file: "k8s/app.yaml", line: 12 })
  })

  test("configMapRef / secretRef / volume names → the object's document", () => {
    expect(manifestDefinition(APP, posOf(APP, "web-config"), env())).toEqual({ file: "k8s/config.yaml", line: 1 })
    expect(manifestDefinition(APP, posOf(APP, "web-secret"), env())).toEqual({ file: "k8s/config.yaml", line: 8 })
    expect(manifestDefinition(APP, posOf(APP, "web-config", 1), env())).toEqual({ file: "k8s/config.yaml", line: 1 })
  })

  test("secretKeyRef key → the key in the Secret", () => {
    expect(manifestDefinition(APP, posOf(APP, "key: token", 0, 6), env())).toEqual({ file: "k8s/config.yaml", line: 13 })
  })

  test("graph nodes locate objects of rendered sources", () => {
    const graph = [node("PersistentVolumeClaim", "web-data", "k8s/storage.yaml", 7), node("ServiceAccount", "web", "k8s/rbac.yaml", 3)]
    expect(manifestDefinition(APP, posOf(APP, "web-data"), env(graph))).toEqual({ file: "k8s/storage.yaml", line: 7 })
    expect(manifestDefinition(APP, posOf(APP, "serviceAccountName: web", 0, 21), env(graph))).toEqual({ file: "k8s/rbac.yaml", line: 3 })
  })

  test("names of nothing known and plain keys go nowhere", () => {
    expect(manifestDefinition(APP, posOf(APP, "nginx"), env())).toBeNull()
    expect(manifestDefinition(APP, posOf(APP, "mountPath"), env())).toBeNull()
    expect(manifestDefinition(APP, posOf(APP, "web-data"), env())).toBeNull()
  })
})
