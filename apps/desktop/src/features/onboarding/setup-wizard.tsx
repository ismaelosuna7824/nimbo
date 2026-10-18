import { useQueryClient } from "@tanstack/react-query"
import { ArrowLeftIcon, ArrowRightIcon, CheckIcon, ChevronDownIcon, EyeIcon, XIcon } from "lucide-react"
import { Dialog as DialogPrimitive } from "radix-ui"
import { useEffect, useRef } from "react"
import { AppLogo } from "@/components/app-logo"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { subscribeEvents } from "@/lib/api/client"
import type { SetupStatus } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { StatusIcon } from "./check-ui"
import { DockerStep, PermissionsStep, StartDockerStep, WslStep } from "./docker-steps"
import { OsIcon } from "./os-icons"
import { PLATFORM_IDS, PLATFORMS, archLabel, detectedPlatform, type Platform } from "./platforms"
import { AgentsStep, HealthStep, ImagesStep, TryStep, WelcomeStep } from "./step-content"
import { stepDone, stepStatus, stepsFor } from "./steps"
import { SETUP_KEY, useOnboarding, useSetup } from "./store"

/** Follows `setup.progress` events while the wizard is open; re-checks setup as an action progresses. */
function useSetupProgressEvents() {
  const queryClient = useQueryClient()
  useEffect(
    () =>
      subscribeEvents(undefined, (e) => {
        if (e.type !== "setup.progress") return
        useOnboarding.getState().applyProgress(e.data)
        if (e.data.done || e.data.step === "image-done") void queryClient.invalidateQueries({ queryKey: SETUP_KEY })
      }),
    [queryClient],
  )
}

/** The full-screen setup wizard; loaded on first open by OnboardingHost. */
export function Wizard() {
  const step = useOnboarding((s) => s.step)
  const setStep = useOnboarding((s) => s.setStep)
  const close = useOnboarding((s) => s.close)
  const previewPlatform = useOnboarding((s) => s.previewPlatform)
  const setup = useSetup()
  const status = setup.data
  const headingRef = useRef<HTMLHeadingElement>(null)
  useSetupProgressEvents()

  const detected = detectedPlatform(status)
  const platform: Platform = previewPlatform ?? detected ?? "darwin"
  const preview = previewPlatform !== null && previewPlatform !== detected
  const steps = stepsFor(platform)
  // Switching OS can drop the current step (WSL on Windows, docker group on Linux): fall back to Install Docker.
  const index = Math.max(
    0,
    steps.findIndex((s) => s.id === step),
  )
  const current = steps[index]
  const inSteps = current.id === step
  const done = (s: (typeof steps)[number]) => (preview && !!s.os) || stepDone(s, status)
  const currentDone = done(current)
  // A step is reachable once every earlier required step is done.
  const reachable = steps.map((_, i) => steps.slice(0, i).every(done))

  useEffect(() => {
    if (!inSteps) setStep("docker")
  }, [inSteps, setStep])

  // Move focus to the new step's heading so keyboard and screen-reader users land on it.
  useEffect(() => {
    headingRef.current?.focus()
  }, [step])

  return (
    <DialogPrimitive.Root open onOpenChange={(o) => !o && close()}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Content
          className="fixed inset-0 z-50 grid grid-cols-[17rem_minmax(0,1fr)] bg-background text-foreground outline-none motion-safe:animate-in motion-safe:fade-in-0"
          // Full-screen setup: Escape shouldn't drop the user out of it by accident.
          onEscapeKeyDown={(e) => e.preventDefault()}
          onOpenAutoFocus={(e) => {
            e.preventDefault()
            headingRef.current?.focus()
          }}
        >
          <aside className="flex min-h-0 flex-col gap-5 border-r bg-muted/30 p-5">
            <div className="flex items-center gap-2.5">
              <AppLogo className="size-8" />
              <div>
                <DialogPrimitive.Title className="font-heading text-sm font-semibold">Set up Nimbo</DialogPrimitive.Title>
                <DialogPrimitive.Description className="text-xs text-muted-foreground">A few minutes, one time</DialogPrimitive.Description>
              </div>
            </div>
            <OsCard status={status} detected={detected} platform={platform} preview={preview} />
            <nav aria-label="Setup steps" className="min-h-0 flex-1 overflow-y-auto">
              <ol className="flex flex-col gap-0.5">
                {steps.map((s, i) => {
                  const st = preview && s.os ? undefined : stepStatus(s, status)
                  const active = i === index
                  return (
                    <li key={s.id}>
                      <button
                        type="button"
                        disabled={!reachable[i]}
                        aria-current={active ? "step" : undefined}
                        onClick={() => setStep(s.id)}
                        className={cn(
                          "flex w-full items-center gap-3 rounded-lg px-2.5 py-2 text-left text-sm text-muted-foreground transition-colors outline-none hover:bg-muted hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50 disabled:pointer-events-none disabled:opacity-50",
                          active && "bg-background font-medium text-foreground shadow-xs ring-1 ring-border",
                        )}
                      >
                        <span
                          className={cn(
                            "flex size-6 shrink-0 items-center justify-center rounded-full border text-[11px] font-medium",
                            active && "border-primary bg-primary text-primary-foreground",
                          )}
                        >
                          {st ? (
                            <StatusIcon status={st} className={cn("size-4", active && "text-primary-foreground")} />
                          ) : i < index && !s.optional ? (
                            <CheckIcon className="size-3.5" aria-hidden />
                          ) : (
                            i + 1
                          )}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate">{s.label}</span>
                          {s.optional && <span className="block text-[11px] font-normal text-muted-foreground">Optional</span>}
                        </span>
                        {st && <span className="sr-only">({st === "ok" ? "done" : st})</span>}
                      </button>
                    </li>
                  )
                })}
              </ol>
            </nav>
            <Button variant="ghost" size="sm" className="justify-start text-muted-foreground" onClick={close}>
              <XIcon />
              I'll do this later
            </Button>
          </aside>

          <div className="flex min-h-0 flex-col">
            <main className="min-h-0 flex-1 overflow-y-auto">
              <div
                key={`${current.id}:${platform}`}
                className="mx-auto flex max-w-2xl flex-col gap-6 px-10 py-12 motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-right-2"
              >
                <div className="flex flex-col gap-1">
                  <span className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
                    Step {index + 1} of {steps.length}
                    {current.optional && " · optional"}
                  </span>
                  <h2 ref={headingRef} tabIndex={-1} className="font-heading text-2xl font-semibold tracking-tight outline-none">
                    {current.title}
                  </h2>
                </div>
                {preview && current.os && detected && <PreviewNote platform={platform} detected={detected} />}
                {current.id === "welcome" && (
                  <WelcomeStep platform={detected} osName={status?.osName} arch={status?.arch} />
                )}
                {current.id === "wsl" && <WslStep platform={platform} preview={preview} />}
                {current.id === "docker" && <DockerStep platform={platform} preview={preview} />}
                {current.id === "start" && <StartDockerStep platform={platform} preview={preview} />}
                {current.id === "permissions" && <PermissionsStep platform={platform} preview={preview} />}
                {current.id === "images" && <ImagesStep />}
                {current.id === "health" && <HealthStep steps={steps} />}
                {current.id === "agents" && <AgentsStep />}
                {current.id === "try" && <TryStep ready={!!status?.ready} />}
              </div>
            </main>
            <footer className="flex shrink-0 items-center gap-3 border-t px-10 py-4">
              <Button variant="ghost" onClick={() => setStep(steps[index - 1].id)} disabled={index === 0}>
                <ArrowLeftIcon />
                Back
              </Button>
              <span className="ml-auto text-sm text-muted-foreground" aria-live="polite">
                {!currentDone && "Finish this step to continue — or set up later."}
              </span>
              {index < steps.length - 1 ? (
                <Button onClick={() => setStep(steps[index + 1].id)} disabled={!currentDone}>
                  {current.optional ? "Skip" : current.id === "welcome" ? "Let's start" : "Next"}
                  <ArrowRightIcon />
                </Button>
              ) : (
                <Button variant="outline" onClick={close}>
                  Close
                </Button>
              )}
            </footer>
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  )
}

