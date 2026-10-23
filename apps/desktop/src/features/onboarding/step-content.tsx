import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import {
  ActivityIcon,
  ArrowRightIcon,
  BotIcon,
  CloudUploadIcon,
  CompassIcon,
  DownloadIcon,
  FileCodeIcon,
  GaugeIcon,
  LaptopIcon,
  PlayIcon,
  RefreshCwIcon,
  RocketIcon,
  ShapesIcon,
  TimerIcon,
} from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { AgentCard } from "@/features/settings/agents-section"
import { AGENTS_KEY, useSettingsView } from "@/features/settings/store"
import { api } from "@/lib/api/client"
import type { AgentClient } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { CheckCard, Lead, SetupError, StatusIcon, StatusPill, SuccessBanner } from "./check-ui"
import { OsIcon } from "./os-icons"
import { PLATFORMS, archLabel, type Platform } from "./platforms"
import { findCheck, stepForCheck, type StepDef } from "./steps"
import { useOnboarding, useSetup, useSetupAction, useStepCheck, type ActionProgress, type ImageProgress } from "./store"

const FIRST_TEMPLATE = "aws-serverless-api"

// ---------------------------------------------------------------- 1. Welcome

export function WelcomeStep({ platform, osName, arch }: { platform: Platform | undefined; osName?: string; arch?: string }) {
  const points = [
    { icon: ShapesIcon, title: "Design by connecting boxes", text: "Drag in an API, a function or a database and connect them — Nimbo fills in the wiring." },
    { icon: LaptopIcon, title: "Run it privately on your laptop", text: "Deploy to a local copy of the cloud. Nothing leaves your computer, nothing costs money." },
    { icon: GaugeIcon, title: "Measure speed and stability", text: "Send realistic traffic and watch response times and errors live. Export real Terraform when you're happy." },
  ]
  return (
    <div className="flex flex-col gap-8">
      <Lead>Nimbo is a workbench for cloud infrastructure. In a few minutes you'll have everything you need to:</Lead>
      <ul className="grid gap-3">
        {points.map((p) => (
          <li key={p.title} className="flex items-start gap-4 rounded-xl border bg-card p-4">
            <span className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <p.icon className="size-5" aria-hidden />
            </span>
            <div>
              <p className="font-medium">{p.title}</p>
              <p className="text-sm text-muted-foreground">{p.text}</p>
            </div>
          </li>
        ))}
      </ul>
      {platform && (
        <p className="flex items-center gap-3 rounded-lg border px-4 py-3 text-sm">
          <OsIcon platform={platform} className="size-5 shrink-0" />
          <span>
            We detected <b className="font-medium">{osName || PLATFORMS[platform].label}</b>
            {arch && ` on ${archLabel(platform, arch)}`} — every step shows the instructions for it.
          </span>
        </p>
      )}
      <p className="flex items-center gap-2 rounded-lg bg-muted/60 px-4 py-3 text-sm">
        <TimerIcon className="size-4 shrink-0 text-muted-foreground" aria-hidden />
        You'll need about 5 minutes and a ~1.5 GB download. We'll check each requirement for you and only ask you to act when something is missing.
      </p>
    </div>
  )
}

// ---------------------------------------------------------------- 4. Download the local cloud

