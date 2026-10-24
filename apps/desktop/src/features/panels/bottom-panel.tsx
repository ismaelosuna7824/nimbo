import { lazy, memo, Suspense, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import { ChevronDown, ChevronUp, ClipboardCheck, Code2, Gauge, Layers, Plug, Rocket, SlidersHorizontal, TriangleAlert, Trophy } from "lucide-react"
import { ThemedIcon } from "@/components/arcade/themed-icon"
import type { PixelIconName } from "@/components/arcade/pixel-icons"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useWorkflowStore, type BottomTab } from "@/state/workflow-store"
import { DeployTab } from "./deploy/deploy-tab"
import { useDeployToast } from "./endpoints/deploy-toast"
import { ProblemsTab } from "./problems/problems-tab"
import { FilesTab } from "./terraform/files-tab"
import { VariablesTab } from "./variables/variables-tab"

// Heavier tabs (code highlighting, charts) load in their own chunks after the first paint.
const EndpointsTab = lazy(() => import("./endpoints/endpoints-tab").then((m) => ({ default: m.EndpointsTab })))
const LoadTestTab = lazy(() => import("./loadtest/loadtest-tab").then((m) => ({ default: m.LoadTestTab })))
const ComposeDeployTab = lazy(() =>
  import("@/features/docker/deploy/compose-deploy-tab").then((m) => ({ default: m.ComposeDeployTab })),
)
const KubeDeployTab = lazy(() =>
  import("@/features/kubernetes/deploy/kube-deploy-tab").then((m) => ({ default: m.KubeDeployTab })),
)
const ReadinessTab = lazy(() => import("@/features/readiness/readiness-tab").then((m) => ({ default: m.ReadinessTab })))
const KubeEnvironmentsTab = lazy(() =>
  import("@/features/kubernetes/environments/environments-tab").then((m) => ({ default: m.KubeEnvironmentsTab })),
)
const ChallengeTab = lazy(() => import("@/features/challenges/challenge-tab").then((m) => ({ default: m.ChallengeTab })))
const TAB_FALLBACK = (
  <div className="flex h-full items-center justify-center">
    <Spinner />
  </div>
)

const TABS: { id: BottomTab; label: string; icon: typeof Code2; pixel: PixelIconName }[] = [
  { id: "challenge", label: "Challenge", icon: Trophy, pixel: "check" },
  { id: "terraform", label: "Code", icon: Code2, pixel: "code" },
  { id: "variables", label: "Variables & Environments", icon: SlidersHorizontal, pixel: "sliders" },
  { id: "environments", label: "Environments", icon: Layers, pixel: "sliders" },
  { id: "deploy", label: "Deploy", icon: Rocket, pixel: "rocket" },
  { id: "endpoints", label: "Endpoints", icon: Plug, pixel: "plug" },
  { id: "loadtest", label: "Load test", icon: Gauge, pixel: "gauge" },
  { id: "problems", label: "Problems", icon: TriangleAlert, pixel: "warning" },
  { id: "readiness", label: "Readiness", icon: ClipboardCheck, pixel: "check" },
]

/**
 * Docker and Kubernetes workspaces have no Terraform variables: their environments are files (compose override
 * files and .env.<env>; Kustomize overlays and Helm values files), managed in the Environments tab.
 */
const CONTAINER_HIDDEN: Partial<Record<BottomTab, true>> = { variables: true }

/** Tabs only container workspaces (docker, Kubernetes) have: their environments and production readiness. */
const CONTAINER_ONLY: Partial<Record<BottomTab, true>> = { environments: true, readiness: true }

/**
 * Bottom dock with the code (file viewer), variables, deploy, endpoints, load test and
 * problems tabs. The tab strip is always rendered; the content only while open.
 * Ctrl/Cmd+J toggles it.
 */
