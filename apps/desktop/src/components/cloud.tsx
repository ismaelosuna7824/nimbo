import { CLOUD_META } from "@/components/cloud-meta"
import type { Cloud } from "@/lib/api/types"
import { cn } from "@/lib/utils"

export function CloudBadge({ cloud, className }: { cloud: Cloud; className?: string }) {
  const meta = CLOUD_META[cloud]
  return (
    <span
      className={cn(
        "inline-flex h-5 items-center gap-1.5 rounded-full border px-2 text-xs font-medium whitespace-nowrap",
        meta.border,
        className,
      )}
    >
      <span className={cn("size-1.5 rounded-full", meta.dot)} />
      {meta.label}
    </span>
  )
}

/**
 * The clouds a workflow is "about", for its badges: the ones its resources use. A provider declared but used by
 * nothing doesn't count (it shows as unused under Providers instead). A workflow with no resources yet falls back to
 * the clouds it was set up for, so a blank AWS workflow still says AWS.
 */
export function displayClouds(w: { clouds: Cloud[]; usedClouds?: Cloud[] | null }): Cloud[] {
  return w.usedClouds?.length ? w.usedClouds : (w.clouds ?? [])
}
