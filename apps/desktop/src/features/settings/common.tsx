import { useState, type ReactNode } from "react"
import { CheckIcon, CopyIcon } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

/** Icon button that copies text and briefly shows a check mark. */
export function CopyButton({ text, label = "Copy", className }: { text: string; label?: string; className?: string }) {
  const [done, setDone] = useState(false)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon-xs"
          className={className}
          aria-label={label}
          onClick={() => {
            void navigator.clipboard.writeText(text).then(
              () => {
                setDone(true)
                window.setTimeout(() => setDone(false), 1500)
              },
              () => toast.error("Couldn’t copy to the clipboard"),
            )
          }}
        >
          {done ? <CheckIcon /> : <CopyIcon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{done ? "Copied" : label}</TooltipContent>
    </Tooltip>
  )
}

/** Monospace block with a copy button in its corner. */
export function CodeBlock({ code, label, className }: { code: string; label?: string; className?: string }) {
  return (
    <div className={cn("group relative rounded-md border bg-muted/40", className)}>
      <pre className="overflow-x-auto p-2.5 pr-9 font-mono text-xs leading-relaxed whitespace-pre">{code}</pre>
      <CopyButton text={code} label={label} className="absolute top-1.5 right-1.5" />
    </div>
  )
}

/** A titled settings section. */
export function Section({ title, description, children, action }: { title: string; description?: ReactNode; children: ReactNode; action?: ReactNode }) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h2 className="font-heading text-base font-semibold">{title}</h2>
          {description && <p className="mt-1 text-sm text-muted-foreground">{description}</p>}
        </div>
        {action}
      </div>
      {children}
    </section>
  )
}

/** Label + description on the left, control on the right. */
export function Row({ label, htmlFor, description, children }: { label: string; htmlFor?: string; description?: ReactNode; children: ReactNode }) {
  return (
    <div className="grid gap-2 border-b py-4 last:border-b-0 sm:grid-cols-[minmax(0,14rem)_minmax(0,1fr)] sm:gap-6">
      <div>
        <label htmlFor={htmlFor} className="text-sm font-medium">
          {label}
        </label>
        {description && <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>}
      </div>
      <div className="min-w-0">{children}</div>
    </div>
  )
}
