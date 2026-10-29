import { DomainError, ItemName, newItem, type Item } from "../domain/item.js";
import type { ItemRepository } from "./ports.js";

// Plain data crossing the boundary: adapters never see domain objects.
export interface ItemView {
  id: string;
  name: string;
  createdAt: string;
}

const toView = (item: Item): ItemView => ({ id: item.id, name: item.name.value, createdAt: item.createdAt });

export class CreateItem {
  constructor(
    private readonly items: ItemRepository,
    private readonly newId: () => string,
    private readonly clock: () => Date = () => new Date(),
  ) {}

  async execute(cmd: { name: unknown }): Promise<ItemView> {
    const item = newItem(this.newId(), ItemName.of(cmd.name), this.clock());
    await this.items.save(item);
    return toView(item);
  }
}

export class GetItem {
  constructor(private readonly items: ItemRepository) {}

  async execute(id: string): Promise<ItemView> {
    const item = await this.items.get(id);
    if (!item) throw new DomainError("not_found", "item not found");
    return toView(item);
  }
}
