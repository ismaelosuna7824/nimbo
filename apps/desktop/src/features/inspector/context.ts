import { createContext, useContext } from "react"
import type { Attr, Diagnostic, NamedBlock, ResourceSchema, WorkflowNode } from "@/lib/api/types"
import type { UserMode } from "@/state/workflow-store"

export interface InspectorContextValue {
  nodeId: string
  mode: UserMode
  /** Every node of the workflow (for reference pickers and chips). */
  nodes: WorkflowNode[]
  /** The workflow's input variables and locals (Link to… lists them). */
  variables: NamedBlock[]
  locals: Attr[]
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
