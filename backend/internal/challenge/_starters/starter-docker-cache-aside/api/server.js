// Product API: GET /products/:id renders a product page from PostgreSQL.
//
// Knobs (environment):
//   DATABASE_URL     PostgreSQL connection string (required)
//   DB_POOL_SIZE     connections the API may hold (default 4)
//   REDIS_URL        when set, product pages are cached in Redis (cache-aside)
//   CACHE_TTL_SEC    how long a cached page lives (default 60)
//   PORT             default 3000
import http from "node:http"
import pg from "pg"
import { Redis } from "./resp.js"

const PRODUCTS = 200
const port = Number(process.env.PORT || 3000)
const ttl = Number(process.env.CACHE_TTL_SEC || 60)
const pool = new pg.Pool({ connectionString: process.env.DATABASE_URL, max: Number(process.env.DB_POOL_SIZE || 4) })
const cache = process.env.REDIS_URL ? new Redis(process.env.REDIS_URL) : null

// product_page() runs the expensive query a real product page needs
// (reviews, stock, pricing rules): see db/init.sql.
async function loadProduct(id) {
  const { rows } = await pool.query("SELECT * FROM product_page($1)", [id])
  return JSON.stringify(rows[0] ?? null)
}

async function product(id) {
  if (!cache) return { body: await loadProduct(id), source: "database" }
  const key = `product:${id}`
  try {
    const hit = await cache.command("GET", key)
    if (hit !== null) return { body: hit, source: "cache" }
  } catch {
    // Cache down: serve from the database.
  }
  const body = await loadProduct(id)
  cache.command("SET", key, body, "EX", ttl).catch(() => {})
  return { body, source: "database" }
}

function send(res, status, body, headers = {}) {
  res.writeHead(status, { "content-type": "application/json", ...headers })
  res.end(body)
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost")
  if (url.pathname === "/health") return send(res, 200, '{"status":"ok"}')
  const m = url.pathname.match(/^\/products\/(\d+)$/)
  if (req.method !== "GET" || !m) return send(res, 404, '{"error":"not found"}')
  // Any number maps onto the catalog, so load tests can use {{rand}}.
  const id = (Number(m[1]) % PRODUCTS) + 1
  try {
    const { body, source } = await product(id)
    send(res, 200, body, { "x-source": source })
  } catch (err) {
    send(res, 500, JSON.stringify({ error: err.message }))
  }
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`product api on :${port}, cache ${cache ? "on" : "off"}`))

// Finish in-flight requests before exiting.
process.on("SIGTERM", () => server.close(() => pool.end().then(() => process.exit(0))))
