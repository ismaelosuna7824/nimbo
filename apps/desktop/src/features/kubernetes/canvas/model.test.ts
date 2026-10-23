import { describe, expect, test } from "bun:test"
import { placeNew } from "@/features/docker/canvas/model"
import type { KubeEdge, KubeNode, KubePod, KubeStatus, KubeWorkloadStatus } from "@/lib/api/types"
import {
  buildKubeCanvas,
  FRAME_HEADER,
  FRAME_PAD,
  NAMESPACE_FRAME,
  podDot,
  replicasText,
  smallFacts,
  WORKLOAD_H,
  WORKLOAD_W,
  type KubeCanvasItem,
} from "./model"

const node = (kind: string, name: string, over: Partial<KubeNode> = {}): KubeNode => {
  const ns = "namespace" in over ? over.namespace : "default"
  return {
    id: ns ? `${kind}/${ns}/${name}` : `${kind}/${name}`,
    kind,
    apiVersion: "v1",
    name,
    namespace: ns,
    category: "config",
    source: 0,
    sourceKind: "manifests",
    sourcePath: "k8s",
    editable: true,
    ...over,
  }
}

const deployment = (name: string, over: Partial<KubeNode> = {}) =>
  node("Deployment", name, {
    apiVersion: "apps/v1",
    category: "workload",
    workload: { replicas: 2, containers: [{ name, image: "nginx:1.27", ports: [], probes: {}, resources: { requests: {}, limits: {} }, env: [], envFrom: [], volumeMounts: [] }] },
    ...over,
  })

const service = (name: string, over: Partial<KubeNode> = {}) =>
  node("Service", name, { category: "network", service: { type: "ClusterIP", ports: [{ port: 80, targetPort: "8080" }] }, ...over })

const edge = (kind: KubeEdge["kind"], source: string, target: string, over: Partial<KubeEdge> = {}): KubeEdge => ({
  id: `${kind}:${source}->${target}`,
  kind,
  source,
  target,
  ...over,
})

const pod = (name: string, over: Partial<KubePod> = {}): KubePod => ({ name, namespace: "default", phase: "Running", ready: true, restarts: 0, containers: [], ...over })

const workloadStatus = (nodeId: string, over: Partial<KubeWorkloadStatus> = {}): KubeWorkloadStatus => ({
  nodeId,
  kind: "Deployment",
  namespace: "default",
  name: nodeId.split("/").pop()!,
  state: "running",
  desired: 2,
  ready: 2,
  updated: 2,
  available: 2,
  images: [],
  pods: [],
  ...over,
})

const status = (workloads: KubeWorkloadStatus[]): KubeStatus => ({
  workflowId: "w",
  cluster: { name: "nimbo-k8s-w", state: "running", image: "rancher/k3s" },
  state: "running",
  workloads,
  forwards: [],
  ingresses: [],
  events: [],
  problems: [],
  updatedAt: "",
})

const byId = (items: KubeCanvasItem[]) => new Map(items.map((i) => [i.id, i]))

