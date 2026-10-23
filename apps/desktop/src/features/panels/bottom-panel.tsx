import { lazy, memo, Suspense, useEffect } from "react"
import { ChevronDown, ChevronUp, ClipboardCheck, Code2, Gauge, Layers, Plug, Rocket, SlidersHorizontal, TriangleAlert } from "lucide-react"
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
const ReadinessTab = lazy(() =>
  import("@/features/kubernetes/readiness/readiness-tab").then((m) => ({ default: m.ReadinessTab })),
)
const KubeEnvironmentsTab = lazy(() =>
  import("@/features/kubernetes/environments/environments-tab").then((m) => ({ default: m.KubeEnvironmentsTab })),
)
const TAB_FALLBACK = (
  <div className="flex h-full items-center justify-center">
    <Spinner />
  </div>
)

const TABS: { id: BottomTab; label: string; icon: typeof Code2; pixel: PixelIconName }[] = [
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

/** Tabs only container workspaces (docker, Kubernetes) have. */
const CONTAINER_ONLY: Partial<Record<BottomTab, true>> = { environments: true }

/** Tabs only Kubernetes workspaces have: production readiness per environment. */
const KUBE_ONLY: Partial<Record<BottomTab, true>> = { readiness: true }

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

  const tabs = (containers ? TABS.filter((t) => !CONTAINER_HIDDEN[t.id]) : TABS.filter((t) => !CONTAINER_ONLY[t.id])).filter(
    (t) => kind === "kubernetes" || !KUBE_ONLY[t.id],
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

  // A workspace opened while a tab it doesn't have was active (Terraform-only, Kubernetes-only): show its code instead.
  useEffect(() => {
    if ((containers && CONTAINER_HIDDEN[tab]) || (!containers && CONTAINER_ONLY[tab]) || (kind !== "kubernetes" && KUBE_ONLY[tab]))
      useWorkflowStore.setState({ bottomTab: "terraform" })
  }, [containers, kind, tab])

  if (!workflowId) return null

  return (
    <Tabs
      value={tab}
      onValueChange={(v) => showBottom(v as BottomTab)}
      className="flex h-full min-h-0 flex-col gap-0 bg-background"
    >
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-2">
        <TabsList variant="line" className="h-full gap-0">
          {tabs.map(({ id, label, icon, pixel }) => (
            <TabsTrigger
              key={id}
              value={id}
              className="px-2.5 text-xs"
              onClick={() => {
                if (!open) showBottom(id)
              }}
            >
              <ThemedIcon icon={icon} pixel={pixel} />
              {label}
              {id === "problems" && alertCount > 0 && (
                <Badge
                  variant={errorCount > 0 ? "destructive" : "secondary"}
                  className="h-4 min-w-4 px-1 text-[10px] tabular-nums"
                >
                  {alertCount}
                </Badge>
              )}
            </TabsTrigger>
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
          className={cn("min-h-0 flex-1 overflow-hidden data-[state=inactive]:hidden", !open && "hidden")}
        >
          {/* Keyed by workflow so edit buffers and wizard state never leak into another workflow. */}
          <TabBody key={workflowId} tab={id} kind={kind ?? ""} />
        </TabsContent>
      ))}
    </Tabs>
  )
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
