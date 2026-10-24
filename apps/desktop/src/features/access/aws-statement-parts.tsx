import { useQuery } from "@tanstack/react-query"
import { ArrowLeftIcon, CheckIcon, LinkIcon, PlusIcon, XIcon } from "lucide-react"
import { useState } from "react"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { api } from "@/lib/api/client"
import type { AWSPrincipal } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { ReferencePicker } from "../inspector/reference-picker"
import {
  actionsAtLevels,
  addActions,
  ARN_SUFFIXES,
  arnExpr,
  type ArnSuffixId,
  awsCatalogKey,
  describeResource,
  groupActions,
  PRINCIPAL_TYPES,
  resourceExpr,
  serviceOf,
  toggleAction,
} from "./aws-policy"
import { exprText, quoteHcl } from "./support"

/** A removable value chip. */
function Chip({ children, onRemove, label, mono = true }: { children: React.ReactNode; onRemove?: () => void; label: string; mono?: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex max-w-full items-center gap-1 rounded-md border bg-muted/50 py-0.5 pr-0.5 pl-1.5 text-[11px] leading-tight",
        mono && "font-mono",
      )}
    >
      <span className="min-w-0 truncate">{children}</span>
      {onRemove && (
        <button
          type="button"
          aria-label={`Remove ${label}`}
          onClick={onRemove}
          className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <XIcon className="size-3" />
        </button>
      )}
    </span>
  )
}

/** "Can do": the statement's actions as chips plus the action picker. */
export function ActionsEditor({ index, actions, onChange }: { index: number; actions: string[]; onChange: (actions: string[]) => void }) {
  return (
    <div className="flex flex-wrap items-center gap-1" data-testid={`aws-actions-${index}`}>
      {actions.map((a) => (
        <Chip key={a} label={a} onRemove={() => onChange(actions.filter((x) => x !== a))}>
          {a === "*" ? "* (every action of every service)" : a}
        </Chip>
      ))}
      <ActionPicker actions={actions} onChange={onChange} />
    </div>
  )
}

/** Picks actions: first a service (catalog), then its actions grouped by access level, with search and quick picks. */
function ActionPicker({ actions, onChange }: { actions: string[]; onChange: (actions: string[]) => void }) {
  const [open, setOpen] = useState(false)
  const [service, setService] = useState("")
  const [search, setSearch] = useState("")
  const services = useQuery({
    queryKey: awsCatalogKey(),
    queryFn: () => api.accessCatalog("aws"),
    staleTime: Infinity,
    enabled: open,
  })
  const catalog = useQuery({
    queryKey: awsCatalogKey(service),
    queryFn: () => api.accessCatalog("aws", { service }),
    staleTime: Infinity,
    enabled: open && !!service,
  })
  const all = catalog.data?.actions ?? []
  const groups = groupActions(all, search)
  const custom = search.trim()
  const offerCustom = /^[a-z0-9-]+:[A-Za-z0-9*]+$/.test(custom) && !all.some((a) => a.name === custom)

  return (
    <Popover
      open={open}
      onOpenChange={(o) => {
        setOpen(o)
        if (o) {
          setService(serviceOf(actions.find((a) => a.includes(":") && !a.startsWith("*")) ?? ""))
          setSearch("")
        }
      }}
    >
      <PopoverTrigger asChild>
        <Button variant="outline" size="xs" className="h-6 gap-1 text-[11px]" data-testid="aws-action-picker">
          <PlusIcon />
          Add actions
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-96 p-0" align="start">
        {!service ? (
          <Command>
            <CommandInput placeholder="Which service? (s3, dynamodb, logs…)" />
            <CommandList className="max-h-80">
              <CommandEmpty>{services.isPending ? "Loading services…" : "No such service."}</CommandEmpty>
              <CommandGroup heading="AWS services">
                {(services.data?.services ?? []).map((s) => (
                  <CommandItem key={s.prefix} value={`${s.prefix} ${s.name}`} onSelect={() => setService(s.prefix)} data-testid={`aws-service-${s.prefix}`}>
                    <span className="font-mono text-xs">{s.prefix}</span>
                    <span className="truncate text-[11px] text-muted-foreground">{s.name}</span>
                  </CommandItem>
                ))}
              </CommandGroup>
            </CommandList>
          </Command>
        ) : (
          <Command shouldFilter={false}>
            <div className="flex items-center gap-1 border-b px-2 py-1.5">
              <Button variant="ghost" size="icon-xs" aria-label="Pick another service" onClick={() => setService("")}>
                <ArrowLeftIcon />
              </Button>
              <span className="font-mono text-xs">{service}</span>
              <span className="truncate text-[11px] text-muted-foreground">{catalog.data?.services?.[0]?.name}</span>
            </div>
            <div className="flex flex-wrap gap-1 border-b px-2 py-1.5">
              <Button
                variant="outline"
                size="xs"
                className="h-6 text-[11px]"
                disabled={all.length === 0}
                onClick={() => onChange(addActions(actions, actionsAtLevels(all, ["List", "Read"])))}
                data-testid="aws-actions-all-read"
              >
                All read actions
              </Button>
              <Button
                variant="outline"
                size="xs"
                className="h-6 text-[11px]"
                disabled={all.length === 0}
                onClick={() => onChange(addActions(actions, actionsAtLevels(all, ["Write"])))}
                data-testid="aws-actions-all-write"
              >
                All write actions
              </Button>
            </div>
            <CommandInput placeholder={`Search ${service} actions…`} value={search} onValueChange={setSearch} />
            <CommandList className="max-h-80">
              {catalog.isPending ? (
                <div className="p-3 text-xs text-muted-foreground">Loading actions…</div>
              ) : (
                groups.length === 0 && !offerCustom && <CommandEmpty>No matching action.</CommandEmpty>
              )}
              {offerCustom && (
                <CommandGroup heading="Not in the reference">
                  <CommandItem value={custom} onSelect={() => onChange(addActions(actions, [custom]))}>
                    <PlusIcon className="size-3" />
                    <span className="font-mono text-xs">Add {custom}</span>
                  </CommandItem>
                </CommandGroup>
              )}
              {groups.map((g) => (
                <CommandGroup key={g.level} heading={g.level}>
                  {g.actions.map((a) => {
                    const on = actions.includes(a.name)
                    return (
                      <CommandItem key={a.name} value={a.name} onSelect={() => onChange(toggleAction(actions, a.name))} data-testid={`aws-action-${a.name}`}>
                        <CheckIcon className={cn("size-3", on ? "opacity-100" : "opacity-0")} />
                        <span className="font-mono text-xs">{a.name.slice(service.length + 1)}</span>
                        {a.resourceTypes?.length ? (
                          <span className="ml-auto truncate text-[10px] text-muted-foreground">{a.resourceTypes.join(", ")}</span>
                        ) : (
                          <span className="ml-auto text-[10px] text-muted-foreground">any resource</span>
                        )}
                      </CommandItem>
                    )
                  })}
                </CommandGroup>
              ))}
            </CommandList>
          </Command>
        )}
      </PopoverContent>
    </Popover>
  )
}

