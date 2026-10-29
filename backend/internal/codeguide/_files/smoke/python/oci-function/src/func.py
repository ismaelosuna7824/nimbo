"""OCI Functions entry (Fn FDK) behind OCI API Gateway."""
import io
import json
from urllib.parse import urlparse

from fdk import response

from smoke import smoke


def handler(ctx, data: io.BytesIO = None):
    status, body = smoke(urlparse(ctx.RequestURL() or "/").path, lambda record: print(json.dumps({"callId": ctx.CallID(), **record}), flush=True))
    return response.Response(ctx, response_data=json.dumps(body), headers={"Content-Type": "application/json"}, status_code=status)
