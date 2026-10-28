import { lazy, memo, Suspense, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react"
import {
  ChevronDown,
  ChevronUp,
  ClipboardCheck,
  Code2,
  Gauge,
  Layers,
  ListChecks,
  Plug,
  Rocket,
  ShieldCheck,
  SlidersHorizontal,
  Trophy,
} from "lucide-react"
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
import { usePlanToast } from "@/features/plan/plan-toast"
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
const ComplianceTab = lazy(() =>
  import("@/features/compliance/compliance-tab").then((m) => ({ default: m.ComplianceTab })),
)
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
  // A neutral icon: the tab is always there, so a warning sign on it would alarm even with nothing wrong. The count
  // badge next to the label carries the signal (red only for errors).
  { id: "problems", label: "Problems", icon: ListChecks, pixel: "list" },
  { id: "readiness", label: "Readiness", icon: ClipboardCheck, pixel: "check" },
  { id: "compliance", label: "Compliance", icon: ShieldCheck, pixel: "check" },
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
  usePlanToast()
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

  const strip = useStripOverflow(`${tabs.map((t) => t.id).join()}|${alertCount > 0}`)

  if (!workflowId) return null

  return (
    <Tabs
      value={tab}
      onValueChange={(v) => showBottom(v as BottomTab)}
      className="flex h-full min-h-0 flex-col gap-0 bg-background"
    >
      {/* Every tab keeps its name: when they do not fit, the strip scrolls sideways and a fade marks the hidden ones,
          instead of collapsing into bare icons on a wide, half-empty bar. */}
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-2">
        <div className="relative min-w-0 flex-1">
          <TabsList
            ref={strip.ref}
            onScroll={strip.onScroll}
            variant="line"
            className="h-full w-full justify-start gap-0 overflow-x-auto overflow-y-hidden [scrollbar-width:none]"
          >
            {tabs.map(({ id, label, icon, pixel }) => (
              <StripTab key={id} id={id} label={label} active={id === tab} onSelect={() => !open && showBottom(id)}>
                <ThemedIcon icon={icon} pixel={pixel} />
                <span>{label}</span>
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
          {strip.more.left && (
            <span aria-hidden className="pointer-events-none absolute inset-y-0 left-0 w-6 bg-gradient-to-r from-background to-transparent" />
          )}
          {strip.more.right && (
            <span aria-hidden className="pointer-events-none absolute inset-y-0 right-0 w-6 bg-gradient-to-l from-background to-transparent" />
          )}
        </div>
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
 * A bottom tab. Every tab shows its name; the strip scrolls instead of collapsing to icons, and the active tab
 * scrolls itself into view when it is opened from elsewhere (palette, canvas, toast).
 */
function StripTab({
  id,
  label,
  active,
  onSelect,
  children,
}: {
  id: BottomTab
  label: string
  active: boolean
  onSelect: () => void
  children: ReactNode
}) {
  const ref = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (active) ref.current?.scrollIntoView({ block: "nearest", inline: "nearest" })
  }, [active])
  return (
    <span className="flex h-full shrink-0" title={label}>
      <TabsTrigger ref={ref} value={id} className="flex-none px-2.5 text-xs" onClick={onSelect}>
        {children}
      </TabsTrigger>
    </span>
  )
}

/**
 * Whether the strip scrolls past either edge, so the fades can point at the hidden tabs. `layout` names what changes
 * the tab widths (which tabs exist, the problems badge); a resize or a scroll re-measures.
 */
function useStripOverflow(layout: string) {
  const ref = useRef<HTMLDivElement>(null)
  const [more, setMore] = useState({ left: false, right: false })
  const measure = () => {
    const list = ref.current
    if (!list) return
    const left = list.scrollLeft > 1
    const right = list.scrollLeft + list.clientWidth < list.scrollWidth - 1
    setMore((m) => (m.left === left && m.right === right ? m : { left, right }))
  }
  const onScroll = () => measure()
  useLayoutEffect(() => {
    const list = ref.current
    if (!list) return
    measure()
    const observer = new ResizeObserver(measure)
    observer.observe(list)
    return () => observer.disconnect()
  }, [layout])
  return { ref, more, onScroll } as const
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
    case "compliance":
      return (
        <Suspense fallback={TAB_FALLBACK}>
          <ComplianceTab />
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
