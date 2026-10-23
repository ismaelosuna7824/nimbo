import { cn } from "@/lib/utils"
import { techIcon, type TechIconInfo } from "./index"

type Props = {
  /** Registry id, or an icon already resolved by `techIconFor`. */
  icon: string | TechIconInfo
  /** Square size in px (default 20). */
  size?: number
  className?: string
  /** Tooltip/alt text; defaults to the icon label. */
  title?: string
}

/** A technology logo. Dark-ink logos get a light backdrop in dark mode so they stay legible. */
export function TechIcon({ icon, size = 20, className, title }: Props) {
  const info = typeof icon === "string" ? techIcon(icon) : icon
  return (
    <img
      src={info.src}
      width={size}
      height={size}
      alt={title ?? info.label}
      title={title ?? info.label}
      loading="lazy"
      decoding="async"
      draggable={false}
      className={cn("shrink-0 object-contain", info.darkInk && "dark:rounded-[3px] dark:bg-white/90 dark:p-px", className)}
    />
  )
}
