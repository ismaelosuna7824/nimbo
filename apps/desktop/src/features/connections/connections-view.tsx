import { useEffect, useState } from "react"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { ArrowLeft, Plus, ShieldAlert, Zap } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { errorMessage } from "@/components/errors"
import { ThemeToggle } from "@/components/theme-toggle"
import { api } from "@/lib/api/client"
import type { Connection } from "@/lib/api/types"
import { cn } from "@/lib/utils"
import { useWorkflowStore } from "@/state/workflow-store"
import { StatusBadge } from "../panels/components/status-badge"
import { CheckStatus } from "./check-status"
import { ConnectionHistory, ConnectionLoadTest } from "./connection-loadtest"
import { ConnectionEditor } from "./editor"
import { connectionAddress, KIND_META } from "./meta"
import { subscribeConnectionEvents, useConnections, useConnectionsStore } from "./store"

/**
 * Full-page Connections library: saved services on the left, the selected one
 * on the right (Settings · Load test · History). Drawn over home or the open
 * workflow, which stays mounted underneath.
 */
export function ConnectionsView() {
  const queryClient = useQueryClient()
  const closeView = useConnectionsStore((s) => s.closeView)
  const selectedId = useConnectionsStore((s) => s.selectedId)
  const select = useConnectionsStore((s) => s.select)
  const workflowName = useWorkflowStore((s) => s.workflow?.name)
  const connections = useConnections()

  // Live load-test events of connection-owned runs (no workflow filter) and list refreshes.
  useEffect(() => subscribeConnectionEvents(queryClient), [queryClient])
  // Opening or closing a workflow (palette, new workflow dialog) navigates away from this page.
  useEffect(
    () =>
      useWorkflowStore.subscribe((s, prev) => {
        if (s.workflow?.id !== prev.workflow?.id) closeView()
      }),
    [closeView],
  )

  const list = connections.data ?? []
  const selected = selectedId && selectedId !== "new" ? list.find((c) => c.id === selectedId) : selectedId === null ? list[0] : undefined
  const creating = selectedId === "new" || (connections.isSuccess && list.length === 0)

  return (
    <div className="fixed inset-0 z-40 flex flex-col bg-background">
      <header className="flex h-12 shrink-0 items-center gap-2 border-b px-3">
        <Button size="sm" variant="ghost" onClick={closeView}>
          <ArrowLeft /> {workflowName ? `Back to ${workflowName}` : "Home"}
        </Button>
        <div className="ml-1 flex min-w-0 items-baseline gap-2">
          <h1 className="font-heading text-sm font-semibold">Connections</h1>
          <p className="hidden truncate text-xs text-muted-foreground md:block">
            Services already deployed elsewhere that you can load-test. Saved on this machine; secrets never leave it.
          </p>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <Button size="sm" onClick={() => select("new")}>
            <Plus /> New connection
          </Button>
          <ThemeToggle />
        </div>
      </header>
      <div className="grid min-h-0 flex-1 grid-cols-[17rem_minmax(0,1fr)]">
        <nav className="flex min-h-0 flex-col gap-1 overflow-y-auto border-r p-2" aria-label="Saved connections">
          {connections.isPending ? (
            Array.from({ length: 3 }, (_, i) => <Skeleton key={i} className="h-14 rounded-lg" />)
          ) : connections.isError ? (
            <p className="p-2 text-xs text-destructive">Couldn’t load connections: {errorMessage(connections.error)}</p>
          ) : (
            <>
              {list.map((c) => (
                <ListItem key={c.id} connection={c} active={!creating && selected?.id === c.id} onSelect={() => select(c.id)} />
              ))}
              {list.length === 0 && <p className="p-2 text-xs text-muted-foreground">No connections yet.</p>}
              {creating && (
                <div className="flex items-center gap-2 rounded-lg border border-primary bg-primary/5 px-2.5 py-2 text-xs font-medium">
                  <Plus className="size-4" /> New connection
                </div>
              )}
            </>
          )}
        </nav>
        <main className="flex min-h-0 min-w-0 flex-col">
          {creating ? (
            <div className="min-h-0 flex-1 overflow-y-auto p-5">
              <ConnectionEditor key="new" onSaved={(c) => select(c.id)} />
            </div>
          ) : selected ? (
            <ConnectionDetail key={selected.id} connection={selected} />
          ) : connections.isSuccess ? (
            <div className="flex flex-1 items-center justify-center text-sm text-muted-foreground">Select a connection.</div>
          ) : null}
        </main>
      </div>
    </div>
  )
}

