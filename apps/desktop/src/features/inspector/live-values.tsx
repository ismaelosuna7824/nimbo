import { CheckIcon, CopyIcon } from "lucide-react"
import { useState } from "react"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import type { DeployedResource } from "@/lib/api/types"

const KEYS = [
  "arn",
  "id",
  "name",
  "url",
  "invoke_url",
  "function_url",
  "queue_url",
  "endpoint",
  "api_endpoint",
  "invoke_arn",
  "bucket",
  "bucket_domain_name",
  "dns_name",
  "address",
  "ip_address",
  "self_link",
]

function CopyValue({ value }: { value: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <button
      type="button"
      className="shrink-0 text-muted-foreground hover:text-foreground"
      aria-label="Copy"
      onClick={() => {
        void navigator.clipboard.writeText(value)
        setCopied(true)
        window.setTimeout(() => setCopied(false), 1200)
      }}
    >
      {copied ? <CheckIcon className="size-3.5" /> : <CopyIcon className="size-3.5" />}
    </button>
  )
}

export function LiveValues({ resources }: { resources: DeployedResource[] }) {
  return (
    <div className="flex flex-col gap-3">
      {resources.map((r) => {
        const highlights = KEYS.flatMap((k) => {
          const v = r.values[k]
          return typeof v === "string" || typeof v === "number" ? [[k, String(v)] as const] : []
        }).filter(([, v]) => v !== "")
        return (
          <div key={r.address} className="flex flex-col gap-1.5">
            {resources.length > 1 && <span className="font-mono text-[11px] text-muted-foreground">{r.address}</span>}
            {highlights.map(([k, v]) => (
              <div key={k} className="flex items-center gap-2 rounded-md bg-muted/50 px-2 py-1">
                <span className="w-20 shrink-0 font-mono text-[11px] text-muted-foreground">{k}</span>
                <span className="min-w-0 flex-1 truncate font-mono text-xs" title={v}>
                  {v}
                </span>
                <CopyValue value={v} />
              </div>
            ))}
            <Collapsible>
              <CollapsibleTrigger className="text-[11px] text-muted-foreground hover:text-foreground">
                All attributes ▾
              </CollapsibleTrigger>
              <CollapsibleContent>
                <pre className="mt-1 max-h-72 overflow-auto rounded-md bg-muted/50 p-2 font-mono text-[11px]">
                  {JSON.stringify(r.values, null, 2)}
                </pre>
              </CollapsibleContent>
            </Collapsible>
          </div>
        )
      })}
    </div>
  )
}
