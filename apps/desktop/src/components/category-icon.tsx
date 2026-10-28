import {
  ActivityIcon,
  BoxesIcon,
  BrainCircuitIcon,
  ChartColumnIcon,
  CircleDollarSignIcon,
  CodeXmlIcon,
  ContainerIcon,
  CpuIcon,
  DatabaseIcon,
  GlobeIcon,
  HardDriveIcon,
  IdCardIcon,
  KeyRoundIcon,
  LayoutGridIcon,
  MessagesSquareIcon,
  NetworkIcon,
  NewspaperIcon,
  ScanSearchIcon,
  ShapesIcon,
  ShieldCheckIcon,
  WaypointsIcon,
  WorkflowIcon,
  WrenchIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react"
import { cn } from "@/lib/utils"

/**
 * Soft tints, one per family of categories so related ones read alike (data, compute, network…). Red, amber and
 * green are left out on purpose: they mean error, warning and success (docs/UX_GUIDELINES.md, rule 2).
 */
const TONE = {
  data: "bg-sky-500/10 text-sky-600 dark:text-sky-400",
  compute: "bg-violet-500/10 text-violet-600 dark:text-violet-400",
  network: "bg-cyan-500/10 text-cyan-600 dark:text-cyan-400",
  events: "bg-fuchsia-500/10 text-fuchsia-600 dark:text-fuchsia-400",
  security: "bg-indigo-500/10 text-indigo-600 dark:text-indigo-400",
  insight: "bg-teal-500/10 text-teal-600 dark:text-teal-400",
  ai: "bg-pink-500/10 text-pink-600 dark:text-pink-400",
  tools: "bg-slate-500/10 text-slate-600 dark:text-slate-300",
} as const

type Tone = keyof typeof TONE

/**
 * Icon and tint of a palette category, for every workspace: the Terraform catalog's categories (by label, the same
 * for every cloud) and the Docker/Kubernetes image categories (by id).
 */
const CATEGORIES: Record<string, [LucideIcon, Tone]> = {
  // Terraform catalog (AWS, Azure, Google Cloud, OCI).
  storage: [HardDriveIcon, "data"],
  databases: [DatabaseIcon, "data"],
  compute: [CpuIcon, "compute"],
  containers: [ContainerIcon, "compute"],
  networking: [NetworkIcon, "network"],
  "messaging & events": [MessagesSquareIcon, "events"],
  identity: [IdCardIcon, "security"],
  security: [ShieldCheckIcon, "security"],
  observability: [ActivityIcon, "insight"],
  analytics: [ChartColumnIcon, "insight"],
  "ai & ml": [BrainCircuitIcon, "ai"],
  "developer tools": [WrenchIcon, "tools"],
  cost: [CircleDollarSignIcon, "insight"],
  other: [ShapesIcon, "tools"],
  // Docker and Kubernetes image catalog (ids; storage and observability are shared with the list above).
  web: [GlobeIcon, "network"],
  gateway: [WaypointsIcon, "network"],
  language: [CodeXmlIcon, "compute"],
  database: [DatabaseIcon, "data"],
  cache: [ZapIcon, "data"],
  search: [ScanSearchIcon, "data"],
  messaging: [MessagesSquareIcon, "events"],
  auth: [KeyRoundIcon, "security"],
  workflow: [WorkflowIcon, "events"],
  ai: [BrainCircuitIcon, "ai"],
  devtools: [WrenchIcon, "tools"],
  apps: [LayoutGridIcon, "compute"],
  cms: [NewspaperIcon, "compute"],
  network: [NetworkIcon, "network"],
  tools: [BoxesIcon, "tools"],
}

/** A palette category's icon on a small tinted tile; a neutral shape for a category this build doesn't know. */
export function CategoryIcon({ category, className }: { category: string; className?: string }) {
  const [Icon, tone] = CATEGORIES[category.toLowerCase()] ?? [ShapesIcon, "tools"]
  return (
    <span aria-hidden className={cn("flex size-6 shrink-0 items-center justify-center rounded-md", TONE[tone], className)}>
      <Icon className="size-3.5" />
    </span>
  )
}
