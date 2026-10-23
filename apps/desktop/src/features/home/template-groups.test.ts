import { describe, expect, test } from "bun:test"
import type { Template } from "@/lib/api/types"
import { templateSections } from "./template-groups"

const t = (id: string, extra: Partial<Template> = {}): Template => ({
  id,
  name: id,
  description: "",
  cloud: "",
  difficulty: "beginner",
  tags: [],
  ...extra,
})

const templates: Template[] = [
  t("aws-queue-worker", { cloud: "aws" }),
  t("gcp-load-balancer", { cloud: "gcp", category: "edge-patterns", pattern: "load-balancer" }),
  t("aws-load-balancer", { cloud: "aws", category: "edge-patterns", pattern: "load-balancer" }),
  t("aws-reverse-proxy", { cloud: "aws", category: "edge-patterns", pattern: "reverse-proxy", difficulty: "intermediate" }),
  t("aws-api-gateway", { cloud: "aws", category: "edge-patterns", pattern: "api-gateway" }),
  t("docker-go-nginx", { kind: "docker" }),
  t("docker-load-balancer", { kind: "docker", category: "edge-patterns", pattern: "load-balancer" }),
  t("docker-reverse-proxy", { kind: "docker", category: "edge-patterns", pattern: "reverse-proxy" }),
  t("k8s-api-gateway", { kind: "kubernetes", category: "edge-patterns", pattern: "api-gateway" }),
]
const ids = (ts: Template[]) => ts.map((x) => x.id)

describe("New workflow template groups", () => {
  test("edge patterns are split from the general starters of the selected kind, in pattern order", () => {
    const docker = templateSections(templates, { kind: "docker", level: "all", filter: "all", cloud: "all" })
    expect(ids(docker.general)).toEqual(["docker-go-nginx"])
    expect(ids(docker.edge)).toEqual(["docker-reverse-proxy", "docker-load-balancer"])
    expect(ids(templateSections(templates, { kind: "kubernetes", level: "all", filter: "all", cloud: "all" }).edge)).toEqual(["k8s-api-gateway"])
  })

  test("Terraform edge patterns sort by pattern then cloud and narrow to one provider", () => {
    const all = templateSections(templates, { kind: "terraform", level: "all", filter: "all", cloud: "all" })
    expect(ids(all.general)).toEqual(["aws-queue-worker"])
    expect(ids(all.edge)).toEqual(["aws-reverse-proxy", "aws-api-gateway", "aws-load-balancer", "gcp-load-balancer"])
    const gcp = templateSections(templates, { kind: "terraform", level: "all", filter: "all", cloud: "gcp" })
    expect(ids(gcp.edge)).toEqual(["gcp-load-balancer"])
    // The provider only narrows the Edge patterns group.
    expect(ids(gcp.general)).toEqual(["aws-queue-worker"])
  })

  test("the Edge patterns filter hides the general starters; difficulty applies to both", () => {
    const edgeOnly = templateSections(templates, { kind: "terraform", level: "all", filter: "edge-patterns", cloud: "all" })
    expect(edgeOnly.general).toEqual([])
    expect(edgeOnly.edge).toHaveLength(4)
    const beginner = templateSections(templates, { kind: "terraform", level: "beginner", filter: "all", cloud: "aws" })
    expect(ids(beginner.edge)).toEqual(["aws-api-gateway", "aws-load-balancer"])
  })
})
