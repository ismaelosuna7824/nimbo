import { describe, expect, test } from "bun:test"
import { k8sIcon, k8sIconFor } from "@/components/k8s-icons"
import { techIcon } from "@/components/tech-icons"
import type { KubeContainer, KubeEdge, KubeNode, KubeStatus, KubeWorkload } from "@/lib/api/types"
import {
  dataTierGroupId,
  deriveKubernetesArchitecture,
  KUBE_CLUSTER_GROUP,
  kubeCardScene,
  kubeGroupIconOf,
  kubeLegend,
  kubeSubtitle,
  namespaceGroupId,
  USERS_ACTOR,
} from "./kubernetes"
import type { ArchModel } from "./model"

const base = (kind: string, name: string, ns: string | undefined, extra: Partial<KubeNode>): KubeNode => ({
  id: ns ? `${kind}/${ns}/${name}` : `${kind}/${name}`,
  kind,
  apiVersion: "v1",
  name,
  namespace: ns,
  category: "workload",
  source: 0,
  sourceKind: "manifests",
  sourcePath: "k8s",
  editable: true,
  ...extra,
})
const container = (image: string, extra: Partial<KubeContainer> = {}): KubeContainer => ({
  name: "main",
  image,
  ports: [],
  probes: {},
  resources: { requests: {}, limits: {} },
  env: [],
  envFrom: [],
  volumeMounts: [],
  ...extra,
})
const workload = (kind: string, name: string, image: string, w: Partial<KubeWorkload> = {}, ns = "shop") =>
  base(kind, name, ns, { category: "workload", workload: { containers: [container(image)], ...w } })
const service = (name: string, type = "ClusterIP", port = 80, targetPort?: string, ns = "shop") =>
  base("Service", name, ns, { category: "network", service: { type, ports: [{ port, targetPort }] } })
/** `source` refers to `target` (the graph's direction). */
const edge = (source: string, target: string, kind: KubeEdge["kind"], extra: Partial<KubeEdge> = {}): KubeEdge => ({
  id: `${source}->${target}:${kind}`,
  source,
  target,
  kind,
  ...extra,
})
const wf = (nodes: KubeNode[], edges: KubeEdge[] = [], environment = "") => ({
  id: "abc",
  name: "Shop",
  kubernetes: { sources: [{ kind: "kustomize" as const, path: "k8s/base", objects: nodes.length }], environment, graph: { nodes, edges } },
})
const card = (m: ArchModel, id: string) => m.cards.find((c) => c.id === id)!
const parentOf = (m: ArchModel, id: string) => card(m, id).parent

// Microservices: Ingress → web (Service web) and api (Service api, HPA); api → postgres (StatefulSet, PVC
// template) and redis (Deployment) through their Services; web → api through its Service; ConfigMap + Secret.
const WEB = "Deployment/shop/web"
const API = "Deployment/shop/api"
const DB = "StatefulSet/shop/postgres"
const CACHE = "Deployment/shop/redis"
const ING = "Ingress/shop/shop"
const micro = () => {
  const nodes = [
    base("Ingress", "shop", "shop", {
      category: "network",
      routes: [
        { host: "shop.local", path: "/", service: "web", port: "80" },
        { host: "shop.local", path: "/api", service: "api", port: "8080", tls: true, class: "traefik" },
      ],
    }),
    service("web", "ClusterIP", 80, "3000"),
    service("api", "ClusterIP", 8080),
    base("Service", "postgres", "shop", { category: "network", service: { type: "ClusterIP", headless: true, ports: [{ port: 5432 }] } }),
    service("redis", "ClusterIP", 6379),
    workload("Deployment", "web", "ghcr.io/acme/web:1.0", { replicas: 2 }),
    workload("Deployment", "api", "node:20-alpine"),
    workload("StatefulSet", "postgres", "postgres:16", { volumeClaimTemplates: [{ name: "data", size: "1Gi" }] }),
    workload("Deployment", "redis", "redis:7"),
    base("HorizontalPodAutoscaler", "api", "shop", { category: "autoscaling", scaler: { target: API, minReplicas: 2, maxReplicas: 10, cpu: 70 } }),
    base("ConfigMap", "api-config", "shop", { category: "config", data: ["LOG_LEVEL"] }),
    base("Secret", "db-creds", "shop", { category: "config", data: ["PASSWORD"] }),
  ]
  const edges = [
    edge(ING, "Service/shop/web", "routes", { label: "80" }),
    edge(ING, "Service/shop/api", "routes", { label: "8080" }),
    edge("Service/shop/web", WEB, "selects"),
    edge("Service/shop/api", API, "selects"),
    edge("Service/shop/postgres", DB, "selects"),
    edge("Service/shop/redis", CACHE, "selects"),
    edge(DB, "Service/shop/postgres", "governs"),
    edge("HorizontalPodAutoscaler/shop/api", API, "scales"),
    edge(WEB, "Service/shop/api", "inferred", { envKey: "API_URL" }),
    edge(API, "Service/shop/postgres", "inferred", { envKey: "DATABASE_URL" }),
    edge(API, "Service/shop/postgres", "inferred", { envKey: "DATABASE_HOST" }),
    edge(API, "Service/shop/redis", "inferred", { envKey: "REDIS_URL" }),
    edge(API, "ConfigMap/shop/api-config", "envFrom"),
    edge(API, "Secret/shop/db-creds", "env", { envKey: "DB_PASSWORD" }),
  ]
  return wf(nodes, edges, "dev")
}

