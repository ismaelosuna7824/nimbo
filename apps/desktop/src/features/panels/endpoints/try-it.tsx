import { useState } from "react"
import { ChevronRight, Database, Play, Plus, Send, Timer, X } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Spinner } from "@/components/ui/spinner"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { errorMessage } from "@/components/errors"
import { api } from "@/lib/api/client"
import type { TryAction, TryResult } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { formatMs } from "../lib/format"
import { HTTP_METHODS } from "../loadtest/targets"
import { CopyButton } from "./bits"
import { CodeInput, HighlightedCode } from "./code"
import type { HeaderRow, HttpDraft } from "./model"

/**
 * "Try it" for one resource: runs its actions in the sandbox (or against a
 * Docker workspace's containers) through the daemon and shows the response.
 * HTTP actions get a small request builder.
 */
export function TryIt({
  workflowId,
  target,
  actions,
  http,
  setHttp,
  startsFunction,
}: {
  workflowId: string
  /** TryRequest.nodeId: node ID or resource address. */
  target: string
  actions: TryAction[]
  http: HttpDraft
  setHttp: (update: (d: HttpDraft) => HttpDraft) => void
  /** The action runs a function (Lambda): its first call has to start it. */
  startsFunction?: boolean
}) {
  const [actionId, setActionId] = useState(actions[0]?.id ?? "")
  const kind = useWorkflowStore((s) => s.workflow?.kind)
  const docker = kind === "docker"
  const kube = kind === "kubernetes"
  /** Edited inputs per action; untouched actions use their default. */
  const [inputs, setInputs] = useState<Record<string, string>>({})
  const [results, setResults] = useState<Record<string, TryResult>>({})
  const [running, setRunning] = useState<string | null>(null)
  const action = actions.find((a) => a.id === actionId) ?? actions[0]
  if (!action) return null

  const run = async () => {
    setRunning(action.id)
    try {
      const headers = Object.fromEntries(
        http.headers.filter((h) => h.name.trim()).map((h) => [h.name.trim(), h.value]),
      )
      const result = await api.tryEndpoint(
        workflowId,
        action.input === "http"
          ? {
              nodeId: target,
              action: action.id,
              method: http.method,
              path: http.url,
              headers,
              input: http.method !== "GET" && http.method !== "HEAD" ? http.body : undefined,
            }
          : {
              nodeId: target,
              action: action.id,
              input: action.input === "none" ? undefined : (inputs[action.id] ?? action.default ?? ""),
            },
      )
      setResults((r) => ({ ...r, [action.id]: result }))
    } catch (err) {
      setResults((r) => ({ ...r, [action.id]: { ok: false, latencyMs: 0, error: errorMessage(err) } }))
    } finally {
      setRunning(null)
    }
  }

  const result = results[action.id]
  const busy = running === action.id

  return (
    <div className="flex flex-col gap-2">
      {actions.length > 1 && (
        <ToggleGroup
          type="single"
          size="sm"
          variant="outline"
          value={action.id}
          onValueChange={(v) => v && setActionId(v)}
          className="flex-wrap"
        >
          {actions.map((a) => (
            <ToggleGroupItem key={a.id} value={a.id} className="text-xs">
              {a.label}
            </ToggleGroupItem>
          ))}
        </ToggleGroup>
      )}
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {actions.length === 1 && <span className="font-medium">{action.label}</span>}
        {action.description && <span className="text-muted-foreground">{action.description}</span>}
        {action.writes && (
          <Tooltip>
            <TooltipTrigger asChild>
              <Badge variant="outline" className="h-4.5 gap-1 border-amber-500/40 text-[10px] text-amber-700 dark:text-amber-400">
                <Database /> {kube ? "Adds data to your cluster" : docker ? "Adds data to your containers" : "Adds data to your sandbox"}
              </Badge>
            </TooltipTrigger>
            <TooltipContent className="max-w-60">
              {kube
                ? "Running this stores something in your local cluster. Destroy with “Also delete PVCs” starts clean."
                : docker
                  ? "Running this stores something in your local containers. Down with “Also delete volumes” starts clean."
                  : "Running this stores something in your local sandbox (never in your real cloud account). Reset the sandbox to start clean."}
            </TooltipContent>
          </Tooltip>
        )}
      </div>

      {action.input === "http" ? (
        <HttpBuilder http={http} setHttp={setHttp} onSend={() => void run()} busy={busy} />
      ) : (
        <div className="flex flex-col gap-1.5">
          {action.input !== "none" && (
            <CodeInput
              language={action.input === "json" ? "json" : "text"}
              value={inputs[action.id] ?? action.default ?? ""}
              onChange={(v) => setInputs((s) => ({ ...s, [action.id]: v }))}
              ariaLabel={`${action.label} input`}
              placeholder={action.input === "json" ? '{"hello": "world"}' : "Type a message…"}
            />
          )}
          <Button size="xs" className="self-start" disabled={busy} onClick={() => void run()}>
            {busy ? <Spinner /> : <Play />} {action.input === "none" ? action.label : "Run"}
          </Button>
        </div>
      )}

      {busy && startsFunction && Object.keys(results).length === 0 && (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <Timer className="size-3.5" /> First call starts the function (a few seconds)
        </p>
      )}
      {result && <ResultView result={result} />}
    </div>
  )
}

