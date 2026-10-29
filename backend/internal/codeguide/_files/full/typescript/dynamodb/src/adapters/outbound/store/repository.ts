// Driven adapter: DynamoDB (table with string hash key "id").
import { ConditionalCheckFailedException, DynamoDBClient, GetItemCommand, PutItemCommand } from "@aws-sdk/client-dynamodb";
import { DomainError, ItemName, type Item } from "../../../domain/item.js";
import type { ItemRepository } from "../../../application/ports.js";

export async function createRepository(env: Record<string, string | undefined>): Promise<ItemRepository> {
  const table = env.TABLE_NAME;
  if (!table) throw new Error("TABLE_NAME is not set");
  const db = new DynamoDBClient({});
  return {
    async save(item: Item) {
      try {
        await db.send(new PutItemCommand({
          TableName: table,
          Item: { id: { S: item.id }, name: { S: item.name.value }, createdAt: { S: item.createdAt } },
          ConditionExpression: "attribute_not_exists(id)",
        }));
      } catch (err) {
        if (err instanceof ConditionalCheckFailedException) throw new DomainError("conflict", "item already exists");
        throw err;
      }
    },
    async get(id: string) {
      const out = await db.send(new GetItemCommand({ TableName: table, Key: { id: { S: id } }, ConsistentRead: true }));
      if (!out.Item) return undefined;
      return { id, name: ItemName.of(out.Item.name?.S), createdAt: out.Item.createdAt?.S ?? "" };
    },
  };
}
