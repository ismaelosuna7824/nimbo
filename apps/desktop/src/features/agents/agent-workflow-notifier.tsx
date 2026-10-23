import { useEffect } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { subscribeEvents } from "@/lib/api/client"
import { useWorkflowStore } from "@/state/workflow-store"

/**
 * An AI agent (through the MCP server) created a workflow: the user is otherwise left looking at the workflow they had
 * open while the agent works on another one. Home opens it right away; an open workflow gets a notification with Open.
 * Workflows created in the app itself open on their own and are ignored here.
 */
export function AgentWorkflowNotifier() {
  const queryClient = useQueryClient()
  useEffect(
    () =>
      subscribeEvents(undefined, (e) => {
        if (e.type !== "workflow.created") return
        void queryClient.invalidateQueries({ queryKey: ["workflows"] })
        if (e.data.origin !== "mcp") return
        const store = useWorkflowStore.getState()
        if (store.workflow?.id === e.data.id) return
        const open = () => void useWorkflowStore.getState().open(e.data.id)
        if (!store.workflow) {
          open()
          return
        }
        toast(`Your AI agent created “${e.data.name}”`, {
          description: `You're looking at “${store.workflow.name}”. Open the new workflow to watch it.`,
          duration: 20_000,
          action: { label: "Open", onClick: open },
        })
      }),
    [queryClient],
  )
  return null
}
