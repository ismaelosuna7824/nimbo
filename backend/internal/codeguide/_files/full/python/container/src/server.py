"""Composition root + HTTP server driving adapter (containers), standard library only."""
import json
import os
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from app.adapters.http import MAX_BODY, handle
from app.adapters.store import create_repository
from app.application import Items

ITEMS = Items(create_repository(os.environ), lambda: str(uuid.uuid4()))


class Handler(BaseHTTPRequestHandler):
    def _serve(self):
        request_id = self.headers.get("X-Request-Id") or str(uuid.uuid4())  # correlation id
        length = min(int(self.headers.get("Content-Length") or 0), MAX_BODY + 1)
        body = self.rfile.read(length).decode() if length else None

        def log(record):
            print(json.dumps({"requestId": request_id, **record}), flush=True)

        status, payload = handle(ITEMS, self.command, self.path.split("?")[0], body, log)
        out = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-Id", request_id)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)
        log({"level": "info", "msg": "request", "path": self.path, "status": status})

    do_GET = do_POST = _serve

    def log_message(self, *args):  # structured logs above replace the default access log
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8080"))), Handler).serve_forever()