describe("deriveKubernetesArchitecture", () => {
  test("nests the namespace in the local cluster, data stores in its data tier", () => {
    const m = deriveKubernetesArchitecture(micro())
    expect(m.groups.map((g) => [g.id, g.kind, g.parent])).toEqual([
      [KUBE_CLUSTER_GROUP, "k8s-cluster", undefined],
      [namespaceGroupId("shop"), "k8s-namespace", KUBE_CLUSTER_GROUP],
      [dataTierGroupId("shop"), "k8s-data", namespaceGroupId("shop")],
    ])
    expect(m.groups[0]).toMatchObject({ label: "Local cluster", sublabel: ["k3s · nimbo-k8s-abc"] })
    expect(m.cards.map((c) => c.id)).toEqual([ING, WEB, API, DB, CACHE])
    expect(parentOf(m, ING)).toBe(namespaceGroupId("shop"))
    expect(parentOf(m, WEB)).toBe(namespaceGroupId("shop"))
    expect(parentOf(m, API)).toBe(namespaceGroupId("shop"))
    expect(parentOf(m, DB)).toBe(dataTierGroupId("shop"))
    // A Deployment running a cache image is a data store too.
    expect(parentOf(m, CACHE)).toBe(dataTierGroupId("shop"))
    expect(card(m, DB)).toMatchObject({ type: "StatefulSet", category: "database", name: "postgres" })
    // Services, the HPA, ConfigMaps and Secrets are folded into the workloads.
    expect(m.cards.some((c) => c.type === "Service" || c.type === "HorizontalPodAutoscaler" || c.type === "ConfigMap")).toBe(false)
  })

  test("Users enter through the Ingress; routes and env references become workload flows via Services", () => {
    const m = deriveKubernetesArchitecture(micro())
    expect(m.actors).toEqual([{ id: USERS_ACTOR, kind: "users", label: "Users", sublabel: "ingress" }])
    expect(m.edges).toEqual([
      { id: `${USERS_ACTOR}->${ING}`, source: USERS_ACTOR, target: ING, kind: "data" },
      { id: `${ING}->${WEB}`, source: ING, target: WEB, kind: "data" },
      { id: `${ING}->${API}`, source: ING, target: API, kind: "data" },
      { id: `${WEB}->${API}`, source: WEB, target: API, kind: "data" },
      { id: `${API}->${DB}`, source: API, target: DB, kind: "data" },
      { id: `${API}->${CACHE}`, source: API, target: CACHE, kind: "data" },
    ])
  })

  test("folds Services, autoscaling, claims, ConfigMaps and Secrets into the workload cards", () => {
    const m = deriveKubernetesArchitecture(micro())
    expect(card(m, ING).details).toEqual(["shop.local/ → web:80", "shop.local/api → api:8080 (TLS)", "class traefik"])
    expect(card(m, WEB).details).toEqual(["ghcr.io/acme/web:1.0", "×2", "svc web :80→3000"])
    expect(card(m, API).details).toEqual(["node:20-alpine", "HPA 2–10 · cpu 70%", "svc api :8080", "cm api-config", "secret db-creds"])
    expect(card(m, DB).details).toEqual(["postgres:16", "svc postgres headless :5432", "pvc data 1Gi"])
  })

  test("adds the cluster and rollout state, and the ingress URLs, when the cluster runs", () => {
    const status: KubeStatus = {
      workflowId: "abc",
      cluster: { name: "nimbo-k8s-abc", state: "running", image: "rancher/k3s:v1.36.5-k3s1", version: "v1.36.5" },
      state: "starting",
      workloads: [
        { nodeId: WEB, kind: "Deployment", namespace: "shop", name: "web", state: "running", desired: 2, ready: 2, updated: 2, available: 2, images: [], pods: [] },
        {
          nodeId: API,
          kind: "Deployment",
          namespace: "shop",
          name: "api",
          state: "starting",
          desired: 2,
          ready: 0,
          updated: 2,
          available: 0,
          images: [],
          pods: [{ name: "api-1", namespace: "shop", phase: "Pending", ready: false, restarts: 0, reason: "ImagePullBackOff", containers: [] }],
        },
        { nodeId: DB, kind: "StatefulSet", namespace: "shop", name: "postgres", state: "absent", desired: 1, ready: 0, updated: 0, available: 0, images: [], pods: [] },
      ],
      forwards: [],
      ingresses: [
        { nodeId: ING, namespace: "shop", ingress: "shop", host: "shop.local", path: "/", service: "web", url: "http://shop.local:8080/", address: "http://127.0.0.1:8080/", serviceNodeId: "Service/shop/web" },
      ],
      events: [],
      problems: [],
      updatedAt: "2026-10-22T00:00:00Z",
    }
    const m = deriveKubernetesArchitecture(micro(), status)
    expect(m.groups[0].sublabel).toEqual(["k3s v1.36.5 · nimbo-k8s-abc"])
    expect(card(m, WEB).details).toContain("2/2 ready")
    expect(card(m, API).details).toContain("starting · 0/2 ready · ImagePullBackOff")
    expect(card(m, DB).details).toContain("not running")
    expect(card(m, ING).details[0]).toBe("http://shop.local:8080/")

    const stopped = deriveKubernetesArchitecture(micro(), { ...status, cluster: { ...status.cluster, state: "absent", version: undefined } })
    expect(stopped.groups[0].sublabel).toEqual(["k3s · nimbo-k8s-abc · not created"])
  })

  test("without an Ingress the Users reach the workloads behind LoadBalancer/NodePort Services", () => {
    const app = "Deployment/default/app"
    const db = "StatefulSet/default/db"
    const pvc = "PersistentVolumeClaim/default/uploads"
    const m = deriveKubernetesArchitecture(
      wf(
        [
          service("app", "LoadBalancer", 80, "8080", "default"),
          service("db", "ClusterIP", 5432, undefined, "default"),
          workload("Deployment", "app", "nginx:1.27", { volumes: [{ name: "uploads", kind: "persistentVolumeClaim", source: "uploads" }] }, "default"),
          workload("StatefulSet", "db", "mysql:8", {}, "default"),
          base("PersistentVolumeClaim", "uploads", "default", { category: "storage", storage: { size: "5Gi", accessModes: ["ReadWriteOnce"], storageClass: "local-path" } }),
          base("Service", "legacy", "default", { category: "network", service: { type: "ExternalName", ports: [], externalName: "legacy.example.com" } }),
        ],
        [
          edge("Service/default/app", app, "selects"),
          edge("Service/default/db", db, "selects"),
          edge(app, "Service/default/db", "inferred", { envKey: "DB_HOST" }),
          edge(app, "Service/default/legacy", "inferred", { envKey: "LEGACY_URL" }),
          edge(app, pvc, "pvc"),
        ],
      ),
    )
    expect(m.groups.map((g) => g.id)).toEqual([KUBE_CLUSTER_GROUP, namespaceGroupId("default"), dataTierGroupId("default")])
    expect(m.actors).toEqual([{ id: USERS_ACTOR, kind: "users", label: "Users", sublabel: "LoadBalancer/NodePort" }])
    expect(m.edges.filter((e) => e.source === USERS_ACTOR).map((e) => e.target)).toEqual([app])
    // A Service selecting no workload stays a card (ExternalName); claims sit in the data tier.
    expect(card(m, "Service/default/legacy")).toMatchObject({ parent: namespaceGroupId("default"), details: ["ExternalName", "→ legacy.example.com"] })
    expect(card(m, pvc)).toMatchObject({ parent: dataTierGroupId("default"), category: "storage", details: ["5Gi", "ReadWriteOnce", "local-path"] })
    expect(card(m, app).details).toEqual(["nginx:1.27", "svc app LoadBalancer :80→8080", "pvc uploads"])
    expect(m.edges.filter((e) => e.source === app).map((e) => e.target)).toEqual([db, "Service/default/legacy", pvc])
  })

  test("draws nothing without workloads or entries", () => {
    const m = deriveKubernetesArchitecture(wf([base("ConfigMap", "x", "default", { category: "config" })]))
    expect(m).toEqual({ groups: [], cards: [], actors: [], edges: [], providers: [], regions: [] })
    expect(deriveKubernetesArchitecture({ id: "abc", name: "Empty" }).cards).toEqual([])
  })

  test("a vendored chart is one card in its release namespace; requests to its objects go to it", () => {
    const w = micro()
    const CHART = "chart:charts/monitoring/charts/kube-prometheus-stack"
    const helm = { chart: CHART, source: 1, sourceKind: "helm" as const, sourcePath: "charts/monitoring" }
    w.kubernetes.graph.nodes.push(
      { ...workload("Deployment", "grafana", "grafana/grafana:12", {}, "monitoring"), ...helm },
      { ...service("grafana", "ClusterIP", 80, "3000", "monitoring"), ...helm },
      { ...workload("StatefulSet", "prometheus", "quay.io/prometheus/prometheus:v3", {}, "monitoring"), ...helm },
      { ...base("ClusterRole", "operator", undefined, { category: "rbac" }), ...helm },
    )
    w.kubernetes.graph.edges.push(edge("Service/monitoring/grafana", "Deployment/monitoring/grafana", "selects"), edge(API, "Service/monitoring/grafana", "inferred", { envKey: "GRAFANA_URL" }))
    const graph = {
      ...w.kubernetes.graph,
      charts: [{ id: CHART, source: 1, sourcePath: "charts/monitoring", chart: "kube-prometheus-stack", version: "92.11.0", release: "monitoring", namespace: "monitoring", objects: 4 }],
    }
    const m = deriveKubernetesArchitecture({ ...w, kubernetes: { ...w.kubernetes, graph } })
    expect(m.cards.filter((c) => c.parent === namespaceGroupId("monitoring")).map((c) => c.id)).toEqual([CHART])
    expect(card(m, CHART)).toMatchObject({ type: "Helm chart", name: "kube-prometheus-stack", nodeId: CHART })
    expect(card(m, CHART).details).toEqual(["92.11.0 · release monitoring", "1 ClusterRole · 1 Deployment · 1 Service · +1 more kind"])
    expect(m.edges.filter((e) => e.target === CHART).map((e) => e.source)).toEqual([API])
    expect(kubeCardScene(card(m, CHART), undefined).icon).toBe(techIcon("helm").src)
  })
})

