import { useEffect, useMemo, useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { ArrowLeftIcon, ClockIcon, GraduationCapIcon, MapIcon, SearchIcon, SwordsIcon, TriangleAlertIcon } from "lucide-react"
import { errorMessage } from "@/components/errors"
import { inline, Markdown } from "@/components/markdown"
import { ThemeToggle } from "@/components/theme-toggle"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { ApiError, api } from "@/lib/api/client"
import type { ChallengeSummary, LearnContentStatus, LearnLevel, LearnProvider, LearnProviderId, LearnStatus, Roadmap } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { ChallengePage } from "./challenge-page"
import { ProgressBadge, ProviderDot } from "./meta"
import { CodeBadge, DocLinks, LevelBadge, Sources } from "./parts"
import { learnKeys, LEVEL_LABEL, LEVELS, PROVIDER_META, PROVIDERS, RUNTIME_LABEL, useAllChallenges, useLearnStore } from "./store"

/**
 * Full-page Learn: per provider a roadmap (beginner → expert) and a challenge
 * catalog, and the challenge page. Drawn over home or the open workflow.
 */
export function LearnView() {
  const closeView = useLearnStore((s) => s.closeView)
  const provider = useLearnStore((s) => s.provider)
  const setProvider = useLearnStore((s) => s.setProvider)
  const challengeId = useLearnStore((s) => s.challengeId)
  const workflowName = useWorkflowStore((s) => s.workflow?.name)
  const overview = useQuery({ queryKey: learnKeys.overview, queryFn: api.learnOverview })

  // Opening or closing a workflow (Start challenge, the palette) navigates away from this page.
  useEffect(
    () =>
      useWorkflowStore.subscribe((s, prev) => {
        if (s.workflow?.id !== prev.workflow?.id) closeView()
      }),
    [closeView],
  )
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return
      const st = useLearnStore.getState()
      if (st.challengeId) st.openChallenge(null)
      else closeView()
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [closeView])

  const providers = overview.data?.providers ?? []

  return (
    <div className="fixed inset-0 z-40 flex flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button size="sm" variant="ghost" onClick={closeView}>
          <ArrowLeftIcon /> {workflowName ? `Back to ${workflowName}` : "Home"}
        </Button>
        <div className="ml-1 flex min-w-0 items-baseline gap-2">
          <h1 className="font-heading text-sm font-semibold">Learn</h1>
          <p className="hidden truncate text-xs text-muted-foreground md:block">
            Roadmaps and hands-on challenges for every provider, from official documentation — graded for real by Nimbo.
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <ThemeToggle />
        </div>
      </header>
      <nav className="flex shrink-0 gap-1 overflow-x-auto border-b px-3 py-2" aria-label="Providers">
        {PROVIDERS.map((id) => (
          <ProviderTab
            key={id}
            id={id}
            info={providers.find((p) => p.id === id)}
            active={provider === id}
            onSelect={() => setProvider(id)}
          />
        ))}
      </nav>
      <main className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex max-w-5xl flex-col gap-6 px-6 py-6">
          {overview.isError ? (
            <Alert variant="destructive">
              <TriangleAlertIcon />
              <AlertTitle>Learn is unavailable</AlertTitle>
              <AlertDescription>{errorMessage(overview.error)}</AlertDescription>
            </Alert>
          ) : (
            overview.data && <ContentErrors status={overview.data.status} />
          )}
          {challengeId ? <ChallengePage id={challengeId} /> : <ProviderPage provider={provider} info={providers.find((p) => p.id === provider)} />}
        </div>
      </main>
    </div>
  )
}

function ProviderTab({ id, info, active, onSelect }: { id: LearnProviderId; info?: LearnProvider; active: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex shrink-0 items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/50",
        active ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
      )}
    >
      <ProviderDot provider={id} />
      {PROVIDER_META[id].label}
      {info && info.challenges > 0 && (
        <span className="text-xs text-muted-foreground tabular-nums">
          {info.completed}/{info.challenges}
        </span>
      )}
    </button>
  )
}

