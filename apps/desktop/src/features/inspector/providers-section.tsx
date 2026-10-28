import { PlusIcon, Trash2Icon } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { Value } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { exprOf, getAttr } from "./body"
import { addProblem, addProvider, PROVIDER_REGION, providerNames, providerRef, providerUsers, regionVariables } from "./providers"

const TYPED = "__typed__"

/**
 * Provider configurations of a Terraform workflow: each one with its alias and region, removable when
 * no resource uses it, and a form adding one with an alias (e.g. a second AWS region for disaster
 * recovery) whose region is typed or linked to a variable. Resources pick an alias in Meta-arguments.
 */
export function ProvidersSection() {
  const wf = useWorkflowStore((s) => s.workflow)!
  const update = useWorkflowStore((s) => s.update)
  const providers = wf.providers ?? []
  const names = providerNames(providers, wf.nodes)
  const vars = regionVariables(wf.variables ?? [])
  const [name, setName] = useState(names[0] ?? "")
  const [alias, setAlias] = useState("")
  const [regionVar, setRegionVar] = useState(TYPED)
  const [regionText, setRegionText] = useState("")
  const [adding, setAdding] = useState(false)
  const chosen = name || names[0] || ""
  const problem = addProblem(providers, chosen, alias.trim())
  const hasRegion = PROVIDER_REGION[chosen] != null

  const add = () => {
    const region: Value | undefined = !hasRegion
      ? undefined
      : regionVar !== TYPED
        ? { expr: regionVar }
        : regionText.trim()
          ? { literal: regionText.trim() }
          : undefined
    update((d) => ({ ...d, providers: addProvider(d.providers ?? [], chosen, alias.trim(), region) }))
    setAlias("")
    setRegionText("")
    setRegionVar(TYPED)
    setAdding(false)
  }

  if (names.length === 0) return null
  return (
    <section className="flex flex-col gap-2" data-testid="providers-section">
      <div className="flex items-center">
        <span className="text-xs font-medium">Providers</span>
        <Button variant="ghost" size="xs" className="ml-auto h-5 text-[11px] text-muted-foreground" onClick={() => setAdding(!adding)}>
          <PlusIcon />
          Add
        </Button>
      </div>
      {providers.length === 0 && <p className="text-[11px] text-muted-foreground">No provider blocks: resources use the provider's defaults.</p>}
      {providers.map((p, i) => {
        const users = providerUsers(wf.nodes, p)
        const regionAttr = PROVIDER_REGION[p.name]
        const region = regionAttr ? getAttr(p.body, regionAttr) : undefined
        // A provider block nothing uses: quiet row, but say what it still costs (its emulator starts with the sandbox).
        const unused = users.length === 0
        return (
          <div
            key={`${providerRef(p)}-${i}`}
            className={cn(
              "flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs",
              unused && "border-dashed text-muted-foreground",
            )}
          >
            <span className="font-mono font-medium">{providerRef(p)}</span>
            {region && <span className="truncate font-mono text-muted-foreground">{exprOf(region)}</span>}
            {unused ? (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="ml-auto text-[10px]">Not used</span>
                </TooltipTrigger>
                <TooltipContent className="max-w-64">
                  No resource uses this provider. It is still configured, so the sandbox starts its local emulator and
                  the export keeps it: remove it if you don&apos;t need it.
                </TooltipContent>
              </Tooltip>
            ) : (
              <span className="ml-auto text-[10px] text-muted-foreground">{users.length} using</span>
            )}
            <Tooltip>
              <TooltipTrigger asChild>
                <span>
                  <Button
                    variant="ghost"
                    size="icon-xs"
                    disabled={users.length > 0}
                    onClick={() => update((d) => ({ ...d, providers: (d.providers ?? []).filter((_, j) => j !== i) }))}
                    aria-label={`Remove provider ${providerRef(p)}`}
                  >
                    <Trash2Icon />
                  </Button>
                </span>
              </TooltipTrigger>
              <TooltipContent>{users.length > 0 ? `Used by ${users.length} resource(s): switch them to another provider first` : "Remove"}</TooltipContent>
            </Tooltip>
          </div>
        )
      })}
      {adding && (
        <div className="flex flex-col gap-2 rounded-md border border-dashed p-2.5">
          <div className="flex gap-1.5">
            <Select value={chosen} onValueChange={setName}>
              <SelectTrigger className="h-7 w-28 text-xs" aria-label="Provider">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {names.map((n) => (
                  <SelectItem key={n} value={n}>
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Input value={alias} placeholder="alias (e.g. dr)" className="h-7 flex-1 font-mono text-xs" onChange={(e) => setAlias(e.target.value)} />
          </div>
          {hasRegion && (
            <div className="flex gap-1.5">
              <Select value={regionVar} onValueChange={setRegionVar}>
                <SelectTrigger className="h-7 w-36 text-xs" aria-label="Region from">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={TYPED}>Region (typed)</SelectItem>
                  {vars.map((v) => (
                    <SelectItem key={v} value={v}>
                      {v}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              {regionVar === TYPED && (
                <Input value={regionText} placeholder="e.g. us-west-2" className="h-7 flex-1 font-mono text-xs" onChange={(e) => setRegionText(e.target.value)} />
              )}
            </div>
          )}
          {problem && alias && <p className="text-[11px] text-destructive">{problem}</p>}
          <Button size="xs" className="self-start" disabled={problem !== null} onClick={add} data-testid="provider-add">
            Add provider
          </Button>
        </div>
      )}
    </section>
  )
}
