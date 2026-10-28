// The Terraform sources an editor resolves names in: the workflow's saved
// .tf files (fetched once per workflow version) overlaid with unsaved
// buffers and the document being edited.

import { api } from "@/lib/api/client"
import type { CodeWorkspace } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { useWorkflowStore } from "@/state/workflow-store"
import { SymbolIndex } from "./symbols"

export interface IntelOptions {
  workflowId: string
  /** Workflow path of the edited file. */
  path: string
  /** Opens a file at a line (go to definition across files). */
  openFile: (path: string, opts: { line: number; column?: number }) => void
  /** Unsaved contents of every edited or new file, by path. */
  buffers: () => Record<string, string>
}

const EMPTY: CodeWorkspace = { files: {}, modules: [] }

function version(workflowId: string): string {
  const s = useWorkflowStore.getState()
  return s.workflow?.id === workflowId ? s.version : ""
}

const workspaceKey = (workflowId: string) => ["code-workspace", workflowId, version(workflowId)]

/** The saved workspace if loaded (starting the load otherwise). */
export function savedWorkspace(workflowId: string): CodeWorkspace | undefined {
  const key = workspaceKey(workflowId)
  const hit = queryClient.getQueryData<CodeWorkspace>(key)
  if (!hit) void loadWorkspace(workflowId).catch(() => {})
  return hit
}

export function loadWorkspace(workflowId: string): Promise<CodeWorkspace> {
  return queryClient.fetchQuery({
    queryKey: workspaceKey(workflowId),
    queryFn: () => api.codeWorkspace(workflowId),
    staleTime: Infinity,
    gcTime: 5 * 60_000,
  })
}

let last: { key: string; files: Record<string, string>; index: SymbolIndex } | null = null

/** Symbols of the workspace with unsaved text applied; doc is the edited file's current text. */
export function symbolIndex(opts: IntelOptions, doc: string, ws: CodeWorkspace = savedWorkspace(opts.workflowId) ?? EMPTY): SymbolIndex {
  const files: Record<string, string> = { ...ws.files, ...opts.buffers(), [opts.path]: doc }
  const key = `${opts.workflowId}\n${version(opts.workflowId)}`
  // Reuse the index while no file changed (hover and completion on the same text).
  if (last && last.key === key && sameFiles(last.files, files)) return last.index
  const index = new SymbolIndex(files, ws.modules)
  last = { key, files, index }
  return index
}

function sameFiles(a: Record<string, string>, b: Record<string, string>): boolean {
  const ka = Object.keys(a)
  if (ka.length !== Object.keys(b).length) return false
  return ka.every((k) => a[k] === b[k])
}
