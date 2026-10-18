import { GaugeIcon, PlugIcon, PlusIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import type { Connection } from "@/lib/api/types"
import { CheckStatus } from "./check-status"
import { connectionAddress, KIND_META } from "./meta"
import { useConnections, useConnectionsStore } from "./store"

/** Home screen: saved connections and the "test a deployed service" entry point. */
export function ConnectionsSection() {
  const connections = useConnections()
  const openView = useConnectionsStore((s) => s.openView)
  const list = connections.data ?? []

  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-medium text-muted-foreground">Connections</h2>
        <span className="hidden text-xs text-muted-foreground sm:inline">· services already deployed elsewhere</span>
        <div className="ml-auto flex gap-1.5">
          {list.length > 0 && (
            <Button size="sm" variant="ghost" onClick={() => openView()}>
              <PlugIcon /> Open connections
            </Button>
          )}
          <Button size="sm" variant="outline" onClick={() => openView("new")}>
            <PlusIcon /> New connection
          </Button>
        </div>
      </div>
      {connections.isPending ? (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <Skeleton className="h-24 rounded-xl" />
        </div>
      ) : connections.isError ? (
        <p className="text-sm text-muted-foreground">Connections will appear here once the engine is reachable.</p>
      ) : (
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {list.map((c) => (
            <ConnectionCard key={c.id} connection={c} onOpen={() => openView(c.id)} />
          ))}
          <div className={list.length === 0 ? "sm:col-span-2 lg:col-span-3" : undefined}>
            <TestDeployedCta compact={list.length > 0} onStart={() => openView("new")} />
          </div>
        </div>
      )}
    </section>
  )
}

function ConnectionCard({ connection: c, onOpen }: { connection: Connection; onOpen: () => void }) {
  const check = useConnectionsStore((s) => s.checks[c.id])
  const meta = KIND_META[c.kind]
  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex min-h-24 flex-col gap-1 rounded-xl border bg-card p-4 text-left transition-colors outline-none hover:border-primary/40 focus-visible:ring-2 focus-visible:ring-ring/50"
    >
      <span className="flex items-center gap-2">
        <meta.icon className="size-4 shrink-0 text-primary" />
        <span className="truncate font-medium">{c.name}</span>
        <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{meta.label}</span>
      </span>
      <span className="truncate font-mono text-xs text-muted-foreground">{connectionAddress(c) || "—"}</span>
      <CheckStatus check={check} className="mt-auto pt-1" />
    </button>
  )
}

function TestDeployedCta({ compact, onStart }: { compact: boolean; onStart: () => void }) {
  return (
    <div className="flex h-full min-h-24 items-start gap-3 rounded-xl border border-dashed p-4">
      <GaugeIcon className="mt-0.5 size-5 shrink-0 text-primary" />
      <div className="flex min-w-0 flex-col gap-1.5">
        <span className="font-medium">Test a deployed service</span>
        <span className="text-xs text-muted-foreground">
          {compact
            ? "Connect another API, database or AWS account and load-test it — no workflow needed."
            : "Already have an API, a database (PostgreSQL, MySQL, Redis, MongoDB) or an AWS account running somewhere? Save it as a connection, check that it answers, then load-test it — no workflow needed. Nothing is sent until you start a test and confirm it."}
        </span>
        <Button size="sm" variant={compact ? "ghost" : "default"} className="self-start" onClick={onStart}>
          <PlugIcon /> Connect a service
        </Button>
      </div>
    </div>
  )
}
