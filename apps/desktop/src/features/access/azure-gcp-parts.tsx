import { CheckIcon, ChevronsUpDownIcon, ShieldAlertIcon, SparklesIcon } from "lucide-react"
import { type ReactNode, useMemo, useState } from "react"
import { useSyncedState } from "@/components/use-synced-state"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Command, CommandEmpty, CommandGroup, CommandInput, CommandItem, CommandList } from "@/components/ui/command"
import { Input } from "@/components/ui/input"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { cn } from "@/lib/utils"
import { type CatalogRole, filterRoles, suggestedLabel } from "./azure-gcp-roles"

/** Shared parts of the Azure role assignment and GCP IAM editors. */

/** A keyword of the who / can do / on what form. */
export function FieldLabel({ children, hint }: { children: ReactNode; hint?: string }) {
  return (
    <div className="flex items-baseline gap-1.5">
      <span className="text-xs font-medium">{children}</span>
      {hint && <span className="text-[11px] text-muted-foreground">{hint}</span>}
    </div>
  )
}

/** An HCL expression field, committed when left or on Enter. */
export function ExprInput({
  value,
  onChange,
  placeholder,
  label,
  testId,
  className,
}: {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  label: string
  testId: string
  className?: string
}) {
  const [text, setText] = useSyncedState(value)
  const commit = () => {
    if (text !== value) onChange(text)
  }
  return (
    <Input
      aria-label={label}
      className={cn("h-7 min-w-0 flex-1 font-mono text-xs", className)}
      placeholder={placeholder}
      value={text}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") commit()
      }}
      data-testid={testId}
    />
  )
}

/** "Roles: <official list> (retrieved YYYY-MM-DD)." */
export function RoleSource({ source }: { source?: { title: string; url: string; retrieved: string } }) {
  if (!source) return null
  return (
    <p className="text-[10px] text-muted-foreground">
      Roles:{" "}
      <a className="underline underline-offset-2" href={source.url} target="_blank" rel="noreferrer">
        {source.title}
      </a>{" "}
      (retrieved {source.retrieved}).
    </p>
  )
}

/** Messages of what blocks saving. */
export function Problems({ problems, testId }: { problems: string[]; testId: string }) {
  if (problems.length === 0) return null
  return (
    <ul className="flex flex-col gap-0.5 text-[11px] text-amber-700 dark:text-amber-400" data-testid={testId}>
      {problems.map((p) => (
        <li key={p}>{p}</li>
      ))}
    </ul>
  )
}

/**
 * Searchable role combobox of the Azure and GCP editors: the roles suggested for the resource first (with the level
 * they fit), privileged/basic roles flagged, descriptions from the pinned official list. A role that isn't in the list
 * (a custom role) can be typed when `custom` accepts it.
 */
export function RolePicker({
  value,
  roles,
  loading,
  by,
  privilegedLabel,
  placeholder,
  custom,
  onPick,
  testId,
}: {
  /** The current role: a name (Azure) or an ID (GCP). */
  value: string
  roles: CatalogRole[]
  loading: boolean
  /** What value holds and onPick receives. */
  by: "name" | "id"
  privilegedLabel: string
  placeholder: string
  /** Whether typed text that isn't in the list can be used as is. */
  custom?: (text: string) => boolean
  onPick: (value: string) => void
  testId: string
}) {
  const [open, setOpen] = useState(false)
  const [search, setSearch] = useState("")
  const shown = useMemo(() => filterRoles(roles, search), [roles, search])
  const current = roles.find((r) => (by === "name" ? r.name : r.id) === value)
  const typed = search.trim()
  const offerCustom = typed !== "" && !!custom?.(typed) && !roles.some((r) => (by === "name" ? r.name : r.id) === typed)
  const suggested = shown.filter((r) => r.suggested)
  const others = shown.filter((r) => !r.suggested)
  const pick = (v: string) => {
    onPick(v)
    setOpen(false)
    setSearch("")
  }
  const item = (r: CatalogRole) => {
    const v = by === "name" ? r.name : r.id
    return (
      <CommandItem key={r.id} value={r.id} onSelect={() => pick(v)} data-testid={`${testId}-option`}>
        <CheckIcon className={cn("mt-0.5 size-3 self-start", v === value ? "opacity-100" : "opacity-0")} />
        <span className="flex min-w-0 flex-1 flex-col gap-0.5">
          <span className="flex flex-wrap items-center gap-1">
            <span className="text-xs font-medium">{r.name}</span>
            {by === "id" && <span className="font-mono text-[10px] text-muted-foreground">{r.id}</span>}
            {r.suggested && (
              <Badge variant="secondary" className="h-4 px-1.5 text-[10px]">
                <SparklesIcon />
                {suggestedLabel(r)}
              </Badge>
            )}
            {r.privileged && (
              <Badge variant="outline" className="h-4 border-amber-500/50 px-1.5 text-[10px] text-amber-700 dark:text-amber-400">
                <ShieldAlertIcon />
                {privilegedLabel}
              </Badge>
            )}
          </span>
          {r.description && <span className="line-clamp-2 text-[11px] text-muted-foreground">{r.description}</span>}
        </span>
      </CommandItem>
    )
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          className={cn("h-auto min-h-7 w-full justify-between gap-2 py-1 text-left text-xs", !value && "text-muted-foreground")}
          aria-label="Can do (role)"
          data-testid={testId}
        >
          <span className="flex min-w-0 flex-col">
            <span className="flex flex-wrap items-center gap-1">
              <span className={cn("truncate", by === "id" && !current && "font-mono")}>{current?.name ?? (value || placeholder)}</span>
              {current?.privileged && (
                <Badge variant="outline" className="h-4 border-amber-500/50 px-1.5 text-[10px] text-amber-700 dark:text-amber-400">
                  <ShieldAlertIcon />
                  {privilegedLabel}
                </Badge>
              )}
            </span>
            {by === "id" && current && <span className="truncate font-mono text-[10px] text-muted-foreground">{current.id}</span>}
          </span>
          <ChevronsUpDownIcon className="size-3 shrink-0 opacity-60" />
        </Button>
      </PopoverTrigger>
      <PopoverContent className="w-96 p-0" align="start">
        <Command shouldFilter={false}>
          <CommandInput placeholder={placeholder} value={search} onValueChange={setSearch} data-testid={`${testId}-search`} />
          <CommandList className="max-h-96">
            <CommandEmpty>{loading ? "Loading the official role list…" : "No role matches."}</CommandEmpty>
            {offerCustom && (
              <CommandGroup heading="Not in the list">
                <CommandItem value={`custom ${typed}`} onSelect={() => pick(typed)}>
                  Use <span className="font-mono">{typed}</span>
                </CommandItem>
              </CommandGroup>
            )}
            {suggested.length > 0 && <CommandGroup heading="Suggested for this resource">{suggested.map(item)}</CommandGroup>}
            {others.length > 0 && (
              <CommandGroup heading={search ? "Matching roles" : "All roles (type to search)"}>{others.map(item)}</CommandGroup>
            )}
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
