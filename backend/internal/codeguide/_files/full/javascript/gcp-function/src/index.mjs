// Composition root + Cloud Functions (2nd gen) driving adapter; entry point "api".
import { randomUUID } from "node:crypto";
import { http } from "@google-cloud/functions-framework";
import { createItem, getItem } from "./application/items.mjs";
import { routes } from "./adapters/inbound/http/routes.mjs";
import { createRepository } from "./adapters/outbound/store/repository.mjs";

const items = await createRepository(process.env);
const handle = routes({ createItem: createItem({ items, newId: randomUUID }), getItem: getItem({ items }) });

http("api", async (req, res) => {
  const log = (o) => console.log(JSON.stringify({ trace: req.get("x-cloud-trace-context"), ...o }));
  const out = await handle(req.method, req.path, req.rawBody?.toString(), log);
  res.status(out.status).json(out.body);
});