export function ImagesStep() {
  const { setup, check } = useStepCheck("images")
  const socket = findCheck(setup.data, "docker-socket")
  const daemon = findCheck(setup.data, "docker-daemon")
  const pull = useSetupAction("pull-images")
  const progress = useOnboarding((s) => s.actions["pull-images"])
  const ok = check?.status === "ok"
  const dockerDown = daemon && daemon.status !== "ok" && daemon.status !== "warning"

  return (
    <div className="flex flex-col gap-6">
      <Lead>
        The local cloud comes as a few downloadable packages called <b className="font-medium text-foreground">images</b>: an emulator that behaves like AWS,
        Terraform (the tool that builds your infrastructure) and a tiny Linux helper. They download once — about 1.5 GB — and every workflow reuses them.
      </Lead>
      {ok && !progress?.running && <SuccessBanner title="The local cloud is downloaded ✓" summary={check.summary} />}
      {ok && !progress && (
        <div>
          <Button variant="outline" size="sm" onClick={pull.run} disabled={pull.busy}>
            <RefreshCwIcon />
            Verify images
          </Button>
        </div>
      )}
      {!ok && dockerDown ? (
        <p className="rounded-lg bg-muted/60 px-4 py-3 text-sm">Start Docker first (previous step) — downloads go through it.</p>
      ) : (
        (!ok || progress) && (
        <div className="flex flex-col gap-4 rounded-xl border bg-card p-5">
          <div className="flex flex-wrap items-center gap-3">
            <Button size="lg" variant={ok ? "outline" : "default"} onClick={pull.run} disabled={pull.busy}>
              {pull.busy ? <Spinner /> : ok ? <RefreshCwIcon /> : <DownloadIcon />}
              {pull.busy ? "Downloading…" : progress?.error ? "Try again" : ok ? "Verify images" : "Download"}
            </Button>
            <StatusPill status={check?.status} checking={setup.isFetching} />
            {!ok && <span className="text-sm text-muted-foreground">About 1.5 GB, one time. Keep Nimbo open while it downloads.</span>}
          </div>
          {progress && (progress.running || progress.images.length > 0) && <PullProgress progress={progress} />}
          {progress?.error && !progress.running && <p className="text-sm text-destructive">{progress.error}</p>}
          {check && check.status !== "ok" && check.detail && <p className="text-sm text-muted-foreground">{check.detail}</p>}
        </div>
        )
      )}
      {setup.isError && <SetupError error={setup.error} />}
      {socket && socket.status !== "ok" && (
        <div className="flex flex-col gap-2">
          <p className="text-sm text-muted-foreground">
            The local cloud also needs to talk to Docker itself, to start the services you deploy (for example your Lambda functions):
          </p>
          <CheckCard check={socket} />
        </div>
      )}
    </div>
  )
}

/** Overall and per-image layer progress of a pull action (pull-images, pull-kube-image). */
function PullProgress({ progress }: { progress: ActionProgress }) {
  const overall = progress.total > 0 ? Math.round((progress.current / progress.total) * 100) : 0
  return (
    <div className="flex flex-col gap-3" aria-live="polite">
      {progress.total > 0 && (
        <div className="flex flex-col gap-1.5">
          <div className="flex justify-between text-xs text-muted-foreground">
            <span>{progress.running ? "Overall" : "Finished"}</span>
            <span>
              {progress.current} of {progress.total} images
            </span>
          </div>
          <Progress value={overall} aria-label="Overall download progress" className="h-2" />
        </div>
      )}
      <ul className="flex flex-col gap-2">
        {progress.images.map((img) => (
          <ImageRow key={img.image} image={img} />
        ))}
      </ul>
    </div>
  )
}

/** Download progress of the Kubernetes node image (`pull-kube-image`), shown in the "kubernetes" check's card. */
function KubeImageProgress() {
  const progress = useOnboarding((s) => s.actions["pull-kube-image"])
  if (!progress) return null
  return (
    <div className="flex flex-col gap-2" data-testid="setup-pull-kube-image">
      {(progress.running || progress.images.length > 0) && <PullProgress progress={progress} />}
      {progress.error && !progress.running && <p className="text-sm text-destructive">{progress.error}</p>}
    </div>
  )
}

function ImageRow({ image: img }: { image: ImageProgress }) {
  const pct = img.layersTotal ? Math.round(((img.layersDone ?? 0) / img.layersTotal) * 100) : undefined
  const state = img.error ? "Failed" : img.step === "skip" ? "Already downloaded" : img.done ? "Done" : pct !== undefined ? `${pct}%` : "Starting…"
  return (
    <li className="flex flex-col gap-1.5 rounded-lg border px-3 py-2.5">
      <div className="flex items-center gap-2 text-sm">
        <StatusIcon status={img.error ? "error" : img.done ? "ok" : undefined} className="size-4 shrink-0" />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{img.image}</span>
        <span className={cn("text-xs", img.error ? "text-destructive" : "text-muted-foreground")}>{state}</span>
      </div>
      {img.done || img.error ? (
        <Progress value={img.error ? 0 : 100} aria-label={`${img.image} download`} className={cn("h-1.5", img.done && "[&>*]:bg-emerald-500")} />
      ) : pct !== undefined ? (
        <Progress value={pct} aria-label={`${img.image} download`} className="h-1.5" />
      ) : (
        <div className="h-1.5 overflow-hidden rounded-full bg-muted" role="progressbar" aria-label={`${img.image} download`}>
          <div className="h-full w-1/3 rounded-full bg-primary/70 motion-safe:animate-pulse" />
        </div>
      )}
      {!img.done && !img.error && img.message && <p className="truncate text-xs text-muted-foreground">{img.message}</p>}
      {img.error && <p className="text-xs text-destructive">{img.error}</p>}
    </li>
  )
}

