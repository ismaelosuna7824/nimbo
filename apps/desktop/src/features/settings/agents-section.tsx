import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { AlertTriangleIcon, CheckCircle2Icon, ChevronDownIcon, MessageSquareTextIcon, PlugIcon, Trash2Icon, WrenchIcon } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { api } from "@/lib/api/client"
import type { AgentClient, McpCatalog, McpToolInfo } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { CodeBlock, CopyButton, Section } from "./common"
import { AGENTS_KEY } from "./store"

const EXAMPLES = [
  "Create a workflow with an HTTP API, a Lambda and a DynamoDB table on AWS",
  "Add an SQS queue to the open workflow and trigger the Lambda from it",
  "Deploy it and run a smoke load test",
  "Import ~/code/infra and tell me what it creates",
]

export function AgentsSection() {
  const queryClient = useQueryClient()
  const agents = useQuery({ queryKey: AGENTS_KEY, queryFn: api.agents, retry: false })
  const command = useQuery({ queryKey: ["mcp", "command"], queryFn: api.mcpCommand, retry: false, staleTime: Infinity })
  const catalog = useQuery({ queryKey: ["mcp", "catalog"], queryFn: api.mcpCatalog, retry: false, staleTime: Infinity })

  const commandLine = command.data ? [quote(command.data.command), ...command.data.args].join(" ") : ""
  const problem = command.data?.problem

  const list = agents.data ?? []
  // Detected apps first, keeping the server's order otherwise.
  const sorted = [...list].sort((a, b) => Number(b.detected) - Number(a.detected))

  return (
    <div className="flex flex-col gap-10">
      <Section
        title="AI agents (MCP)"
        description={
          <>
            Let AI agents such as Claude, Cursor or Codex design, deploy and test infrastructure in Nimbo through the Model
            Context Protocol. <b className="font-medium text-foreground">Install</b> adds a <code className="font-mono text-xs">nimbo</code> server to
            the agent’s configuration; your other servers stay untouched and a backup of the file is saved next to it.
          </>
        }
      >
        <div className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-muted-foreground">Command agents run to reach Nimbo</span>
          {command.isPending ? <Skeleton className="h-9" /> : command.data && <CodeBlock code={commandLine} label="Copy command" />}
          <p className="text-xs text-muted-foreground">
            Nimbo must be open (or the daemon running) while the agent works; the agent sees the workflow open here as the current one.
          </p>
        </div>
        {problem && (
          <Alert variant="destructive">
            <AlertTriangleIcon />
            <AlertTitle>One-click install is unavailable</AlertTitle>
            <AlertDescription>{problem}</AlertDescription>
          </Alert>
        )}
        {agents.isError && <p className="text-sm text-destructive">Couldn’t look for AI agents: {errorMessage(agents.error)}</p>}
        <div className="grid gap-3 lg:grid-cols-2">
          {agents.isPending
            ? Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-36 rounded-xl" />)
            : sorted.map((a) => (
                <AgentCard
                  key={a.id}
                  agent={a}
                  installDisabled={!!problem}
                  onChange={(updated) =>
                    queryClient.setQueryData<AgentClient[]>(AGENTS_KEY, (old) => old?.map((x) => (x.id === updated.id ? updated : x)))
                  }
                />
              ))}
        </div>
      </Section>

      <Section title="What can agents do?" description="Once connected, ask in plain language. For example:">
        <ul className="grid gap-2 sm:grid-cols-2">
          {EXAMPLES.map((ex) => (
            <li key={ex} className="flex items-start gap-2 rounded-lg border bg-muted/30 px-3 py-2.5 text-sm">
              <MessageSquareTextIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1">“{ex}”</span>
              <CopyButton text={ex} label="Copy request" className="-my-1" />
            </li>
          ))}
        </ul>
        {catalog.isPending ? (
          <Skeleton className="h-40" />
        ) : catalog.isError ? (
          <p className="text-sm text-destructive">Couldn’t load the capability list: {errorMessage(catalog.error)}</p>
        ) : (
          catalog.data && <Capabilities catalog={catalog.data} />
        )}
      </Section>
    </div>
  )
}

