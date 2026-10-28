import { useEffect, useMemo, useState } from "react"
import { keepPreviousData, useQuery, useQueryClient } from "@tanstack/react-query"
import { Download, Eye, EyeOff, RefreshCw, ShieldCheck, Undo2 } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { useShownVersion } from "@/features/panels/lib/use-shown-version"
import { showObject } from "@/features/kubernetes/actions"
import { useSettings } from "@/features/settings/store"
import { api } from "@/lib/api/client"
import type { ComplianceControl, ComplianceResource, ComplianceResult } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { exportReport } from "./export"
import { clearComplianceHighlight, complianceMarks, useComplianceHighlight } from "./highlight"
import { sections, STATUS_META, STATUS_ORDER } from "./status"

/** The framework picked last (any workflow): offered first when it applies. */
const FRAMEWORK_KEY = "nimbo.compliance.framework"

function lastFramework(): string | null {
  try {
    return localStorage.getItem(FRAMEWORK_KEY)
  } catch {
    return null
  }
}

/** Resource chips shown per row before "+N". */
const MAX_CHIPS = 6

/**
 * Compliance pre-audit of the design: the workflow graded against the benchmarks of its platforms (CIS Foundations per
 * cloud, CIS Docker, CIS Kubernetes) from its best-practice findings. Manual controls are the user's to attest. Never
 * a certification. Refreshes when the workflow is saved while the tab is shown, or when Settings change.
 */
