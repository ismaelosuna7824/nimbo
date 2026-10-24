// Orders service. One image, two roles:
//   ROLE=api     (default) serves HTTP: GET /orders/:id and
//                GET /reports/latest (200 while the sales report is fresh)
//   ROLE=worker  no HTTP; only runs the background report job
//
// The report job crunches the day's sales every second: about 400 ms of
// CPU work, during which a Node process can do nothing else. It stores
// the finished report's time in Redis (report:last).
//
// Knobs (environment):
//   ROLE        api | worker (default api)
//   RUN_JOBS    "true": the api role also runs the report job (default true)
//   REDIS_URL   Redis connection string (required)
//   PORT        default 3000
import http from "node:http"
import { Redis } from "./resp.js"

const role = process.env.ROLE || "api"
const runJobs = role === "worker" || (process.env.RUN_JOBS ?? "true") === "true"
const port = Number(process.env.PORT || 3000)
const redis = new Redis(process.env.REDIS_URL)

// The sales report: CPU-bound for JOB_MS, measured in time so it weighs
// the same on every machine.
const JOB_MS = 400
function salesReport() {
  const end = Date.now() + JOB_MS
  let x = 0
  while (Date.now() < end) for (let i = 0; i < 10_000; i++) x = (x * 31 + i) % 1_000_003
  redis.command("SET", "report:last", String(Date.now())).catch(() => {})
  return x
}

if (runJobs) {
  setInterval(salesReport, 1000)
  console.log(`${role}: running the report job every second`)
}

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" })
  res.end(JSON.stringify(body))
}

if (role === "api") {
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, "http://localhost")
    if (url.pathname === "/health") return send(res, 200, { status: "ok" })
    if (url.pathname === "/reports/latest") {
      try {
        const last = Number(await redis.command("GET", "report:last"))
        const ageSec = (Date.now() - last) / 1000
        if (last && ageSec < 10) return send(res, 200, { status: "fresh", ageSec })
        return send(res, 503, { error: "no report in the last 10 s: is the report job running?" })
      } catch (err) {
        return send(res, 503, { error: err.message })
      }
    }
    const m = url.pathname.match(/^\/orders\/(\d+)$/)
    if (req.method !== "GET" || !m) return send(res, 404, { error: "not found" })
    const id = Number(m[1]) % 100_000
    send(res, 200, { id, status: id % 7 === 0 ? "shipped" : "processing", items: (id % 4) + 1 })
  })
  server.keepAliveTimeout = 65_000
  server.listen(port, () => console.log(`orders api on :${port}`))
  process.on("SIGTERM", () => server.close(() => process.exit(0)))
} else {
  process.on("SIGTERM", () => process.exit(0))
}
