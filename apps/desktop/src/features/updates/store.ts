import { invoke, isTauri } from "@tauri-apps/api/core"
import { relaunch } from "@tauri-apps/plugin-process"
import { check, type Update } from "@tauri-apps/plugin-updater"
import { create } from "zustand"
import { errorMessage } from "@/components/errors"
import { openExternal } from "@/components/open-external"

const RELEASES_URL = "https://github.com/ismaelosuna7824/nimbo/releases"
const AUTO_CHECK_KEY = "nimbo.updates.autoCheck"

export type UpdatePhase = "idle" | "checking" | "up-to-date" | "available" | "downloading" | "restarting" | "error"

export interface AvailableUpdate {
  version: string
  notes: string
  date: string | null
}

interface UpdatesState {
  phase: UpdatePhase
  available: AvailableUpdate | null
  /** False for Linux .deb/.rpm installs: the package manager owns the files, so we link to the release. */
  selfUpdate: boolean
  downloaded: number
  total: number | null
  error: string | null
  autoCheck: boolean
  setAutoCheck: (on: boolean) => void
  /** Asks the release endpoint for a newer version; resolves to it, or null. */
  check: () => Promise<AvailableUpdate | null>
  /** Downloads, verifies and installs the available update, then relaunches (or opens the release page). */
  install: () => Promise<void>
}

/** The updater only exists inside the desktop app, not in the browser dev UI. */
export const updatesEnabled = isTauri()

let pending: Update | null = null

export const useUpdates = create<UpdatesState>((set, get) => ({
  phase: "idle",
  available: null,
  selfUpdate: true,
  downloaded: 0,
  total: null,
  error: null,
  autoCheck: localStorage.getItem(AUTO_CHECK_KEY) !== "false",

  setAutoCheck: (on) => {
    localStorage.setItem(AUTO_CHECK_KEY, String(on))
    set({ autoCheck: on })
  },

  check: async () => {
    const busy = get().phase
    if (!updatesEnabled || busy === "checking" || busy === "downloading" || busy === "restarting") return get().available
    set({ phase: "checking", error: null })
    try {
      const [update, selfUpdate] = await Promise.all([check(), invoke<boolean>("updater_supported")])
      void pending?.close()
      pending = update
      const available = update ? { version: update.version, notes: update.body ?? "", date: update.date ?? null } : null
      set({ phase: available ? "available" : "up-to-date", available, selfUpdate })
      return available
    } catch (err) {
      set({ phase: "error", error: errorMessage(err) })
      return null
    }
  },

  install: async () => {
    const { available, selfUpdate, phase } = get()
    if (!available || phase === "downloading" || phase === "restarting") return
    if (!selfUpdate || !pending) {
      await openExternal(`${RELEASES_URL}/tag/v${available.version}`)
      return
    }
    set({ phase: "downloading", downloaded: 0, total: null, error: null })
    try {
      await pending.downloadAndInstall((event) => {
        if (event.event === "Started") set({ total: event.data.contentLength ?? null })
        else if (event.event === "Progress") set((s) => ({ downloaded: s.downloaded + event.data.chunkLength }))
      })
      set({ phase: "restarting" })
      await relaunch()
    } catch (err) {
      set({ phase: "error", error: errorMessage(err) })
    }
  },
}))