export function BottomPanel() {
  const workflowId = useWorkflowStore((s) => s.workflow?.id)
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const containers = kind === "docker" || kind === "kubernetes"
  const problems = useWorkflowStore((s) => s.workflow?.diagnostics) ?? []
  const tab = useWorkflowStore((s) => s.bottomTab)
  const open = useWorkflowStore((s) => s.bottomOpen)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const setBottomOpen = useWorkflowStore((s) => s.setBottomOpen)
  const errorCount = problems.filter((d) => d.severity === "error").length
  // Info (mostly best-practice suggestions) is listed in the tab but not counted, like the node badges.
  const alertCount = problems.filter((d) => d.severity !== "info").length

  const challenge = useWorkflowStore((s) => !!s.workflow?.challenge)
  // The Challenge tab only exists in workflows started from a Learn challenge.
  const tabs = (containers ? TABS.filter((t) => !CONTAINER_HIDDEN[t.id]) : TABS.filter((t) => !CONTAINER_ONLY[t.id])).filter(
    (t) => challenge || t.id !== "challenge",
  )

  useDeployToast()
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "j") {
        e.preventDefault()
        const s = useWorkflowStore.getState()
        s.setBottomOpen(!s.bottomOpen)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])

  // A workspace opened while a tab it doesn't have was active (Terraform-only, container-only): show its code instead.
  useEffect(() => {
    if ((containers && CONTAINER_HIDDEN[tab]) || (!containers && CONTAINER_ONLY[tab]) || (!challenge && tab === "challenge"))
      useWorkflowStore.setState({ bottomTab: "terraform" })
  }, [containers, challenge, tab])

  const [stripRef, compact] = useCompactStrip(`${tabs.map((t) => t.id).join()}|${alertCount > 0}`)

  if (!workflowId) return null

  return (
    <Tabs
      value={tab}
      onValueChange={(v) => showBottom(v as BottomTab)}
      className="flex h-full min-h-0 flex-col gap-0 bg-background"
    >
      {/* When the names don't fit (narrow panel), the tabs show only their icons, the active one keeps its name (the
          others keep it as accessible text and a tooltip); a strip that still doesn't fit scrolls sideways. */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-2">
        <TabsList
          ref={stripRef}
          variant="line"
          className="h-full min-w-0 flex-1 justify-start gap-0 overflow-x-auto overflow-y-hidden [scrollbar-width:none]"
        >
          {tabs.map(({ id, label, icon, pixel }) => (
            <StripTab key={id} id={id} label={label} active={id === tab} iconOnly={compact && id !== tab} onSelect={() => !open && showBottom(id)}>
              <ThemedIcon icon={icon} pixel={pixel} />
              <span className={cn(compact && id !== tab && "sr-only")}>{label}</span>
              {id === "problems" && alertCount > 0 && (
                <Badge
                  variant={errorCount > 0 ? "destructive" : "secondary"}
                  className="h-4 min-w-4 px-1 text-[10px] tabular-nums"
                >
                  {alertCount}
                </Badge>
              )}
            </StripTab>
          ))}
        </TabsList>
        <div className="ml-auto flex items-center">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={open ? "Collapse panel" : "Expand panel"}
                onClick={() => setBottomOpen(!open)}
              >
                {open ? <ChevronDown /> : <ChevronUp />}
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              {open ? "Collapse" : "Expand"} panel <Kbd>⌘J</Kbd>
            </TooltipContent>
          </Tooltip>
        </div>
      </div>
      {tabs.map(({ id }) => (
        <TabsContent
          key={id}
          value={id}
          forceMount
          // The panel is the container: tabs lay themselves out by its width (`@3xl:` …), not the window's.
          className={cn("@container min-h-0 flex-1 overflow-hidden data-[state=inactive]:hidden", !open && "hidden")}
        >
          {/* Keyed by workflow so edit buffers and wizard state never leak into another workflow. */}
          <TabBody key={workflowId} tab={id} kind={kind ?? ""} />
        </TabsContent>
      ))}
    </Tabs>
  )
}

/**
 * A bottom tab, named by a tooltip while it shows only its icon. The tooltip hangs on a wrapper so its data-state
 * doesn't replace the tab's.
 */
function StripTab({
  id,
  label,
  active,
  iconOnly,
  onSelect,
  children,
}: {
  id: BottomTab
  label: string
  active: boolean
  iconOnly: boolean
  onSelect: () => void
  children: ReactNode
}) {
  const [tip, setTip] = useState(false)
  const ref = useRef<HTMLButtonElement>(null)
  // A tab opened from elsewhere (palette, canvas, toast) scrolls into a strip too narrow to show them all.
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [active])
  return (
    <Tooltip open={iconOnly && tip} onOpenChange={setTip}>
      <TooltipTrigger asChild>
        <span className="flex h-full shrink-0">
          <TabsTrigger ref={ref} value={id} className="flex-none px-2.5 text-xs" onClick={onSelect}>
            {children}
          </TabsTrigger>
        </span>
      </TooltipTrigger>
      <TooltipContent side="top">{label}</TooltipContent>
    </Tooltip>
  )
}

/**
 * Whether the tab strip is compact (icons only): it turns compact when its tabs overflow with their names and back
 * once it is as wide as they needed. `layout` names what changes that width (which tabs, the problems badge); a new
 * one measures again from the full names.
 */
function useCompactStrip(layout: string) {
  const ref = useRef<HTMLDivElement>(null)
  const [compactFor, setCompactFor] = useState<{ layout: string; needed: number } | null>(null)
  const compact = compactFor?.layout === layout
  useLayoutEffect(() => {
    const list = ref.current
    if (!list) return
    const check = () => {
      if (compact) {
        if (list.clientWidth >= compactFor.needed) setCompactFor(null)
      } else if (list.scrollWidth > list.clientWidth + 1) setCompactFor({ layout, needed: list.scrollWidth })
    }
    check()
    const observer = new ResizeObserver(check)
    observer.observe(list)
    return () => observer.disconnect()
  }, [compact, compactFor, layout])
  return [ref, compact] as const
}

/** Memoized so switching tabs or collapsing the panel doesn't re-render every (always mounted) tab. */
const TabBody = memo(function TabBody({ tab, kind }: { tab: BottomTab; kind: string }) {
  switch (tab) {
    case "terraform":
      return <FilesTab />
    case "variables":
      return <VariablesTab />
    case "deploy":
      return kind === "docker" ? (
        <Suspense fallback={TAB_FALLBACK}>
          <ComposeDeployTab />
        </Suspense>
      ) : kind === "kubernetes" ? (
        <Suspense fallback={TAB_FALLBACK}>
          <KubeDeployTab />
        </Suspense>
      ) : (
        <DeployTab />
      )
    case "endpoints":
      return (
        <Suspense fallback={TAB_FALLBACK}>
          <EndpointsTab />
        </Suspense>
      )
    case "loadtest":
      return (
        <Suspense fallback={TAB_FALLBACK}>
          <LoadTestTab />
        </Suspense>
      )
    case "problems":
      return <ProblemsTab />
    case "challenge":
      return (
        <Suspense fallback={TAB_FALLBACK}>
          <ChallengeTab />
        </Suspense>
      )
    case "readiness":
      return (
        <Suspense fallback={TAB_FALLBACK}>
          <ReadinessTab />
        </Suspense>
      )
    case "environments":
      // One tab for both container kinds: the same API, kind-aware copy inside.
      return kind === "kubernetes" || kind === "docker" ? (
        <Suspense fallback={TAB_FALLBACK}>
          <KubeEnvironmentsTab />
        </Suspense>
      ) : null
  }
})
