import { TechIcon } from "@/components/tech-icons/tech-icon"
import type { WorkflowSummary } from "@/lib/api/types"
import { cn } from "@/lib/utils"

/** Marks a Docker workspace in lists (home, switcher, import) the way cloud badges mark Terraform ones. */
export function DockerBadge({ className, compact }: { className?: string; compact?: boolean }) {
  return (
    <span
      data-testid="docker-badge"
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1 rounded-full border border-sky-500/30 bg-sky-500/10 px-1.5 text-[11px] font-medium text-sky-700 dark:text-sky-300",
        compact && "px-1",
        className,
      )}
    >
      <TechIcon icon="docker" size={12} title="Docker" />
      {!compact && "Docker"}
    </span>
  )
}

/** "3 services" for a Docker workspace, "7 objects" for a Kubernetes one, "5 resources" for a Terraform workflow. */
export function contentCount(w: Pick<WorkflowSummary, "kind" | "nodeCount">): string {
  const noun = w.kind === "docker" ? "service" : w.kind === "kubernetes" ? "object" : "resource"
  return `${w.nodeCount} ${noun}${w.nodeCount === 1 ? "" : "s"}`
}
