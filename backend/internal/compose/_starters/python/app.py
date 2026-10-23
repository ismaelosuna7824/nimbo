import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit

from prometheus_client import CONTENT_TYPE_LATEST, Histogram, generate_latest

PORT = int(os.environ.get("PORT", "8080"))

# HTTP server metrics named after the OpenTelemetry semantic conventions
# (http.server.request.duration in seconds), as Prometheus scrapes them at
# GET /metrics: Add monitoring's 5xx and p95 alerts read this histogram.
REQUEST_DURATION = Histogram(
    "http_server_request_duration_seconds",
    "Duration of HTTP server requests.",
    ["http_request_method", "http_route", "http_response_status_code"],
    buckets=(0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10),
)


def hello(_handler):
    return 200, {"message": "Hello from Python"}


# Route templates (low-cardinality http_route values) and their handlers.
ROUTES = {"/": hello}


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        path = urlsplit(self.path).path
        if path == "/metrics":
            # Not measured itself: scrapes would skew the request metrics.
            self.reply(200, generate_latest(), CONTENT_TYPE_LATEST)
            return
        start = time.perf_counter()
        route = ROUTES.get(path)
        status, payload = route(self) if route else (404, {"error": "not found"})
        self.reply(status, json.dumps(payload).encode(), "application/json")
        REQUEST_DURATION.labels("GET", path if route else "", str(status)).observe(time.perf_counter() - start)

    def reply(self, status, body, content_type):
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    print(f"listening on :{PORT}", flush=True)
    ThreadingHTTPServer(("0.0.0.0", PORT), Handler).serve_forever()
