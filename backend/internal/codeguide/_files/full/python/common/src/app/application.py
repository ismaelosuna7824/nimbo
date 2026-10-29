"""Use cases and the ports they own."""
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Callable, Optional, Protocol

from app.domain import DomainError, Item, ItemName


class ItemRepository(Protocol):
    """Driven port. save raises DomainError("conflict") when the id exists (idempotent create)."""

    def save(self, item: Item) -> None: ...

    def get(self, item_id: str) -> Optional[Item]: ...


def to_view(item: Item) -> dict:
    """Plain data crossing the boundary: adapters never see domain objects."""
    return {"id": item.id, "name": item.name.value, "createdAt": item.created_at.isoformat()}


@dataclass
class Items:
    repo: ItemRepository
    new_id: Callable[[], str]
    clock: Callable[[], datetime] = lambda: datetime.now(timezone.utc)

    def create(self, name) -> dict:
        item = Item(self.new_id(), ItemName(name), self.clock())
        self.repo.save(item)
        return to_view(item)

    def get(self, item_id: str) -> dict:
        item = self.repo.get(item_id)
        if item is None:
            raise DomainError("not_found", "item not found")
        return to_view(item)
