import { useMemo, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { RefreshCw, RotateCcw } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { api } from "@/lib/api/client"
import type { EnvSetting, EnvSettings } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { changeEnvironments } from "./actions"
import { initialSettingValue, settingsPatch, settingValueError } from "./model"

/** The common knobs of one workload in one environment, written to that environment's own files. */
export function EditPane({ environment }: { environment: string }) {
  if (environment === "") {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle className="text-sm">Base</EmptyTitle>
          <EmptyDescription className="text-xs">
            Every environment builds on the base: change it in the Code tab or on the canvas. Pick an environment to edit
            what only that environment overrides.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }
  return <EnvironmentSettings key={environment} environment={environment} />
}

function EnvironmentSettings({ environment }: { environment: string }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const updatedAt = useWorkflowStore((s) => s.workflow!.updatedAt)
  const [target, setTarget] = useState<string | null>(null)
  const settings = useQuery({
    queryKey: ["environment-settings", workflowId, environment, target ?? "", updatedAt],
    queryFn: () => api.environmentSettings(workflowId, environment, target ?? undefined),
    placeholderData: keepPreviousData,
  })

  if (settings.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (settings.isError && !settings.data) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle>Couldn't read the settings of {environment}</EmptyTitle>
          <EmptyDescription>{errorMessage(settings.error)}</EmptyDescription>
        </EmptyHeader>
        <Button size="sm" variant="outline" onClick={() => void settings.refetch()}>
          <RefreshCw /> Try again
        </Button>
      </Empty>
    )
  }

  const data = settings.data
  const docker = data.targets.some((t) => t.kind === "service")
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5">
        <Select value={data.target || undefined} onValueChange={setTarget} disabled={data.targets.length === 0}>
          <SelectTrigger size="sm" className="h-7 max-w-64 min-w-40 text-xs" aria-label={docker ? "Service" : "Workload"} data-testid="env-target">
            <SelectValue placeholder={docker ? "No services" : "No workloads"} />
          </SelectTrigger>
          <SelectContent>
            {data.targets.map((t) => (
              <SelectItem key={t.id} value={t.id} data-testid={`env-target-${t.id}`}>
                <span className="text-muted-foreground">{t.kind}</span> {t.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {settings.isFetching && <Spinner className="size-3" />}
        {data.file && (
          <span className="min-w-0 truncate text-[11px] text-muted-foreground" title={data.file}>
            Writes to <span className="font-mono">{data.file}</span>
          </span>
        )}
      </div>
      {data.note && <p className="shrink-0 border-b px-3 py-1.5 text-[11px] text-muted-foreground">{data.note}</p>}
      {data.settings.length === 0 ? (
        <Empty className="flex-1">
          <EmptyHeader>
            <EmptyTitle className="text-sm">Nothing to edit</EmptyTitle>
            <EmptyDescription className="text-xs">
              {data.targets.length === 0 ? "This environment renders no workloads." : "This workload has no settings environments can change."}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : (
        <SettingsForm environment={environment} data={data} />
      )}
    </div>
  )
}

function SettingsForm({ environment, data }: { environment: string; data: EnvSettings }) {
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [pending, setPending] = useState<string | null>(null)
  // New settings from the daemon (saved, other target, refetch): start from them.
  const [seen, setSeen] = useState(data)
  if (seen !== data) {
    setSeen(data)
    setDraft({})
  }

  const groups = useMemo(() => {
    const byGroup = new Map<string, EnvSetting[]>()
    for (const s of data.settings) byGroup.set(s.group, [...(byGroup.get(s.group) ?? []), s])
    return [...byGroup]
  }, [data.settings])
  const values = settingsPatch(data.settings, draft)
  const changed = Object.keys(values).length
  // Compose and Kubernetes write CPU and memory differently (1.5 / 512M vs 1500m / 512Mi).
  const kind = useWorkflowStore((s) => (s.workflow?.kind === "docker" ? "docker" : "kubernetes"))
  const invalid = data.settings.some((s) => s.key in draft && settingValueError(s.type, draft[s.key], kind) !== null)

  /** `busy`: "save", or the key being reset. */
  const write = async (busy: string, patch: Record<string, string | null>, success: string) => {
    setPending(busy)
    await changeEnvironments((id) => api.setEnvironmentSettings(id, environment, { target: data.target, values: patch }), {
      success,
      failure: `Couldn't save ${environment}`,
    })
    setPending(null)
  }

  return (
    <form
      className="flex min-h-0 flex-1 flex-col"
      onSubmit={(e) => {
        e.preventDefault()
        if (changed > 0 && !invalid && !pending) void write("save", values, `Saved to ${data.file ?? environment}`)
      }}
    >
      <div className="min-h-0 flex-1 overflow-auto">
        {groups.map(([group, items]) => (
          <section key={group} aria-label={group} className="border-b last:border-b-0">
            <h4 className="px-3 pt-2 pb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">{group}</h4>
            {items.map((s) => (
              <SettingRow
                key={s.key}
                setting={s}
                kind={kind}
                value={draft[s.key] ?? initialSettingValue(s)}
                onChange={(v) => setDraft((d) => ({ ...d, [s.key]: v }))}
                resetting={pending === s.key}
                onReset={() => void write(s.key, { [s.key]: null }, `${s.label} follows the base again`)}
                disabled={pending !== null}
              />
            ))}
          </section>
        ))}
      </div>
      <div className="flex shrink-0 items-center gap-2 border-t px-3 py-1.5">
        <span className="min-w-0 truncate text-[11px] text-muted-foreground">
          {changed > 0 ? `${changed} change${changed === 1 ? "" : "s"} to ${environment}` : "Empty fields follow the base."}
        </span>
        <Button type="button" size="xs" variant="ghost" className="ml-auto" disabled={changed === 0 || pending !== null} onClick={() => setDraft({})}>
          Discard
        </Button>
        <Button type="submit" size="xs" disabled={changed === 0 || invalid || pending !== null} data-testid="env-save">
          {pending === "save" && <Spinner className="size-3" />}
          Save
        </Button>
      </div>
    </form>
  )
}

function SettingRow({
  setting: s,
  kind,
  value,
  onChange,
  onReset,
  resetting,
  disabled,
}: {
  setting: EnvSetting
  kind: "kubernetes" | "docker"
  value: string
  onChange: (value: string) => void
  onReset: () => void
  resetting: boolean
  disabled: boolean
}) {
  const error = settingValueError(s.type, value, kind)
  const input = (
    <Input
      id={`env-setting-${s.key}`}
      type={s.type === "int" ? "number" : "text"}
      min={s.type === "int" ? 0 : undefined}
      inputMode={s.type === "int" ? "numeric" : undefined}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={s.base || s.value || undefined}
      disabled={!s.editable || disabled}
      spellCheck={false}
      autoComplete="off"
      aria-invalid={!!error}
      title={error ?? undefined}
      className="h-7 w-32 shrink-0 font-mono text-xs"
      data-testid={`env-setting-${s.key}`}
    />
  )
  return (
    <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-1 hover:bg-muted/40">
      <label htmlFor={`env-setting-${s.key}`} className="w-44 min-w-0 shrink truncate text-xs" title={s.label}>
        {s.label}
      </label>
      {s.editable ? (
        input
      ) : (
        <Tooltip>
          <TooltipTrigger asChild>
            <span tabIndex={0} className="shrink-0">
              {input}
            </span>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">{s.reason ?? "Can't be changed here"}</TooltipContent>
        </Tooltip>
      )}
      <span className={cn("min-w-0 truncate font-mono text-[11px] text-muted-foreground", error && "text-destructive")} title={error ?? s.base}>
        {error ?? (s.base ? `base: ${s.base}` : "not set in base")}
      </span>
      {s.overridden && (
        <span className="flex shrink-0 items-center gap-0.5">
          <Badge variant="secondary" className="h-4 px-1.5 text-[10px]">
            overridden
          </Badge>
          <Button
            type="button"
            size="icon-xs"
            variant="ghost"
            aria-label={`Reset ${s.label} to the base`}
            title="Reset to the base"
            disabled={!s.editable || disabled}
            onClick={onReset}
            data-testid={`env-setting-reset-${s.key}`}
          >
            {resetting ? <Spinner className="size-3" /> : <RotateCcw />}
          </Button>
        </span>
      )}
    </div>
  )
}
