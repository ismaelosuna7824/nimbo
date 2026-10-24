// Pricing service: GET /price/:id answers the current price of a product
// (a few milliseconds of work). The quote api calls it on every request.
import http from "node:http"

const port = Number(process.env.PORT || 4000)

const server = http.createServer((req, res) => {
  const m = new URL(req.url, "http://localhost").pathname.match(/^\/price\/(\d+)$/)
  if (req.url === "/health") {
    res.writeHead(200, { "content-type": "application/json" })
    return res.end('{"status":"ok"}')
  }
  if (!m) {
    res.writeHead(404, { "content-type": "application/json" })
    return res.end('{"error":"not found"}')
  }
  const id = Number(m[1])
  // Today's price: the list price with a small daily adjustment.
  const day = Math.floor(Date.now() / 86_400_000)
  const cents = 1000 + ((id * 37 + day) % 900)
  setTimeout(() => {
    res.writeHead(200, { "content-type": "application/json" })
    res.end(JSON.stringify({ id, cents }))
  }, 3)
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`pricing on :${port}`))
process.on("SIGTERM", () => server.close(() => process.exit(0)))