function quote(s: string) {
  return /^[\w./:@+-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`
}

export function AgentCard({ agent: a, installDisabled, onChange }: { agent: AgentClient; installDisabled: boolean; onChange: (a: AgentClient) => void }) {
  const [manualOpen, setManualOpen] = useState(false)
  const install = useMutation({
    mutationFn: () => api.installAgent(a.id),
    onSuccess: (updated) => {
      onChange(updated)
      toast.success(`Nimbo added to ${a.name}`, { description: updated.note })
    },
    onError: (err) => toast.error(`Couldn’t set up ${a.name}`, { description: errorMessage(err) }),
  })
  const remove = useMutation({
    mutationFn: () => api.uninstallAgent(a.id),
    onSuccess: (updated) => {
      onChange(updated)
      toast.success(`Nimbo removed from ${a.name}`)
    },
    onError: (err) => toast.error(`Couldn’t remove Nimbo from ${a.name}`, { description: errorMessage(err) }),
  })
  const busy = install.isPending || remove.isPending

  return (
    <div className={cn("flex flex-col gap-3 rounded-xl border p-4", a.configured && "border-emerald-500/40")} data-agent={a.id}>
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-1.5">
            <h3 className="text-sm font-semibold">{a.name}</h3>
            {a.configured ? (
              <Badge className="bg-emerald-500/15 text-emerald-600 dark:text-emerald-400">
                <CheckCircle2Icon /> Connected
              </Badge>
            ) : a.detected ? (
              <Badge variant="secondary">Installed</Badge>
            ) : (
              <Badge variant="outline" className="text-muted-foreground">
                Not found
              </Badge>
            )}
          </div>
          <p className="mt-1 truncate font-mono text-[11px] text-muted-foreground" title={a.configPath}>
            {a.configPath}
          </p>
        </div>
      </div>
      {a.note && <p className="text-xs text-muted-foreground">{a.note}</p>}
      <div className="flex flex-wrap items-center gap-2">
        <Button
          size="sm"
          variant={a.configured ? "outline" : "default"}
          disabled={busy || installDisabled}
          onClick={() => install.mutate()}
          title={a.detected ? undefined : `${a.name} was not found on this machine; installing still writes its config file.`}
        >
          {install.isPending ? <Spinner /> : <PlugIcon />}
          {a.configured ? "Reinstall" : "Install"}
        </Button>
        {a.configured && (
          <Button size="sm" variant="ghost" disabled={busy} onClick={() => remove.mutate()}>
            {remove.isPending ? <Spinner /> : <Trash2Icon />} Remove
          </Button>
        )}
        <Collapsible open={manualOpen} onOpenChange={setManualOpen} className="contents">
          <CollapsibleTrigger asChild>
            <Button size="sm" variant="ghost" className="ml-auto text-muted-foreground" aria-expanded={manualOpen}>
              Manual setup <ChevronDownIcon className={cn("transition-transform", manualOpen && "rotate-180")} />
            </Button>
          </CollapsibleTrigger>
          <CollapsibleContent className="w-full">
            <p className="mb-1.5 text-xs text-muted-foreground">
              {a.snippet.startsWith("claude ") ? "Run in a terminal:" : "Add to"}{" "}
              {!a.snippet.startsWith("claude ") && <code className="font-mono">{a.configPath}</code>}
            </p>
            <CodeBlock code={a.snippet} label="Copy snippet" />
          </CollapsibleContent>
        </Collapsible>
      </div>
    </div>
  )
}

function Capabilities({ catalog }: { catalog: McpCatalog }) {
  const groups = new Map<string, McpToolInfo[]>()
  for (const t of catalog.tools) {
    const g = t.group || "Other"
    groups.set(g, [...(groups.get(g) ?? []), t])
  }
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <h3 className="flex items-center gap-2 text-sm font-semibold">
          <WrenchIcon className="size-4" /> Tools <span className="font-normal text-muted-foreground">({catalog.tools.length})</span>
        </h3>
        <div className="grid gap-3 md:grid-cols-2">
          {[...groups].map(([group, tools]) => (
            <div key={group} className="rounded-lg border">
              <div className="border-b bg-muted/30 px-3 py-1.5 text-xs font-medium">{group}</div>
              <ul className="divide-y">
                {tools.map((t) => (
                  <li key={t.name} className="px-3 py-2">
                    <div className="flex items-baseline gap-2">
                      <span className="text-sm">{t.title || t.name}</span>
                      <code className="truncate font-mono text-[11px] text-muted-foreground">{t.name}</code>
                    </div>
                    <p className="line-clamp-2 text-xs text-muted-foreground" title={t.description}>
                      {t.description}
                    </p>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>
      {catalog.prompts.length > 0 && (
        <div className="flex flex-col gap-3">
          <h3 className="flex items-center gap-2 text-sm font-semibold">
            <MessageSquareTextIcon className="size-4" /> Prompts
            <span className="font-normal text-muted-foreground">— ready-made conversations agents offer as slash commands</span>
          </h3>
          <ul className="grid gap-2 md:grid-cols-2">
            {catalog.prompts.map((p) => (
              <li key={p.name} className="rounded-lg border px-3 py-2">
                <div className="flex items-baseline gap-2">
                  <span className="text-sm">{p.title || p.name}</span>
                  <code className="font-mono text-[11px] text-muted-foreground">/{p.name}</code>
                </div>
                <p className="text-xs text-muted-foreground">{p.description}</p>
                {p.arguments && p.arguments.length > 0 && (
                  <p className="mt-1 text-[11px] text-muted-foreground">
                    Asks for: <span className="font-mono">{p.arguments.join(", ")}</span>
                  </p>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  )
}
