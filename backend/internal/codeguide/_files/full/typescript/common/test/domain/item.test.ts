import { describe, expect, it } from "vitest";
import { DomainError, ItemName } from "../../src/domain/item.js";
import { CreateItem, GetItem } from "../../src/application/items.js";
import { InMemoryItemRepository } from "../../src/adapters/outbound/store/in-memory.js";

describe("ItemName", () => {
  it("trims a valid name", () => expect(ItemName.of("  pen ").value).toBe("pen"));
  it.each(["", "   ", "x".repeat(101), 42])("rejects %j", (raw) => {
    expect(() => ItemName.of(raw)).toThrow(DomainError);
  });
});

describe("CreateItem + GetItem", () => {
  it("round-trips through the repository port", async () => {
    const repo = new InMemoryItemRepository();
    const created = await new CreateItem(repo, () => "id-1", () => new Date(0)).execute({ name: "pen" });
    expect(await new GetItem(repo).execute("id-1")).toEqual(created);
  });
  it("reports a missing item as not_found", async () => {
    await expect(new GetItem(new InMemoryItemRepository()).execute("nope")).rejects.toMatchObject({ code: "not_found" });
  });
});
