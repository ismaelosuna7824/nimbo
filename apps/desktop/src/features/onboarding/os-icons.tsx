import { AppleIcon, TerminalSquareIcon } from "lucide-react"
import type { ComponentType } from "react"
import type { Platform } from "./platforms"

/** Windows logo (lucide has no brand icon for it). */
function WindowsIcon({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 16 16" fill="currentColor" className={className} aria-hidden="true">
      <path d="M1 2.6 6.7 1.8v5.5H1zM7.5 1.7 15 .6v6.7H7.5zM1 8.1h5.7v5.5L1 12.8zM7.5 8.1H15v6.8l-7.5-1z" />
    </svg>
  )
}

const ICONS: Record<Platform, ComponentType<{ className?: string }>> = {
  darwin: AppleIcon,
  windows: WindowsIcon,
  linux: TerminalSquareIcon,
}

export function OsIcon({ platform, className }: { platform: Platform; className?: string }) {
  const Icon = ICONS[platform]
  return <Icon className={className} />
}
