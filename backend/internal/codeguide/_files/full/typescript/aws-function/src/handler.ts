// Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0).
import { randomUUID } from "node:crypto";
import { CreateItem, GetItem } from "./application/items.js";
import { routes } from "./adapters/inbound/http/routes.js";
import { createRepository } from "./adapters/outbound/store/repository.js";

const items = await createRepository(process.env);
const handle = routes({ createItem: new CreateItem(items, randomUUID), getItem: new GetItem(items) });

interface HttpEvent {
  rawPath?: string;
  body?: string;
  isBase64Encoded?: boolean;
  requestContext?: { http?: { method?: string } };
}

export const handler = async (event: HttpEvent, context: { awsRequestId: string }) => {
  const log = (o: object) => console.log(JSON.stringify({ requestId: context.awsRequestId, ...o }));
  const body = event.isBase64Encoded && event.body ? Buffer.from(event.body, "base64").toString() : event.body;
  const res = await handle(event.requestContext?.http?.method ?? "GET", event.rawPath ?? "/", body, log);
  log({ level: "info", msg: "request", path: event.rawPath, status: res.status });
  return { statusCode: res.status, headers: { "content-type": "application/json" }, body: JSON.stringify(res.body) };
};