describe("buildKubeCanvas: namespace frames", () => {
  test("namespaced objects sit in an implicit frame per namespace; cluster-scoped ones outside", () => {
    const c = buildKubeCanvas({
      nodes: [deployment("web"), deployment("api", { namespace: "backend" }), node("ClusterRole", "admin", { namespace: undefined, category: "rbac" })],
      edges: [],
    })
    const items = byId(c.items)
    expect(items.get(`${NAMESPACE_FRAME}default`)?.kind).toBe("frame")
    expect(items.get(`${NAMESPACE_FRAME}backend`)?.namespace).toBe("backend")
    expect(items.get("Deployment/default/web")?.parent).toBe(`${NAMESPACE_FRAME}default`)
    expect(items.get("Deployment/backend/api")?.parent).toBe(`${NAMESPACE_FRAME}backend`)
    expect(items.get("ClusterRole/admin")?.parent).toBeUndefined()
    expect(items.get(`${NAMESPACE_FRAME}default`)?.placed).toBe(false)
  })

  test("a Namespace object is the frame of its objects", () => {
    const ns = node("Namespace", "shop", { namespace: undefined, category: "cluster" })
    const c = buildKubeCanvas({ nodes: [deployment("web", { namespace: "shop" }), ns], edges: [] })
    const items = byId(c.items)
    expect(items.get("Namespace/shop")?.kind).toBe("frame")
    expect(items.get("Namespace/shop")?.node).toBe(ns)
    expect(items.get("Deployment/shop/web")?.parent).toBe("Namespace/shop")
    expect(items.has(`${NAMESPACE_FRAME}shop`)).toBe(false)
  })

  test("frames wrap their placed members", () => {
    const c = buildKubeCanvas({
      nodes: [deployment("web", { position: { x: 100, y: 100 } }), service("web", { position: { x: 500, y: 100 } })],
      edges: [],
    })
    const frame = byId(c.items).get(`${NAMESPACE_FRAME}default`)!
    expect(frame).toMatchObject({ x: 100 - FRAME_PAD, y: 100 - FRAME_HEADER, placed: true })
    expect(frame.w).toBe(500 + 216 - 100 + 2 * FRAME_PAD)
    expect(frame.h).toBe(WORKLOAD_H + FRAME_HEADER + FRAME_PAD)
  })

  test("workloads get workload cards, others small cards with facts", () => {
    const c = buildKubeCanvas({ nodes: [deployment("web"), service("web"), node("ConfigMap", "cfg", { data: ["A", "B"] })], edges: [] })
    const items = byId(c.items)
    expect(items.get("Deployment/default/web")).toMatchObject({ kind: "workload", w: WORKLOAD_W, h: WORKLOAD_H })
    expect(items.get("Service/default/web")).toMatchObject({ kind: "small", facts: "ClusterIP · 80→8080" })
    expect(items.get("ConfigMap/default/cfg")?.facts).toBe("2 keys")
  })

  test("unmatched Service selectors are flagged", () => {
    const c = buildKubeCanvas({ nodes: [service("lost", { service: { type: "ClusterIP", ports: [], unmatched: true } })], edges: [] })
    expect(byId(c.items).get("Service/default/lost")?.unmatched).toBe(true)
  })
})

describe("buildKubeCanvas: edges", () => {
  test("edges keep the graph's direction and labels; dangling ones are dropped", () => {
    const c = buildKubeCanvas({
      nodes: [deployment("web"), service("web"), node("Ingress", "web", { category: "network", routes: [{ host: "web.local", path: "/", service: "web", port: "80" }] }), node("ConfigMap", "cfg")],
      edges: [
        edge("selects", "Service/default/web", "Deployment/default/web"),
        edge("routes", "Ingress/default/web", "Service/default/web", { label: "80" }),
        edge("volume", "Deployment/default/web", "ConfigMap/default/cfg", { label: "/etc/app" }),
        edge("inferred", "Deployment/default/web", "Service/default/web", { envKey: "API_URL" }),
        edge("pvc", "Deployment/default/web", "PersistentVolumeClaim/default/gone"),
      ],
    })
    expect(c.edges.map((e) => [e.kind, e.source, e.target, e.label])).toEqual([
      ["selects", "Service/default/web", "Deployment/default/web", undefined],
      ["routes", "Ingress/default/web", "Service/default/web", "80"],
      ["volume", "Deployment/default/web", "ConfigMap/default/cfg", "/etc/app"],
      ["inferred", "Deployment/default/web", "Service/default/web", "API_URL"],
    ])
  })

  test("HPAs badge the workload they scale", () => {
    const hpa = node("HorizontalPodAutoscaler", "web", { category: "autoscaling", scaler: { target: "Deployment/default/web", minReplicas: 2, maxReplicas: 6, cpu: 70 } })
    const c = buildKubeCanvas({ nodes: [deployment("web"), hpa], edges: [edge("scales", hpa.id, "Deployment/default/web")] })
    const items = byId(c.items)
    expect(items.get("Deployment/default/web")?.hpa).toEqual({ id: hpa.id, min: 2, max: 6 })
    expect(items.get(hpa.id)?.facts).toBe("2–6 · cpu 70%")
  })
})

