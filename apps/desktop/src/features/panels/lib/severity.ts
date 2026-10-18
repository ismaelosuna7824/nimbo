import { CircleAlert, Info, TriangleAlert } from "lucide-react"
import type { Severity } from "@/lib/api/types"

export const SEVERITY_META: Record<Severity, { label: string; icon: typeof Info; className: string }> = {
  error: { label: "Errors", icon: CircleAlert, className: "text-destructive" },
  warning: { label: "Warnings", icon: TriangleAlert, className: "text-amber-600 dark:text-amber-400" },
  info: { label: "Info", icon: Info, className: "text-sky-600 dark:text-sky-400" },
}
