import { useEffect, useState } from "react"
import { getVersion } from "@tauri-apps/api/app"
import { AlertCircleIcon, CheckCircle2Icon, DownloadIcon, RefreshCwIcon, SparklesIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Progress } from "@/components/ui/progress"
import { Spinner } from "@/components/ui/spinner"
import { Switch } from "@/components/ui/switch"
import { Row, Section } from "@/features/settings/common"
import { updatesEnabled, useUpdates } from "./store"

/** Settings › About: the installed version and the updater. */
export function AboutSection() {
  const [version, setVersion] = useState<string | null>(null)
  useEffect(() => {
    if (updatesEnabled) void getVersion().then(setVersion)
  }, [])

  return (
    <Section title="About Nimbo" description="Design, deploy and load-test cloud infrastructure locally.">
      <div>
        <Row label="Version">
          <p className="pt-0.5 font-mono text-sm">{updatesEnabled ? (version ?? "…") : "development build (browser)"}</p>
        </Row>
        {updatesEnabled && <UpdateRows />}
      </div>
    </Section>
  )
}

function UpdateRows() {
  const { phase, available, selfUpdate, downloaded, total, error, autoCheck, setAutoCheck, check, install } = useUpdates()
  const busy = phase === "checking" || phase === "downloading" || phase === "restarting"

  return (
    <>
      <Row label="Updates" description="New versions are published on GitHub. Your workflows and settings are kept.">
        <div className="flex flex-col items-start gap-3">
          <Status />
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void check()}>
            {phase === "checking" ? <Spinner /> : <RefreshCwIcon />}
            Check for updates
          </Button>
          {available && (
            <div className="flex w-full flex-col gap-3 rounded-md border bg-muted/30 p-3">
              <div className="flex items-center gap-2 text-sm font-medium">
                <SparklesIcon className="size-4 text-primary" />
                Nimbo {available.version}
                {available.date && (
                  <span className="font-normal text-muted-foreground">· {new Date(available.date).toLocaleDateString()}</span>
                )}
              </div>
              {available.notes && (
                <pre className="max-h-64 overflow-y-auto font-sans text-xs leading-relaxed whitespace-pre-wrap text-muted-foreground">
                  {available.notes}
                </pre>
              )}
              {phase === "downloading" || phase === "restarting" ? (
                <div className="flex flex-col gap-1.5">
                  <Progress value={total ? Math.min(100, (downloaded / total) * 100) : undefined} />
                  <p className="text-xs text-muted-foreground">
                    {phase === "restarting"
                      ? "Installed. Restarting Nimbo…"
                      : `Downloading… ${formatMB(downloaded)}${total ? ` of ${formatMB(total)}` : ""}`}
                  </p>
                </div>
              ) : (
                <Button size="sm" className="self-start" onClick={() => void install()}>
                  <DownloadIcon />
                  {selfUpdate ? "Update & restart" : `Download v${available.version}`}
                </Button>
              )}
              {!selfUpdate && (
                <p className="text-xs text-muted-foreground">
                  This copy was installed with a system package (.deb/.rpm); install the new package from the release page.
                </p>
              )}
            </div>
          )}
          {phase === "error" && error && (
            <p className="flex items-start gap-1.5 text-xs text-destructive">
              <AlertCircleIcon className="mt-px size-3.5 shrink-0" />
              {error}
            </p>
          )}
        </div>
      </Row>
      <Row label="Check automatically" htmlFor="updates-auto-check" description="Look for a new version each time Nimbo starts.">
        <Switch id="updates-auto-check" checked={autoCheck} onCheckedChange={setAutoCheck} />
      </Row>
    </>
  )
}

function Status() {
  const phase = useUpdates((s) => s.phase)
  if (phase === "up-to-date")
    return (
      <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
        <CheckCircle2Icon className="size-4 text-green-500" />
        You’re on the latest version.
      </p>
    )
  if (phase === "checking") return <p className="text-sm text-muted-foreground">Checking for updates…</p>
  return null
}

function formatMB(bytes: number) {
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
