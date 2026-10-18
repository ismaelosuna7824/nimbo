import { useMutation, useQueryClient } from "@tanstack/react-query"
import { GraduationCapIcon, MoonIcon, SunIcon, TerminalSquareIcon } from "lucide-react"
import { useTheme } from "next-themes"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api/client"
import type { Settings } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore, type UserMode } from "@/state/workflow-store"
import { Row, Section } from "./common"
import { SETTINGS_KEY, useSettings } from "./store"

const MODES: { value: UserMode; title: string; description: string; icon: typeof GraduationCapIcon }[] = [
  {
    value: "guided",
    title: "Guided",
    description: "Typed forms, plain-language labels and the most common options first. Best when you are learning.",
    icon: GraduationCapIcon,
  },
  {
    value: "pro",
    title: "Pro",
    description: "Every attribute and raw Terraform expressions up front. For people who already know Terraform.",
    icon: TerminalSquareIcon,
  },
]

export function GeneralSection() {
  const settings = useSettings()
  const queryClient = useQueryClient()
  const mode = useWorkflowStore((s) => s.mode)
  const setMode = useWorkflowStore((s) => s.setMode)
  const { resolvedTheme, setTheme } = useTheme()

  const save = useMutation({
    mutationFn: (s: Settings) => api.saveSettings(s),
    onSuccess: (s) => queryClient.setQueryData(SETTINGS_KEY, s),
    onError: (err) => toast.error("Couldn’t save the default mode", { description: errorMessage(err) }),
  })

  const chooseMode = (m: UserMode) => {
    setMode(m)
    if (settings.data && settings.data.defaultMode !== m) save.mutate({ ...settings.data, defaultMode: m })
  }

  return (
    <Section title="General" description="How Nimbo looks and how much detail it shows.">
      <div>
        <Row label="Mode" description="Switch anytime from the top bar; this choice is also the default for new sessions.">
          {settings.isPending ? (
            <Skeleton className="h-20" />
          ) : (
            <div role="radiogroup" aria-label="Mode" className="grid gap-2 sm:grid-cols-2">
              {MODES.map((m) => (
                <Choice key={m.value} selected={mode === m.value} onSelect={() => chooseMode(m.value)} icon={<m.icon />} title={m.title}>
                  {m.description}
                </Choice>
              ))}
            </div>
          )}
        </Row>
        <Row label="Theme">
          <div role="radiogroup" aria-label="Theme" className="grid max-w-sm grid-cols-2 gap-2">
            <Choice selected={resolvedTheme === "light"} onSelect={() => setTheme("light")} icon={<SunIcon />} title="Light" />
            <Choice selected={resolvedTheme !== "light"} onSelect={() => setTheme("dark")} icon={<MoonIcon />} title="Dark" />
          </div>
        </Row>
      </div>
    </Section>
  )
}

function Choice({
  selected,
  onSelect,
  icon,
  title,
  children,
}: {
  selected: boolean
  onSelect: () => void
  icon: React.ReactNode
  title: string
  children?: React.ReactNode
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onSelect}
      className={cn(
        "flex flex-col gap-1 rounded-lg border p-3 text-left transition-colors outline-none hover:bg-muted/50 focus-visible:ring-[3px] focus-visible:ring-ring/50",
        selected && "border-primary bg-primary/5 hover:bg-primary/10",
      )}
    >
      <span className="flex items-center gap-2 text-sm font-medium [&>svg]:size-4">
        {icon}
        {title}
      </span>
      {children && <span className="text-xs text-muted-foreground">{children}</span>}
    </button>
  )
}
