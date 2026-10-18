import { isTauri } from "@tauri-apps/api/core"
import { open } from "@tauri-apps/plugin-shell"

/** Opens a URL in the user's browser (system browser inside Tauri). */
export async function openExternal(url: string) {
  if (isTauri()) await open(url)
  else window.open(url, "_blank", "noopener,noreferrer")
}
