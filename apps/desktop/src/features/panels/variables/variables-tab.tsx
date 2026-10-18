import { useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"
import { useShallow } from "zustand/react/shallow"
import { useWorkflowStore } from "@/state/workflow-store"
import { missingRequired } from "../lib/values"
import { LocalsEditor, OutputsEditor, VariablesEditor } from "./blocks-editors"
import { EnvironmentsEditor } from "./environments-editor"

type Section = "variables" | "outputs" | "locals" | "environments"

/** Below this width the environments editor becomes a section instead of a side column. */
const SIDE_BY_SIDE_MIN_PX = 1100

export function VariablesTab() {
  const workflow = useWorkflowStore(
    useShallow(
      (s) =>
        s.workflow && {
          variables: s.workflow.variables,
          outputs: s.workflow.outputs,
          locals: s.workflow.locals,
          environments: s.workflow.environments,
          activeEnvironment: s.workflow.activeEnvironment,
        },
    ),
  )
  const [section, setSection] = useState<Section>("variables")
  const [wide, setWide] = useState(true)
  const root = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const el = root.current
    if (!el) return
    const ro = new ResizeObserver(([entry]) => setWide(entry.contentRect.width >= SIDE_BY_SIDE_MIN_PX))
    ro.observe(el)
    return () => ro.disconnect()
  }, [])

  if (!workflow) return null

  const missing = missingRequired(workflow, workflow.activeEnvironment).length
  const sections: { id: Section; label: string; count: number; alert?: boolean }[] = [
    { id: "variables", label: "Variables", count: workflow.variables.length },
    { id: "outputs", label: "Outputs", count: workflow.outputs.length },
    { id: "locals", label: "Locals", count: workflow.locals.length },
  ]
  if (!wide) {
    sections.push({ id: "environments", label: "Environments", count: workflow.environments.length, alert: missing > 0 })
  }
  const shown = wide && section === "environments" ? "variables" : section

  return (
    <div
      ref={root}
      className={cn("grid h-full min-h-0", wide ? "grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)]" : "grid-cols-1")}
    >
      <div className="flex min-h-0 flex-col border-r">
        <div className="flex h-8 shrink-0 items-center gap-0.5 border-b px-1.5" role="tablist">
          {sections.map((s) => (
            <button
              key={s.id}
              type="button"
              role="tab"
              aria-selected={shown === s.id}
              onClick={() => setSection(s.id)}
              className={cn(
                "flex h-6 items-center gap-1.5 rounded-md px-2 text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50",
                shown === s.id && "bg-muted font-medium text-foreground",
              )}
            >
              {s.label}
              <span className={cn("text-[10px] tabular-nums opacity-70", s.alert && "text-destructive opacity-100")}>
                {s.count}
              </span>
            </button>
          ))}
        </div>
        <div className="min-h-0 flex-1 overflow-auto">
          {shown === "variables" && <VariablesEditor />}
          {shown === "outputs" && <OutputsEditor />}
          {shown === "locals" && <LocalsEditor />}
          {shown === "environments" && <EnvironmentsEditor />}
        </div>
      </div>
      {wide && (
        <div className="min-h-0">
          <EnvironmentsEditor />
        </div>
      )}
    </div>
  )
}
