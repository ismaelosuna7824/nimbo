"""In-memory adapter for use-case tests and local runs."""
from app.domain import DomainError, Item


class InMemoryItemRepository:
    def __init__(self):
        self._rows: dict[str, Item] = {}

    def save(self, item: Item) -> None:
        if item.id in self._rows:
            raise DomainError("conflict", "item already exists")
        self._rows[item.id] = item

    def get(self, item_id: str):
        return self._rows.get(item_id)
