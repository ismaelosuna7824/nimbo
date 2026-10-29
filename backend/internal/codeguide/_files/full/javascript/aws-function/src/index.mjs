// Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0).
// The nodejs22.x runtime ships the AWS SDK v3, so src/ deploys as-is (no build).
import { randomUUID } from "node:crypto";
import { createItem, getItem } from "./application/items.mjs";
import { routes } from "./adapters/inbound/http/routes.mjs";
import { createRepository } from "./adapters/outbound/store/repository.mjs";

const items = await createRepository(process.env);
const handle = routes({ createItem: createItem({ items, newId: randomUUID }), getItem: getItem({ items }) });

export const handler = async (event, context) => {
  const log = (o) => console.log(JSON.stringify({ requestId: context.awsRequestId, ...o }));
  const body = event.isBase64Encoded && event.body ? Buffer.from(event.body, "base64").toString() : event.body;
  const res = await handle(event.requestContext?.http?.method ?? "GET", event.rawPath ?? "/", body, log);
  log({ level: "info", msg: "request", path: event.rawPath, status: res.status });
  return { statusCode: res.status, headers: { "content-type": "application/json" }, body: JSON.stringify(res.body) };
};
