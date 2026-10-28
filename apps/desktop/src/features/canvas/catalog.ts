import { useQueries, useQuery, type UseQueryResult } from "@tanstack/react-query"
import { useCallback, useMemo } from "react"
import type { NodeKind, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import { loadSchema } from "../panels/terraform/intel/catalog"

export const schemaKey = (kind: NodeKind, type: string) => `${kind}:${type}`

function schemaQuery(kind: NodeKind, type: string) {
  return {
    queryKey: ["schema", kind, type],
    // Batched with every schema asked for in the same tick (a workflow's node types at once, the code editor's) and
    // shared with code intelligence: one request instead of one per type. Null: a type the catalog doesn't have.
    queryFn: () => loadSchema(kind === "data" ? "data" : "resource", type),
    staleTime: Infinity,
    retry: false,
    enabled: kind !== "module",
  }
}

export function useSchema(kind: NodeKind | undefined, type: string | undefined) {
  return useQuery({ ...schemaQuery(kind ?? "resource", type ?? ""), enabled: !!kind && !!type && kind !== "module" })
}

/** Schemas (which include the palette summary: label, icon, floci support) for every distinct node type. */
export function useNodeSchemas(nodes: WorkflowNode[] | undefined): Map<string, ResourceSchema> {
  const signature = [...new Set((nodes ?? []).map((n) => schemaKey(n.kind, n.type)))].sort().join(" ")
  const types = useMemo(
    () =>
      signature
        .split(" ")
        .filter(Boolean)
        .map((key) => {
          const [kind, type] = key.split(":") as [NodeKind, string]
          return { kind, type }
        }),
    [signature],
  )
  const combine = useCallback(
    (results: UseQueryResult<ResourceSchema | null>[]) => {
      const m = new Map<string, ResourceSchema>()
      types.forEach((t, i) => {
        const d = results[i]?.data
        if (d) m.set(schemaKey(t.kind, t.type), d)
      })
      return m
    },
    [types],
  )
  return useQueries({ queries: types.map((t) => schemaQuery(t.kind, t.type)), combine })
}

/** Terraform address used in references: "aws_x.y", "data.aws_x.y" or "module.y". */
export function nodeAddress(n: Pick<WorkflowNode, "kind" | "type" | "name">) {
  if (n.kind === "data") return `data.${n.type}.${n.name}`
  if (n.kind === "module") return `module.${n.name}`
  return `${n.type}.${n.name}`
}

export function nodeLabel(n: Pick<WorkflowNode, "label" | "type">, schema?: ResourceSchema) {
  return n.label || schema?.label || n.type
}
