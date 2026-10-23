import express from "express";
import pg from "pg";
import client from "prom-client";
import { createClient } from "redis";

const db = new pg.Pool({ connectionString: process.env.DATABASE_URL });
const cache = createClient({ url: process.env.REDIS_URL });
// A database or cache restart drops connections: log it (they reconnect)
// instead of crashing on an unhandled error event.
db.on("error", (err) => console.error("postgres:", err.message));
cache.on("error", (err) => console.error("redis:", err.message));
await cache.connect();
await db.query(
  "CREATE TABLE IF NOT EXISTS notes (id SERIAL PRIMARY KEY, text TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT now())",
);

// HTTP server metrics named after the OpenTelemetry semantic conventions
// (http.server.request.duration in seconds), served at GET /metrics for
// Prometheus (compose labels prometheus.io/*).
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
  res.json({ service: "api", endpoints: ["GET /health", "GET /notes", "POST /notes {text}", "GET /metrics"] });
});

app.get("/health", async (_req, res) => {
  try {
    await db.query("SELECT 1");
    await cache.ping();
    res.json({ status: "ok" });
  } catch (err) {
    res.status(503).json({ status: "error", error: String(err) });
  }
});

app.get("/notes", async (_req, res) => {
  const cached = await cache.get("notes");
  if (cached) return res.json({ source: "cache", notes: JSON.parse(cached) });
  const { rows } = await db.query("SELECT id, text, created_at FROM notes ORDER BY id DESC LIMIT 50");
  await cache.set("notes", JSON.stringify(rows), { EX: 10 });
  res.json({ source: "db", notes: rows });
});

app.post("/notes", async (req, res) => {
  const text = String(req.body?.text ?? "").trim();
  if (!text) return res.status(400).json({ error: "text is required" });
  const { rows } = await db.query("INSERT INTO notes (text) VALUES ($1) RETURNING id, text, created_at", [text]);
  await cache.del("notes");
  res.status(201).json(rows[0]);
});

const port = Number(process.env.PORT ?? 3000);
app.listen(port, () => console.log(`api listening on :${port}`));
