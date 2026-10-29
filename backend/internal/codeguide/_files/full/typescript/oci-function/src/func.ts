// Composition root + OCI Functions driving adapter (Fn FDK) behind OCI API Gateway.
import { randomUUID } from "node:crypto";
// @ts-expect-error: the Fn FDK ships without type declarations
import fdk from "@fnproject/fdk";
import { CreateItem, GetItem } from "./application/items.js";
import { routes } from "./adapters/inbound/http/routes.js";
import { createRepository } from "./adapters/outbound/store/repository.js";

const items = await createRepository(process.env);
const handle = routes({ createItem: new CreateItem(items, randomUUID), getItem: new GetItem(items) });

fdk.handle(async (input: string, ctx: { callID: string; httpGateway: { method: string; requestURL: string; statusCode: number } }) => {
  const gw = ctx.httpGateway;
  const log = (o: object) => console.log(JSON.stringify({ callId: ctx.callID, ...o }));
  const out = await handle(gw.method, new URL(gw.requestURL, "http://gateway").pathname, input, log);
  gw.statusCode = out.status;
  return out.body;
}, { inputMode: "string" });