/** Content migrations that failed to load (Learn keeps serving what applied). */
function ContentErrors({ status }: { status: LearnContentStatus }) {
  const [open, setOpen] = useState(false)
  if (status.errors.length === 0) return null
  const shown = open ? status.errors : status.errors.slice(0, 2)
  return (
    <Alert variant="destructive">
      <TriangleAlertIcon />
      <AlertTitle>
        Some learning content could not be loaded
        {status.pending > 0 && ` · ${status.pending} ${status.pending === 1 ? "update" : "updates"} not applied`}
      </AlertTitle>
      <AlertDescription>
        <ul className="mt-1 list-disc space-y-1 pl-4 font-mono text-xs break-words">
          {shown.map((e, i) => (
            <li key={i}>{e}</li>
          ))}
        </ul>
        {status.errors.length > 2 && (
          <Button variant="link" size="sm" className="h-auto px-0" onClick={() => setOpen(!open)}>
            {open ? "Show less" : `Show all ${status.errors.length}`}
          </Button>
        )}
      </AlertDescription>
    </Alert>
  )
}

function ProviderPage({ provider, info }: { provider: LearnProviderId; info?: LearnProvider }) {
  const tab = useLearnStore((s) => s.tab)
  const setTab = useLearnStore((s) => s.setTab)
  const meta = PROVIDER_META[provider]
  return (
    <>
      <div className="flex flex-wrap items-end gap-4">
        <div className="flex min-w-0 flex-col gap-1">
          <h2 className="flex items-center gap-2 font-heading text-xl font-semibold">
            <ProviderDot provider={provider} className="size-2.5" />
            {meta.label}
          </h2>
          <p className="text-sm text-muted-foreground">{meta.blurb}</p>
        </div>
        {info && info.challenges > 0 && <LevelProgress info={info} />}
      </div>
      <Tabs value={tab} onValueChange={(v) => setTab(v as typeof tab)}>
        <TabsList>
          <TabsTrigger value="roadmap">
            <MapIcon /> Roadmap
          </TabsTrigger>
          <TabsTrigger value="challenges">
            <SwordsIcon /> Challenges{info ? ` (${info.challenges})` : ""}
          </TabsTrigger>
        </TabsList>
      </Tabs>
      {/* Keyed by provider: switching provider starts with fresh filters. */}
      {tab === "roadmap" ? <RoadmapView provider={provider} /> : <ChallengeCatalog key={provider} provider={provider} />}
    </>
  )
}

function LevelProgress({ info }: { info: LearnProvider }) {
  return (
    <div className="ml-auto flex gap-3" aria-label="Completed challenges per level">
      {info.levels.map((l) => (
        <div key={l.level} className="flex w-24 flex-col gap-1">
          <span className="flex justify-between text-[11px] text-muted-foreground">
            {LEVEL_LABEL[l.level]}
            <span className="tabular-nums">
              {l.completed}/{l.challenges}
            </span>
          </span>
          <span className="h-1.5 overflow-hidden rounded-full bg-muted">
            <span
              className="block h-full rounded-full bg-emerald-500 transition-all"
              style={{ width: l.challenges ? `${(l.completed / l.challenges) * 100}%` : "0%" }}
            />
          </span>
        </div>
      ))}
    </div>
  )
}

function RoadmapView({ provider }: { provider: LearnProviderId }) {
  const roadmap = useQuery({
    queryKey: learnKeys.roadmap(provider),
    queryFn: () => api.learnRoadmap(provider),
    retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 2,
  })
  const challenges = useAllChallenges()
  const setTab = useLearnStore((s) => s.setTab)
  if (roadmap.isPending) return <Skeleton className="h-64 rounded-xl" />
  if (roadmap.isError) {
    const missing = roadmap.error instanceof ApiError && roadmap.error.status === 404
    return (
      <Empty className="border border-dashed">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <MapIcon />
          </EmptyMedia>
          <EmptyTitle>{missing ? "No roadmap yet" : "Couldn’t load the roadmap"}</EmptyTitle>
          <EmptyDescription>
            {missing ? `${PROVIDER_META[provider].label} has no roadmap in this version of Nimbo yet.` : errorMessage(roadmap.error)}
          </EmptyDescription>
        </EmptyHeader>
        {missing && (
          <Button variant="outline" onClick={() => setTab("challenges")}>
            Browse challenges
          </Button>
        )}
      </Empty>
    )
  }
  const byId = new Map((challenges.data ?? []).map((c) => [c.id, c]))
  return <RoadmapLevels roadmap={roadmap.data} byId={byId} />
}

