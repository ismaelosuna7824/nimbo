// Composition root + Azure Functions driving adapter (Node.js model v4).
import { randomUUID } from "node:crypto";
import { app } from "@azure/functions";
import { createItem, getItem } from "../application/items.mjs";
import { routes } from "../adapters/inbound/http/routes.mjs";
import { createRepository } from "../adapters/outbound/store/repository.mjs";

const items = await createRepository(process.env);
const handle = routes({ createItem: createItem({ items, newId: randomUUID }), getItem: getItem({ items }) });

app.http("api", {
  methods: ["GET", "POST"],
  authLevel: "anonymous",
  route: "{*path}",
  handler: async (request, context) => {
    const log = (o) => context.log(JSON.stringify({ invocationId: context.invocationId, ...o }));
    const res = await handle(request.method, new URL(request.url).pathname, await request.text(), log);
    return { status: res.status, jsonBody: res.body };
  },
});