export function ComplianceTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const workflowName = useWorkflowStore((s) => s.workflow!.name)
  // Frozen while the tab is hidden: saves made meanwhile don't re-grade until it is shown.
  const updatedAt = useShownVersion("compliance")
  // Turning a rule on or off in Settings changes what can be graded.
  const settingsAt = useSettings().dataUpdatedAt
  const queryClient = useQueryClient()
  const queryKey = ["compliance", workflowId, updatedAt, settingsAt]
  const report = useQuery({ queryKey, queryFn: () => api.compliance(workflowId), placeholderData: keepPreviousData })

  const [picked, setPicked] = useState<string | null>(lastFramework)
  const frameworks = report.data?.frameworks ?? []
  const result = report.data?.results.find((r) => r.framework.id === picked) ?? report.data?.results[0]
  const [selected, setSelected] = useState<string | null>(null)
  const control = result?.controls.find((c) => c.id === selected) ?? null

  // "Show on canvas" rings the selected control's resources (or every failing one) while this tab is shown.
  const [onCanvas, setOnCanvas] = useState(false)
  const shown = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "compliance")
  useEffect(() => {
    if (onCanvas && shown && result) useComplianceHighlight.setState({ marks: complianceMarks(result.controls, control) })
    else clearComplianceHighlight()
  }, [onCanvas, shown, result, control])
  useEffect(() => clearComplianceHighlight, [])

  const pick = (id: string) => {
    setPicked(id)
    setSelected(null)
    try {
      localStorage.setItem(FRAMEWORK_KEY, id)
    } catch {
      // Private mode: the choice lasts for this session.
    }
  }
  const refresh = () => void queryClient.invalidateQueries({ queryKey: ["compliance", workflowId] })

  if (report.isPending) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  }
  if (report.isError && !report.data) {
    return (
      <Empty className="h-full">
        <EmptyHeader>
          <EmptyTitle>Couldn't check compliance</EmptyTitle>
          <EmptyDescription>{errorMessage(report.error)}</EmptyDescription>
        </EmptyHeader>
        <Button size="sm" variant="outline" onClick={() => void report.refetch()}>
          <RefreshCw /> Try again
        </Button>
      </Empty>
    )
  }
  if (!result || frameworks.length === 0) {
    return (
      <Empty className="h-full" data-testid="compliance-empty">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <ShieldCheck />
          </EmptyMedia>
          <EmptyTitle>No benchmark applies yet</EmptyTitle>
          <EmptyDescription>
            Add resources of a cloud (AWS, Azure, Google Cloud or OCI) and the design is checked against its CIS
            Foundations benchmark. Docker and Kubernetes workflows get CIS Docker and CIS Kubernetes.
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="@container flex h-full min-h-0 flex-col" data-testid="compliance-tab">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1.5 border-b px-3 py-1.5">
        <Select value={result.framework.id} onValueChange={pick}>
          <SelectTrigger size="sm" className="h-7 max-w-72 text-xs" aria-label="Framework" data-testid="compliance-framework">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {frameworks.map((f) => (
              <SelectItem key={f.id} value={f.id} className="text-xs">
                {f.name} v{f.version}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <div className="min-w-0">
          <p className="text-xs font-medium tabular-nums" data-testid="compliance-summary">
            {result.met} / {result.graded} automatic controls met
          </p>
          <p className="text-[11px] text-muted-foreground">
            Pre-audit based on the design · not a certification ·{" "}
            <a href={result.framework.source} target="_blank" rel="noreferrer" className="underline-offset-2 hover:underline">
              benchmark
            </a>
          </p>
        </div>
        <Legend result={result} />
        <div className="ml-auto flex items-center gap-1">
          <Button
            size="xs"
            variant={onCanvas ? "secondary" : "outline"}
            aria-pressed={onCanvas}
            data-testid="compliance-show-on-canvas"
            title={control ? `Ring the resources of ${control.id} on the canvas` : "Ring every resource with a violation on the canvas"}
            onClick={() => setOnCanvas((v) => !v)}
          >
            {onCanvas ? <EyeOff /> : <Eye />} {onCanvas ? "Hide on canvas" : "Show on canvas"}
          </Button>
          <ExportButton workflowId={workflowId} workflowName={workflowName} frameworkId={result.framework.id} />
          <Button size="icon-xs" variant="ghost" aria-label="Check again" title="Check again" onClick={refresh}>
            {report.isFetching ? <Spinner className="size-3" /> : <RefreshCw />}
          </Button>
        </div>
      </header>
      <ControlTable
        result={result}
        selected={selected}
        onSelect={(id) => setSelected((cur) => (cur === id ? null : id))}
        onChanged={refresh}
      />
    </div>
  )
}

/** Counts per status; zero counts stay silent. */
function Legend({ result }: { result: ComplianceResult }) {
  return (
    <ul className="flex flex-wrap items-center gap-1" aria-label="Controls per status" data-testid="compliance-legend">
      {STATUS_ORDER.filter((s) => result.counts[s] > 0).map((s) => {
        const meta = STATUS_META[s]
        return (
          <li key={s}>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className={cn("inline-flex h-5 items-center gap-1 rounded-md px-1.5 text-[11px] tabular-nums", meta.chip)} data-status={s}>
                  <meta.icon className="size-3" />
                  {result.counts[s]} {meta.label.toLowerCase()}
                </span>
              </TooltipTrigger>
              <TooltipContent>{meta.hint}</TooltipContent>
            </Tooltip>
          </li>
        )
      })}
    </ul>
  )
}

function ExportButton({ workflowId, workflowName, frameworkId }: { workflowId: string; workflowName: string; frameworkId: string }) {
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="xs"
      variant="outline"
      disabled={busy}
      data-testid="compliance-export"
      onClick={() => {
        setBusy(true)
        exportReport(workflowId, workflowName, frameworkId)
          .then((saved) => saved && toast.success("Compliance report saved"))
          .catch((err) => toast.error("Couldn't export the report", { description: errorMessage(err) }))
          .finally(() => setBusy(false))
      }}
    >
      {busy ? <Spinner className="size-3" /> : <Download />} Export report
    </Button>
  )
}

function ControlTable({
  result,
  selected,
  onSelect,
  onChanged,
}: {
  result: ComplianceResult
  selected: string | null
  onSelect: (id: string) => void
  onChanged: () => void
}) {
  const groups = useMemo(() => sections(result.controls), [result])
  const [attesting, setAttesting] = useState<ComplianceControl | null>(null)
  return (
    // Block, not Radix's table wrapper: rows keep the pane's width, so long titles wrap instead of being cut off.
    <ScrollArea className="min-h-0 flex-1 [&_[data-slot=scroll-area-viewport]>div]:block!" data-testid="compliance-controls">
      {groups.map((g) => (
        <section key={g.section} aria-label={g.section}>
          <h3 className="sticky top-0 z-10 border-b bg-background/95 px-3 py-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase backdrop-blur">
            {g.section}
          </h3>
          <div className="divide-y">
            {g.controls.map((c) => (
              <ControlRow
                key={c.id}
                control={c}
                framework={result.framework.id}
                selected={c.id === selected}
                onSelect={() => onSelect(c.id)}
                onAttest={() => setAttesting(c)}
                onChanged={onChanged}
              />
            ))}
          </div>
        </section>
      ))}
      <AttestDialog control={attesting} framework={result.framework.id} onClose={() => setAttesting(null)} onSaved={onChanged} />
    </ScrollArea>
  )
}

function ControlRow({
  control: c,
  framework,
  selected,
  onSelect,
  onAttest,
  onChanged,
}: {
  control: ComplianceControl
  framework: string
  selected: boolean
  onSelect: () => void
  onAttest: () => void
  onChanged: () => void
}) {
  const meta = STATUS_META[c.status]
  const quiet = c.status === "not_applicable" || c.status === "not_evaluated"
  const resources = c.status === "met" || c.status === "violated" ? c.resources : []
  // Failing resources first: they are the ones to fix.
  const chips = [...resources].sort((a, b) => Number(!!b.failing) - Number(!!a.failing))
  const failing = chips.filter((r) => r.failing).length
  return (
    <div
      role="row"
      aria-selected={selected}
      tabIndex={0}
      className={cn(
        "grid cursor-default grid-cols-[1rem_minmax(0,1fr)] items-start gap-x-2 gap-y-1 px-3 py-1.5 text-xs outline-none hover:bg-muted/40 focus-visible:bg-muted @2xl:grid-cols-[1rem_minmax(0,2fr)_minmax(0,2fr)_minmax(11rem,1fr)_auto]",
        selected && "bg-muted/70 hover:bg-muted/70",
      )}
      data-testid={`compliance-control-${c.id}`}
      data-status={c.status}
      onClick={onSelect}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault()
          onSelect()
        }
      }}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <meta.icon className={cn("mt-0.5 size-3.5", meta.className)} aria-label={meta.label} />
        </TooltipTrigger>
        <TooltipContent className="max-w-72">
          {meta.label}: {c.reason || meta.hint}
        </TooltipContent>
      </Tooltip>
      <div className={cn("min-w-0", quiet && "text-muted-foreground")}>
        <span className="font-mono text-[11px] text-muted-foreground">{c.id}</span> <span className="font-medium">{c.title}</span>
        {quiet && c.reason && <p className="text-[11px]">{c.reason}</p>}
        {/* Full guidance on the selected row; manual rows otherwise show one line, so 40+ of them don't bury the rest. */}
        {c.guidance &&
          (selected ? (
            <p className="text-[11px] text-muted-foreground">{c.guidance}</p>
          ) : (
            c.status === "manual" && <p className="line-clamp-1 text-[11px] text-muted-foreground">{c.guidance}</p>
          ))}
        {c.status === "attested" && c.attestation && (
          <p className="text-[11px] text-muted-foreground">
            “{c.attestation.note}” · {new Date(c.attestation.at).toLocaleDateString()}
          </p>
        )}
        {selected &&
          c.findings.map((f, i) => (
            <p key={`${f.code}-${f.nodeId ?? ""}-${i}`} className="mt-0.5 text-[11px]">
              <span className={STATUS_META[c.status].className}>{f.address ? `${f.address}: ` : ""}</span>
              {f.message}
              {f.fix && <span className="text-muted-foreground"> — {f.fix}</span>}
            </p>
          ))}
      </div>
      <div className="col-start-2 flex min-w-0 flex-wrap gap-1 @2xl:col-start-auto">
        {c.status === "violated" && (
          <span className="self-center text-[11px] text-destructive tabular-nums" data-testid={`compliance-failing-${c.id}`}>
            {chips.length ? `${failing} of ${chips.length}` : "design-wide"}
          </span>
        )}
        {chips.slice(0, MAX_CHIPS).map((r) => (
          <ResourceChip key={r.nodeId} resource={r} />
        ))}
        {chips.length > MAX_CHIPS && <span className="self-center text-[11px] text-muted-foreground">+{chips.length - MAX_CHIPS}</span>}
      </div>
      <div className="col-start-2 flex min-w-0 flex-col font-mono text-[11px] text-muted-foreground @2xl:col-start-auto">
        {c.rules?.length
          ? c.rules.map((r) => (
              <span key={r} className="truncate" title={r}>
                {r}
              </span>
            ))
          : "manual"}
      </div>
      <div className="col-start-2 flex items-center gap-1 @2xl:col-start-auto @2xl:justify-end">
        {c.status === "manual" && (
          <Button
            size="xs"
            variant="outline"
            data-testid={`compliance-attest-${c.id}`}
            onClick={(e) => {
              e.stopPropagation()
              onAttest()
            }}
          >
            Mark as done…
          </Button>
        )}
        {c.status === "attested" && (
          <>
            <span className={cn("inline-flex h-5 items-center gap-1 rounded-md px-1.5 text-[11px]", meta.chip)}>
              <meta.icon className="size-3" /> Attested
            </span>
            <UndoButton control={c} framework={framework} onDone={onChanged} />
          </>
        )}
      </div>
    </div>
  )
}

