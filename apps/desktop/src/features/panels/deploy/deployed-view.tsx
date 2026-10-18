import { useMemo } from "react"
import { Copy, Lock } from "lucide-react"
import { toast } from "sonner"
import { Badge } from "@/components/ui/badge"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import type { DeployedResource } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { copyText, displayJSON } from "../lib/format"
import { DENSE_TABLE } from "../variables/blocks-editors"

/**
 * Attributes worth showing at a glance, in priority order. API Gateway
 * api_endpoint/invoke_url are omitted: in the sandbox they hold real-AWS URLs
 * that do not resolve.
 */
const KEY_ATTRS = [
  "url",
  "queue_url",
  "function_url",
  "endpoint",
  "arn",
  "id",
  "name",
  "bucket",
  "function_name",
  "domain_name",
]

function keyValues(r: DeployedResource): [string, string][] {
  const out: [string, string][] = []
  for (const k of KEY_ATTRS) {
    const v = r.values[k]
    if (v === null || v === undefined || v === "" || typeof v === "object") continue
    if (out.some(([, existing]) => existing === String(v))) continue
    out.push([k, String(v)])
    if (out.length === 3) break
  }
  return out
}

function CopyValue({ label, value }: { label?: string; value: string }) {
  return (
    <button
      type="button"
      title={`Copy ${label ?? "value"}`}
      onClick={(e) => {
        e.stopPropagation()
        void copyText(value).then(() => toast.success(`Copied ${label ?? "value"}`))
      }}
      className="group/copy flex max-w-full min-w-0 items-center gap-1 rounded px-1 text-left font-mono text-[11px] hover:bg-muted"
    >
      {label && <span className="shrink-0 text-muted-foreground">{label}</span>}
      <span className="truncate">{value}</span>
      <Copy className="size-3 shrink-0 opacity-0 group-hover/copy:opacity-60" />
    </button>
  )
}

export function DeployedResources() {
  const deployed = useWorkflowStore((s) => s.deployed)
  const select = useWorkflowStore((s) => s.select)
  const resources = useMemo(
    () => [...(deployed?.resources ?? [])].sort((a, b) => a.address.localeCompare(b.address)),
    [deployed],
  )

  if (resources.length === 0) {
    return (
      <p className="p-4 text-xs text-muted-foreground">
        Nothing is deployed yet. Click <b>Apply</b> to create the resources of your diagram in the sandbox.
      </p>
    )
  }

  return (
    <Table className={DENSE_TABLE}>
      <TableHeader>
        <TableRow>
          <TableHead className="w-[30%]">Address</TableHead>
          <TableHead className="w-[20%]">Type</TableHead>
          <TableHead>Key values</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {resources.map((r) => (
          <TableRow
            key={r.address}
            className={r.nodeId ? "cursor-pointer" : undefined}
            onClick={r.nodeId ? () => select(r.nodeId!) : undefined}
          >
            <TableCell className="max-w-0 truncate font-mono" title={r.address}>
              {r.mode === "data" && (
                <Badge variant="outline" className="mr-1 h-4 px-1 text-[10px]">
                  data
                </Badge>
              )}
              {r.address}
            </TableCell>
            <TableCell className="max-w-0 truncate font-mono text-muted-foreground">{r.type}</TableCell>
            <TableCell className="max-w-0">
              <div className="flex min-w-0 flex-wrap gap-x-2">
                {keyValues(r).map(([k, v]) => (
                  <CopyValue key={k} label={k} value={v} />
                ))}
              </div>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  )
}

export function DeployedOutputs() {
  const workflowOutputs = useWorkflowStore((s) => s.workflow?.outputs)
  const deployed = useWorkflowStore((s) => s.deployed)
  const outputs = Object.entries(deployed?.outputs ?? {})
  const sensitive = new Set(
    (workflowOutputs ?? [])
      .filter((o) => o.body.attrs.some((a) => a.name === "sensitive" && a.value.literal === true))
      .map((o) => o.name),
  )

  if (outputs.length === 0) {
    return (
      <p className="p-4 text-xs text-muted-foreground">
        No outputs yet. Define outputs in <b>Variables &amp; Environments → Outputs</b>, then apply.
      </p>
    )
  }

  return (
    <div className="divide-y">
      {outputs.map(([name, value]) => (
        <div key={name} className="grid grid-cols-[minmax(8rem,30%)_1fr] items-start gap-2 px-3 py-1.5 text-xs">
          <span className="flex items-center gap-1 truncate font-mono font-medium">
            {sensitive.has(name) && <Lock className="size-3 text-muted-foreground" />}
            {name}
          </span>
          <CopyValue value={displayJSON(value)} />
        </div>
      ))}
    </div>
  )
}
