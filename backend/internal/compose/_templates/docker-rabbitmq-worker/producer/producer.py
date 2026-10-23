"""HTTP API that publishes jobs to RabbitMQ (standard library + pika)."""

import json
import os
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pika

AMQP_URL = os.environ["AMQP_URL"]
QUEUE = os.environ.get("QUEUE", "jobs")
lock = threading.Lock()
connection = None
channel = None


def get_channel():
    global connection, channel
    if connection is None or connection.is_closed:
        connection = pika.BlockingConnection(pika.URLParameters(AMQP_URL))
        channel = connection.channel()
        channel.queue_declare(queue=QUEUE, durable=True)
    return channel


class Handler(BaseHTTPRequestHandler):
    def reply(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/health":
            try:
                with lock:
                    get_channel()
                self.reply(200, {"status": "ok"})
            except pika.exceptions.AMQPError as err:
                self.reply(503, {"status": "error", "error": str(err)})
            return
        self.reply(200, {"service": "producer", "endpoints": ["GET /health", "POST /jobs {task}"]})

    def do_POST(self):
        if self.path != "/jobs":
            self.reply(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length") or 0)
        try:
            payload = json.loads(self.rfile.read(length) or b"{}")
        except json.JSONDecodeError:
            self.reply(400, {"error": "body must be JSON"})
            return
        job = {"id": str(uuid.uuid4()), "task": payload.get("task", "hello")}
        with lock:
            get_channel().basic_publish(
                exchange="",
                routing_key=QUEUE,
                body=json.dumps(job),
                properties=pika.BasicProperties(delivery_mode=2),
            )
        self.reply(202, job)

    def log_message(self, fmt, *args):
        print("producer:", fmt % args)


if __name__ == "__main__":
    print(f"producer listening on :8000, publishing to {QUEUE}")
    ThreadingHTTPServer(("0.0.0.0", 8000), Handler).serve_forever()
