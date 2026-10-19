import { RotateCcwIcon } from "lucide-react"
import { useId, useMemo } from "react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { useSyncedState } from "@/components/use-synced-state"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { Workflow, WorkflowNode } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"

/** A root .tf file Terraform loads as configuration (mirrors hclcodec.ValidFileName). */
function fileNameError(name: string): string | undefined {
  if (/[/\\]/.test(name)) return "Use a file name in the project root, without folders."
  if (!name.endsWith(".tf") || name === ".tf") return "The file name must end in .tf."
  if (name.startsWith(".") || name.startsWith("#") || name.endsWith("~")) return "Terraform ignores this file name."
  if (name === "override.tf" || name.endsWith("_override.tf")) return "Override files are merged into other blocks; pick another name."
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(name)) return "The file name contains control characters."
  return undefined
}

/** The root .tf files the workflow writes blocks to, sorted. */
function workflowTfFiles(wf: Workflow): string[] {
  const files = new Set<string>(Object.values(wf.nodeFiles ?? {}))
  for (const b of [...wf.variables, ...wf.outputs, ...wf.providers, ...wf.other]) if (b.file) files.add(b.file)
  for (const p of [...(wf.localsBlocks ?? []), ...(wf.terraformBlocks ?? [])]) files.add(p.file)
  return [...files].sort()
}

/**
 * Pro mode: which root .tf file the node is exported to. Empty means Nimbo's recommended layout
 * (category files, one main.tf for small projects); typing a name moves the node there.
 */
export function FileField({ node }: { node: WorkflowNode }) {
  const setWorkflow = useWorkflowStore((s) => s.setWorkflow)
  const workflow = useWorkflowStore((s) => s.workflow!)
  const files = useMemo(() => workflowTfFiles(workflow), [workflow])
  const listId = useId()
  const [value, setValue] = useSyncedState(node.file ?? "")
  const trimmed = value.trim()
  const error = trimmed ? fileNameError(trimmed) : undefined
  const current = workflow.nodeFiles?.[node.id]

  const move = async (file: string) => {
    const store = useWorkflowStore.getState()
    await store.flush()
    try {
      setWorkflow(await api.patchNode(store.workflow!.id, node.id, { file }))
    } catch (err) {
      toast.error("Couldn't move the node", { description: errorMessage(err) })
      setValue(node.file ?? "")
    }
  }

  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={`${listId}-input`} className="text-xs font-medium">
        File
      </label>
      <div className="flex items-center gap-1">
        <Input
          id={`${listId}-input`}
          list={listId}
          className="h-8 font-mono text-xs"
          value={value}
          placeholder={current ? `${current} (automatic)` : "Automatic"}
          aria-invalid={!!error || undefined}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => setValue(e.target.value)}
          onBlur={() => {
            if (error) setValue(node.file ?? "")
            else if (trimmed !== (node.file ?? "")) void move(trimmed)
          }}
          onKeyDown={(e) => {
            if (e.key === "Enter") e.currentTarget.blur()
            if (e.key === "Escape") {
              setValue(node.file ?? "")
              e.currentTarget.blur()
            }
          }}
        />
        <datalist id={listId}>
          {files.map((f) => (
            <option key={f} value={f} />
          ))}
        </datalist>
        {node.file && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label="Use the recommended file" onClick={() => void move("")}>
                <RotateCcwIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Back to the recommended file</TooltipContent>
          </Tooltip>
        )}
      </div>
      <p className={error ? "text-[11px] text-destructive" : "text-[11px] text-muted-foreground"}>
        {error ??
          (node.file
            ? "Kept in this file on every export."
            : "Chosen by Nimbo: grouped by service category, or main.tf for small projects. Pick or type a file to move it.")}
      </p>
    </div>
  )
}
