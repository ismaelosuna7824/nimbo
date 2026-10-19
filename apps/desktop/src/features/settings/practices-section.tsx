import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Skeleton } from "@/components/ui/skeleton"
import { Switch } from "@/components/ui/switch"
import { api } from "@/lib/api/client"
import type { BestPracticeRule, Settings } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { Row, Section } from "./common"
import { SETTINGS_KEY, useSettings } from "./store"

/** Display order and labels of the rule categories. */
const CATEGORIES: { id: BestPracticeRule["category"]; label: string }[] = [
  { id: "security", label: "Security" },
  { id: "inputs", label: "Variables & outputs" },
  { id: "versions", label: "Versions" },
  { id: "hardcoding", label: "Hardcoded values" },
  { id: "tagging", label: "Tags & labels" },
  { id: "naming", label: "Naming" },
  { id: "structure", label: "Structure" },
]

/** Settings → Best-practice checks: the master switch and one switch per rule. */
export function PracticesSection() {
  const settings = useSettings()
  const rules = useQuery({ queryKey: ["best-practices", "rules"], queryFn: api.bestPracticeRules, staleTime: Infinity, retry: false })
  const queryClient = useQueryClient()

  const save = useMutation({
    mutationFn: (s: Settings) => api.saveSettings(s),
    onSuccess: async (s) => {
      queryClient.setQueryData(SETTINGS_KEY, s)
      // The daemon recomputes findings on read: refresh the open workflow's diagnostics only, so unsaved edits stay.
      const id = useWorkflowStore.getState().workflow?.id
      if (!id) return
      const fresh = await api.getWorkflow(id)
      useWorkflowStore.setState((st) => (st.workflow?.id === id ? { workflow: { ...st.workflow, diagnostics: fresh.diagnostics } } : {}))
    },
    onError: (err) => toast.error("Couldn’t save the best-practice settings", { description: errorMessage(err) }),
  })

  const current = settings.data
  const bp = current?.bestPractices ?? { disabledRules: [] }
  const enabled = !bp.disabled
  const update = (next: Settings["bestPractices"]) => {
    if (current) save.mutate({ ...current, bestPractices: next })
  }
  const toggleRule = (code: string, on: boolean) => {
    const disabledRules = on ? bp.disabledRules.filter((c) => c !== code) : [...bp.disabledRules, code]
    update({ ...bp, disabledRules })
  }

  return (
    <Section
      title="Best-practice checks"
      description="Nimbo reviews the Terraform on every edit and lists findings under Problems → Best practices, with why they matter and how to fix them. They are hints: they never block a deploy."
    >
      <div>
        <Row label="Best-practice checks" htmlFor="bp-enabled" description="Turn every check off, or pick the ones you want below.">
          {settings.isPending ? (
            <Skeleton className="h-5 w-9" />
          ) : (
            <Switch id="bp-enabled" checked={enabled} disabled={!current || save.isPending} onCheckedChange={(on) => update({ ...bp, disabled: !on })} />
          )}
        </Row>
        {rules.isPending ? (
          <Skeleton className="mt-4 h-40" />
        ) : (
          CATEGORIES.map(({ id, label }) => {
            const list = (rules.data ?? []).filter((r) => r.category === id)
            if (list.length === 0) return null
            return (
              <Row key={id} label={label}>
                <ul className={cn("flex flex-col gap-3", !enabled && "opacity-50")}>
                  {list.map((r) => (
                    <li key={r.code} className="flex items-start gap-3">
                      <Switch
                        id={r.code}
                        className="mt-0.5"
                        aria-label={r.title}
                        checked={!bp.disabledRules.includes(r.code)}
                        disabled={!enabled || !current || save.isPending}
                        onCheckedChange={(on) => toggleRule(r.code, on)}
                      />
                      <label htmlFor={r.code} className="min-w-0 flex-1 text-sm">
                        <span className="font-medium">{r.title}</span>
                        {r.severity === "warning" && (
                          <span className="ml-2 rounded bg-amber-500/12 px-1 text-[11px] text-amber-600 dark:text-amber-400">warning</span>
                        )}
                        <span className="block text-xs text-muted-foreground">{r.description}</span>
                      </label>
                    </li>
                  ))}
                </ul>
              </Row>
            )
          })
        )}
      </div>
    </Section>
  )
}
