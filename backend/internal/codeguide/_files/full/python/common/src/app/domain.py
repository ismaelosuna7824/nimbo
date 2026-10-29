"""Domain: pure Python, no SDK, HTTP or database imports."""
from dataclasses import dataclass
from datetime import datetime


class DomainError(Exception):
    """Typed domain error; code is "invalid", "not_found" or "conflict"."""

    def __init__(self, code: str, message: str):
        super().__init__(message)
        self.code = code


@dataclass(frozen=True)
class ItemName:
    """Value object: valid once constructed."""

    value: str

    def __post_init__(self):
        if not isinstance(self.value, str):
            raise DomainError("invalid", "name must be a string")
        trimmed = self.value.strip()
        if not 0 < len(trimmed) <= 100:
            raise DomainError("invalid", "name must be 1-100 characters")
        object.__setattr__(self, "value", trimmed)


@dataclass(frozen=True)
class Item:
    id: str
    name: ItemName
    created_at: datetime
