"""Cloud Functions (2nd gen) entry; entry point "smoke"."""
import json

import functions_framework

from smoke import smoke as run_smoke


@functions_framework.http
def smoke(request):
    trace = request.headers.get("X-Cloud-Trace-Context")
    status, body = run_smoke(request.path, lambda record: print(json.dumps({"trace": trace, **record})))
    return (json.dumps(body), status, {"Content-Type": "application/json"})
