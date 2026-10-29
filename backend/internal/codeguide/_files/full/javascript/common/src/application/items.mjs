// Use cases. The repository port: { save(item), get(id) }; save rejects with
// DomainError("conflict") when the id exists (idempotent create).
import { DomainError, ItemName, newItem } from "../domain/item.mjs";

// Plain data crossing the boundary: adapters never see domain objects.
const toView = (item) => ({ id: item.id, name: item.name.value, createdAt: item.createdAt });

export const createItem = ({ items, newId, clock = () => new Date() }) => async ({ name }) => {
  const item = newItem(newId(), new ItemName(name), clock());
  await items.save(item);
  return toView(item);
};

export const getItem = ({ items }) => async (id) => {
  const item = await items.get(id);
  if (!item) throw new DomainError("not_found", "item not found");
  return toView(item);
};