/** Detected OS (icon, name, CPU) plus the "Using a different OS?" switcher. */
function OsCard({ status, detected, platform, preview }: { status: SetupStatus | undefined; detected: Platform | undefined; platform: Platform; preview: boolean }) {
  const setPreviewPlatform = useOnboarding((s) => s.setPreviewPlatform)
  const shown = PLATFORMS[platform]
  return (
    <div className={cn("flex flex-col gap-2 rounded-xl border bg-background p-3", preview && "border-amber-500/50")}>
      <div className="flex items-center gap-2.5">
        <span className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-muted">
          <OsIcon platform={platform} className="size-5" />
        </span>
        <div className="min-w-0">
          <p className="truncate text-sm font-medium">
            {preview ? shown.label : status?.osName || (detected ? PLATFORMS[detected].label : "Detecting…")}
          </p>
          <p className="truncate text-xs text-muted-foreground">
            {preview
              ? "Previewing instructions"
              : status
                ? [archLabel(platform, status.arch), status.packageManager].filter(Boolean).join(" · ")
                : "\u00a0"}
          </p>
        </div>
      </div>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="xs" className="justify-between text-muted-foreground">
            {preview ? (
              <>
                <EyeIcon /> Previewing {shown.label}
              </>
            ) : (
              "Using a different OS?"
            )}
            <ChevronDownIcon />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="w-56">
          <DropdownMenuLabel>Show instructions for</DropdownMenuLabel>
          <DropdownMenuRadioGroup
            value={platform}
            onValueChange={(v) => setPreviewPlatform(v === detected ? null : (v as Platform))}
          >
            {PLATFORM_IDS.map((p) => (
              <DropdownMenuRadioItem key={p} value={p}>
                <OsIcon platform={p} className="size-4" />
                {PLATFORMS[p].label}
                {p === detected && <span className="ml-auto text-xs text-muted-foreground">this computer</span>}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

function PreviewNote({ platform, detected }: { platform: Platform; detected: Platform }) {
  const setPreviewPlatform = useOnboarding((s) => s.setPreviewPlatform)
  return (
    <div className="flex flex-wrap items-center gap-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-4 py-3 text-sm text-amber-800 dark:text-amber-200">
      <EyeIcon className="size-4 shrink-0" aria-hidden />
      <span className="min-w-0 flex-1">
        These are the {PLATFORMS[platform].label} instructions. This computer runs {PLATFORMS[detected].label}, so live checks are paused while you preview.
      </span>
      <Button size="sm" variant="outline" onClick={() => setPreviewPlatform(null)}>
        Back to {PLATFORMS[detected].label}
      </Button>
    </div>
  )
}
