import { BookOpenIcon, ExternalLinkIcon } from "lucide-react"
import { openExternal } from "@/components/open-external"
import { Badge } from "@/components/ui/badge"
import type { LearnLevel, LearnSource } from "@/lib/api/types"
import { LEVEL_LABEL } from "./store"

function ExternalLink({ source, className, icon }: { source: LearnSource; className: string; icon: React.ReactNode }) {
  return (
    <a
      href={source.url}
      onClick={(e) => {
        e.preventDefault()
        void openExternal(source.url)
      }}
      className={className}
    >
      {icon}
      {source.title}
    </a>
  )
}

/** A roadmap step's official docs. */
export function DocLinks({ docs }: { docs: LearnSource[] }) {
  if (docs.length === 0) return null
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1">
      {docs.map((d) => (
        <li key={d.url}>
          <ExternalLink
            source={d}
            icon={<BookOpenIcon className="size-3" />}
            className="inline-flex items-center gap-1 text-xs text-primary underline-offset-4 hover:underline"
          />
        </li>
      ))}
    </ul>
  )
}

export function Sources({ sources, title = "Official sources" }: { sources: LearnSource[]; title?: string }) {
  if (sources.length === 0) return null
  return (
    <section className="flex flex-col gap-2">
      <h4 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{title}</h4>
      <ul className="flex flex-col gap-1">
        {sources.map((s) => (
          <li key={s.url}>
            <ExternalLink
              source={s}
              icon={<ExternalLinkIcon className="size-3.5 shrink-0" />}
              className="inline-flex items-center gap-1.5 text-sm text-primary underline-offset-4 hover:underline"
            />
          </li>
        ))}
      </ul>
    </section>
  )
}

const LEVEL_TONE: Record<LearnLevel, string> = {
  beginner: "border-emerald-500/40 text-emerald-700 dark:text-emerald-400",
  intermediate: "border-sky-500/40 text-sky-700 dark:text-sky-400",
  advanced: "border-amber-500/40 text-amber-700 dark:text-amber-400",
  expert: "border-rose-500/40 text-rose-700 dark:text-rose-400",
}

export function LevelBadge({ level }: { level: LearnLevel }) {
  return (
    <Badge variant="outline" className={LEVEL_TONE[level]}>
      {LEVEL_LABEL[level]}
    </Badge>
  )
}
