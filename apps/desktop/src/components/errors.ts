import { ApiError } from "@/lib/api/client"

/** Human-readable message for an API or runtime error, including the first diagnostic when present. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const d = err.diagnostics[0]
    return d ? `${err.message}: ${d.message}` : err.message
  }
  if (err instanceof TypeError && err.message.toLowerCase().includes("fetch")) {
    return "Can't reach the Nimbo engine (nimbo). Is it running?"
  }
  return err instanceof Error ? err.message : String(err)
}
