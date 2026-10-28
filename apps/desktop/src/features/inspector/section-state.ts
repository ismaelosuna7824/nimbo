import { create } from "zustand"

// What the user opened or closed in the inspector, remembered across selections and restarts. Keyed by section type
// ("settings", "connect", "docker:ports"…), not by node: closing Access on one resource closes it on the next one too,
// which is what someone who doesn't need it wants.
const SECTIONS_KEY = "nimbo.inspector.sections"
const VIEW_KEY = "nimbo.inspector.settingsView"

/** Settings of a big resource: every attribute, or only the configured ones (plus required and "Add setting…"). */
export type SettingsView = "all" | "configured"

function loadSections(): Record<string, boolean> {
  try {
    const raw: unknown = JSON.parse(localStorage.getItem(SECTIONS_KEY) ?? "{}")
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {}
    return Object.fromEntries(Object.entries(raw).filter((e): e is [string, boolean] => typeof e[1] === "boolean"))
  } catch {
    return {}
  }
}

function loadView(): SettingsView {
  try {
    return localStorage.getItem(VIEW_KEY) === "configured" ? "configured" : "all"
  } catch {
    return "all"
  }
}

function save(key: string, value: string) {
  try {
    localStorage.setItem(key, value)
  } catch {
    // Storage unavailable: the choice lasts for this session only.
  }
}

interface SectionState {
  open: Record<string, boolean>
  view: SettingsView
  setOpen: (id: string, open: boolean) => void
  setView: (view: SettingsView) => void
}

export const useSectionState = create<SectionState>((set, get) => ({
  open: loadSections(),
  view: loadView(),
  setOpen(id, open) {
    const next = { ...get().open, [id]: open }
    save(SECTIONS_KEY, JSON.stringify(next))
    set({ open: next })
  },
  setView(view) {
    save(VIEW_KEY, view)
    set({ view })
  },
}))

/** Whether section `id` is open: the user's last choice, else `defaultOpen`. */
export function useSectionOpen(id: string, defaultOpen: boolean): [boolean, (open: boolean) => void] {
  const stored = useSectionState((s) => s.open[id])
  const setOpen = useSectionState((s) => s.setOpen)
  return [stored ?? defaultOpen, (open: boolean) => setOpen(id, open)]
}