describe("buildKubeCanvas: live state", () => {
  const graph = {
    nodes: [deployment("web"), service("web"), node("Ingress", "web", { category: "network" }), node("ConfigMap", "cfg")],
    edges: [
      edge("selects", "Service/default/web", "Deployment/default/web"),
      edge("routes", "Ingress/default/web", "Service/default/web"),
      edge("envFrom", "Deployment/default/web", "ConfigMap/default/cfg"),
    ],
  }

  test("workload status and pods merge by node id", () => {
    const st = workloadStatus("Deployment/default/web", { pods: [pod("web-1"), pod("web-2", { phase: "Pending", ready: false })] })
    const item = byId(buildKubeCanvas(graph, status([st])).items).get("Deployment/default/web")!
    expect(item.status).toBe(st)
    expect(item.pods?.map((p) => p.tone)).toEqual(["ok", "pending"])
  })

  test("traffic edges are live only while the workload runs", () => {
    const running = buildKubeCanvas(graph, status([workloadStatus("Deployment/default/web")]))
    expect(running.edges.map((e) => [e.kind, e.live])).toEqual([
      ["selects", true],
      ["routes", true],
      ["envFrom", false],
    ])
    const stopped = buildKubeCanvas(graph, status([workloadStatus("Deployment/default/web", { state: "error" })]))
    expect(stopped.edges.every((e) => !e.live)).toBe(true)
    expect(buildKubeCanvas(graph).edges.every((e) => !e.live)).toBe(true)
  })
})

describe("podDot", () => {
  test("crash loops, OOM kills and failures are errors", () => {
    expect(podDot(pod("a", { phase: "Running", ready: false, reason: "CrashLoopBackOff", restarts: 4 }))).toMatchObject({ tone: "error", reason: "CrashLoopBackOff" })
    expect(podDot(pod("a", { containers: [{ name: "c", image: "x", ready: false, state: "waiting", restarts: 1, oomKilled: true }] })).reason).toBe("OOMKilled")
    expect(podDot(pod("a", { phase: "Failed" })).tone).toBe("error")
    expect(podDot(pod("a", { phase: "Succeeded", ready: false })).tone).toBe("done")
    expect(podDot(pod("a", { phase: "Pending", ready: false, reason: "ContainerCreating" })).tone).toBe("pending")
  })

  test("the label lists phase, readiness, reason and restarts", () => {
    expect(podDot(pod("web-1", { restarts: 2 })).label).toBe("web-1: Running · ready · 2 restarts")
  })
})

describe("replicasText", () => {
  test("ready of desired when live, declared replicas otherwise", () => {
    const web = deployment("web")
    expect(replicasText(web)).toBe("×2")
    expect(replicasText(web, workloadStatus(web.id, { ready: 1, desired: 2 }))).toBe("1/2")
    expect(replicasText(node("CronJob", "nightly", { category: "workload", workload: { containers: [], schedule: "0 3 * * *" } }))).toBe("0 3 * * *")
  })
})

describe("smallFacts", () => {
  test("ingress hosts, claims and headless services", () => {
    expect(smallFacts(node("Ingress", "i", { routes: [{ host: "a.local", path: "/api", service: "s" }, { host: "b.local", service: "s" }] }))).toBe("a.local/api +1")
    expect(smallFacts(node("PersistentVolumeClaim", "data", { storage: { size: "1Gi", storageClass: "local-path" } }))).toBe("1Gi · local-path")
    expect(smallFacts(service("db", { service: { type: "ClusterIP", headless: true, ports: [{ port: 5432 }] } }))).toBe("headless · 5432")
  })
})

describe("placeNew with namespace frames", () => {
  test("a new workload joins its namespace below the placed members", () => {
    const c = buildKubeCanvas({ nodes: [deployment("web", { position: { x: 0, y: 0 } }), deployment("api")], edges: [] })
    const out = placeNew(c.items, c.edges, (i) => i.kind === "workload")
    expect(out.get("Deployment/default/api")).toEqual({ x: 0, y: WORKLOAD_H + 40 })
  })
})