/** A resource of a control; clicking selects it on the canvas (Kubernetes: expands its chart if needed). */
function ResourceChip({ resource: r }: { resource: ComplianceResource }) {
  const kube = useWorkflowStore((s) => s.workflow?.kind === "kubernetes")
  return (
    <button
      type="button"
      className={cn(
        "max-w-56 truncate rounded-md px-1.5 py-0.5 text-left font-mono text-[11px] hover:underline",
        r.failing ? "bg-destructive/12 text-destructive" : "bg-muted text-muted-foreground",
      )}
      title={`${r.address}${r.failing ? " — has findings" : ""}`}
      onClick={(e) => {
        e.stopPropagation()
        if (kube) void showObject(r.nodeId)
        else useWorkflowStore.getState().select(r.nodeId)
      }}
    >
      {r.address}
    </button>
  )
}

function UndoButton({ control, framework, onDone }: { control: ComplianceControl; framework: string; onDone: () => void }) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const [busy, setBusy] = useState(false)
  return (
    <Button
      size="icon-xs"
      variant="ghost"
      aria-label={`Undo: mark ${control.id} as not done`}
      title="Undo"
      disabled={busy}
      data-testid={`compliance-unattest-${control.id}`}
      onClick={(e) => {
        e.stopPropagation()
        setBusy(true)
        api
          .unattestControl(workflowId, framework, control.id)
          .then(onDone)
          .catch((err) => toast.error("Couldn't undo", { description: errorMessage(err) }))
          .finally(() => setBusy(false))
      }}
    >
      {busy ? <Spinner className="size-3" /> : <Undo2 />}
    </Button>
  )
}

