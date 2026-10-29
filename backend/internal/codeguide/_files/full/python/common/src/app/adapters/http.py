"""Driving adapter: HTTP -> use cases, domain errors -> status codes."""
import json
import re

from app.application import Items
from app.domain import DomainError

MAX_BODY = 16 * 1024  # OWASP API4: bound resource consumption
ITEM_PATH = re.compile(r"/items/([\w-]{1,64})$")
STATUS = {"invalid": 400, "not_found": 404, "conflict": 409}


def handle(items: Items, method: str, path: str, body, log) -> tuple[int, dict]:
    try:
        if method == "GET" and path.endswith("/health"):
            return 200, {"status": "ok"}
        if method == "POST" and path.endswith("/items"):
            if body and len(body) > MAX_BODY:
                return 413, {"error": "body too large"}
            return 201, items.create(json.loads(body or "{}").get("name"))
        match = ITEM_PATH.search(path) if method == "GET" else None
        if match:
            return 200, items.get(match.group(1))
        return 404, {"error": "route not found"}
    except DomainError as err:
        return STATUS[err.code], {"error": str(err)}
    except (json.JSONDecodeError, AttributeError):
        return 400, {"error": "body must be a JSON object"}
    except Exception as err:  # never leak internals
        log({"level": "error", "msg": "unhandled", "error": type(err).__name__})
        return 500, {"error": "internal error"}
