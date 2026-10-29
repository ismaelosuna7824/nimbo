"""Composition root + OCI Functions driving adapter (Fn FDK) behind OCI API Gateway."""
import io
import json
import os
import uuid
from urllib.parse import urlparse

from fdk import response

from app.adapters.http import handle
from app.adapters.store import create_repository
from app.application import Items

ITEMS = Items(create_repository(os.environ), lambda: str(uuid.uuid4()))


def handler(ctx, data: io.BytesIO = None):
    body = data.getvalue().decode() if data else None
    status, payload = handle(ITEMS, ctx.Method() or "GET", urlparse(ctx.RequestURL() or "/").path, body or None,
                             lambda record: print(json.dumps({"callId": ctx.CallID(), **record}), flush=True))
    return response.Response(ctx, response_data=json.dumps(payload), headers={"Content-Type": "application/json"}, status_code=status)
