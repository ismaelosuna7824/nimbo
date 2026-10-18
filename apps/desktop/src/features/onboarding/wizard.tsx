import { lazy, Suspense, useEffect, useRef } from "react"
import { FirstStepsGuide } from "./first-steps-guide"
import { ONBOARDED_KEY, openSetupChecklist, useOnboarding, useSetup } from "./store"

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
    if (!localStorage.getItem(ONBOARDED_KEY)) useOnboarding.getState().openWizard("welcome")
    else if (!setup.data.ready) openSetupChecklist()
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
