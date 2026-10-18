import { useQueries, useQuery, type UseQueryResult } from "@tanstack/react-query"
import { useCallback, useMemo } from "react"
import { api } from "@/lib/api/client"
import type { NodeKind, ResourceSchema, WorkflowNode } from "@/lib/api/types"

export const schemaKey = (kind: NodeKind, type: string) => `${kind}:${type}`

function schemaQuery(kind: NodeKind, type: string) {
  return {
    queryKey: ["schema", kind, type],
    queryFn: () => api.schema(kind, type),
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
    (results: UseQueryResult<ResourceSchema>[]) => {
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

export function nodeLabel(n: WorkflowNode, schema?: ResourceSchema) {
  return n.label || schema?.label || n.type
}