/** Note dialog of "Mark as done…": the note is the evidence the report shows for a manual control. */
function AttestDialog({
  control,
  framework,
  onClose,
  onSaved,
}: {
  control: ComplianceControl | null
  framework: string
  onClose: () => void
  onSaved: () => void
}) {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  const [note, setNote] = useState("")
  const [busy, setBusy] = useState(false)
  // The next control starts with an empty note.
  const close = () => {
    setNote("")
    onClose()
  }
  const submit = () => {
    if (!control || !note.trim()) return
    setBusy(true)
    api
      .attestControl(workflowId, framework, control.id, note.trim())
      .then(() => {
        onSaved()
        close()
      })
      .catch((err) => toast.error("Couldn't save", { description: errorMessage(err) }))
      .finally(() => setBusy(false))
  }
  return (
    <Dialog open={!!control} onOpenChange={(open) => !open && close()}>
      <DialogContent className="sm:max-w-md" data-testid="compliance-attest-dialog">
        <DialogHeader>
          <DialogTitle>Mark {control?.id} as done</DialogTitle>
          <DialogDescription>{control?.title}</DialogDescription>
        </DialogHeader>
        {control?.guidance && <p className="text-xs text-muted-foreground">{control.guidance}</p>}
        <Textarea
          autoFocus
          value={note}
          onChange={(e) => setNote(e.target.value)}
          placeholder="How is it met? E.g. root MFA on a hardware key since March; checked by the platform team."
          className="min-h-24 text-xs"
          aria-label="Note"
          onKeyDown={(e) => {
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit()
          }}
        />
        <p className="text-[11px] text-muted-foreground">The note and today's date go into the exported report.</p>
        <DialogFooter>
          <Button variant="outline" onClick={close}>
            Cancel
          </Button>
          <Button disabled={busy || !note.trim()} onClick={submit} data-testid="compliance-attest-save">
            {busy && <Spinner className="size-3" />} Mark as done
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
