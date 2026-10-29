// Composition root + Azure Functions driving adapter (Node.js model v4).
import { randomUUID } from "node:crypto";
import { app } from "@azure/functions";
import { CreateItem, GetItem } from "../application/items.js";
import { routes } from "../adapters/inbound/http/routes.js";
import { createRepository } from "../adapters/outbound/store/repository.js";

const items = await createRepository(process.env);
const handle = routes({ createItem: new CreateItem(items, randomUUID), getItem: new GetItem(items) });

app.http("api", {
  methods: ["GET", "POST"],
  authLevel: "anonymous",
  route: "{*path}",
  handler: async (request, context) => {
    const log = (o: object) => context.log(JSON.stringify({ invocationId: context.invocationId, ...o }));
    const res = await handle(request.method, new URL(request.url).pathname, await request.text(), log);
    return { status: res.status, jsonBody: res.body };
  },
});
