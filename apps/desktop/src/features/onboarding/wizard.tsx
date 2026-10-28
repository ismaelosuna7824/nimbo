import { lazy, Suspense, useEffect, useRef } from "react"
import { useWorkflowStore } from "@/state/workflow-store"
import { FirstStepsGuide } from "./first-steps-guide"
import { ONBOARDED_KEY, openSetupChecklist, useOnboarding, useSetup } from "./store"

/**
 * Runs `open` once no app dialog (new workflow, import, export) is open. The setup check takes a few seconds; a user
 * who already opened a dialog meanwhile finishes it before the wizard takes over the screen, instead of seeing two
 * windows stacked on each other.
 */
function whenNoDialog(open: () => void) {
  if (!useWorkflowStore.getState().dialog) return open()
  const off = useWorkflowStore.subscribe((s) => {
    if (s.dialog) return
    off()
    open()
  })
}

const Wizard = lazy(() => import("./setup-wizard").then((m) => ({ default: m.Wizard })))

/**
 * Mounted once by App. Opens the setup wizard on first launch and whenever
 * the app starts while setup isn't complete, and hosts the first-steps guide.
 */
export function OnboardingHost() {
  const open = useOnboarding((s) => s.open)
  const setup = useSetup()
  const decided = useRef(false)

  useEffect(() => {
    if (decided.current || setup.isPending) return
    decided.current = true
    // Engine unreachable: the home banner explains how to start it.
    if (setup.isError) return
    if (!localStorage.getItem(ONBOARDED_KEY)) whenNoDialog(() => useOnboarding.getState().openWizard("welcome"))
    else if (!setup.data.ready) whenNoDialog(openSetupChecklist)
  }, [setup.isPending, setup.isError, setup.data])

  return (
    <>
      {open && (
        <Suspense fallback={<div className="fixed inset-0 z-50 bg-background" />}>
          <Wizard />
        </Suspense>
      )}
      <FirstStepsGuide />
    </>
  )
}
