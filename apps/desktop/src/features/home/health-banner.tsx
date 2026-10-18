import { useQuery } from "@tanstack/react-query"
import { isTauri } from "@tauri-apps/api/core"
import { CheckCircle2Icon, ListChecksIcon, RefreshCwIcon, ServerCrashIcon, TriangleAlertIcon } from "lucide-react"
import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Button } from "@/components/ui/button"
import { openSetupChecklist, useSetup } from "@/features/onboarding/store"
import { api, apiBaseUrl } from "@/lib/api/client"

function useHealth() {
  return useQuery({
    queryKey: ["health"],
    queryFn: api.health,
    retry: false,
    refetchInterval: (q) => (q.state.status === "error" ? 5000 : 60_000),
  })
}

export function HealthBanner() {
  const health = useHealth()
  const setup = useSetup({ interval: 10_000 })
  const retry = (
    <Button variant="outline" size="sm" onClick={() => void health.refetch()} disabled={health.isFetching}>
      <RefreshCwIcon className={health.isFetching ? "animate-spin" : undefined} />
      Retry
    </Button>
  )

  if (health.isError)
    return (
      <Alert variant="destructive">
        <ServerCrashIcon />
        <AlertTitle>Can't reach the Nimbo engine</AlertTitle>
        <AlertDescription>
          {isTauri() ? (
            <p>The engine bundled with the app didn't answer. Quit and reopen Nimbo; if it keeps happening, check that port access to 127.0.0.1 isn't blocked.</p>
          ) : (
            <>
              <p>
                The app talks to the local <code className="font-mono">nimbo</code> engine at{" "}
                <code className="font-mono">{apiBaseUrl()}</code>. Start it from the repository root:
              </p>
              <pre className="mt-1 rounded-md bg-muted px-2 py-1 font-mono text-xs text-foreground">
                cd backend && go run ./cmd/nimbo serve --addr 127.0.0.1:7788
              </pre>
            </>
          )}
        </AlertDescription>
        <AlertAction>{retry}</AlertAction>
      </Alert>
    )

  const status = setup.data
  if (status && !status.ready) {
    const pending = status.checks.filter((c) => c.required && c.status !== "ok" && c.status !== "warning")
    return (
      <Alert className="border-amber-500/40 text-amber-700 dark:text-amber-300">
        <TriangleAlertIcon />
        <AlertTitle>Finish setting up Nimbo</AlertTitle>
        <AlertDescription>
          <p>
            You can design workflows and export Terraform already. Deploying to your local cloud and load testing need a few more things
            {pending[0] ? ` (${pending[0].summary.replace(/\.$/, "")})` : ""} — the setup checklist walks you through them step by step.
          </p>
        </AlertDescription>
        <AlertAction>
          <Button size="sm" onClick={openSetupChecklist}>
            <ListChecksIcon />
            Open setup checklist
          </Button>
        </AlertAction>
      </Alert>
    )
  }

  if (health.data)
    return (
      <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <CheckCircle2Icon className="size-3.5 text-emerald-500" />
        Engine {health.data.version} ready{status?.ready && " · everything is set up"} · data in <span className="font-mono">{health.data.home}</span>
      </p>
    )

  return null
}
