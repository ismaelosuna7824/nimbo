import express from "express";
import pg from "pg";

// DATABASE_URL comes from the web-db Secret (see k8s/web.yaml).
const db = new pg.Pool({ connectionString: process.env.DATABASE_URL });
await db.query(
  "CREATE TABLE IF NOT EXISTS notes (id SERIAL PRIMARY KEY, text TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT now())",
);

const app = express();
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
