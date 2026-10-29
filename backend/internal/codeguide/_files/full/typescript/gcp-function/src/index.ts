// Composition root + Cloud Functions (2nd gen) driving adapter; entry point "api".
import { randomUUID } from "node:crypto";
import { http } from "@google-cloud/functions-framework";
import { CreateItem, GetItem } from "./application/items.js";
import { routes } from "./adapters/inbound/http/routes.js";
import { createRepository } from "./adapters/outbound/store/repository.js";

const items = await createRepository(process.env);
const handle = routes({ createItem: new CreateItem(items, randomUUID), getItem: new GetItem(items) });

http("api", async (req, res) => {
  const log = (o: object) => console.log(JSON.stringify({ trace: req.get("x-cloud-trace-context"), ...o }));
  const out = await handle(req.method, req.path, req.rawBody?.toString(), log);
  res.status(out.status).json(out.body);
});
