import { useQuery } from "@tanstack/react-query"
import { KeyRoundIcon } from "lucide-react"
import { useCallback, type ReactNode } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api/client"
import type { AccessEdit, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { ACCESS_EDITORS } from "./editors"
import { AccessWarnings, LeastPrivilegeOk, ReadOnlySource, RelatedLinks } from "./parts"
import { accessSupported } from "./support"

const accessKeys = {
  view: (workflowId: string, nodeId: string, version: string) => ["access", workflowId, nodeId, version] as const,
}

/**
 * The inspector's "Access" section of a Terraform node with a permission model: the provider's visual editor
 * (statements, role, members, policy sentences), least-privilege warnings, and read-only code for anything that can't
 * be shown visually.
 */
export function AccessSection({
  node,
  section: Section,
}: {
  node: WorkflowNode
  section: (props: { title: string; icon?: ReactNode; children: ReactNode }) => ReactNode
}) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const select = useWorkflowStore((s) => s.select)
  const file = useWorkflowStore((s) => node.file ?? s.workflow!.nodeFiles?.[node.id])
  const supported = accessSupported(node)
  // Saved edits from anywhere (inspector, code, MCP, grants) change updatedAt and refresh the view.
  const query = useQuery({
    queryKey: accessKeys.view(workflowId, node.id, updatedAt),
    queryFn: () => api.access(workflowId, node.id),
    enabled: supported,
    placeholderData: (prev, prevQuery) => (prevQuery?.queryKey[2] === node.id ? prev : undefined),
    retry: false,
  })

  const save = useCallback(
    async (edit: AccessEdit) => {
      const store = useWorkflowStore.getState()
      await store.flush()
      try {
        setWorkflow(await api.putAccess(workflowId, edit))
      } catch (err) {
        toast.error("Couldn't save the permissions", { description: errorMessage(err) })
        throw err
      }
    },
    [workflowId, setWorkflow],
  )

  if (!supported) return null
  const view = query.data
  const Editor = view ? ACCESS_EDITORS[view.provider] : undefined
  return (
    <Section title="Access" icon={<KeyRoundIcon className="size-3.5 text-amber-500" />}>
      <div className="flex flex-col gap-3" data-testid="access-section">
        {query.isPending && !view ? (
          <div className="flex flex-col gap-2">
            <Skeleton className="h-16" />
            <Skeleton className="h-16" />
          </div>
        ) : query.isError && !view ? (
          <ReadOnlySource title="Can't show these permissions visually" reason={errorMessage(query.error)} file={file} />
        ) : view ? (
          <>
            <AccessWarnings warnings={view.warnings.filter((w) => !w.path)} />
            {Editor ? (
              <Editor workflowId={workflowId} node={node} view={view} save={save} select={select} />
            ) : (
              <ReadOnlySource reason="No visual editor for this permission model yet." file={file} />
            )}
            {view.warnings.length === 0 && <LeastPrivilegeOk />}
            <RelatedLinks related={view.related} onSelect={select} />
          </>
        ) : null}
      </div>
    </Section>
  )
}
