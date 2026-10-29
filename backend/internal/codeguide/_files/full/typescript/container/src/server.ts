// Composition root + HTTP server driving adapter (containers).
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { CreateItem, GetItem } from "./application/items.js";
import { routes } from "./adapters/inbound/http/routes.js";
import { createRepository } from "./adapters/outbound/store/repository.js";

const items = await createRepository(process.env);
const handle = routes({ createItem: new CreateItem(items, randomUUID), getItem: new GetItem(items) });
const port = Number(process.env.PORT ?? 8080);

createServer(async (req, res) => {
  const requestId = String(req.headers["x-request-id"] ?? randomUUID()); // correlation id
  const log = (o: object) => console.log(JSON.stringify({ requestId, ...o }));
  const chunks: Buffer[] = [];
  for await (const chunk of req) chunks.push(chunk as Buffer);
  const out = await handle(req.method ?? "GET", new URL(req.url ?? "/", "http://x").pathname, Buffer.concat(chunks).toString(), log);
  log({ level: "info", msg: "request", path: req.url, status: out.status });
  res.writeHead(out.status, { "content-type": "application/json", "x-request-id": requestId }).end(JSON.stringify(out.body));
}).listen(port, () => console.log(JSON.stringify({ level: "info", msg: "listening", port })));
