// Catalog API. Like many real services it needs a warm-up after it starts
// (loading reference data, filling in-memory caches): for WARMUP_SEC
// seconds it accepts connections but answers 503 to everything, including
// GET /health. After that, GET /health is 200 and GET /catalog/:id works.
//
// Knobs (environment):
//   WARMUP_SEC  seconds of warm-up after start (default 15)
//   PORT        default 3000
import http from "node:http"
import os from "node:os"

const port = Number(process.env.PORT || 3000)
const warmupMs = Number(process.env.WARMUP_SEC ?? 15) * 1000
const startedAt = Date.now()
const warm = () => Date.now() - startedAt >= warmupMs

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" })
  res.end(JSON.stringify(body))
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://localhost")
  if (!warm()) {
    const left = Math.ceil((warmupMs - (Date.now() - startedAt)) / 1000)
    return send(res, 503, { error: "warming up", readyInSec: left, instance: os.hostname() })
  }
  if (url.pathname === "/health") return send(res, 200, { status: "ok" })
  const m = url.pathname.match(/^\/catalog\/(\d+)$/)
  if (req.method !== "GET" || !m) return send(res, 404, { error: "not found" })
  const id = (Number(m[1]) % 500) + 1
  send(res, 200, { id, name: `Item ${id}`, instance: os.hostname() })
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`catalog api on :${port}, warming up for ${warmupMs / 1000}s`))
process.on("SIGTERM", () => server.close(() => process.exit(0)))
