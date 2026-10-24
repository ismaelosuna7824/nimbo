import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { CheckCircle2, CircleDashed, CircleX, Lightbulb, Loader2, Play, Square, TriangleAlert, Trophy } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import { useArcadeStore } from "@/components/arcade/arcade-store"
import { errorMessage } from "@/components/errors"
import { inline, Markdown } from "@/components/markdown"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { revealLoadTest } from "@/features/panels/loadtest/reveal"
import { api } from "@/lib/api/client"
import type { ChallengeGoalResult, ChallengeResult, ChallengeRunStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"

const TYPE_LABEL: Record<string, string> = {
  validate: "Validate",
  rules_clean: "Best practices",
  has: "Structure",
  deploy: "Deploy",
  http: "HTTP",
  load: "Load test",
  scenario: "Incident",
  readiness: "Readiness",
}

/**
 * The Challenge tab of a workflow started from a Learn challenge: the problem, its requirements, hints revealed
 * one at a time, the graded checks with their live status, "Check my solution" (deploys and runs every check for
 * real) and the reference solution.
 */
export function ChallengeTab() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const qc = useQueryClient()
  const [hints, setHints] = useState(0)
  const [showSolution, setShowSolution] = useState(false)
  const view = useQuery({
    queryKey: ["challenge", workflowId],
    queryFn: () => api.workflowChallenge(workflowId!),
    enabled: !!workflowId,
    // A running check is polled; it takes seconds (static checks) to minutes (load tests, incidents).
    refetchInterval: (q) => (q.state.data?.last?.status === "running" ? 2000 : false),
  })
  const last = view.data?.last
  const running = last?.status === "running"

  // Celebrate a check that just passed (not one loaded already passed).
  const prev = useRef<ChallengeResult | undefined>(undefined)
  useEffect(() => {
    if (prev.current?.status === "running" && last?.status === "passed") {
      useArcadeStore.getState().celebrate("Challenge complete!")
      toast.success("Challenge complete", {
        description: `Score ${last.score}/100`,
      })
    }
    prev.current = last
  }, [last])

  const check = useMutation({
    mutationFn: () => api.checkChallenge(workflowId!),
    onSuccess: (res) => qc.setQueryData(["challenge", workflowId], (v: typeof view.data) => (v ? { ...v, last: res } : v)),
    onError: (err) =>
      toast.error("Couldn't start the check", {
        description: errorMessage(err),
      }),
  })
  const cancel = useMutation({
    mutationFn: () => api.cancelChallengeCheck(workflowId!),
    onSettled: () => qc.invalidateQueries({ queryKey: ["challenge", workflowId] }),
  })
  const solutionCopy = useMutation({
    mutationFn: () => api.challengeSolution(workflowId!),
    onSuccess: (wf) => useWorkflowStore.getState().open(wf.id),
    onError: (err) =>
      toast.error("Couldn't open the solution", {
        description: errorMessage(err),
      }),
  })

  if (view.isPending)
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner />
      </div>
    )
  if (view.isError || !view.data) return <p className="p-4 text-sm text-destructive">Couldn't load the challenge: {errorMessage(view.error)}</p>

  const c = view.data.challenge
  const goals = c.checks.map(
    (k) =>
      last?.goals.find((g) => g.id === k.id) ?? {
        ...k,
        status: "pending" as const,
        passed: false,
        detail: "",
        checks: [],
      },
  )
  const passedCount = goals.filter((g) => g.passed).length

  return (
    // Narrow bottom panels stack the goals above the brief and scroll as one page; wide ones (container ≥ 48rem) put
    // them side by side, each scrolling on its own. Long code in the brief wraps instead of being cut off.
    <div className="@container h-full min-h-0 overflow-y-auto @3xl:overflow-hidden" data-testid="challenge-tab">
      <div className="flex min-h-full flex-col @3xl:h-full @3xl:flex-row">
        <div className="min-w-0 @3xl:flex-1 @3xl:overflow-y-auto @3xl:border-r">
          <div className="flex flex-col gap-3 p-4 text-sm [overflow-wrap:anywhere]">
            <div className="flex flex-wrap items-center gap-2">
              <h2 className="text-base font-semibold">{c.title}</h2>
              <Badge variant="secondary" className="capitalize">
                {c.level}
              </Badge>
              <Badge variant="outline">{c.minutes} min</Badge>
            </div>
            <Markdown text={c.problem} className="text-muted-foreground" />
            <section>
              <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">Requirements</h3>
              <ul className="list-disc space-y-1 pl-5">
                {c.requirements.map((r, i) => (
                  <li key={i}>{inline(r)}</li>
                ))}
              </ul>
            </section>
            <section className="flex flex-col gap-2">
              <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Hints</h3>
              {c.hints.slice(0, hints).map((h, i) => (
                <div key={i} className="flex gap-2 rounded-md border bg-muted/40 p-2">
                  <Lightbulb className="mt-0.5 size-4 shrink-0 text-amber-500" />
                  <span>{inline(h)}</span>
                </div>
              ))}
              {hints < c.hints.length && (
                <Button variant="outline" size="sm" className="self-start" onClick={() => setHints(hints + 1)}>
                  <Lightbulb />
                  {hints === 0 ? "Show a hint" : `Next hint (${hints + 1}/${c.hints.length})`}
                </Button>
              )}
            </section>
            <section className="flex flex-col gap-2">
              {showSolution ? (
                <>
                  <h3 className="text-xs font-medium uppercase tracking-wide text-muted-foreground">Solution</h3>
                  <Markdown text={c.solution} />
                  {c.solutionFiles && Object.keys(c.solutionFiles).length > 0 && (
                    <Button variant="outline" size="sm" className="self-start" disabled={solutionCopy.isPending} onClick={() => solutionCopy.mutate()}>
                      Open the solution in a copy
                    </Button>
                  )}
                </>
              ) : (
                <Button variant="ghost" size="sm" className="self-start text-muted-foreground" onClick={() => setShowSolution(true)}>
                  Show solution
                </Button>
              )}
            </section>
            {c.sources.length > 0 && (
              <section>
                <h3 className="mb-1 text-xs font-medium uppercase tracking-wide text-muted-foreground">Official docs</h3>
                <Markdown text={c.sources.map((s) => `- [${s.title}](${s.url})`).join("\n")} />
              </section>
            )}
          </div>
        </div>
        <div className="order-first flex w-full flex-col border-b @3xl:order-none @3xl:w-[22rem] @3xl:shrink-0 @3xl:border-b-0">
          <div className="flex flex-col gap-2 border-b p-3">
            <div className="flex items-center gap-2">
              {running ? (
                <Button size="sm" variant="outline" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
                  <Square />
                  Stop
                </Button>
              ) : (
                <Button size="sm" onClick={() => check.mutate()} disabled={check.isPending} data-testid="challenge-check">
                  <Play />
                  Check my solution
                </Button>
              )}
              <span className="ml-auto text-xs tabular-nums text-muted-foreground">
                {passedCount}/{goals.length} passed
              </span>
            </div>
            {running && (
              <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
                <Loader2 className="size-3 animate-spin" />
                {last?.phase || "Checking"}…
              </p>
            )}
            {last && !running && <ResultBanner result={last} />}
            {last && <Progress value={last.score} className="h-1.5" aria-label="Score" />}
          </div>
          <div className="@3xl:min-h-0 @3xl:flex-1 @3xl:overflow-y-auto">
            <ul className="flex flex-col gap-1 p-2" data-testid="challenge-goals">
              {goals.map((g) => (
                <GoalRow key={g.id} goal={g} />
              ))}
            </ul>
          </div>
        </div>
      </div>
    </div>
  )
}

