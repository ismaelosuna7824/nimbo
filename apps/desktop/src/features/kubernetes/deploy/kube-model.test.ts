import { describe, expect, test } from "bun:test"
import type { KubeEvent, KubeGraph, KubeLogLine, KubePod, KubeProblem, KubeStatus, KubeWorkloadStatus, LoadResources } from "@/lib/api/types"
import {
  appendKubeLogRows,
  backingWorkload,
  firstKubeUrl,
  formatAge,
  groupProblems,
  kubeChipSummary,
  kubectlSnippet,
  kubeKey,
  kubeLogRowsText,
  kubeProblemTone,
  nodeKind,
  nodeName,
  podAge,
  relabelResources,
  runningWorkloads,
  workloadEvents,
  workloadLabels,
  workloadLimits,
  workloadPods,
  workloadRestarts,
} from "./kube-model"

const pod = (name: string, over: Partial<KubePod> = {}): KubePod => ({
  name,
  namespace: "default",
  phase: "Running",
  ready: true,
  restarts: 0,
  containers: [],
  ...over,
})

const workload = (name: string, over: Partial<KubeWorkloadStatus> = {}): KubeWorkloadStatus => ({
  nodeId: `Deployment/default/${name}`,
  kind: "Deployment",
  namespace: "default",
  name,
  state: "running",
  desired: 1,
  ready: 1,
  updated: 1,
  available: 1,
  images: [],
  pods: [pod(`${name}-abc-1`)],
  ...over,
})

const status = (over: Partial<KubeStatus> = {}): KubeStatus => ({
  workflowId: "wf",
  cluster: { name: "nimbo-k8s-wf", state: "running", image: "rancher/k3s" },
  state: "running",
  workloads: [],
  forwards: [],
  ingresses: [],
  events: [],
  problems: [],
  updatedAt: "2026-01-01T00:00:00Z",
  ...over,
})

const problem = (nodeId: string, severity: "error" | "warning", kind = "crashloop"): KubeProblem => ({
  nodeId,
  namespace: "default",
  kind,
  severity,
  message: `${kind} ${severity}`,
})

describe("kubeChipSummary", () => {
  test("no status: cluster off", () => {
    expect(kubeChipSummary(null)).toEqual({ cluster: "absent", state: "absent", label: "Cluster off", running: 0, total: 0 })
  })

  test("cluster states other than running win", () => {
    expect(kubeChipSummary(status({ cluster: { name: "c", state: "starting", image: "" } })).label).toBe("Starting…")
    expect(kubeChipSummary(status({ cluster: { name: "c", state: "stopped", image: "" } })).label).toBe("Cluster stopped")
    expect(kubeChipSummary(status({ cluster: { name: "c", state: "error", image: "" } })).state).toBe("error")
  })

  test("running cluster follows its workloads", () => {
    expect(kubeChipSummary(status({ state: "absent" })).label).toBe("Cluster running")
    const s = status({ workloads: [workload("web"), workload("db", { pods: [pod("db-0", { phase: "Pending", ready: false })] })] })
    expect(kubeChipSummary(s)).toMatchObject({ label: "Running 1/2", running: 1, total: 2, state: "running" })
    expect(kubeChipSummary({ ...s, state: "starting" }).label).toBe("Rolling out 1/2")
    expect(kubeChipSummary({ ...s, state: "error" }).label).toBe("Workload error")
  })
})

describe("running counts", () => {
  test("runningWorkloads counts workloads with a running pod", () => {
    expect(runningWorkloads(undefined)).toBe(0)
    expect(
      runningWorkloads(
        status({ workloads: [workload("a"), workload("b", { pods: [pod("b-1", { phase: "Succeeded" })] }), workload("c", { pods: [] })] }),
      ),
    ).toBe(1)
  })

  test("pods, restarts and limits of a workload", () => {
    const w = workload("web", {
      pods: [pod("web-1", { restarts: 2, cpuLimit: 0.5, memLimit: 100 }), pod("web-2", { ready: false, restarts: 1, cpuLimit: 0.5 })],
    })
    expect(workloadPods(w)).toEqual({ ready: 1, total: 2 })
    expect(workloadRestarts(w)).toBe(3)
    // One pod without a memory limit: the workload is not memory-limited.
    expect(workloadLimits(w)).toEqual({ cpu: 1, mem: 0 })
    expect(workloadLimits(workload("x", { pods: [] }))).toEqual({ cpu: 0, mem: 0 })
  })
})

