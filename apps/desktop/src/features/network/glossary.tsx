import { InfoIcon } from "lucide-react"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { SubnetClassification } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { NET_GLOSSARY, SUBNET_META, type NetTerm } from "./terms"

/** A label with a dotted underline that explains a network term on hover. */
export function Term({ term, children, className }: { term: NetTerm; children: React.ReactNode; className?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          className={cn("cursor-help underline decoration-muted-foreground/50 decoration-dotted underline-offset-2", className)}
        >
          {children}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{NET_GLOSSARY[term]}</TooltipContent>
    </Tooltip>
  )
}

/** A small info icon with a tooltip, for headers and buttons. */
export function TermHint({ text, label = "What is this?" }: { text: string; label?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" aria-label={label} className="text-muted-foreground hover:text-foreground">
          <InfoIcon className="size-3.5" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{text}</TooltipContent>
    </Tooltip>
  )
}

/** Public / Private / Isolated badge with the beginner explanation as tooltip. */
export function SubnetBadge({ classification }: { classification: SubnetClassification }) {
  const meta = SUBNET_META[classification] ?? SUBNET_META.unknown
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          data-testid="subnet-classification"
          className={cn("inline-flex h-5 cursor-help items-center rounded-full px-2 text-xs font-medium", meta.className)}
        >
          {meta.label}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{NET_GLOSSARY[classification] ?? NET_GLOSSARY.unknown}</TooltipContent>
    </Tooltip>
  )
}

/** "Based on the deployed sandbox" / "Based on the design" with the server's reason as tooltip. */
export function BasisBadge({ basis, reason }: { basis: "deployed" | "design"; reason?: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          data-testid="network-basis"
          className={cn(
            "inline-flex h-5 cursor-help items-center rounded-full border px-2 text-[11px] font-medium",
            basis === "deployed" ? "border-sky-500/40 text-sky-700 dark:text-sky-300" : "text-muted-foreground",
          )}
        >
          {basis === "deployed" ? "Based on the deployed sandbox" : "Based on the design"}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-72">{reason || NET_GLOSSARY.basis}</TooltipContent>
    </Tooltip>
  )
}
