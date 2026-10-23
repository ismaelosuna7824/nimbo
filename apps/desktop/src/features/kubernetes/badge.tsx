import { TechIcon } from "@/components/tech-icons/tech-icon"
import { cn } from "@/lib/utils"

/** Marks a Kubernetes workspace in lists (home, switcher, import) the way cloud badges mark Terraform ones. */
export function KubernetesBadge({ className, compact }: { className?: string; compact?: boolean }) {
  return (
    <span
      data-testid="kubernetes-badge"
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-full border border-blue-500/30 bg-blue-500/10 px-1.5 text-[11px] font-medium text-blue-700 dark:text-blue-300",
        compact && "px-1",
        className,
      )}
    >
      <TechIcon icon="kubernetes" size={12} title="Kubernetes" />
      {!compact && "Kubernetes"}
    </span>
  )
}
