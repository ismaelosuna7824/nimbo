// The workflow files Kubernetes intelligence reads besides the edited one: a
// chart's values.yaml, values.schema.json and templates, kustomizations,
// other manifests. Saved texts come from the same query cache as the Code
// tab (GET /files, GET /files/content per workflow version); unsaved buffers
// win over them.

import { api } from "@/lib/api/client"
import type { WorkflowFile, WorkflowFileContent } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { useWorkflowStore } from "@/state/workflow-store"

function version(workflowId: string): string {
  const s = useWorkflowStore.getState()
  return s.workflow?.id === workflowId ? s.version : ""
}

// Same keys as the Code tab's listing and file queries: one fetch serves both.
const listKey = (workflowId: string) => ["files", workflowId, version(workflowId)]
const fileKey = (workflowId: string, path: string) => ["file", workflowId, version(workflowId), path]

export class KubeProject {
  readonly workflowId: string
  /** Unsaved text of the open files. */
  readonly buffers: () => Record<string, string>

  constructor(workflowId: string, buffers: () => Record<string, string>) {
    this.workflowId = workflowId
    this.buffers = buffers
  }

  /** The saved text files' paths if listed (starting the listing otherwise). */
  paths(): string[] {
    const hit = queryClient.getQueryData<{ files: WorkflowFile[] }>(listKey(this.workflowId))
    if (!hit) void this.loadPaths().catch(() => {})
    const saved = hit?.files.filter((f) => !f.binary).map((f) => f.path) ?? []
    return [...new Set([...saved, ...Object.keys(this.buffers())])]
  }

  async loadPaths(): Promise<string[]> {
    const res = await queryClient.fetchQuery({ queryKey: listKey(this.workflowId), queryFn: () => api.files(this.workflowId), staleTime: Infinity })
    return [...new Set([...res.files.filter((f) => !f.binary).map((f) => f.path), ...Object.keys(this.buffers())])]
  }

  /** A file's text: unsaved buffer, else the cached saved text (undefined while not loaded). */
  cached(path: string): string | undefined {
    const b = this.buffers()[path]
    if (b !== undefined) return b
    return queryClient.getQueryData<WorkflowFileContent>(fileKey(this.workflowId, path))?.content
  }

  /** A file's text, fetched when needed; undefined when it doesn't exist or isn't text. */
  async text(path: string): Promise<string | undefined> {
    const b = this.buffers()[path]
    if (b !== undefined) return b
    try {
      const res = await queryClient.fetchQuery({
        queryKey: fileKey(this.workflowId, path),
        queryFn: () => api.fileContent(this.workflowId, path),
        staleTime: Infinity,
      })
      return res.content
    } catch {
      return undefined
    }
  }

  /** Texts of several files (missing ones left out). */
  async texts(paths: readonly string[]): Promise<Record<string, string>> {
    const entries = await Promise.all(paths.map(async (p) => [p, await this.text(p)] as const))
    return Object.fromEntries(entries.filter((e): e is readonly [string, string] => e[1] !== undefined))
  }

  /** Cached texts of several files (for synchronous features such as go to definition). */
  cachedTexts(paths: readonly string[]): Record<string, string> {
    const out: Record<string, string> = {}
    for (const p of paths) {
      const t = this.cached(p)
      if (t !== undefined) out[p] = t
    }
    return out
  }

  /** Starts loading files in the background. */
  prefetch(paths: readonly string[]) {
    for (const p of paths) if (this.cached(p) === undefined) void this.text(p)
  }
}
