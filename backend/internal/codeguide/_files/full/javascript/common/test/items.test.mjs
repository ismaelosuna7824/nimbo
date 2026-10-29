import assert from "node:assert/strict";
import { test } from "node:test";
import { DomainError, ItemName } from "../src/domain/item.mjs";
import { createItem, getItem } from "../src/application/items.mjs";
import { inMemoryRepository } from "../src/adapters/outbound/store/in-memory.mjs";

test("ItemName trims and bounds the name", () => {
  assert.equal(new ItemName("  pen ").value, "pen");
  for (const raw of ["", "   ", "x".repeat(101), 42]) assert.throws(() => new ItemName(raw), DomainError);
});

test("create then get round-trips through the repository port", async () => {
  const items = inMemoryRepository();
  const created = await createItem({ items, newId: () => "id-1", clock: () => new Date(0) })({ name: "pen" });
  assert.deepEqual(await getItem({ items })("id-1"), created);
  await assert.rejects(getItem({ items })("nope"), { code: "not_found" });
});
