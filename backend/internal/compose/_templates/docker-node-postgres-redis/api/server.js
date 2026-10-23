import express from "express";
import pg from "pg";
import { createClient } from "redis";

const db = new pg.Pool({ connectionString: process.env.DATABASE_URL });
const cache = createClient({ url: process.env.REDIS_URL });
await cache.connect();
await db.query(
  "CREATE TABLE IF NOT EXISTS notes (id SERIAL PRIMARY KEY, text TEXT NOT NULL, created_at TIMESTAMPTZ DEFAULT now())",
);

const app = express();
app.use(express.json());

app.get("/", (_req, res) => {
  res.json({ service: "api", endpoints: ["GET /health", "GET /notes", "POST /notes {text}"] });
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
