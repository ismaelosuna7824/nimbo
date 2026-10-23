import { RotateCcwIcon } from "lucide-react"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { ComposeEnvironment, ServiceFacts } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { overridden } from "../environments/model"

/** The active docker environment (its override file is `file`); undefined on the base. */
export function useActiveComposeEnvironment(): ComposeEnvironment | undefined {
  return useWorkflowStore((s) => {
    const c = s.workflow?.compose
    return c?.environment ? c.environments?.find((e) => e.name === c.environment) : undefined
  })
}

/**
 * "<env> override" next to a field the active environment's override file sets (edits of it go there), with a
 * reset that removes it from the override so the base value applies again. Nothing on the base or when the field
 * comes from the base files.
 */
export function OverrideMark({ facts, attrs, onReset }: { facts: ServiceFacts; attrs: string[]; onReset: (attrs: string[]) => void }) {
  const env = useActiveComposeEnvironment()
  if (!env || !overridden(facts, ...attrs)) return null
  const set = (facts.overrides ?? []).filter((o) => attrs.some((a) => o === a || (a === "deploy" && o.startsWith("deploy."))))
  return (
    <span
      className="inline-flex h-4 shrink-0 items-center gap-0.5 rounded-full border border-sky-500/40 bg-sky-500/10 pl-1.5 text-[10px] leading-none font-medium text-sky-700 dark:text-sky-300"
      data-testid={`override-${attrs[0]}`}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="cursor-default">{env.name} override</span>
        </TooltipTrigger>
        <TooltipContent className="max-w-60">
          Set in <span className="font-mono">{env.file}</span>: edits go there. Other environments and the base keep the base
          value.
        </TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="flex size-4 items-center justify-center rounded-full outline-none hover:bg-sky-500/20 focus-visible:ring-2 focus-visible:ring-ring/50"
            aria-label={`Reset to base (remove from ${env.file})`}
            onClick={() => onReset(set)}
            data-testid={`override-reset-${attrs[0]}`}
          >
            <RotateCcwIcon className="size-2.5" />
          </button>
        </TooltipTrigger>
        <TooltipContent>Reset to base</TooltipContent>
      </Tooltip>
    </span>
  )
}
