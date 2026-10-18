import { useQuery } from "@tanstack/react-query"
import { getVersion } from "@tauri-apps/api/app"
import { isTauri } from "@tauri-apps/api/core"
import { open } from "@tauri-apps/plugin-shell"
import { CopyIcon, FolderOpenIcon } from "lucide-react"
import { toast } from "sonner"
import { errorMessage } from "@/components/errors"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { api } from "@/lib/api/client"
import { Row, Section } from "./common"

const FOLDER_LABEL = navigator.userAgent.includes("Mac") ? "Show in Finder" : "Open folder"

function copyPath(path: string) {
  navigator.clipboard.writeText(path).then(
    () => toast.success("Path copied"),
    () => toast.error("Couldn’t copy to the clipboard"),
  )
}

export function DataSection() {
  const health = useQuery({ queryKey: ["health"], queryFn: api.health, staleTime: 30_000 })
  const appVersion = useQuery({ queryKey: ["app-version"], queryFn: getVersion, enabled: isTauri(), staleTime: Infinity })

  const openFolder = async (path: string) => {
    try {
      await open(path)
    } catch (err) {
      toast.error("Couldn’t open the folder", { description: errorMessage(err) })
    }
  }

  return (
    <Section title="Data" description="Everything Nimbo stores lives on this machine.">
      <div>
        <Row label="Nimbo folder" description="Workflows, run history and logs, load-test results, saved connections and these settings.">
          {health.isPending ? (
            <Skeleton className="h-9" />
          ) : health.isError ? (
            <p className="text-sm text-destructive">Couldn’t reach the Nimbo engine: {errorMessage(health.error)}</p>
          ) : (
            <div className="flex flex-col gap-2">
              <code className="rounded-md border bg-muted/40 px-2.5 py-2 font-mono text-xs break-all">{health.data.home}</code>
              <div className="flex gap-2">
                {isTauri() && (
                  <Button size="sm" variant="outline" onClick={() => void openFolder(health.data.home)}>
                    <FolderOpenIcon /> {FOLDER_LABEL}
                  </Button>
                )}
                <Button size="sm" variant="ghost" onClick={() => copyPath(health.data.home)}>
                  <CopyIcon /> Copy path
                </Button>
              </div>
            </div>
          )}
        </Row>
        <Row label="Versions">
          <dl className="grid grid-cols-[8rem_minmax(0,1fr)] gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">App</dt>
            <dd className="font-mono text-xs leading-5">{isTauri() ? (appVersion.data ?? "…") : "browser preview"}</dd>
            <dt className="text-muted-foreground">Engine</dt>
            <dd className="font-mono text-xs leading-5">{health.data?.version ?? "…"}</dd>
            <dt className="text-muted-foreground">Docker</dt>
            <dd>
              {health.data &&
                (health.data.docker ? (
                  <Badge variant="secondary">Available</Badge>
                ) : (
                  <span className="text-xs text-destructive">{health.data.dockerError ?? "Not available"}</span>
                ))}
            </dd>
          </dl>
        </Row>
      </div>
    </Section>
  )
}