describe("kubernetes scene", () => {
  test("workload cards use their container's technology icon, else their kind's", () => {
    const w = micro()
    const m = deriveKubernetesArchitecture(w)
    const nodes = new Map(w.kubernetes.graph.nodes.map((n) => [n.id, n]))
    const db = kubeCardScene(card(m, DB), nodes.get(DB))
    expect(db.icon).toBe(techIcon("postgres").src)
    expect(db.text.title).toBe("postgres")
    expect(db.text.lines.join(" ")).toContain("StatefulSet")
    expect(db.text.tooltip).toContain(DB)
    expect(kubeCardScene(card(m, CACHE), nodes.get(CACHE)).icon).toBe(techIcon("redis").src)
    expect(kubeCardScene(card(m, WEB), nodes.get(WEB)).icon).toBe(k8sIconFor("Deployment").src)
    expect(kubeCardScene(card(m, ING), nodes.get(ING)).icon).toBe(k8sIconFor("Ingress").src)
  })

  test("group icons, subtitle and legend", () => {
    const m = deriveKubernetesArchitecture(micro())
    const icons = new Map(m.groups.map((g) => [g.kind, kubeGroupIconOf(g)]))
    expect(icons.get("k8s-cluster")).toBe(techIcon("kubernetes").src)
    expect(icons.get("k8s-namespace")).toBe(k8sIcon("ns").src)
    expect(icons.get("k8s-data")).toBe(k8sIcon("pvc").src)
    expect(kubeSubtitle(micro())).toBe("Kubernetes · Kustomize · dev")
    expect(kubeLegend(m)).toEqual([{ kind: "data", label: "Requests, routes & volumes" }])
  })
})
