import type { WorkflowNode } from "@/lib/api/types"

/** Terraform address of a node: "aws_s3_bucket.b", "data.x.y" or "module.m". */
export function nodeAddress(n: WorkflowNode): string {
  if (n.kind === "module") return `module.${n.name}`
  return n.kind === "data" ? `data.${n.type}.${n.name}` : `${n.type}.${n.name}`
}

/** Label shown to users: the custom label when set, else the address. */
export function nodeDisplayName(n: WorkflowNode): string {
  return n.label || nodeAddress(n)
}

export function nodeLabelById(nodes: WorkflowNode[] | undefined): Record<string, string> {
  const out: Record<string, string> = {}
  for (const n of nodes ?? []) out[n.id] = nodeDisplayName(n)
  return out
}
