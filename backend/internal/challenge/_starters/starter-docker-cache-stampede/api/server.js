// Product API with a Redis cache: GET /featured/:n serves one of the 20
// featured products. Pages are cached for CACHE_TTL_SEC; a miss loads the
// page from PostgreSQL (about 100 ms) and stores it.
//
// Knobs (environment):
//   DATABASE_URL     PostgreSQL connection string (required)
//   DB_POOL_SIZE     connections the API may hold (default 2)
//   REDIS_URL        Redis connection string (required)
//   CACHE_TTL_SEC    how long a cached page lives (default 300)
//   COALESCE_MISSES  "true": concurrent misses for the same product share
//                    one database load (request coalescing) instead of
//                    each running its own
//   PORT             default 3000
import http from "node:http"
import pg from "pg"
import { Redis } from "./resp.js"

const FEATURED = 20
const port = Number(process.env.PORT || 3000)
const ttl = Number(process.env.CACHE_TTL_SEC || 300)
const coalesce = process.env.COALESCE_MISSES === "true"
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, max: Number(process.env.DB_POOL_SIZE || 2) })
const cache = new Redis(process.env.REDIS_URL)

// Loads in flight, by product (used when COALESCE_MISSES is on).
const inflight = new Map()

async function loadAndStore(id) {
  const { rows } = await pool.query("SELECT * FROM product_page($1)", [id])
  const body = JSON.stringify(rows[0] ?? null)
  cache.command("SET", `product:${id}`, body, "EX", ttl).catch(() => {})
  return body
}

function load(id) {
  if (!coalesce) return loadAndStore(id)
  let p = inflight.get(id)
  if (!p) {
    p = loadAndStore(id).finally(() => inflight.delete(id))
    inflight.set(id, p)
  }
  return p
}

async function product(id) {
  try {
    const hit = await cache.command("GET", `product:${id}`)
    if (hit !== null) return { body: hit, source: "cache" }
  } catch {
    // Cache down: load from the database.
  }
  return { body: await load(id), source: "database" }
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "content-type": "application/json", ...headers })
  res.end(body)
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost")
  if (url.pathname === "/health") return send(res, 200, '{"status":"ok"}')
  const m = url.pathname.match(/^\/featured\/(\d+)$/)
  if (req.method !== "GET" || !m) return send(res, 404, '{"error":"not found"}')
  const id = (Number(m[1]) % FEATURED) + 1
  try {
    const { body, source } = await product(id)
    send(res, 200, body, { "x-source": source })
  } catch (err) {
    send(res, 500, JSON.stringify({ error: err.message }))
  }
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`product api on :${port}, coalescing ${coalesce ? "on" : "off"}`))
process.on("SIGTERM", () => server.close(() => pool.end().then(() => process.exit(0))))
