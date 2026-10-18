import markUrl from "@/assets/nimbo-mark.svg"
import { cn } from "@/lib/utils"

/** Nimbo mark: pixel-art cloud holding a cube (same artwork as the app icon), crisp at any size. */
export function AppLogo({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <img src={markUrl} alt="" aria-hidden="true" draggable={false} className={cn("shrink-0 select-none", className)} style={style} />
}
