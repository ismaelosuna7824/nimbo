import { describe, expect, test } from "bun:test"
import type { AccessCatalog, KubeGraph, KubeNode } from "@/lib/api/types"
import {
  addedNodes,
  catalogGroups,
  catalogResources,
  isRbacNode,
  roleOptions,
  ruleProblem,
  ruleSentence,
  serviceAccountNameProblem,
  serviceAccountsIn,
  subjectComplete,
  toggleVerb,
  verbsLevel,
} from "./rbac-model"

const node = (kind: string, name: string, namespace?: string, apiVersion = "rbac.authorization.k8s.io/v1"): KubeNode => ({
  id: namespace ? `${kind}/${namespace}/${name}` : `${kind}/${name}`,
  kind,
  apiVersion,
  name,
  namespace,
  category: "rbac" as KubeNode["category"],
  source: 0,
  sourceKind: "manifests",
  sourcePath: "k8s",
  editable: true,
})

const graph: KubeGraph = {
  nodes: [
    node("Role", "reader", "shop"),
    node("Role", "other", "billing"),
    node("ClusterRole", "pod-reader"),
    node("ClusterRole", "view"),
    node("ServiceAccount", "api", "shop", "v1"),
    node("ServiceAccount", "x", "billing", "v1"),
  ],
  edges: [],
}

describe("verbs", () => {
  test("toggle keeps the editor's order", () => {
    expect(toggleVerb(["watch", "get"], "list")).toEqual(["get", "list", "watch"])
    expect(toggleVerb(["get", "list"], "get")).toEqual(["list"])
    expect(toggleVerb(["get"], "custom")).toEqual(["get", "custom"])
  })
  test("levels", () => {
    expect(verbsLevel(["watch", "list", "get"])).toBe("read")
    expect(verbsLevel(["get", "list", "watch", "update", "patch"])).toBe("write")
    expect(verbsLevel(["get", "list", "watch", "update", "patch", "delete"])).toBe("manage")
    expect(verbsLevel(["get"])).toBeUndefined()
  })
})

describe("rules", () => {
  test("sentence", () => {
    expect(ruleSentence({ apiGroups: [""], resources: ["configmaps"], resourceNames: ["settings"], verbs: ["watch", "get", "list"] })).toBe(
      "Can get, list and watch configmaps “settings” (core)",
    )
    expect(ruleSentence({ apiGroups: ["*"], resources: ["*"], verbs: ["*"] })).toBe("Can do anything every resource (every API group)")
    expect(ruleSentence({ apiGroups: [], nonResourceURLs: ["/healthz"], verbs: ["get"] })).toBe("Can get on the URLs /healthz")
  })
  test("problems mirror the daemon", () => {
    expect(ruleProblem({ apiGroups: [""], resources: ["pods"], verbs: ["get"] }, "Role")).toBeUndefined()
    expect(ruleProblem({ apiGroups: [""], resources: ["pods"], verbs: [] }, "Role")).toContain("verb")
    expect(ruleProblem({ apiGroups: [""], resources: [], verbs: ["get"] }, "Role")).toContain("resource")
    expect(ruleProblem({ apiGroups: [], resources: ["pods"], verbs: ["get"] }, "Role")).toContain("API group")
    expect(ruleProblem({ apiGroups: [], nonResourceURLs: ["/metrics"], verbs: ["get"] }, "Role")).toContain("ClusterRole")
    expect(ruleProblem({ apiGroups: [], nonResourceURLs: ["/metrics"], verbs: ["get"] }, "ClusterRole")).toBeUndefined()
    expect(ruleProblem({ apiGroups: [""], resources: ["pods"], resourceNames: ["a"], verbs: ["create"] }, "Role")).toContain("names")
  })
})

describe("catalog", () => {
  const catalog: AccessCatalog = {
    provider: "kubernetes",
    source: { title: "k8s", url: "u", retrieved: "2026-10-23" },
    resourceTypes: [
      { name: "deployments", group: "apps" },
      { name: "configmaps", group: "" },
      { name: "pods/log", group: "" },
      { name: "cronjobs", group: "batch" },
    ],
  }
  test("groups, core first", () => {
    expect(catalogGroups(catalog)).toEqual(["", "apps", "batch"])
  })
  test("resources of a group", () => {
    expect(catalogResources(catalog, "").map((r) => r.name)).toEqual(["configmaps", "pods/log"])
  })
})

describe("graph pickers", () => {
  test("a RoleBinding picks its namespace's Roles, ClusterRoles and built-ins", () => {
    const opts = roleOptions(graph, { kind: "RoleBinding", namespace: "shop" }).map((o) => `${o.kind}/${o.name}${o.broad ? "!" : ""}`)
    expect(opts).toEqual(["Role/reader", "ClusterRole/pod-reader", "ClusterRole/view", "ClusterRole/edit!", "ClusterRole/admin!", "ClusterRole/cluster-admin!"])
  })
  test("a ClusterRoleBinding only ClusterRoles", () => {
    expect(roleOptions(graph, { kind: "ClusterRoleBinding" }).every((o) => o.kind === "ClusterRole")).toBe(true)
  })
  test("ServiceAccounts of a namespace", () => {
    expect(serviceAccountsIn(graph, "shop")).toEqual(["api"])
  })
  test("RBAC nodes", () => {
    expect(isRbacNode({ kind: "Role", apiVersion: "rbac.authorization.k8s.io/v1" })).toBe(true)
    expect(isRbacNode({ kind: "Role", apiVersion: "example.com/v1" })).toBe(false)
    expect(isRbacNode({ kind: "ServiceAccount", apiVersion: "v1" })).toBe(true)
  })
  test("added nodes", () => {
    const after = { ...graph, nodes: [...graph.nodes, node("Role", "api", "shop")] }
    expect(addedNodes(graph, after, "Role")).toEqual(["Role/shop/api"])
  })
})

describe("subjects and names", () => {
  test("ServiceAccounts of ClusterRoleBindings need a namespace", () => {
    expect(subjectComplete({ kind: "ServiceAccount", name: "api" }, "RoleBinding")).toBe(true)
    expect(subjectComplete({ kind: "ServiceAccount", name: "api" }, "ClusterRoleBinding")).toBe(false)
    expect(subjectComplete({ kind: "Group", name: "" }, "RoleBinding")).toBe(false)
  })
  test("ServiceAccount names", () => {
    expect(serviceAccountNameProblem("config-reader")).toBeUndefined()
    expect(serviceAccountNameProblem("Config")).toBeDefined()
  })
})