function ResultBanner({ result }: { result: ChallengeResult }) {
  if (result.status === "passed")
    return (
      <p className="flex items-center gap-1.5 text-xs font-medium text-emerald-700 dark:text-emerald-300">
        <Trophy className="size-4" />
        Solved! Score {result.score}/100.
      </p>
    )
  if (result.status === "canceled") return <p className="text-xs text-muted-foreground">{result.error || "Canceled."}</p>
  return (
    <p className="text-xs text-muted-foreground">
      Score {result.score}/100. {result.error ?? "Fix the failing checks and check again."}
    </p>
  )
}

const STATUS_ICON: Record<ChallengeRunStatus, React.ReactNode> = {
  pending: <CircleDashed className="size-4 text-muted-foreground" />,
  running: <Loader2 className="size-4 animate-spin text-primary" />,
  passed: <CheckCircle2 className="size-4 text-emerald-600 dark:text-emerald-400" />,
  failed: <CircleX className="size-4 text-destructive" />,
  error: <TriangleAlert className="size-4 text-amber-600 dark:text-amber-400" />,
  canceled: <CircleDashed className="size-4 text-muted-foreground" />,
}

function GoalRow({ goal }: { goal: ChallengeGoalResult }) {
  const isRun = (goal.type === "load" || goal.type === "scenario") && !!goal.runId
  return (
    <li className={cn("rounded-md border p-2 text-xs", goal.status === "running" && "border-primary/50")} data-status={goal.status}>
      <div className="flex items-start gap-2">
        <span className="mt-px">{STATUS_ICON[goal.status]}</span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="font-medium">{goal.label}</span>
            <Badge variant="outline" className="h-4 px-1 text-[10px]">
              {TYPE_LABEL[goal.type] ?? goal.type}
            </Badge>
          </div>
          {goal.checks.length > 0 && (
            <ul className="mt-1 space-y-0.5">
              {goal.checks.map((m, i) => (
                <li key={i} className={cn("tabular-nums", m.passed ? "text-muted-foreground" : "text-destructive")}>
                  {m.label}: {m.actual} <span className="text-muted-foreground">(needs {m.expected})</span>
                </li>
              ))}
            </ul>
          )}
          {goal.status === "error" && goal.detail && <p className="mt-1 text-amber-700 dark:text-amber-300">{goal.detail}</p>}
          {isRun && (
            <Button variant="link" size="sm" className="h-auto p-0 text-xs" onClick={() => revealLoadTest(goal.runId)}>
              Open the run
            </Button>
          )}
        </div>
      </div>
    </li>
  )
}
