// Recommendations API: GET /recommendations/:userId scores the catalog for
// a user. Scoring is CPU work (hashing feature vectors, WORK_ROUNDS
// rounds per request, a few milliseconds of CPU) and uses almost no
// memory, so load shows up as CPU, not memory.
//
// Knobs (environment):
//   WORK_ROUNDS  hashing rounds per request (default 10000)
//   PORT         default 3000
import crypto from "node:crypto"
import http from "node:http"
import os from "node:os"

const port = Number(process.env.PORT || 3000)
const rounds = Number(process.env.WORK_ROUNDS || 10_000)

function score(userId) {
  let h = Buffer.from(`user-${userId}`)
  for (let i = 0; i < rounds; i++) h = crypto.createHash("sha256").update(h).digest()
  return h.readUInt32BE(0)
}

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" })
  res.end(JSON.stringify(body))
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, "http://localhost")
  if (url.pathname === "/health") return send(res, 200, { status: "ok" })
  const m = url.pathname.match(/^\/recommendations\/(\d+)$/)
  if (req.method !== "GET" || !m) return send(res, 404, { error: "not found" })
  const s = score(Number(m[1]) % 10_000)
  send(res, 200, { user: Number(m[1]) % 10_000, items: [s % 97, s % 89, s % 83], pod: os.hostname() })
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`recommendations api on :${port}, ${rounds} rounds per request`))
process.on("SIGTERM", () => setTimeout(() => server.close(() => process.exit(0)), 5000))
