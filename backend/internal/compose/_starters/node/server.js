const http = require("node:http");
const client = require("prom-client");

const port = Number(process.env.PORT ?? 8080);

// HTTP server metrics named after the OpenTelemetry semantic conventions
// (http.server.request.duration in seconds), as Prometheus scrapes them at
// GET /metrics: Add monitoring's 5xx and p95 alerts read this histogram.
const requestDuration = new client.Histogram({
  name: "http_server_request_duration_seconds",
  help: "Duration of HTTP server requests.",
  labelNames: ["http_request_method", "http_route", "http_response_status_code"],
  buckets: [0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10],
});
client.collectDefaultMetrics();

// Route templates (low-cardinality http_route values) and their handlers.
const routes = {
  "/": (req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ message: "Hello from Node.js" }));
  },
};

http
  .createServer(async (req, res) => {
    const path = new URL(req.url, "http://localhost").pathname;
    if (path === "/metrics") {
      // Not measured itself: scrapes would skew the request metrics.
      res.writeHead(200, { "Content-Type": client.register.contentType });
      res.end(await client.register.metrics());
      return;
    }
    const handler = routes[path];
    const stop = requestDuration.startTimer({ http_request_method: req.method, http_route: handler ? path : "" });
    res.on("finish", () => stop({ http_response_status_code: res.statusCode }));
    if (!handler) {
      res.writeHead(404, { "Content-Type": "application/json" });
      res.end(JSON.stringify({ error: "not found" }));
      return;
    }
    handler(req, res);
  })
  .listen(port, () => console.log(`listening on :${port}`));