function RoadmapLevels({ roadmap, byId }: { roadmap: Roadmap; byId: Map<string, ChallengeSummary> }) {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-1">
        <h3 className="font-heading text-base font-semibold">{roadmap.title}</h3>
        <Markdown text={roadmap.summary} className="text-muted-foreground" />
      </div>
      <ol className="flex flex-col gap-6">
        {roadmap.levels.map((lvl) => {
          const linked = [...new Set(lvl.steps.flatMap((s) => s.challenges ?? []))].map((id) => byId.get(id)).filter((c) => c !== undefined)
          const done = linked.filter((c) => c.progress.status === "completed").length
          return (
            <li key={lvl.level} className="rounded-xl border bg-card">
              <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
                <LevelBadge level={lvl.level} />
                <h4 className="font-medium">{lvl.title}</h4>
                {linked.length > 0 && (
                  <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                    {done}/{linked.length} challenges completed
                  </span>
                )}
              </div>
              <div className="flex flex-col gap-4 px-4 py-4">
                <div className="flex gap-2 text-sm">
                  <GraduationCapIcon className="mt-0.5 size-4 shrink-0 text-primary" />
                  <Markdown text={lvl.goals} />
                </div>
                <ol className="relative flex flex-col gap-4 border-l pl-5">
                  {lvl.steps.map((step, i) => (
                    <li key={i} className="relative flex flex-col gap-2">
                      <span className="absolute top-0.5 -left-[1.95rem] flex size-5 items-center justify-center rounded-full border bg-background text-[10px] font-medium tabular-nums">
                        {i + 1}
                      </span>
                      <span className="font-medium">{step.title}</span>
                      <Markdown text={step.description} className="text-muted-foreground" />
                      {(step.challenges ?? []).length > 0 && (
                        <div className="flex flex-wrap gap-2">
                          {(step.challenges ?? []).map((id) => (
                            <ChallengeChip key={id} id={id} summary={byId.get(id)} />
                          ))}
                        </div>
                      )}
                      <DocLinks docs={step.docs} />
                    </li>
                  ))}
                </ol>
              </div>
            </li>
          )
        })}
      </ol>
      <Sources sources={roadmap.sources} title="Roadmap sources" />
    </div>
  )
}

function ChallengeChip({ id, summary }: { id: string; summary?: ChallengeSummary }) {
  const openView = useLearnStore((s) => s.openView)
  const current = useLearnStore((s) => s.provider)
  if (!summary) return <span className="rounded-md border border-dashed px-2 py-1 text-xs text-muted-foreground">{id}</span>
  return (
    <button
      type="button"
      onClick={() => openView({ challengeId: id })}
      className="flex items-center gap-2 rounded-md border bg-background px-2 py-1 text-xs transition-colors outline-none hover:border-primary/50 focus-visible:ring-2 focus-visible:ring-ring/50"
    >
      <SwordsIcon className="size-3.5 text-primary" />
      {summary.provider !== current && <ProviderDot provider={summary.provider} />}
      <span className="font-medium">{summary.title}</span>
      <ProgressBadge progress={summary.progress} compact />
    </button>
  )
}

const ALL = "all"

