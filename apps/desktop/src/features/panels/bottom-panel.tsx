import { useEffect } from "react"
import { ChevronDown, ChevronUp, Code2, Gauge, Plug, Rocket, SlidersHorizontal, TriangleAlert } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useWorkflowStore, type BottomTab } from "@/state/workflow-store"
import { DeployTab } from "./deploy/deploy-tab"
import { useDeployToast } from "./endpoints/deploy-toast"
import { EndpointsTab } from "./endpoints/endpoints-tab"
import { LoadTestTab } from "./loadtest/loadtest-tab"
import { ProblemsTab } from "./problems/problems-tab"
import { TerraformTab } from "./terraform/terraform-tab"
import { VariablesTab } from "./variables/variables-tab"

const TABS: { id: BottomTab; label: string; icon: typeof Code2 }[] = [
  { id: "terraform", label: "Terraform", icon: Code2 },
  { id: "variables", label: "Variables & Environments", icon: SlidersHorizontal },
  { id: "deploy", label: "Deploy", icon: Rocket },
  { id: "endpoints", label: "Endpoints", icon: Plug },
  { id: "loadtest", label: "Load test", icon: Gauge },
  { id: "problems", label: "Problems", icon: TriangleAlert },
]

/**
 * Bottom dock with the Terraform, variables, deploy, endpoints, load test and
 * problems tabs. The tab strip is always rendered; the content only while open.
 * Ctrl/Cmd+J toggles it.
 */
export function BottomPanel() {
  const workflow = useWorkflowStore((s) => s.workflow)
  const tab = useWorkflowStore((s) => s.bottomTab)
  const open = useWorkflowStore((s) => s.bottomOpen)
  const showBottom = useWorkflowStore((s) => s.showBottom)
  const setBottomOpen = useWorkflowStore((s) => s.setBottomOpen)
  const problems = workflow?.diagnostics ?? []
  const errorCount = problems.filter((d) => d.severity === "error").length

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

  if (!workflow) return null

  return (
    <Tabs
      value={tab}
      onValueChange={(v) => showBottom(v as BottomTab)}
      className="flex h-full min-h-0 flex-col gap-0 bg-background"
    >
      <div className="flex h-9 shrink-0 items-center gap-2 border-b px-2">
        <TabsList variant="line" className="h-full gap-0">
          {TABS.map(({ id, label, icon: Icon }) => (
            <TabsTrigger
              key={id}
              value={id}
              className="px-2.5 text-xs"
              onClick={() => {
                if (!open) showBottom(id)
              }}
            >
              <Icon />
              {label}
              {id === "problems" && problems.length > 0 && (
                <Badge
                  variant={errorCount > 0 ? "destructive" : "secondary"}
                  className="h-4 min-w-4 px-1 text-[10px] tabular-nums"
                >
                  {problems.length}
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
      {TABS.map(({ id }) => (
        <TabsContent
          key={id}
          value={id}
          forceMount
          className={cn("min-h-0 flex-1 overflow-hidden data-[state=inactive]:hidden", !open && "hidden")}
        >
          {/* Keyed by workflow so edit buffers and wizard state never leak into another workflow. */}
          <TabBody key={workflow.id} tab={id} />
        </TabsContent>
      ))}
    </Tabs>
  )
}

function TabBody({ tab }: { tab: BottomTab }) {
  switch (tab) {
    case "terraform":
      return <TerraformTab />
    case "variables":
      return <VariablesTab />
    case "deploy":
      return <DeployTab />
    case "endpoints":
      return <EndpointsTab />
    case "loadtest":
      return <LoadTestTab />
    case "problems":
      return <ProblemsTab />
  }
}