function ListItem({ connection: c, active, onSelect }: { connection: Connection; active: boolean; onSelect: () => void }) {
  const check = useConnectionsStore((s) => s.checks[c.id])
  const Icon = KIND_META[c.kind].icon
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-current={active ? "page" : undefined}
      className={cn(
        "flex items-start gap-2 rounded-lg border border-transparent px-2.5 py-2 text-left outline-none hover:bg-muted/60 focus-visible:ring-2 focus-visible:ring-ring/50",
        active && "border-primary/40 bg-primary/5",
      )}
    >
      <Icon className="mt-0.5 size-4 shrink-0 text-primary" />
      <span className="min-w-0 flex-1">
        <span className="block truncate text-sm font-medium">{c.name}</span>
        <span className="block truncate font-mono text-[11px] text-muted-foreground">{connectionAddress(c) || KIND_META[c.kind].label}</span>
        <CheckStatus check={check} className="mt-0.5 text-[11px]" />
      </span>
    </button>
  )
}

type DetailTab = "settings" | "loadtest" | "history"

function ConnectionDetail({ connection }: { connection: Connection }) {
  const check = useConnectionsStore((s) => s.checks[connection.id])
  const recordCheck = useConnectionsStore((s) => s.recordCheck)
  const running = useConnectionsStore((s) => s.runs[connection.id]?.some((r) => r.status === "running") ?? false)
  const runCount = useConnectionsStore((s) => s.runs[connection.id]?.length ?? 0)
  const [tab, setTab] = useState<DetailTab>(check?.ok ? "loadtest" : "settings")
  const [runId, setRunId] = useState<string | null>(null)
  const meta = KIND_META[connection.kind]
  const address = connectionAddress(connection)

  const test = useMutation({
    mutationFn: () => api.testConnection(connection.id),
    onSuccess: (result) => {
      recordCheck(connection.id, result)
      if (!result.ok) toast.error(`${connection.name}: ${result.message || "connection failed"}`, { description: result.detail })
    },
    onError: (err) => toast.error("The test could not run", { description: errorMessage(err) }),
  })

  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v as DetailTab)} className="flex min-h-0 flex-1 flex-col gap-0">
      <div className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-4 py-2.5">
        <meta.icon className="size-5 text-primary" />
        <div className="min-w-0">
          <h2 className="truncate text-sm font-semibold">{connection.name}</h2>
          <p className="truncate font-mono text-[11px] text-muted-foreground">
            {meta.label}
            {address && ` · ${address}`}
          </p>
        </div>
        <CheckStatus check={check} className="min-w-0" />
        <Button size="xs" variant="outline" disabled={test.isPending} onClick={() => test.mutate()}>
          {test.isPending ? <Spinner /> : <Zap />} Test
        </Button>
        <span
          className="ml-auto flex items-center gap-1 text-[11px] text-amber-700 dark:text-amber-400"
          title="Load tests on this connection send real traffic outside your local sandbox; you confirm each one."
        >
          <ShieldAlert className="size-3.5" /> External target — traffic leaves your local sandbox
        </span>
      </div>
      <div className="flex shrink-0 items-center border-b px-4 py-1.5">
        <TabsList>
          <TabsTrigger value="settings">Settings</TabsTrigger>
          <TabsTrigger value="loadtest">
            Load test {running && <StatusBadge status="running" label="Live" className="h-4" />}
          </TabsTrigger>
          <TabsTrigger value="history">
            History <span className="text-[10px] opacity-70">{runCount}</span>
          </TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="settings" className="min-h-0 flex-1 overflow-y-auto p-5">
        <ConnectionEditor key={connection.updatedAt} connection={connection} />
      </TabsContent>
      <TabsContent value="loadtest" className="min-h-0 flex-1">
        <ConnectionLoadTest connection={connection} runId={runId} setRunId={setRunId} />
      </TabsContent>
      <TabsContent value="history" className="min-h-0 flex-1">
        <ConnectionHistory
          connection={connection}
          onOpen={(id) => {
            setRunId(id)
            setTab("loadtest")
          }}
        />
      </TabsContent>
    </Tabs>
  )
}