function ChallengeCatalog({ provider }: { provider: LearnProviderId }) {
  const challenges = useAllChallenges()
  const [level, setLevel] = useState<LearnLevel | typeof ALL>(ALL)
  const [tag, setTag] = useState<string>(ALL)
  const [status, setStatus] = useState<LearnStatus | typeof ALL>(ALL)
  const [query, setQuery] = useState("")
  // Server-side search (title, summary, problem, tags) once the user types.
  const search = useQuery({
    queryKey: [...learnKeys.challenges, "search", provider, query.trim()],
    queryFn: () => api.learnChallenges({ provider, q: query.trim() }),
    enabled: query.trim() !== "",
  })
  const ofProvider = useMemo(() => (challenges.data ?? []).filter((c) => c.provider === provider), [challenges.data, provider])
  const tags = useMemo(() => [...new Set(ofProvider.flatMap((c) => c.tags))].sort(), [ofProvider])
  const matched = query.trim() ? new Set((search.data ?? []).map((c) => c.id)) : null
  const list = ofProvider.filter(
    (c) =>
      (level === ALL || c.level === level) &&
      (tag === ALL || c.tags.includes(tag)) &&
      (status === ALL || c.progress.status === status) &&
      (!matched || matched.has(c.id)),
  )

  if (challenges.isPending) return <Skeleton className="h-64 rounded-xl" />
  if (challenges.isError) return <p className="text-sm text-destructive">Couldn’t load challenges: {errorMessage(challenges.error)}</p>

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative w-64">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input value={query} onChange={(e) => setQuery(e.target.value)} placeholder="Search challenges" className="pl-8" aria-label="Search challenges" />
        </div>
        <Select value={level} onValueChange={(v) => setLevel(v as typeof level)}>
          <SelectTrigger className="w-40" aria-label="Level">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All levels</SelectItem>
            {LEVELS.map((l) => (
              <SelectItem key={l} value={l}>
                {LEVEL_LABEL[l]}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={tag} onValueChange={setTag}>
          <SelectTrigger className="w-44" aria-label="Tag">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All topics</SelectItem>
            {tags.map((t) => (
              <SelectItem key={t} value={t}>
                {t}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={status} onValueChange={(v) => setStatus(v as typeof status)}>
          <SelectTrigger className="w-40" aria-label="Progress">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Any progress</SelectItem>
            <SelectItem value="not_started">Not started</SelectItem>
            <SelectItem value="in_progress">In progress</SelectItem>
            <SelectItem value="completed">Completed</SelectItem>
          </SelectContent>
        </Select>
        <span className="ml-auto text-xs text-muted-foreground tabular-nums">
          {list.length} of {ofProvider.length}
        </span>
      </div>
      {ofProvider.length === 0 ? (
        <Empty className="border border-dashed">
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <SwordsIcon />
            </EmptyMedia>
            <EmptyTitle>No challenges yet</EmptyTitle>
            <EmptyDescription>{PROVIDER_META[provider].label} has no challenges in this version of Nimbo yet.</EmptyDescription>
          </EmptyHeader>
        </Empty>
      ) : list.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">{search.isFetching ? "Searching…" : "No challenge matches these filters."}</p>
      ) : (
        LEVELS.filter((l) => list.some((c) => c.level === l)).map((l) => (
          <section key={l} className="flex flex-col gap-2">
            <h3 className="flex items-center gap-2 text-sm font-medium text-muted-foreground">
              <LevelBadge level={l} />
            </h3>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              {list
                .filter((c) => c.level === l)
                .map((c) => (
                  <ChallengeCard key={c.id} challenge={c} />
                ))}
            </div>
          </section>
        ))
      )}
    </div>
  )
}

function ChallengeCard({ challenge: c }: { challenge: ChallengeSummary }) {
  const openView = useLearnStore((s) => s.openView)
  return (
    <button
      type="button"
      onClick={() => openView({ challengeId: c.id })}
      className="flex flex-col gap-2 rounded-xl border bg-card p-4 text-left transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring/50"
    >
      <span className="flex items-start gap-2">
        <span className="font-medium">{c.title}</span>
        <ProgressBadge progress={c.progress} compact className="mt-0.5 ml-auto" />
      </span>
      <span className="text-sm text-muted-foreground">{inline(c.summary)}</span>
      <span className="mt-auto flex flex-wrap items-center gap-x-3 gap-y-1 pt-1 text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1">
          <ClockIcon className="size-3" />~{c.minutes} min
        </span>
        <span title={RUNTIME_LABEL[c.runtime].hint}>{RUNTIME_LABEL[c.runtime].label}</span>
        <span>
          {c.checks} {c.checks === 1 ? "goal" : "goals"}
        </span>
        <CodeBadge solvableWithoutCode={c.solvableWithoutCode} place="card" />
        {c.tags.slice(0, 3).map((t) => (
          <span key={t} className="rounded bg-muted px-1.5 py-0.5">
            {t}
          </span>
        ))}
      </span>
    </button>
  )
}
