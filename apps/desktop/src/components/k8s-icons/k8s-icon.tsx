import { cn } from "@/lib/utils"
import { k8sIconFor, type K8sIconInfo } from "./index"

type Props = {
  /** Object kind ("Deployment"), or an icon already resolved. */
  icon: string | K8sIconInfo
  /** Square size in px (default 20). */
  size?: number
  className?: string
  /** Tooltip/alt text; defaults to the kind. */
  title?: string
}

/** A Kubernetes object icon (blue heptagon of the community icon set). */
export function K8sIcon({ icon, size = 20, className, title }: Props) {
  const info = typeof icon === "string" ? k8sIconFor(icon) : icon
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
      className={cn("shrink-0 object-contain", className)}
    />
  )
}
