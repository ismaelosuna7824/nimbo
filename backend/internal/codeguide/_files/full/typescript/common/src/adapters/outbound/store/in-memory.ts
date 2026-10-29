import { DomainError, type Item } from "../../../domain/item.js";
import type { ItemRepository } from "../../../application/ports.js";

// In-memory adapter for use-case tests and local runs.
export class InMemoryItemRepository implements ItemRepository {
  private readonly rows = new Map<string, Item>();

  async save(item: Item): Promise<void> {
    if (this.rows.has(item.id)) throw new DomainError("conflict", "item already exists");
    this.rows.set(item.id, item);
  }

  async get(id: string): Promise<Item | undefined> {
    return this.rows.get(id);
  }
}
