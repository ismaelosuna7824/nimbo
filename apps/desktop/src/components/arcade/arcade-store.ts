import { create } from "zustand"

/**
 * Arcade: an opt-in 8-bit look layered over the light/dark theme. It lives on
 * <html> as data-style="arcade" (and data-crt for the scanline overlay), so the
 * CSS in arcade.css can scope every rule to it; off, nothing changes.
 */
const STYLE_KEY = "nimbo.style"
const CRT_KEY = "nimbo.crt"

function read(key: string): string | null {
  try {
    return localStorage.getItem(key)
  } catch {
    return null
  }
}

function write(key: string, value: string | null) {
  try {
    if (value === null) localStorage.removeItem(key)
    else localStorage.setItem(key, value)
  } catch {
    // Storage unavailable: the choice lasts for this session only.
  }
}

function apply(arcade: boolean, crt: boolean) {
  const root = document.documentElement
  if (arcade) root.dataset.style = "arcade"
  else delete root.dataset.style
  if (arcade && crt) root.dataset.crt = ""
  else delete root.dataset.crt
}

interface ArcadeState {
  arcade: boolean
  /** CRT scanlines over the whole window; only drawn while Arcade is on. */
  crt: boolean
  /** Bumped on each successful deploy; the celebration replays when it changes. */
  celebration: number
  /** Banner of the current celebration ("Deployed!", "Challenge complete!"). */
  celebrationLabel: string
  setArcade: (on: boolean) => void
  setCrt: (on: boolean) => void
  /** Plays the mascot's deploy celebration (only drawn while Arcade is on). */
  celebrate: (label?: string) => void
}

const initialArcade = read(STYLE_KEY) === "arcade"
const initialCrt = read(CRT_KEY) === "1"
// Applied at import, before the first render, so the app never flashes the default look.
apply(initialArcade, initialCrt)

export const useArcadeStore = create<ArcadeState>((set, get) => ({
  arcade: initialArcade,
  crt: initialCrt,
  celebration: 0,
  celebrationLabel: "Deployed!",
  setArcade: (on) => {
    write(STYLE_KEY, on ? "arcade" : null)
    apply(on, get().crt)
    set({ arcade: on })
  },
  setCrt: (on) => {
    write(CRT_KEY, on ? "1" : null)
    apply(get().arcade, on)
    set({ crt: on })
  },
  celebrate: (label = "Deployed!") => set((s) => ({ celebration: s.celebration + 1, celebrationLabel: label })),
}))

/** True while the Arcade (8-bit) style is on. */
export function useArcade(): boolean {
  return useArcadeStore((s) => s.arcade)
}
