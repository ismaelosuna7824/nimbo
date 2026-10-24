import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ArrowLeftIcon,
  CheckCircle2Icon,
  CircleIcon,
  ClockIcon,
  EyeIcon,
  FileCodeIcon,
  LightbulbIcon,
  PlayIcon,
  RotateCcwIcon,
  SwordsIcon,
} from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { inline, Markdown } from "@/components/markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api/client"
import type { Challenge, ChallengeCheckType, ChallengeDetail, LearnProviderId } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { CodeBadge, LevelBadge, Sources } from "./parts"
import { ProgressBadge, ProviderDot } from "./meta"
import { learnKeys, PROVIDER_META, RUNTIME_LABEL, useLearnStore } from "./store"

const CHECK_LABEL: Record<ChallengeCheckType, string> = {
  validate: "Validation",
  rules_clean: "Best-practice rules",
  has: "Structure",
  deploy: "Deploy",
  http: "HTTP",
  load: "Load test",
  scenario: "Incident scenario",
  readiness: "Readiness score",
}

/** A challenge: the problem, requirements, goals, progressive hints, the solution on request and Start challenge. */
export function ChallengePage({ id }: { id: string }) {
  const openChallenge = useLearnStore((s) => s.openChallenge)
  const provider = useLearnStore((s) => s.provider)
  const detail = useQuery({ queryKey: learnKeys.challenge(id), queryFn: () => api.learnChallenge(id) })

  const back = (
    <Button variant="ghost" size="sm" className="-ml-2 self-start text-muted-foreground" onClick={() => openChallenge(null)}>
      <ArrowLeftIcon /> {PROVIDER_META[provider].label}
    </Button>
  )
  if (detail.isPending) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <Skeleton className="h-10 w-2/3" />
        <Skeleton className="h-64 rounded-xl" />
      </div>
    )
  }
  if (detail.isError) {
    return (
      <div className="flex flex-col gap-4">
        {back}
        <p className="text-sm text-destructive">Couldn’t load this challenge: {errorMessage(detail.error)}</p>
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-6">
      {back}
      <ChallengeHeader detail={detail.data} />
      <div className="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
        <div className="flex min-w-0 flex-col gap-6">
          <Section title="The problem">
            <Markdown text={detail.data.challenge.problem} />
          </Section>
          <Section title="Requirements">
            <ul className="ml-5 list-disc space-y-1 text-sm">
              {detail.data.challenge.requirements.map((r, i) => (
                <li key={i}>{inline(r)}</li>
              ))}
            </ul>
          </Section>
          {detail.data.challenge.adaptations && Object.keys(detail.data.challenge.adaptations).length > 0 && (
            <Adaptations adaptations={detail.data.challenge.adaptations} />
          )}
          <Hints key={id} hints={detail.data.challenge.hints} />
          <Solution key={`solution-${id}`} challenge={detail.data.challenge} />
        </div>
        <aside className="flex flex-col gap-6">
          <Goals detail={detail.data} />
          <Prerequisites detail={detail.data} />
          {detail.data.steps.length > 0 && (
            <Section title="On the roadmap">
              <ul className="flex flex-col gap-1 text-sm text-muted-foreground">
                {detail.data.steps.map((s) => (
                  <li key={s} className="first-letter:uppercase">
                    {s}
                  </li>
                ))}
              </ul>
            </Section>
          )}
          <Sources sources={detail.data.challenge.sources} />
        </aside>
      </div>
    </div>
  )
}

function Section({ title, children, action }: { title: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <h3 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</h3>
        {action && <div className="ml-auto">{action}</div>}
      </div>
      {children}
    </section>
  )
}

