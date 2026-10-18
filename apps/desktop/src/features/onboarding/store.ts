import { useMutation, useQuery } from "@tanstack/react-query"
import { toast } from "sonner"
import { create } from "zustand"
import { errorMessage } from "@/components/errors"
import { ApiError, api } from "@/lib/api/client"
import type { SetupProgress, SetupStatus } from "@/lib/api/types"
import { queryClient } from "@/lib/query-client"
import { detectedPlatform, type Platform } from "./platforms"
import { findCheck, firstOpenStep, stepsFor } from "./steps"

/** Set once the user has been through (or dismissed) the setup wizard. */
export const ONBOARDED_KEY = "nimbo.onboarded"
export const SETUP_KEY = ["setup"] as const

export type StepId = "welcome" | "wsl" | "docker" | "start" | "permissions" | "images" | "health" | "agents" | "try"

/** Live state of one image while `pull-images` runs. */
export interface ImageProgress {
  image: string
  step: SetupProgress["step"]
  message: string
  layersDone?: number
  layersTotal?: number
  done: boolean
  error?: string
}

/** What the wizard knows about a running (or the last) setup action, from `setup.progress` events. */
export interface ActionProgress {
  running: boolean
  current: number
  total: number
  message: string
  error?: string
  /** Pull only: per image, in the order the daemon reported them. */
  images: ImageProgress[]
}

interface OnboardingState {
  open: boolean
  step: StepId
  actions: Partial<Record<string, ActionProgress>>
  /** Workflow the "first steps" guide (Deploy → Load test → Start test) runs on; null when hidden. */
  guideWorkflowId: string | null
  /** "Using a different OS?": show another OS's instructions instead of this machine's. */
  previewPlatform: Platform | null
  openWizard: (step?: StepId) => void
  /** Closes the wizard; the user has seen it, so first-launch auto-open stops. */
  close: () => void
  setStep: (step: StepId) => void
  /** Marks an action as started locally (before its first event arrives). */
  beginAction: (action: string) => void
  applyProgress: (p: SetupProgress) => void
  startGuide: (workflowId: string) => void
  endGuide: () => void
  setPreviewPlatform: (platform: Platform | null) => void
}

export const useOnboarding = create<OnboardingState>((set) => ({
  open: false,
  step: "welcome",
  actions: {},
  guideWorkflowId: null,
  previewPlatform: null,
  openWizard: (step) => set((s) => ({ open: true, step: step ?? s.step })),
  close: () => {
    localStorage.setItem(ONBOARDED_KEY, "1")
    set({ open: false, previewPlatform: null })
  },
  setStep: (step) => set({ step }),
  beginAction: (action) =>
    set((s) => ({
      actions: { ...s.actions, [action]: { running: true, current: 0, total: 0, message: "", images: s.actions[action]?.running ? s.actions[action].images : [] } },
    })),
  applyProgress: (p) =>
    set((s) => {
      const prev = s.actions[p.action]
      // A "start" event after a finished run begins a fresh one.
      const fresh = !prev || (!prev.running && p.step === "start")
      const images = fresh ? [] : [...prev.images]
      if (p.image) {
        const i = images.findIndex((x) => x.image === p.image)
        const old = i >= 0 ? images[i] : undefined
        const next: ImageProgress = {
          image: p.image,
          step: p.step,
          message: p.message,
          layersDone: p.layersDone ?? old?.layersDone,
          layersTotal: p.layersTotal ?? old?.layersTotal,
          done: p.step === "skip" || p.step === "image-done" || (old?.done ?? false),
          error: p.error,
        }
        if (i >= 0) images[i] = next
        else images.push(next)
      }
      const action: ActionProgress = {
        running: !p.done,
        current: p.current,
        total: p.total,
        message: p.message,
        error: p.error,
        images,
      }
      return { actions: { ...s.actions, [p.action]: action } }
    }),
  startGuide: (guideWorkflowId) => set({ guideWorkflowId }),
  endGuide: () => set({ guideWorkflowId: null }),
  setPreviewPlatform: (previewPlatform) => set({ previewPlatform }),
}))

/** GET /setup, re-checked every `interval` ms when given (3 s on steps waiting for the user to fix something). */
export function useSetup({ interval }: { interval?: number } = {}) {
  return useQuery({
    queryKey: SETUP_KEY,
    queryFn: api.getSetup,
    retry: false,
    refetchInterval: interval ?? false,
  })
}

/** Opens the wizard where the user has something left to do (the Health check once everything is ready). */
export function openSetupChecklist() {
  const status = queryClient.getQueryData<SetupStatus>(SETUP_KEY)
  useOnboarding.getState().openWizard(firstOpenStep(stepsFor(detectedPlatform(status) ?? "darwin"), status))
}

/** Live status of a step's main check while the step is visible (polls every 3 s). */
export function useStepCheck(id: string) {
  const setup = useSetup({ interval: 3000 })
  return { setup, check: findCheck(setup.data, id) }
}

/** Starts a setup action (POST /setup/actions/{action}); progress arrives as `setup.progress` events. */
export function useSetupAction(action: string) {
  const running = useOnboarding((s) => s.actions[action]?.running ?? false)
  const mutation = useMutation({
    mutationFn: () => api.setupAction(action),
    onMutate: () => useOnboarding.getState().beginAction(action),
    onError: (err) => {
      // Already running (e.g. started before the wizard was reopened): keep following its events.
      if (err instanceof ApiError && err.status === 409) return
      useOnboarding.getState().applyProgress({ action, step: "done", current: 0, total: 0, message: "", done: true, error: errorMessage(err) })
      toast.error("That didn't work", { description: errorMessage(err) })
    },
    onSettled: () => void queryClient.invalidateQueries({ queryKey: SETUP_KEY }),
  })
  return { run: () => mutation.mutate(), busy: mutation.isPending || running }
}