describe("kubeKey", () => {
  test("changes with reachability, not with timestamps", () => {
    const a = status({ workloads: [workload("web")] })
    expect(kubeKey(a)).toBe(kubeKey({ ...a, updatedAt: "later", events: [] }))
    const forwarded = {
      ...a,
      forwards: [{ nodeId: "Service/default/web", namespace: "default", service: "web", port: 80, protocol: "TCP", hostPort: 30080, ready: true }],
    }
    expect(kubeKey(forwarded)).not.toBe(kubeKey(a))
    expect(kubeKey(null)).toBe("")
  })

  test("firstKubeUrl prefers a ready forward, then an ingress", () => {
    const ingress = {
      nodeId: "Ingress/default/web",
      namespace: "default",
      ingress: "web",
      path: "/",
      service: "web",
      url: "http://web.localhost/",
      address: "http://127.0.0.1:8081/",
      serviceNodeId: "Service/default/web",
    }
    expect(firstKubeUrl(status({ ingresses: [ingress] }))).toBe("http://127.0.0.1:8081/")
    expect(
      firstKubeUrl(
        status({
          ingresses: [ingress],
          forwards: [
            { nodeId: "s", namespace: "d", service: "s", port: 80, protocol: "TCP", hostPort: 1, ready: false, url: "http://x" },
            { nodeId: "s", namespace: "d", service: "s", port: 81, protocol: "TCP", hostPort: 2, ready: true, url: "http://127.0.0.1:2" },
          ],
        }),
      ),
    ).toBe("http://127.0.0.1:2")
  })
})

describe("node ids and labels", () => {
  test("nodeName and nodeKind", () => {
    expect(nodeName("Deployment/default/web")).toBe("web")
    expect(nodeName("ClusterRole/admin")).toBe("admin")
    expect(nodeKind("Service/default/web")).toBe("Service")
    expect(nodeKind("Certificate.cert-manager.io/default/x")).toBe("Certificate")
  })

  test("workloadLabels disambiguates shared names by namespace", () => {
    expect(
      workloadLabels([
        { nodeId: "Deployment/a/web", name: "web", namespace: "a" },
        { nodeId: "Deployment/b/web", name: "web", namespace: "b" },
        { nodeId: "StatefulSet/a/db", name: "db", namespace: "a" },
      ]),
    ).toEqual({ "Deployment/a/web": "a/web", "Deployment/b/web": "b/web", "StatefulSet/a/db": "db" })
  })

  test("relabelResources names workloads in peaks, verdicts and the target", () => {
    const r = {
      service: "Deployment/default/web",
      services: [{ service: "Deployment/default/web" }, { service: "StatefulSet/default/db" }],
      verdicts: [{ service: "Deployment/default/web", kind: "ok", message: "fine" }],
      verdict: "ok",
    } as unknown as LoadResources
    const out = relabelResources(r, { "Deployment/default/web": "web" })
    expect(out.service).toBe("web")
    expect(out.services.map((s) => s.service)).toEqual(["web", "db"])
    expect(out.verdicts[0].service).toBe("web")
  })

  test("backingWorkload follows selects, and routes for ingresses", () => {
    const node = (id: string) => ({ id }) as KubeGraph["nodes"][number]
    const graph = {
      nodes: [node("Deployment/default/web"), node("Service/default/web"), node("Ingress/default/web")],
      edges: [
        { id: "1", source: "Service/default/web", target: "Deployment/default/web", kind: "selects" },
        { id: "2", source: "Ingress/default/web", target: "Service/default/web", kind: "routes" },
      ],
    } as KubeGraph
    expect(backingWorkload("Service/default/web", graph)?.id).toBe("Deployment/default/web")
    expect(backingWorkload("Ingress/default/web", graph)?.id).toBe("Deployment/default/web")
    expect(backingWorkload("Service/default/none", graph)).toBeUndefined()
    expect(backingWorkload("Service/default/web", undefined)).toBeUndefined()
  })
})

