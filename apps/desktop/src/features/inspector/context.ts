import { createContext, useContext } from "react"
import type { Diagnostic, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import type { UserMode } from "@/state/workflow-store"

export interface InspectorContextValue {
  nodeId: string
  mode: UserMode
  /** Every node of the workflow (for reference pickers and chips). */
  nodes: WorkflowNode[]
  /** Schemas by schemaKey(kind, type). */
  schemas: Map<string, ResourceSchema>
  diagnostics: Diagnostic[]
  select: (nodeId: string) => void
}

export const InspectorContext = createContext<InspectorContextValue | null>(null)

export function useInspector() {
  const ctx = useContext(InspectorContext)
  if (!ctx) throw new Error("useInspector must be used inside InspectorContext")
  return ctx
}
