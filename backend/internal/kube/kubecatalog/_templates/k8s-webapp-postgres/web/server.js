import express from "express";
import pg from "pg";
import client from "prom-client";

// DATABASE_URL comes from the web-db Secret (see k8s/web.yaml).
const db = new pg.Pool({ connectionString: process.env.DATABASE_URL });
await db.query(
  "CREATE TABLE IF NOT EXISTS notes (id SERIAL PRIMARY KEY, text TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT now())",
);

// HTTP server metrics named after the OpenTelemetry semantic conventions
// (http.server.request.duration in seconds), served at GET /metrics for
// Prometheus (pod annotations prometheus.io/* in k8s/web.yaml).
const requestDuration = new client.Histogram({
  name: "http_server_request_duration_seconds",
  help: "Duration of HTTP server requests.",
  labelNames: ["http_request_method", "http_route", "http_response_status_code"],
  buckets: [0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10],
});
client.collectDefaultMetrics();

const app = express();

app.get("/metrics", async (_req, res) => {
  res.type(client.register.contentType).send(await client.register.metrics());
});

// Every other request is measured, labelled with its route template
// ("/notes", not the URL: no per-path cardinality; "" when none matched).
app.use((req, res, next) => {
  const stop = requestDuration.startTimer({ http_request_method: req.method });
  res.on("finish", () =>
    stop({ http_route: req.route ? req.baseUrl + req.route.path : "", http_response_status_code: res.statusCode }),
  );
  next();
});
app.use(express.json());

app.get("/", (_req, res) => {
  res.json({
    service: "web",
    greeting: process.env.GREETING,
    endpoints: ["GET /healthz", "GET /readyz", "GET /notes", "POST /notes {text}"],
  });
});

// Liveness: the process answers. Readiness: it can reach PostgreSQL.
app.get("/healthz", (_req, res) => res.json({ status: "ok" }));
app.get("/readyz", async (_req, res) => {
  try {
    await db.query("SELECT 1");
    res.json({ status: "ready" });
  } catch (err) {
    res.status(503).json({ status: "unavailable", error: String(err) });
  }
});

app.get("/notes", async (_req, res) => {
  const { rows } = await db.query("SELECT id, text, created_at FROM notes ORDER BY id DESC LIMIT 50");
  res.json(rows);
});

app.post("/notes", async (req, res) => {
  const text = String(req.body?.text ?? "").trim();
  if (!text) return res.status(400).json({ error: "text is required" });
  const { rows } = await db.query("INSERT INTO notes (text) VALUES ($1) RETURNING id, text, created_at", [text]);
  res.status(201).json(rows[0]);
});

const port = Number(process.env.PORT ?? 3000);
const server = app.listen(port, () => console.log(`web listening on :${port}`));
// Kubernetes sends SIGTERM before killing the pod: finish requests, close the pool.
process.on("SIGTERM", () => server.close(() => db.end().then(() => process.exit(0))));
