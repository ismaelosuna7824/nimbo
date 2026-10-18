import { CircleDollarSign, ShieldAlert, TriangleAlert } from "lucide-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogMedia,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import type { LoadTestSpec } from "@/lib/api/types"
import { formatDuration, formatNumber } from "../panels/lib/format"
import { modeUnit } from "../panels/loadtest/metrics"
import { estimateTraffic, type ExternalTarget } from "./safety"

/**
 * Asks before a load test sends traffic outside the local sandbox: where it goes,
 * how much, and whether it writes data or costs money. Open while spec is set.
 */
export function ConfirmExternalDialog({
  spec,
  info,
  onConfirm,
  onCancel,
}: {
  spec: LoadTestSpec | null
  info: ExternalTarget | null
  onConfirm: () => void
  onCancel: () => void
}) {
  const traffic = spec ? estimateTraffic(spec.profile) : null
  return (
    <AlertDialog open={!!spec} onOpenChange={(open) => !open && onCancel()}>
      <AlertDialogContent className="sm:max-w-lg">
        <AlertDialogHeader>
          <AlertDialogMedia className="bg-amber-500/10 text-amber-600 dark:text-amber-400">
            <ShieldAlert />
          </AlertDialogMedia>
          <AlertDialogTitle>Send real traffic to {info?.where ?? "an external service"}?</AlertDialogTitle>
          <AlertDialogDescription>
            This test leaves your local sandbox and hits a service that is already deployed. Make sure you own it and
            that it can take the load.
          </AlertDialogDescription>
        </AlertDialogHeader>
        {spec && traffic && (
          <div className="flex flex-col gap-2 text-xs">
            <dl className="grid grid-cols-[6.5rem_1fr] gap-x-3 gap-y-1 rounded-lg border p-2.5">
              {info?.details.map((d) => (
                <div key={d.label} className="contents">
                  <dt className="text-muted-foreground">{d.label}</dt>
                  <dd className="min-w-0 truncate font-mono" title={d.value}>
                    {d.value}
                  </dd>
                </div>
              ))}
              <dt className="text-muted-foreground">Traffic</dt>
              <dd>
                {traffic.requests !== undefined
                  ? `≈ ${formatNumber(traffic.requests)} requests over ${formatDuration(traffic.durationSec * 1000)}, peak ${formatNumber(traffic.peak)} req/s`
                  : `Up to ${formatNumber(traffic.peak)} ${modeUnit(spec.profile.mode)} for ${formatDuration(traffic.durationSec * 1000)} — the number of requests depends on how fast the service answers`}
              </dd>
            </dl>
            {info?.sideEffect === "writes data" && (
              <p className="flex gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-2 text-amber-800 dark:text-amber-300">
                <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                This target writes data: every request creates or changes records on the real service.
              </p>
            )}
            {info?.sideEffect === "invokes function" && (
              <p className="flex gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-2 text-amber-800 dark:text-amber-300">
                <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                This target invokes a function: every request runs your code, with whatever side effects it has.
              </p>
            )}
            {info?.aws && (
              <p className="flex gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-2 text-destructive">
                <CircleDollarSign className="mt-0.5 size-3.5 shrink-0" />
                Real AWS traffic and possible cost: requests are signed with your AWS profile and billed to that account
                (API calls, invocations, storage, data transfer).
              </p>
            )}
          </div>
        )}
        <AlertDialogFooter>
          <AlertDialogCancel>Cancel</AlertDialogCancel>
          <AlertDialogAction variant={info?.aws || info?.sideEffect ? "destructive" : "default"} onClick={onConfirm}>
            Send traffic
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
