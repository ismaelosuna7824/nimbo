"""Azure Functions entry (Python programming model v2): /api/health, /api/smoke."""
import json
from urllib.parse import urlparse

import azure.functions as func

from smoke import smoke

app = func.FunctionApp(http_auth_level=func.AuthLevel.ANONYMOUS)


@app.route(route="{*path}", methods=["GET"])
def handle(req: func.HttpRequest, context: func.Context) -> func.HttpResponse:
    def log(record):
        print(json.dumps({"invocationId": context.invocation_id, **record}))

    status, body = smoke(urlparse(req.url).path, log)
    return func.HttpResponse(json.dumps(body), status_code=status, mimetype="application/json")
