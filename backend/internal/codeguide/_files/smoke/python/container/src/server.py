"""Container / VM entry: standard-library HTTP server on PORT (default 8080)."""
import json
import os
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from smoke import smoke


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        request_id = self.headers.get("X-Request-Id") or str(uuid.uuid4())  # correlation id
        status, body = smoke(self.path.split("?")[0], lambda record: print(json.dumps({"requestId": request_id, **record}), flush=True))
        out = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Request-Id", request_id)
        self.send_header("Content-Length", str(len(out)))
        self.end_headers()
        self.wfile.write(out)

    def log_message(self, *args):  # structured logs replace the default access log
        pass


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", int(os.environ.get("PORT", "8080"))), Handler).serve_forever()
