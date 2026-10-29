import sys
from datetime import datetime, timezone
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "src"))

from app.adapters.memory import InMemoryItemRepository  # noqa: E402
from app.application import Items  # noqa: E402
from app.domain import DomainError, ItemName  # noqa: E402


def test_item_name_is_trimmed_and_bounded():
    assert ItemName("  pen ").value == "pen"
    for raw in ["", "   ", "x" * 101, 42]:
        with pytest.raises(DomainError):
            ItemName(raw)


def test_create_then_get():
    items = Items(InMemoryItemRepository(), lambda: "id-1", lambda: datetime(2020, 1, 1, tzinfo=timezone.utc))
    created = items.create("pen")
    assert items.get("id-1") == created
    with pytest.raises(DomainError) as err:
        items.get("nope")
    assert err.value.code == "not_found"
