// Composition root + HTTP server driving adapter (containers).
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { createItem, getItem } from "./application/items.mjs";
import { routes } from "./adapters/inbound/http/routes.mjs";
import { createRepository } from "./adapters/outbound/store/repository.mjs";

const items = await createRepository(process.env);
const handle = routes({ createItem: createItem({ items, newId: randomUUID }), getItem: getItem({ items }) });
const port = Number(process.env.PORT ?? 8080);

createServer(async (req, res) => {
  const requestId = String(req.headers["x-request-id"] ?? randomUUID()); // correlation id
  const log = (o) => console.log(JSON.stringify({ requestId, ...o }));
  const chunks = [];
  for await (const chunk of req) chunks.push(chunk);
  const out = await handle(req.method ?? "GET", new URL(req.url ?? "/", "http://x").pathname, Buffer.concat(chunks).toString(), log);
  log({ level: "info", msg: "request", path: req.url, status: out.status });
  res.writeHead(out.status, { "content-type": "application/json", "x-request-id": requestId }).end(JSON.stringify(out.body));
}).listen(port, () => console.log(JSON.stringify({ level: "info", msg: "listening", port })));