function ChallengeHeader({ detail }: { detail: ChallengeDetail }) {
  const c = detail.challenge
  const p = detail.progress
  const queryClient = useQueryClient()
  const start = useMutation({
    mutationFn: () => api.startChallenge(c.id),
    onSuccess: async (wf) => {
      await queryClient.invalidateQueries({ queryKey: learnKeys.all })
      await useWorkflowStore.getState().open(wf.id)
      useWorkflowStore.getState().showBottom("challenge")
    },
    onError: (e) => toast.error("Couldn’t start the challenge", { description: errorMessage(e) }),
  })
  const resume = useMutation({
    mutationFn: async (workflowId: string) => {
      await useWorkflowStore.getState().open(workflowId)
      useWorkflowStore.getState().showBottom("challenge")
    },
    onError: () => toast.error("That workflow no longer exists", { description: "Start the challenge again to get a fresh copy." }),
  })
  const reset = useMutation({
    mutationFn: () => api.setLearnProgress(c.id, "not_started"),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: learnKeys.all })
      toast.success("Progress reset")
    },
    onError: (e) => toast.error("Couldn’t reset progress", { description: errorMessage(e) }),
  })
  const busy = start.isPending || resume.isPending

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <ProviderDot provider={c.provider} />
        <span className="text-xs text-muted-foreground">{PROVIDER_META[c.provider].label}</span>
        <LevelBadge level={c.level} />
        <Badge variant="outline" title={RUNTIME_LABEL[c.runtime].hint}>
          {RUNTIME_LABEL[c.runtime].label}
        </Badge>
        <CodeBadge solvableWithoutCode={c.solvableWithoutCode} place="header" />
        <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
          <ClockIcon className="size-3" />~{c.minutes} min
        </span>
        <ProgressBadge progress={p} className="ml-auto" />
      </div>
      <h2 className="font-heading text-2xl font-semibold">{c.title}</h2>
      <p className="text-muted-foreground">{inline(c.summary)}</p>
      <div className="flex flex-wrap items-center gap-2">
        {p.workflowId ? (
          <>
            <Button disabled={busy} onClick={() => resume.mutate(p.workflowId!)}>
              {resume.isPending ? <Spinner /> : <PlayIcon />} Continue challenge
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => start.mutate()}>
              {start.isPending ? <Spinner /> : <SwordsIcon />} Start over in a new workflow
            </Button>
          </>
        ) : (
          <Button disabled={busy} onClick={() => start.mutate()}>
            {start.isPending ? <Spinner /> : <SwordsIcon />} Start challenge
          </Button>
        )}
        {p.status !== "not_started" && (
          <Button variant="ghost" size="sm" className="text-muted-foreground" disabled={reset.isPending} onClick={() => reset.mutate()}>
            <RotateCcwIcon /> Reset progress
          </Button>
        )}
        {c.tags.map((t) => (
          <span key={t} className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
            {t}
          </span>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">
        Starting creates a workflow from the challenge’s starter; check your solution from its Challenge tab — Nimbo grades every goal for real.
      </p>
    </div>
  )
}

function Goals({ detail }: { detail: ChallengeDetail }) {
  const done = new Set(detail.progress.completedGoals)
  return (
    <Section title={`Goals · ${done.size}/${detail.challenge.checks.length}`}>
      <ul className="flex flex-col gap-2">
        {detail.challenge.checks.map((ch) => {
          const passed = done.has(ch.id)
          return (
            <li key={ch.id} className="flex items-start gap-2 text-sm">
              {passed ? (
                <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-emerald-500" aria-label="Passed" />
              ) : (
                <CircleIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-label="Not passed yet" />
              )}
              <span className="flex min-w-0 flex-col">
                <span>{inline(ch.label)}</span>
                <span className="text-[11px] text-muted-foreground">
                  {CHECK_LABEL[ch.type] ?? ch.type}
                  {ch.environment ? ` · ${ch.environment} environment` : ""}
                </span>
              </span>
            </li>
          )
        })}
      </ul>
    </Section>
  )
}

function Prerequisites({ detail }: { detail: ChallengeDetail }) {
  const openView = useLearnStore((s) => s.openView)
  if (detail.prerequisites.length === 0) return null
  return (
    <Section title="Before this one">
      <ul className="flex flex-col gap-1">
        {detail.prerequisites.map((p) => (
          <li key={p.id} className="flex items-center gap-2 text-sm">
            {p.completed ? <CheckCircle2Icon className="size-4 text-emerald-500" /> : <CircleIcon className="size-4 text-muted-foreground" />}
            {p.missing ? (
              <span className="text-muted-foreground">{p.id}</span>
            ) : (
              <button type="button" className="text-left text-primary underline-offset-4 hover:underline" onClick={() => openView({ challengeId: p.id })}>
                {p.title}
              </button>
            )}
          </li>
        ))}
      </ul>
    </Section>
  )
}

function Adaptations({ adaptations }: { adaptations: Partial<Record<LearnProviderId, string>> }) {
  const providers = (Object.keys(PROVIDER_META) as LearnProviderId[]).filter((p) => adaptations[p])
  return (
    <Section title="On each provider">
      <Tabs defaultValue={providers[0]}>
        <TabsList className="flex-wrap">
          {providers.map((p) => (
            <TabsTrigger key={p} value={p}>
              <ProviderDot provider={p} /> {PROVIDER_META[p].label}
            </TabsTrigger>
          ))}
        </TabsList>
        {providers.map((p) => (
          <TabsContent key={p} value={p} className="pt-2">
            <Markdown text={adaptations[p] ?? ""} />
          </TabsContent>
        ))}
      </Tabs>
    </Section>
  )
}

function Hints({ hints }: { hints: string[] }) {
  const [shown, setShown] = useState(0)
  return (
    <Section
      title={`Hints · ${shown}/${hints.length}`}
      action={
        shown < hints.length && (
          <Button variant="outline" size="sm" onClick={() => setShown(shown + 1)}>
            <LightbulbIcon /> {shown === 0 ? "Show a hint" : "Next hint"}
          </Button>
        )
      }
    >
      {shown === 0 ? (
        <p className="text-sm text-muted-foreground">Stuck? Hints are revealed one at a time, each closer to the answer.</p>
      ) : (
        <ol className="flex flex-col gap-2">
          {hints.slice(0, shown).map((h, i) => (
            <li key={i} className="flex gap-2 rounded-lg border bg-amber-500/5 p-3 text-sm">
              <LightbulbIcon className="mt-0.5 size-4 shrink-0 text-amber-500" />
              <Markdown text={h} />
            </li>
          ))}
        </ol>
      )}
    </Section>
  )
}

function Solution({ challenge: c }: { challenge: Challenge }) {
  const [revealed, setRevealed] = useState(false)
  const files = Object.entries(c.solutionFiles ?? {})
  return (
    <Section
      title="Solution"
      action={
        !revealed && (
          <Button variant="outline" size="sm" onClick={() => setRevealed(true)}>
            <EyeIcon /> Reveal the solution
          </Button>
        )
      }
    >
      {!revealed ? (
        <p className="text-sm text-muted-foreground">Try the hints first — the solution explains the steps and why they follow the provider’s best practice.</p>
      ) : (
        <div className="flex flex-col gap-3 rounded-lg border p-4">
          <Markdown text={c.solution} />
          {files.length > 0 && (
            <div className="flex flex-col gap-2">
              <span className="text-xs font-medium text-muted-foreground">Reference files</span>
              {files.map(([path, content]) => (
                <details key={path} className="rounded-md border">
                  <summary className="flex cursor-pointer items-center gap-2 px-3 py-2 font-mono text-xs">
                    <FileCodeIcon className="size-3.5 text-muted-foreground" />
                    {path}
                  </summary>
                  <pre className="max-h-96 overflow-auto border-t bg-muted/40 p-3 font-mono text-xs leading-relaxed">{content}</pre>
                </details>
              ))}
            </div>
          )}
        </div>
      )}
    </Section>
  )
}
