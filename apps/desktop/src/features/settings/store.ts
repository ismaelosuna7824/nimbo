import { create } from "zustand"
import { useQuery } from "@tanstack/react-query"
import { api } from "@/lib/api/client"

export type SettingsSection = "general" | "sandbox" | "practices" | "agents" | "data" | "about"

interface SettingsViewState {
  /** The full-page Settings view covers home / the editor while open. */
  open: boolean
  section: SettingsSection
  openView: (section?: SettingsSection) => void
  closeView: () => void
  setSection: (section: SettingsSection) => void
}

export const useSettingsView = create<SettingsViewState>((set) => ({
  open: false,
  section: "general",
  openView: (section) => set((s) => ({ open: true, section: section ?? s.section })),
  closeView: () => set({ open: false }),
  setSection: (section) => set({ section }),
}))

export const SETTINGS_KEY = ["settings"] as const
export const AGENTS_KEY = ["agents"] as const

export function useSettings() {
  return useQuery({ queryKey: SETTINGS_KEY, queryFn: api.settings, retry: false })
}

export function useSettingsDefaults() {
  return useQuery({ queryKey: ["settings", "defaults"], queryFn: api.settingsDefaults, staleTime: Infinity, retry: false })
}
