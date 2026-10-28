import { create } from "zustand"
import { useWorkflowStore } from "@/state/workflow-store"

const KEY = "nimbo.openWorkflows"

function load(): string[] {
  try {
    const ids: unknown = JSON.parse(localStorage.getItem(KEY) ?? "[]")
    return Array.isArray(ids) ? ids.filter((x): x is string => typeof x === "string") : []
  } catch {
    return []
  }
}

/**
 * The workflows open in the rail, like an IDE's open files: in the order they were opened, kept across restarts.
 * One is active in the editor at a time (the workflow store's); the others are a click away.
 */
export const useOpenWorkflows = create<{ ids: string[] }>(() => ({ ids: load() }))

function save(ids: string[]) {
  localStorage.setItem(KEY, JSON.stringify(ids))
  useOpenWorkflows.setState({ ids })
}

function add(id: string) {
  const { ids } = useOpenWorkflows.getState()
  if (!ids.includes(id)) save([...ids, id])
}

/**
 * Takes a workflow out of the open list. Returns the one to show instead when it was the active one: its neighbor,
 * like closing an editor tab (the next one, else the previous).
 */
export function forgetOpen(id: string): string | undefined {
  const { ids } = useOpenWorkflows.getState()
  const i = ids.indexOf(id)
  if (i < 0) return undefined
  const rest = ids.filter((x) => x !== id)
  save(rest)
  return rest[i] ?? rest[i - 1]
}

/** Adds every workflow that opens (from Home, the palette, a template, a conversion…) to the list. Returns the unsubscribe. */
export function trackOpenWorkflows(): () => void {
  const current = useWorkflowStore.getState().workflow?.id
  if (current) add(current)
  return useWorkflowStore.subscribe((s, prev) => {
    const id = s.workflow?.id
    if (id && id !== prev.workflow?.id) add(id)
  })
}
