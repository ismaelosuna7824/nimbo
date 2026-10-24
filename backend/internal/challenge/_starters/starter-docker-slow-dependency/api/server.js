// Quote API: GET /quote/:id returns a product with its price, asking the
// pricing service on every request.
//
// Knobs (environment):
//   PRICING_URL         base URL of the pricing service (required)
//   PRICING_TIMEOUT_MS  give up on a pricing call after this many ms
//                       (default 0: wait as long as it takes)
//   PRICE_FALLBACK      "list-price": when pricing fails or times out,
//                       answer with the catalog list price (marked
//                       "priceSource": "list") instead of an error
//   CIRCUIT_BREAKER     "on": after 5 failed calls in a row, stop calling
//                       pricing for 5 s (answers use the fallback), then
//                       let one call through to see if it recovered
//   PORT                default 3000
import http from "node:http"

const port = Number(process.env.PORT || 3000)
const pricingUrl = process.env.PRICING_URL
const timeoutMs = Number(process.env.PRICING_TIMEOUT_MS || 0)
const fallback = process.env.PRICE_FALLBACK === "list-price"
const breakerOn = process.env.CIRCUIT_BREAKER === "on"

// Connections to pricing are pooled: at most 20 calls in flight.
const agent = new http.Agent({ keepAlive: true, maxSockets: 20 })

const PRODUCTS = 50
const listPrice = (id) => 1000 + ((id * 37) % 900)

const breaker = { failures: 0, openUntil: 0 }

function fetchPrice(id) {
  return new Promise((resolve, reject) => {
    const req = http.get(`${pricingUrl}/price/${id}`, { agent }, (res) => {
      let body = ""
      res.setEncoding("utf8")
      res.on("data", (c) => (body += c))
      res.on("end", () => {
        if (res.statusCode !== 200) return reject(new Error(`pricing answered ${res.statusCode}`))
        try {
          resolve(JSON.parse(body).cents)
        } catch (err) {
          reject(err)
        }
      })
      res.on("error", reject)
    })
    req.on("error", reject)
    if (timeoutMs > 0) {
      // The whole call, including the wait for a free connection.
      const timer = setTimeout(() => req.destroy(new Error(`pricing timed out after ${timeoutMs} ms`)), timeoutMs)
      req.on("close", () => clearTimeout(timer))
    }
  })
}

async function price(id) {
  if (breakerOn && Date.now() < breaker.openUntil) throw new Error("circuit open")
  try {
    const cents = await fetchPrice(id)
    breaker.failures = 0
    return cents
  } catch (err) {
    if (breakerOn && ++breaker.failures >= 5) {
      breaker.openUntil = Date.now() + 5000
      breaker.failures = 4 // one more failure after the pause reopens it
    }
    throw err
  }
}

function send(res, status, body) {
  res.writeHead(status, { "content-type": "application/json" })
  res.end(JSON.stringify(body))
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost")
  if (url.pathname === "/health") return send(res, 200, { status: "ok" })
  const m = url.pathname.match(/^\/quote\/(\d+)$/)
  if (req.method !== "GET" || !m) return send(res, 404, { error: "not found" })
  const id = (Number(m[1]) % PRODUCTS) + 1
  try {
    send(res, 200, { id, name: `Product ${id}`, cents: await price(id), priceSource: "pricing" })
  } catch (err) {
    if (fallback) return send(res, 200, { id, name: `Product ${id}`, cents: listPrice(id), priceSource: "list" })
    send(res, 502, { error: err.message })
  }
})

server.keepAliveTimeout = 65_000
server.listen(port, () => console.log(`quote api on :${port}`))
process.on("SIGTERM", () => server.close(() => process.exit(0)))