// ---------------------------------------------------------------- 5. Health check

export function HealthStep({ steps }: { steps: StepDef[] }) {
  const setup = useSetup()
  const setStep = useOnboarding((s) => s.setStep)
  const status = setup.data
  const blocking = status?.checks.filter((c) => c.required && c.status !== "ok" && c.status !== "warning") ?? []

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start gap-4">
        <Lead>Here's everything Nimbo checked. Amber items won't stop you, but fixing them makes things smoother.</Lead>
        <Button variant="outline" size="sm" className="ml-auto shrink-0" onClick={() => void setup.refetch()} disabled={setup.isFetching}>
          <RefreshCwIcon className={cn(setup.isFetching && "motion-safe:animate-spin")} />
          Check again
        </Button>
      </div>
      {status?.ready ? (
        <SuccessBanner title="You're ready ✓" summary="Everything Nimbo needs is installed and running." />
      ) : status ? (
        <div className="rounded-xl border border-sky-500/40 bg-sky-500/10 p-4 text-sm">
          <p className="font-medium">
            {blocking.length === 1 ? "One thing still needs" : `${blocking.length} things still need`} your attention
          </p>
          <ul className="mt-2 flex flex-col gap-1">
            {blocking.map((c) => {
              const target = stepForCheck(steps, c.id)
              return (
                <li key={c.id} className="flex items-center gap-2">
                  <StatusIcon status={c.status} className="size-4" />
                  <span>{c.title}</span>
                  {target && (
                    <Button variant="link" size="sm" className="h-auto p-0" onClick={() => setStep(target.id)}>
                      Go to “{target.label}”
                      <ArrowRightIcon />
                    </Button>
                  )}
                </li>
              )
            })}
          </ul>
        </div>
      ) : null}
      {setup.isError && <SetupError error={setup.error} />}
      <div className="flex flex-col gap-3">
        {status
          ? status.checks.map((c) => (
              <CheckCard key={c.id} check={c}>
                {c.id === "kubernetes" && <KubeImageProgress />}
              </CheckCard>
            ))
          : !setup.isError && <Skeleton className="h-28 rounded-xl" />}
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- 6. AI agents

export function AgentsStep() {
  const queryClient = useQueryClient()
  const agents = useQuery({ queryKey: AGENTS_KEY, queryFn: api.agents, retry: false })
  const command = useQuery({ queryKey: ["mcp", "command"], queryFn: api.mcpCommand, retry: false, staleTime: Infinity })
  const close = useOnboarding((s) => s.close)
  const detected = (agents.data ?? []).filter((a) => a.detected || a.configured)

  return (
    <div className="flex flex-col gap-6">
      <Lead>
        If you use an AI assistant such as Claude, Cursor or Codex, you can let it work in Nimbo for you — “add a queue to my API and load-test it”. Agents
        connect through <b className="font-medium text-foreground">MCP</b> (Model Context Protocol), a standard way for AI apps to use tools. This step is
        optional.
      </Lead>
      {agents.isPending ? (
        <div className="grid gap-3 lg:grid-cols-2">
          <Skeleton className="h-36 rounded-xl" />
          <Skeleton className="h-36 rounded-xl" />
        </div>
      ) : agents.isError ? (
        <p className="text-sm text-destructive">Couldn't look for AI agents: {errorMessage(agents.error)}</p>
      ) : detected.length === 0 ? (
        <div className="flex items-center gap-3 rounded-xl border bg-muted/30 p-4 text-sm">
          <BotIcon className="size-5 shrink-0 text-muted-foreground" aria-hidden />
          No AI agent apps found on this computer. That's fine — you can connect one any time from Settings › AI agents.
        </div>
      ) : (
        <div className="grid gap-3 lg:grid-cols-2">
          {detected.map((a) => (
            <AgentCard
              key={a.id}
              agent={a}
              installDisabled={!!command.data?.problem}
              onChange={(updated) =>
                queryClient.setQueryData<AgentClient[]>(AGENTS_KEY, (old) => old?.map((x) => (x.id === updated.id ? updated : x)))
              }
            />
          ))}
        </div>
      )}
      {command.data?.problem && <p className="text-sm text-muted-foreground">{command.data.problem}</p>}
      <div>
        <Button
          variant="outline"
          onClick={() => {
            close()
            useSettingsView.getState().openView("agents")
          }}
        >
          <BotIcon />
          All agents and manual setup in Settings
        </Button>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------- 7. Try it

const GUIDE = [
  { icon: CloudUploadIcon, title: "Deploy", text: "Build it in your private local cloud." },
  { icon: ActivityIcon, title: "Load test", text: "Open the test panel and pick the API." },
  { icon: PlayIcon, title: "Start test", text: "Send traffic and watch it hold up." },
]

export function TryStep({ ready }: { ready: boolean }) {
  const close = useOnboarding((s) => s.close)
  const startGuide = useOnboarding((s) => s.startGuide)
  const templates = useQuery({ queryKey: ["templates"], queryFn: api.templates, staleTime: Infinity, retry: false })
  const template = templates.data?.find((t) => t.id === FIRST_TEMPLATE)

  const create = useMutation({
    mutationFn: async () => {
      const wf = await api.createWorkflow({ name: template?.name ?? "My first API", templateId: FIRST_TEMPLATE })
      await useWorkflowStore.getState().open(wf.id)
      return wf
    },
    onSuccess: (wf) => {
      startGuide(wf.id)
      close()
    },
    onError: (err) => toast.error("Couldn't create the workflow", { description: errorMessage(err) }),
  })

  return (
    <div className="flex flex-col gap-6">
      <Lead>The best way to learn Nimbo is a quick loop: open a ready-made API, deploy it locally and put some traffic on it. We'll guide you through it.</Lead>
      <div className="flex flex-col gap-5 rounded-2xl border bg-card p-6">
        <div className="flex items-start gap-4">
          <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
            <RocketIcon className="size-5" aria-hidden />
          </span>
          <div>
            <p className="font-heading text-base font-semibold">{template?.name ?? "Serverless REST API"}</p>
            <p className="text-sm text-muted-foreground">
              {template?.description ?? "An HTTP API in front of a Lambda function that stores items in a DynamoDB table."}
            </p>
          </div>
        </div>
        <ol className="grid gap-2 sm:grid-cols-3">
          {GUIDE.map((g, i) => (
            <li key={g.title} className="flex flex-col gap-1 rounded-lg bg-muted/50 p-3">
              <span className="flex items-center gap-2 text-sm font-medium">
                <span className="flex size-5 items-center justify-center rounded-full bg-primary text-[11px] text-primary-foreground">{i + 1}</span>
                <g.icon className="size-4 text-muted-foreground" aria-hidden />
                {g.title}
              </span>
              <span className="text-xs text-muted-foreground">{g.text}</span>
            </li>
          ))}
        </ol>
        {!ready && (
          <p className="text-sm text-amber-700 dark:text-amber-300">
            Some setup steps aren't finished yet: you can explore the design now, but deploying needs Docker and the local cloud.
          </p>
        )}
        <div className="flex flex-wrap gap-2">
          <Button size="lg" onClick={() => create.mutate()} disabled={create.isPending}>
            {create.isPending ? <Spinner /> : <FileCodeIcon />}
            Create my first workflow
          </Button>
          <Button size="lg" variant="ghost" onClick={close}>
            <CompassIcon />
            Explore on my own
          </Button>
        </div>
      </div>
    </div>
  )
}