/** "On what": resources as chips; link a resource (with an ARN suffix) or type an ARN. */
export function ResourcesEditor({ index, resources, onChange }: { index: number; resources: string[]; onChange: (resources: string[]) => void }) {
  const [suffixId, setSuffixId] = useState<ArnSuffixId>("none")
  const [customSuffix, setCustomSuffix] = useState("")
  const [arn, setArn] = useState("")
  const suffix = suffixId === "custom" ? customSuffix : (ARN_SUFFIXES.find((s) => s.id === suffixId)?.suffix ?? "")
  const add = (expr: string) => {
    if (!resources.some((r) => r.trim() === expr.trim())) onChange([...resources, expr])
  }

  return (
    <div className="flex flex-col gap-1.5" data-testid={`aws-resources-${index}`}>
      {resources.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {resources.map((r, k) => {
            const d = describeResource(r)
            return (
              <Chip key={`${r}-${k}`} label={r} onRemove={() => onChange(resources.filter((_, j) => j !== k))}>
                {d.kind === "ref" ? (
                  <>
                    <LinkIcon className="mr-1 inline size-3 text-sky-500" />
                    {d.ref}
                    {d.suffix && <span className="ml-0.5 rounded bg-sky-500/15 px-1 text-sky-700 dark:text-sky-300">{d.suffix}</span>}
                  </>
                ) : d.kind === "text" ? (
                  d.text === "*" ? (
                    <span className="text-amber-700 dark:text-amber-300">* (every resource)</span>
                  ) : (
                    d.text
                  )
                ) : (
                  d.code
                )}
              </Chip>
            )
          })}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-1">
        <Select value={suffixId} onValueChange={(v) => setSuffixId(v as ArnSuffixId)}>
          <SelectTrigger size="sm" className="h-6 w-auto text-[11px]" aria-label="Which part of the resource" data-testid={`aws-resource-suffix-${index}`}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {ARN_SUFFIXES.map((s) => (
              <SelectItem key={s.id} value={s.id} className="text-xs">
                {s.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {suffixId === "custom" && (
          <Input
            aria-label="ARN suffix"
            className="h-6 w-24 font-mono text-[11px]"
            placeholder="/index/*"
            value={customSuffix}
            onChange={(e) => setCustomSuffix(e.target.value)}
          />
        )}
        <span data-testid="aws-resource-link">
          <ReferencePicker onPick={(expr) => add(resourceExpr(expr, suffix))} className="h-6" />
        </span>
      </div>
      <form
        className="flex items-center gap-1"
        onSubmit={(e) => {
          e.preventDefault()
          if (!arn.trim()) return
          add(arnExpr(arn))
          setArn("")
        }}
      >
        <Input
          aria-label="ARN"
          className="h-6 min-w-0 flex-1 font-mono text-[11px]"
          placeholder="or type an ARN: arn:aws:s3:::bucket/*"
          value={arn}
          onChange={(e) => setArn(e.target.value)}
          data-testid={`aws-resource-arn-${index}`}
        />
        <Button type="submit" variant="outline" size="xs" className="h-6 text-[11px]" disabled={!arn.trim()}>
          Add
        </Button>
      </form>
    </div>
  )
}

const PRINCIPAL_HINTS: Record<string, string> = {
  Service: "lambda.amazonaws.com",
  AWS: "arn:aws:iam::123456789012:root",
  Federated: "cognito-identity.amazonaws.com",
}

const PRINCIPAL_LABELS: Record<string, string> = {
  Service: "AWS service",
  AWS: "Account, role or user",
  Federated: "Federated identity",
  "*": "Anyone",
}

/** "Who": principals of trust and resource policies. */
export function PrincipalsEditor({
  index,
  principals,
  onChange,
}: {
  index: number
  principals: AWSPrincipal[]
  onChange: (principals: AWSPrincipal[]) => void
}) {
  const set = (k: number, p: AWSPrincipal) => onChange(principals.map((x, j) => (j === k ? p : x)))
  return (
    <div className="flex flex-col gap-1.5" data-testid={`aws-principals-${index}`}>
      {principals.map((p, k) => (
        <PrincipalRow
          key={k}
          principal={p}
          onChange={(next) => set(k, next)}
          onRemove={() => onChange(principals.filter((_, j) => j !== k))}
        />
      ))}
      <Button
        variant="ghost"
        size="xs"
        className="h-6 self-start text-[11px]"
        onClick={() => onChange([...principals, { type: "Service", values: [] }])}
        data-testid={`aws-add-principal-${index}`}
      >
        <PlusIcon />
        Add who
      </Button>
    </div>
  )
}

function PrincipalRow({ principal: p, onChange, onRemove }: { principal: AWSPrincipal; onChange: (p: AWSPrincipal) => void; onRemove: () => void }) {
  const [text, setText] = useState("")
  const known = (PRINCIPAL_TYPES as readonly string[]).includes(p.type)
  const add = (expr: string) => {
    if (!p.values.some((v) => v.trim() === expr.trim())) onChange({ ...p, values: [...p.values, expr] })
  }
  return (
    <div className="flex flex-col gap-1 rounded-md border border-dashed p-1.5">
      <div className="flex items-center gap-1">
        <Select
          value={p.type}
          onValueChange={(type) => onChange(type === "*" ? { type, values: ['"*"'] } : { type, values: p.type === "*" ? [] : p.values })}
        >
          <SelectTrigger size="sm" className="h-6 w-auto text-[11px]" aria-label="Kind of principal">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {!known && <SelectItem value={p.type}>{p.type}</SelectItem>}
            {PRINCIPAL_TYPES.map((t) => (
              <SelectItem key={t} value={t} className="text-xs">
                {PRINCIPAL_LABELS[t]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="flex-1" />
        <Button variant="ghost" size="icon-xs" aria-label="Remove who" onClick={onRemove}>
          <XIcon />
        </Button>
      </div>
      {p.type === "*" ? (
        <p className="text-[11px] text-amber-700 dark:text-amber-300">Anyone on the internet, unless a condition narrows it.</p>
      ) : (
        <>
          <div className="flex flex-wrap gap-1">
            {p.values.map((v, k) => (
              <Chip key={`${v}-${k}`} label={v} onRemove={() => onChange({ ...p, values: p.values.filter((_, j) => j !== k) })}>
                {exprText(v)}
              </Chip>
            ))}
          </div>
          <div className="flex items-center gap-1">
            <form
              className="flex min-w-0 flex-1 items-center gap-1"
              onSubmit={(e) => {
                e.preventDefault()
                if (!text.trim()) return
                add(quoteHcl(text.trim()))
                setText("")
              }}
            >
              <Input
                aria-label="Principal"
                className="h-6 min-w-0 flex-1 font-mono text-[11px]"
                placeholder={PRINCIPAL_HINTS[p.type] ?? ""}
                value={text}
                onChange={(e) => setText(e.target.value)}
              />
              <Button type="submit" variant="outline" size="xs" className="h-6 text-[11px]" disabled={!text.trim()}>
                Add
              </Button>
            </form>
            {p.type !== "Service" && <ReferencePicker onPick={add} className="h-6" />}
          </div>
        </>
      )}
    </div>
  )
}
