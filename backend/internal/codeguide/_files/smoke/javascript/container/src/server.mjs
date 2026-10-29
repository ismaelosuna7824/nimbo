// Container / VM entry: plain HTTP server on PORT (default 8080).
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { smoke } from "./smoke.mjs";

const port = Number(process.env.PORT ?? 8080);
createServer(async (req, res) => {
  const requestId = String(req.headers["x-request-id"] ?? randomUUID()); // correlation id
  const out = await smoke(new URL(req.url ?? "/", "http://x").pathname, (o) => console.log(JSON.stringify({ requestId, ...o })));
  res.writeHead(out.status, { "content-type": "application/json", "x-request-id": requestId }).end(JSON.stringify(out.body));
}).listen(port, () => console.log(JSON.stringify({ level: "info", msg: "listening", port })));
