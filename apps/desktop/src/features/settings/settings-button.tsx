import { SettingsIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Kbd } from "@/components/ui/kbd"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { MOD_KEY } from "../toolbar/actions"
import { useSettingsView } from "./store"

export function SettingsButton() {
  const openView = useSettingsView((s) => s.openView)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon-sm" onClick={() => openView()} aria-label="Settings">
          <SettingsIcon />
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        Settings <Kbd>{MOD_KEY} ,</Kbd>
      </TooltipContent>
    </Tooltip>
  )
}
