// Composition root + OCI Functions driving adapter (Fn FDK) behind OCI API Gateway.
import { randomUUID } from "node:crypto";
import fdk from "@fnproject/fdk";
import { createItem, getItem } from "./application/items.mjs";
import { routes } from "./adapters/inbound/http/routes.mjs";
import { createRepository } from "./adapters/outbound/store/repository.mjs";

const items = await createRepository(process.env);
const handle = routes({ createItem: createItem({ items, newId: randomUUID }), getItem: getItem({ items }) });

fdk.handle(async (input, ctx) => {
  const gw = ctx.httpGateway;
  const log = (o) => console.log(JSON.stringify({ callId: ctx.callID, ...o }));
  const out = await handle(gw.method, new URL(gw.requestURL, "http://gateway").pathname, input, log);
  gw.statusCode = out.status;
  return out.body;
}, { inputMode: "string" });
