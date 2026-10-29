import type { Item } from "../domain/item.js";

// Driven port, owned by the application; adapters implement it.
export interface ItemRepository {
  // save fails with DomainError("conflict") when the id already exists (idempotent create).
  save(item: Item): Promise<void>;
  get(id: string): Promise<Item | undefined>;
}
