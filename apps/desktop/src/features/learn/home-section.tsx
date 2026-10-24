import { useQuery } from "@tanstack/react-query"
import { GraduationCapIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api/client"
import { ProviderDot } from "./meta"
import { learnKeys, PROVIDER_META, PROVIDERS, useLearnStore } from "./store"

/** Home screen: the Learn entry point with each provider's progress. */
export function LearnSection() {
  const openView = useLearnStore((s) => s.openView)
  const overview = useQuery({ queryKey: learnKeys.overview, queryFn: api.learnOverview, retry: false })
  const providers = overview.data?.providers ?? []

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-medium text-muted-foreground">Learn</h2>
        <span className="hidden text-xs text-muted-foreground sm:inline">· roadmaps and hands-on challenges, beginner to expert</span>
        <Button size="sm" variant="ghost" className="ml-auto" onClick={() => openView({ challengeId: null })}>
          <GraduationCapIcon /> Open Learn
        </Button>
      </div>
      {overview.isPending ? (
        <Skeleton className="h-20 rounded-xl" />
      ) : overview.isError ? (
        <p className="text-sm text-muted-foreground">Learning paths will appear here once the engine is reachable.</p>
      ) : (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
          {PROVIDERS.map((id) => {
            const p = providers.find((x) => x.id === id)
            const total = p?.challenges ?? 0
            const done = p?.completed ?? 0
            return (
              <button
                key={id}
                type="button"
                onClick={() => openView({ provider: id, challengeId: null })}
                className="flex flex-col gap-1.5 rounded-xl border bg-card p-3 text-left transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring/50"
              >
                <span className="flex items-center gap-1.5 text-sm font-medium">
                  <ProviderDot provider={id} />
                  <span className="truncate">{PROVIDER_META[id].label}</span>
                </span>
                <span className="text-xs text-muted-foreground tabular-nums">
                  {total === 0 ? (p?.hasRoadmap ? "Roadmap" : "Coming soon") : `${done}/${total} challenges`}
                </span>
                <span className="h-1 overflow-hidden rounded-full bg-muted">
                  <span className="block h-full rounded-full bg-emerald-500" style={{ width: total ? `${(done / total) * 100}%` : "0%" }} />
                </span>
              </button>
            )
          })}
        </div>
      )}
      {overview.data && overview.data.status.errors.length > 0 && (
        <p className="text-xs text-destructive">Some learning content could not be loaded — open Learn for details.</p>
      )}
    </section>
  )
}