function HttpBuilder({
  http,
  setHttp,
  onSend,
  busy,
}: {
  http: HttpDraft
  setHttp: (update: (d: HttpDraft) => HttpDraft) => void
  onSend: () => void
  busy: boolean
}) {
  const setHeader = (i: number, patch: Partial<HeaderRow>) =>
    setHttp((d) => ({ ...d, headers: d.headers.map((h, j) => (j === i ? { ...h, ...patch } : h)) }))
  return (
    <form
      className="flex flex-col gap-1.5"
      onSubmit={(e) => {
        e.preventDefault()
        onSend()
      }}
    >
      <div className="flex gap-1">
        <Select value={http.method} onValueChange={(m) => setHttp((d) => ({ ...d, method: m }))}>
          <SelectTrigger size="sm" className="w-24 shrink-0 font-mono text-xs" aria-label="Method">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {[...HTTP_METHODS, "OPTIONS"].map((m) => (
              <SelectItem key={m} value={m} className="font-mono text-xs">
                {m}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Input
          className="h-7 min-w-0 flex-1 font-mono text-xs"
          value={http.url}
          onChange={(e) => setHttp((d) => ({ ...d, url: e.target.value }))}
          aria-label="Request URL"
          spellCheck={false}
        />
        <Button type="submit" size="sm" disabled={busy || !http.url.trim()}>
          {busy ? <Spinner /> : <Send />} Send
        </Button>
      </div>
      <Collapsible>
        <CollapsibleTrigger className="group/h flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
          <ChevronRight className="size-3 transition-transform group-data-[state=open]/h:rotate-90" />
          Headers <span className="tabular-nums">({http.headers.filter((h) => h.name.trim()).length})</span>
        </CollapsibleTrigger>
        <CollapsibleContent className="mt-1 flex flex-col gap-1">
          {http.headers.map((h, i) => (
            <div key={i} className="flex gap-1">
              <Input
                className="h-6 w-40 font-mono text-[11px]"
                placeholder="Header name"
                value={h.name}
                onChange={(e) => setHeader(i, { name: e.target.value })}
                aria-label="Header name"
              />
              <Input
                className="h-6 min-w-0 flex-1 font-mono text-[11px]"
                placeholder="Value"
                value={h.value}
                onChange={(e) => setHeader(i, { value: e.target.value })}
                aria-label="Header value"
              />
              <Button
                type="button"
                variant="ghost"
                size="icon-xs"
                aria-label="Remove header"
                onClick={() => setHttp((d) => ({ ...d, headers: d.headers.filter((_, j) => j !== i) }))}
              >
                <X />
              </Button>
            </div>
          ))}
          <Button
            type="button"
            variant="ghost"
            size="xs"
            className="self-start text-muted-foreground"
            onClick={() => setHttp((d) => ({ ...d, headers: [...d.headers, { name: "", value: "" }] }))}
          >
            <Plus /> Add header
          </Button>
        </CollapsibleContent>
      </Collapsible>
      {http.method !== "GET" && http.method !== "HEAD" && (
        <CodeInput
          language="json"
          value={http.body}
          onChange={(v) => setHttp((d) => ({ ...d, body: v }))}
          ariaLabel="Request body"
          placeholder='Request body, e.g. {"name": "hello"}'
        />
      )}
    </form>
  )
}

function statusTone(result: TryResult): string {
  const code = Number(/^(\d{3})\b/.exec(result.status ?? "")?.[1])
  if (!result.ok || code >= 500) return "bg-destructive/12 text-destructive"
  if (code >= 400) return "bg-amber-500/15 text-amber-700 dark:text-amber-400"
  if (code >= 300) return "bg-sky-500/15 text-sky-700 dark:text-sky-400"
  return "bg-emerald-500/15 text-emerald-700 dark:text-emerald-400"
}

function ResultView({ result }: { result: TryResult }) {
  const headers = Object.entries(result.headers ?? {})
  const body = result.body ?? ""
  const json = (result.contentType ?? "").includes("json") || /^\s*[[{]/.test(body)
  return (
    <div className="flex flex-col gap-1.5 rounded-md border bg-muted/20 p-2" aria-live="polite">
      <div className="flex items-center gap-2 text-xs">
        <span className="text-muted-foreground">Response</span>
        <span className={cn("rounded px-1.5 py-0.5 font-mono text-[11px] font-semibold", statusTone(result))}>
          {result.status || (result.ok ? "OK" : "Failed")}
        </span>
        {result.latencyMs > 0 && (
          <span className="inline-flex items-center gap-0.5 text-[11px] text-muted-foreground tabular-nums">
            <Timer className="size-3" /> {formatMs(result.latencyMs)}
          </span>
        )}
        {result.contentType && (
          <span className="truncate font-mono text-[10px] text-muted-foreground">{result.contentType}</span>
        )}
        {body && <CopyButton value={body} label="Copy response body" className="ml-auto" />}
      </div>
      {result.error && <p className="text-xs break-words text-destructive">{result.error}</p>}
      {headers.length > 0 && (
        <Collapsible>
          <CollapsibleTrigger className="group/rh flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground">
            <ChevronRight className="size-3 transition-transform group-data-[state=open]/rh:rotate-90" />
            Response headers ({headers.length})
          </CollapsibleTrigger>
          <CollapsibleContent>
            <dl className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 font-mono text-[10px]">
              {headers.map(([k, v]) => (
                <div key={k} className="contents">
                  <dt className="text-muted-foreground">{k}</dt>
                  <dd className="break-all">{v}</dd>
                </div>
              ))}
            </dl>
          </CollapsibleContent>
        </Collapsible>
      )}
      {body ? (
        <HighlightedCode
          code={body}
          language={json ? "json" : "text"}
          className="max-h-64 rounded bg-background p-2"
        />
      ) : (
        !result.error && <p className="text-[11px] text-muted-foreground">Empty response body.</p>
      )}
    </div>
  )
}
