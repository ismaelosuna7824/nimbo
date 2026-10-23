// The Compose Specification JSON schema (the one compose-go, and so
// `docker compose`, validates against), navigated with the shared YAML
// schema helpers of code-intel/yaml/schema.

import { api } from "@/lib/api/client"
import type { JsonSchema } from "@/features/code-intel/yaml/schema"

let schemaPromise: Promise<JsonSchema> | undefined

/** The daemon's schema (GET /catalog/docker/compose-schema), fetched once per session. */
export function loadComposeSchema(): Promise<JsonSchema> {
  schemaPromise ??= api.composeSchema().then(
    (s) => s as JsonSchema,
    (err: unknown) => {
      schemaPromise = undefined
      throw err
    },
  )
  return schemaPromise
}
