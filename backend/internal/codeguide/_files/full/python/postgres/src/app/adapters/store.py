"""Driven adapter: PostgreSQL via psycopg 3."""
import psycopg

from app.domain import DomainError, Item, ItemName


class PostgresItemRepository:
    def __init__(self, url: str):
        self._url = url
        with psycopg.connect(url) as conn:
            conn.execute("CREATE TABLE IF NOT EXISTS items (id text PRIMARY KEY, name text NOT NULL, created_at timestamptz NOT NULL)")

    def save(self, item: Item) -> None:
        with psycopg.connect(self._url) as conn:
            cur = conn.execute(
                "INSERT INTO items (id, name, created_at) VALUES (%s, %s, %s) ON CONFLICT (id) DO NOTHING",
                (item.id, item.name.value, item.created_at),
            )
            if cur.rowcount == 0:
                raise DomainError("conflict", "item already exists")

    def get(self, item_id: str):
        with psycopg.connect(self._url) as conn:
            row = conn.execute("SELECT name, created_at FROM items WHERE id = %s", (item_id,)).fetchone()
        return Item(item_id, ItemName(row[0]), row[1]) if row else None


def create_repository(env) -> PostgresItemRepository:
    """Builds the repository from the env the composition root passes in (use a pool under load)."""
    if not env.get("DATABASE_URL"):
        raise RuntimeError("DATABASE_URL is not set")
    return PostgresItemRepository(env["DATABASE_URL"])
