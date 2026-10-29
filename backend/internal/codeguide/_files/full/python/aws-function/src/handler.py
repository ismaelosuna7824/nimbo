"""Composition root + Lambda driving adapter (API Gateway HTTP API, payload 2.0).

boto3 ships with the Python Lambda runtimes, so src/ deploys as-is (no build).
"""
import base64
import json
import os
import uuid

from app.adapters.http import handle
from app.adapters.store import create_repository
from app.application import Items

# Built once per execution environment (init phase), reused across invocations.
ITEMS = Items(create_repository(os.environ), lambda: str(uuid.uuid4()))


def handler(event, context):
    def log(record):
        print(json.dumps({"requestId": context.aws_request_id, **record}))

    body = event.get("body")
    if body and event.get("isBase64Encoded"):
        body = base64.b64decode(body).decode()
    method = event.get("requestContext", {}).get("http", {}).get("method", "GET")
    status, payload = handle(ITEMS, method, event.get("rawPath", "/"), body, log)
    log({"level": "info", "msg": "request", "path": event.get("rawPath"), "status": status})
    return {"statusCode": status, "headers": {"content-type": "application/json"}, "body": json.dumps(payload)}
