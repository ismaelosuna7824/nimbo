import { lazy, Suspense, useState } from "react"
import { keepPreviousData, useQuery } from "@tanstack/react-query"
import { Layers, Plus, RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { Spinner } from "@/components/ui/spinner"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { useShownVersion } from "@/features/panels/lib/use-shown-version"
import { api } from "@/lib/api/client"
import type { EnvironmentSource, EnvironmentsView } from "@/lib/api/types"
import { useWorkflowStore } from "@/state/workflow-store"
import { changeEnvironments } from "./actions"
import { ComparePane } from "./compare-pane"
import { type EnvDialog, EnvironmentDialogs } from "./dialogs"
import { EditPane } from "./edit-pane"
import { EnvironmentList } from "./environment-list"
import { environmentSources, missingDefaults, newEnvironmentFiles } from "./model"

const KIND_LABEL: Record<string, string> = { kustomize: "Kustomize", helm: "Helm", manifests: "Manifests", compose: "Compose" }

const VariablesPane = lazy(() => import("@/features/docker/environments/variables-pane").then((m) => ({ default: m.VariablesPane })))

type Pane = "compare" | "edit" | "variables"

/**
 * Environments of a container workspace: Kustomize overlays and Helm values files (Kubernetes), compose override files
 * plus .env.<env> (docker) on top of the base. Lists them (switch, duplicate, rename, delete), compares what each
 * renders, edits the common knobs of one environment and, for docker, its variables.
 */
export function KubeEnvironmentsTab() {
  const workflowId = useWorkflowStore((s) => s.workflow!.id)
  // Frozen while the tab is hidden: saves made meanwhile don't re-read the environments until it is shown.
  const updatedAt = useShownVersion("environments")
  const view = useQuery({
    queryKey: ["environments", workflowId, updatedAt],
    queryFn: () => api.listEnvironments(workflowId),
    placeholderData: keepPreviousData,
  })
  const [picked, setPicked] = useState<string | null>(null)
  const [pane, setPane] = useState<Pane>("compare")
  const [dialog, setDialog] = useState<EnvDialog>(null)

  if (view.isPending) {
    return (
      <div className="flex h-full items-center justify-center" data-testid="env-tab-root">
        <Spinner />
      </div>
    )
  }
  if (view.isError && !view.data) {
    return (
      <Empty className="h-full" data-testid="env-tab-root">
        <EmptyHeader>
          <EmptyTitle>Couldn't read the environments</EmptyTitle>
          <EmptyDescription>{errorMessage(view.error)}</EmptyDescription>
        </EmptyHeader>
        <Button size="sm" variant="outline" onClick={() => void view.refetch()}>
          <RefreshCw /> Try again
        </Button>
      </Empty>
    )
  }

  const data = view.data
  const names = data.environments.map((e) => e.name)
  // The picked one while it exists, else the active one (the base when none is).
  const selected = picked !== null && (picked === "" || names.includes(picked)) ? picked : data.active
  const supported = environmentSources(data.sources).length > 0
  const docker = data.kind === "docker"
  const shown: Pane = pane === "variables" && !docker ? "compare" : pane
  const dialogs = (
    <EnvironmentDialogs
      dialog={dialog}
      view={data}
      onClose={() => setDialog(null)}
      onDone={(name) => {
        setDialog(null)
        setPicked(name)
      }}
    />
  )

  if (data.environments.length === 0) {
    return (
      <div className="flex h-full min-h-0 flex-col overflow-auto" data-testid="env-tab-root">
        <NoEnvironments view={data} supported={supported} onCreate={() => setDialog({ kind: "new", from: "" })} onCreated={setPicked} />
        {dialogs}
      </div>
    )
  }

  return (
    <div className="@container flex h-full min-h-0 flex-col" data-testid="env-tab-root">
      <header className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b px-3 py-1.5">
        {data.note && (
          <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground" title={data.note}>
            {data.note}
          </span>
        )}
        <SourcesSummary sources={data.sources} />
        {view.isFetching && <Spinner className="size-3" />}
        <Button
          size="xs"
          variant="outline"
          className="ml-auto"
          disabled={!supported}
          title={supported ? undefined : docker ? "This workspace has no compose file" : "No Kustomize base or Helm chart takes environments"}
          onClick={() => setDialog({ kind: "new", from: "" })}
          data-testid="env-new"
        >
          <Plus /> New environment
        </Button>
      </header>
      <div className="flex min-h-0 flex-1 flex-col @3xl:flex-row">
        <EnvironmentList view={data} selected={selected} onSelect={setPicked} onDialog={setDialog} />
        <Tabs value={shown} onValueChange={(v) => setPane(v as Pane)} className="flex min-h-0 min-w-0 flex-1 flex-col gap-0">
          <TabsList variant="line" className="h-8 w-full shrink-0 justify-start rounded-none border-b px-2">
            <TabsTrigger value="compare" className="flex-none text-xs" data-testid="env-compare-tab">
              Compare
            </TabsTrigger>
            <TabsTrigger value="edit" className="flex-none text-xs" data-testid="env-edit-tab">
              Edit <span className="max-w-32 truncate text-muted-foreground">{selected || "Base"}</span>
            </TabsTrigger>
            {docker && (
              <TabsTrigger value="variables" className="flex-none text-xs" data-testid="env-variables-tab">
                Variables <span className="max-w-32 truncate text-muted-foreground">{selected || "Base"}</span>
              </TabsTrigger>
            )}
          </TabsList>
          <TabsContent value="compare" className="min-h-0 flex-1">
            <ComparePane active={data.active} />
          </TabsContent>
          <TabsContent value="edit" className="min-h-0 flex-1">
            <EditPane environment={selected} />
          </TabsContent>
          {docker && (
            <TabsContent value="variables" className="min-h-0 flex-1">
              <Suspense
                fallback={
                  <div className="flex h-full items-center justify-center">
                    <Spinner />
                  </div>
                }
              >
                <VariablesPane view={data} environment={selected} />
              </Suspense>
            </TabsContent>
          )}
        </Tabs>
      </div>
      {dialogs}
    </div>
  )
}

/** Which sources take environments; plain manifests render the same in every environment. */
function SourcesSummary({ sources }: { sources: EnvironmentSource[] }) {
  if (sources.length === 0) return null
  const plain = sources.filter((s) => !s.supported)
  return (
    <span className="flex min-w-0 flex-wrap items-center gap-1">
      {sources.map((s) => (
        <Badge
          key={`${s.kind}:${s.path}`}
          variant="outline"
          className={s.supported ? "h-5 font-normal" : "h-5 font-normal text-muted-foreground"}
          title={s.note ?? (s.supported ? `${s.path}: takes environments` : `${s.path}: same in every environment`)}
        >
          <span className="text-muted-foreground">{KIND_LABEL[s.kind] ?? s.kind}</span>
          <span className="max-w-40 truncate font-mono text-[10px]">{s.path || s.label}</span>
        </Badge>
      ))}
      {plain.length > 0 && <span className="text-[11px] text-muted-foreground">Plain manifests are the same in every environment.</span>}
    </span>
  )
}

/** No overlays or values files yet: what environments are, and a one-click dev + prod. */
function NoEnvironments({
  view,
  supported,
  onCreate,
  onCreated,
}: {
  view: EnvironmentsView
  supported: boolean
  onCreate: () => void
  onCreated: (name: string) => void
}) {
  const [pending, setPending] = useState(false)
  const createDefaults = async () => {
    setPending(true)
    const created: string[] = []
    for (const name of missingDefaults(view.environments.map((e) => e.name))) {
      const ok = await changeEnvironments((id) => api.createEnvironment(id, { name }), { failure: `Couldn't create ${name}` })
      if (!ok) break
      created.push(name)
    }
    setPending(false)
    if (created.length > 0) {
      toast.success(`Created ${created.join(" and ")}`)
      onCreated(created[0])
    }
  }

  return (
    <Empty className="flex-1">
      <EmptyHeader>
        <EmptyMedia variant="icon">
          <Layers />
        </EmptyMedia>
        <EmptyTitle>No environments yet</EmptyTitle>
        <EmptyDescription>
          {view.kind === "docker" ? (
            <>
              The base is <span className="font-mono">{(view.baseFiles ?? []).join(", ") || "compose.yaml"}</span>, shared by
              every environment. An environment is an override file (
              <span className="font-mono">{newEnvironmentFiles(view.sources, "<env>")[0] ?? "compose.<env>.yaml"}</span>) and/or
              an env file (<span className="font-mono">.env.&lt;env&gt;</span> or{" "}
              <span className="font-mono">&lt;env&gt;.env</span>); such files become environments automatically. Canvas,
              rules, deploy and load tests follow the active one.
            </>
          ) : (
            <>
              An environment is a Kustomize overlay (<span className="font-mono">overlays/&lt;env&gt;</span>) or a Helm values
              file (<span className="font-mono">values-&lt;env&gt;.yaml</span>) on top of the base. Deploy, rules and readiness
              follow the active one.
            </>
          )}
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent>
        <SourcesSummary sources={view.sources} />
        <div className="flex flex-wrap justify-center gap-2">
          <Button size="sm" disabled={!supported || pending} onClick={() => void createDefaults()} data-testid="env-create-defaults">
            {pending && <Spinner />}
            Create dev and prod
          </Button>
          <Button size="sm" variant="outline" disabled={!supported || pending} onClick={onCreate} data-testid="env-new">
            <Plus /> New environment
          </Button>
        </div>
        {!supported && (
          <p className="max-w-md text-xs text-muted-foreground">
            {view.sources.length === 0
              ? "This workspace has nothing to deploy yet."
              : view.kind === "docker"
                ? "This workspace has no compose file yet."
                : "Plain manifests are the same in every environment. To get environments, turn them into a Kustomize base: in the Code tab use New from template, or move them into base/ with a kustomization.yaml listing them."}
          </p>
        )}
      </EmptyContent>
    </Empty>
  )
}
