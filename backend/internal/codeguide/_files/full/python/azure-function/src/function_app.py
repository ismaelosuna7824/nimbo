"""Composition root + Azure Functions driving adapter (Python model v2)."""
import json
import os
import uuid
from urllib.parse import urlparse

import azure.functions as func

from app.adapters.http import handle
from app.adapters.store import create_repository
from app.application import Items

ITEMS = Items(create_repository(os.environ), lambda: str(uuid.uuid4()))
app = func.FunctionApp(http_auth_level=func.AuthLevel.ANONYMOUS)


@app.route(route="{*path}", methods=["GET", "POST"])
def api(req: func.HttpRequest, context: func.Context) -> func.HttpResponse:
    def log(record):
        print(json.dumps({"invocationId": context.invocation_id, **record}))

    status, payload = handle(ITEMS, req.method, urlparse(req.url).path, req.get_body().decode() or None, log)
    return func.HttpResponse(json.dumps(payload), status_code=status, mimetype="application/json")
