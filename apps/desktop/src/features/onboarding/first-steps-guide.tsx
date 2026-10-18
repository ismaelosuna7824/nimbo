import { CheckIcon, PartyPopperIcon, XIcon } from "lucide-react"
import { useEffect, useState } from "react"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { useConnectionsStore } from "@/features/connections/store"
import { useSettingsView } from "@/features/settings/store"
import { runAction } from "@/features/toolbar/actions"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { useOnboarding } from "./store"

/**
 * Floating checklist shown in the editor after "Create my first workflow":
 * Deploy → Load test → Start test, each ticked off from the workflow's live state.
 */
export function FirstStepsGuide() {
  const guideId = useOnboarding((s) => s.guideWorkflowId)
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const settingsOpen = useSettingsView((s) => s.open)
  const connectionsOpen = useConnectionsStore((s) => s.viewOpen)
  if (!guideId || guideId !== workflowId || settingsOpen || connectionsOpen) return null
  return <Guide />
}

function Guide() {
  const endGuide = useOnboarding((s) => s.endGuide)
  const runs = useWorkflowStore((s) => s.runs)
  const loadTests = useWorkflowStore((s) => s.loadTests)
  const testPanelOpen = useWorkflowStore((s) => s.bottomOpen && s.bottomTab === "loadtest")
  const [testOpenedBefore, setTestOpenedBefore] = useState(false)

  const applies = runs.filter((r) => r.action === "apply")
  const deployed = applies.some((r) => r.status === "succeeded")
  const deploying = applies.some((r) => r.status === "queued" || r.status === "running")
  const deployFailed = !deployed && !deploying && applies[0]?.status === "failed"
  const started = loadTests.length > 0

  // Opening the test panel after deploying counts once, even if the user closes it again.
  useEffect(
    () =>
      useWorkflowStore.subscribe((s, prev) => {
        const open = (x: typeof s) => x.bottomOpen && x.bottomTab === "loadtest"
        if (open(prev) && !open(s)) setTestOpenedBefore(true)
      }),
    [],
  )
  const testOpened = deployed && (testPanelOpen || testOpenedBefore)

  const steps = [
    {
      title: "Deploy",
      done: deployed,
      text: deployFailed
        ? "The deploy failed — the Deploy log below says why. Fix it or try again."
        : deploying
          ? "Building your API in the local cloud. The first deploy takes about a minute."
          : "Press Deploy (top right) to build this API in your private local cloud.",
      action: deploying ? (
        <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Spinner className="size-3.5" /> Deploying…
        </span>
      ) : (
        <Button size="sm" onClick={() => void runAction("apply")}>
          {deployFailed ? "Try again" : "Deploy now"}
        </Button>
      ),
    },
    {
      title: "Load test",
      done: started || testOpened,
      text: "Open the Test panel to choose what to send traffic to.",
      action: (
        <Button size="sm" onClick={() => useWorkflowStore.getState().showBottom("loadtest")}>
          Open load test
        </Button>
      ),
    },
    {
      title: "Start test",
      done: started,
      text: "Pick the API endpoint, keep the suggested traffic shape and press Start test. Watch response times and errors live.",
      action: null,
    },
  ]
  const currentIndex = steps.findIndex((s) => !s.done)
  const finished = currentIndex === -1

  return (
    <aside
      aria-label="First steps"
      className="fixed top-14 right-3 z-30 flex w-80 flex-col gap-3 rounded-xl border bg-popover p-4 text-popover-foreground shadow-lg motion-safe:animate-in motion-safe:fade-in-0 motion-safe:slide-in-from-top-2"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="text-sm font-semibold">{finished ? "That's the whole loop!" : "Your first test run"}</p>
          <p className="text-xs text-muted-foreground">
            {finished ? "Design → deploy → test. Change something on the canvas and run it again." : "Three steps, about two minutes."}
          </p>
        </div>
        <Button variant="ghost" size="icon-xs" aria-label="Hide the guide" onClick={endGuide}>
          <XIcon />
        </Button>
      </div>
      <ol className="flex flex-col gap-2">
        {steps.map((s, i) => {
          const current = i === currentIndex
          return (
            <li
              key={s.title}
              aria-current={current ? "step" : undefined}
              className={cn("flex gap-3 rounded-lg p-2", current && "bg-muted/60")}
            >
              <span
                className={cn(
                  "mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border text-[11px] font-medium",
                  s.done && "border-emerald-500 bg-emerald-500 text-white",
                  current && "border-primary bg-primary text-primary-foreground",
                )}
              >
                {s.done ? <CheckIcon className="size-3" aria-label="done" /> : i + 1}
              </span>
              <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                <span className={cn("text-sm font-medium", !current && !s.done && "text-muted-foreground")}>{s.title}</span>
                {current && <p className="text-xs text-muted-foreground">{s.text}</p>}
                {current && s.action && <div>{s.action}</div>}
              </div>
            </li>
          )
        })}
      </ol>
      {finished && (
        <Button size="sm" onClick={endGuide}>
          <PartyPopperIcon />
          Done
        </Button>
      )}
    </aside>
  )
}
