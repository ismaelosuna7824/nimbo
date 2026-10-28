// Client cache of the catalog data completion runs on: the type index, the
// built-in functions and schemas fetched on demand (batched, kept for the
// session; the catalog is embedded in the daemon and never changes while it
// runs). The canvas and the inspector read their node schemas through it too.

import { api } from "@/lib/api/client"
import type { FunctionSignature, ResourceSchema, SchemaKind, TypeInfo } from "@/lib/api/types"

export interface TypeIndex {
  all: TypeInfo[]
  resources: TypeInfo[]
  data: TypeInfo[]
  /** "resource/aws_s3_bucket" -> entry. */
  byKey: Map<string, TypeInfo>
  /** Provider local names with types ("aws", "azurerm", "random", ...). */
  providers: string[]
}

let types: Promise<TypeIndex> | null = null
let functions: Promise<Map<string, FunctionSignature>> | null = null
let typesReady: TypeIndex | null = null
let functionsReady: Map<string, FunctionSignature> | null = null

/** The type index, fetched once. */
export function loadTypes(): Promise<TypeIndex> {
  types ??= api.catalogTypes().then(
    (all) => {
      const byKey = new Map(all.map((t) => [`${t.kind}/${t.type}`, t]))
      const providers = [...new Set(all.map((t) => t.type.slice(0, t.type.indexOf("_"))))]
      typesReady = {
        all,
        resources: all.filter((t) => t.kind === "resource"),
        data: all.filter((t) => t.kind === "data"),
        byKey,
        providers,
      }
      return typesReady
    },
    (err) => {
      types = null
      throw err
    },
  )
  return types
}

/** The type index if already loaded. */
export const typeIndex = () => typesReady

/** Built-in functions by name, fetched once. */
export function loadFunctions(): Promise<Map<string, FunctionSignature>> {
  functions ??= api.catalogFunctions().then(
    (fns) => (functionsReady = new Map(fns.map((f) => [f.name, f]))),
    (err) => {
      functions = null
      throw err
    },
  )
  return functions
}

export const functionIndex = () => functionsReady

const schemas = new Map<string, ResourceSchema | null>()
const pending = new Map<string, Promise<ResourceSchema | null>>()
let queue: { key: string; resolve: (s: ResourceSchema | null) => void; reject: (e: unknown) => void }[] = []
let scheduled = false
const BATCH = 200

export const schemaKey = (kind: SchemaKind, type: string) => `${kind}/${type}`

/** A cached schema: undefined while unknown, null for types the catalog doesn't have. */
export const cachedSchema = (kind: SchemaKind, type: string) => schemas.get(schemaKey(kind, type))

function flush() {
  scheduled = false
  const batch = queue
  queue = []
  for (let i = 0; i < batch.length; i += BATCH) {
    const part = batch.slice(i, i + BATCH)
    api.catalogSchemas(part.map((q) => q.key)).then(
      (found) => {
        for (const q of part) {
          const s = found[q.key] ?? null
          schemas.set(q.key, s)
          pending.delete(q.key)
          q.resolve(s)
        }
      },
      (err) => {
        for (const q of part) {
          pending.delete(q.key)
          q.reject(err)
        }
      },
    )
  }
}

/** A schema, fetched with every other one requested in the same tick. */
export function loadSchema(kind: SchemaKind, type: string): Promise<ResourceSchema | null> {
  const key = schemaKey(kind, type)
  if (schemas.has(key)) return Promise.resolve(schemas.get(key)!)
  let p = pending.get(key)
  if (!p) {
    p = new Promise((resolve, reject) => queue.push({ key, resolve, reject }))
    pending.set(key, p)
    if (!scheduled) {
      scheduled = true
      queueMicrotask(flush)
    }
  }
  return p
}

/** Fetches schemas in the background; failures are ignored (completion fetches again when needed). */
export function prefetchSchemas(keys: Iterable<[SchemaKind, string]>) {
  for (const [kind, type] of keys) loadSchema(kind, type).catch(() => {})
}
