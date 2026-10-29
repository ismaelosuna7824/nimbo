import { DomainError } from "../../../domain/item.mjs";

// In-memory adapter for use-case tests and local runs.
export const inMemoryRepository = () => {
  const rows = new Map();
  return {
    async save(item) {
      if (rows.has(item.id)) throw new DomainError("conflict", "item already exists");
      rows.set(item.id, item);
    },
    async get(id) {
      return rows.get(id);
    },
  };
};
