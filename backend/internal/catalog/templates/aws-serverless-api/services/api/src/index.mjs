import { randomUUID } from "node:crypto";
import { DynamoDBClient, GetItemCommand, PutItemCommand, ScanCommand } from "@aws-sdk/client-dynamodb";

const db = new DynamoDBClient({});
const table = process.env.TABLE_NAME;

const reply = (statusCode, body) => ({
  statusCode,
  headers: { "content-type": "application/json" },
  body: JSON.stringify(body),
});

const toItem = (record) => ({ id: record.id.S, ...JSON.parse(record.data?.S ?? "{}") });

export const handler = async (event) => {
  const method = event.requestContext?.http?.method ?? "GET";
  const path = event.rawPath ?? "/";
  const id = path.match(/^\/items\/([^/]+)$/)?.[1];

  if (path === "/items" && method === "POST") {
    const data = event.body ? JSON.parse(event.isBase64Encoded ? Buffer.from(event.body, "base64").toString() : event.body) : {};
    const item = { id: randomUUID(), ...data };
    await db.send(new PutItemCommand({
      TableName: table,
      Item: { id: { S: item.id }, data: { S: JSON.stringify(data) } },
    }));
    return reply(201, item);
  }
  if (path === "/items" && method === "GET") {
    const out = await db.send(new ScanCommand({ TableName: table, Limit: 100 }));
    return reply(200, (out.Items ?? []).map(toItem));
  }
  if (id && method === "GET") {
    const out = await db.send(new GetItemCommand({ TableName: table, Key: { id: { S: id } } }));
    return out.Item ? reply(200, toItem(out.Item)) : reply(404, { message: "not found" });
  }
  return reply(200, { message: "Items API", routes: ["GET /items", "POST /items", "GET /items/{id}"] });
};
