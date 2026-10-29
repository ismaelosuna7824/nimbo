// Driven adapter: PostgreSQL via node-postgres.
import pg from "pg";
import { DomainError, ItemName, type Item } from "../../../domain/item.js";
import type { ItemRepository } from "../../../application/ports.js";

export async function createRepository(env: Record<string, string | undefined>): Promise<ItemRepository> {
  if (!env.DATABASE_URL) throw new Error("DATABASE_URL is not set");
  const pool = new pg.Pool({ connectionString: env.DATABASE_URL, max: 5 });
  await pool.query("CREATE TABLE IF NOT EXISTS items (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)");
  return {
    async save(item: Item) {
      const res = await pool.query(
        "INSERT INTO items (id, name, created_at) VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING",
        [item.id, item.name.value, item.createdAt],
      );
      if (res.rowCount === 0) throw new DomainError("conflict", "item already exists");
    },
    async get(id: string) {
      const res = await pool.query("SELECT name, created_at FROM items WHERE id = $1", [id]);
      const row = res.rows[0];
      return row ? { id, name: ItemName.of(row.name), createdAt: new Date(row.created_at).toISOString() } : undefined;
    },
  };
}
