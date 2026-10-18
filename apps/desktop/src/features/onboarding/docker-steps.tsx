import { PlayIcon } from "lucide-react"
import type { ReactNode } from "react"
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import type { SetupFix } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CheckCard, FixList, Lead, SetupError, StatusPill, SuccessBanner } from "./check-ui"
import { PLATFORMS, type Platform } from "./platforms"
import { findCheck } from "./steps"
import { useOnboarding, useSetupAction, useStepCheck } from "./store"

/** OS-dependent steps get the OS to explain and whether it's a preview of another OS (static instructions, no live checks). */
interface OsProps {
  platform: Platform
  preview: boolean
}

/**
 * The live part of a check step: green banner when ok, otherwise the check
 * with the daemon's fixes for this machine. In preview, the generic
 * instructions for the previewed OS instead.
 */
function CheckOrInstructions({
  checkId,
  preview,
  okTitle,
  previewFixes,
  waitingNote,
}: {
  checkId: string
  preview: boolean
  okTitle: string
  previewFixes: SetupFix[]
  waitingNote: ReactNode
}) {
  const { setup, check } = useStepCheck(checkId)
  if (preview)
    return (
      <div className="flex flex-col gap-3 rounded-xl border border-dashed p-4">
        <FixList fixes={previewFixes} />
      </div>
    )
  if (check?.status === "ok") return <SuccessBanner title={okTitle} summary={check.summary} />
  return (
    <>
      {check ? <CheckCard check={check} /> : setup.isError ? <SetupError error={setup.error} /> : <Skeleton className="h-28 rounded-xl" />}
      <div className="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
        <StatusPill status={check?.status} checking={setup.isFetching} />
        <span>{waitingNote}</span>
      </div>
    </>
  )
}

// ---------------------------------------------------------------- Windows: WSL 2

export function WslStep({ preview }: OsProps) {
  return (
    <div className="flex flex-col gap-6">
      <Lead>
        On Windows, Docker runs on <b className="font-medium text-foreground">WSL 2</b> (Windows Subsystem for Linux): a built-in Windows feature that runs
        Linux next to your apps. Turning it on takes one command and a restart.
      </Lead>
      <CheckOrInstructions
        checkId="wsl"
        preview={preview}
        okTitle="WSL 2 is on ✓"
        previewFixes={PLATFORMS.windows.wslFixes ?? []}
        waitingNote="After restarting, reopen Nimbo — this page notices automatically."
      />
    </div>
  )
}

// ---------------------------------------------------------------- Install Docker

export function DockerStep({ platform, preview }: OsProps) {
  const { check } = useStepCheck("docker-cli")
  const info = PLATFORMS[platform]
  const ok = !preview && check?.status === "ok"
  return (
    <div className="flex flex-col gap-6">
      <Lead>
        Nimbo runs your infrastructure inside <b className="font-medium text-foreground">containers</b>: small, sealed-off boxes that each run one program
        on your computer. That's how you get a private copy of the cloud with no account and no bill. <b className="font-medium text-foreground">Docker</b> is
        the free app that runs containers.
      </Lead>
      {!ok && (
        <div className="rounded-xl border bg-muted/30 p-4">
          <p className="font-medium">{info.install.title}</p>
          <p className="mt-1 text-sm text-muted-foreground">{info.install.text}</p>
        </div>
      )}
      <CheckOrInstructions
        checkId="docker-cli"
        preview={preview}
        okTitle="Docker is installed ✓"
        previewFixes={info.installFixes}
        waitingNote={
          platform === "linux"
            ? "After installing, this page notices automatically."
            : "After installing, open Docker once and come back — this page notices automatically."
        }
      />
    </div>
  )
}

// ---------------------------------------------------------------- Start Docker

export function StartDockerStep({ platform, preview }: OsProps) {
  const { setup, check } = useStepCheck("docker-daemon")
  const cli = findCheck(setup.data, "docker-cli")
  const start = useSetupAction("start-docker")
  const progress = useOnboarding((s) => s.actions["start-docker"])
  const info = PLATFORMS[platform]
  const ok = !preview && check?.status === "ok"
  const notInstalled = !preview && cli && cli.status !== "ok" && cli.status !== "warning"
  // Linux: the engine is a system service only the user can start (sudo); show the command instead of a button.
  const liveFixes = check?.fixes?.filter((f) => f.action !== "start-docker")
  const fixes = preview || !liveFixes?.length ? info.startFixes : liveFixes

  return (
    <div className="flex flex-col gap-6">
      <Lead>
        Docker has to be running in the background while you deploy or test.{" "}
        {platform === "linux"
          ? "Once its service is enabled, it starts by itself every time your computer boots."
          : "Starting it takes one click; it then stays on until you quit it or restart your computer."}
      </Lead>
      {ok ? (
        <SuccessBanner title="Docker is running ✓" summary={check.summary} />
      ) : notInstalled ? (
        <p className="rounded-lg bg-muted/60 px-4 py-3 text-sm">Install Docker first (previous step) — then you can start it from here.</p>
      ) : (
        <>
          <div className={cn("flex flex-col gap-4 rounded-xl border bg-card p-5", preview && "border-dashed")}>
            <p className="text-sm">{info.startText}</p>
            {info.canStart && (
              <div className="flex flex-wrap items-center gap-3">
                <Button size="lg" onClick={start.run} disabled={preview || start.busy}>
                  {start.busy ? <Spinner /> : <PlayIcon />}
                  {start.busy ? "Starting Docker…" : "Start Docker"}
                </Button>
                {!preview && <StatusPill status={check?.status} checking={setup.isFetching} />}
              </div>
            )}
            <FixList fixes={fixes} />
            {!info.canStart && !preview && (
              <div className="flex items-center gap-3 text-sm text-muted-foreground">
                <StatusPill status={check?.status} checking={setup.isFetching} />
                This page turns green as soon as Docker answers.
              </div>
            )}
            <p className="text-sm text-muted-foreground">
              What to expect: {info.runningSign}. The first start can take up to a minute.
            </p>
            {!preview && progress?.message && (
              <p role="status" className={cn("text-sm", progress.error ? "text-destructive" : "text-muted-foreground")}>
                {progress.error ?? progress.message}
              </p>
            )}
          </div>
          {!preview && check?.detail && <p className="text-sm leading-relaxed">{check.detail}</p>}
          {!preview && setup.isError && <SetupError error={setup.error} />}
          <Accordion type="single" collapsible className="rounded-xl border px-4">
            {info.tips.map((t) => (
              <AccordionItem key={t.id} value={t.id}>
                <AccordionTrigger>{t.question}</AccordionTrigger>
                <AccordionContent className="text-muted-foreground">{t.answer}</AccordionContent>
              </AccordionItem>
            ))}
          </Accordion>
        </>
      )}
    </div>
  )
}

// ---------------------------------------------------------------- Linux: docker group

export function PermissionsStep({ preview }: OsProps) {
  return (
    <div className="flex flex-col gap-6">
      <Lead>
        On Linux, Docker only takes orders from administrators and members of the <code className="font-mono text-[0.92em]">docker</code> group. Add yourself
        once so Nimbo can use Docker without asking for your password. Group changes apply to new sessions: log out and back in (or restart) afterwards.
      </Lead>
      <CheckOrInstructions
        checkId="docker-permissions"
        preview={preview}
        okTitle="Your user can run Docker ✓"
        previewFixes={PLATFORMS.linux.permissionFixes ?? []}
        waitingNote="After logging back in, reopen Nimbo — this page notices automatically."
      />
    </div>
  )
}
