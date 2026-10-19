import { useEffect } from "react"
import { ArrowLeft, BotIcon, BoxesIcon, DatabaseIcon, InfoIcon, ListChecksIcon, ShieldCheckIcon, SlidersHorizontalIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import { openSetupChecklist } from "@/features/onboarding/store"
import { AboutSection } from "@/features/updates/about-section"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { AgentsSection } from "./agents-section"
import { DataSection } from "./data-section"
import { GeneralSection } from "./general-section"
import { PracticesSection } from "./practices-section"
import { SandboxSection } from "./sandbox-section"
import { useSettingsView, type SettingsSection } from "./store"

const SECTIONS: { id: SettingsSection; label: string; icon: typeof BotIcon }[] = [
  { id: "general", label: "General", icon: SlidersHorizontalIcon },
  { id: "sandbox", label: "Sandbox & emulators", icon: BoxesIcon },
  { id: "practices", label: "Best-practice checks", icon: ShieldCheckIcon },
  { id: "agents", label: "AI agents (MCP)", icon: BotIcon },
  { id: "data", label: "Data", icon: DatabaseIcon },
  { id: "about", label: "About", icon: InfoIcon },
]

/** The full-page Settings view; loaded on first open by SettingsHost. */
export function SettingsView() {
  const section = useSettingsView((s) => s.section)
  const setSection = useSettingsView((s) => s.setSection)
  const closeView = useSettingsView((s) => s.closeView)
  const workflowName = useWorkflowStore((s) => s.workflow?.name)

  // Opening or closing a workflow (palette) navigates away from this page.
  useEffect(
    () =>
      useWorkflowStore.subscribe((s, prev) => {
        if (s.workflow?.id !== prev.workflow?.id) closeView()
      }),
    [closeView],
  )

  return (
    <div className="fixed inset-0 z-40 flex flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button size="sm" variant="ghost" onClick={closeView}>
          <ArrowLeft /> {workflowName ? `Back to ${workflowName}` : "Home"}
        </Button>
        <h1 className="ml-1 font-heading text-sm font-semibold">Settings</h1>
      </header>
      <div className="grid min-h-0 flex-1 grid-cols-[15rem_minmax(0,1fr)]">
        <nav className="flex flex-col gap-0.5 border-r p-2" aria-label="Settings sections">
          {SECTIONS.map((s) => (
            <button
              key={s.id}
              type="button"
              aria-current={section === s.id ? "page" : undefined}
              onClick={() => setSection(s.id)}
              className={cn(
                "flex items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground [&>svg]:size-4",
                section === s.id && "bg-muted font-medium text-foreground",
              )}
            >
              <s.icon />
              {s.label}
            </button>
          ))}
          <Separator className="my-2" />
          <button
            type="button"
            onClick={openSetupChecklist}
            className="flex items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground [&>svg]:size-4"
          >
            <ListChecksIcon />
            Setup checklist
          </button>
        </nav>
        <main className="min-h-0 overflow-y-auto">
          <div className="mx-auto max-w-4xl px-8 py-8">
            {section === "general" && <GeneralSection />}
            {section === "sandbox" && <SandboxSection />}
            {section === "practices" && <PracticesSection />}
            {section === "agents" && <AgentsSection />}
            {section === "data" && <DataSection />}
            {section === "about" && <AboutSection />}
          </div>
        </main>
      </div>
    </div>
  )
}
