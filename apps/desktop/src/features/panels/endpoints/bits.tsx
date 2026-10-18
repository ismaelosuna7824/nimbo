import { useState } from "react"
import { CheckIcon, CopyIcon, Eye, EyeOff, Info } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { AccessField } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { copyText } from "../lib/format"

/**
 * Explanations for cloud jargon shown next to field labels when the server
 * did not send a hint of its own.
 */
const GLOSSARY: Record<string, string> = {
  arn: "Amazon Resource Name: the unique ID AWS gives every resource. Some SDK calls and IAM policies ask for it.",
  region:
    "The AWS data-center location your SDK talks to. In the sandbox any value works, but it must match what the resources were created with.",
  "queue url": "The address SDKs and the AWS CLI use to send messages to and read messages from this queue.",
  endpoint: "The base address of your local cloud. Point your SDK or CLI here instead of the real AWS.",
  "access key id": "Like a username for AWS APIs. The sandbox accepts these test values; they never reach real AWS.",
  "secret access key": "Like a password for AWS APIs. The sandbox accepts these test values; they never reach real AWS.",
  "invoke url": "The address that runs this code when you send an HTTP request to it.",
  "function name": "The name you pass to the SDK or CLI to run this function.",
  "table name": "The name you pass to the SDK or CLI to read or write items in this table.",
  "bucket name": "The name you pass to the SDK or CLI to upload or download files.",
  "topic arn": "The ID you pass to the SDK or CLI to publish a message to this topic.",
}

export function CopyButton({
  value,
  label = "Copy",
  what,
  className,
}: {
  value: string
  label?: string
  /** Toast text, e.g. "URL copied". */
  what?: string
  className?: string
}) {
  const [copied, setCopied] = useState(false)
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          aria-label={label}
          className={cn("text-muted-foreground hover:text-foreground", className)}
          onClick={(e) => {
            e.stopPropagation()
            copyText(value).then(
              () => {
                setCopied(true)
                window.setTimeout(() => setCopied(false), 1200)
                if (what) toast.success(what)
              },
              () => toast.error("Couldn't copy to the clipboard"),
            )
          }}
        >
          {copied ? <CheckIcon className="text-emerald-600 dark:text-emerald-400" /> : <CopyIcon />}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{copied ? "Copied" : label}</TooltipContent>
    </Tooltip>
  )
}

export function HintIcon({ text }: { text: string }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button type="button" className="inline-flex text-muted-foreground hover:text-foreground" aria-label={text}>
          <Info className="size-3" />
        </button>
      </TooltipTrigger>
      <TooltipContent className="max-w-64">{text}</TooltipContent>
    </Tooltip>
  )
}

/** One label/value row with copy, an optional explanation and masking for secrets. */
export function FieldRow({ field, compact }: { field: AccessField; compact?: boolean }) {
  const [revealed, setRevealed] = useState(false)
  const hint = field.hint || GLOSSARY[field.label.toLowerCase()]
  const masked = field.secret && !revealed
  return (
    <div className="flex min-w-0 items-center gap-2 rounded-md bg-muted/50 py-0.5 pr-0.5 pl-2">
      <span
        className={cn(
          "flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground",
          compact ? "w-24" : "w-32",
        )}
      >
        <span className="truncate" title={field.label}>
          {field.label}
        </span>
        {hint && <HintIcon text={hint} />}
      </span>
      <span className="min-w-0 flex-1 truncate font-mono text-xs select-all" title={masked ? undefined : field.value}>
        {masked ? "•".repeat(Math.min(field.value.length, 12)) : field.value}
      </span>
      {field.secret && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          className="text-muted-foreground"
          aria-label={revealed ? "Hide value" : "Show value"}
          onClick={() => setRevealed((r) => !r)}
        >
          {revealed ? <EyeOff /> : <Eye />}
        </Button>
      )}
      <CopyButton value={field.value} label={`Copy ${field.label}`} />
    </div>
  )
}

const METHOD_CLASS: Record<string, string> = {
  GET: "bg-emerald-500/12 text-emerald-700 dark:text-emerald-400",
  POST: "bg-sky-500/12 text-sky-700 dark:text-sky-400",
  PUT: "bg-amber-500/12 text-amber-700 dark:text-amber-400",
  PATCH: "bg-violet-500/12 text-violet-700 dark:text-violet-400",
  DELETE: "bg-rose-500/12 text-rose-700 dark:text-rose-400",
}

export function MethodBadge({ method, className }: { method: string; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-4.5 min-w-11 shrink-0 items-center justify-center rounded px-1 font-mono text-[10px] font-semibold uppercase",
        METHOD_CLASS[method.toUpperCase()] ?? "bg-muted text-muted-foreground",
        className,
      )}
    >
      {method}
    </span>
  )
}
