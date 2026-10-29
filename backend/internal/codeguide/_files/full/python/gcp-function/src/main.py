"""Composition root + Cloud Functions (2nd gen) driving adapter; entry point "api"."""
import json
import os
import uuid

import functions_framework

from app.adapters.http import handle
from app.adapters.store import create_repository
from app.application import Items

ITEMS = Items(create_repository(os.environ), lambda: str(uuid.uuid4()))


@functions_framework.http
def api(request):
    trace = request.headers.get("X-Cloud-Trace-Context")
    status, payload = handle(ITEMS, request.method, request.path, request.get_data(as_text=True) or None,
                             lambda record: print(json.dumps({"trace": trace, **record})))
    return (json.dumps(payload), status, {"Content-Type": "application/json"})