describe("problems", () => {
  test("groups per workload, errors first", () => {
    const groups = groupProblems([
      problem("Deployment/default/web", "warning", "restarts"),
      problem("Deployment/default/api", "warning"),
      problem("Deployment/default/web", "error", "oom"),
      problem("StatefulSet/default/db", "error", "image-pull"),
    ])
    expect(groups.map((g) => [g.name, g.severity])).toEqual([
      ["db", "error"],
      ["web", "error"],
      ["api", "warning"],
    ])
    expect(groups[1].problems.map((p) => p.kind)).toEqual(["oom", "restarts"])
  })

  test("tone", () => {
    expect(kubeProblemTone([])).toBeUndefined()
    expect(kubeProblemTone([problem("a", "warning")])).toBe("warning")
    expect(kubeProblemTone([problem("a", "warning"), problem("b", "error")])).toBe("error")
  })
})

describe("workloadEvents", () => {
  const ev = (name: string, over: Partial<KubeEvent> = {}): KubeEvent => ({
    type: "Normal",
    reason: "Scheduled",
    message: "",
    kind: "Pod",
    namespace: "default",
    name,
    count: 1,
    lastSeen: "",
    ...over,
  })
  const events = [
    ev("web-abc-1"),
    ev("web-abc", { kind: "ReplicaSet" }),
    ev("web", { kind: "Deployment", nodeId: "Deployment/default/web" }),
    ev("webhook-1"),
    ev("web-abc-1", { namespace: "other" }),
    ev("db-0"),
  ]
  test("filters by workload, its pods and owned objects", () => {
    expect(workloadEvents(events, workload("web")).map((e) => `${e.namespace}/${e.name}`)).toEqual([
      "default/web-abc-1",
      "default/web-abc",
      "default/web",
    ])
    expect(workloadEvents(events, undefined)).toHaveLength(events.length)
  })
})

describe("formatting", () => {
  test("ages", () => {
    expect(formatAge(45_000)).toBe("45s")
    expect(formatAge(12 * 60_000)).toBe("12m")
    expect(formatAge(3 * 3_600_000)).toBe("3h")
    expect(formatAge(5 * 86_400_000)).toBe("5d")
    expect(podAge(undefined)).toBe("—")
    expect(podAge("2026-01-01T00:00:00Z", Date.parse("2026-01-01T00:02:00Z"))).toBe("2m")
  })

  test("kubectl snippet quotes paths with spaces", () => {
    expect(kubectlSnippet("/home/me/kube/kubeconfig")).toBe("kubectl --kubeconfig /home/me/kube/kubeconfig get pods -A")
    expect(kubectlSnippet("/Users/Me/App Support/kubeconfig")).toBe(
      "kubectl --kubeconfig '/Users/Me/App Support/kubeconfig' get pods -A",
    )
  })
})

describe("log rows", () => {
  const line = (pod: string, container: string, text: string): KubeLogLine => ({
    namespace: "default",
    pod,
    container,
    line: { t: "2026-01-01T00:00:00Z", stream: "stdout", text },
  })

  test("appends, strips ANSI, prefixes multi-container pods and caps", () => {
    let rows = appendKubeLogRows([], [line("web-1", "web", "\x1b[32mhello\x1b[0m"), line("api-1", "app", "a")], new Set(["api-1"]))
    expect(rows.map((r) => [r.id, r.source, r.text])).toEqual([
      [0, "web-1", "hello"],
      [1, "api-1/app", "a"],
    ])
    rows = appendKubeLogRows(rows, [line("web-1", "web", "x"), line("web-1", "web", "y")], new Set(), 3)
    expect(rows.map((r) => r.id)).toEqual([1, 2, 3])
    expect(appendKubeLogRows(rows, [])).toBe(rows)
    expect(kubeLogRowsText(rows.slice(0, 1))).toBe("[api-1/app] a")
  })
})
